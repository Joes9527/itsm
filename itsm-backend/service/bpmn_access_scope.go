package service

import (
	"context"

	"itsm-backend/common"
)

type BPMNAccessScope struct {
	UserID, TenantID      int
	CanReadAllInstances   bool
	CanUpdateAllInstances bool
	CanReadAllTasks       bool
	CanUpdateAllTasks     bool
}

type bpmnAccessScopeContextKey struct{}
type bpmnTrustedTenantContextKey struct{}

func WithBPMNAccessScope(ctx context.Context, scope BPMNAccessScope) context.Context {
	return context.WithValue(ctx, bpmnAccessScopeContextKey{}, scope)
}

// WithTrustedBPMNTenantContext marks an application-service call whose tenant
// was resolved from an authoritative domain record rather than participant
// input. HTTP controllers must use BPMNAccessScope instead.
func WithTrustedBPMNTenantContext(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, bpmnTrustedTenantContextKey{}, tenantID)
}

func trustedBPMNTenantFromContext(ctx context.Context) (int, bool) {
	tenantID, ok := ctx.Value(bpmnTrustedTenantContextKey{}).(int)
	return tenantID, ok && tenantID > 0
}

func bpmnAuthorizedTenantFromContext(ctx context.Context) (int, error) {
	if _, present := bpmnAccessScopeValue(ctx); present {
		scope, err := BPMNAccessScopeFromContext(ctx)
		if err != nil {
			return 0, err
		}
		return scope.TenantID, nil
	}
	if tenantID, ok := trustedBPMNTenantFromContext(ctx); ok {
		return tenantID, nil
	}
	return 0, common.NewForbiddenError("缺少 BPMN 租户授权上下文")
}

func bpmnAccessScopeValue(ctx context.Context) (BPMNAccessScope, bool) {
	scope, ok := ctx.Value(bpmnAccessScopeContextKey{}).(BPMNAccessScope)
	return scope, ok
}

func BPMNAccessScopeFromContext(ctx context.Context) (BPMNAccessScope, error) {
	scope, ok := bpmnAccessScopeValue(ctx)
	if !ok || scope.UserID <= 0 || scope.TenantID <= 0 {
		return BPMNAccessScope{}, common.NewForbiddenError("缺少 BPMN 实例授权上下文")
	}
	return scope, nil
}

func RequireBPMNInstanceReadAll(ctx context.Context) (BPMNAccessScope, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return BPMNAccessScope{}, err
	}
	if !scope.CanReadAllInstances {
		return BPMNAccessScope{}, common.NewForbiddenError("无权读取流程实例汇总数据")
	}
	return scope, nil
}
