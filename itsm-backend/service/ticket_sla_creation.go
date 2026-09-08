package service

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// ApplyCreationSLA persists both deadlines with the owning creation transaction.
// This path never fabricates a missing configured SLA definition.
func (s *TicketSLAService) ApplyCreationSLA(ctx context.Context, tx *ent.Tx, item *ent.Ticket, definitionID *int) error {
	if definitionID == nil {
		return nil
	}
	definition, err := tx.SLADefinition.Query().Where(sladefinition.IDEQ(*definitionID), sladefinition.TenantIDEQ(item.TenantID), sladefinition.IsActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return creation.NewReferenceNotFound("configured SLA is unavailable", err)
	}
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load creation SLA", err)
	}
	response, err := s.calculateDeadlineWithBusinessHours(item.CreatedAt, definition.ResponseTime, definition.BusinessHours)
	if err != nil {
		return creation.NewDomainValidationFailed("invalid SLA calendar", err)
	}
	resolution, err := s.calculateDeadlineWithBusinessHours(item.CreatedAt, definition.ResolutionTime, definition.BusinessHours)
	if err != nil {
		return creation.NewDomainValidationFailed("invalid SLA calendar", err)
	}
	_, err = tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantIDEQ(item.TenantID)).SetSLADefinitionID(definition.ID).SetSLAResponseDeadline(response).SetSLAResolutionDeadline(resolution).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not persist creation SLA", err)
	}
	return nil
}
