package workitemmutation

import (
	"context"
	"fmt"
	"strconv"

	"itsm-backend/common/workitemidentity"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
)

// Mutation owners pair this RR read with their Ticket write fence. A mere
// snapshot read or row lock would miss a concurrently accepted callback.
func RequireSettledChangeCallbacks(ctx context.Context, tx *ent.Tx, tenantID, itemID int, ownMetadataCallback ...Meta) error {
	key, keyErr := workitemidentity.BusinessKey(workitemidentity.RecordClassChangeRequest, itemID)
	if keyErr != nil {
		return keyErr
	}
	ids, err := tx.ProcessInstance.Query().Where(processinstance.TenantID(tenantID), processinstance.BusinessType(workitemidentity.RecordClassChangeRequest), processinstance.BusinessID(itemID), processinstance.BusinessKey(key)).IDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		if len(ownMetadataCallback) > 0 {
			return &UnresolvedChangeCallbackError{}
		}
		return nil
	}
	unresolved, err := tx.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.TenantID(tenantID), processcallbackoutbox.ProcessInstanceIDIn(ids...), processcallbackoutbox.StatusNEQ("completed")).All(ctx)
	if err != nil {
		return err
	}
	if len(unresolved) == 0 && len(ownMetadataCallback) > 0 {
		return &UnresolvedChangeCallbackError{}
	}
	for _, row := range unresolved {
		if len(ownMetadataCallback) != 1 {
			return &UnresolvedChangeCallbackError{}
		}
		m := ownMetadataCallback[0]
		version, parseErr := strconv.Atoi(fmt.Sprint(row.Variables["version"]))
		if m.TenantID != tenantID || m.ActorID <= 0 || m.Source != "workflow" || m.OperationID == "" || row.ExecutionKey != m.OperationID || row.Status != "processing" || row.HandlerID != "change_service_handler" || row.Action != "update_change" || parseErr != nil || version != m.ExpectedVersion || version <= 0 {
			return &UnresolvedChangeCallbackError{}
		}
		instance, err := tx.ProcessInstance.Get(ctx, row.ProcessInstanceID)
		if err != nil {
			return err
		}
		if instance.Status != "running" || instance.CurrentActivityID != row.ElementID || instance.ProcessInstanceID != m.CorrelationID {
			return &UnresolvedChangeCallbackError{}
		}
		switch row.CallbackKind {
		case "user_task_callback":
			if row.ActorID != m.ActorID || row.ActorSource != "workflow" {
				return &UnresolvedChangeCallbackError{}
			}
			owns, err := tx.ProcessTask.Query().Where(processtask.ID(row.ProcessTaskID), processtask.TenantID(tenantID), processtask.ProcessInstanceID(instance.ID), processtask.TaskID(row.TaskID), processtask.TaskDefinitionKey(row.ElementID), processtask.Status("completed"), processtask.CallbackHandlerID(row.HandlerID), processtask.CallbackAction(row.Action)).Exist(ctx)
			if err != nil {
				return err
			}
			if !owns {
				return &UnresolvedChangeCallbackError{}
			}
		case "service_task":
			actor, err := strconv.Atoi(instance.Initiator)
			if err != nil || actor != m.ActorID || row.ActorID != 0 && row.ActorID != m.ActorID {
				return &UnresolvedChangeCallbackError{}
			}
		default:
			return &UnresolvedChangeCallbackError{}
		}
	}
	return nil
}

type UnresolvedChangeCallbackError struct{}

func (*UnresolvedChangeCallbackError) Error() string { return "prior callback is unresolved" }
