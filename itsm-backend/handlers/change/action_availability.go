package change

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"time"
)

type completionPreview interface {
	CheckTaskCompletionTx(context.Context, *ent.Tx, string) error
}

// GetChangeActionView reads current identity, task eligibility and the displayed
// version from one stable snapshot. A positive action still requires its input facts.
func (s *Service) GetChangeActionView(ctx context.Context, id int, m workitemmutation.Meta) (*Change, map[string]dto.ActionPermission, map[string]string, error) {
	if m.ActorID <= 0 || m.TenantID <= 0 {
		return nil, nil, nil, common.NewForbiddenError("authenticated actor required")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return nil, nil, nil, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	role := authorization.EffectiveSessionRole(actor)
	identity := creation.Identity{TenantID: m.TenantID, ActorID: m.ActorID, Role: role}
	current, err := tx.Change.Query().Where(change.ID(id), change.HasWorkItemWith(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil())).WithWorkItem().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, nil, common.NewNotFoundError("change")
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, "change", "read"); err != nil {
		return nil, nil, nil, err
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), current.WorkItemID, m.TenantID, role, "read"); err != nil {
		return nil, nil, nil, err
	}
	result := toDomain(current)
	repo := NewEntRepository(tx.Client(), nil)
	if err = repo.hydrateUsers(ctx, []*Change{result}, m.TenantID); err != nil {
		return nil, nil, nil, err
	}
	if err = s.projectRelationsTx(ctx, tx, m, result); err != nil {
		return nil, nil, nil, err
	}
	actions := map[string]dto.ActionPermission{}
	tasks := map[string]string{}
	settled := workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, current.WorkItemID)
	if settled != nil {
		if _, ok := settled.(*workitemmutation.UnresolvedChangeCallbackError); !ok {
			return nil, nil, nil, settled
		}
	}
	writeErr := authorization.RequireCurrentPermission(ctx, tx, identity, "change", "write")
	if writeErr != nil && !changeActionDenied(writeErr) {
		return nil, nil, nil, writeErr
	}
	_, _, updateErr := authorization.AuthorizeWorkItem(ctx, tx.Client(), current.WorkItemID, m.TenantID, role, "update")
	if updateErr != nil && !changeActionDenied(updateErr) {
		return nil, nil, nil, updateErr
	}

	for _, action := range []string{"submit", "cancel", "metadata", "assign", "risk", "assess", "approve", "reject", "schedule", "implement", "record_outcome", "review", "close"} {
		actions[action] = dto.ActionPermission{Allowed: false, Reason: "no current task for this action"}
	}
	terminal := result.Status == "completed" || result.Status == "cancelled" || result.Status == "rejected"
	if writeErr == nil && updateErr == nil && !terminal && settled == nil {
		actions["metadata"] = dto.ActionPermission{Allowed: true}
		actions["assign"] = dto.ActionPermission{Allowed: true}
		actions["cancel"] = dto.ActionPermission{Allowed: common.IsValidChangeStatusTransition(result.Status, "cancelled", current.Type)}
		if result.Status == "draft" {
			actions["submit"] = dto.ActionPermission{Allowed: current.ImplementationPlan != "" && current.RollbackPlan != "" && s.processEngine != nil}
		}
		if (result.Status == "draft" || isChangeSubmitted(result.Status)) && current.AssessmentDigest == "" && current.AssessmentEvidence == "" && current.AssessedBy == 0 && current.AssessedAt.IsZero() {
			actions["risk"] = dto.ActionPermission{Allowed: true}
		}
	}
	instances, err := tx.ProcessInstance.Query().Where(processinstance.TenantID(m.TenantID), processinstance.BusinessID(current.WorkItemID), processinstance.BusinessType("change"), processinstance.BusinessKey(fmt.Sprintf("change:%d", current.WorkItemID)), processinstance.Status("running")).All(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(instances) > 1 {
		return nil, nil, nil, common.NewConflictError("Change workflow", "multiple running processes")
	}
	preview, previewOK := s.processEngine.(completionPreview)
	for _, instance := range instances {
		rows, err := tx.ProcessTask.Query().Where(processtask.TenantID(m.TenantID), processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey(instance.CurrentActivityID), processtask.CallbackHandlerID("change_service_handler")).All(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, task := range rows {
			if service.ValidateBPMNTaskLifecycle(service.BPMNTaskCommandComplete, task.Status) != nil {
				continue
			}
			for action, callback := range changeTaskActions {
				if task.CallbackAction != callback {
					continue
				}
				tasks[action] = task.TaskID
				permission := "write"
				if action == "approve" || action == "reject" {
					permission = "approve"
				}
				permissionErr := authorization.RequireCurrentPermission(ctx, tx, identity, "change", permission)
				if permissionErr != nil && !changeActionDenied(permissionErr) {
					return nil, nil, nil, permissionErr
				}
				allowed := permissionErr == nil && updateErr == nil && settled == nil && previewOK
				if allowed {
					previewErr := preview.CheckTaskCompletionTx(service.WithBPMNAccessScope(ctx, service.BPMNAccessScope{TenantID: m.TenantID, UserID: m.ActorID}), tx, task.TaskID)
					if previewErr != nil && !changeActionDenied(previewErr) {
						return nil, nil, nil, previewErr
					}
					allowed = previewErr == nil
				}
				if allowed && (action == "approve" || action == "reject") {
					allowed = current.Edges.WorkItem.OpenedByID != m.ActorID && isChangeSubmitted(result.Status)
				}
				if allowed && (action == "approve" || action == "reject" || action == "schedule" || action == "implement") {
					digest, err := assessmentDigestTx(ctx, tx, current, m.TenantID)
					if err != nil {
						return nil, nil, nil, err
					}
					if action == "implement" {
						allowed = validateChangeImplementation(current, digest, time.Now().UTC()) == nil
					} else {
						allowed = currentChangeAssessment(current, digest)
					}
					if action == "schedule" {
						allowed = allowed && common.IsValidChangeStatusTransition(result.Status, "scheduled", current.Type)
					}
				}
				actions[action] = dto.ActionPermission{Allowed: allowed}
				if !allowed {
					actions[action] = dto.ActionPermission{Reason: "current actor or workflow cannot complete this task"}
				}
			}
		}
	}
	if settled != nil {
		for action := range actions {
			actions[action] = dto.ActionPermission{Reason: "prior callback is unresolved"}
		}
	}
	return result, actions, tasks, nil
}

func changeActionDenied(err error) bool {
	if errors.Is(err, creation.ErrPermissionDenied) || errors.Is(err, creation.ErrAuthenticationRequired) {
		return true
	}
	if app, ok := common.AsAppError(err); ok && app.Code == common.ErrCodeForbidden {
		return true
	}
	var business *common.BusinessError
	return errors.As(err, &business) && business.Code == common.ForbiddenErrorCode
}
