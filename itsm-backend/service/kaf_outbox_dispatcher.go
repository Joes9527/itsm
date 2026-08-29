package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// KafDelegateRequested is the signed event sent when ITSM delegates a task to KAF.
type KafDelegateRequested struct {
	EventType     string `json:"event_type"`
	EventID       string `json:"eventId"`
	TenantID      int    `json:"tenantId"`
	WorkItemID    string `json:"workItemId"`
	TicketID      string `json:"ticketId"`
	TaskID        string `json:"taskId"`
	RecordClass   string `json:"recordClass"`
	Timestamp     string `json:"timestamp"`
	Version       int    `json:"version"`
	CorrelationID string `json:"correlationId"`
}

// SignKafDelegateRequest serializes the canonical payload and signs those exact bytes.
func SignKafDelegateRequest(event KafDelegateRequested, secret string) ([]byte, string, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return body, "sha256=" + hex.EncodeToString(mac.Sum(nil)), nil
}
