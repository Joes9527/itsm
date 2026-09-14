package change

import (
	"context"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

// Only the currently authorized domain owner constructs this instance capability.
// It is scoped to the exact persisted Change WorkItem identity, never request data.
func (s *Service) terminateChangeTx(ctx context.Context, tx *ent.Tx, c *ent.Change, m workitemmutation.Meta, reason string) error {
	if err := workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, c.WorkItemID); err != nil {
		if _, unresolved := err.(*workitemmutation.UnresolvedChangeCallbackError); unresolved {
			err = common.NewConflictError("Change workflow", err.Error())
		}
		return err
	}
	key, keyErr := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, c.WorkItemID)
	if keyErr != nil {
		return keyErr
	}
	instances, err := tx.ProcessInstance.Query().Where(processinstance.TenantID(m.TenantID), processinstance.Or(processinstance.And(processinstance.BusinessType(string(dto.BusinessTypeChangeRequest)), processinstance.BusinessID(c.WorkItemID)), processinstance.BusinessKey(key))).All(ctx)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.BusinessType != string(dto.BusinessTypeChangeRequest) || instance.BusinessID != c.WorkItemID || instance.BusinessKey != key {
			return common.NewForbiddenError("Change process identity mismatch")
		}
		if instance.Status == "completed" || instance.Status == "terminated" {
			continue
		}
		if s.processEngine == nil {
			return common.NewValidationError("change process engine required", nil)
		}
		scoped := service.WithBPMNAccessScope(ctx, service.BPMNAccessScope{UserID: m.ActorID, TenantID: m.TenantID, CanUpdateAllInstances: true})
		if err = s.processEngine.TerminateProcessTx(scoped, tx, instance.ProcessInstanceID, reason); err != nil {
			return err
		}
	}
	return nil
}
