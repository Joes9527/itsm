package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func (e *CustomProcessEngine) SetAssignmentDirectory(directory database.DirectorySnapshot) {
	e.assignmentDirectory = directory
	e.participationResolver.assignmentDirectory = directory
}

func (e *CustomProcessEngine) forTransaction(tx *ent.Tx, keys *[]string) *CustomProcessEngine {
	clone := e.forClient(tx.Client(), keys)
	clone.assignmentTx = tx
	clone.participationResolver.assignmentTx = tx
	return clone
}

func unavailableBPMNIdentity(err error) bool {
	return ent.IsNotFound(err) || errors.Is(err, creation.ErrAuthenticationRequired) || errors.Is(err, creation.ErrPermissionDenied)
}

// The directory is a restricted snapshot of the ordinary customer transaction.
// It never supplies business rows, permissions, or an alternative actor ID.
func (e *CustomProcessEngine) resolveAssignmentUser(ctx context.Context, client *ent.Client, id, tenantID int) (*ent.User, error) {
	if view := taskReadSnapshot(ctx, client); view != nil {
		key := [2]int{tenantID, id}
		if actor, ok := view.users[key]; ok {
			return actor, nil
		}
		var actor *ent.User
		var err error
		if e.assignmentDirectory == nil {
			actor, err = client.User.Query().Where(user.ID(id), user.TenantID(tenantID), user.Active(true)).Only(ctx)
		} else {
			if view.directory == nil {
				view.directory, view.closeDirectory, err = e.assignmentDirectory.Open(ctx, view.tx, tenantID)
				if err != nil {
					return nil, err
				}
			}
			actor, err = authorization.ResolveCurrentTenantUser(ctx, view.directory, id, tenantID, view.now)
		}
		if err == nil {
			view.users[key] = actor
		}
		return actor, err
	}

	if e.assignmentDirectory == nil {
		return client.User.Query().Where(user.ID(id), user.TenantID(tenantID), user.Active(true)).Only(ctx)
	}
	tx := e.assignmentTx
	if tx == nil {
		var err error
		tx, err = client.Tx(ctx)
		if err != nil {
			return nil, fmt.Errorf("bound identity requires owning transaction: %w", err)
		}
		defer tx.Rollback()
	} else if tx.Client() != client {
		return nil, fmt.Errorf("bound identity transaction mismatch")
	}
	view, close, err := e.assignmentDirectory.Open(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	actor, resolveErr := authorization.ResolveCurrentTenantUser(ctx, view, id, tenantID, time.Now())
	closeErr := close()
	if closeErr != nil {
		return nil, closeErr
	}
	return actor, resolveErr
}

func (e *CustomProcessEngine) loadLifecycleActor(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope) (*ent.User, error) {
	if task.AssigneeSource != "" {
		return e.resolveAssignmentUser(ctx, client, scope.UserID, scope.TenantID)
	}
	return loadTaskMutationActor(ctx, client, scope)
}

func (e *CustomProcessEngine) resolveLifecycleStartActor(ctx context.Context, tenantID int, variables map[string]interface{}, bound bool) (*ent.User, string, error) {
	if bound {
		if _, intake := ctx.Value(intakeStartActorKey{}).(intakeStartActor); !intake {
			id, trusted, err := trustedBPMNProcessStartActorID(ctx, tenantID)
			if err != nil {
				return nil, "", err
			}
			if trusted {
				actor, err := e.resolveAssignmentUser(ctx, e.client, id, tenantID)
				if err != nil {
					return nil, "", err
				}
				return actor, actor.Name, nil
			}
		}
	}
	return resolveBPMNProcessStartActor(ctx, e.client, tenantID, variables)
}
