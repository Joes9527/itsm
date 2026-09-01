package bpmn

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveCallbackOutcome(t *testing.T) {
	tests := []struct {
		name     string
		effect   *CallbackEffect
		optional bool
		status   string
		advance  bool
		audit    string
		metric   CallbackEffectStatus
		errClass CallbackBlockCode
	}{
		{
			name:    "applied completes and advances",
			effect:  AppliedEffect("created", nil),
			status:  CallbackOutboxCompleted,
			advance: true,
			metric:  CallbackEffectApplied,
		},
		{
			name:    "idempotent completes and advances",
			effect:  IdempotentEffect("already delivered", nil),
			status:  CallbackOutboxCompleted,
			advance: true,
			metric:  CallbackEffectIdempotent,
		},
		{
			name:     "required blocked effect is terminal",
			effect:   BlockedEffect(CallbackBlockTargetMissing, "target was not found"),
			status:   CallbackOutboxBlocked,
			audit:    CallbackAuditActionBlocked,
			metric:   CallbackEffectBlocked,
			errClass: CallbackBlockTargetMissing,
		},
		{
			name:     "declared optional blocked effect completes and advances",
			effect:   BlockedEffect(CallbackBlockTargetMissing, "target was not found"),
			optional: true,
			status:   CallbackOutboxCompleted,
			advance:  true,
			audit:    CallbackAuditActionSkippedOptional,
			metric:   CallbackEffectSkippedOptional,
		},
		{
			name:     "nil effect is handler contract block",
			status:   CallbackOutboxBlocked,
			audit:    CallbackAuditActionBlocked,
			metric:   CallbackEffectBlocked,
			errClass: CallbackBlockHandlerContract,
		},
		{
			name:     "handler produced optional skip is handler contract block",
			effect:   &CallbackEffect{Status: CallbackEffectSkippedOptional},
			optional: true,
			status:   CallbackOutboxBlocked,
			audit:    CallbackAuditActionBlocked,
			metric:   CallbackEffectBlocked,
			errClass: CallbackBlockHandlerContract,
		},
		{
			name:     "unknown block code is handler contract block",
			effect:   &CallbackEffect{Status: CallbackEffectBlocked, BlockCode: "secret-detail"},
			status:   CallbackOutboxBlocked,
			audit:    CallbackAuditActionBlocked,
			metric:   CallbackEffectBlocked,
			errClass: CallbackBlockHandlerContract,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := ResolveCallbackOutcome(tt.effect, tt.optional)
			require.Equal(t, tt.status, outcome.OutboxStatus)
			require.Equal(t, tt.advance, outcome.Advance)
			require.Equal(t, tt.audit, outcome.AuditAction)
			require.Equal(t, tt.metric, outcome.MetricEffect)
			require.Equal(t, tt.errClass, outcome.LastErrorClass)
		})
	}
}

func TestResolveCallbackOutcomeDoesNotMutateHandlerEffect(t *testing.T) {
	effect := AppliedEffect("created", map[string]interface{}{"result": "created"})
	outputPointer := reflect.ValueOf(effect.OutputVars).Pointer()

	ResolveCallbackOutcome(effect, false)

	require.Equal(t, outputPointer, reflect.ValueOf(effect.OutputVars).Pointer())
}
