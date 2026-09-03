package intake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/repository/workitemnumber"
)

type WorkItemCreator struct {
	numbers workitemnumber.Allocator
	now     func() time.Time
}

func NewWorkItemCreator(numbers workitemnumber.Allocator) *WorkItemCreator {
	return &WorkItemCreator{numbers: numbers, now: time.Now}
}

func (c *WorkItemCreator) CreateBase(ctx context.Context, tx *ent.Tx, plan *CreationPlan) (*ent.Ticket, error) {
	if tx == nil || plan == nil {
		return nil, NewInternalFailure("work item transaction and creation plan are required", nil)
	}
	draft := &plan.WorkItem
	if draft.TenantID <= 0 || draft.ActorID <= 0 || draft.RequesterID <= 0 || strings.TrimSpace(draft.Title) == "" {
		return nil, NewDomainValidationFailed("work item draft is incomplete", nil)
	}
	if draft.RecordClass != RecordClassIncident && draft.RecordClass != RecordClassServiceRequestItem && draft.RecordClass != RecordClassChangeRequest {
		return nil, NewUnsupportedRecordClass("work item record class is unsupported", nil)
	}
	if draft.TicketNumber == "" {
		if c.numbers == nil {
			return nil, NewInternalFailure("work item number allocator is required", nil)
		}
		issuedAt := c.now().UTC()
		number, err := c.numbers.Allocate(ctx, tx.Client(), draft.TenantID, issuedAt)
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
	switch draft.RecordClass {
	case RecordClassIncident:
		legacyType = "incident"
	case RecordClassChangeRequest:
		legacyType = "change"
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
	if draft.AssigneeID != nil {
		create.SetAssigneeID(*draft.AssigneeID)
	}
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
