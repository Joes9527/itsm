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
	if strings.TrimSpace(p.Status) != "open" {
		return dto.ActionPermission{Allowed: false, Reason: "只有待处理的问题可以开始调查"}
	}
	return CanEditProblem(actor)
}

func CanResolveProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	switch strings.TrimSpace(p.Status) {
	case "investigating", "identified", "in_progress":
		return CanEditProblem(actor)
	default:
		return dto.ActionPermission{Allowed: false, Reason: "只有调查中或已识别的问题可以标记解决"}
	}
}

func CanCloseProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
	if !canCloseProblemStatus(p.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "只有已解决的问题可以关闭"}
	}
	return CanEditProblem(actor)
}

func BuildProblemActions(actor service.ActionActor, p *Problem) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"edit":                CanEditProblem(actor),
		"start_investigation": CanStartInvestigation(actor, p),
		"resolve":             CanResolveProblem(actor, p),
		"close":               CanCloseProblem(actor, p),
	}
}
