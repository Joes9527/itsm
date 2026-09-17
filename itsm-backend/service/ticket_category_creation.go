package service

import (
	"context"
	"errors"

	"itsm-backend/ent"
	"itsm-backend/ent/ticketcategory"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// ResolveCreationClassification 解析创建时的工单分类（CTI）。
//
// 权威顺序（设计 §5.1）：
//  1. 目录默认分类（由目录所有者按已确认的目录版本解析后填入）是初始权威：必须解析出当前
//     有效的完整三级路径；客户端提交相同最深节点可兼容，提交不同路径直接冲突。
//  2. 普通报障允许未分类或部分分类：只校验已提供路径的租户/层级/启用状态。
//  3. 遗留 `category`/`subcategory` 名称仍按租户解析，但最终统一走同一条路径校验。
//
// 工单只保存所选最深节点（见 workitemcreation.NewPlan）。
func (s *TicketCategoryService) ResolveCreationClassification(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) (creation.ResolvedCTI, error) {
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

	// 目录默认分类：完整三级路径是提交的硬前提，客户端不能降级或改写。
	if command.CatalogDefaultCategoryID != nil {
		selected := *command.CatalogDefaultCategoryID
		if selected <= 0 {
			return result, creation.NewDomainValidationFailed("catalog default classification is invalid", nil)
		}
		if deepest := deepestCTIID(result); deepest != 0 && deepest != selected {
			return result, creation.NewDomainValidationFailed("CTI conflicts with the catalog default classification", nil)
		}
		path, err := s.ResolveCTIPath(ctx, tx, identity.TenantID, selected, true, true)
		if err != nil {
			return result, mapCTIPathError(err)
		}
		return resolvedCTIFromPath(path), nil
	}

	deepest := deepestCTIID(result)
	if deepest == 0 {
		// 未分类：普通报障合法；目录强制由目录所有者与 intake 解析器判定。
		return result, nil
	}
	// 提供的槽位必须连续：不能只给 typeId 而缺 categoryId（那是一条不存在的分类声明）。
	slots := []*int{result.CategoryID, result.TypeID, result.ItemID}
	for index, provided := range slots {
		if provided != nil && *provided > 0 {
			continue
		}
		for deeper := index + 1; deeper < len(slots); deeper++ {
			if slots[deeper] != nil && *slots[deeper] > 0 {
				return result, creation.NewDomainValidationFailed("CTI requires a complete hierarchy", nil)
			}
		}
	}
	path, err := s.ResolveCTIPath(ctx, tx, identity.TenantID, deepest, false, true)
	if err != nil {
		return result, mapCTIPathError(err)
	}
	// 每个显式提供的节点都必须出现在真实路径中且相对顺序一致：允许多级节点用更深槽位
	// 表达（历史契约），但拒绝自报层级或颠倒的父子关系。
	previousIndex := -1
	for _, provided := range slots {
		if provided == nil || *provided <= 0 {
			continue
		}
		index := ctiPathIndex(path, *provided)
		if index < 0 || index <= previousIndex {
			return result, creation.NewDomainValidationFailed("CTI hierarchy does not match", nil)
		}
		previousIndex = index
	}
	return resolvedCTIFromProvidedSlots(path, result), nil
}

// ctiPathIndex 返回节点在真实路径中的下标，未命中返回 -1。
func ctiPathIndex(path []CTINode, id int) int {
	for index, node := range path {
		if node.ID == id {
			return index
		}
	}
	return -1
}

func deepestCTIID(result creation.ResolvedCTI) int {
	switch {
	case result.ItemID != nil && *result.ItemID > 0:
		return *result.ItemID
	case result.TypeID != nil && *result.TypeID > 0:
		return *result.TypeID
	case result.CategoryID != nil && *result.CategoryID > 0:
		return *result.CategoryID
	default:
		return 0
	}
}

// resolvedCTIFromPath 把完整路径投影为创建契约：最深节点是持久化权威。
func resolvedCTIFromPath(path []CTINode) creation.ResolvedCTI {
	if len(path) == 0 {
		return creation.ResolvedCTI{}
	}
	result := creation.ResolvedCTI{CategoryName: path[0].Name}
	result.CategoryID = &path[0].ID
	if len(path) > 1 {
		result.TypeName = path[1].Name
		result.TypeID = &path[1].ID
	}
	if len(path) > 2 {
		result.ItemID = &path[2].ID
	}
	return result
}

// resolvedCTIFromProvidedSlots 保留调用方提供的槽位 ID（历史契约），名称取真实路径，
// 使工作流变量等投影与真实分类一致。
func resolvedCTIFromProvidedSlots(path []CTINode, provided creation.ResolvedCTI) creation.ResolvedCTI {
	result := provided
	for _, slot := range []struct {
		id   *int
		name *string
	}{
		{result.CategoryID, &result.CategoryName},
		{result.TypeID, &result.TypeName},
	} {
		if slot.id == nil || *slot.id <= 0 {
			continue
		}
		if index := ctiPathIndex(path, *slot.id); index >= 0 {
			*slot.name = path[index].Name
		}
	}
	return result
}

// mapCTIPathError 把分类结构错误映射到创建契约的既有错误类型，且不泄露跨租户对象是否存在。
func mapCTIPathError(err error) error {
	switch {
	case errors.Is(err, ErrCTIPathIncomplete), errors.Is(err, ErrCTIPathTooDeep),
		errors.Is(err, ErrCTIPathHierarchy), errors.Is(err, ErrCTIPathInactive):
		return creation.NewDomainValidationFailed("CTI classification is incomplete or unavailable", err)
	case errors.Is(err, ErrCTIPathOutsideTenant), errors.Is(err, ErrCTICategoryNotFound):
		return creation.NewReferenceNotFound("CTI node is outside tenant", err)
	default:
		return creation.NewInfrastructureUnavailable("could not resolve CTI classification", err)
	}
}
