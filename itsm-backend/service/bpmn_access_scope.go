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

func WithBPMNAccessScope(ctx context.Context, scope BPMNAccessScope) context.Context {
	return context.WithValue(ctx, bpmnAccessScopeContextKey{}, scope)
}

func BPMNAccessScopeFromContext(ctx context.Context) (BPMNAccessScope, error) {
	scope, ok := ctx.Value(bpmnAccessScopeContextKey{}).(BPMNAccessScope)
	if !ok || scope.UserID <= 0 || scope.TenantID <= 0 {
		return BPMNAccessScope{}, common.NewForbiddenError("缺少 BPMN 实例授权上下文")
	}
	return scope, nil
}
