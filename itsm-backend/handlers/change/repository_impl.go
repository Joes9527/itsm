package change

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	entpredicate "itsm-backend/ent/predicate"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	entticket "itsm-backend/ent/ticket"
	entuser "itsm-backend/ent/user"

	entsql "entgo.io/ent/dialect/sql"
)

type EntRepository struct {
	client *ent.Client
	db     *sql.DB
}

func changeTenantScope(tenantID int, extra ...entpredicate.Ticket) entpredicate.Change {
	predicates := []entpredicate.Ticket{entticket.TenantIDEQ(tenantID), entticket.DeletedAtIsNil()}
	predicates = append(predicates, extra...)
	return change.HasWorkItemWith(predicates...)
}

func NewEntRepository(client *ent.Client, db *sql.DB) *EntRepository {
	return &EntRepository{
		client: client,
		db:     db,
	}
}

// Map base persistence fields; the application owns current-actor relation projection.
func toDomain(ec *ent.Change) *Change {
	if ec == nil {
		return nil
	}
	workItem := ec.Edges.WorkItem
	if workItem == nil {
		return nil
	}
	c := &Change{
		Number:             workItem.TicketNumber,
		ID:                 ec.ID,
		Title:              workItem.Title,
		Description:        workItem.Description,
		Justification:      ec.Justification,
		Type:               ec.Type,
		Status:             workItem.Status,
		Version:            workItem.Version,
		Priority:           workItem.Priority,
		ImpactScope:        ec.ImpactScope,
		RiskLevel:          ec.RiskLevel,
		CreatedBy:          workItem.OpenedByID,
		TenantID:           workItem.TenantID,
		PlannedStartDate:   &ec.PlannedStartDate,
		PlannedEndDate:     &ec.PlannedEndDate,
		ActualStartDate:    &ec.ActualStartDate,
		ActualEndDate:      &ec.ActualEndDate,
		Outcome:            ec.Outcome,
		OutcomeEvidence:    ec.OutcomeEvidence,
		ReviewEvidence:     ec.ReviewEvidence,
		ReviewedBy:         ec.ReviewedBy,
		ReviewedAt:         ec.ReviewedAt,
		StandardTemplateID: ec.StandardTemplateID,
		ImplementationPlan: ec.ImplementationPlan,
		RollbackPlan:       ec.RollbackPlan,
		AffectedCIs:        ec.AffectedCis,
		CreatedAt:          workItem.CreatedAt,
		UpdatedAt:          workItem.UpdatedAt,
	}
	if c.CreatedBy == 0 {
		c.CreatedBy = workItem.RequesterID
	}
	if workItem.AssigneeID > 0 {
		assigneeID := workItem.AssigneeID
		c.AssigneeID = &assigneeID
	}
	if ec.WorkItemID != 0 {
		id := ec.WorkItemID
		c.WorkItemID = &id
	}
	return c
}

// hydrateUsers loads all users referenced by the supplied changes in one
// tenant-scoped query. Change currently stores user IDs without Ent edges, so
// this provides the domain associations without introducing N+1 queries.
func (r *EntRepository) hydrateUsers(ctx context.Context, changes []*Change, tenantID int) error {
	userIDs := make(map[int]struct{})
	for _, c := range changes {
		if c == nil {
			continue
		}
		if c.CreatedBy > 0 {
			userIDs[c.CreatedBy] = struct{}{}
		}
		if c.AssigneeID != nil && *c.AssigneeID > 0 {
			userIDs[*c.AssigneeID] = struct{}{}
		}
	}
	if len(userIDs) == 0 {
		return nil
	}

	ids := make([]int, 0, len(userIDs))
	for id := range userIDs {
		ids = append(ids, id)
	}
	users, err := r.client.User.Query().
		Where(entuser.IDIn(ids...), entuser.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		return err
	}

	usersByID := make(map[int]*User, len(users))
	for _, u := range users {
		usersByID[u.ID] = &User{ID: u.ID, Name: u.Name}
	}
	for _, c := range changes {
		if c == nil {
			continue
		}
		c.CreatedByUser = usersByID[c.CreatedBy]
		if c.AssigneeID != nil {
			c.Assignee = usersByID[*c.AssigneeID]
		}
	}
	return nil
}

