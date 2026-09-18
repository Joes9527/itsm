package common

import (
	"context"
	"fmt"

	"itsm-backend/ent/department"
	"itsm-backend/ent/user"
)

// maxSubtreeNodes 是子树遍历的节点预算：部门树实测最深 14 层、单租户近 8000 节点。
// 统计必须有界，避免脏环或异常数据把一次请求变成无界遍历。
const maxSubtreeNodes = 10000

// subtreeNodeBudget 返回本次遍历的节点预算。
// 默认用 maxSubtreeNodes；测试可设小值走同一条代码路径。
func (r *EntRepository) subtreeNodeBudget() int {
	if r.subtreeBudget > 0 {
		return r.subtreeBudget
	}
	return maxSubtreeNodes
}

// CountDepartmentSubtreeEmployees 统计某部门**及其所有后代**的在职员工数。
//
// 只应在部门详情/显式展开时调用；部门列表接口禁止逐行调用（设计 §6 性能契约）。
// 超预算时返回错误——可见的失败优于不准确的数字或跑飞的查询。
func (r *EntRepository) CountDepartmentSubtreeEmployees(ctx context.Context, tenantID, departmentID int) (int, error) {
	if departmentID == 0 {
		return 0, fmt.Errorf("department id is required")
	}

	budget := r.subtreeNodeBudget()
	visited := map[int]bool{}
	frontier := []int{departmentID}
	total := 0
	expanded := 0

	for len(frontier) > 0 {
		level := make([]int, 0, len(frontier))
		for _, id := range frontier {
			if visited[id] {
				continue
			}
			visited[id] = true
			level = append(level, id)
		}
		if len(level) == 0 {
			break
		}

		count, err := r.client.User.Query().
			Where(
				user.TenantIDEQ(tenantID),
				user.ActiveEQ(true),
				user.DepartmentIDIn(level...),
			).
			Count(ctx)
		if err != nil {
			return 0, err
		}
		total += count

		expanded += len(level)
		if expanded >= budget {
			return 0, fmt.Errorf("department subtree exceeds the %d-node budget; count a narrower range instead", budget)
		}

		children, err := r.client.Department.Query().
			Where(
				department.TenantIDEQ(tenantID),
				department.DeletedAtIsNil(),
				department.ParentIDIn(level...),
			).
			Select(department.FieldID).
			All(ctx)
		if err != nil {
			return 0, err
		}

		next := make([]int, 0, len(children))
		for _, child := range children {
			if !visited[child.ID] {
				next = append(next, child.ID)
			}
		}
		frontier = next
	}
	return total, nil
}
