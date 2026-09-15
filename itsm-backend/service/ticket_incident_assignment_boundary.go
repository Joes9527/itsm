package service

import (
	"context"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
)

// Core fields of professional WorkItems are writable only through their domain commands.
// Collaboration (comments, attachments, CC and forwarding without ownership) remains shared.
func rejectProfessionalTicketMutation(recordClass string) error {
	switch recordClass {
	case dto.RecordClassIncident, dto.RecordClassProblem, dto.RecordClassChangeRequest:
		return common.NewValidationError(fmt.Sprintf("%s writes require the owning domain command", recordClass), nil)
	}
	return nil
}

// Validate the whole batch before any core mutation, including mixed-class batches.
func validateTicketMutationClasses(ctx context.Context, client *ent.Client, tenantID int, ids []int) error {
	items, err := client.Ticket.Query().Where(ticket.IDIn(ids...), ticket.TenantID(tenantID)).All(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := rejectProfessionalTicketMutation(item.RecordClass); err != nil {
			return err
		}
	}
	return nil
}
