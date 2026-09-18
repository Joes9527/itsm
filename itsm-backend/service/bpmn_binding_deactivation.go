package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent/processbinding"
)

// DeactivateBinding is a narrow command: legacy identity is preserved, never
// translated or republished. Caller identity comes from the authenticated boundary.
func (s *ProcessBindingService) DeactivateBinding(ctx context.Context, caller ActionActor, id int, request dto.DeactivateProcessBindingRequest) (*dto.ProcessBinding, error) {
	if caller.TenantID <= 0 || caller.UserID <= 0 || caller.Role != "super_admin" {
		return nil, common.NewForbiddenError("流程绑定停用需要当前租户的管理员身份")
	}
	reason := strings.TrimSpace(request.Reason)
	if id <= 0 || reason == "" || len([]rune(reason)) > 4000 || request.ExpectedUpdatedAt.IsZero() {
		return nil, common.NewBadRequestError("停用原因和观察版本必填", nil)
	}
	ctx = tenantctx.WithTenantID(ctx, caller.TenantID)
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	binding, err := tx.ProcessBinding.Query().Where(processbinding.IDEQ(id), processbinding.TenantIDEQ(caller.TenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if !binding.IsActive {
		// State-idempotent retry: no second audit or timestamp mutation. Reactivation
		// followed by a stale retry still reaches the CAS below and is rejected.
		return s.toBindingResponse(binding), nil
	}
	if !binding.UpdatedAt.Equal(request.ExpectedUpdatedAt) {
		return nil, common.NewConflictError("process binding", "绑定已更新，请重新读取后确认")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	count, err := tx.ProcessBinding.Update().Where(processbinding.IDEQ(id), processbinding.TenantIDEQ(caller.TenantID), processbinding.IsActiveEQ(true), processbinding.UpdatedAtEQ(request.ExpectedUpdatedAt)).SetIsActive(false).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, common.NewConflictError("process binding", "绑定已更新，请重新读取后确认")
	}
	evidence, err := json.Marshal(struct {
		BindingID         int       `json:"bindingId"`
		BusinessType      string    `json:"businessType"`
		Reason            string    `json:"reason"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
		WasActive         bool      `json:"wasActive"`
	}{id, binding.BusinessType, reason, request.ExpectedUpdatedAt, true})
	if err != nil {
		return nil, err
	}
	if err := tx.AuditLog.Create().SetTenantID(caller.TenantID).SetUserID(caller.UserID).SetResource("bpmn").SetAction("deactivate").SetPath(fmt.Sprintf("/api/v1/process-bindings/%d/deactivate", id)).SetMethod("POST").SetStatusCode(200).SetRequestBody(string(evidence)).Exec(ctx); err != nil {
		return nil, err
	}
	binding.IsActive = false
	binding.UpdatedAt = now
	result := s.toBindingResponse(binding)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