func (r *EntRepository) Get(ctx context.Context, id int, tenantID int) (*Change, error) {
	ec, err := r.client.Change.Query().
		Where(change.ID(id), changeTenantScope(tenantID)).
		WithWorkItem().
		First(ctx)
	if err != nil {
		return nil, err
	}
	result := toDomain(ec)
	if err := r.hydrateUsers(ctx, []*Change{result}, tenantID); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string, scope ...entpredicate.Ticket) ([]*Change, int, error) {
	q := r.client.Change.Query().Where(changeTenantScope(tenantID, scope...))

	if status != "" && status != "全部" {
		q = q.Where(change.HasWorkItemWith(entticket.StatusEQ(status)))
	}
	if riskLevel != "" && riskLevel != "全部" {
		q = q.Where(change.RiskLevel(riskLevel))
	}
	if search != "" {
		q = q.Where(change.HasWorkItemWith(entticket.Or(entticket.TitleContains(search), entticket.DescriptionContains(search))))
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	ecs, err := q.WithWorkItem().Order(change.ByWorkItemField(entticket.FieldCreatedAt, entsql.OrderDesc())).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	var results []*Change
	for _, ec := range ecs {
		results = append(results, toDomain(ec))
	}
	if err := r.hydrateUsers(ctx, results, tenantID); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

func (r *EntRepository) GetStats(ctx context.Context, tenantID int) (*Stats, error) {
	stats := &Stats{}

	// Total
	total, err := r.client.Change.Query().Where(changeTenantScope(tenantID)).Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.Total = total

	// Single GROUP BY query instead of 11 sequential COUNT queries
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.status, COALESCE(c.outcome, ''), COUNT(*)
		FROM changes c
		JOIN tickets t ON t.id = c.work_item_id
		WHERE t.tenant_id = $1 AND t.deleted_at IS NULL
		GROUP BY t.status, c.outcome
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status, outcome string
		var count int
		if err := rows.Scan(&status, &outcome, &count); err != nil {
			return nil, err
		}
		switch outcome {
		case "successful":
			stats.SuccessfulOutcomes += count
		case "failed":
			stats.FailedOutcomes += count
		case "rolled_back":
			stats.RolledBackOutcomes += count
		}
		switch status {
		case "draft":
			stats.Draft += count
		case "pending", "submitted":
			stats.Pending += count
		case "pending_review":
			// pending_review is a seed-data alias for pending (changes awaiting approval)
			stats.Pending += count
		case "approved":
			stats.Approved += count
		case "scheduled":
			stats.Scheduled += count
		case "in_progress":
			stats.InProgress += count
		case "completed":
			stats.Completed += count
		case "failed":
			stats.Failed += count
		case "rolled_back":
			stats.RolledBack += count
		case "rejected":
			stats.Rejected += count
		case "cancelled":
			stats.Cancelled += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// InProgress reflects changes actively being implemented (status='in_progress').
	// Scheduled is reported separately so the frontend can distinguish "已排期" from "实施中".
	// (The previous implementation summed Scheduled + Implementing, but Implementing was
	// never written anywhere — see canonical statuses in dto.ChangeStatus and the
	// canonical change status definitions.)

	return stats, nil
}

// resolveWorkItemID 返回一个变更关联的 WorkItem ID（tickets.id）——Wave 2 起这是 BPMN
// businessKey/ProcessApprovalDecision.BusinessID 的权威身份来源，不再是 changeID 自己。
// 返回 0 且非 nil error 表示这条变更不存在、不属于当前租户，或者开发数据违反
// WorkItem 创建不变量。调用方（GetApprovalHistory/pendingApprovalRecord）据此决定读路径
// 是否有可用的 WorkItem 身份；写路径必须 fail closed。
func (r *EntRepository) resolveWorkItemID(ctx context.Context, changeID, tenantID int) (int, error) {
	c, err := r.client.Change.Query().
		Where(change.ID(changeID), changeTenantScope(tenantID)).
		Only(ctx)
	if err != nil {
		return 0, err
	}
	if c.WorkItemID <= 0 {
		return 0, fmt.Errorf("change %d has no linked work item yet", changeID)
	}
	return c.WorkItemID, nil
}

// GetApprovalHistory 读取审批历史。数据源是 BPMN 引擎写入的
// ent.ProcessApprovalDecision 审计表（Track4 把 change 的 CAB 审批决策路径
// 迁移到 BPMN 之后，每次 TransitionStatus 的 approve/reject 都会在这张表
// 落一条记录），不再是 change_approvals 表——那张表的写入路径已经在
// SubmitChange/TransitionStatus 里被下线，留着旧查询会读到空数据。
// DTO 形状（ApprovalRecord）保持不变，前端 ChangeDetail.tsx 不用改。
func (r *EntRepository) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	workItemID, err := r.resolveWorkItemID(ctx, changeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("解析变更 WorkItem 身份失败: %w", err)
	}

	decisions, err := r.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType(string(dto.BusinessTypeChangeRequest)),
			processapprovaldecision.BusinessID(fmt.Sprintf("%d", workItemID)),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询审批历史失败: %w", err)
	}

	// 返回空切片而非 nil，避免 JSON 序列化为 null 导致前端崩溃
	records := make([]*ApprovalRecord, 0, len(decisions))
	for _, d := range decisions {
		var comment *string
		if d.Comment != "" {
			c := d.Comment
			comment = &c
		}
		createdAt := d.CreatedAt
		// ApprovedAt 只在决策是"通过"时才有意义——驳回记录也套用同一个时间戳会让
		// 调用方误以为一条 rejected 记录同时也是"批准时间"。
		var approvedAt *time.Time
		if d.Decision == "approved" {
			approvedAt = &createdAt
		}
		records = append(records, &ApprovalRecord{
			ID:           d.ID,
			ChangeID:     changeID,
			TenantID:     tenantID,
			ApproverID:   d.ActorID,
			ApproverName: d.ActorName,
			Status:       d.Decision,
			Comment:      comment,
			ApprovedAt:   approvedAt,
			CreatedAt:    createdAt,
		})
	}

	if pending := r.pendingApprovalRecord(ctx, changeID, tenantID); pending != nil {
		records = append(records, pending)
	}
	return records, nil
}

// pendingApprovalRecord 查这个变更当前是否卡在 CAB 审批这一步，如果是，合成一条
// Status="pending" 的 ApprovalRecord 附加到审批历史末尾。ProcessApprovalDecision
// 只记录已经做出的决策，审批人做出决定之前审批列表天然是空的——对调用方（前端审批
// 详情页）来说这看起来像"没人在审批"，实际是"正在等 CAB 决定"。返回 nil 表示当前
// 没有待处理的 CAB 审批（没有运行中的流程实例、变更还没有关联的 WorkItem、或者流程
// 还没推进到这一步、或者已经走完了）——这些情况不是错误，静默跳过，不影响已有的审批
// 历史返回。
func (r *EntRepository) pendingApprovalRecord(ctx context.Context, changeID, tenantID int) *ApprovalRecord {
	workItemID, err := r.resolveWorkItemID(ctx, changeID, tenantID)
	if err != nil {
		return nil
	}
	businessKey, identityErr := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, workItemID)
	if identityErr != nil {
		return nil
	}
	instance, err := r.client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
		Only(ctx)
	if err != nil {
		return nil
	}

	task, err := r.client.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
			processtask.TaskType("user_task"),
			processtask.TaskDefinitionKey("Activity_CABApproval"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		return nil
	}

	// CandidateUsers 是 resolveRoleCandidates 展开好的候选人显示名 CSV（username，
	// 缺失兜底 email/ID），角色未解析到候选人时可能落到 CandidateGroups；两个都拿不到
	// 就退化成裸角色名，好过什么都不显示。
	approverName := task.CandidateUsers
	if approverName == "" {
		approverName = task.CandidateGroups
	}
	if approverName == "" {
		approverName = "change_manager"
	}

	createdAt := task.CreatedTime
	return &ApprovalRecord{
		ID:           0, // 合成记录，没有真实的 ProcessApprovalDecision.ID
		ChangeID:     changeID,
		TenantID:     tenantID,
		ApproverName: approverName,
		Status:       "pending",
		CreatedAt:    createdAt,
	}
}

// Risk Assessment (Raw SQL)
// ListByDateRange retrieves changes within a date range
func (r *EntRepository) ListByDateRange(ctx context.Context, tenantID int, startDate, endDate, status string) ([]*Change, error) {
	// Parse date range
	start, err1 := time.Parse("2006-01-02", startDate)
	end, err2 := time.Parse("2006-01-02", endDate)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("invalid date format")
	}
	end = end.Add(24*time.Hour - time.Second) // End of day

	query := r.client.Change.Query().
		Where(changeTenantScope(tenantID))

	if status != "" {
		query = query.Where(change.HasWorkItemWith(entticket.StatusEQ(status)))
	}

	// Filter by planned date range in memory
	changes, err := query.WithWorkItem().All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]*Change, 0)
	for _, c := range changes {
		if !c.PlannedStartDate.IsZero() && !c.PlannedEndDate.IsZero() {
			// Check if date ranges overlap
			if (c.PlannedStartDate.Before(end) || c.PlannedStartDate.Equal(end)) &&
				(c.PlannedEndDate.After(start) || c.PlannedEndDate.Equal(start)) {
				result = append(result, toDomain(c))
			}
		}
	}

	return result, nil
}
