package problem

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	entpredicate "itsm-backend/ent/predicate"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"

	entsql "entgo.io/ent/dialect/sql"
)

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

func (r *EntRepository) toDomain(e *ent.Problem) *Problem {
	if e == nil {
		return nil
	}
	workItem := e.Edges.WorkItem
	if workItem == nil {
		return nil
	}
	p := &Problem{
		Number:           workItem.TicketNumber,
		Version:          workItem.Version,
		VerifiedVersion:  e.VerifiedVersion,
		VerificationNote: e.VerificationNote,
		ID:               e.ID,
		CategoryID:       &workItem.CategoryID,
		Title:            workItem.Title,
		Description:      workItem.Description,
		Status:           workItem.Status,
		Priority:         workItem.Priority,
		RootCause:        e.RootCause,
		Workaround:       e.Workaround,
		Resolution:       e.Resolution,
		Impact:           e.Impact,
		CreatedBy:        workItem.OpenedByID,
		TenantID:         workItem.TenantID,
		CreatedAt:        workItem.CreatedAt,
		UpdatedAt:        workItem.UpdatedAt,
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

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}, scope ...entpredicate.Ticket) ([]*Problem, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 200 {
		size = 200
	}
	query := r.client.Problem.Query().Where(problemTenantScope(tenantID, scope...))

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
	if p.Version != current.Edges.WorkItem.Version {
		return nil, rollbackProblemTx(tx, common.NewVersionConflictError("problem", p.ID, p.Version, current.Edges.WorkItem.Version))
	}
	now := time.Now()
	var selected *ent.TicketCategory
	if p.CategoryID != nil && *p.CategoryID != 0 {
		selected, err = tx.TicketCategory.Query().Where(ticketcategory.IDEQ(*p.CategoryID), ticketcategory.TenantIDEQ(p.TenantID), ticketcategory.IsActiveEQ(true)).Only(ctx)
		if err != nil {
			return nil, rollbackProblemTx(tx, fmt.Errorf("active ticket category not found in tenant: %w", err))
		}
	}
	workItemUpdate := tx.Ticket.UpdateOneID(current.WorkItemID).
		Where(ticket.TenantIDEQ(p.TenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(p.Version)).
		SetTitle(p.Title).SetDescription(p.Description).SetPriority(p.Priority).
		SetUpdatedAt(now).AddVersion(1)
	if p.AssigneeID == nil {
		workItemUpdate.ClearAssigneeID()
	} else {
		workItemUpdate.SetAssigneeID(*p.AssigneeID)
	}
	if p.CategoryID != nil {
		if *p.CategoryID == 0 {
			workItemUpdate.ClearCategoryID()
		} else {
			workItemUpdate.SetCategoryID(*p.CategoryID)
		}
	}
	workItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		return nil, rollbackProblemTx(tx, fmt.Errorf("update problem work item: %w", err))
	}
	update := tx.Problem.UpdateOneID(p.ID).
		Where(problemTenantScope(p.TenantID)).
		SetWorkaround(p.Workaround).
		SetResolution(p.Resolution).
		SetImpact(p.Impact)
	if p.RootCause != "" {
		update.SetRootCause(p.RootCause)
	}

	saved, err := update.Save(ctx)
	if err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, rollbackProblemTx(tx, err)
	}
	saved.Edges.WorkItem = workItem
	if p.CategoryID == nil {
		saved.Edges.WorkItem.Edges.Category = current.Edges.WorkItem.Edges.Category
	} else {
		saved.Edges.WorkItem.Edges.Category = selected
	}
	return r.toDomain(saved), nil
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
