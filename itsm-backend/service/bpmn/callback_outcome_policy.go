package bpmn

// Callback outbox states are duplicated here as plain values so this pure
// policy can remain independent from the parent service package.
const (
	CallbackOutboxCompleted = "completed"
	CallbackOutboxBlocked   = "blocked"

	CallbackAuditActionBlocked         = "callback_blocked"
	CallbackAuditActionSkippedOptional = "callback_skipped_optional"
)

// CallbackOutcome is the worker-owned decision derived from a validated
// handler effect and the definition-declared optional flag snapshotted in the
// outbox row.
type CallbackOutcome struct {
	OutboxStatus   string
	Advance        bool
	AuditAction    string
	MetricEffect   CallbackEffectStatus
	BlockCode      CallbackBlockCode
	LastErrorClass CallbackBlockCode
}

// ResolveCallbackOutcome is intentionally pure. A handler cannot produce an
// optional skip: only this policy derives it from a valid blocked effect and a
// definition-declared optional flag.
func ResolveCallbackOutcome(effect *CallbackEffect, optionalDeclared bool) CallbackOutcome {
	if effect == nil {
		return blockedCallbackOutcome(CallbackBlockHandlerContract)
	}
	validationCopy := *effect
	if err := ValidateHandlerEffect(&validationCopy); err != nil {
		return blockedCallbackOutcome(CallbackBlockHandlerContract)
	}

	switch effect.Status {
	case CallbackEffectApplied:
		return CallbackOutcome{
			OutboxStatus: CallbackOutboxCompleted,
			Advance:      true,
			MetricEffect: CallbackEffectApplied,
		}
	case CallbackEffectIdempotent:
		return CallbackOutcome{
			OutboxStatus: CallbackOutboxCompleted,
			Advance:      true,
			MetricEffect: CallbackEffectIdempotent,
		}
	case CallbackEffectBlocked:
		if optionalDeclared {
			return CallbackOutcome{
				OutboxStatus: CallbackOutboxCompleted,
				Advance:      true,
				AuditAction:  CallbackAuditActionSkippedOptional,
				MetricEffect: CallbackEffectSkippedOptional,
				BlockCode:    effect.BlockCode,
			}
		}
		return CallbackOutcome{
			OutboxStatus:   CallbackOutboxBlocked,
			AuditAction:    CallbackAuditActionBlocked,
			MetricEffect:   CallbackEffectBlocked,
			BlockCode:      effect.BlockCode,
			LastErrorClass: effect.BlockCode,
		}
	default:
		return blockedCallbackOutcome(CallbackBlockHandlerContract)
	}
}

func blockedCallbackOutcome(code CallbackBlockCode) CallbackOutcome {
	return CallbackOutcome{
		OutboxStatus:   CallbackOutboxBlocked,
		AuditAction:    CallbackAuditActionBlocked,
		MetricEffect:   CallbackEffectBlocked,
		BlockCode:      code,
		LastErrorClass: code,
	}
}
