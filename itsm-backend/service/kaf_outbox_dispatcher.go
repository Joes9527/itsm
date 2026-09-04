package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"itsm-backend/ent"

	"github.com/google/uuid"
)

const kafWebhookRequestTimeout = 10 * time.Second

const kafWebhookErrorBodyLimit = 1024

const KafDelegateRequestedEventType = "kaf_delegate_requested"

// KafOutboxConfig contains the runtime settings needed to deliver delegated
// task events. It is constructed only by the dedicated KAF worker.
type KafOutboxConfig struct {
	WebhookURL    string
	WebhookSecret string
	BatchSize     int
	PollInterval  time.Duration
	MaxAttempts   int
}

// KafOutboxDispatcher delivers tenant-owned KAF delegation events after their
// domain transaction has committed.
type KafOutboxDispatcher struct {
	repository *OutboxEventRepository
	config     KafOutboxConfig
	httpClient *http.Client
	now        func() time.Time
	metrics    *KafOutboxMetrics
}

func NewKafOutboxDispatcher(repository *OutboxEventRepository, config KafOutboxConfig, metrics ...*KafOutboxMetrics) (*KafOutboxDispatcher, error) {
	config.WebhookURL = strings.TrimSpace(config.WebhookURL)
	config.WebhookSecret = strings.TrimSpace(config.WebhookSecret)
	if config.WebhookURL == "" {
		return nil, fmt.Errorf("KAF_WEBHOOK_URL is required for the KAF worker")
	}
	if config.WebhookSecret == "" {
		return nil, fmt.Errorf("KAF_WEBHOOK_SECRET is required for the KAF worker")
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 5
	}
	if config.BatchSize <= 0 || config.PollInterval <= 0 || config.MaxAttempts <= 0 {
		return nil, fmt.Errorf("KAF worker outbox settings must be positive")
	}
	dispatcher := &KafOutboxDispatcher{
		repository: repository,
		config:     config,
		httpClient: &http.Client{Timeout: kafWebhookRequestTimeout},
		now:        time.Now,
	}
	if len(metrics) > 0 {
		dispatcher.metrics = metrics[0]
	}
	return dispatcher, nil
}

// DispatchOnce claims due events and finalizes a delivery only with the lease
// token returned by the repository claim operation.
func (d *KafOutboxDispatcher) DispatchOnce(ctx context.Context) error {
	events, err := d.repository.ClaimDueByEventType(ctx, d.now().UTC(), d.config.BatchSize, KafDelegateRequestedEventType)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := d.dispatchEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// Run dispatches immediately and then at the configured interval until the
// owning application context is cancelled.
func (d *KafOutboxDispatcher) Run(ctx context.Context) {
	_ = d.DispatchOnce(ctx)
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = d.DispatchOnce(ctx)
		}
	}
}

func (d *KafOutboxDispatcher) dispatchEvent(ctx context.Context, event *ent.OutboxEvent) error {
	if err := d.repository.MarkDeliveryAttemptStarted(ctx, event.ID, event.ClaimToken, event.EventID); err != nil {
		return fmt.Errorf("mark KAF webhook delivery attempt: %w", err)
	}
	d.metrics.RecordAttempt()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.config.WebhookURL, bytes.NewReader(event.Payload))
	if err != nil {
		if markErr := d.repository.MarkBlocked(ctx, event.ID, event.ClaimToken, fmt.Sprintf("create KAF webhook request: %v", err)); markErr != nil {
			return fmt.Errorf("block invalid KAF webhook request: %w", markErr)
		}
		d.metrics.RecordTransition("blocked", "local_contract")
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-ID", event.EventID)
	req.Header.Set("X-Webhook-Signature", signKafOutboxPayload(event.Payload, d.config.WebhookSecret))

	response, err := d.httpClient.Do(req)
	if err != nil {
		if markErr := d.repository.MarkDeliveryUnknown(ctx, event, event.ClaimToken, fmt.Sprintf("deliver KAF webhook: %v", err)); markErr != nil {
			return fmt.Errorf("mark KAF webhook delivery unknown: %w", markErr)
		}
		d.metrics.RecordTransition("delivery_unknown", "transport")
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, kafWebhookErrorBodyLimit+1))
		if readErr != nil {
			if markErr := d.repository.MarkDeliveryUnknown(ctx, event, event.ClaimToken, fmt.Sprintf("read KAF webhook HTTP %d response: %v", response.StatusCode, readErr)); markErr != nil {
				return fmt.Errorf("mark unreadable KAF webhook response unknown: %w", markErr)
			}
			d.metrics.RecordTransition("delivery_unknown", "response_read")
			return nil
		}
		if len(responseBody) > kafWebhookErrorBodyLimit {
			responseBody = responseBody[:kafWebhookErrorBodyLimit]
		}
		deliveryError := fmt.Sprintf("KAF webhook returned HTTP %d", response.StatusCode)
		if detail := strings.TrimSpace(string(responseBody)); detail != "" {
			deliveryError += ": " + detail
		}
		if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError && response.StatusCode != http.StatusTooManyRequests {
			if err := d.repository.MarkBlocked(ctx, event.ID, event.ClaimToken, deliveryError); err != nil {
				return fmt.Errorf("block KAF webhook rejection: %w", err)
			}
			d.metrics.RecordTransition("blocked", "permanent_http")
			return nil
		}
		return d.scheduleRetry(ctx, event, deliveryError)
	}
	if err := d.repository.MarkPublished(ctx, event.ID, event.ClaimToken, d.now().UTC()); err != nil {
		return fmt.Errorf("mark KAF webhook event published: %w", err)
	}
	d.metrics.RecordTransition("published", "")
	return nil
}

