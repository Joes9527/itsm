package service_request

import (
	"context"
	"database/sql"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

// ClassificationCorrection 是 Requested Item 的分类纠正命令。
//
// 由本域拥有（不在通用工单更新里实现），因为申请项的完整度要求与普通工单不同：
// 服务目录已声明完整的三级默认分类，因此申请项**不允许**被改成部分分类或清空，
// 否则该申请项在统计、分派规则与知识引用里都不再可解释。
type ClassificationCorrection struct {
	Meta             workitemmutation.Meta
	ServiceRequestID int
	TargetCategoryID int
	Reason           string
	// ActorRole 由 HTTP 层从会话解析后传入；服务层据此判定是否为管理侧操作。
	ActorRole string
}

// CorrectClassification 在单一事务内完成：权限与执行范围校验、版本 CAS、
// 完整三级目标校验、最深节点写入与前后路径证据写入。
func (s *Service) CorrectClassification(ctx context.Context, cmd ClassificationCorrection) (*ent.Ticket, error) {
	if s.client == nil {
		return nil, common.NewInternalError("classification correction application is unavailable", nil)
	}
	if cmd.ServiceRequestID <= 0 || cmd.Meta.TenantID <= 0 || cmd.Meta.ActorID <= 0 {
		return nil, common.NewValidationError("classification correction requires tenant, actor and service request", nil)
	}
	if cmd.Meta.ExpectedVersion <= 0 {
		return nil, common.NewValidationError("explicit expected version required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != cmd.Meta.TenantID {
		return nil, common.NewForbiddenError("tenant context mismatch")
	}
	if !s.canManageServiceRequest(ctx, cmd.ActorRole, cmd.Meta.TenantID) {
		return nil, common.NewForbiddenError("Only an administrator can reclassify this requested item")
	}
	ctx = tenantctx.WithTenantID(ctx, cmd.Meta.TenantID)

	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	request, err := tx.ServiceRequest.Query().Where(servicerequest.ID(cmd.ServiceRequestID), requestScope(cmd.Meta.TenantID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, common.NewNotFoundError("Service Request not found")
		}
		return nil, err
	}
	item, err := tx.Ticket.Query().
		Where(ticket.IDEQ(request.TicketID), ticket.TenantIDEQ(cmd.Meta.TenantID),
			ticket.RecordClassEQ(creation.RecordClassServiceRequestItem), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, common.NewNotFoundError("Requested item not found")
		}
		return nil, err
	}
	if err := requireRequestExecutionTx(ctx, tx, s.execution, cmd.Meta.TenantID, item.ID); err != nil {
		return nil, err
	}
	if item.Version != cmd.Meta.ExpectedVersion {
		return nil, common.NewVersionConflictError("服务请求", request.ID, cmd.Meta.ExpectedVersion, item.Version)
	}

	// 原因仅在分类确实变化时必填。
	changed := cmd.TargetCategoryID != item.CategoryID
	if err := service.RequireCTICorrectionReason(cmd.Reason, changed); err != nil {
		return nil, err
	}
	if !changed {
		return nil, common.NewValidationError("classification correction requires a change", nil)
	}
	beforePath, err := service.CTICorrectionBeforePathTx(ctx, tx, cmd.Meta.TenantID, item.CategoryID)
	if err != nil {
		return nil, err
	}
	// 申请项必须保持完整三级：既不允许清空，也不接受部分分类。
	targetPath, err := service.ValidateCTICorrectionTargetTx(ctx, tx, cmd.Meta.TenantID, cmd.TargetCategoryID,
		service.CTICorrectionTargetPolicy{RequireComplete: true})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	updated, err := tx.Ticket.UpdateOneID(item.ID).
		Where(ticket.TenantIDEQ(cmd.Meta.TenantID), ticket.RecordClassEQ(creation.RecordClassServiceRequestItem),
			ticket.DeletedAtIsNil(), ticket.VersionEQ(cmd.Meta.ExpectedVersion)).
		SetCategoryID(cmd.TargetCategoryID).
		SetUpdatedAt(now).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, common.NewVersionConflictError("服务请求", request.ID, cmd.Meta.ExpectedVersion, 0)
		}
		return nil, err
	}

	// 证据与写入同一事务：审计失败整笔回滚。
	if err := service.RecordCTICorrectionAuditTx(ctx, tx, service.CTICorrectionAudit{
		TenantID: cmd.Meta.TenantID, ActorID: cmd.Meta.ActorID, Source: cmd.Meta.Source,
		WorkItemID: item.ID, Reason: cmd.Reason, CorrelationID: cmd.Meta.CorrelationID,
	}, service.CTICorrectionEvidence{Before: beforePath, After: targetPath}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}
