package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/ent"
)

// ExecutionPolicy freezes the trusted startup manifest. It does not authorize a
// business action or discover additional scopes from request data or the database.
// Bootstrap must admit the configured database role before injecting this policy.
type ExecutionPolicy struct {
	mode         string
	deploymentID string
	scopes       map[int]executionscope.Ref
}

func NewExecutionPolicy(cfg config.ExecutionConfig) (*ExecutionPolicy, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &ExecutionPolicy{mode: cfg.Mode, deploymentID: cfg.DeploymentID, scopes: make(map[int]executionscope.Ref, len(cfg.Scopes))}
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

// CandidateTenantIDs returns a copy of the frozen discovery manifest. Each
// tenant still needs its own bound transaction; this is not authorization.
func (p *ExecutionPolicy) CandidateTenantIDs() []int {
	if p == nil || !p.IsCandidate() {
		return nil
	}
	ids := make([]int, 0, len(p.scopes))
	for id := range p.scopes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// CandidateRef returns a copy of the frozen identity, never execution permission.
func (p *ExecutionPolicy) CandidateRef(tenantID int) (executionscope.Ref, error) {
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return executionscope.Ref{}, err
	}
	if !scoped {
		return executionscope.Ref{}, executionscope.ErrDenied
	}
	return ref, nil
}

// EventRef returns the frozen deployment identity for a persistent event. An
// empty scope is valid only in standard mode; it never grants database access.
// Candidate membership remains mandatory in the original owner transaction.
func (p *ExecutionPolicy) EventRef(tenantID int) (executionscope.Ref, error) {
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return executionscope.Ref{}, err
	}
	if scoped {
		return ref, nil
	}
	if err := executionscope.ValidateDeploymentID(p.deploymentID); err != nil {
		return executionscope.Ref{}, executionscope.ErrDenied
	}
	return executionscope.Ref{DeploymentID: p.deploymentID, TenantID: tenantID}, nil
}

// RequireEntToolInvocation checks structural origin in the caller's transaction.
// It is not approval or actor authorization and never enrolls an existing source.
func (p *ExecutionPolicy) RequireEntToolInvocation(ctx context.Context, tx *ent.Tx, tenantID, invocationID int) error {
	if ctx == nil || tx == nil || invocationID <= 0 {
		return executionscope.ErrDenied
	}
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return err
	}
	if !scoped {
		return nil
	}
	if err := validateExecutionContext(ctx, ref); err != nil {
		return err
	}
	var id int
	err = scanExecutionRow(ctx, tx.Client(), &id, `SELECT m.invocation_id FROM public.execution_tool_invocations m
 JOIN public.tool_invocations i ON i.id=m.invocation_id AND i.tenant_id=m.tenant_id
 JOIN public.execution_scopes s ON s.id=m.scope_id AND s.tenant_id=m.tenant_id
 JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id
 WHERE b.runtime_role=session_user AND b.mode='candidate' AND s.status='active'
 AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND m.invocation_id=$4
 AND current_setting('app.execution_scope_id',true)=$1::text`, ref.ScopeID, ref.DeploymentID, ref.TenantID, invocationID)
	if err == sql.ErrNoRows {
		return executionscope.ErrDenied
	}
	if err != nil {
		return fmt.Errorf("verify tool invocation origin: %w", err)
	}
	return nil
}
