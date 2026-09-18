package authorization

import (
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
)

// WorkItemDataScopeAllRole is shared by WorkItem lists and row authorization.
func WorkItemDataScopeAllRole(roleName string) bool {
	return roleName == "super_admin" || roleName == "sysadmin"
}

// WorkItemOwnedOrAssignedScope is the authoritative restricted row predicate.
func WorkItemOwnedOrAssignedScope(actorID int) predicate.Ticket {
	if actorID <= 0 {
		return ticket.IDEQ(-1)
	}
	return ticket.Or(ticket.RequesterIDEQ(actorID), ticket.AssigneeIDEQ(actorID))
}

func WorkItemRowScope(actorID int, roleName string) predicate.Ticket {
	if actorID <= 0 {
		return ticket.IDEQ(-1)
	}
	if WorkItemDataScopeAllRole(roleName) {
		return ticket.IDGT(0)
	}
	return WorkItemOwnedOrAssignedScope(actorID)
}

// FulfillmentAction preserves the professional permission vocabulary; Requested
// Item submission permission never authorizes provisioning.
func (policy WorkItemPolicy) FulfillmentAction() string {
	if policy.Resource == "service_request" {
		return "provision"
	}
	return policy.ResolveAction("update")
}
