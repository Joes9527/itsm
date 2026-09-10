package problem

import (
	"context"

	"itsm-backend/common"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

// ApplyRelation resolves this public Problem ID once; the authoritative command
// retains the caller's explicit source WorkItem, observed version and operation.
func (s *Service) ApplyRelation(ctx context.Context, problemID int, cmd service.RelationCommand, remove bool) (workitemmutation.Result, error) {
	p, err := s.repo.Get(ctx, problemID, cmd.Meta.TenantID)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	if p.WorkItemID == nil || (*p.WorkItemID != cmd.SourceID && *p.WorkItemID != cmd.TargetID) {
		return workitemmutation.Result{}, common.NewValidationError("Problem must be a relation endpoint", nil)
	}
	return service.NewWorkItemRelationService(s.client, s.directory).Apply(ctx, cmd, remove)
}
