package approver

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// PersonalManagerResolver 顺着申请人自己的真实汇报链（user.manager_id）向上爬，找到第一个
// 职位头衔（job_title）包含"总经理"的人作为审批人。矩阵组织下同一个部门节点常年并存多条
// 业务线的平级总经理，DeptManagerResolver（部门维度，同一个节点对所有人给出同一个 answer）
// 在这种场景下无法唯一定位；PersonalManagerResolver 按人（顺着提交人自己的真实关系链）解析，
// 天然按业务线区分，不需要额外的业务线匹配规则。设计详见
// docs/superpowers/specs/2026-08-20-personal-manager-chain-approval-design.md。
type PersonalManagerResolver struct{}

// NewPersonalManagerResolver creates a new PersonalManagerResolver
func NewPersonalManagerResolver() *PersonalManagerResolver {
	return &PersonalManagerResolver{}
}

// GetType returns the resolver type
func (r *PersonalManagerResolver) GetType() string {
	return "personal_manager_gm"
}

// Resolve resolves the nearest GM-titled person in the requester's personal reporting chain
func (r *PersonalManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	if appCtx.RequesterID == 0 {
		return nil, fmt.Errorf("requester_id is required for personal_manager_gm resolver")
	}
	return r.resolve(ctx, client, appCtx.RequesterID, appCtx.TenantID, make(map[int]bool))
}

// resolve 是 Resolve 的内部实现，多带一个 visited 集合用来检测汇报链里的环——理论上
// manager_id 不该成环，但 HR 数据录入错误可能造成环，原来的纯递归会无限循环到栈溢出。
// 比照 DeptManagerResolver 的环路检测模式（service/approver/dept_manager_resolver.go）。
func (r *PersonalManagerResolver) resolve(ctx context.Context, client *ent.Client, userID, tenantID int, visited map[int]bool) ([]*ApproverInfo, error) {
	if visited[userID] {
		return nil, fmt.Errorf("reporting chain cycle detected at user %d", userID)
	}
	visited[userID] = true

	u, err := client.User.Query().
		Where(user.IDEQ(userID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("user not found: %d", userID)
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if u.ManagerID == 0 {
		return nil, fmt.Errorf("no GM-level manager found in reporting chain starting at user %d", userID)
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(u.ManagerID), user.TenantIDEQ(tenantID), user.Active(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("manager user not found or inactive: %d", u.ManagerID)
		}
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	if strings.Contains(manager.JobTitle, "总经理") {
		return []*ApproverInfo{
			{
				UserID:    manager.ID,
				UserName:  manager.Name,
				UserEmail: manager.Email,
				Role:      "personal_manager_gm",
				Source:    fmt.Sprintf("reporting_chain:%d", userID),
			},
		}, nil
	}

	return r.resolve(ctx, client, manager.ID, tenantID, visited)
}
