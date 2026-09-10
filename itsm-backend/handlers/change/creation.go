package change

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/standardchange"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"strconv"
	"strings"
	"time"
)

type changeCreation struct {
	Input      creation.ChangeInput
	Start, End *time.Time
	Policy     map[string]any
}

func (*Service) RecordClass() string { return creation.RecordClassChangeRequest }
func (s *Service) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	input := creation.ChangeInput{}
	var policy map[string]any
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
		policy = map[string]any{"templateId": template.ID, "tenantId": template.TenantID, "updatedAt": template.UpdatedAt.UTC().Format(time.RFC3339Nano), "active": template.IsActive, "approvalRequired": template.ApprovalRequired, "riskLevel": template.RiskLevel, "impactScope": template.ImpactScope, "implementationPlan": template.ImplementationPlan, "rollbackPlan": template.RollbackPlan, "prerequisites": template.Prerequisites, "affectedCis": append([]string(nil), template.AffectedCis...)}
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
	plan := creation.NewPlan(in, "draft", priority, in.Identity.Channel)
	plan.BusinessSubtype = input.Type
	for key, value := range map[string]any{"change_type": input.Type, "justification": input.Justification, "risk_level": input.RiskLevel, "impact_scope": input.ImpactScope, "implementation_plan": input.ImplementationPlan, "rollback_plan": input.RollbackPlan, "planned_start_date": start, "planned_end_date": end, "affected_cis": input.AffectedCIs} {
		plan.WorkflowVariables[key] = value
	}
	plan.RoutingValues = map[string]any{"riskLevel": input.RiskLevel, "impactScope": input.ImpactScope}
	plan.ProfessionalInput = changeCreation{Input: input, Start: start, End: end, Policy: policy}
	return plan, nil
}
func (*Service) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	prepared, ok := plan.ProfessionalInput.(changeCreation)
	if !ok {
		return nil, creation.NewInternalFailure("change creation plan is invalid", nil)
	}
	input := prepared.Input
	builder := tx.Change.Create().SetWorkItemID(item.ID).SetJustification(input.Justification).SetType(input.Type).
		SetImpactScope(input.ImpactScope).SetRiskLevel(input.RiskLevel).SetImplementationPlan(input.ImplementationPlan).
		SetRollbackPlan(input.RollbackPlan).SetNillablePlannedStartDate(prepared.Start).SetNillablePlannedEndDate(prepared.End).
		SetAffectedCis(input.AffectedCIs)
	if input.StandardTemplateID != nil {
		builder.SetStandardTemplateID(*input.StandardTemplateID).SetStandardPolicy(prepared.Policy)
	}
	record, err := builder.Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create change extension", err)
	}
	plan.WorkflowVariables["change_id"] = record.ID
	return &creation.ProfessionalReference{Type: "change", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)
