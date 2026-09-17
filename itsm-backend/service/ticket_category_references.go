package service

import (
	"context"
	"fmt"
	"strconv"

	"itsm-backend/ent"
	"itsm-backend/ent/incidentescalationrule"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/servicecatalog"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/ticketautomationrule"
	"itsm-backend/ent/tickettemplate"
)

// 引用保护错误。维护动作失败时返回通用原因，不泄露调用者无权查看的对象名称或计数。
var (
	ErrCTICategoryHasChildren      = fmt.Errorf("ticket category still has child nodes")
	ErrCTICategoryReferenced       = fmt.Errorf("ticket category is still referenced")
	ErrCTICategoryPublishedCatalog = fmt.Errorf("ticket category is used by a published service catalog")
	ErrCTICategoryCodeImmutable    = fmt.Errorf("ticket category code cannot be changed after creation")
	ErrCTICategoryNotFound         = fmt.Errorf("ticket category not found")
)

// CTIReferences 汇总一组分类节点在业务与配置中的引用。计数与 ID 只用于维护决策与
// 影响展示；面向无权限调用者时只能报告“存在引用”。
type CTIReferences struct {
	WorkItems             int
	Catalogs              int
	PublishedCatalogs     int
	SLADefinitionIDs      []int
	AssignmentRuleIDs     []int
	AutomationRuleIDs     []int
	TemplateIDs           []int
	ProcessBindingIDs     []int
	IncidentEscalationIDs []int
}

// Blocking 表示存在任一业务/配置引用，因此不可删除或移动。
func (r CTIReferences) Blocking() bool {
	return r.WorkItems > 0 || r.Catalogs > 0 ||
		len(r.SLADefinitionIDs) > 0 || len(r.AssignmentRuleIDs) > 0 || len(r.AutomationRuleIDs) > 0 ||
		len(r.TemplateIDs) > 0 || len(r.ProcessBindingIDs) > 0 || len(r.IncidentEscalationIDs) > 0
}

// PublishedCatalogBlocking 表示已发布目录引用了该节点（或其子树的节点）。
// 停用前必须先调整目录，否则在途申请会失去默认分类。
func (r CTIReferences) PublishedCatalogBlocking() bool { return r.PublishedCatalogs > 0 }

