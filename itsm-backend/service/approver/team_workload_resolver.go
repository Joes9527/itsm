package approver

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/team"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
)

// TeamWorkloadResolver 把 taskPurpose="fulfillment" 任务的 assignee 解析为指定团队里当前工单
// 负载最低的 active 成员。跟审批用的 DeptManagerResolver/PersonalManagerResolver/TeamLeaderResolver
// 语义不同：那三个解析的是"谁有资格批"，这个解析的是"谁来干活"，所以不看 RequesterID/DepartmentID，
// 只看目标团队现在谁最闲。设计详见
// docs/superpowers/specs/2026-08-20-fulfillment-team-assignment-design.md。
type TeamWorkloadResolver struct{}

func NewTeamWorkloadResolver() *TeamWorkloadResolver {
	return &TeamWorkloadResolver{}
}

func (r *TeamWorkloadResolver) GetType() string {
	return "team_workload"
}

func (r *TeamWorkloadResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	if appCtx.TeamCode == "" {
		return nil, fmt.Errorf("team_code is required for team_workload resolver")
	}

	teamEntity, err := client.Team.Query().
		Where(team.CodeEQ(appCtx.TeamCode), team.TenantID(appCtx.TenantID), team.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("team not found: code=%s", appCtx.TeamCode)
		}
		return nil, fmt.Errorf("failed to query team: %w", err)
	}

	candidates, err := teamEntity.QueryUsers().
		Where(user.TenantIDEQ(appCtx.TenantID), user.ActiveEQ(true)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query team members: %w", err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no active member found in team: code=%s", appCtx.TeamCode)
	}

	candidateIDs := make([]int, 0, len(candidates))
	byID := make(map[int]*ent.User, len(candidates))
	for _, u := range candidates {
		candidateIDs = append(candidateIDs, u.ID)
		byID[u.ID] = u
	}

	// 批量聚合查询全体候选人的当前活跃工单数，一次 SQL 往返而不是每个候选人一次——
	// 这是本轮会话在 TicketAssignmentService 里已经验证过、能把 O(候选人数) 查询次数
	// 压成 O(1) 的写法，这里从一开始就用对，不留性能债。
	type assigneeCount struct {
		AssigneeID int `json:"assignee_id"`
		Count      int `json:"count"`
	}
	var counts []assigneeCount
	if err := client.Ticket.Query().
		Where(ticket.AssigneeIDIn(candidateIDs...), ticket.StatusIn("open", "in_progress", "pending")).
		GroupBy(ticket.FieldAssigneeID).
		Aggregate(ent.Count()).
		Scan(ctx, &counts); err != nil {
		return nil, fmt.Errorf("failed to count active tickets: %w", err)
	}

	loadByUser := make(map[int]int, len(candidateIDs))
	for _, c := range counts {
		loadByUser[c.AssigneeID] = c.Count
	}

	bestID := candidateIDs[0]
	bestLoad := loadByUser[bestID]
	for _, id := range candidateIDs[1:] {
		if loadByUser[id] < bestLoad {
			bestID = id
			bestLoad = loadByUser[id]
		}
	}

	best := byID[bestID]
	return []*ApproverInfo{{
		UserID:    best.ID,
		UserName:  best.Username,
		UserEmail: best.Email,
		Role:      "team_workload",
		Source:    fmt.Sprintf("team:%s", appCtx.TeamCode),
	}}, nil
}
