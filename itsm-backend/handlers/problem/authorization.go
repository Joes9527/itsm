package problem

import (
	"strings"

	"itsm-backend/dto"
	"itsm-backend/middleware"
	"itsm-backend/service"
)

func canWriteProblem(actor service.ActionActor) bool {
	return middleware.HasResourcePermission(actor.Client, actor.Role, "problem", "write", actor.TenantID)
}

func CanEditProblem(actor service.ActionActor) dto.ActionPermission {
	if !canWriteProblem(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限编辑问题"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanStartInvestigation(actor service.ActionActor, p *Problem) dto.ActionPermission {
	status := strings.TrimSpace(p.Status)
	if status == "investigating" || !isValidProblemStatusTransition(status, "investigating") {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态的问题不能开始调查"}
	}
	return CanEditProblem(actor)
}

func CanResolveProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	status := strings.TrimSpace(p.Status)
	if status == "resolved" || !isValidProblemStatusTransition(status, "resolved") {
		return dto.ActionPermission{Allowed: false, Reason: "当前状态的问题不能标记解决"}
	}
	return CanEditProblem(actor)
}

func CanCloseProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if !canCloseProblemStatus(p.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "只有已解决的问题可以关闭"}
	}
	return CanEditProblem(actor)
}

func BuildProblemActions(actor service.ActionActor, p *Problem) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"edit":               CanEditProblem(actor),
		"startInvestigation": CanStartInvestigation(actor, p),
		"resolve":            CanResolveProblem(actor, p),
		"close":              CanCloseProblem(actor, p),
	}
}
