package problem

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	assignment "itsm-backend/handlers/common/workitemassignment"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"

	"go.uber.org/zap"
)

type Service struct {
	sessions *authorization.SessionReader
	repo     Repository
	logger   *zap.SugaredLogger
}

func NewService(repo Repository, logger *zap.SugaredLogger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

func (s *Service) Get(ctx context.Context, id int, tenantID int) (*Problem, error) {
	return s.repo.Get(ctx, id, tenantID)
}

func (s *Service) GetWithAssociations(ctx context.Context, id int, tenantID int) (*Problem, error) {
	return s.repo.GetWithAssociations(ctx, id, tenantID)
}

func (s *Service) AddAssociations(ctx context.Context, tenantID, problemID, actorUserID int, relatedType string, relatedIDs []int) error {
	relatedIDs = uniquePositiveIDs(relatedIDs)
	if len(relatedIDs) == 0 {
		return fmt.Errorf("at least one related id is required")
	}
	if actorUserID <= 0 {
		return fmt.Errorf("invalid actor user id")
	}
	return s.repo.AddAssociations(ctx, tenantID, problemID, actorUserID, relatedType, relatedIDs)
}

func (s *Service) RemoveAssociation(ctx context.Context, tenantID, problemID int, relatedType string, relatedID int) error {
	if relatedID <= 0 {
		return fmt.Errorf("invalid related id")
	}
	return s.repo.RemoveAssociation(ctx, tenantID, problemID, relatedType, relatedID)
}

func (s *Service) List(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}) ([]*Problem, int, error) {
	return s.repo.List(ctx, tenantID, page, size, filters)
}

func (s *Service) SetSessionReader(sessions *authorization.SessionReader) { s.sessions = sessions }

func (s *Service) Update(ctx context.Context, tenantID, id int, p *Problem, identity creation.Identity) (*Problem, error) {
	if p.AssigneeID == nil {
		existing, err := s.repo.Get(ctx, id, tenantID)
		if err != nil {
			return nil, err
		}
		merged, err := mergeProblemUpdate(existing, p)
		if err != nil {
			return nil, err
		}
		return s.repo.Update(ctx, merged)
	}
	if s.sessions == nil || identity.TenantID != tenantID {
		return nil, fmt.Errorf("verified Problem assignment session is required")
	}
	var result *Problem
	err := s.sessions.Write(ctx, identity, func(session *authorization.SessionSnapshot) error {
		existing, err := s.repo.GetTx(ctx, session.Tx, id, tenantID)
		if err != nil {
			return err
		}
		if existing.WorkItemID == nil {
			return fmt.Errorf("Problem WorkItem is required")
		}
		item, err := session.AuthorizeWorkItemAssignment(ctx, *existing.WorkItemID)
		if err != nil {
			return err
		}
		allowed, err := s.IsUnfinished(ctx, session.Tx.Client(), item)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("Problem is not assignable")
		}
		merged, err := mergeProblemUpdate(existing, p)
		if err != nil {
			return err
		}
		_, err = service.NewWorkItemAssignmentWriter(session).Apply(ctx, session.Tx.Client(), assignment.Command{WorkItemID: item.ID, TenantID: tenantID, ActorID: session.Actor.ID, ActorTenantID: session.Actor.TenantID, AssigneeID: *p.AssigneeID, ExpectedVersion: item.Version, Source: identity.Channel + ".problem.update"})
		if err != nil {
			return err
		}
		result, err = s.repo.UpdateTx(ctx, session.Tx, merged, item.Version+1)
		return err
	})
	return result, err
}

