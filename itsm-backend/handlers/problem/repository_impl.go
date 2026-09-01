package problem

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	entpredicate "itsm-backend/ent/predicate"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/repository/workitemnumber"
)

// problemTicketRelationType 是 Problem 关联普通工单（record_class 未收敛的一般关联，
// 不是"调查根因"那条 investigated_by/caused_by 方向性关系）在 WorkItemRelation 里使用的
// relation_type。见 docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md §10。
const problemTicketRelationType = "related_to"

type EntRepository struct {
	client          *ent.Client
	numberAllocator workitemnumber.Allocator
}

func NewEntRepository(client *ent.Client, numberAllocator workitemnumber.Allocator) *EntRepository {
	return &EntRepository{client: client, numberAllocator: numberAllocator}
}

func (r *EntRepository) toDomain(e *ent.Problem) *Problem {
	if e == nil {
		return nil
	}
	p := &Problem{
		ID:          e.ID,
		Title:       e.Title,
		Description: e.Description,
		Status:      e.Status,
		Priority:    e.Priority,
		Category:    e.Category,
		RootCause:   e.RootCause,
		Workaround:  e.Workaround,
		Resolution:  e.Resolution,
		Impact:      e.Impact,
		CreatedBy:   e.CreatedBy,
		TenantID:    e.TenantID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
	if e.ResolvedAt != nil {
		p.ResolvedAt = e.ResolvedAt
	}
	if e.ClosedAt != nil {
		p.ClosedAt = e.ClosedAt
	}
	// Handle optional fields
	// Ent fields might be zero value if not set, or pointer depending on schema.
	// Schema says: AssigneeID optional.
	if e.AssigneeID != 0 {
		id := e.AssigneeID
		p.AssigneeID = &id
	}
	if e.WorkItemID != 0 {
		id := e.WorkItemID
		p.WorkItemID = &id
	}
	return p
}

func (r *EntRepository) toDomainWithAssociations(e *ent.Problem) *Problem {
	p := r.toDomain(e)

	// 注意：Tickets 关联不在这里填充——历史上通过 ent 的 Problem<->Ticket 多对多 edge
	// eager-load（e.Edges.Tickets），Wave 2 起改为从 WorkItemRelation 读取（见
	// GetWithAssociations 里对 loadTicketAssociations 的调用），因为普通工单关联现在是
	// WorkItem 层面的结构化关系，不再是 Problem 专业扩展表自己的 edge。
	if e.Edges.Incidents != nil {
		p.Incidents = make([]*AssociatedItem, 0, len(e.Edges.Incidents))
		for _, inc := range e.Edges.Incidents {
			p.Incidents = append(p.Incidents, &AssociatedItem{
				ID:     inc.ID,
				Title:  inc.Title,
				Status: inc.Status,
				Number: inc.IncidentNumber,
				Type:   "incident",
			})
		}
	}
	if e.Edges.Changes != nil {
		p.Changes = make([]*AssociatedItem, 0, len(e.Edges.Changes))
		for _, ch := range e.Edges.Changes {
			p.Changes = append(p.Changes, &AssociatedItem{
				ID:     ch.ID,
				Title:  ch.Title,
				Status: ch.Status,
				Type:   "change",
			})
		}
	}

	return p
}

func (r *EntRepository) AddAssociations(ctx context.Context, tenantID, problemID, actorUserID int, relatedType string, relatedIDs []int) error {
	prob, err := r.client.Problem.Query().
		Where(problem.IDEQ(problemID), problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("problem not found")
		}
		return err
	}

	switch relatedType {
	case "ticket":
		count, err := r.client.Ticket.Query().
			Where(ticket.IDIn(relatedIDs...), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).
			Count(ctx)
		if err != nil {
			return err
		}
		if count != len(relatedIDs) {
			return fmt.Errorf("one or more tickets do not belong to the current tenant")
		}
		if prob.WorkItemID <= 0 {
			return fmt.Errorf("problem %d has no linked work item yet; run cmd/backfill_problem_work_item first", problemID)
		}
		return r.linkTicketsAsWorkItemRelations(ctx, tenantID, prob.WorkItemID, actorUserID, relatedIDs)
	case "incident":
		count, err := r.client.Incident.Query().
			Where(incident.IDIn(relatedIDs...), incident.TenantIDEQ(tenantID)).
			Count(ctx)
		if err != nil {
			return err
		}
		if count != len(relatedIDs) {
			return fmt.Errorf("one or more incidents do not belong to the current tenant")
		}
	case "change":
		count, err := r.client.Change.Query().
			Where(change.IDIn(relatedIDs...), change.TenantIDEQ(tenantID)).
			Count(ctx)
		if err != nil {
			return err
		}
		if count != len(relatedIDs) {
			return fmt.Errorf("one or more changes do not belong to the current tenant")
		}
	default:
		return fmt.Errorf("unsupported related type: %s", relatedType)
	}

	// incident/change 分支保持迁移前的写法不变：仍然写 Problem<->Incident /
	// Problem<->Change 的 ent 多对多 edge。这两个方向本次任务范围内不迁移到
	// WorkItemRelation（Change 域尚未迁移到 WorkItem；Incident 方向对应的是设计文档里
	// investigated_by/caused_by 关系，跟这里"Problem 关联任意工单"的无方向性 related_to
	// 语义不同，混用会产生错误的关系类型）。
	update := r.client.Problem.Update().
		Where(problem.IDEQ(problemID), problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil())
	switch relatedType {
	case "incident":
		update.AddIncidentIDs(relatedIDs...)
	case "change":
		update.AddChangeIDs(relatedIDs...)
	}
	updated, err := update.Save(ctx)
	if err == nil && updated != 1 {
		return fmt.Errorf("problem not found")
	}
	return err
}

