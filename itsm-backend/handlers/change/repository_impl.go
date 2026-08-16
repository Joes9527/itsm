package change

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	entuser "itsm-backend/ent/user"
)

type EntRepository struct {
	client *ent.Client
	db     *sql.DB
}

func NewEntRepository(client *ent.Client, db *sql.DB) *EntRepository {
	return &EntRepository{
		client: client,
		db:     db,
	}
}

// Map ent entity to domain entity
func toDomain(ec *ent.Change) *Change {
	if ec == nil {
		return nil
	}
	return &Change{
		ID:                 ec.ID,
		Title:              ec.Title,
		Description:        ec.Description,
		Justification:      ec.Justification,
		Type:               ec.Type,
		Status:             ec.Status,
		Priority:           ec.Priority,
		ImpactScope:        ec.ImpactScope,
		RiskLevel:          ec.RiskLevel,
		AssigneeID:         &ec.AssigneeID,
		CreatedBy:          ec.CreatedBy,
		TenantID:           ec.TenantID,
		PlannedStartDate:   &ec.PlannedStartDate,
		PlannedEndDate:     &ec.PlannedEndDate,
		ActualStartDate:    &ec.ActualStartDate,
		ActualEndDate:      &ec.ActualEndDate,
		ImplementationPlan: ec.ImplementationPlan,
		RollbackPlan:       ec.RollbackPlan,
		AffectedCIs:        ec.AffectedCis,
		RelatedTickets:     ec.RelatedTickets,
		CreatedAt:          ec.CreatedAt,
		UpdatedAt:          ec.UpdatedAt,
	}
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

func (r *EntRepository) Create(ctx context.Context, c *Change) (*Change, error) {
	ec, err := r.client.Change.Create().
		SetTitle(c.Title).
		SetDescription(c.Description).
		SetJustification(c.Justification).
		SetType(c.Type).
		SetStatus(c.Status).
		SetPriority(c.Priority).
		SetImpactScope(c.ImpactScope).
		SetRiskLevel(c.RiskLevel).
		SetCreatedBy(c.CreatedBy).
		SetTenantID(c.TenantID).
		SetImplementationPlan(c.ImplementationPlan).
		SetRollbackPlan(c.RollbackPlan).
		SetNillablePlannedStartDate(c.PlannedStartDate).
		SetNillablePlannedEndDate(c.PlannedEndDate).
		SetAffectedCis(c.AffectedCIs).
		SetRelatedTickets(c.RelatedTickets).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	result := toDomain(ec)
	if err := r.hydrateUsers(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *EntRepository) Get(ctx context.Context, id int, tenantID int) (*Change, error) {
	ec, err := r.client.Change.Query().
		Where(change.ID(id), change.TenantID(tenantID)).
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

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error) {
	q := r.client.Change.Query().Where(change.TenantID(tenantID))

	if status != "" && status != "全部" {
		q = q.Where(change.Status(status))
	}
	if riskLevel != "" && riskLevel != "全部" {
		q = q.Where(change.RiskLevel(riskLevel))
	}
	if search != "" {
		q = q.Where(change.Or(
			change.TitleContains(search),
			change.DescriptionContains(search),
		))
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	ecs, err := q.Order(ent.Desc(change.FieldCreatedAt)).
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

func (r *EntRepository) Update(ctx context.Context, c *Change) (*Change, error) {
	update := r.client.Change.UpdateOneID(c.ID).
		SetTitle(c.Title).
		SetDescription(c.Description).
		SetJustification(c.Justification).
		SetType(c.Type).
		SetStatus(c.Status).
		SetPriority(c.Priority).
		SetImpactScope(c.ImpactScope).
		SetRiskLevel(c.RiskLevel).
		SetImplementationPlan(c.ImplementationPlan).
		SetRollbackPlan(c.RollbackPlan).
		SetAffectedCis(c.AffectedCIs).
		SetRelatedTickets(c.RelatedTickets)

	if c.AssigneeID != nil {
		update.SetAssigneeID(*c.AssigneeID)
	}
	if c.PlannedStartDate != nil {
		update.SetPlannedStartDate(*c.PlannedStartDate)
	}
	if c.PlannedEndDate != nil {
		update.SetPlannedEndDate(*c.PlannedEndDate)
	}
	if c.ActualStartDate != nil {
		update.SetActualStartDate(*c.ActualStartDate)
	}
	if c.ActualEndDate != nil {
		update.SetActualEndDate(*c.ActualEndDate)
	}

	ec, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	result := toDomain(ec)
	if err := r.hydrateUsers(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *EntRepository) Delete(ctx context.Context, id int, tenantID int) error {
	_, err := r.client.Change.Delete().
		Where(change.ID(id), change.TenantID(tenantID)).
		Exec(ctx)
	return err
}

func (r *EntRepository) GetStats(ctx context.Context, tenantID int) (*Stats, error) {
	stats := &Stats{}

	// Total
	total, err := r.client.Change.Query().Where(change.TenantID(tenantID)).Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.Total = total

	// Single GROUP BY query instead of 11 sequential COUNT queries
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM changes
		WHERE tenant_id = $1
		GROUP BY status
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		switch status {
		case "draft":
			stats.Draft = count
		case "pending":
			stats.Pending += count
		case "pending_review":
			// pending_review is a seed-data alias for pending (changes awaiting approval)
			stats.Pending += count
		case "approved":
			stats.Approved = count
		case "scheduled":
			stats.Scheduled = count
		case "in_progress":
			stats.InProgress = count
		case "completed":
			stats.Completed = count
		case "failed":
			stats.Failed = count
		case "rolled_back":
			stats.RolledBack = count
		case "rejected":
			stats.Rejected = count
		case "cancelled":
			stats.Cancelled = count
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

// MarkSubmittedForApproval 只做 draft -> pending 的状态转换，不写
// change_approvals/change_approval_chains（这两张表的写入路径正在被
// Track4 迁移到 BPMN，见 handlers/change/service.go 的 SubmitChange）。
// 用跟 SubmitForApproval 相同的乐观守卫：要求恰好 1 行受影响，否则说明
// change 已经不是 draft 状态了。
func (r *EntRepository) MarkSubmittedForApproval(ctx context.Context, changeID, tenantID int) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE changes SET status = 'pending', updated_at = $1
		 WHERE id = $2 AND tenant_id = $3 AND status = 'draft'`,
		time.Now(), changeID, tenantID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("change is not an editable draft")
	}
	return nil
}

// GetApprovalHistory 读取审批历史。数据源是 BPMN 引擎写入的
// ent.ProcessApprovalDecision 审计表（Track4 把 change 的 CAB 审批决策路径
// 迁移到 BPMN 之后，每次 TransitionStatus 的 approve/reject 都会在这张表
// 落一条记录），不再是 change_approvals 表——那张表的写入路径已经在
// SubmitChange/TransitionStatus 里被下线，留着旧查询会读到空数据。
// DTO 形状（ApprovalRecord）保持不变，前端 ChangeDetail.tsx 不用改。
func (r *EntRepository) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	decisions, err := r.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType("change"),
			processapprovaldecision.BusinessID(fmt.Sprintf("%d", changeID)),
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
		records = append(records, &ApprovalRecord{
			ID:           d.ID,
			ChangeID:     changeID,
			TenantID:     tenantID,
			ApproverID:   d.ActorID,
			ApproverName: d.ActorName,
			Status:       d.Decision,
			Comment:      comment,
			ApprovedAt:   &createdAt,
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
// 没有待处理的 CAB 审批（没有运行中的流程实例、或者流程还没推进到这一步、或者已经
// 走完了）——这些情况不是错误，静默跳过，不影响已有的审批历史返回。
func (r *EntRepository) pendingApprovalRecord(ctx context.Context, changeID, tenantID int) *ApprovalRecord {
	businessKey := fmt.Sprintf("change:%d", changeID)
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
func (r *EntRepository) CreateRiskAssessment(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error) {
	query := `
		INSERT INTO change_risk_assessments (
			change_id, tenant_id, risk_level, risk_description, impact_analysis,
			mitigation_measures, contingency_plan, risk_owner, risk_review_date,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at
	`
	now := time.Now()
	err := r.db.QueryRowContext(ctx, query,
		ra.ChangeID, ra.TenantID, ra.RiskLevel, ra.RiskDescription, ra.ImpactAnalysis,
		ra.MitigationMeasures, ra.ContingencyPlan, ra.RiskOwner, ra.RiskReviewDate,
		now, now).
		Scan(&ra.ID, &ra.CreatedAt)
	if err != nil {
		return nil, err
	}
	ra.UpdatedAt = now
	return ra, nil
}

func (r *EntRepository) GetRiskAssessment(ctx context.Context, changeID int, tenantID int) (*RiskAssessment, error) {
	query := `
		SELECT id, tenant_id, risk_level, risk_description, impact_analysis,
		       mitigation_measures, contingency_plan, risk_owner, risk_review_date,
		       created_at, updated_at
		FROM change_risk_assessments 
		WHERE change_id = $1 AND tenant_id = $2
	`
	var ra RiskAssessment
	var riskReviewDate sql.NullTime
	err := r.db.QueryRowContext(ctx, query, changeID, tenantID).Scan(
		&ra.ID, &ra.TenantID, &ra.RiskLevel, &ra.RiskDescription, &ra.ImpactAnalysis,
		&ra.MitigationMeasures, &ra.ContingencyPlan, &ra.RiskOwner, &riskReviewDate,
		&ra.CreatedAt, &ra.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found is not an error here
		}
		return nil, err
	}
	ra.ChangeID = changeID
	if riskReviewDate.Valid {
		ra.RiskReviewDate = &riskReviewDate.Time
	}
	return &ra, nil
}

func (r *EntRepository) UpdateRiskAssessment(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error) {
	query := `
		UPDATE change_risk_assessments
		SET risk_level = $1, risk_description = $2, impact_analysis = $3,
		    mitigation_measures = $4, contingency_plan = $5, risk_owner = $6,
		    risk_review_date = $7, updated_at = $8
		WHERE change_id = $9 AND tenant_id = $10
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(
		ctx, query,
		ra.RiskLevel, ra.RiskDescription, ra.ImpactAnalysis,
		ra.MitigationMeasures, ra.ContingencyPlan, ra.RiskOwner,
		ra.RiskReviewDate, time.Now(), ra.ChangeID, ra.TenantID,
	).Scan(&ra.ID, &ra.CreatedAt, &ra.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return ra, nil
}

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
		Where(change.TenantID(tenantID))

	if status != "" {
		query = query.Where(change.Status(status))
	}

	// Filter by planned date range in memory
	changes, err := query.All(ctx)
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
