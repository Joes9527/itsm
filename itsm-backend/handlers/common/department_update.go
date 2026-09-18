package common

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
)

// departmentUpdateRequest 用指针区分"没传这个字段"与"把它清空"。
//
// 旧实现用 `!= 0` 判断，导致负责人与父节点**永远无法被清空**
// （传 0 会被忽略），与设计"负责人允许暂缺"直接冲突。
type departmentUpdateRequest struct {
	Name        *string `json:"name"`
	Code        *string `json:"code"`
	Description *string `json:"description"`
	NodeType    *string `json:"nodeType"`
	ManagerID   *int    `json:"managerId"`
	ParentID    *int    `json:"parentId"`
	Reason      string  `json:"reason"`
}

type departmentChange struct {
	ManagerFrom int
	ManagerTo   int
	ParentFrom  int
	ParentTo    int
	Reason      string
	Changed     bool
}

// applyDepartmentUpdate 计算并落库一次部门变更。
//
// 任何**实际**变更都必须带 reason（设计 §3：负责人变更留痕——谁、何时、改了什么、为什么）。
// 只传了字段但值没变，不算变更，也不需要 reason。
func applyDepartmentUpdate(ctx context.Context, client *ent.Client, current *Department, req departmentUpdateRequest) (*Department, *departmentChange, error) {
	if current == nil {
		return nil, nil, fmt.Errorf("current department is required")
	}

	change := &departmentChange{
		ManagerFrom: current.ManagerID,
		ManagerTo:   current.ManagerID,
		ParentFrom:  current.ParentID,
		ParentTo:    current.ParentID,
		Reason:      strings.TrimSpace(req.Reason),
	}

	nameChanged := req.Name != nil && *req.Name != current.Name
	descriptionChanged := req.Description != nil && *req.Description != current.Description
	codeChanged := req.Code != nil && *req.Code != current.Code

	// 节点类型必须参与"是否发生变更"的判定，否则"只改类型"会被判成无变更而提前返回，
	// 类型**静默不落库**。校验也放在这里（早于提前返回），未知取值一律 fail-closed，
	// 不会因为"其它字段都没变"而被悄悄放过。
	nodeTypeChanged := false
	if req.NodeType != nil {
		normalized, err := NormalizeDepartmentNodeType(*req.NodeType)
		if err != nil {
			return nil, nil, err
		}
		req.NodeType = &normalized
		nodeTypeChanged = normalized != current.NodeType
	}

	if codeChanged {
		// 保留既有"改编码"能力（旧 API 支持），但必须沿用租户内唯一约束。
		exists, err := client.Department.Query().
			Where(
				department.CodeEQ(*req.Code),
				department.TenantIDEQ(current.TenantID),
				department.DeletedAtIsNil(),
				department.IDNEQ(current.ID),
			).
			Exist(ctx)
		if err != nil {
			return nil, nil, err
		}
		if exists {
			return nil, nil, fmt.Errorf("department code already exists: %s", *req.Code)
		}
	}

	if req.ManagerID != nil {
		if err := validateDepartmentManager(ctx, client, current.TenantID, *req.ManagerID); err != nil {
			return nil, nil, err
		}
		change.ManagerTo = *req.ManagerID
	}
	if req.ParentID != nil {
		if err := validateDepartmentParent(ctx, client, current.TenantID, current.ID, *req.ParentID); err != nil {
			return nil, nil, err
		}
		change.ParentTo = *req.ParentID
	}

	change.Changed = nameChanged || descriptionChanged || codeChanged || nodeTypeChanged ||
		change.ManagerFrom != change.ManagerTo ||
		change.ParentFrom != change.ParentTo
	if !change.Changed {
		return current, change, nil
	}
	if change.Reason == "" {
		return nil, nil, fmt.Errorf("a department change requires a reason")
	}

	update := client.Department.UpdateOneID(current.ID).Where(department.TenantIDEQ(current.TenantID))
	if req.Name != nil {
		update = update.SetName(*req.Name)
	}
	if req.Code != nil {
		update = update.SetCode(*req.Code)
	}
	if req.Description != nil {
		update = update.SetDescription(*req.Description)
	}
	if req.NodeType != nil {
		// 节点类型与创建路径共用同一把权威；未知取值在写入前 fail-closed，
		// 不把它悄悄存成空串或原样落库。
		nodeType, err := NormalizeDepartmentNodeType(*req.NodeType)
		if err != nil {
			return nil, nil, err
		}
		update = update.SetNodeType(nodeType)
	}
	if req.ManagerID != nil {
		update = update.SetManagerID(*req.ManagerID)
	}
	if req.ParentID != nil {
		if *req.ParentID == 0 {
			// 顶层在库里是 NULL 而不是 0：parent_id 有自引用外键，写 0 会违反外键。
			update = update.SetNillableParentID(nil)
		} else {
			update = update.SetParentID(*req.ParentID)
		}
	}
	saved, err := update.Save(ctx)
	if err != nil {
		return nil, nil, err
	}
	return toDeptDomain(saved), change, nil
}

// validateDepartmentParent 拒绝自引用与"挂到自己的后代下"——组织树必须无环。
// parentID == 0 表示移到顶层，合法。
func validateDepartmentParent(ctx context.Context, client *ent.Client, tenantID, selfID, parentID int) error {
	if parentID == 0 {
		return nil
	}
	if parentID == selfID {
		return fmt.Errorf("a department cannot be its own parent")
	}

	// 沿父链上溯；visited 防止既有脏环把校验变成死循环。
	visited := map[int]bool{selfID: true}
	cursor := parentID
	for cursor != 0 {
		if cursor == selfID {
			return fmt.Errorf("a department cannot be moved under its own descendant")
		}
		if visited[cursor] {
			return fmt.Errorf("department tree already contains a cycle at %d", cursor)
		}
		visited[cursor] = true

		node, err := client.Department.Query().
			Where(department.IDEQ(cursor), department.TenantIDEQ(tenantID)).
			Select(department.FieldParentID).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return fmt.Errorf("parent department %d does not exist in tenant %d", cursor, tenantID)
			}
			return err
		}
		cursor = node.ParentID
	}
	return nil
}
