package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	assignment "itsm-backend/handlers/common/workitemassignment"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/service/bpmn"
)

const callbackProvenanceAction = "callback_execution_provenance"

type callbackActorKey struct{}

// This private context is populated only after command authentication or from
// an exact durable callback audit. Workflow variables never establish identity.
type callbackActor struct {
	id, nativeTenant, targetTenant int
	source                         string
}

func recordCallbackProvenance(ctx context.Context, client *ent.Client, row *ent.ProcessCallbackOutbox) error {
	actor, ok := ctx.Value(callbackActorKey{}).(callbackActor)
	if !ok {
		return nil
	} // Old/system commands remain usable, but cannot assign.
	if actor.id <= 0 || actor.nativeTenant <= 0 || actor.targetTenant != row.TenantID || actor.source == "" {
		return fmt.Errorf("invalid callback actor provenance")
	}
	instance, err := client.ProcessInstance.Query().Where(processinstance.ID(row.ProcessInstanceID), processinstance.TenantID(row.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	_, err = client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetProcessDefinitionID(instance.ProcessDefinitionID).SetActivityID(row.ElementID).SetActivityType(row.CallbackKind).SetAction(callbackProvenanceAction).SetUserID(actor.id).SetTenantID(row.TenantID).SetMetadata(map[string]interface{}{
		"execution_key": row.ExecutionKey, "outbox_id": row.ID, "process_task_id": row.ProcessTaskID, "task_id": row.TaskID, "native_tenant_id": actor.nativeTenant, "target_tenant_id": actor.targetTenant, "source": actor.source,
	}).Save(ctx)
	return err
}

func callbackMetadataInt(value interface{}) int {
	n, err := strconv.Atoi(fmt.Sprint(value))
	if err != nil {
		return 0
	}
	return n
}
func loadCallbackProvenance(ctx context.Context, client *ent.Client, row *ent.ProcessCallbackOutbox) (callbackActor, error) {
	records, err := client.ProcessAuditLog.Query().Where(processauditlog.TenantID(row.TenantID), processauditlog.ProcessInstanceID(row.ProcessInstanceID), processauditlog.ActionEQ(callbackProvenanceAction)).All(ctx)
	if err != nil {
		return callbackActor{}, err
	}
	var result callbackActor
	count := 0
	for _, r := range records {
		// A key collision with any mismatching event identity is invalid evidence.
		if r.Metadata["execution_key"] != row.ExecutionKey {
			continue
		}
		count++
		if callbackMetadataInt(r.Metadata["outbox_id"]) != row.ID || callbackMetadataInt(r.Metadata["process_task_id"]) != row.ProcessTaskID || r.Metadata["task_id"] != row.TaskID || r.ActivityID != row.ElementID || r.ActivityType != row.CallbackKind || callbackMetadataInt(r.Metadata["target_tenant_id"]) != row.TenantID {
			return callbackActor{}, fmt.Errorf("callback provenance event mismatch")
		}
		source, _ := r.Metadata["source"].(string)
		result = callbackActor{r.UserID, callbackMetadataInt(r.Metadata["native_tenant_id"]), row.TenantID, source}
	}
	if count != 1 || result.id <= 0 || result.nativeTenant <= 0 || result.source == "" {
		return callbackActor{}, fmt.Errorf("callback actor provenance missing or ambiguous")
	}
	return result, nil
}

// NewWorkflowAssignmentBoundary uses the existing durable execution key and
// audit envelope, rechecking identities in the caller's directory snapshot.
func NewWorkflowAssignmentBoundary(directory database.DirectorySnapshot) workflowcallback.AssignmentBoundary {
	return func(ctx context.Context, tx *ent.Tx, tenantID int) (*assignment.Writer, assignment.Command, error) {
		key, ok := bpmn.BPMNCallbackExecutionKey(ctx)
		if !ok || tx == nil || directory == nil {
			return nil, assignment.Command{}, fmt.Errorf("verified callback assignment provenance is required")
		}
		row, err := tx.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKeyEQ(key), processcallbackoutbox.TenantID(tenantID)).Only(ctx)
		if err != nil {
			return nil, assignment.Command{}, err
		}
		actor, err := loadCallbackProvenance(ctx, tx.Client(), row)
		if err != nil {
			return nil, assignment.Command{}, err
		}
		instance, err := tx.ProcessInstance.Query().Where(processinstance.ID(row.ProcessInstanceID), processinstance.TenantID(tenantID)).Only(ctx)
		if err != nil {
			return nil, assignment.Command{}, err
		}
		command := assignment.Command{TenantID: tenantID, ActorID: actor.id, ActorTenantID: actor.nativeTenant, Source: actor.source}
		writer := assignment.NewWriter(EnqueueWorkItemAssignment, func(ctx context.Context, client *ent.Client, cmd assignment.Command) error {
			if instance.BusinessID <= 0 || cmd.WorkItemID != instance.BusinessID || client != tx.Client() || cmd.TenantID != actor.targetTenant || cmd.ActorID != actor.id || cmd.ActorTenantID != actor.nativeTenant || cmd.Source != actor.source {
				return fmt.Errorf("callback assignment actor mismatch")
			}
			view, close, err := directory.Open(ctx, tx, tenantID)
			if err != nil {
				return err
			}
			current, validationErr := authorization.ResolveCurrentTenantUser(ctx, view, actor.id, tenantID, time.Now())
			if validationErr == nil && current.TenantID != actor.nativeTenant {
				validationErr = fmt.Errorf("callback actor native tenant changed")
			}
			if validationErr == nil && cmd.AssigneeID > 0 {
				_, validationErr = authorization.ResolveCurrentTenantUser(ctx, view, cmd.AssigneeID, tenantID, time.Now())
			}
			closeErr := close()
			if validationErr != nil {
				return validationErr
			}
			return closeErr
		})
		return writer, command, nil
	}
}
