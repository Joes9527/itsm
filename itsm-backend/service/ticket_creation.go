package service

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/tickettype"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
)

func (*TicketService) RecordClass() string { return creation.RecordClassGeneric }
func (s *TicketService) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	priority := in.Command.Priority
	if priority == "" {
		priority = "medium"
	}
	switch priority {
	case "low", "medium", "high", "critical", "urgent":
	default:
		return nil, creation.NewDomainValidationFailed("invalid ticket priority", nil)
	}
	plan := creation.NewPlan(in, "new", priority, in.Identity.Channel)
	if input := in.Command.Generic; input != nil {
		plan.WorkItem.GenericSubtype = input.Type
		if input.Source != "" {
			plan.WorkItem.Source = input.Source
		}
		if input.TypeID != "" {
			id, _ := strconv.Atoi(input.TypeID)
			configured, err := tx.TicketType.Query().Where(tickettype.IDEQ(id), tickettype.TenantIDEQ(int64(in.Identity.TenantID)), tickettype.StatusEQ("active")).Only(ctx)
			if ent.IsNotFound(err) {
				return nil, creation.NewReferenceNotFound("ticket type is unavailable", err)
			}
			if err != nil {
				return nil, creation.NewInfrastructureUnavailable("could not resolve ticket type", err)
			}
			if input.Type != "" && input.Type != configured.Code {
				return nil, creation.NewDomainValidationFailed("ticket subtype does not match type definition", nil)
			}
			plan.WorkItem.GenericSubtype = configured.Code
		}
		subtype := plan.WorkItem.GenericSubtype
		switch subtype {
		case "incident", "problem", "change", "change_request", "service_request", "service_request_item", "catalog_task":
			return nil, creation.NewDomainValidationFailed("professional class cannot be a generic subtype", nil)
		case "", "ticket", "improvement":
		default:
			exists, err := tx.TicketType.Query().Where(tickettype.CodeEQ(subtype), tickettype.TenantIDEQ(int64(in.Identity.TenantID)), tickettype.StatusEQ("active")).Exist(ctx)
			if err != nil {
				return nil, creation.NewInfrastructureUnavailable("could not resolve generic subtype", err)
			}
			if !exists {
				return nil, creation.NewDomainValidationFailed("generic subtype is not configured", nil)
			}
		}

	}
	if source := in.Command.FeishuTask; source != nil {
		switch source.Status {
		case "", "not_started", "in_progress", "completed", "canceled", "cancelled":
		default:
			return nil, creation.NewDomainValidationFailed("unsupported initial Feishu task status", nil)
		}
		plan.WorkItem.Source = "feishu"
		plan.WorkItem.Status = mapFeishuStatusToTicket(source.Status, source.Completed)
		plan.WorkflowVariables["status"] = plan.WorkItem.Status
	}
	if in.Command.Email != nil {
		plan.WorkItem.Source = "email"
		plan.WorkItem.ExternalMessageID = in.Command.SourceReference.EventID
		plan.WorkItem.ConversationID = in.Command.SourceReference.ConversationID
		plan.WorkItem.CreatorEmail = in.Command.Email.SenderEmail
	}
	plan.BusinessSubtype = plan.WorkItem.GenericSubtype
	plan.WorkflowVariables["generic_subtype"] = plan.WorkItem.GenericSubtype
	if err := s.prepareCreationEffects(ctx, tx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}
func (s *TicketService) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	if plan.Resolved.Command.Email != nil {
		if err := writeEmailCreationSource(ctx, tx, item, plan); err != nil {
			return nil, err
		}
	}
	if source := plan.Resolved.Command.FeishuTask; source != nil {
		if err := writeFeishuCreationSource(ctx, tx, item, source); err != nil {
			return nil, err
		}
	}
	if err := s.writeCreationEffects(ctx, tx, item, plan); err != nil {
		return nil, err
	}
	return &creation.ProfessionalReference{}, nil
}

var _ creation.ProfessionalCreator = (*TicketService)(nil)
