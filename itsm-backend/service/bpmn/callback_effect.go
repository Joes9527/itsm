package bpmn

import "fmt"

// CallbackEffectStatus describes the durable business effect produced by a
// synchronous BPMN callback.
type CallbackEffectStatus string

const (
	CallbackEffectApplied         CallbackEffectStatus = "applied"
	CallbackEffectIdempotent      CallbackEffectStatus = "idempotent"
	CallbackEffectSkippedOptional CallbackEffectStatus = "skipped_optional"
	CallbackEffectBlocked         CallbackEffectStatus = "blocked"
)

// CallbackBlockCode is a bounded, non-sensitive reason for a blocked effect.
type CallbackBlockCode string

const (
	CallbackBlockTargetTypeMismatch  CallbackBlockCode = "target_type_mismatch"
	CallbackBlockTargetMissing       CallbackBlockCode = "target_missing"
	CallbackBlockRecipientMissing    CallbackBlockCode = "recipient_missing"
	CallbackBlockRecipientEmpty      CallbackBlockCode = "recipient_empty"
	CallbackBlockUnsupportedCCType   CallbackBlockCode = "unsupported_cc_type"
	CallbackBlockUnsupportedTemplate CallbackBlockCode = "unsupported_placeholder"
	CallbackBlockChannelUnavailable  CallbackBlockCode = "channel_unavailable"
	CallbackBlockDeliveryNotCreated  CallbackBlockCode = "delivery_not_created"
	CallbackBlockHandlerContract     CallbackBlockCode = "handler_contract"
)

// CallbackEffect is the explicit outcome returned by a synchronous callback
// handler. Skipped-optional is reserved for the orchestration layer and is not
// a valid handler result.
type CallbackEffect struct {
	Status      CallbackEffectStatus
	BlockCode   CallbackBlockCode
	Message     string
	OutputVars  map[string]interface{}
	UpdatedData map[string]interface{}
}

func AppliedEffect(message string, output map[string]interface{}) *CallbackEffect {
	return &CallbackEffect{
		Status:     CallbackEffectApplied,
		Message:    message,
		OutputVars: output,
	}
}

func IdempotentEffect(message string, output map[string]interface{}) *CallbackEffect {
	return &CallbackEffect{
		Status:     CallbackEffectIdempotent,
		Message:    message,
		OutputVars: output,
	}
}

func BlockedEffect(code CallbackBlockCode, message string) *CallbackEffect {
	return &CallbackEffect{
		Status:    CallbackEffectBlocked,
		BlockCode: code,
		Message:   message,
	}
}

// IsAllowedCallbackBlockCode reports whether code is safe for durable status,
// audit, and metric classification.
func IsAllowedCallbackBlockCode(code CallbackBlockCode) bool {
	switch code {
	case CallbackBlockTargetTypeMismatch,
		CallbackBlockTargetMissing,
		CallbackBlockRecipientMissing,
		CallbackBlockRecipientEmpty,
		CallbackBlockUnsupportedCCType,
		CallbackBlockUnsupportedTemplate,
		CallbackBlockChannelUnavailable,
		CallbackBlockDeliveryNotCreated,
		CallbackBlockHandlerContract:
		return true
	default:
		return false
	}
}

// ValidateHandlerEffect enforces the outcomes a callback handler may return.
func ValidateHandlerEffect(effect *CallbackEffect) error {
	if effect == nil {
		return fmt.Errorf("callback handler returned a nil effect")
	}

	switch effect.Status {
	case CallbackEffectApplied, CallbackEffectIdempotent:
		if effect.BlockCode != "" {
			return fmt.Errorf("callback effect %q cannot include block code %q", effect.Status, effect.BlockCode)
		}
		return nil
	case CallbackEffectBlocked:
		if !IsAllowedCallbackBlockCode(effect.BlockCode) {
			return fmt.Errorf("callback effect has unsupported block code %q", effect.BlockCode)
		}
		return nil
	case CallbackEffectSkippedOptional:
		return fmt.Errorf("callback handlers cannot return %q", CallbackEffectSkippedOptional)
	default:
		return fmt.Errorf("callback handler returned unsupported effect status %q", effect.Status)
	}
}
