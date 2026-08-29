package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func expectedKafDelegateHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSignKafDelegateRequest_ProducesStableHMACAndMinimalPayload(t *testing.T) {
	event := KafDelegateRequested{
		EventType:     "kaf_delegate_requested",
		EventID:       "8c4d0fb8-7895-4b2a-969a-1f38829a3c83",
		TenantID:      7,
		WorkItemID:    "42",
		TicketID:      "42",
		TaskID:        "TASK-42",
		RecordClass:   "service_request_item",
		Timestamp:     "2026-08-29T12:00:00Z",
		Version:       3,
		CorrelationID: "corr-42",
	}

	body, signature, err := SignKafDelegateRequest(event, "test-secret")

	require.NoError(t, err)
	assert.JSONEq(t, `{"event_type":"kaf_delegate_requested","eventId":"8c4d0fb8-7895-4b2a-969a-1f38829a3c83","tenantId":7,"workItemId":"42","ticketId":"42","taskId":"TASK-42","recordClass":"service_request_item","timestamp":"2026-08-29T12:00:00Z","version":3,"correlationId":"corr-42"}`, string(body))
	assert.Equal(t, "sha256="+expectedKafDelegateHMAC(body, "test-secret"), signature)
	assert.NotContains(t, string(body), "description")
}
