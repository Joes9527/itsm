package change

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	entpredicate "itsm-backend/ent/predicate"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	entticket "itsm-backend/ent/ticket"
	entuser "itsm-backend/ent/user"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/repository/workitemnumber"

	entsql "entgo.io/ent/dialect/sql"
	"go.uber.org/zap"
)

// changeTicketRelationType 是 Change 关联普通工单（自由文本 relatedTickets，无方向性的
// 一般关联）在 WorkItemRelation 里使用的 relation_type。不是 requested_change——那是
// design doc §10.2 特指 "Requested Item → Change" 这个方向的关系，语义不同，ServiceRequest
// 域自己的 Wave 2 任务负责，这里不能借用。
const changeTicketRelationType = "related_to"

type EntRepository struct {
	client          *ent.Client
	db              *sql.DB
	numberAllocator workitemnumber.Allocator
}

func changeTenantScope(tenantID int, extra ...entpredicate.Ticket) entpredicate.Change {
	predicates := []entpredicate.Ticket{entticket.TenantIDEQ(tenantID), entticket.DeletedAtIsNil()}
	predicates = append(predicates, extra...)
	return change.HasWorkItemWith(predicates...)
}

func NewEntRepository(client *ent.Client, db *sql.DB, numberAllocator workitemnumber.Allocator) *EntRepository {
	return &EntRepository{
		client:          client,
		db:              db,
		numberAllocator: numberAllocator,
	}
}

