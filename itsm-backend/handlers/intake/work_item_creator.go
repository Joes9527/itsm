package intake

import (
	"context"
	"fmt"
	"itsm-backend/handlers/common/workitemcreation"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/group"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/user"
	"itsm-backend/repository/workitemnumber"
)

type WorkItemCreator struct {
	numbers workitemnumber.Allocator
	now     func() time.Time
}

func NewWorkItemCreator(numbers workitemnumber.Allocator) *WorkItemCreator {
	return &WorkItemCreator{numbers: numbers, now: time.Now}
}

func (c *WorkItemCreator) CreateBase(ctx context.Context, tx *ent.Tx, plan *workitemcreation.CreationPlan, authorized *authorization.CreationAuthorization) (*ent.Ticket, error) {
	if tx == nil || plan == nil {
		return nil, workitemcreation.NewInternalFailure("work item transaction and creation plan are required", nil)
	}
	if err := authorized.Validate(tx, plan.Resolved.Identity); err != nil {
		return nil, err
	}
	draft := &plan.WorkItem
	identity := authorized.Identity()
	if draft.ActorID != identity.ActorID || draft.TenantID != identity.TenantID || draft.RequesterID != identity.RequesterID {
		return nil, workitemcreation.NewPermissionDenied("draft differs from authorized identity", nil)
	}
	if draft.TenantID <= 0 || draft.ActorID <= 0 || draft.RequesterID <= 0 || strings.TrimSpace(draft.Title) == "" {
		return nil, workitemcreation.NewDomainValidationFailed("work item draft is incomplete", nil)
	}
	if !workitemcreation.IsSupportedRecordClass(draft.RecordClass) {
		return nil, workitemcreation.NewUnsupportedRecordClass("unsupported prepared class", nil)
	}
	if strings.TrimSpace(draft.Status) == "" || strings.TrimSpace(draft.Priority) == "" || strings.TrimSpace(draft.Source) == "" || draft.TicketNumber != "" {
		return nil, workitemcreation.NewDomainValidationFailed("prepared status, priority and source are required; numbers are allocated internally", nil)
	}
	if err := validateDraftReferences(ctx, tx, draft); err != nil {
		return nil, err
	}
	if c == nil || missingDependency(c.numbers) {
		return nil, workitemcreation.NewInternalFailure("number allocator is required", nil)
	}
	number, err := c.numbers.Allocate(ctx, tx.Client(), draft.TenantID, c.now().UTC())
	if err != nil {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not allocate work item number", err)
	}
	if strings.TrimSpace(number) == "" {
		return nil, workitemcreation.NewInfrastructureUnavailable("allocator returned empty number", nil)
	}
	create := tx.Ticket.Create().
		SetTitle(strings.TrimSpace(draft.Title)).
		SetDescription(strings.TrimSpace(draft.Description)).
		SetStatus(draft.Status).
		SetRecordClass(draft.RecordClass).
		SetType("").
		SetGenericSubtype(draft.GenericSubtype).
		SetNillableTemplateID(draft.TemplateID).
		SetNillableParentTicketID(draft.ParentTicketID).
		AddTagIDs(draft.TagIDs...).
		SetPriority(draft.Priority).
		SetSource(draft.Source).
		SetTicketNumber(number).
		SetRequesterID(draft.RequesterID).
		SetOpenedByID(draft.ActorID).
		SetTenantID(draft.TenantID)
	if draft.RecordClass != workitemcreation.RecordClassGeneric && draft.GenericSubtype != "" {
		return nil, workitemcreation.NewDomainValidationFailed("professional work items cannot carry a generic subtype", nil)
	}
	// SourceReference is provider-scoped provenance, persisted by the snapshot.
	// Email thread identity is assigned only by its trusted email boundary.
	if draft.ExternalMessageID != "" {
		if plan.Resolved.Command.Email == nil || plan.Resolved.Identity.Channel != "email" || plan.Resolved.Identity.Provider != "msgraph_email" {
			return nil, workitemcreation.NewPermissionDenied("email identity requires verified email creation", nil)
		}
		create.SetExternalMessageID(draft.ExternalMessageID).SetConversationID(draft.ConversationID).SetCreatorEmail(draft.CreatorEmail)
	}
	if draft.AssigneeID != nil {
		create.SetAssigneeID(*draft.AssigneeID)
	}
	if draft.AssignmentGroupID != nil {
		create.SetAssignmentGroupID(*draft.AssignmentGroupID)
	}
	if draft.CategoryID != nil {
		create.SetCategoryID(*draft.CategoryID)
	}
	if draft.SLADefinitionID != nil {
		create.SetSLADefinitionID(*draft.SLADefinitionID)
	}
	created, err := create.Save(ctx)
	if err != nil {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not create work item", fmt.Errorf("create ticket: %w", err))
	}
	return created, nil
}

// Validate the base writer's associations independently of preparation. The
// resolver remains responsible for operation-specific authorization.
func validateDraftReferences(ctx context.Context, tx *ent.Tx, draft *workitemcreation.WorkItemDraft) error {
	for _, id := range []int{draft.RequesterID} {
		ok, err := tx.User.Query().Where(user.IDEQ(id), user.TenantIDEQ(draft.TenantID), user.ActiveEQ(true)).Exist(ctx)
		if err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
		}
		if !ok {
			return workitemcreation.NewReferenceNotFound("work item user is outside tenant", err)
		}
	}
	if draft.AssigneeID != nil {
		ok, err := tx.User.Query().Where(user.IDEQ(*draft.AssigneeID), user.TenantIDEQ(draft.TenantID), user.ActiveEQ(true)).Exist(ctx)
		if err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
		}
		if !ok {
			return workitemcreation.NewReferenceNotFound("assignee is outside tenant", err)
		}
	}
	if draft.AssignmentGroupID != nil {
		ok, err := tx.Group.Query().Where(group.IDEQ(*draft.AssignmentGroupID), group.TenantIDEQ(draft.TenantID)).Exist(ctx)
		if err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
		}
		if !ok {
			return workitemcreation.NewReferenceNotFound("assignment group is outside tenant", err)
		}
	}
	if draft.CategoryID != nil {
		ok, err := tx.TicketCategory.Query().Where(ticketcategory.IDEQ(*draft.CategoryID), ticketcategory.TenantIDEQ(draft.TenantID)).Exist(ctx)
		if err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
		}
		if !ok {
			return workitemcreation.NewReferenceNotFound("category is outside tenant", err)
		}
	}
	if draft.SLADefinitionID != nil {
		ok, err := tx.SLADefinition.Query().Where(sladefinition.IDEQ(*draft.SLADefinitionID), sladefinition.TenantIDEQ(draft.TenantID)).Exist(ctx)
		if err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
		}
		if !ok {
			return workitemcreation.NewReferenceNotFound("SLA is outside tenant", err)
		}
	}
	return nil
}
