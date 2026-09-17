package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGenericFulfillmentReservedInputs(t *testing.T) {
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult"} {
		for _, value := range []interface{}{nil, false, true, "approved"} {
			require.ErrorContains(t, RejectGenericFulfillmentReservedInputs(map[string]interface{}{key: value}), key)
		}
	}
	require.NoError(t, RejectGenericFulfillmentReservedInputs(nil))
	require.NoError(t, RejectGenericFulfillmentReservedInputs(map[string]interface{}{"workItemCompletionNote": "evidence", "title": "work"}))
}
