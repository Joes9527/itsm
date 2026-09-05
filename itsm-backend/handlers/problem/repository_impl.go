package problem

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	entpredicate "itsm-backend/ent/predicate"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/workitemrelation"

	entsql "entgo.io/ent/dialect/sql"
)

// problemTicketRelationType 是 Problem 关联普通工单（record_class 未收敛的一般关联，
// 不是"调查根因"那条 investigated_by/caused_by 方向性关系）在 WorkItemRelation 里使用的
// relation_type。见 docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md §10。
const problemTicketRelationType = "related_to"

type EntRepository struct {
	client *ent.Client
}

func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{client: client}
}

func problemTenantScope(tenantID int, extra ...entpredicate.Ticket) entpredicate.Problem {
	predicates := []entpredicate.Ticket{ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()}
	predicates = append(predicates, extra...)
	return problem.HasWorkItemWith(predicates...)
}

func withProblemWorkItemProjection(query *ent.TicketQuery) {
	query.WithCategory()
}

func (r *EntRepository) resolveCategory(ctx context.Context, tenantID int, name string) (*int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	category, err := r.client.TicketCategory.Query().Where(
		ticketcategory.TenantIDEQ(tenantID), ticketcategory.IsActiveEQ(true), ticketcategory.NameEQ(name),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("ticket category not found in tenant")
		}
		return nil, fmt.Errorf("resolve ticket category: %w", err)
	}
	return &category.ID, nil
}

func (r *EntRepository) toDomain(e *ent.Problem) *Problem {
	if e == nil {
		return nil
	}
	workItem := e.Edges.WorkItem
	if workItem == nil {
		return nil
	}
	p := &Problem{
		ID:          e.ID,
		Title:       workItem.Title,
		Description: workItem.Description,
		Status:      workItem.Status,
		Priority:    workItem.Priority,
		RootCause:   e.RootCause,
		Workaround:  e.Workaround,
		Resolution:  e.Resolution,
		Impact:      e.Impact,
		CreatedBy:   workItem.OpenedByID,
		TenantID:    workItem.TenantID,
		CreatedAt:   workItem.CreatedAt,
		UpdatedAt:   workItem.UpdatedAt,
	}
	if p.CreatedBy == 0 {
		p.CreatedBy = workItem.RequesterID
	}
	if workItem.Edges.Category != nil {
		p.Category = workItem.Edges.Category.Name
	}
	if !workItem.ResolvedAt.IsZero() {
		resolvedAt := workItem.ResolvedAt
		p.ResolvedAt = &resolvedAt
	}
	if workItem.ClosedAt != nil {
		p.ClosedAt = workItem.ClosedAt
	}
	// Handle optional fields
	// Ent fields might be zero value if not set, or pointer depending on schema.
	// Schema says: AssigneeID optional.
	if workItem.AssigneeID != 0 {
		id := workItem.AssigneeID
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
			if inc.Edges.WorkItem == nil {
				continue
			}
			p.Incidents = append(p.Incidents, &AssociatedItem{
				ID:     inc.ID,
				Title:  inc.Edges.WorkItem.Title,
				Status: inc.Edges.WorkItem.Status,
				Number: inc.IncidentNumber,
				Type:   "incident",
			})
		}
	}
	if e.Edges.Changes != nil {
		p.Changes = make([]*AssociatedItem, 0, len(e.Edges.Changes))
		for _, ch := range e.Edges.Changes {
			if ch.Edges.WorkItem == nil {
				continue
			}
			p.Changes = append(p.Changes, &AssociatedItem{
				ID:     ch.ID,
				Title:  ch.Edges.WorkItem.Title,
				Status: ch.Edges.WorkItem.Status,
				Type:   "change",
			})
		}
	}

	return p
}

func (r *EntRepository) AddAssociations(ctx context.Context, tenantID, problemID, actorUserID int, relatedType string, relatedIDs []int) error {
	prob, err := r.client.Problem.Query().
		Where(problem.IDEQ(problemID), problemTenantScope(tenantID)).
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
			return fmt.Errorf("problem %d violates WorkItem creation invariant: missing work_item_id", problemID)
		}
		return r.linkTicketsAsWorkItemRelations(ctx, tenantID, prob.WorkItemID, actorUserID, relatedIDs)
	case "incident":
		count, err := r.client.Incident.Query().
			Where(incident.IDIn(relatedIDs...), incident.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil())).
			Count(ctx)
		if err != nil {
			return err
		}
		if count != len(relatedIDs) {
			return fmt.Errorf("one or more incidents do not belong to the current tenant")
		}
	case "change":
		count, err := r.client.Change.Query().
			Where(change.IDIn(relatedIDs...), change.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil())).
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
		Where(problem.IDEQ(problemID), problemTenantScope(tenantID))
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
			Where(problem.IDEQ(problemID), problemTenantScope(tenantID)).
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
		Where(problem.IDEQ(problemID), problemTenantScope(tenantID))

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

func rollbackProblemTx(tx *ent.Tx, cause error) error {
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, rollbackErr)
	}
	return cause
}

func (r *EntRepository) Get(ctx context.Context, id int, tenantID int) (*Problem, error) {
	e, err := r.client.Problem.Query().
		Where(problem.ID(id), problemTenantScope(tenantID)).
		WithWorkItem(withProblemWorkItemProjection).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(e), nil
}

