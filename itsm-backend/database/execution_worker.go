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
	}, nil
}
