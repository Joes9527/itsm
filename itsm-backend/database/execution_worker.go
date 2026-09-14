package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	entsql "entgo.io/ent/dialect/sql"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
)

// WorkerPredicate validates a frozen cross-tenant manifest in the owning write
// transaction. The returned predicate must be attached to every selection and
// authoritative mutation; a successful preflight alone is not write permission.
// Column names are repository-owned identifiers, never request data.
func (p *ExecutionPolicy) WorkerPredicate(ctx context.Context, tx *ent.Tx, tenantColumn, workItemColumn string) (func(*entsql.Selector), error) {
	if p == nil || ctx == nil || tx == nil {
		return nil, executionscope.ErrDenied
	}
	if p.mode == "standard" {
		return func(*entsql.Selector) {}, nil
	}
	if p.mode != "candidate" || !tenantctx.IsSystemBypass(ctx) || len(p.scopes) == 0 {
		return nil, executionscope.ErrDenied
	}
	tenants := make([]int, 0, len(p.scopes))
	for tenant := range p.scopes {
		tenants = append(tenants, tenant)
	}
	sort.Ints(tenants)
	refs := make([]executionscope.Ref, 0, len(tenants))
	for _, tenant := range tenants {
		ref := p.scopes[tenant]
		var actual string
		err := scanExecutionRow(ctx, tx.Client(), &actual, `SELECT s.id::text FROM public.execution_scopes s JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id WHERE b.runtime_role=session_user AND b.mode='candidate' AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND s.status='active'`, ref.ScopeID, ref.DeploymentID, ref.TenantID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: worker binding or active manifest missing", executionscope.ErrDenied)
		}
		if err != nil {
			return nil, fmt.Errorf("validate worker execution manifest: %w", err)
		}
		refs = append(refs, ref)
	}
	return executionMemberPredicate(refs, tenantColumn, workItemColumn), nil
}

func executionMemberPredicate(refs []executionscope.Ref, tenantColumn, workItemColumn string) func(*entsql.Selector) {
	return func(outer *entsql.Selector) {
		members := entsql.Table("execution_scope_members").Schema("public").As("candidate_members")
		scopes := entsql.Table("execution_scopes").Schema("public").As("candidate_scopes")
		bindings := entsql.Table("execution_runtime_bindings").Schema("public").As("candidate_bindings")
		admitted := make([]*entsql.Predicate, 0, len(refs))
		for _, ref := range refs {
			admitted = append(admitted, entsql.And(entsql.EQ(scopes.C("id"), ref.ScopeID), entsql.EQ(scopes.C("deployment_id"), ref.DeploymentID), entsql.EQ(scopes.C("tenant_id"), ref.TenantID)))
		}
		query := entsql.Select(members.C("work_item_id")).From(members).Join(scopes).On(members.C("scope_id"), scopes.C("id")).Join(bindings).On(bindings.C("deployment_id"), scopes.C("deployment_id")).Where(entsql.And(
			entsql.Or(admitted...), entsql.EQ(scopes.C("status"), "active"), entsql.EQ(bindings.C("mode"), "candidate"),
			entsql.P(func(b *entsql.Builder) { b.Ident(bindings.C("runtime_role")).WriteString(" = session_user") }),
			entsql.ColumnsEQ(scopes.C("tenant_id"), outer.C(tenantColumn)), entsql.ColumnsEQ(members.C("work_item_id"), outer.C(workItemColumn)),
		))
		outer.Where(entsql.Exists(query))
	}
}

// TenantPredicate binds only the tenant already authorized by the caller. It
// uses the same membership SQL as transport, without accepting system bypass.
func (p *ExecutionPolicy) TenantPredicate(ctx context.Context, tx *ent.Tx, tenantID int, tenantColumn, workItemColumn string) (func(*entsql.Selector), error) {
	if err := p.BindEnt(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	ref, scoped, err := p.scopeFor(tenantID)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return func(*entsql.Selector) {}, nil
	}
	return executionMemberPredicate([]executionscope.Ref{ref}, tenantColumn, workItemColumn), nil
}

// CallbackPredicate follows the immutable process instance execution reference.
// tenantID zero is reserved for the separate system discovery transaction.
func (p *ExecutionPolicy) CallbackPredicate(ctx context.Context, tx *ent.Tx, tenantID int) (func(*entsql.Selector), error) {
	var member func(*entsql.Selector)
	var err error
	if tenantID == 0 {
		member, err = p.WorkerPredicate(ctx, tx, "tenant_id", "execution_work_item_id")
	} else {
		member, err = p.TenantPredicate(ctx, tx, tenantID, "tenant_id", "execution_work_item_id")
	}
	if err != nil {
		return nil, err
	}
	if !p.IsCandidate() {
		return member, nil
	}
	return func(outer *entsql.Selector) {
		instance := entsql.Table("process_instances").Schema("public").As("candidate_instance")
		query := entsql.Select(instance.C("id")).From(instance).Where(entsql.And(entsql.ColumnsEQ(instance.C("id"), outer.C("process_instance_id")), entsql.ColumnsEQ(instance.C("tenant_id"), outer.C("tenant_id"))))
		member(query)
		outer.Where(entsql.Exists(query))
	}, nil
}
