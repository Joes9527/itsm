package service

import (
	"context"

	"itsm-backend/dto"
	"itsm-backend/ent/processinstance"
	"itsm-backend/middleware"
	"itsm-backend/repository/ticket"

	"fmt"
)

func isRequester(t *ticket.Ticket, actorUserID int) bool {
	return t.RequesterID == actorUserID
}

// CanApprove/CanReject：ticket:update 权限 + 非本人提交 + 工单未结束。
func CanApprove(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isRequester(t, actor.UserID) {
		return dto.ActionPermission{Allowed: false, Reason: "不能审批自己提交的工单"}
	}
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法操作"}
	}
	if !middleware.HasResourcePermission(actor.Client, actor.Role, "ticket", "update", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无审批权限"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanReject(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	perm := CanApprove(actor, t)
	if !perm.Allowed && perm.Reason == "不能审批自己提交的工单" {
		perm.Reason = "不能拒绝自己提交的工单"
	}
	return perm
}

// CanAssign：ticket:assign 权限 + 工单未结束，不排除本人（分配是路由工作，非职责分离场景）。
func CanAssign(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法分配"}
	}
	if !middleware.HasResourcePermission(actor.Client, actor.Role, "ticket", "assign", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无分配权限"}
	}
	return dto.ActionPermission{Allowed: true}
}

// CanEdit：ticket:update 权限 + 工单未结束，不排除本人。
func CanEdit(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法编辑"}
	}
	if !middleware.HasResourcePermission(actor.Client, actor.Role, "ticket", "update", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无编辑权限"}
	}
	return dto.ActionPermission{Allowed: true}
}

// CanDelete：ticket:delete 权限 + 工单未结束 + 无运行中的 BPMN 流程实例。
func CanDelete(ctx context.Context, actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
	if isFinalStatus(t.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "工单已结束，无法删除"}
	}
	if !middleware.HasResourcePermission(actor.Client, actor.Role, "ticket", "delete", actor.TenantID) {
		return dto.ActionPermission{Allowed: false, Reason: "无删除权限"}
	}
	running, err := actor.Client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(fmt.Sprintf("ticket:%d", t.ID)),
			processinstance.Status("running"),
			processinstance.TenantID(actor.TenantID),
		).
		Exist(ctx)
	if err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "校验流程状态失败"}
	}
	if running {
		return dto.ActionPermission{Allowed: false, Reason: "工单流程流转中，不可删除"}
	}
	return dto.ActionPermission{Allowed: true}
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

// BuildTicketActions 组装工单核心域的 6 个动作权限，供详情响应的 actions 字段使用。
func BuildTicketActions(ctx context.Context, actor ActionActor, t *ticket.Ticket) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"approve": CanApprove(actor, t),
		"reject":  CanReject(actor, t),
		"assign":  CanAssign(actor, t),
		"edit":    CanEdit(actor, t),
		"cc":      CanCC(ctx, actor, t.ID),
		"delete":  CanDelete(ctx, actor, t),
	}
}
