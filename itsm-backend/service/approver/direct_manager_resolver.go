package approver

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// DirectManagerResolver 沿**个人汇报链**（users.manager_id）上溯解析审批人。
//
// level 语义：
//   - level=0 → 直属上级（第 1 跳）
//   - level=N → 从第 N 级往上（第 N+1 跳起）
//
// 命中规则：从请求的那一级开始**继续往上**，取第一个在职的人。这样"上级已离职"
// 不会让审批断流，而是自然上溯（与设计"先往上找，最后兜底组"一致）。
//
// 未命中（链到顶、撞到既有脏环、租户内查不到）一律返回空，
// **绝不返回申请人自己**——调用方会转兜底路径。
//
// 注意这条链是**汇报线**（人），与 departments.manager_id 那条组织负责人的轴无关。
type DirectManagerResolver struct {
	level int
}

func NewDirectManagerResolver(level int) *DirectManagerResolver {
	if level < 0 {
		level = 0
	}
	return &DirectManagerResolver{level: level}
}

func (r *DirectManagerResolver) GetType() string { return string(ReasonDirectManager) }

func (r *DirectManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	if appCtx == nil || appCtx.RequesterID == 0 {
		return nil, nil
	}

	lookup := func(id int) (*ent.User, error) {
		return client.User.Query().
			Where(user.IDEQ(id), user.TenantIDEQ(appCtx.TenantID)).
			Only(ctx)
	}

	requester, err := lookup(appCtx.RequesterID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	visited := map[int]bool{requester.ID: true}
	cursor := requester
	hop := 0

	for {
		next := cursor.ManagerID
		if next == 0 {
			return nil, nil // 链到顶，未命中
		}
		if visited[next] {
			return nil, nil // 既有脏环：终止上溯，不让它把解析变成死循环
		}
		visited[next] = true
		hop++

		manager, err := lookup(next)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		if hop > r.level && manager.Active {
			return []*ApproverInfo{{
				UserID:    manager.ID,
				UserName:  manager.Name,
				UserEmail: manager.Email,
				Source:    string(ReasonDirectManager),
			}}, nil
		}
		cursor = manager
	}
}

var _ ApproverResolver = (*DirectManagerResolver)(nil)
