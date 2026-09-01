package bpmn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallbackEffectConstructorsPreserveOutcomeData(t *testing.T) {
	output := map[string]interface{}{"deliveryID": 42}

	applied := AppliedEffect("sent", output)
	require.Equal(t, CallbackEffectApplied, applied.Status)
	require.Empty(t, applied.BlockCode)
	require.Equal(t, "sent", applied.Message)
	require.Equal(t, output, applied.OutputVars)

	idempotent := IdempotentEffect("already sent", output)
	require.Equal(t, CallbackEffectIdempotent, idempotent.Status)
	require.Empty(t, idempotent.BlockCode)
	require.Equal(t, "already sent", idempotent.Message)
	require.Equal(t, output, idempotent.OutputVars)

	blocked := BlockedEffect(CallbackBlockRecipientEmpty, "no recipients")
	require.Equal(t, CallbackEffectBlocked, blocked.Status)
	require.Equal(t, CallbackBlockRecipientEmpty, blocked.BlockCode)
	require.Equal(t, "no recipients", blocked.Message)
}

func TestCallbackEffectStatusValues(t *testing.T) {
	require.Equal(t, CallbackEffectStatus("applied"), CallbackEffectApplied)
	require.Equal(t, CallbackEffectStatus("idempotent"), CallbackEffectIdempotent)
	require.Equal(t, CallbackEffectStatus("skipped_optional"), CallbackEffectSkippedOptional)
	require.Equal(t, CallbackEffectStatus("blocked"), CallbackEffectBlocked)
}

func TestAllowedCallbackBlockCodesAreClosed(t *testing.T) {
	allowed := []CallbackBlockCode{
		CallbackBlockTargetTypeMismatch,
		CallbackBlockTargetMissing,
		CallbackBlockRecipientMissing,
		CallbackBlockRecipientEmpty,
		CallbackBlockUnsupportedCCType,
		CallbackBlockUnsupportedTemplate,
		CallbackBlockChannelUnavailable,
		CallbackBlockDeliveryNotCreated,
		CallbackBlockHandlerContract,
	}
	for _, code := range allowed {
		require.True(t, IsAllowedCallbackBlockCode(code), "code %q must be allowed", code)
	}

	require.False(t, IsAllowedCallbackBlockCode(""))
	require.False(t, IsAllowedCallbackBlockCode("database_timeout"))
}

func TestValidateHandlerEffect(t *testing.T) {
	tests := []struct {
		name    string
		effect  *CallbackEffect
		wantErr bool
	}{
		{"applied", AppliedEffect("sent", nil), false},
		{"idempotent", IdempotentEffect("already sent", nil), false},
		{"blocked", BlockedEffect(CallbackBlockRecipientEmpty, "no recipients"), false},
		{"nil", nil, true},
		{"blocked without code", &CallbackEffect{Status: CallbackEffectBlocked}, true},
		{"blocked with unknown code", &CallbackEffect{Status: CallbackEffectBlocked, BlockCode: "database_timeout"}, true},
		{"applied with block code", &CallbackEffect{Status: CallbackEffectApplied, BlockCode: CallbackBlockRecipientEmpty}, true},
		{"idempotent with block code", &CallbackEffect{Status: CallbackEffectIdempotent, BlockCode: CallbackBlockRecipientEmpty}, true},
		{"unknown", &CallbackEffect{Status: "unknown"}, true},
		{"handler cannot skip", &CallbackEffect{Status: CallbackEffectSkippedOptional}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHandlerEffect(tt.effect)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
