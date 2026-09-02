package intake

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
)

type WorkItemNumberAllocator interface {
	GenerateWorkItemNumber(ctx context.Context, tenantID int) (string, error)
}

type WorkItemNumberFunc func(ctx context.Context, tenantID int) (string, error)

func (f WorkItemNumberFunc) GenerateWorkItemNumber(ctx context.Context, tenantID int) (string, error) {
	return f(ctx, tenantID)
}

type WorkItemCreator struct {
	numbers WorkItemNumberAllocator
}

func NewWorkItemCreator(numbers WorkItemNumberAllocator) *WorkItemCreator {
	return &WorkItemCreator{numbers: numbers}
}

func (c *WorkItemCreator) CreateBase(ctx context.Context, tx *ent.Tx, plan *CreationPlan) (*ent.Ticket, error) {
	if tx == nil || plan == nil {
		return nil, NewInternalFailure("work item transaction and creation plan are required", nil)
	}
	draft := &plan.WorkItem
	if draft.TenantID <= 0 || draft.ActorID <= 0 || draft.RequesterID <= 0 || strings.TrimSpace(draft.Title) == "" {
		return nil, NewDomainValidationFailed("work item draft is incomplete", nil)
	}
	if draft.RecordClass != RecordClassIncident && draft.RecordClass != RecordClassServiceRequestItem {
		return nil, NewUnsupportedRecordClass("work item record class is unsupported", nil)
	}
	if draft.TicketNumber == "" {
		if c.numbers == nil {
			return nil, NewInternalFailure("work item number allocator is required", nil)
		}
		number, err := c.numbers.GenerateWorkItemNumber(ctx, draft.TenantID)
		if err != nil {
			return nil, NewInfrastructureUnavailable("could not allocate work item number", err)
		}
		draft.TicketNumber = strings.TrimSpace(number)
	}
	if draft.TicketNumber == "" {
		return nil, NewInfrastructureUnavailable("work item number allocator returned an empty number", nil)
	}
	if draft.Priority == "" {
		draft.Priority = "medium"
	}
	if draft.Status == "" {
		draft.Status = "open"
	}
	if draft.Source == "" {
		draft.Source = "manual"
	}
	legacyType := "service_request"
	if draft.RecordClass == RecordClassIncident {
		legacyType = "incident"
	}
	create := tx.Ticket.Create().
		SetTitle(strings.TrimSpace(draft.Title)).
		SetDescription(strings.TrimSpace(draft.Description)).
		SetStatus(draft.Status).
		SetType(legacyType).
		SetRecordClass(draft.RecordClass).
		SetPriority(draft.Priority).
		SetSource(draft.Source).
		SetTicketNumber(draft.TicketNumber).
		SetRequesterID(draft.RequesterID).
		SetOpenedByID(draft.ActorID).
		SetTenantID(draft.TenantID)
	if draft.CategoryID != nil {
		create.SetCategoryID(*draft.CategoryID)
	}
	if draft.SLADefinitionID != nil {
		create.SetSLADefinitionID(*draft.SLADefinitionID)
	}
	created, err := create.Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create work item", fmt.Errorf("create ticket: %w", err))
	}
	return created, nil
}
