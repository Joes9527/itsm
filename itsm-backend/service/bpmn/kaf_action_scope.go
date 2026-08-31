package bpmn

import "context"

type kafActionScopeContextKey struct{}

// KafActionScope is the immutable execution identity supplied to KAF callback
// handlers. Domain handlers use its idempotency key to make recovery safe.
type KafActionScope struct {
	ledgerID         int
	tenantID         int
	taskID           string
	runID            string
	stepID           string
	action           string
	idempotencyKey   string
	correlationID    string
	procedureRef     string
	procedureVersion string
}

// NewKafActionScope creates the only constructible KAF action scope value.
func NewKafActionScope(ledgerID, tenantID int, taskID, runID, stepID, action, idempotencyKey, correlationID, procedureRef, procedureVersion string) KafActionScope {
	return KafActionScope{
		ledgerID: ledgerID, tenantID: tenantID, taskID: taskID, runID: runID, stepID: stepID,
		action: action, idempotencyKey: idempotencyKey, correlationID: correlationID,
		procedureRef: procedureRef, procedureVersion: procedureVersion,
	}
}

func (s KafActionScope) LedgerID() int          { return s.ledgerID }
func (s KafActionScope) TenantID() int          { return s.tenantID }
func (s KafActionScope) TaskID() string         { return s.taskID }
func (s KafActionScope) RunID() string          { return s.runID }
func (s KafActionScope) StepID() string         { return s.stepID }
func (s KafActionScope) Action() string         { return s.action }
func (s KafActionScope) IdempotencyKey() string { return s.idempotencyKey }
func (s KafActionScope) CorrelationID() string  { return s.correlationID }
func (s KafActionScope) ProcedureRef() string   { return s.procedureRef }
func (s KafActionScope) ProcedureVersion() string {
	return s.procedureVersion
}

// WithKafActionScope attaches an immutable copy of the action scope to a
// callback context. It is intentionally separate from caller-controlled vars.
func WithKafActionScope(ctx context.Context, scope KafActionScope) context.Context {
	return context.WithValue(ctx, kafActionScopeContextKey{}, scope)
}

// KafActionScopeFromContext returns the action scope available to a callback.
func KafActionScopeFromContext(ctx context.Context) (KafActionScope, bool) {
	if ctx == nil {
		return KafActionScope{}, false
	}
	scope, ok := ctx.Value(kafActionScopeContextKey{}).(KafActionScope)
	return scope, ok
}
