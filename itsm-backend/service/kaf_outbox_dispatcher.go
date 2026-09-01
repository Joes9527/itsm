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

const kafDelegateRequestedEventType = "kaf_delegate_requested"

// KafOutboxConfig contains the runtime settings needed to deliver delegated
// task events. The application creates a dispatcher only when WebhookURL is set.
type KafOutboxConfig struct {
	WebhookURL    string
	WebhookSecret string
	BatchSize     int
	PollInterval  time.Duration
}

// KafOutboxDispatcher delivers tenant-owned KAF delegation events after their
// domain transaction has committed.
type KafOutboxDispatcher struct {
	repository *OutboxEventRepository
	config     KafOutboxConfig
	httpClient *http.Client
	now        func() time.Time
}

func NewKafOutboxDispatcher(repository *OutboxEventRepository, config KafOutboxConfig) (*KafOutboxDispatcher, error) {
	config.WebhookURL = strings.TrimSpace(config.WebhookURL)
	config.WebhookSecret = strings.TrimSpace(config.WebhookSecret)
	if config.WebhookURL != "" && config.WebhookSecret == "" {
		return nil, fmt.Errorf("KAF_WEBHOOK_SECRET is required when KAF_WEBHOOK_URL is configured")
	}
	return &KafOutboxDispatcher{
		repository: repository,
		config:     config,
		httpClient: &http.Client{Timeout: kafWebhookRequestTimeout},
		now:        time.Now,
	}, nil
}

// DispatchOnce claims due events and finalizes a delivery only with the lease
// token returned by the repository claim operation.
func (d *KafOutboxDispatcher) DispatchOnce(ctx context.Context) error {
	events, err := d.repository.ClaimDueByEventType(ctx, d.now().UTC(), d.config.BatchSize, kafDelegateRequestedEventType)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.config.WebhookURL, bytes.NewReader(event.Payload))
	if err != nil {
		return fmt.Errorf("create KAF webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-ID", event.EventID)
	req.Header.Set("X-Webhook-Signature", signKafOutboxPayload(event.Payload, d.config.WebhookSecret))

	response, err := d.httpClient.Do(req)
	if err != nil {
		return d.scheduleRetry(ctx, event, fmt.Sprintf("deliver KAF webhook: %v", err))
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, kafWebhookErrorBodyLimit+1))
		if readErr != nil {
			return d.scheduleRetry(ctx, event, fmt.Sprintf("read KAF webhook HTTP %d response: %v", response.StatusCode, readErr))
		}
		if len(responseBody) > kafWebhookErrorBodyLimit {
			responseBody = responseBody[:kafWebhookErrorBodyLimit]
		}
		deliveryError := fmt.Sprintf("KAF webhook returned HTTP %d", response.StatusCode)
		if detail := strings.TrimSpace(string(responseBody)); detail != "" {
			deliveryError += ": " + detail
		}
		if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError {
			return d.scheduleClientErrorRetry(ctx, event, deliveryError, response.StatusCode)
		}
		return d.scheduleRetry(ctx, event, deliveryError)
	}
	if err := d.repository.MarkPublished(ctx, event.ID, event.ClaimToken, d.now().UTC()); err != nil {
		return fmt.Errorf("mark KAF webhook event published: %w", err)
	}
	return nil
}

func (d *KafOutboxDispatcher) scheduleRetry(ctx context.Context, event *ent.OutboxEvent, deliveryError string) error {
	nextAttemptAt := d.now().UTC().Add(outboxRetryDelay(event.AttemptCount + 1))
	if err := d.repository.MarkRetry(ctx, event.ID, event.ClaimToken, deliveryError, nextAttemptAt); err != nil {
		return fmt.Errorf("schedule KAF webhook retry: %w", err)
	}
	return nil
}

func (d *KafOutboxDispatcher) scheduleClientErrorRetry(ctx context.Context, event *ent.OutboxEvent, deliveryError string, statusCode int) error {
	nextAttemptAt := d.now().UTC().Add(outboxRetryDelay(event.AttemptCount + 1))
	if err := d.repository.MarkRetryWithAudit(ctx, event.ID, event.ClaimToken, deliveryError, nextAttemptAt, OutboxRetryAudit{
		TenantID:   event.TenantID,
		RequestID:  event.EventID,
		Resource:   "outbox_event",
		Action:     "kaf_outbox.delivery_rejected",
		Path:       "kaf/webhook",
		Method:     http.MethodPost,
		StatusCode: statusCode,
	}); err != nil {
		return fmt.Errorf("schedule KAF webhook client-error retry: %w", err)
	}
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
