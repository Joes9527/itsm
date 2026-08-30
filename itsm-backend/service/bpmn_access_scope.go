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
type noBPMNAccessScope struct{}

func WithBPMNAccessScope(ctx context.Context, scope BPMNAccessScope) context.Context {
	return context.WithValue(ctx, bpmnAccessScopeContextKey{}, scope)
}

func bpmnAccessScopeValue(ctx context.Context) (BPMNAccessScope, bool) {
	scope, ok := ctx.Value(bpmnAccessScopeContextKey{}).(BPMNAccessScope)
	return scope, ok
}

// WithoutBPMNAccessScope marks an established internal call as having no external actor scope.
func WithoutBPMNAccessScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, bpmnAccessScopeContextKey{}, noBPMNAccessScope{})
}

func BPMNAccessScopeFromContext(ctx context.Context) (BPMNAccessScope, error) {
	scope, ok := bpmnAccessScopeValue(ctx)
	if !ok || scope.UserID <= 0 || scope.TenantID <= 0 {
		return BPMNAccessScope{}, common.NewForbiddenError("缺少 BPMN 实例授权上下文")
	}
	return scope, nil
}
