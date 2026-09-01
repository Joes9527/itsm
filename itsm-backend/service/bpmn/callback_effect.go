package bpmn

import (
	"fmt"
	"reflect"
)

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
		OutputVars: snapshotCallbackEvidence(output),
	}
}

func IdempotentEffect(message string, output map[string]interface{}) *CallbackEffect {
	return &CallbackEffect{
		Status:     CallbackEffectIdempotent,
		Message:    message,
		OutputVars: snapshotCallbackEvidence(output),
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
	case CallbackEffectBlocked:
		if !IsAllowedCallbackBlockCode(effect.BlockCode) {
			return fmt.Errorf("callback effect has unsupported block code %q", effect.BlockCode)
		}
	case CallbackEffectSkippedOptional:
		return fmt.Errorf("callback handlers cannot return %q", CallbackEffectSkippedOptional)
	default:
		return fmt.Errorf("callback handler returned unsupported effect status %q", effect.Status)
	}

	effect.OutputVars = snapshotCallbackEvidence(effect.OutputVars)
	effect.UpdatedData = snapshotCallbackEvidence(effect.UpdatedData)
	return nil
}

func snapshotCallbackEvidence(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	snapshot := make(map[string]interface{}, len(source))
	for key, value := range source {
		snapshot[key] = snapshotCallbackEvidenceValue(value)
	}
	return snapshot
}

func snapshotCallbackEvidenceValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	return snapshotCallbackEvidenceReflectValue(reflect.ValueOf(value)).Interface()
}

func snapshotCallbackEvidenceReflectValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		item := snapshotCallbackEvidenceReflectValue(value.Elem())
		snapshot := reflect.New(value.Type()).Elem()
		snapshot.Set(item)
		return snapshot
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		snapshot := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			snapshot.SetMapIndex(iterator.Key(), snapshotCallbackEvidenceReflectValue(iterator.Value()))
		}
		return snapshot
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		snapshot := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			snapshot.Index(i).Set(snapshotCallbackEvidenceReflectValue(value.Index(i)))
		}
		return snapshot
	case reflect.Array:
		snapshot := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			snapshot.Index(i).Set(snapshotCallbackEvidenceReflectValue(value.Index(i)))
		}
		return snapshot
	default:
		return value
	}
}
