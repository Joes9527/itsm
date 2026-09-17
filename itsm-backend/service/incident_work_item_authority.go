package service

import (
	"context"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
)

// incidentTenantScope derives tenant and soft-delete visibility exclusively
// from the authoritative WorkItem row.
func incidentTenantScope(tenantID int, extra ...predicate.Ticket) predicate.Incident {
	predicates := []predicate.Ticket{ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()}
	predicates = append(predicates, extra...)
	return incident.HasWorkItemWith(predicates...)
}

func withIncidentWorkItemProjection(query *ent.TicketQuery) {
	query.WithCategory(func(categoryQuery *ent.TicketCategoryQuery) {
		categoryQuery.WithParent()
	})
}

// UpdateClassification 是事件分类的**受控纠正入口**（PUT /incidents/:id/classification），
// 与录入/其它专业域一致地使用分类 ID 契约：只接受最深节点 ID。
//
//   - categoryID > 0：解析为该租户内的路径，校验完整性与启用状态（由 UpdateIncident 内
//     的共享纠正契约完成），并记录前后路径证据；
//   - categoryID == 0：清空分类（事件允许清空，但必须给出原因）；
//   - 任何变化都必须带原因，否则拒绝 —— 不再接受名称解析，避免"空载荷静默成功"。
func (s *IncidentService) UpdateClassification(ctx context.Context, id, tenantID, version, categoryID int, reason string) (*dto.IncidentResponse, error) {
	return s.UpdateIncident(ctx, id, &dto.UpdateIncidentRequest{
		CategoryID:           &categoryID,
		Version:              version,
		ClassificationReason: reason,
	}, tenantID)
}