// linkTicketsAsWorkItemRelations 把 Problem 对普通工单的关联写入 WorkItemRelation
// （relation_type="related_to"），source 是 Problem 自己的 WorkItem（tickets.id），
// target 是被关联的工单本身（工单表本来就是 WorkItem 物理表，工单 ID 就是它的 WorkItem
// ID）。幂等：同一对 WorkItem 之间已存在未软删除的 related_to 关系时跳过，不报错——
// 与旧的 ent AddTicketIDs（m2m，重复添加是 no-op）行为一致。
func (r *EntRepository) linkTicketsAsWorkItemRelations(ctx context.Context, tenantID, sourceWorkItemID, actorUserID int, targetWorkItemIDs []int) error {
	for _, targetID := range targetWorkItemIDs {
		exists, err := r.client.WorkItemRelation.Query().
			Where(
				workitemrelation.TenantID(tenantID),
				workitemrelation.SourceWorkItemID(sourceWorkItemID),
				workitemrelation.TargetWorkItemID(targetID),
				workitemrelation.RelationType(problemTicketRelationType),
				workitemrelation.DeletedAtIsNil(),
			).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("failed to check existing work item relation: %w", err)
		}
		if exists {
			continue
		}
		_, err = r.client.WorkItemRelation.Create().
			SetTenantID(tenantID).
			SetSourceWorkItemID(sourceWorkItemID).
			SetTargetWorkItemID(targetID).
			SetRelationType(problemTicketRelationType).
			SetCreatedByID(actorUserID).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create work item relation: %w", err)
		}
	}
	return nil
}

func (r *EntRepository) RemoveAssociation(ctx context.Context, tenantID, problemID int, relatedType string, relatedID int) error {
	if relatedType == "ticket" {
		prob, err := r.client.Problem.Query().
			Where(problem.IDEQ(problemID), problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil()).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return fmt.Errorf("problem not found")
			}
			return err
		}
		if prob.WorkItemID <= 0 {
			// 没有 WorkItem 就不可能存在任何 WorkItemRelation 记录，视为已经是"未关联"
			// 状态，幂等返回成功（与旧 RemoveTicketIDs 对不存在的 edge 静默 no-op 一致）。
			return nil
		}
		_, err = r.client.WorkItemRelation.Update().
			Where(
				workitemrelation.TenantID(tenantID),
				workitemrelation.SourceWorkItemID(prob.WorkItemID),
				workitemrelation.TargetWorkItemID(relatedID),
				workitemrelation.RelationType(problemTicketRelationType),
				workitemrelation.DeletedAtIsNil(),
			).
			SetDeletedAt(time.Now()).
			Save(ctx)
		return err
	}

	update := r.client.Problem.Update().
		Where(problem.IDEQ(problemID), problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil())

	switch relatedType {
	case "incident":
		update.RemoveIncidentIDs(relatedID)
	case "change":
		update.RemoveChangeIDs(relatedID)
	default:
		return fmt.Errorf("unsupported related type: %s", relatedType)
	}

	updated, err := update.Save(ctx)
	if err == nil && updated != 1 {
		return fmt.Errorf("problem not found")
	}
	return err
}