// countCTIReferences 在调用方事务内扫描结构性引用与遗留字符串引用。
// JSON 条件在 Go 中解析：规则条件/动作的 category_id 语义与运行时判定保持一致
// （见 ticket_rule_conditions.go），并覆盖 equals/not_equals/in/not_in 等所有含该 ID 的条件。
func countCTIReferences(ctx context.Context, tx *ent.Tx, tenantID int, nodes []CTINode) (CTIReferences, error) {
	references := CTIReferences{}
	ids := make([]int, 0, len(nodes))
	idSet := make(map[int]struct{}, len(nodes))
	legacyNames := make(map[string]struct{}, len(nodes)*2)
	for _, node := range nodes {
		ids = append(ids, node.ID)
		idSet[node.ID] = struct{}{}
		if node.Name != "" {
			legacyNames[node.Name] = struct{}{}
		}
		if node.Code != "" {
			legacyNames[node.Code] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return references, nil
	}

	tickets, err := tx.Ticket.Query().
		Where(ticket.TenantIDEQ(tenantID), ticket.CategoryIDIn(ids...)).
		Count(ctx)
	if err != nil {
		return references, err
	}
	references.WorkItems = tickets

	catalogs, err := tx.ServiceCatalog.Query().
		Where(servicecatalog.TenantIDEQ(tenantID), servicecatalog.DefaultTicketCategoryIDIn(ids...)).
		All(ctx)
	if err != nil {
		return references, err
	}
	for _, catalog := range catalogs {
		references.Catalogs++
		if catalog.IsActive && (catalog.Status == "active" || catalog.Status == "enabled") {
			references.PublishedCatalogs++
		}
	}

	slaDefinitions, err := tx.SLADefinition.Query().Where(sladefinition.TenantIDEQ(tenantID)).All(ctx)
	if err != nil {
		return references, err
	}
	for _, definition := range slaDefinitions {
		if intSliceIntersects(definition.CategoryIds, idSet) {
			references.SLADefinitionIDs = append(references.SLADefinitionIDs, definition.ID)
		}
	}

	assignmentRules, err := tx.TicketAssignmentRule.Query().Where(ticketassignmentrule.TenantIDEQ(tenantID)).All(ctx)
	if err != nil {
		return references, err
	}
	for _, rule := range assignmentRules {
		if ctiJSONReferences(rule.Conditions, idSet) {
			references.AssignmentRuleIDs = append(references.AssignmentRuleIDs, rule.ID)
		}
	}

	automationRules, err := tx.TicketAutomationRule.Query().Where(ticketautomationrule.TenantIDEQ(tenantID)).All(ctx)
	if err != nil {
		return references, err
	}
	for _, rule := range automationRules {
		// 条件决定命中，动作可以写入分类；两者都构成引用。
		if ctiJSONReferences(rule.Conditions, idSet) || ctiJSONReferences(rule.Actions, idSet) {
			references.AutomationRuleIDs = append(references.AutomationRuleIDs, rule.ID)
		}
	}

	templates, err := tx.TicketTemplate.Query().Where(tickettemplate.TenantIDEQ(tenantID)).All(ctx)
	if err != nil {
		return references, err
	}
	for _, template := range templates {
		if intSliceIntersects(template.CategoryIds, idSet) {
			references.TemplateIDs = append(references.TemplateIDs, template.ID)
		}
	}

	bindings, err := tx.ProcessBinding.Query().
		Where(processbinding.TenantIDEQ(tenantID), processbinding.CategoryIDIn(ids...)).
		All(ctx)
	if err != nil {
		return references, err
	}
	for _, binding := range bindings {
		references.ProcessBindingIDs = append(references.ProcessBindingIDs, binding.ID)
	}

	// 遗留字符串匹配无法映射回 ID：按名称/编码精确匹配，命中即视为不明确引用，
	// 阻止未确认的维护动作，由配置所有者先迁移到结构化引用。
	escalationRules, err := tx.IncidentEscalationRule.Query().Where(incidentescalationrule.TenantIDEQ(tenantID)).All(ctx)
	if err != nil {
		return references, err
	}
	for _, rule := range escalationRules {
		if rule.CategoryMatch == "" {
			continue
		}
		if _, matched := legacyNames[rule.CategoryMatch]; matched {
			references.IncidentEscalationIDs = append(references.IncidentEscalationIDs, rule.ID)
		}
	}
	return references, nil
}

func intSliceIntersects(values []int, wanted map[int]struct{}) bool {
	for _, value := range values {
		if _, ok := wanted[value]; ok {
			return true
		}
	}
	return false
}

// ctiJSONReferences 报告规则 JSON 是否引用了给定分类 ID。它只识别分类条件/动作，
// 不重新解释规则优先级或匹配优先级（那些仍由各所有者保留）。
func ctiJSONReferences(entries []map[string]interface{}, wanted map[int]struct{}) bool {
	for _, entry := range entries {
		if field, ok := entry["field"].(string); ok && field != "category_id" {
			continue
		}
		value, ok := entry["value"]
		if !ok {
			value, ok = entry["category_id"]
			if !ok {
				continue
			}
		}
		if ctiJSONValueReferences(value, wanted) {
			return true
		}
	}
	return false
}

func ctiJSONValueReferences(value interface{}, wanted map[int]struct{}) bool {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			if ctiJSONValueReferences(item, wanted) {
				return true
			}
		}
		return false
	case map[string]interface{}:
		for _, item := range typed {
			if ctiJSONValueReferences(item, wanted) {
				return true
			}
		}
		return false
	case int:
		_, ok := wanted[typed]
		return ok
	case int64:
		_, ok := wanted[int(typed)]
		return ok
	case float64:
		if typed != float64(int(typed)) {
			return false
		}
		_, ok := wanted[int(typed)]
		return ok
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return false
		}
		_, ok := wanted[parsed]
		return ok
	default:
		return false
	}
}