// Map ent entity to domain entity. RelatedTickets 不在这里填充——Wave 2 起权威来源是
// WorkItemRelation（见 hydrateRelatedTickets），调用方需要它时显式调用 hydrate。
func toDomain(ec *ent.Change) *Change {
	if ec == nil {
		return nil
	}
	workItem := ec.Edges.WorkItem
	if workItem == nil {
		return nil
	}
	c := &Change{
		ID:                 ec.ID,
		Title:              workItem.Title,
		Description:        workItem.Description,
		Justification:      ec.Justification,
		Type:               ec.Type,
		Status:             workItem.Status,
		Priority:           workItem.Priority,
		ImpactScope:        ec.ImpactScope,
		RiskLevel:          ec.RiskLevel,
		CreatedBy:          workItem.OpenedByID,
		TenantID:           workItem.TenantID,
		PlannedStartDate:   &ec.PlannedStartDate,
		PlannedEndDate:     &ec.PlannedEndDate,
		ActualStartDate:    &ec.ActualStartDate,
		ActualEndDate:      &ec.ActualEndDate,
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

// hydrateRelatedTickets 用 WorkItemRelation（relation_type="related_to"）填充
// Change.RelatedTickets 使用 WorkItemRelation（relation_type="related_to"）作为唯一权威来源。
// 返回值按目标工单的 ticket_number 字符串组装，保持 dto.ChangeResponse.RelatedTickets
// "相关工单编号" 的既有契约不变。没有 WorkItemID 的 Change（无效开发数据）不可能有任何
// WorkItemRelation 指向它，直接跳过。
func (r *EntRepository) hydrateRelatedTickets(ctx context.Context, changes []*Change, tenantID int) error {
	sourceIDs := make([]int, 0, len(changes))
	bySource := make(map[int][]*Change)
	for _, c := range changes {
		if c == nil || c.WorkItemID == nil || *c.WorkItemID <= 0 {
			continue
		}
		sourceIDs = append(sourceIDs, *c.WorkItemID)
		bySource[*c.WorkItemID] = append(bySource[*c.WorkItemID], c)
	}
	if len(sourceIDs) == 0 {
		return nil
	}

	relations, err := r.client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenantID),
			workitemrelation.SourceWorkItemIDIn(sourceIDs...),
			workitemrelation.RelationType(changeTicketRelationType),
			workitemrelation.DeletedAtIsNil(),
		).
		Order(ent.Asc(workitemrelation.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query related ticket relations: %w", err)
	}
	if len(relations) == 0 {
		return nil
	}

	targetIDs := make([]int, 0, len(relations))
	for _, rel := range relations {
		targetIDs = append(targetIDs, rel.TargetWorkItemID)
	}
	targets, err := r.client.Ticket.Query().
		Where(entticket.IDIn(targetIDs...), entticket.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query related tickets: %w", err)
	}
	numberByID := make(map[int]string, len(targets))
	for _, t := range targets {
		numberByID[t.ID] = t.TicketNumber
	}

	for _, rel := range relations {
		number, ok := numberByID[rel.TargetWorkItemID]
		if !ok {
			// 目标工单不属于当前租户或已不存在（不应该发生——写入时已经做过租户过滤，
			// 防御性跳过，不让一条脏关系搞坏整个响应）。
			continue
		}
		for _, c := range bySource[rel.SourceWorkItemID] {
			c.RelatedTickets = append(c.RelatedTickets, number)
		}
	}
	return nil
}

// resolveTicketNumbers 把一组自由文本的工单编号解析成当前租户下真实存在的工单 ID。
// 查不到的编号被跳过并计入 unresolved 返回给调用方记录，不阻塞调用方的主流程——这是一个
// 业务判断（见交付说明"related_tickets 迁移"一节）：relatedTickets 是软性的辅助关联，
// 一个拼错/过期的工单编号不应该让整个变更创建/更新失败，不属于 AGENTS.md 要求 fail closed
// 的安全/租户边界范畴。
func (r *EntRepository) resolveTicketNumbers(ctx context.Context, client *ent.Client, tenantID int, numbers []string) (resolvedIDs []int, unresolved []string, err error) {
	if len(numbers) == 0 {
		return nil, nil, nil
	}
	seen := make(map[string]struct{}, len(numbers))
	ordered := make([]string, 0, len(numbers))
	for _, n := range numbers {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		ordered = append(ordered, n)
	}
	if len(ordered) == 0 {
		return nil, nil, nil
	}

	found, err := client.Ticket.Query().
		Where(entticket.TicketNumberIn(ordered...), entticket.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve related ticket numbers: %w", err)
	}
	byNumber := make(map[string]int, len(found))
	for _, t := range found {
		byNumber[t.TicketNumber] = t.ID
	}
	for _, n := range ordered {
		if id, ok := byNumber[n]; ok {
			resolvedIDs = append(resolvedIDs, id)
		} else {
			unresolved = append(unresolved, n)
		}
	}
	return resolvedIDs, unresolved, nil
}

// reconcileRelatedTicketRelations 把 sourceWorkItemID 名下的 related_to WorkItemRelation
// 收敛到 desiredTicketNumbers 描述的目标集合：软删除当前存在但不再被期望的关系，为期望但
// 尚不存在的目标新建关系。用"全量替换"而不是增量 diff——desiredTicketNumbers 语义上是
// PUT 语义的完整期望列表（见 handlers/change/handler.go 的 UpdateChange：只有
// req.RelatedTickets != nil 时才会覆盖
// existing.RelatedTickets，否则调用方传入的就是 GetChange 已经从 WorkItemRelation
// 水合出来的当前值，等价于"不变"）。actorUserID 用于新建关系的 created_by_id；Update
// 路径目前没有独立的"当前操作人"概念（handler.go 的 UpdateChange 没有从请求上下文提取
// user_id），退化用 Change 自己的创建人作为近似值，这是已知的不精确之处，在交付说明里说明。
func (r *EntRepository) reconcileRelatedTicketRelations(ctx context.Context, client *ent.Client, tenantID, sourceWorkItemID, actorUserID int, desiredTicketNumbers []string) error {
	resolvedIDs, unresolved, err := r.resolveTicketNumbers(ctx, client, tenantID, desiredTicketNumbers)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		zap.S().Warnw("变更关联工单编号未能解析，已跳过",
			"tenant_id", tenantID, "source_work_item_id", sourceWorkItemID, "unresolved_ticket_numbers", unresolved)
	}

	existing, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenantID),
			workitemrelation.SourceWorkItemID(sourceWorkItemID),
			workitemrelation.RelationType(changeTicketRelationType),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query existing related ticket relations: %w", err)
	}

	desired := make(map[int]struct{}, len(resolvedIDs))
	for _, id := range resolvedIDs {
		desired[id] = struct{}{}
	}
	existingByTarget := make(map[int]*ent.WorkItemRelation, len(existing))
	for _, rel := range existing {
		existingByTarget[rel.TargetWorkItemID] = rel
	}

	now := time.Now()
	for targetID, rel := range existingByTarget {
		if _, keep := desired[targetID]; keep {
			continue
		}
		if _, err := client.WorkItemRelation.UpdateOneID(rel.ID).
			SetDeletedAt(now).
			Save(ctx); err != nil {
			return fmt.Errorf("failed to remove stale related ticket relation: %w", err)
		}
	}
	for targetID := range desired {
		if _, exists := existingByTarget[targetID]; exists {
			continue
		}
		if _, err := client.WorkItemRelation.Create().
			SetTenantID(tenantID).
			SetSourceWorkItemID(sourceWorkItemID).
			SetTargetWorkItemID(targetID).
			SetRelationType(changeTicketRelationType).
			SetCreatedByID(actorUserID).
			Save(ctx); err != nil {
			return fmt.Errorf("failed to create related ticket relation: %w", err)
		}
	}
	return nil
}

