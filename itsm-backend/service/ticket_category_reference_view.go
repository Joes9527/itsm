package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/ticketautomationrule"
	"itsm-backend/ent/tickettemplate"
)

// 分类详情"真实引用"的授权读取契约（B3）。
//
// 设计边界：引用清单由**各资源所有者**提供显示名（owner-provided port），
// 聚合层只负责合并、分页与 RBAC 过滤，不跨域调用其它域的 repository。
// 维护保护（删除/移动/停用）仍使用不经过 RBAC 过滤的 countCTIReferences：
// 即使当前 actor 看不到某个引用对象，也必须**阻止**危险操作，只报告"存在引用"。

// CTIReferenceNameSource 由各资源所有者实现，向分类聚合层提供引用对象的显示名。
type CTIReferenceNameSource interface {
	// Kind 是该来源对应的引用种类。
	Kind() string
	// Resource 是查看该种类明细所需的 RBAC 资源。
	Resource() string
	// Names 返回给定 ID 中真实存在对象的显示名；不存在的 ID 直接忽略，
	// 不得返回错误，以免通过错误差异推断对象是否存在。
	Names(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error)
}

// CTIReferenceKind 是引用种类与"查看明细所需 RBAC 资源"的注册表。
// 新增引用种类时必须同时注册资源，避免出现"能看见名称但无权查看该模块"的越权展示。
type CTIReferenceKind struct {
	Kind     string
	Resource string
	IDs      func(CTIReferences) []int
	// Names 由所有者提供；为空表示该种类只报告"存在引用"，不提供明细。
	Names func(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error)
}

// CTIReferenceItem 是引用清单中的一条（仅在调用者有权查看该种类时返回）。
type CTIReferenceItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CTIReferenceGroup 是某一引用种类的可见性结果。
//
// Referenced 来自**不受 RBAC 影响**的真实扫描结果：即使调用者无权查看明细，
// 也能知道"存在引用"（这是执行维护操作所必需的信息）；
// Visible=false 时不返回 Total/Items，避免泄露对象名称、数量与存在性细节。
type CTIReferenceGroup struct {
	Kind       string             `json:"kind"`
	Referenced bool               `json:"referenced"`
	Visible    bool               `json:"visible"`
	Total      int                `json:"total,omitempty"`
	Items      []CTIReferenceItem `json:"items,omitempty"`
}

// CTIReferenceView 是分类详情页的引用视图。
type CTIReferenceView struct {
	CategoryID   int                 `json:"categoryId"`
	CategoryPath []CTINode           `json:"categoryPath"`
	Blocking     bool                `json:"blocking"`
	Page         int                 `json:"page"`
	PageSize     int                 `json:"pageSize"`
	Groups       []CTIReferenceGroup `json:"groups"`
}

// CTIReferenceQuery 是引用视图的查询条件。
type CTIReferenceQuery struct {
	TenantID  int
	ActorRole string
	// CategoryID 是要查看的分类节点（含其子树：删除/移动保护按子树判定）。
	CategoryID int
	Page       int
	PageSize   int
	// IncludeWorkItems 控制是否展示工单引用计数（需要 ticket:read）。
	IncludeWorkItems bool
}

const (
	CTIReferenceDefaultPageSize = 20
	CTIReferenceMaxPageSize     = 100
)

// ctiReferenceKinds 是引用种类注册表。顺序稳定，便于前端与测试断言。
var ctiReferenceKinds = []CTIReferenceKind{
	{Kind: "catalog", Resource: "service_catalog", IDs: func(r CTIReferences) []int { return r.CatalogIDs }},
	{Kind: "sla_definition", Resource: "sla",
		IDs:   func(r CTIReferences) []int { return r.SLADefinitionIDs },
		Names: ctiNamesFromSLADefinitions},
	{Kind: "assignment_rule", Resource: "assignment_rule",
		IDs:   func(r CTIReferences) []int { return r.AssignmentRuleIDs },
		Names: ctiNamesFromAssignmentRules},
	{Kind: "automation_rule", Resource: "automation_rule",
		IDs:   func(r CTIReferences) []int { return r.AutomationRuleIDs },
		Names: ctiNamesFromAutomationRules},
	{Kind: "ticket_template", Resource: "ticket_template",
		IDs:   func(r CTIReferences) []int { return r.TemplateIDs },
		Names: ctiNamesFromTicketTemplates},
	// 以下种类暂无所有者提供的名称契约，或属于历史字符串引用：
	// 只报告"存在引用"，不提供明细。
	{Kind: "process_binding", Resource: "", IDs: func(r CTIReferences) []int { return r.ProcessBindingIDs }},
	{Kind: "incident_escalation_rule", Resource: "", IDs: func(r CTIReferences) []int { return r.IncidentEscalationIDs }},
}