// Create 在同一数据库事务内先建 tickets 行（record_class="problem"，创建后不可变），
// 再建 problems 行并把 work_item_id 回填指向那条 tickets 行——统一 WorkItem 领域模型
// 宪章 §3.2 的事务边界约束，任一边失败整体回滚。模式与 IncidentService.CreateIncident
// 完全一致（service/incident_service.go）。
func (r *EntRepository) Create(ctx context.Context, p *Problem) (*Problem, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start problem transaction: %w", err)
	}

	created, err := r.createInTx(ctx, tx, p)
	if err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, rollbackProblemTx(tx, fmt.Errorf("commit problem transaction: %w", err))
	}
	return created, nil
}

// createInTx creates the WorkItem base and Problem extension in the caller's
// transaction. Transaction lifecycle is owned by the caller.
func (r *EntRepository) createInTx(ctx context.Context, tx *ent.Tx, p *Problem) (*Problem, error) {
	// Ticket.requester_id 是一条指向 users 表的必填 FK edge（edge.From("requester",
	// User.Type).Required()），Problem 自己的 created_by 字段历史上没有这层约束。既然
	// 现在每条 Problem 都会同步建一条 tickets 行并把 requester_id 设成 Problem 的创建人，
	// 这里必须显式校验创建人存在且属于同一租户——否则 tx.Ticket.Create 会因为 FK 违反
	// 直接失败，报错信息对调用方不友好（同 IncidentService.CreateIncident 的
	// reporterExists 校验）。
	creatorExists, err := tx.User.Query().
		Where(user.IDEQ(p.CreatedBy), user.TenantIDEQ(p.TenantID), user.ActiveEQ(true)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate problem creator: %w", err)
	}
	if !creatorExists {
		return nil, fmt.Errorf("problem creator not found or inactive")
	}

	issuedAt := time.Now().UTC()
	ticketNumber, err := r.numberAllocator.Allocate(ctx, tx.Client(), p.TenantID, issuedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate work item ticket number: %w", err)
	}

	workItem, err := tx.Ticket.Create().
		SetTitle(p.Title).
		SetDescription(p.Description).
		SetType("problem").
		SetRecordClass("problem").
		SetPriority(p.Priority).
		SetTicketNumber(ticketNumber).
		SetRequesterID(p.CreatedBy).
		SetTenantID(p.TenantID).
		SetCreatedAt(issuedAt).
		SetUpdatedAt(issuedAt).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create work item: %w", err)
	}

	create := tx.Problem.Create().
		SetTitle(p.Title).
		SetDescription(p.Description).
		SetStatus(p.Status).
		SetPriority(p.Priority).
		SetCategory(p.Category).
		SetRootCause(p.RootCause).
		SetWorkaround(p.Workaround).
		SetResolution(p.Resolution).
		SetImpact(p.Impact).
		SetCreatedBy(p.CreatedBy).
		SetTenantID(p.TenantID).
		SetWorkItemID(workItem.ID).
		SetCreatedAt(issuedAt).
		SetUpdatedAt(issuedAt)

	if p.AssigneeID != nil {
		create.SetAssigneeID(*p.AssigneeID)
	}

	saved, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create problem: %w", err)
	}

	return r.toDomain(saved), nil
}

func rollbackProblemTx(tx *ent.Tx, cause error) error {
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, rollbackErr)
	}
	return cause
}

