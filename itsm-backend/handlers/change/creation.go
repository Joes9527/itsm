package change

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/standardchange"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"sort"
	"strconv"
	"strings"
	"time"
)

type changeCreation struct {
	Input      creation.ChangeInput
	Start, End *time.Time
}

func (*Service) RecordClass() string { return creation.RecordClassChangeRequest }
func (s *Service) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	input := creation.ChangeInput{}
	if in.Command.Change != nil {
		input = *in.Command.Change
	}
	if input.StandardTemplateID != nil {
		template, err := tx.StandardChange.Query().Where(standardchange.IDEQ(*input.StandardTemplateID), standardchange.TenantIDEQ(in.Identity.TenantID), standardchange.IsActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, creation.NewReferenceNotFound("standard change template is unavailable", nil)
		}
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not resolve standard change template", err)
		}
		if in.Command.Title == "" {
			in.Command.Title = template.Title
		}
		in.Command.Description = template.Description
		input.Type = "standard"
		input.Justification = template.Justification
		input.ImplementationPlan = template.ImplementationPlan
		input.RollbackPlan = template.RollbackPlan
		input.RiskLevel = template.RiskLevel
		input.ImpactScope = template.ImpactScope
		if len(input.AffectedCIs) == 0 {
			input.AffectedCIs = append([]string(nil), template.AffectedCis...)
		}
		ids := []int{}
		for _, raw := range input.AffectedCIs {
			id, err := strconv.Atoi(raw)
			if err != nil || id <= 0 {
				return nil, creation.NewDomainValidationFailed("standard change template has invalid affected CI", nil)
			}
			ids = append(ids, id)
		}
		cis, err := service.NewConfigurationItemService(tx.Client(), s.logger, nil, nil).ResolveCreationCIs(ctx, tx, in.Identity, ids, nil)
		if err != nil {
			return nil, err
		}
		in.ConfigurationItems = cis
		in.CIIDs = nil
		for _, ci := range cis {
			in.CIIDs = append(in.CIIDs, ci.ID)
		}
	}
	if len(input.RelatedTicketNumbers) > 0 {
		if err := authorization.RequireCurrentPermission(ctx, tx, in.Identity, "ticket", "read"); err != nil {
			return nil, err
		}
		distinctNumbers := map[string]bool{}
		for _, number := range input.RelatedTicketNumbers {
			distinctNumbers[number] = true
		}
		rows, err := tx.Ticket.Query().Where(ticket.TenantIDEQ(in.Identity.TenantID), ticket.DeletedAtIsNil(), ticket.TicketNumberIn(input.RelatedTicketNumbers...)).All(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not resolve related work item numbers", err)
		}
		if len(rows) != len(distinctNumbers) {
			return nil, creation.NewReferenceNotFound("related work item number is unavailable", nil)
		}
		ids := map[int]bool{}
		for _, id := range input.RelatedTickets {
			ids[id] = true
		}
		for _, row := range rows {
			ids[row.ID] = true
		}
		input.RelatedTickets = nil
		for id := range ids {
			input.RelatedTickets = append(input.RelatedTickets, id)
		}
		sort.Ints(input.RelatedTickets)
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
	// Required professional fields are checked after authoritative standard-template
	// expansion. Storage defaults support historical records, not incomplete intake.
	for _, field := range []struct{ name, value string }{
		{"justification", input.Justification}, {"impactScope", input.ImpactScope},
		{"riskLevel", input.RiskLevel}, {"implementationPlan", input.ImplementationPlan},
		{"rollbackPlan", input.RollbackPlan},
	} {
		if strings.TrimSpace(field.value) == "" {
			return nil, creation.NewDomainValidationFailed("change."+field.name+" is required", nil)
		}
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
	plan.BusinessSubtype = input.Type
	for key, value := range map[string]any{"change_type": input.Type, "justification": input.Justification, "risk_level": input.RiskLevel, "impact_scope": input.ImpactScope, "implementation_plan": input.ImplementationPlan, "rollback_plan": input.RollbackPlan, "planned_start_date": start, "planned_end_date": end, "affected_cis": input.AffectedCIs, "related_tickets": input.RelatedTickets} {
		plan.WorkflowVariables[key] = value
	}
	plan.RoutingValues = map[string]any{"riskLevel": input.RiskLevel, "impactScope": input.ImpactScope}
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
	plan.WorkflowVariables["change_id"] = record.ID
	return &creation.ProfessionalReference{Type: "change", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)
