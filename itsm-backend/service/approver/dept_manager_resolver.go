package approver

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
	"itsm-backend/ent/user"
)

// DeptManagerResolver resolves approvers based on department manager
type DeptManagerResolver struct{}

// NewDeptManagerResolver creates a new DeptManagerResolver
func NewDeptManagerResolver() *DeptManagerResolver {
	return &DeptManagerResolver{}
}

// GetType returns the resolver type
func (r *DeptManagerResolver) GetType() string {
	return "dept_manager"
}

// Resolve resolves department manager as approver
func (r *DeptManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	return r.resolve(ctx, client, appCtx, make(map[int]bool))
}

// resolve 是 Resolve 的内部实现，多带一个 visited 集合用来检测部门树里的环——
// parent_id 理论上不该成环，但一旦脏数据/误操作真的造成了环，原来的纯递归会无限
// 循环到栈溢出。总经理/分公司负责人这类固定部门审批人现在也复用这条链路
// （见 resolveFixedScopeAssignee），触发概率比过去只做"部门经理审批"兜底时更高，
// 所以补上这层保护。
func (r *DeptManagerResolver) resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext, visited map[int]bool) ([]*ApproverInfo, error) {
	if appCtx.DepartmentID == 0 {
		return nil, fmt.Errorf("department_id is required for dept_manager resolver")
	}
	if visited[appCtx.DepartmentID] {
		return nil, fmt.Errorf("department hierarchy cycle detected at department %d", appCtx.DepartmentID)
	}
	visited[appCtx.DepartmentID] = true

	// Get department with manager
	dept, err := client.Department.Query().
		Where(
			department.IDEQ(appCtx.DepartmentID),
			department.TenantIDEQ(appCtx.TenantID),
			department.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("department not found: %d", appCtx.DepartmentID)
		}
		return nil, fmt.Errorf("failed to query department: %w", err)
	}

	// If no manager, try parent department
	if dept.ManagerID == 0 {
		if dept.ParentID > 0 {
			// Recursive call with parent department
			parentCtx := *appCtx
			parentCtx.DepartmentID = dept.ParentID
			return r.resolve(ctx, client, &parentCtx, visited)
		}
		return nil, fmt.Errorf("no manager found for department %d or its ancestors", appCtx.DepartmentID)
	}

	// Get manager user info
	manager, err := client.User.Query().
		Where(
			user.IDEQ(dept.ManagerID),
			user.TenantIDEQ(appCtx.TenantID),
			user.Active(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("manager user not found or inactive: %d", dept.ManagerID)
		}
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	return []*ApproverInfo{
		{
			UserID:    manager.ID,
			UserName:  manager.Name,
			UserEmail: manager.Email,
			Role:      "department_manager",
			Source:    fmt.Sprintf("department:%d", appCtx.DepartmentID),
		},
	}, nil
}