// Create 在同一数据库事务内先建 tickets 行（record_class="change_request"，创建后不可变——
// 注意这个取值跟 Incident/Problem 不同，两者的 recordClass 恰好等于领域名本身，Change 的
// recordClass 是 "change_request"，见 ent/schema/ticket.go 的字段注释和
// cmd/check_work_item_integrity 的已知取值枚举），再建 changes 行并回填 work_item_id——
// 统一 WorkItem 领域模型宪章 §3.2 的事务边界约束，任一边失败整体回滚。模式与
// handlers/problem.EntRepository.Create / IncidentService.CreateIncident 一致。
//
// relatedTickets（自由文本工单编号数组）在同一事务内解析并写入 WorkItemRelation
// （relation_type="related_to"）；changes 表不保存关系 JSON 副本。
func (r *EntRepository) Create(ctx context.Context, c *Change) (*Change, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start change transaction: %w", err)
	}
	rollback := func(cause error) (*Change, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("%w (rollback also failed: %v)", cause, rbErr)
		}
		return nil, cause
	}

	// Ticket.requester_id 是一条指向 users 表的必填 FK edge，Change 自己的 created_by
	// 历史上没有这层约束。既然现在每条 Change 都会同步建一条 tickets 行并把 requester_id
	// 设成 Change 的创建人，这里必须显式校验创建人存在且属于同一租户——否则
	// tx.Ticket.Create 会因为 FK 违反直接失败，报错信息对调用方不友好（同
	// handlers/problem.EntRepository.Create 的 creatorExists 校验）。
	creatorExists, err := tx.User.Query().
		Where(entuser.IDEQ(c.CreatedBy), entuser.TenantIDEQ(c.TenantID), entuser.ActiveEQ(true)).
		Exist(ctx)
	if err != nil {
		return rollback(fmt.Errorf("failed to validate change creator: %w", err))
	}
	if !creatorExists {
		return rollback(fmt.Errorf("change creator not found or inactive"))
	}

	issuedAt := time.Now().UTC()
	ticketNumber, err := r.numberAllocator.Allocate(ctx, tx.Client(), c.TenantID, issuedAt)
	if err != nil {
		return rollback(fmt.Errorf("failed to allocate work item ticket number: %w", err))
	}

	workItem, err := tx.Ticket.Create().
		SetTitle(c.Title).
		SetDescription(c.Description).
		SetType("change").
		SetRecordClass("change_request").
		SetPriority(c.Priority).
		SetStatus(c.Status).
		SetTicketNumber(ticketNumber).
		SetRequesterID(c.CreatedBy).
		SetOpenedByID(c.CreatedBy).
		SetTenantID(c.TenantID).
		SetNillableAssigneeID(c.AssigneeID).
		SetCreatedAt(issuedAt).
		SetUpdatedAt(issuedAt).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("failed to create work item: %w", err))
	}

	create := tx.Change.Create().
		SetJustification(c.Justification).
		SetType(c.Type).
		SetImpactScope(c.ImpactScope).
		SetRiskLevel(c.RiskLevel).
		SetWorkItemID(workItem.ID).
		SetImplementationPlan(c.ImplementationPlan).
		SetRollbackPlan(c.RollbackPlan).
		SetNillablePlannedStartDate(c.PlannedStartDate).
		SetNillablePlannedEndDate(c.PlannedEndDate).
		SetAffectedCis(c.AffectedCIs)

	saved, err := create.Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("failed to create change: %w", err))
	}

	if err := r.reconcileRelatedTicketRelations(ctx, tx.Client(), c.TenantID, workItem.ID, c.CreatedBy, c.RelatedTickets); err != nil {
		return rollback(fmt.Errorf("failed to link related tickets: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("failed to commit change transaction: %w", err))
	}

	saved.Edges.WorkItem = workItem
	result := toDomain(saved)
	if err := r.hydrateUsers(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	if err := r.hydrateRelatedTickets(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	return result, nil
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
	if err := r.hydrateRelatedTickets(ctx, []*Change{result}, tenantID); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error) {
	q := r.client.Change.Query().Where(changeTenantScope(tenantID))

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
	if err := r.hydrateRelatedTickets(ctx, results, tenantID); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

// Update 在同一事务内更新专业字段、WorkItem 共享字段，并把 c.RelatedTickets 描述的期望
// 集合收敛到 WorkItemRelation（见 reconcileRelatedTicketRelations）。
func (r *EntRepository) Update(ctx context.Context, c *Change) (*Change, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start change update transaction: %w", err)
	}
	rollback := func(cause error) (*Change, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("%w (rollback also failed: %v)", cause, rbErr)
		}
		return nil, cause
	}

	current, err := tx.Change.Query().Where(change.IDEQ(c.ID), changeTenantScope(c.TenantID)).WithWorkItem().Only(ctx)
	if err != nil {
		return rollback(err)
	}
	update := tx.Change.UpdateOneID(c.ID).
		Where(changeTenantScope(c.TenantID)).
		SetJustification(c.Justification).
		SetType(c.Type).
		SetImpactScope(c.ImpactScope).
		SetRiskLevel(c.RiskLevel).
		SetImplementationPlan(c.ImplementationPlan).
		SetRollbackPlan(c.RollbackPlan).
		SetAffectedCis(c.AffectedCIs)

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
		return rollback(err)
	}
	workItemUpdate := tx.Ticket.UpdateOneID(ec.WorkItemID).
		Where(entticket.TenantIDEQ(c.TenantID), entticket.DeletedAtIsNil(), entticket.VersionEQ(current.Edges.WorkItem.Version)).
		SetTitle(c.Title).SetDescription(c.Description).SetStatus(c.Status).SetPriority(c.Priority).
		SetUpdatedAt(time.Now()).AddVersion(1)
	if c.AssigneeID == nil {
		workItemUpdate.ClearAssigneeID()
	} else {
		workItemUpdate.SetAssigneeID(*c.AssigneeID)
	}
	workItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("failed to update change work item: %w", err))
	}

	// 用 Change 自己的创建人作为关系写入的 actor 近似值——UpdateChange 目前没有
	// 独立的"当前操作人"概念可用，见 reconcileRelatedTicketRelations 顶部注释。
	if err := r.reconcileRelatedTicketRelations(ctx, tx.Client(), c.TenantID, ec.WorkItemID, c.CreatedBy, c.RelatedTickets); err != nil {
		return rollback(fmt.Errorf("failed to reconcile related tickets: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("failed to commit change update transaction: %w", err))
	}

	ec.Edges.WorkItem = workItem
	result := toDomain(ec)
	if err := r.hydrateUsers(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	if err := r.hydrateRelatedTickets(ctx, []*Change{result}, c.TenantID); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *EntRepository) Delete(ctx context.Context, id int, tenantID int) error {
	entity, err := r.client.Change.Query().Where(change.ID(id), changeTenantScope(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	_, err = r.client.Ticket.UpdateOneID(entity.WorkItemID).
		Where(entticket.TenantIDEQ(tenantID), entticket.DeletedAtIsNil()).
		SetDeletedAt(time.Now()).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	return err
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
		SELECT t.status, COUNT(*)
		FROM changes c
		JOIN tickets t ON t.id = c.work_item_id
		WHERE t.tenant_id = $1 AND t.deleted_at IS NULL
		GROUP BY t.status
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
	workItemID, err := r.resolveWorkItemID(ctx, changeID, tenantID)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE tickets SET status = 'pending', updated_at = $1
		 WHERE id = $2 AND tenant_id = $3 AND status = 'draft'`,
		time.Now(), workItemID, tenantID)
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
			processapprovaldecision.BusinessType("change"),
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
	businessKey := fmt.Sprintf("change:%d", workItemID)
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
