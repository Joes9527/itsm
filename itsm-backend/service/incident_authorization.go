package service

import (
	"context"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/middleware"
)

func canWriteIncident(actor ActionActor) bool {
	return middleware.HasResourcePermission(actor.Client, actor.Role, "incident", "write", actor.TenantID)
}

func CanEditIncident(actor ActionActor) dto.ActionPermission {
	if !canWriteIncident(actor) {
		return dto.ActionPermission{Allowed: false, Reason: "无权限编辑事件"}
	}
	return dto.ActionPermission{Allowed: true}
}

func CanResolveIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.Status != common.IncidentStatusInProgress {
		return dto.ActionPermission{Allowed: false, Reason: "只有处理中的事件可以解决"}
	}
	return CanEditIncident(actor)
}

func CanCloseIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.Status != common.IncidentStatusResolved {
		return dto.ActionPermission{Allowed: false, Reason: "只有已解决的事件可以关闭"}
	}
	return CanEditIncident(actor)
}

func CanReopenIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.Status != common.IncidentStatusResolved && incident.Status != common.IncidentStatusClosed {
		return dto.ActionPermission{Allowed: false, Reason: "只有已解决或已关闭的事件可以重新打开"}
	}
	return CanEditIncident(actor)
}

func CanEscalateIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.Status == common.IncidentStatusClosed || incident.Status == common.IncidentStatusCancelled {
		return dto.ActionPermission{Allowed: false, Reason: "终态事件不能升级"}
	}
	return CanEditIncident(actor)
}

func CanAssignIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if !canAssignIncidentStatus(incident.Status) {
		return dto.ActionPermission{Allowed: false, Reason: "已解决或已关闭的事件不能重新指派"}
	}
	return CanEditIncident(actor)
}

func CanMarkMajorIncident(actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.IsMajorIncident {
		return dto.ActionPermission{Allowed: false, Reason: "已经是重大事件"}
	}
	if incident.Status == common.IncidentStatusResolved || incident.Status == common.IncidentStatusClosed {
		return dto.ActionPermission{Allowed: false, Reason: "已解决或已关闭的事件不能标记为重大事件"}
	}
	return CanEditIncident(actor)
}

func hasIncidentProblemRelation(ctx context.Context, actor ActionActor, incident *ent.Incident) (bool, error) {
	if actor.Client == nil {
		return false, fmt.Errorf("incident relation client is unavailable")
	}
	if incident.WorkItemID <= 0 {
		return false, fmt.Errorf("incident WorkItem is missing")
	}
	_, err := actor.Client.Ticket.Query().
		Where(
			ticket.IDEQ(incident.WorkItemID),
			ticket.TenantIDEQ(actor.TenantID),
			ticket.RecordClassEQ("incident"),
			ticket.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		return false, fmt.Errorf("validate incident source WorkItem: %w", err)
	}
	return actor.Client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantIDEQ(actor.TenantID),
			workitemrelation.SourceWorkItemIDEQ(incident.WorkItemID),
			workitemrelation.RelationTypeEQ("investigated_by"),
			workitemrelation.DeletedAtIsNil(),
		).
		Exist(ctx)
}

func CanConvertToProblem(ctx context.Context, actor ActionActor, incident *ent.Incident) dto.ActionPermission {
	if incident.Status == common.IncidentStatusClosed {
		return dto.ActionPermission{Allowed: false, Reason: "已关闭的事件不能转为问题"}
	}
	converted, err := hasIncidentProblemRelation(ctx, actor, incident)
	if err != nil {
		return dto.ActionPermission{Allowed: false, Reason: "无法确认事件是否已转为问题"}
	}
	if converted {
		return dto.ActionPermission{Allowed: false, Reason: "已经转为问题"}
	}
	return CanEditIncident(actor)
}

func BuildIncidentActions(ctx context.Context, actor ActionActor, incident *ent.Incident) map[string]dto.ActionPermission {
	return map[string]dto.ActionPermission{
		"edit":                CanEditIncident(actor),
		"resolve":             CanResolveIncident(actor, incident),
		"close":               CanCloseIncident(actor, incident),
		"reopen":              CanReopenIncident(actor, incident),
		"escalate":            CanEscalateIncident(actor, incident),
		"assign":              CanAssignIncident(actor, incident),
		"mark_major_incident": CanMarkMajorIncident(actor, incident),
		"convert_to_problem":  CanConvertToProblem(ctx, actor, incident),
	}
}
