package database

import (
	"context"
	"fmt"

	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/ent"
)

// ExecutionPolicy freezes the trusted startup manifest. It does not authorize a
// business action or discover additional scopes from request data or the database.
// Bootstrap must admit the configured database role before injecting this policy.
type ExecutionPolicy struct {
	mode   string
	scopes map[int]executionscope.Ref
}

func NewExecutionPolicy(cfg config.ExecutionConfig) (*ExecutionPolicy, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &ExecutionPolicy{mode: cfg.Mode, scopes: make(map[int]executionscope.Ref, len(cfg.Scopes))}
	for _, s := range cfg.Scopes {
		p.scopes[s.TenantID] = executionscope.Ref{DeploymentID: cfg.DeploymentID, ScopeID: s.ScopeID, TenantID: s.TenantID}
	}
	return p, nil
}

func (p *ExecutionPolicy) scopeFor(tenantID int) (executionscope.Ref, bool, error) {
	if p == nil || tenantID <= 0 {
		return executionscope.Ref{}, false, executionscope.ErrDenied
	}
	switch p.mode {
	case "standard":
		return executionscope.Ref{}, false, nil
	case "candidate":
		if ref, ok := p.scopes[tenantID]; ok {
			return ref, true, nil
		}
	}
	return executionscope.Ref{}, false, fmt.Errorf("%w: tenant is not in the admitted execution manifest", executionscope.ErrDenied)
}

// BindEnt applies the manifest in the original authorized write transaction.
// Repeated attempts must call this again on their new transaction.
func (p *ExecutionPolicy) BindEnt(ctx context.Context, tx *ent.Tx, tenantID int) error {
	if ctx == nil || tx == nil {
		return executionscope.ErrDenied
	}
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return err
	}
	if scoped {
		return BindEntExecutionScope(ctx, tx, ref)
	}
	return nil
}

// RequireEntMembers checks every written WorkItem against the same authority.
// It never enrolls an existing ID and never substitutes another transaction.
func (p *ExecutionPolicy) RequireEntMembers(ctx context.Context, tx *ent.Tx, tenantID int, ids ...int) error {
	if ctx == nil || tx == nil || len(ids) == 0 {
		return executionscope.ErrDenied
	}
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id <= 0 {
			return executionscope.ErrDenied
		}
		if scoped {
			if err := RequireEntExecutionMember(ctx, tx, ref, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// IsCandidate reports the frozen deployment mode, not authorization. Callers must
// BindEnt first so absent policy or unadmitted tenant cannot bypass enforcement.
func (p *ExecutionPolicy) IsCandidate() bool { return p != nil && p.mode == "candidate" }
