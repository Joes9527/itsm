package service

import (
	"context"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
)

// Legacy Ticket assignment must not mutate the Incident owner command's fields.
func rejectIncidentTicketAssignment(recordClass string) error {
	if recordClass == "incident" {
		return common.NewValidationError("Incident assignment requires the Incident command", nil)
	}
	return nil
}

// Validate the whole batch before any assignment, including mixed-class batches.
func validateTicketAssignmentClasses(ctx context.Context, client *ent.Client, tenantID int, ids []int) error {
	items, err := client.Ticket.Query().Where(ticket.IDIn(ids...), ticket.TenantID(tenantID)).All(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := rejectIncidentTicketAssignment(item.RecordClass); err != nil {
			return err
		}
	}
	return nil
}
