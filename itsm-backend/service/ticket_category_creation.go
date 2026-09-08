package service

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/ticketcategory"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func (*TicketCategoryService) ResolveCreationClassification(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) (creation.ResolvedCTI, error) {
	result := creation.ResolvedCTI{}
	if command.CTI != nil {
		result = creation.ResolvedCTI{CategoryID: command.CTI.CategoryID, TypeID: command.CTI.TypeID, ItemID: command.CTI.ItemID}
	}
	category, subcategory := "", ""
	if command.Incident != nil {
		category, subcategory = command.Incident.Category, command.Incident.Subcategory
	}
	if command.Problem != nil {
		category = command.Problem.Category
	}
	if command.Change != nil {
		category = command.Change.Category
	}
	if command.Generic != nil {
		category = command.Generic.Category
	}
	if result.CategoryID == nil && category != "" {
		record, err := tx.TicketCategory.Query().Where(ticketcategory.TenantIDEQ(identity.TenantID), ticketcategory.NameEQ(category), ticketcategory.IsActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) || ent.IsNotSingular(err) {
			return result, creation.NewReferenceNotFound("category is missing or ambiguous", err)
		}
		if err != nil {
			return result, creation.NewInfrastructureUnavailable("could not resolve category", err)
		}
		result.CategoryID = &record.ID
	}
	if subcategory != "" {
		if result.CategoryID == nil {
			return result, creation.NewDomainValidationFailed("subcategory requires category", nil)
		}
		record, err := tx.TicketCategory.Query().Where(ticketcategory.TenantIDEQ(identity.TenantID), ticketcategory.NameEQ(subcategory), ticketcategory.ParentIDEQ(*result.CategoryID), ticketcategory.IsActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) || ent.IsNotSingular(err) {
			return result, creation.NewReferenceNotFound("subcategory is missing or ambiguous", err)
		}
		if err != nil {
			return result, creation.NewInfrastructureUnavailable("could not resolve subcategory", err)
		}
		if result.TypeID != nil && *result.TypeID != record.ID {
			return result, creation.NewDomainValidationFailed("subcategory conflicts with CTI type", nil)
		}
		result.TypeID = &record.ID
	}
	parent := 0
	for level, id := range []*int{result.CategoryID, result.TypeID, result.ItemID} {
		if id == nil {
			if level == 0 && (result.TypeID != nil || result.ItemID != nil) || level == 1 && result.ItemID != nil {
				return result, creation.NewDomainValidationFailed("CTI requires a complete hierarchy", nil)
			}
			continue
		}
		record, err := tx.TicketCategory.Query().Where(ticketcategory.IDEQ(*id), ticketcategory.TenantIDEQ(identity.TenantID), ticketcategory.IsActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return result, creation.NewReferenceNotFound("CTI node is outside tenant", err)
		}
		if err != nil {
			return result, creation.NewInfrastructureUnavailable("could not resolve CTI node", err)
		}
		if level > 0 && record.ParentID != parent {
			return result, creation.NewDomainValidationFailed("CTI hierarchy does not match", nil)
		}
		if level == 0 {
			result.CategoryName = record.Name
		}
		parent = record.ID
	}
	return result, nil
}