// RegisterCTIReferenceNameSource 允许资源所有者注入自己的名称契约（如服务目录域）。
// 未注册的种类只报告"存在引用"，不提供明细；重复注册会 panic（启动期配置错误应立即暴露）。
func RegisterCTIReferenceNameSource(source CTIReferenceNameSource) {
	if source == nil {
		return
	}
	for index := range ctiReferenceKinds {
		if ctiReferenceKinds[index].Kind != source.Kind() {
			continue
		}
		if ctiReferenceKinds[index].Resource != "" && ctiReferenceKinds[index].Resource != source.Resource() {
			panic(fmt.Sprintf("cti reference kind %s already registered with resource %s", source.Kind(), ctiReferenceKinds[index].Resource))
		}
		ctiReferenceKinds[index].Resource = source.Resource()
		ctiReferenceKinds[index].Names = source.Names
		return
	}
	panic(fmt.Sprintf("unknown cti reference kind %s", source.Kind()))
}

// ReferenceView 返回分类（含子树）的真实引用，并按调用者的 RBAC 过滤明细。
func (s *TicketCategoryService) ReferenceView(ctx context.Context, query CTIReferenceQuery) (CTIReferenceView, error) {
	view := CTIReferenceView{CategoryID: query.CategoryID, Page: 1, PageSize: CTIReferenceDefaultPageSize, Groups: []CTIReferenceGroup{}}
	if s == nil || s.client == nil {
		return view, errors.New("CTI reference view requires a client")
	}
	if query.TenantID <= 0 {
		return view, ErrCTIPathOutsideTenant
	}
	page, pageSize := query.Page, query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = CTIReferenceDefaultPageSize
	}
	if pageSize > CTIReferenceMaxPageSize {
		pageSize = CTIReferenceMaxPageSize
	}
	view.Page, view.PageSize = page, pageSize

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return view, err
	}
	defer tx.Rollback()

	// 先确认分类存在且在租户范围内，再扫描引用（跨租户返回与不存在一致的错误）。
	current, err := lockTicketCategory(ctx, tx, query.TenantID, query.CategoryID)
	if err != nil {
		return view, mapCTIPathError(err)
	}
	subtree, _, err := s.loadSubtree(ctx, tx, query.TenantID, current.ID)
	if err != nil {
		return view, err
	}
	path, err := s.ProjectCTIPath(ctx, tx, query.TenantID, current.ID)
	if err != nil {
		return view, mapCTIPathError(err)
	}
	view.CategoryPath = path

	references, err := countCTIReferences(ctx, tx, query.TenantID, subtree)
	if err != nil {
		return view, err
	}
	// 维护保护按**未经 RBAC 过滤**的真实引用计算（含工单与历史字符串引用）。
	view.Blocking = references.Blocking()

	visible := func(resource string) bool {
		if strings.TrimSpace(resource) == "" {
			// 没有注册查看资源：只报告"存在引用"。
			return false
		}
		return authorization.HasResourcePermission(s.client, query.ActorRole, resource, "read", query.TenantID)
	}

	if query.IncludeWorkItems {
		group := CTIReferenceGroup{Kind: "work_item", Referenced: references.WorkItems > 0}
		if visible("ticket") {
			group.Visible = true
			group.Total = references.WorkItems
		}
		view.Groups = append(view.Groups, group)
	}

	for _, kind := range ctiReferenceKinds {
		ids := kind.IDs(references)
		group := CTIReferenceGroup{Kind: kind.Kind, Referenced: len(ids) > 0}
		if len(ids) > 0 && visible(kind.Resource) && kind.Names != nil {
			names, err := kind.Names(ctx, s.client, query.TenantID, ids)
			if err != nil {
				return view, fmt.Errorf("could not load %s reference names: %w", kind.Kind, err)
			}
			items := make([]CTIReferenceItem, 0, len(ids))
			for _, id := range ids {
				name, ok := names[id]
				if !ok {
					// 所有者报告对象不存在（可能刚被删除）：不返回该条，也不报错。
					continue
				}
				items = append(items, CTIReferenceItem{ID: id, Name: name})
			}
			sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
			group.Visible = true
			group.Total = len(items)
			group.Items = ctiPaginate(items, page, pageSize)
		}
		view.Groups = append(view.Groups, group)
	}

	if err := tx.Commit(); err != nil {
		return view, err
	}
	return view, nil
}

func ctiPaginate(items []CTIReferenceItem, page, pageSize int) []CTIReferenceItem {
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []CTIReferenceItem{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func ctiNamesFromSLADefinitions(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	rows, err := client.SLADefinition.Query().
		Where(sladefinition.TenantIDEQ(tenantID), sladefinition.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

func ctiNamesFromAssignmentRules(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	rows, err := client.TicketAssignmentRule.Query().
		Where(ticketassignmentrule.TenantIDEQ(tenantID), ticketassignmentrule.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

func ctiNamesFromAutomationRules(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	rows, err := client.TicketAutomationRule.Query().
		Where(ticketautomationrule.TenantIDEQ(tenantID), ticketautomationrule.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

func ctiNamesFromTicketTemplates(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	rows, err := client.TicketTemplate.Query().
		Where(tickettemplate.TenantIDEQ(tenantID), tickettemplate.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}
