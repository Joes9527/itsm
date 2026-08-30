package change

import (
	"errors"

	"itsm-backend/dto"
	"itsm-backend/middleware"
	"itsm-backend/service"
)

func canWriteChange(actor service.ActionActor) bool {
	return middleware.HasResourcePermission(actor.Client, actor.Role, "change", "write", actor.TenantID)
}

func canApproveChangePermission(actor service.ActionActor) bool {
	return middleware.HasResourcePermission(actor.Client, actor.Role, "change", "approve", actor.TenantID)
}

func CanSubmitForApproval(actor service.ActionActor, c *Change) dto.ActionPermission {
	if c.Status != string(dto.ChangeStatusDraft) {
		return dto.ActionPermission{Allowed: false, Reason: "只有草稿状态的变更可以提交审批"}
	}
	if !canWriteChange(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限提交变更审批"}
	}
	return dto.ActionPermission{Allowed: true}
}

func isChangeSubmitted(status string) bool {
	return status == string(dto.ChangeStatusPending) || status == "submitted"
}

// canApproveChange is shared by the read projection and command path so direct
// approve/reject requests cannot bypass the self-approval guard.
func canApproveChange(actorUserID int, c *Change) error {
	return canDecideChange(actorUserID, c, "不能审批自己提交的变更", "只有已提交待审批的变更可以批准")
}

func canRejectChange(actorUserID int, c *Change) error {
	return canDecideChange(actorUserID, c, "不能驳回自己提交的变更", "只有已提交待审批的变更可以驳回")
}

func canDecideChange(actorUserID int, c *Change, selfReason, statusReason string) error {
	if c.CreatedBy == actorUserID {
		return errors.New(selfReason)
	}
	if !isChangeSubmitted(c.Status) {
		return errors.New(statusReason)
	}
	return nil
}

func CanApproveChange(actor service.ActionActor, c *Change) dto.ActionPermission {
	if err := canApproveChange(actor.UserID, c); err != nil {
		return dto.ActionPermission{Allowed: false, Reason: err.Error()}
	}
	if !canApproveChangePermission(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限审批变更"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanRejectChange(actor service.ActionActor, c *Change) dto.ActionPermission {
	if err := canRejectChange(actor.UserID, c); err != nil {
		return dto.ActionPermission{Allowed: false, Reason: err.Error()}
	}
	if !canApproveChangePermission(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限驳回变更"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanStartImplementation(actor service.ActionActor, c *Change) dto.ActionPermission {
	if !service.IsValidChangeStatusTransition(c.Status, string(dto.ChangeStatusInProgress), c.Type) {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态和变更类型不允许开始实施"}
	}
	if !canWriteChange(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限开始实施"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanCompleteImplementation(actor service.ActionActor, c *Change) dto.ActionPermission {
	if c.Status != string(dto.ChangeStatusInProgress) {
		return dto.ActionPermission{Allowed: false, Reason: "只有实施中的变更可以标记完成"}
	}
	if !canWriteChange(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限完成实施"}
	}
	return dto.ActionPermission{Allowed: true}
}

func BuildChangeActions(actor service.ActionActor, c *Change) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"submitForApproval":      CanSubmitForApproval(actor, c),
		"approve":                CanApproveChange(actor, c),
		"reject":                 CanRejectChange(actor, c),
		"startImplementation":    CanStartImplementation(actor, c),
		"completeImplementation": CanCompleteImplementation(actor, c),
	}
}
