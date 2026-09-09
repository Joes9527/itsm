//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/service/bpmn"
	"testing"
	"time"
)

func TestWorkItemChangeMetadataCallbackRequiresDurableIdentity(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	h := f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler)
	h.SetChangeService(f.owner)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	effect, err := h.Execute(changeCallbackContext(f, f.actor), nil, map[string]interface{}{"action": "update_change", "change_id": f.c.ID, "title": "forged", "version": before.Version, "bpmn_callback_execution_key": "forged"})
	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackEffectBlocked, effect.Status)
	require.Nil(t, effect.LifecycleResult)
	require.Equal(t, before.Title, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Title)
}

func TestWorkItemChangeMetadataCallbackContinuation(t *testing.T) {
	for _, mode := range []string{"complete", "continuation_retry", "other_unresolved", "forged_actor", "stale_version", "audit_rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			h := f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler)
			h.SetChangeService(f.owner)
			xml := []byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="change" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:userTask id="metadata" name="Edit"><bpmn:extensionElements><bpmn:metaData name="service_task_type">change_task</bpmn:metaData><bpmn:metaData name="action">update_change</bpmn:metaData></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="metadata"/><bpmn:sequenceFlow id="b" sourceRef="metadata" targetRef="end"/></bpmn:process></bpmn:definitions>`)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			f.apply(t, f.command("submit", "submit"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			task := f.client.ProcessTask.Query().OnlyX(f.ctx)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			enabled := true
			f.runtime.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if enabled && mode == "continuation_retry" {
						if update, ok := m.(*ent.ProcessInstanceMutation); ok {
							if value, exists := update.CurrentActivityID(); exists && value == "end" {
								return nil, errors.New("continuation unavailable")
							}
						}
					}
					if enabled && mode == "audit_rollback" {
						if update, ok := m.(*ent.AuditLogMutation); ok {
							if action, _ := update.Action(); action == "change.metadata" {
								return nil, errors.New("receipt unavailable")
							}
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			if mode == "other_unresolved" {
				f.client.ProcessCallbackOutbox.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetExecutionKey("other").SetCallbackKind("service_task").SetHandlerID("change_service_handler").SetTaskType("change_task").SetElementID("metadata").SetAction("assess_risk").SetStatus("blocked").SetVariables(map[string]interface{}{"version": before.Version}).SaveX(f.ctx)
			}
			version := before.Version
			if mode == "stale_version" {
				version++
			}
			require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, map[string]interface{}{"title": "Durable metadata", "version": version}))
			row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Action("update_change")).OnlyX(f.ctx)
			if mode == "forged_actor" {
				observed := f.client.Ticket.GetX(f.ctx, before.ID)
				meta := f.command("metadata", row.ExecutionKey).Meta
				meta.ActorID += 999
				meta.Source = "workflow"
				meta.CorrelationID = instance.ProcessInstanceID
				title := "Forged metadata"
				_, err := f.owner.ApplyChangeWorkflowCallback(bpmn.WithBPMNCallbackExecutionKey(f.ctx, row.ExecutionKey), workflowcallback.ChangeCommand{Meta: meta, TenantID: f.tenant.ID, ChangeID: f.c.ID, Action: "update_change", Title: &title})
				require.Error(t, err)
				require.Equal(t, observed.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
				return
			}
			_, err := f.engine.ProcessPendingCallbacks(f.ctx, "metadata-worker", 10)
			require.NoError(t, err)
			after := f.client.Ticket.GetX(f.ctx, before.ID)

			if mode != "complete" && mode != "continuation_retry" {
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.Title, after.Title)
				require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
				return
			}
			require.Equal(t, "Durable metadata", after.Title)
			require.Equal(t, before.Version+1, after.Version)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
			if mode == "continuation_retry" {
				require.Equal(t, "pending", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
				require.Equal(t, before.Version, bpmn.GetIntFromVars(f.client.ProcessInstance.GetX(f.ctx, instance.ID).Variables, "version"))
				enabled = false
				f.client.ProcessCallbackOutbox.UpdateOneID(row.ID).SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
				_, err = f.engine.ProcessPendingCallbacks(f.ctx, "metadata-retry", 10)
				require.NoError(t, err)
			}
			require.Equal(t, "completed", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
			require.Equal(t, before.Version+1, bpmn.GetIntFromVars(f.client.ProcessInstance.GetX(f.ctx, instance.ID).Variables, "version"))
			require.Equal(t, before.Version+1, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
		})
	}
}

func TestWorkItemChangeMetadataCallbackOwnerRejectsUnprovenKey(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	meta := f.command("metadata", "forged-processing").Meta
	meta.Source = "workflow"
	meta.CorrelationID = "claimed-instance"
	title := "unproven metadata"
	result, err := f.owner.ApplyChangeWorkflowCallback(bpmn.WithBPMNCallbackExecutionKey(f.ctx, meta.OperationID), workflowcallback.ChangeCommand{Meta: meta, TenantID: f.tenant.ID, ChangeID: f.c.ID, Action: "update_change", Title: &title})
	require.True(t, err != nil || result.Status == workflowcallback.StatusBlocked, "an arbitrary context key cannot replace a persisted processing callback")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
	require.Equal(t, before.Title, f.client.Ticket.GetX(f.ctx, before.ID).Title)
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
}
