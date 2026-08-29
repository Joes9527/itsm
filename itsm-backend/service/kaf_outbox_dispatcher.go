package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
