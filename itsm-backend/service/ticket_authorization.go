package service

import (
	"context"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/workitemidentity"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/repository/ticket"
)

// CanAssign：ticket:assign 权限 + 工单未结束，不排除本人（分配是路由工作，非职责分离场景）。
func CanAssign(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法分配"}
	}
	if !authorization.HasResourcePermission(actor.Client, actor.Role, "ticket", "assign", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无分配权限"}
	}
	return dto.ActionPermission{Allowed: true}
}

// CanEdit：ticket:update 权限 + 工单未结束，不排除本人。
func CanEdit(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法编辑"}
	}
	if !authorization.HasResourcePermission(actor.Client, actor.Role, "ticket", "update", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无编辑权限"}
	}
	return dto.ActionPermission{Allowed: true}
}

// CanDelete：ticket:delete 权限 + 工单未结束 + 无运行中的 BPMN 流程实例。
func CanDelete(ctx context.Context, actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if !authorization.HasResourcePermission(actor.Client, actor.Role, "ticket", "delete", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无删除权限"}
	}
	if err := requireTicketDeletionPrecondition(ctx, actor.Client, t.ID, actor.TenantID, string(t.Status), t.RecordClass); err != nil {
		if app, ok := common.AsAppError(err); ok {
			return dto.ActionPermission{Allowed: false, Reason: app.Message}
		}
		return dto.ActionPermission{Allowed: false, Reason: "校验流程状态失败"}
	}
	return dto.ActionPermission{Allowed: true}
}

// requireTicketDeletionPrecondition preserves the existing Ticket entry policy.
// C1 converged the legacy ticket:<id> workflow identity onto the canonical
// Actual deletion calls this with the owning RR transaction after authorization.
func requireTicketDeletionPrecondition(ctx context.Context, client *ent.Client, id, tenantID int, status, recordClass string) error {
	if isFinalStatus(ticket.Status(status)) {
		return common.NewForbiddenError("工单已结束，无法删除")
	}
	// 按 WorkItem 的真实 recordClass 组装流程键：硬编码 generic 会漏掉
	// incident/problem/change/service_request_item 上运行中的流程实例，让带活跃流程的
	// 工单被删除。未知记录类在此失败关闭（删除是破坏性操作，不能靠猜）。
	businessKey, identityErr := workitemidentity.BusinessKey(recordClass, id)
	if identityErr != nil {
		// Fail closed: an unidentifiable workflow state must not permit deletion.
		return common.NewInternalError("工单流程身份无效", identityErr)
	}
	running, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(businessKey), processinstance.Status("running"), processinstance.TenantID(tenantID)).Exist(ctx)
	if err != nil {
		return common.NewInternalError("校验流程状态失败", err)
	}
	if running {
		return common.NewForbiddenError("工单流程流转中，不可删除")
	}
	return nil
}

// CanCC：复用 TicketWorkflowService.EnsureCanCCTicket 的既有业务规则，不重新实现。
// 该函数需要 *ent.Ticket（ent 原生实体），跟其余 CanXxx 接收的 *ticket.Ticket 领域模型不是同一类型，
// 因此单独按 ticketID 重新查询一次，不复用调用方已有的 *ticket.Ticket。
func CanCC(ctx context.Context, actor ActionActor, ticketID int) dto.ActionPermission {
	entTk, err := actor.Client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "工单不存在"}
	}
	workflowSvc := NewTicketWorkflowService(actor.Client, nil)
	if err := workflowSvc.EnsureCanCCTicket(ctx, entTk, actor.UserID, actor.TenantID); err != nil {
		return dto.ActionPermission{Allowed: false, Reason: err.Error()}
	}
	return dto.ActionPermission{Allowed: true}
}

// BuildTicketActions 只组装 Ticket 领域命令。审批命令由 BPMN ProcessTask API
// 单独投影，避免详情接口产生可绕过流程的 approve/reject 动作。
func BuildTicketActions(ctx context.Context, actor ActionActor, t *ticket.Ticket) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"assign": CanAssign(actor, t),
		"edit":   CanEdit(actor, t),
		"cc":     CanCC(ctx, actor, t.ID),
		"delete": CanDelete(ctx, actor, t),
	}
}
