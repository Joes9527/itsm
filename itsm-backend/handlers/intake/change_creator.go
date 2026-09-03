package intake

import (
	"context"
	"strings"
	"time"

	"itsm-backend/ent"
)

type ChangeExtensionPlan struct {
	Justification      string
	Type               string
	ImpactScope        string
	RiskLevel          string
	PlannedStartDate   *time.Time
	PlannedEndDate     *time.Time
	ImplementationPlan string
	RollbackPlan       string
	AffectedCIs        []string
}

type ChangeCreator struct{}

func NewChangeCreator() *ChangeCreator { return &ChangeCreator{} }

func (c *ChangeCreator) RecordClass() string { return RecordClassChangeRequest }

func (c *ChangeCreator) Prepare(_ context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error) {
	if tx == nil {
		return nil, NewInternalFailure("change transaction is required", nil)
	}
	if in.RecordClass != RecordClassChangeRequest {
		return nil, NewUnsupportedRecordClass("change creator received another record class", nil)
	}
	change := in.Command.Change
	if change == nil {
		change = &ChangeInput{}
	}
	plannedStart, err := parseOptionalTime(change.PlannedStartDate)
	if err != nil {
		return nil, NewDomainValidationFailed("plannedStartDate is invalid", err)
	}
	plannedEnd, err := parseOptionalTime(change.PlannedEndDate)
	if err != nil {
		return nil, NewDomainValidationFailed("plannedEndDate is invalid", err)
	}
	return &CreationPlan{
		Resolved: in,
		WorkItem: WorkItemDraft{
			TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
			RecordClass: RecordClassChangeRequest, Title: in.Command.Title, Description: in.Command.Description,
			SLADefinitionID: copyInt(in.SLADefinitionID),
		},
		ProfessionalInput: ChangeExtensionPlan{
			Justification:      strings.TrimSpace(change.Justification),
			Type:               defaultString(change.Type, "normal"),
			ImpactScope:        defaultString(change.ImpactScope, "medium"),
			RiskLevel:          defaultString(change.RiskLevel, "medium"),
			PlannedStartDate:   plannedStart,
			PlannedEndDate:     plannedEnd,
			ImplementationPlan: strings.TrimSpace(change.ImplementationPlan),
			RollbackPlan:       strings.TrimSpace(change.RollbackPlan),
			AffectedCIs:        append([]string(nil), change.AffectedCIs...),
		},
	}, nil
}

func (c *ChangeCreator) CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error) {
	if tx == nil || workItem == nil || plan == nil {
		return nil, NewInternalFailure("change extension transaction, work item, and plan are required", nil)
	}
	input, ok := plan.ProfessionalInput.(ChangeExtensionPlan)
	if !ok {
		return nil, NewDomainValidationFailed("change creation plan is invalid", nil)
	}
	created, err := tx.Change.Create().
		SetJustification(input.Justification).
		SetType(input.Type).
		SetImpactScope(input.ImpactScope).
		SetRiskLevel(input.RiskLevel).
		SetWorkItemID(workItem.ID).
		SetImplementationPlan(input.ImplementationPlan).
		SetRollbackPlan(input.RollbackPlan).
		SetNillablePlannedStartDate(input.PlannedStartDate).
		SetNillablePlannedEndDate(input.PlannedEndDate).
		SetAffectedCis(input.AffectedCIs).
		Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create change extension", err)
	}
	return &ProfessionalReference{Type: "change", ID: created.ID}, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func parseOptionalTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
