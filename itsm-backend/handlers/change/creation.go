package change

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"time"
)

type changeCreation struct {
	Input      creation.ChangeInput
	Start, End *time.Time
}

func (*Service) RecordClass() string { return creation.RecordClassChangeRequest }
func (*Service) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	input := creation.ChangeInput{}
	if in.Command.Change != nil {
		input = *in.Command.Change
	}
	if len(in.CIIDs) > 0 {
		input.AffectedCIs = nil
		for _, id := range in.CIIDs {
			input.AffectedCIs = append(input.AffectedCIs, strconv.Itoa(id))
		}
	}
	if input.Type == "" {
		input.Type = "normal"
	}
	if input.ImpactScope == "" {
		input.ImpactScope = "medium"
	}
	if input.RiskLevel == "" {
		input.RiskLevel = "medium"
	}
	switch input.Type {
	case "normal", "standard", "emergency":
	default:
		return nil, creation.NewDomainValidationFailed("invalid change type", nil)
	}
	for _, value := range []string{input.ImpactScope, input.RiskLevel} {
		switch value {
		case "low", "medium", "high":
		default:
			return nil, creation.NewDomainValidationFailed("invalid change risk or impact", nil)
		}
	}
	priority := in.Command.Priority
	if priority == "" {
		priority = "medium"
	}
	switch priority {
	case "low", "medium", "high", "critical":
	default:
		return nil, creation.NewDomainValidationFailed("invalid change priority", nil)
	}
	start, err := creation.ParseOptionalTime(input.PlannedStartDate, "change.plannedStartDate")
	if err != nil {
		return nil, err
	}
	end, err := creation.ParseOptionalTime(input.PlannedEndDate, "change.plannedEndDate")
	if err != nil {
		return nil, err
	}
	if start != nil && end != nil && !end.After(*start) {
		return nil, creation.NewDomainValidationFailed("change end must follow start", nil)
	}
	if len(input.RelatedTickets) > 0 {
		if err := authorization.RequireCurrentPermission(ctx, tx, in.Identity, "ticket", "read"); err != nil {
			return nil, err
		}
		count, err := tx.Ticket.Query().Where(ticket.IDIn(input.RelatedTickets...), ticket.TenantIDEQ(in.Identity.TenantID), ticket.DeletedAtIsNil()).Count(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not resolve related work items", err)
		}
		if count != len(input.RelatedTickets) {
			return nil, creation.NewReferenceNotFound("related work item is unavailable", nil)
		}
	}
	plan := creation.NewPlan(in, "draft", priority, in.Identity.Channel)
	plan.ProfessionalInput = changeCreation{Input: input, Start: start, End: end}
	return plan, nil
}
func (*Service) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	prepared, ok := plan.ProfessionalInput.(changeCreation)
	if !ok {
		return nil, creation.NewInternalFailure("change creation plan is invalid", nil)
	}
	input := prepared.Input
	record, err := tx.Change.Create().SetWorkItemID(item.ID).SetJustification(input.Justification).SetType(input.Type).
		SetImpactScope(input.ImpactScope).SetRiskLevel(input.RiskLevel).SetImplementationPlan(input.ImplementationPlan).
		SetRollbackPlan(input.RollbackPlan).SetNillablePlannedStartDate(prepared.Start).SetNillablePlannedEndDate(prepared.End).
		SetAffectedCis(input.AffectedCIs).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create change extension", err)
	}
	for _, target := range input.RelatedTickets {
		_, err = tx.WorkItemRelation.Create().SetTenantID(item.TenantID).SetSourceWorkItemID(item.ID).SetTargetWorkItemID(target).
			SetRelationType(changeTicketRelationType).SetCreatedByID(plan.Resolved.Identity.ActorID).Save(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not create change relation", err)
		}
	}
	return &creation.ProfessionalReference{Type: "change", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)
