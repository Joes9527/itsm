package intake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	itsmservice "itsm-backend/service"
)

type IncidentNumberGenerator interface {
	GenerateIncidentNumber(ctx context.Context, tenantID int) (string, error)
}

type IncidentNumberFunc func(ctx context.Context, tenantID int) (string, error)

func (f IncidentNumberFunc) GenerateIncidentNumber(ctx context.Context, tenantID int) (string, error) {
	return f(ctx, tenantID)
}

type CategoryResolver interface {
	ResolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error)
}

type CategoryResolverFunc func(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error)

func (f CategoryResolverFunc) ResolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error) {
	return f(ctx, client, tenantID, categoryName, subcategoryName)
}

type IncidentExtensionPlan struct {
	IncidentNumber string
	Type           string
	Severity       string
	Impact         string
	Urgency        string
	DetectedAt     time.Time
}

type IncidentCreator struct {
	numbers    IncidentNumberGenerator
	categories CategoryResolver
	matrix     *itsmservice.PriorityMatrixService
	now        func() time.Time
}

func NewIncidentCreator(numbers IncidentNumberGenerator, categories CategoryResolver, matrix *itsmservice.PriorityMatrixService) *IncidentCreator {
	return &IncidentCreator{numbers: numbers, categories: categories, matrix: matrix, now: time.Now}
}

func (c *IncidentCreator) RecordClass() string { return RecordClassIncident }

func (c *IncidentCreator) Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error) {
	if tx == nil {
		return nil, NewInternalFailure("incident transaction is required", nil)
	}
	if in.RecordClass != RecordClassIncident {
		return nil, NewUnsupportedRecordClass("incident creator received another record class", nil)
	}
	if c.numbers == nil {
		return nil, NewInternalFailure("incident number allocator is required", nil)
	}
	number, err := c.numbers.GenerateIncidentNumber(ctx, in.Identity.TenantID)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not allocate incident number", err)
	}
	input := in.Command.Incident
	if input == nil {
		input = &IncidentInput{}
	}
	severity := defaultLevel(input.Severity)
	impact := defaultLevel(input.Impact)
	urgency := defaultLevel(input.Urgency)
	priority := itsmservice.ResolveIncidentPriority(ctx, c.matrix, in.Identity.TenantID, input.ExplicitPriority, impact, urgency)
	detectedAt := c.now().UTC()
	if input.DetectedAt != "" {
		detectedAt, err = time.Parse(time.RFC3339, input.DetectedAt)
		if err != nil {
			return nil, NewDomainValidationFailed("incident detected time is invalid", err)
		}
		detectedAt = detectedAt.UTC()
	}
	incidentType := strings.TrimSpace(input.Type)
	if incidentType == "" {
		incidentType = "incident"
	}
	source := strings.TrimSpace(in.Identity.Channel)
	if in.Command.SourceReference != nil && strings.TrimSpace(in.Command.SourceReference.Provider) != "" {
		source = strings.TrimSpace(in.Command.SourceReference.Provider)
	}
	categoryID := copyInt(in.CTI.CategoryID)
	if categoryID == nil && (strings.TrimSpace(input.Category) != "" || strings.TrimSpace(input.Subcategory) != "") {
		if c.categories == nil {
			return nil, NewInternalFailure("incident category resolver is required", nil)
		}
		categoryID, err = c.categories.ResolveIncidentCategory(ctx, tx.Client(), in.Identity.TenantID, input.Category, input.Subcategory)
		if err != nil {
			return nil, NewDomainValidationFailed("incident category is invalid", err)
		}
	}
	professional := IncidentExtensionPlan{
		IncidentNumber: strings.TrimSpace(number), Type: incidentType, Severity: severity, Impact: impact,
		Urgency: urgency, DetectedAt: detectedAt,
	}
	return &CreationPlan{
		Resolved: in,
		WorkItem: WorkItemDraft{
			TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
			RecordClass: RecordClassIncident, Title: in.Command.Title, Description: in.Command.Description,
			Status: string(common.IncidentStatusNew), Priority: priority, Source: source, CategoryID: categoryID, SLADefinitionID: copyInt(in.SLADefinitionID),
		},
		ProfessionalInput: professional,
	}, nil
}

func (c *IncidentCreator) CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error) {
	if tx == nil || workItem == nil || plan == nil {
		return nil, NewInternalFailure("incident extension transaction, work item, and plan are required", nil)
	}
	input, ok := plan.ProfessionalInput.(IncidentExtensionPlan)
	if !ok {
		return nil, NewDomainValidationFailed("incident creation plan is invalid", nil)
	}
	create := tx.Incident.Create().
		SetWorkItemID(workItem.ID).
		SetType(input.Type).
		SetSeverity(input.Severity).
		SetImpact(input.Impact).
		SetUrgency(input.Urgency).
		SetIncidentNumber(input.IncidentNumber).
		SetDetectedAt(input.DetectedAt).
		SetIsAutomated(false)
	created, err := create.Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create incident extension", err)
	}
	if len(plan.Resolved.CIIDs) > 0 {
		if _, err := created.Update().AddConfigurationItemIDs(plan.Resolved.CIIDs...).Save(ctx); err != nil {
			return nil, NewInfrastructureUnavailable("could not link incident configuration items", err)
		}
	}
	_, err = tx.IncidentEvent.Create().
		SetIncidentID(created.ID).
		SetEventType("creation").
		SetEventName("事件创建").
		SetDescription(fmt.Sprintf("事件 %s 已创建", input.IncidentNumber)).
		SetStatus("active").
		SetSeverity("info").
		SetSource("system").
		SetUserID(plan.WorkItem.ActorID).
		SetOccurredAt(c.now().UTC()).
		SetTenantID(plan.WorkItem.TenantID).
		Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create incident event", err)
	}
	return &ProfessionalReference{Type: "incident", ID: created.ID}, nil
}

func defaultLevel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "medium"
	}
	return value
}
