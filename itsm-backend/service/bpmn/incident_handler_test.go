package bpmn

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"testing"
)

func TestIncidentServiceTaskHandlerLifecycleRequiresDurableIdentity(t *testing.T) {
	h := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	for _, action := range []string{"assign_incident", "escalate_incident", "start_incident", "resolve_incident", "close_incident", "reopen_incident", "acknowledge_incident"} {
		ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 1)
		effect, err := h.Execute(ctx, nil, map[string]interface{}{"action": action, "incident_id": 1, "assignee_id": 1, "version": 1})
		require.NoError(t, err)
		require.Equal(t, CallbackEffectBlocked, effect.Status)
	}
}
func TestIncidentServiceTaskHandlerUnknownActionBlocks(t *testing.T) {
	h := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	effect, err := h.Execute(context.Background(), nil, map[string]interface{}{"action": "unknown"})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
}