func mergeProblemUpdate(existing, p *Problem) (*Problem, error) {
	// Update fields if they are set (non-zero/non-empty check in Handler or here)
	// Here assuming 'p' contains only fields to update usually, but domain entity isn't partial.
	// We merge changes here.
	if p.Title != "" {
		existing.Title = p.Title
	}
	if p.Description != "" {
		existing.Description = p.Description
	}
	if p.Status != "" {
		if !isValidProblemStatusTransition(existing.Status, p.Status) {
			return nil, fmt.Errorf("invalid problem status transition: %s -> %s", existing.Status, p.Status)
		}
		existing.Status = p.Status
		now := time.Now()
		switch p.Status {
		case "resolved":
			existing.ResolvedAt = &now
			existing.ClosedAt = nil
		case "closed":
			existing.ClosedAt = &now
		case "investigating":
			existing.ResolvedAt = nil
			existing.ClosedAt = nil
		}
	}
	if p.Priority != "" {
		if !isValidProblemPriority(p.Priority) {
			return nil, fmt.Errorf("invalid problem priority: %s", p.Priority)
		}
		existing.Priority = p.Priority
	}
	// Preserve omission so unrelated edits do not revalidate or rewrite classification.
	existing.CategoryID = p.CategoryID
	// Preserve root-cause omission; unrelated edits must not replay a stale RCA body.
	existing.RootCause = p.RootCause
	if p.Workaround != "" {
		existing.Workaround = p.Workaround
	}
	if p.Resolution != "" {
		existing.Resolution = p.Resolution
	}
	if p.Impact != "" {
		existing.Impact = p.Impact
	}
	if p.AssigneeID != nil {
		existing.AssigneeID = p.AssigneeID
	}

	return existing, nil
}

// InvestigateProblem starts the investigation lifecycle for a problem.
func (s *Service) InvestigateProblem(ctx context.Context, tenantID, id int) (*Problem, error) {
	return s.Update(ctx, tenantID, id, &Problem{Status: "investigating"}, creation.Identity{})
}

// UpdateRootCause records the confirmed root cause.
func (s *Service) UpdateRootCause(ctx context.Context, tenantID, id int, rootCause string) (*Problem, error) {
	rootCause = strings.TrimSpace(rootCause)
	if rootCause == "" {
		return nil, fmt.Errorf("rootCause is required")
	}
	return s.Update(ctx, tenantID, id, &Problem{RootCause: rootCause}, creation.Identity{})
}

// UpdateSolution records a workaround and/or final resolution.
func (s *Service) UpdateSolution(ctx context.Context, tenantID, id int, workaround, resolution string) (*Problem, error) {
	workaround = strings.TrimSpace(workaround)
	resolution = strings.TrimSpace(resolution)
	if workaround == "" && resolution == "" {
		return nil, fmt.Errorf("solution, workaround or resolution is required")
	}
	return s.Update(ctx, tenantID, id, &Problem{Workaround: workaround, Resolution: resolution}, creation.Identity{})
}

// CloseProblem closes a problem and optionally records its final resolution.
func (s *Service) CloseProblem(ctx context.Context, tenantID, id int, resolution string) (*Problem, error) {
	return s.Update(ctx, tenantID, id, &Problem{
		Status:     "closed",
		Resolution: strings.TrimSpace(resolution),
	}, creation.Identity{})
}

func isValidProblemPriority(priority string) bool {
	switch priority {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func isValidProblemStatusTransition(current, next string) bool {
	if current == next {
		return true
	}
	if next == "closed" {
		return canCloseProblemStatus(current)
	}
	transitions := map[string]map[string]struct{}{
		"open":          {"investigating": {}, "identified": {}, "resolved": {}},
		"investigating": {"identified": {}, "resolved": {}},
		"identified":    {"investigating": {}, "resolved": {}},
		"resolved":      {"investigating": {}},
		"closed":        {},
		// 兼容存量 in_progress 数据，仅允许进入规范状态。
		"in_progress": {"identified": {}, "resolved": {}},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[next]
	return ok
}

func canCloseProblemStatus(status string) bool {
	return strings.TrimSpace(status) == "resolved"
}

// IsUnfinished projects this owner's canonical lifecycle, including its supported
// legacy in_progress state. A Problem has no cancelled state.
func (s *Service) IsUnfinished(_ context.Context, _ *ent.Client, item *ent.Ticket) (bool, error) {
	if item == nil || item.RecordClass != "problem" {
		return false, fmt.Errorf("Problem WorkItem is required")
	}
	switch item.Status {
	case "open", "investigating", "identified", "in_progress":
		return true, nil
	case "resolved", "closed":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported Problem status %q", item.Status)
	}
}

func uniquePositiveIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *Service) Delete(ctx context.Context, id int, tenantID int) error {
	return s.repo.Delete(ctx, id, tenantID)
}

func (s *Service) GetStats(ctx context.Context, tenantID int) (*ProblemStats, error) {
	return s.repo.GetStats(ctx, tenantID)
}
