package problem

import (
	"context"
	"strings"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent/user"

	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/service"
)

func canWriteProblem(actor service.ActionActor) bool {
	return authorization.HasResourcePermission(actor.Client, actor.Role, "problem", "write", actor.TenantID)
}

func CanEditProblem(actor service.ActionActor) dto.ActionPermission {
	if !canWriteProblem(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限编辑问题"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanStartInvestigation(actor service.ActionActor, p *Problem) dto.ActionPermission {
	status := strings.TrimSpace(p.Status)
	if status != "open" && status != "identified" && status != "in_progress" {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态的问题不能开始调查"}
	}
	return CanEditProblem(actor)
}

func CanResolveProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	status := strings.TrimSpace(p.Status)
	if status == "resolved" || !isValidProblemStatusTransition(status, "resolved") {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态的问题不能标记解决"}
	}
	if err := ValidateResolution(ResolutionEvidence{p.RootCause, p.Resolution, CurrentResolutionVerification(p.RootCause, p.Resolution, p.VerificationDigest, p.VerificationNote, p.VerifiedBy, p.VerifiedVersion, p.VerifiedAt), p.VerificationNote}); err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "请先验证当前根因与永久解决方案"}
	}
	return CanEditProblem(actor)
}

func CanCloseProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if !canCloseProblemStatus(p.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "只有已解决的问题可以关闭"}
	}
	if err := ValidateResolution(ResolutionEvidence{p.RootCause, p.Resolution, CurrentResolutionVerification(p.RootCause, p.Resolution, p.VerificationDigest, p.VerificationNote, p.VerifiedBy, p.VerifiedVersion, p.VerifiedAt), p.VerificationNote}); err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "缺少有效的永久方案验证，请重新打开后验证"}
	}
	return CanEditProblem(actor)
}

func BuildProblemActions(actor service.ActionActor, p *Problem) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"assign":             CanAssignProblem(actor, p),
		"verifyResolution":   CanVerifyProblem(actor, p),
		"reopen":             CanReopenProblem(actor, p),
		"edit":               CanEditProblem(actor),
		"startInvestigation": CanStartInvestigation(actor, p),
		"resolve":            CanResolveProblem(actor, p),
		"close":              CanCloseProblem(actor, p),
	}
}

func CanVerifyProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if p.Status != "investigating" && p.Status != "identified" && p.Status != "in_progress" {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态不能验证方案"}
	}
	if strings.TrimSpace(p.RootCause) == "" || strings.TrimSpace(p.Resolution) == "" {
		return dto.ActionPermission{Allowed: false, Reason: "请先记录根因并选定永久方案"}
	}
	return CanEditProblem(actor)
}

func CanReopenProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if p.Status != "resolved" && p.Status != "closed" {
		return dto.ActionPermission{Allowed: false, Reason: "仅已解决或关闭的问题可以重新打开"}
	}
	return CanEditProblem(actor)
}

func CanAssignProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if !canAssignProblemStatus(p.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态不允许分派问题"}
	}
	permission := CanEditProblem(actor)
	if !permission.Allowed {
		return permission
	}
	if actor.Client == nil {
		return dto.ActionPermission{Allowed: false, Reason: "负责人目录不可用"}
	}
	query := actor.Client.User.Query().Where(user.TenantID(actor.TenantID), user.Active(true))
	if p.AssigneeID != nil {
		query.Where(user.IDNEQ(*p.AssigneeID))
	}
	available, err := query.Exist(tenantctx.WithTenantID(context.Background(), actor.TenantID))
	if err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "负责人目录不可用"}
	}
	if !available {
		return dto.ActionPermission{Allowed: false, Reason: "没有可用的目标负责人"}
	}
	return permission
}
