package service_request

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/servicerequestaccessresult"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/service"
)

// ReadFulfillment receives the already authorized, current WorkItem from A6.
// SR owns professional cancellation and verified access results; BPMN owns
// approval and delegation progress. No transport status proves external grant.
func (s *Service) ReadFulfillment(ctx context.Context, client *ent.Client, item *ent.Ticket) (accessgrant.Fulfillment, error) {
	result := accessgrant.Fulfillment{State: "unknown"}
	if item == nil || item.RecordClass != "service_request_item" {
		return result, nil
	}
	row, err := client.ServiceRequestAccessResult.Query().Where(servicerequestaccessresult.WorkItemIDEQ(item.ID), servicerequestaccessresult.HasWorkItemWith(ticket.TenantIDEQ(item.TenantID), ticket.DeletedAtIsNil())).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return result, err
	}
	if row != nil {
		// Storage projection still fails closed if legacy/manual data is inconsistent.
		if row.VerifiedAt.IsZero() || row.EvidenceRef == "" || row.SubjectID == "" || row.GroupID == "" {
			return result, nil
		}
		managed := row.Outcome == "granted" && row.Baseline == "not_member" && row.ExpiresAt != nil && row.ExpiresAt.After(row.VerifiedAt)
		existing := row.Outcome == "already_present" && row.Baseline == "member" && row.ExpiresAt == nil
		if !managed && !existing {
			return result, nil
		}
		return accessgrant.Fulfillment{State: "completed", AccessResult: &accessgrant.View{Outcome: string(row.Outcome), VerifiedAt: row.VerifiedAt.UTC(), ExpiresAt: row.ExpiresAt, Managed: managed}}, nil
	}
	if item.Status == "cancelled" {
		result.State = "cancelled"
		return result, nil
	}
	if item.Status == "rejected" {
		result.State = "rejected"
		return result, nil
	}
	progress, err := service.ReadWorkflowFulfillment(ctx, client, item.TenantID, item.ID)
	if err != nil {
		return result, err
	}
	result.State = progress.State
	return result, nil
}