func (r *EntRepository) Get(ctx context.Context, id int, tenantID int) (*Problem, error) {
	e, err := r.client.Problem.Query().
		Where(problem.ID(id), problem.TenantID(tenantID), problem.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(e), nil
}

func (r *EntRepository) GetWithAssociations(ctx context.Context, id int, tenantID int) (*Problem, error) {
	e, err := r.client.Problem.Query().
		Where(problem.ID(id), problem.TenantID(tenantID), problem.DeletedAtIsNil()).
		WithIncidents(func(q *ent.IncidentQuery) {
			q.Where(incident.TenantIDEQ(tenantID), incident.DeletedAtIsNil()).
				Select("id", "title", "status", "incident_number")
		}).
		WithChanges(func(q *ent.ChangeQuery) {
			q.Where(change.TenantIDEQ(tenantID)).
				Select("id", "title", "status")
		}).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	p := r.toDomainWithAssociations(e)
	incidents, err := r.loadIncidentAssociations(ctx, tenantID, e.WorkItemID)
	if err != nil {
		return nil, err
	}
	p.Incidents = mergeAssociatedItems(p.Incidents, incidents)
	tickets, err := r.loadTicketAssociations(ctx, tenantID, e.WorkItemID)
	if err != nil {
		return nil, err
	}
	p.Tickets = tickets
	return p, nil
}

// loadIncidentAssociations resolves Incident -> Problem investigated_by links.
// The relation stores WorkItem IDs, while the public association contract uses
// professional Incident IDs, so the join is completed at the repository boundary.
func (r *EntRepository) loadIncidentAssociations(ctx context.Context, tenantID, problemWorkItemID int) ([]*AssociatedItem, error) {
	if problemWorkItemID <= 0 {
		return []*AssociatedItem{}, nil
	}
	relations, err := r.client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenantID),
			workitemrelation.TargetWorkItemID(problemWorkItemID),
			workitemrelation.RelationType(common.WorkItemRelationInvestigatedBy),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query incident problem relations: %w", err)
	}
	if len(relations) == 0 {
		return []*AssociatedItem{}, nil
	}

	sourceWorkItemIDs := make([]int, 0, len(relations))
	for _, relation := range relations {
		sourceWorkItemIDs = append(sourceWorkItemIDs, relation.SourceWorkItemID)
	}
	incidents, err := r.client.Incident.Query().
		Where(
			incident.TenantIDEQ(tenantID),
			incident.WorkItemIDIn(sourceWorkItemIDs...),
			incident.DeletedAtIsNil(),
		).
		Select("id", "title", "status", "incident_number").
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load linked incidents: %w", err)
	}

	items := make([]*AssociatedItem, 0, len(incidents))
	for _, inc := range incidents {
		items = append(items, &AssociatedItem{
			ID: inc.ID, Title: inc.Title, Status: inc.Status,
			Number: inc.IncidentNumber, Type: "incident",
		})
	}
	return items, nil
}

func mergeAssociatedItems(existing, additional []*AssociatedItem) []*AssociatedItem {
	seen := make(map[int]struct{}, len(existing)+len(additional))
	merged := make([]*AssociatedItem, 0, len(existing)+len(additional))
	for _, items := range [][]*AssociatedItem{existing, additional} {
		for _, item := range items {
			if item == nil {
				continue
			}
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			merged = append(merged, item)
		}
	}
	return merged
}

