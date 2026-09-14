package authorization

import (
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
)

// WorkItemReadScope preserves the existing ticket-list row policy. Tenant and
// deleted predicates and current class permission remain separate requirements.
// The caller supplies the authenticated effective session role, not a wire role.
func WorkItemReadScope(actorID int, effectiveRole string) predicate.Ticket {
	switch effectiveRole {
	case "super_admin", "sysadmin":
		return ticket.IDGT(0)
	default:
		if actorID <= 0 {
			return ticket.IDEQ(-1)
		}
		return ticket.Or(ticket.RequesterIDEQ(actorID), ticket.AssigneeIDEQ(actorID))
	}
}
