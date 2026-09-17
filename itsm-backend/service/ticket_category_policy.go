package service

import (
	"errors"
)

// CTI（Category → Type → Item）是工单的业务分类，最多三级。本文件只拥有
// 纯结构语义：路径不变量与规则匹配范围。持久化、租户解析与锁在
// ticket_category_service.go / ticket_category_references.go，调用方负责事务。
//
// 不变量（与设计 §5、§5.3、§5.4 对应）：
//   - 一条路径按根到所选节点排序，Level 从 1 连续递增；
//   - 根节点 ParentID 为 0，其余节点 ParentID 等于前一个节点 ID；
//   - 所有节点同租户，最多三级，无重复（即无环）；
//   - requireActive 为真时所有节点必须启用；
//   - requireFull 为真时必须是完整三级；未分类/部分分类仅在 requireFull 为假时合法。

// CTINode 是分类路径上的一个节点投影。
type CTINode struct {
	ID       int
	ParentID int
	Level    int
	TenantID int
	Name     string
	Code     string
	Active   bool
}

// CTIMatchScope 声明规则条件引用分类时的匹配范围。
type CTIMatchScope string

const (
	// CTIExact 仅匹配选中的最深节点，保留历史规则的实际匹配集合。
	CTIExact CTIMatchScope = "exact"
	// CTISubtree 匹配当前节点及其下级，即完整路径上的任一祖先。
	CTISubtree CTIMatchScope = "subtree"
)

// CTI 路径校验与匹配的结构化失败原因。调用方负责映射为各领域既有错误类型。
var (
	ErrCTIPathIncomplete    = errors.New("CTI path is incomplete")
	ErrCTIPathTooDeep       = errors.New("CTI path exceeds three levels")
	ErrCTIPathHierarchy     = errors.New("CTI path hierarchy is inconsistent")
	ErrCTIPathOutsideTenant = errors.New("CTI path is outside the tenant")
	ErrCTIPathInactive      = errors.New("CTI path contains a disabled node")
	ErrCTIUnknownMatchScope = errors.New("CTI match scope is unknown")
)

const (
	// CTIMaxDepth 是产品的最大分类层级。
	CTIMaxDepth = 3
	// CTICompleteDepth 是完成质量门禁要求的完整层级。
	CTICompleteDepth = 3
)

// ValidateCTIPath 校验一条根到所选节点的候选路径。
// nodes 为空表示未分类；tenantID 为调用方已认证的租户。
func ValidateCTIPath(nodes []CTINode, tenantID int, requireFull, requireActive bool) error {
	if tenantID <= 0 {
		return ErrCTIPathOutsideTenant
	}
	if len(nodes) == 0 {
		if requireFull {
			return ErrCTIPathIncomplete
		}
		return nil
	}
	if len(nodes) > CTIMaxDepth {
		return ErrCTIPathTooDeep
	}
	seen := make(map[int]struct{}, len(nodes))
	for index, node := range nodes {
		if node.TenantID != tenantID {
			return ErrCTIPathOutsideTenant
		}
		if node.ID <= 0 {
			return ErrCTIPathHierarchy
		}
		if _, duplicate := seen[node.ID]; duplicate {
			return ErrCTIPathHierarchy
		}
		seen[node.ID] = struct{}{}
		if node.Level != index+1 {
			return ErrCTIPathHierarchy
		}
		expectedParent := 0
		if index > 0 {
			expectedParent = nodes[index-1].ID
		}
		if node.ParentID != expectedParent {
			return ErrCTIPathHierarchy
		}
		if requireActive && !node.Active {
			return ErrCTIPathInactive
		}
	}
	if requireFull && len(nodes) != CTICompleteDepth {
		return ErrCTIPathIncomplete
	}
	return nil
}

// MatchCTI 判断一条完整根路径是否命中规则条件引用的节点。
// path 是候选工单的完整根路径（可少于三级）；未知 scope 必须失败，不得静默按精确处理。
func MatchCTI(path []CTINode, criterionID int, scope CTIMatchScope) (bool, error) {
	switch scope {
	case CTIExact, CTISubtree:
	default:
		return false, ErrCTIUnknownMatchScope
	}
	if criterionID <= 0 {
		return false, ErrCTIPathIncomplete
	}
	if len(path) == 0 {
		return false, ErrCTIPathIncomplete
	}
	if scope == CTIExact {
		return path[len(path)-1].ID == criterionID, nil
	}
	for _, node := range path {
		if node.ID == criterionID {
			return true, nil
		}
	}
	return false, nil
}