// loadTicketAssociations 通过 WorkItemRelation 读取 Problem 关联的普通工单（Wave 2 起
// 不再走 ent 的 Problem<->Ticket 多对多 edge）。sourceWorkItemID<=0 表示这条 Problem
// 还没有关联的 WorkItem（迁移前创建、尚未跑 cmd/backfill_problem_work_item），此时没有
// 任何 WorkItemRelation 行可能引用它，直接返回空列表。
func (r *EntRepository) loadTicketAssociations(ctx context.Context, tenantID, sourceWorkItemID int) ([]*AssociatedItem, error) {
	if sourceWorkItemID <= 0 {
		return []*AssociatedItem{}, nil
	}
	relations, err := r.client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenantID),
			workitemrelation.SourceWorkItemID(sourceWorkItemID),
			workitemrelation.RelationType(problemTicketRelationType),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query work item relations: %w", err)
	}
	if len(relations) == 0 {
		return []*AssociatedItem{}, nil
	}
	targetIDs := make([]int, 0, len(relations))
	for _, rel := range relations {
		targetIDs = append(targetIDs, rel.TargetWorkItemID)
	}
	tickets, err := r.client.Ticket.Query().
		Where(ticket.IDIn(targetIDs...), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).
		Select("id", "title", "status", "ticket_number").
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load linked tickets: %w", err)
	}
	items := make([]*AssociatedItem, 0, len(tickets))
	for _, t := range tickets {
		items = append(items, &AssociatedItem{
			ID:     t.ID,
			Title:  t.Title,
			Status: t.Status,
			Number: t.TicketNumber,
			Type:   "ticket",
		})
	}
	return items, nil
}

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}) ([]*Problem, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 200 {
		size = 200
	}
	query := r.client.Problem.Query().Where(problem.TenantID(tenantID), problem.DeletedAtIsNil())

	if v, ok := filters["status"].(string); ok && v != "" {
		query = query.Where(problem.StatusEQ(v))
	}
	if v, ok := filters["priority"].(string); ok && v != "" {
		query = query.Where(problem.PriorityEQ(v))
	}
	if v, ok := filters["category"].(string); ok && v != "" {
		query = query.Where(problem.CategoryEQ(v))
	}
	if v, ok := filters["keyword"].(string); ok && v != "" {
		query = query.Where(problem.Or(
			problem.TitleContains(v),
			problem.DescriptionContains(v),
		))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((page - 1) * size).
		Limit(size).
		Order(ent.Desc(problem.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	var result []*Problem
	for _, item := range list {
		result = append(result, r.toDomain(item))
	}
	return result, total, nil
}

func (r *EntRepository) Update(ctx context.Context, p *Problem) (*Problem, error) {
	update := r.client.Problem.UpdateOneID(p.ID).
		Where(problem.TenantIDEQ(p.TenantID), problem.DeletedAtIsNil()).
		SetTitle(p.Title).
		SetDescription(p.Description).
		SetStatus(p.Status).
		SetPriority(p.Priority).
		SetCategory(p.Category).
		SetRootCause(p.RootCause).
		SetWorkaround(p.Workaround).
		SetResolution(p.Resolution).
		SetImpact(p.Impact).
		SetUpdatedAt(time.Now())

	if p.AssigneeID != nil {
		update.SetAssigneeID(*p.AssigneeID)
	} else {
		update.ClearAssigneeID()
	}
	if p.ResolvedAt != nil {
		update.SetResolvedAt(*p.ResolvedAt)
	} else {
		update.ClearResolvedAt()
	}
	if p.ClosedAt != nil {
		update.SetClosedAt(*p.ClosedAt)
	} else {
		update.ClearClosedAt()
	}

	saved, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(saved), nil
}

func (r *EntRepository) Delete(ctx context.Context, id int, tenantID int) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start problem delete transaction: %w", err)
	}
	fail := func(cause error) error {
		return rollbackProblemTx(tx, cause)
	}

	existing, err := tx.Problem.Query().Where(
		problem.IDEQ(id),
		problem.TenantIDEQ(tenantID),
		problem.DeletedAtIsNil(),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fail(fmt.Errorf("problem not found"))
		}
		return fail(fmt.Errorf("load problem for delete: %w", err))
	}

	deletedAt := time.Now()
	if existing.WorkItemID > 0 {
		_, err = tx.WorkItemRelation.Update().Where(
			workitemrelation.TenantIDEQ(tenantID),
			workitemrelation.TargetWorkItemIDEQ(existing.WorkItemID),
			workitemrelation.RelationTypeEQ(common.WorkItemRelationInvestigatedBy),
			workitemrelation.DeletedAtIsNil(),
		).SetDeletedAt(deletedAt).Save(ctx)
		if err != nil {
			return fail(fmt.Errorf("soft-delete incident problem relations: %w", err))
		}
	}

	if _, err = tx.Problem.UpdateOne(existing).SetDeletedAt(deletedAt).Save(ctx); err != nil {
		return fail(fmt.Errorf("soft-delete problem: %w", err))
	}
	if err = tx.Commit(); err != nil {
		return rollbackProblemTx(tx, fmt.Errorf("commit problem delete transaction: %w", err))
	}
	return nil
}

func (r *EntRepository) GetStats(ctx context.Context, tenantID int) (*ProblemStats, error) {
	base := []entpredicate.Problem{problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil()}
	query := r.client.Problem.Query().Where(base...)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Simple count queries. Optimization: group by status/priority?
	// For now keeping it simple as per original service.
	count := func(pred entpredicate.Problem) (int, error) {
		return r.client.Problem.Query().Where(problem.TenantIDEQ(tenantID), problem.DeletedAtIsNil(), pred).Count(ctx)
	}
	open, err := count(problem.StatusEQ("open"))
	if err != nil {
		return nil, err
	}
	inProgress, err := count(problem.StatusIn("investigating", "in_progress"))
	if err != nil {
		return nil, err
	}
	resolved, err := count(problem.StatusEQ("resolved"))
	if err != nil {
		return nil, err
	}
	closed, err := count(problem.StatusEQ("closed"))
	if err != nil {
		return nil, err
	}
	high, err := count(problem.PriorityIn("high", "critical"))
	if err != nil {
		return nil, err
	}

	return &ProblemStats{
		Total:        total,
		Open:         open,
		InProgress:   inProgress,
		Resolved:     resolved,
		Closed:       closed,
		HighPriority: high,
	}, nil
}
