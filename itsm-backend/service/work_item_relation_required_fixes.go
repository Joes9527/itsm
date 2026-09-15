package service

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// RequiredFixDependency is one live outgoing required resolved_by_change dependency
// together with the authoritative Change outcome owned by the change domain.
type RequiredFixDependency struct {
	RelationID       int
	ChangeWorkItemID int
	ChangeID         int
	Outcome          string
}

// RequiredFixDependenciesTx reads the live outgoing required resolved_by_change
// dependencies of a WorkItem through the sole relation authority, returning the
// authoritative Change outcome instead of inferring success from a status string.
//
// This is a business precondition read for the owning command, not a user-facing
// relation read: the caller has already authorized the actor for the source through
// its own domain policy, and the source CAS is held by that same transaction. It
// therefore does not re-impose the relation read scope, which would make resolve
// depend on shared row visibility the domain deliberately does not require (for
// example an MSP-allocated investigator). Only dependencies explicitly marked
// required are returned, and a missing Change extension fails closed rather than
// reporting no dependency.
func (s *WorkItemRelationService) RequiredFixDependenciesTx(ctx context.Context, tx *ent.Tx, meta workitemmutation.Meta, workItemID int) ([]RequiredFixDependency, error) {
	if meta.TenantID <= 0 || workItemID <= 0 {
		return nil, fmt.Errorf("required fix dependency read requires tenant and work item")
	}
	rows, err := tx.WorkItemRelation.Query().Where(
		workitemrelation.TenantID(meta.TenantID),
		workitemrelation.SourceWorkItemID(workItemID),
		workitemrelation.RelationType("resolved_by_change"),
		workitemrelation.DeletedAtIsNil(),
	).All(ctx)
	if err != nil {
		return nil, err
	}
	dependencies := make([]RequiredFixDependency, 0, len(rows))
	for _, row := range rows {
		if !row.Metadata.Required {
			continue
		}
		record, err := tx.Change.Query().Where(change.WorkItemID(row.TargetWorkItemID)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("required fix dependency change outcome is unavailable: %w", err)
		}
		dependencies = append(dependencies, RequiredFixDependency{
			RelationID:       row.ID,
			ChangeWorkItemID: row.TargetWorkItemID,
			ChangeID:         record.ID,
			Outcome:          record.Outcome,
		})
	}
	return dependencies, nil
}
