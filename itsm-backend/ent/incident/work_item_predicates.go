package incident

import (
	"time"

	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
)

// These predicates make WorkItem ownership explicit at Incident query sites.
// They always compile to an EXISTS join against tickets; no Incident public
// field or fallback column is read.
func OwnedByTenant(tenantID int) predicate.Incident {
	return HasWorkItemWith(ticket.TenantIDEQ(tenantID))
}

func WorkItemStatusEQ(status string) predicate.Incident {
	return HasWorkItemWith(ticket.StatusEQ(status))
}

func WorkItemStatusIn(statuses ...string) predicate.Incident {
	return HasWorkItemWith(ticket.StatusIn(statuses...))
}

func WorkItemStatusNotIn(statuses ...string) predicate.Incident {
	return HasWorkItemWith(ticket.StatusNotIn(statuses...))
}

func WorkItemPriorityEQ(priority string) predicate.Incident {
	return HasWorkItemWith(ticket.PriorityEQ(priority))
}

func WorkItemPriorityIn(priorities ...string) predicate.Incident {
	return HasWorkItemWith(ticket.PriorityIn(priorities...))
}

func WorkItemTitleEQ(title string) predicate.Incident {
	return HasWorkItemWith(ticket.TitleEQ(title))
}

func WorkItemTitleContains(value string) predicate.Incident {
	return HasWorkItemWith(ticket.TitleContains(value))
}

func WorkItemTitleContainsFold(value string) predicate.Incident {
	return HasWorkItemWith(ticket.TitleContainsFold(value))
}

func WorkItemDescriptionContains(value string) predicate.Incident {
	return HasWorkItemWith(ticket.DescriptionContains(value))
}

func WorkItemDescriptionContainsFold(value string) predicate.Incident {
	return HasWorkItemWith(ticket.DescriptionContainsFold(value))
}

func WorkItemCreatedAtGTE(value time.Time) predicate.Incident {
	return HasWorkItemWith(ticket.CreatedAtGTE(value))
}

func WorkItemCreatedAtLTE(value time.Time) predicate.Incident {
	return HasWorkItemWith(ticket.CreatedAtLTE(value))
}
