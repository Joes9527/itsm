package authorization

import (
	"context"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
)

// WorkItemLifecycleReader is implemented by each professional lifecycle owner.
// The caller supplies an already authorized WorkItem and its transaction client;
// this read projection neither authorizes writes nor grants delegation.
type WorkItemLifecycleReader interface {
	IsUnfinished(context.Context, *ent.Client, *ent.Ticket) (bool, error)
}

// WorkItemPolicy binds an immutable record class to both its professional ACL
// resource and its canonical BPMN business type. BPMN business IDs are always
// tickets.id (WorkItem ID).
const (
	WorkflowStartOnCreation = "creation"
	WorkflowStartOnSubmit   = "submit"
)

type WorkItemPolicy struct {
	WorkflowStartTiming  string
	Resource             string
	BusinessType         dto.BusinessType
	UsesProfessionalVerb bool
	// DeleteNonRequesterAction preserves an additional professional condition; it never grants deletion.
	DeleteNonRequesterAction string
}

var workItemPolicies = map[string]WorkItemPolicy{
	"generic":              {WorkflowStartTiming: WorkflowStartOnCreation, Resource: "ticket", BusinessType: dto.BusinessTypeGeneric},
	"incident":             {WorkflowStartTiming: WorkflowStartOnCreation, Resource: "incident", BusinessType: dto.BusinessTypeIncident, UsesProfessionalVerb: true},
	"problem":              {WorkflowStartTiming: WorkflowStartOnCreation, Resource: "problem", BusinessType: dto.BusinessTypeProblem, UsesProfessionalVerb: true},
	"change_request":       {WorkflowStartTiming: WorkflowStartOnSubmit, Resource: "change", BusinessType: dto.BusinessTypeChangeRequest, UsesProfessionalVerb: true},
	"service_request_item": {WorkflowStartTiming: WorkflowStartOnCreation, Resource: "service_request", BusinessType: dto.BusinessTypeServiceRequestItem, DeleteNonRequesterAction: "write"},
	"catalog_task":         {WorkflowStartTiming: WorkflowStartOnCreation, Resource: "service_request", BusinessType: dto.BusinessTypeCatalogTask},
}

func ResolveWorkItemPolicy(recordClass string) (WorkItemPolicy, error) {
	policy, exists := workItemPolicies[recordClass]
	if !exists {
		return WorkItemPolicy{}, fmt.Errorf("unsupported WorkItem record class %q", recordClass)
	}
	return policy, nil
}

func (policy WorkItemPolicy) ResolveAction(action string) string {
	if policy.UsesProfessionalVerb && (action == "create" || action == "update") {
		return "write"
	}
	return action
}

// ResolveWorkItemIdentity loads the canonical WorkItem identity exactly once.
func ResolveWorkItemIdentity(ctx context.Context, client *ent.Client, workItemID, tenantID int, readScope ...predicate.Ticket) (*ent.Ticket, WorkItemPolicy, error) {
	if client == nil {
		return nil, WorkItemPolicy{}, common.NewInternalError("WorkItem authorization client is unavailable", nil)
	}
	workItem, err := client.Ticket.Query().
		Where(ticket.ID(workItemID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
		Where(readScope...).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, WorkItemPolicy{}, common.NewNotFoundError("work item")
		}
		return nil, WorkItemPolicy{}, common.NewInternalError("query WorkItem for authorization", err)
	}
	policy, err := ResolveWorkItemPolicy(workItem.RecordClass)
	if err != nil {
		return nil, WorkItemPolicy{}, common.NewForbiddenError("unsupported WorkItem record class")
	}
	return workItem, policy, nil
}

// ValidateWorkItemBusinessIdentity enforces recordClass -> BPMN business_type
// and WorkItem ID for every WorkItem-owned process start. Release is not a
// WorkItem and remains owned by its professional aggregate.
func ValidateWorkItemBusinessIdentity(ctx context.Context, client *ent.Client, workItemID, tenantID int, businessType dto.BusinessType) error {
	if businessType == dto.BusinessTypeRelease {
		return nil
	}
	_, policy, err := ResolveWorkItemIdentity(ctx, client, workItemID, tenantID)
	if err != nil {
		return err
	}
	if policy.BusinessType != businessType {
		return fmt.Errorf("business type %q disagrees with WorkItem record class", businessType)
	}
	return nil
}

// AuthorizeWorkItem is the single application-level authority for a live,
// tenant-scoped WorkItem and its professional record-class permission.
func AuthorizeWorkItem(ctx context.Context, client *ent.Client, workItemID, tenantID int, roleName, action string) (*ent.Ticket, WorkItemPolicy, error) {
	workItem, policy, err := ResolveWorkItemIdentity(ctx, client, workItemID, tenantID)
	if err != nil {
		return nil, WorkItemPolicy{}, err
	}
	if !HasResourcePermission(client, roleName, policy.Resource, policy.ResolveAction(action), tenantID) {
		return nil, WorkItemPolicy{}, common.NewForbiddenError("insufficient WorkItem permission")
	}
	return workItem, policy, nil
}

// AuthorizeWorkItemCollaboration covers shared comment creation/editing and attachment
// upload, not professional updates, provisioning, approval or deletion.
func AuthorizeWorkItemCollaboration(ctx context.Context, client *ent.Client, workItemID, tenantID, actorID int, roleName, action string) (*ent.Ticket, WorkItemPolicy, error) {
	if action != "create" && action != "update" {
		return nil, WorkItemPolicy{}, common.NewForbiddenError("unsupported collaboration action")
	}
	item, policy, err := ResolveWorkItemIdentity(ctx, client, workItemID, tenantID)
	if err != nil {
		return nil, WorkItemPolicy{}, err
	}
	if item.RecordClass != "service_request_item" {
		if !HasResourcePermission(client, roleName, policy.Resource, policy.ResolveAction(action), tenantID) {
			return nil, WorkItemPolicy{}, common.NewForbiddenError("insufficient WorkItem permission")
		}
		return item, policy, nil
	}
	if actorID <= 0 || !HasResourcePermission(client, roleName, policy.Resource, "read", tenantID) {
		return nil, WorkItemPolicy{}, common.NewForbiddenError("insufficient collaboration permission")
	}
	// An explicit resource-wide administrative grant keeps its existing authority.
	admin := HasResourcePermission(client, roleName, policy.Resource, "*", tenantID)
	requester := item.RequesterID == actorID && HasResourcePermission(client, roleName, policy.Resource, "write", tenantID)
	assigned := item.AssigneeID == actorID && HasResourcePermission(client, roleName, policy.Resource, "provision", tenantID)
	if !admin && !requester && !assigned {
		return nil, WorkItemPolicy{}, common.NewForbiddenError("service request collaboration requires requester or current assignment")
	}
	return item, policy, nil
}
