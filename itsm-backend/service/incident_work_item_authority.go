package service

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"
)

// incidentTenantScope derives tenant and soft-delete visibility exclusively
// from the authoritative WorkItem row.
func incidentTenantScope(tenantID int, extra ...predicate.Ticket) predicate.Incident {
	predicates := []predicate.Ticket{ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()}
	predicates = append(predicates, extra...)
	return incident.HasWorkItemWith(predicates...)
}

func withIncidentWorkItemProjection(query *ent.TicketQuery) {
	query.WithCategory(func(categoryQuery *ent.TicketCategoryQuery) {
		categoryQuery.WithParent()
	})
}

// resolveIncidentCategory resolves the existing string API into the one
// structured WorkItem category relation. A supplied subcategory must be an
// active child of the supplied category in the same tenant.
func resolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error) {
	categoryName = strings.TrimSpace(categoryName)
	subcategoryName = strings.TrimSpace(subcategoryName)
	if categoryName == "" && subcategoryName == "" {
		return nil, nil
	}
	if categoryName == "" {
		return nil, fmt.Errorf("category is required when subcategory is supplied")
	}

	query := client.TicketCategory.Query().Where(
		ticketcategory.TenantIDEQ(tenantID),
		ticketcategory.IsActiveEQ(true),
	)
	if subcategoryName == "" {
		query = query.Where(ticketcategory.NameEQ(categoryName))
	} else {
		query = query.Where(
			ticketcategory.NameEQ(subcategoryName),
			ticketcategory.HasParentWith(
				ticketcategory.NameEQ(categoryName),
				ticketcategory.TenantIDEQ(tenantID),
				ticketcategory.IsActiveEQ(true),
			),
		)
	}
	category, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("ticket category not found in tenant")
		}
		return nil, fmt.Errorf("resolve ticket category: %w", err)
	}
	return &category.ID, nil
}