func (r *EntRepository) GetWithAssociations(ctx context.Context, id int, tenantID int) (*Problem, error) {
	e, err := r.client.Problem.Query().
		Where(problem.ID(id), problemTenantScope(tenantID)).
		WithIncidents(func(q *ent.IncidentQuery) {
			q.Where(incident.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil())).WithWorkItem()
		}).
		WithChanges(func(q *ent.ChangeQuery) {
			q.Where(change.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil())).
				WithWorkItem()
		}).
		WithWorkItem(withProblemWorkItemProjection).
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
			incident.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()),
			incident.WorkItemIDIn(sourceWorkItemIDs...),
		).
		WithWorkItem().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load linked incidents: %w", err)
	}

	items := make([]*AssociatedItem, 0, len(incidents))
	for _, inc := range incidents {
		if inc.Edges.WorkItem == nil {
			continue
		}
		items = append(items, &AssociatedItem{
			ID: inc.ID, Title: inc.Edges.WorkItem.Title, Status: inc.Edges.WorkItem.Status,
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
// 不再走 ent 的 Problem<->Ticket 多对多 edge）。sourceWorkItemID<=0 表示开发数据违反
// WorkItem 创建不变量；读取端没有可查询的 WorkItemRelation，返回空列表。
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
	query := r.client.Problem.Query().Where(problemTenantScope(tenantID))

	if v, ok := filters["status"].(string); ok && v != "" {
		query = query.Where(problem.HasWorkItemWith(ticket.StatusEQ(v)))
	}
	if v, ok := filters["priority"].(string); ok && v != "" {
		query = query.Where(problem.HasWorkItemWith(ticket.PriorityEQ(v)))
	}
	if v, ok := filters["category"].(string); ok && v != "" {
		query = query.Where(problem.HasWorkItemWith(ticket.HasCategoryWith(ticketcategory.NameEQ(v))))
	}
	if v, ok := filters["keyword"].(string); ok && v != "" {
		query = query.Where(problem.HasWorkItemWith(ticket.Or(ticket.TitleContains(v), ticket.DescriptionContains(v))))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		WithWorkItem(withProblemWorkItemProjection).
		Offset((page - 1) * size).
		Limit(size).
		Order(problem.ByWorkItemField(ticket.FieldCreatedAt, entsql.OrderDesc())).
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
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start problem update transaction: %w", err)
	}
	current, err := tx.Problem.Query().Where(problem.IDEQ(p.ID), problemTenantScope(p.TenantID)).WithWorkItem(withProblemWorkItemProjection).Only(ctx)
	if err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	now := time.Now()
	categoryID, err := r.resolveCategory(ctx, p.TenantID, p.Category)
	if err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	workItemUpdate := tx.Ticket.UpdateOneID(current.WorkItemID).
		Where(ticket.TenantIDEQ(p.TenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).
		SetTitle(p.Title).SetDescription(p.Description).SetStatus(p.Status).SetPriority(p.Priority).
		SetUpdatedAt(now).AddVersion(1)
	if p.AssigneeID == nil {
		workItemUpdate.ClearAssigneeID()
	} else {
		workItemUpdate.SetAssigneeID(*p.AssigneeID)
	}
	if categoryID == nil {
		workItemUpdate.ClearCategoryID()
	} else {
		workItemUpdate.SetCategoryID(*categoryID)
	}
	if p.ResolvedAt == nil {
		workItemUpdate.ClearResolvedAt()
	} else {
		workItemUpdate.SetResolvedAt(*p.ResolvedAt)
	}
	if p.ClosedAt == nil {
		workItemUpdate.ClearClosedAt()
	} else {
		workItemUpdate.SetClosedAt(*p.ClosedAt)
	}
	workItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		return nil, rollbackProblemTx(tx, fmt.Errorf("update problem work item: %w", err))
	}
	update := tx.Problem.UpdateOneID(p.ID).
		Where(problemTenantScope(p.TenantID)).
		SetRootCause(p.RootCause).
		SetWorkaround(p.Workaround).
		SetResolution(p.Resolution).
		SetImpact(p.Impact)

	saved, err := update.Save(ctx)
	if err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	saved.Edges.WorkItem = workItem
	if categoryID != nil {
		saved.Edges.WorkItem.Edges.Category, err = r.client.TicketCategory.Get(ctx, *categoryID)
		if err != nil {
			return nil, err
		}
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
		problemTenantScope(tenantID),
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

	if _, err = tx.Ticket.UpdateOneID(existing.WorkItemID).Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).SetDeletedAt(deletedAt).SetUpdatedAt(deletedAt).AddVersion(1).Save(ctx); err != nil {
		return fail(fmt.Errorf("soft-delete problem: %w", err))
	}
	if err = tx.Commit(); err != nil {
		return rollbackProblemTx(tx, fmt.Errorf("commit problem delete transaction: %w", err))
	}
	return nil
}

func (r *EntRepository) GetStats(ctx context.Context, tenantID int) (*ProblemStats, error) {
	query := r.client.Problem.Query().Where(problemTenantScope(tenantID))

	total, err := query.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Simple count queries. Optimization: group by status/priority?
	// For now keeping it simple as per original service.
	count := func(preds ...entpredicate.Ticket) (int, error) {
		q := r.client.Problem.Query().Where(problemTenantScope(tenantID))
		q = q.Where(problem.HasWorkItemWith(preds...))
		return q.Count(ctx)
	}
	open, err := count(ticket.StatusEQ("open"))
	if err != nil {
		return nil, err
	}
	inProgress, err := count(ticket.StatusIn("investigating", "in_progress"))
	if err != nil {
		return nil, err
	}
	resolved, err := count(ticket.StatusEQ("resolved"))
	if err != nil {
		return nil, err
	}
	closed, err := count(ticket.StatusEQ("closed"))
	if err != nil {
		return nil, err
	}
	high, err := count(ticket.PriorityIn("high", "critical"))
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