func (d *KafOutboxDispatcher) scheduleRetry(ctx context.Context, event *ent.OutboxEvent, deliveryError string) error {
	if event.AttemptCount+1 >= d.config.MaxAttempts {
		if err := d.repository.MarkDeadLetter(ctx, event.ID, event.ClaimToken, deliveryError); err != nil {
			return fmt.Errorf("dead-letter KAF webhook event: %w", err)
		}
		d.metrics.RecordTransition("dead_letter", "retry_exhausted")
		return nil
	}
	nextAttemptAt := d.now().UTC().Add(outboxRetryDelay(event.AttemptCount + 1))
	if err := d.repository.MarkRetry(ctx, event.ID, event.ClaimToken, deliveryError, nextAttemptAt); err != nil {
		return fmt.Errorf("schedule KAF webhook retry: %w", err)
	}
	d.metrics.RecordTransition("retry", "retryable_http")
	return nil
}

func signKafOutboxPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// KafDelegateActor identifies the ITSM BPMN system subject that created a delegation.
type KafDelegateActor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName"`
}

// KafDelegateRequested is the signed event sent when ITSM delegates a task to KAF.
type KafDelegateRequested struct {
	EventType     string           `json:"event_type"`
	EventID       string           `json:"eventId"`
	TenantID      string           `json:"tenantId"`
	WorkItemID    string           `json:"workItemId"`
	TicketID      string           `json:"ticketId"`
	TaskID        string           `json:"taskId"`
	RecordClass   string           `json:"recordClass"`
	Actor         KafDelegateActor `json:"actor"`
	Timestamp     string           `json:"timestamp"`
	Version       int              `json:"version"`
	CorrelationID string           `json:"correlationId"`
}

// SignKafDelegateRequest serializes the canonical payload and signs those exact bytes.
func SignKafDelegateRequest(event KafDelegateRequested, secret string) ([]byte, string, error) {
	if err := validateKafDelegateRequested(event); err != nil {
		return nil, "", err
	}

	body, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return body, "sha256=" + hex.EncodeToString(mac.Sum(nil)), nil
}

func validateKafDelegateRequested(event KafDelegateRequested) error {
	if event.EventType != "kaf_delegate_requested" {
		return fmt.Errorf("event_type must be kaf_delegate_requested")
	}
	if _, err := uuid.Parse(event.EventID); err != nil {
		return fmt.Errorf("eventId must be a UUID: %w", err)
	}
	for field, value := range map[string]string{
		"tenantId":          event.TenantID,
		"workItemId":        event.WorkItemID,
		"ticketId":          event.TicketID,
		"taskId":            event.TaskID,
		"correlationId":     event.CorrelationID,
		"actor.id":          event.Actor.ID,
		"actor.displayName": event.Actor.DisplayName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", field)
		}
	}
	if event.Actor.Kind != "system" {
		return fmt.Errorf("actor.kind must be system")
	}
	if event.RecordClass != "service_request_item" && event.RecordClass != "incident" {
		return fmt.Errorf("recordClass must be service_request_item or incident")
	}
	if _, err := time.Parse(time.RFC3339, event.Timestamp); err != nil {
		return fmt.Errorf("timestamp must be RFC3339: %w", err)
	}
	if event.Version <= 0 {
		return fmt.Errorf("version must be greater than zero")
	}
	return nil
}
