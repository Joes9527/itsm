//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func changeCallbackContext(f *changeLifecycleFixture, actor *ent.User) context.Context {
	ctx := service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{TenantID: f.tenant.ID, UserID: actor.ID})
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	return context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actor.ID)
}

func TestWorkItemChangeLifecycleDefaultWorkflow(t *testing.T) {
	for _, kind := range []string{"normal", "standard", "emergency", "standard_cab_rejected", "standard_cab_write_only", "standard_cab_approved"} {
		outcomes := []string{"successful", "failed", "rolled_back"}
		if kind == "standard_cab_rejected" || kind == "standard_cab_write_only" || kind == "standard_cab_approved" {
			outcomes = []string{"rejected"}
			if kind == "standard_cab_approved" {
				outcomes = []string{"successful"}
			}
		}
		for _, outcome := range outcomes {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				recordType := kind
				if kind == "standard_cab_rejected" || kind == "standard_cab_write_only" || kind == "standard_cab_approved" {
					recordType = "standard"
				}
				f := newChangeLifecycleFixture(t, recordType)
				h := f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler)
				h.SetChangeService(f.owner)
				// Refuse exactly the downstream token write after a real command
				// has committed. Cover user and service continuation owners.
				var injected atomic.Bool
				retryChecked := false
				faultTarget := ""
				if outcome == "successful" && kind == "normal" {
					faultTarget = "Activity_CABApproval"
				}
				if outcome == "successful" && kind == "standard" {
					faultTarget = "Activity_Schedule"
				}
				if faultTarget != "" {
					f.runtime.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							if mutation, ok := m.(*ent.ProcessInstanceMutation); ok {
								if activity, exists := mutation.CurrentActivityID(); exists && activity == faultTarget && injected.CompareAndSwap(false, true) {
									return nil, errors.New("injected continuation failure")
								}
							}
							return next.Mutate(ctx, m)
						})
					})
				}

				key := "change_normal_flow"
				if kind == "emergency" {
					key = "change_emergency_flow"
				}
				xml, err := os.ReadFile("../../service/bpmn/" + key + ".bpmn")
				require.NoError(t, err)
				f.client.ProcessDefinition.Update().Where(processdefinition.Key(key)).SetBpmnXML(xml).ExecX(f.ctx)
				approver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("cab").SetName("CAB").SetEmail("cab@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
				role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetName("Change manager").SetCode("change_manager").SaveX(f.ctx)
				approver.Update().AddRoleIDs(role.ID).ExecX(f.ctx)
				if recordType == "standard" {
					template := f.client.StandardChange.Create().SetTenantID(f.tenant.ID).SetCreatedBy(f.actor.ID).SetTitle("standard policy").SetJustification("routine").SetImplementationPlan("deploy package").SetRollbackPlan("restore previous package").SetIsActive(true).SetApprovalRequired(false).SaveX(f.ctx)
					tx, err := f.runtime.Tx(f.ctx)
					require.NoError(t, err)
					defer tx.Rollback()
					plan, err := f.owner.Prepare(f.ctx, tx, creation.ResolvedIntake{Identity: creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, Role: "super_admin", Channel: "http"}, Command: creation.CreateWorkItemCommand{Title: "standard", Change: &creation.ChangeInput{StandardTemplateID: &template.ID}}})
					require.NoError(t, err)
					item := tx.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("Standard").SetTicketNumber("CHG-DEFAULT-POLICY").SetRecordClass("change_request").SetStatus("draft").SaveX(f.ctx)
					ref, err := f.owner.CreateExtension(f.ctx, tx, item, plan)
					require.NoError(t, err)
					require.NoError(t, tx.Commit())
					f.c = f.client.Change.GetX(f.ctx, ref.ID)
				}
				f.apply(t, f.command("submit", "submit"))
				instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
				if kind == "standard_cab_rejected" || kind == "standard_cab_write_only" || kind == "standard_cab_approved" {
					variables := instance.Variables
					variables["approval_required"] = true
					instance.Update().SetVariables(variables).ExecX(f.ctx)
				}
				if kind == "standard_cab_write_only" {
					approver = approver.Update().SetRole("agent").SaveX(f.ctx)
					writeRole := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Change writer").SaveX(f.ctx)
					permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Change write").SetResource("change").SetAction("write").SaveX(f.ctx)
					f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(writeRole.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
					authorization.InvalidateAllPermissionCaches()
					t.Cleanup(authorization.InvalidateAllPermissionCaches)
				}
				complete := func(node string, actor *ent.User, vars map[string]interface{}) {
					t.Helper()
					task := f.client.ProcessTask.Query().Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey(node), processtask.StatusNEQ("completed")).OnlyX(f.ctx)
					vars["actor_id"] = 999999
					vars["approval_decision_id"] = 999999
					vars["version"] = bpmn.GetIntFromVars(f.client.ProcessInstance.GetX(f.ctx, instance.ID).Variables, "version")
					err := f.engine.CompleteTask(changeCallbackContext(f, actor), task.TaskID, vars)
					require.NoError(t, err)
					for i := 0; i < 4; i++ {
						if injected.Load() && !retryChecked {
							pending := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Status("pending")).OnlyX(f.ctx)
							frozen := bpmn.GetIntFromVars(pending.Variables, "version")
							require.Equal(t, frozen+1, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
							require.Equal(t, frozen, bpmn.GetIntFromVars(f.client.ProcessInstance.GetX(f.ctx, instance.ID).Variables, "version"))
							require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationID(pending.ExecutionKey)).CountX(f.ctx))
							retryChecked = true
						}

						f.client.ProcessCallbackOutbox.Update().SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
						_, err = f.engine.ProcessPendingCallbacks(f.ctx, "change-default-worker", 20)
						if err != nil {
							require.True(t, injected.Load() && !retryChecked, "unexpected callback error: %v", err)
						}
					}
					for _, row := range f.client.ProcessCallbackOutbox.Query().AllX(f.ctx) {
						require.Equal(t, "completed", row.Status, "%s: %s", row.Action, row.LastErrorClass)
						require.NotContains(t, row.Variables, "actor_id")
						require.NotContains(t, row.Variables, "approval_decision_id")
						require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationID(row.ExecutionKey)).CountX(f.ctx))
					}
					item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
					current := f.client.ProcessInstance.GetX(f.ctx, instance.ID)
					require.Equal(t, item.Version, bpmn.GetIntFromVars(current.Variables, "version"))
					require.Equal(t, item.Status, current.Variables["status"])
					count := f.client.ProcessCallbackOutbox.Query().CountX(f.ctx)
					require.Error(t, f.engine.CompleteTask(changeCallbackContext(f, actor), task.TaskID, vars), "duplicate completion cannot enqueue again")
					require.Equal(t, count, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				}
				complete("Activity_Assessment", f.actor, map[string]interface{}{"evidence": "risk and implementation reviewed"})
				if kind == "standard_cab_rejected" {
					complete("Activity_CABApproval", approver, map[string]interface{}{"approvalAction": "reject", "approvalResult": "rejected", "evidence": "CAB rejected", "approvalComment": "unacceptable risk"})
					require.Equal(t, "rejected", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status, "real CAB rejection overrides configured policy")
					assertChangeAuthorizationAudit(t, f, "cab_decision", f.client.ProcessApprovalDecision.Query().OnlyX(f.ctx).ID)
					require.Equal(t, "completed", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
					return
				}
				if kind == "standard_cab_write_only" {
					task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
					require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, approver), task.TaskID, map[string]interface{}{"version": 3, "approvalAction": "approve", "approvalResult": "approved"}))
					require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
					require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status, "real CAB decision still requires current approve permission")
					require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
					require.Equal(t, "pending", f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Action("approve_change")).OnlyX(f.ctx).Status)
					return
				}
				if kind != "standard" {
					complete("Activity_CABApproval", approver, map[string]interface{}{"approvalAction": "approve", "approvalResult": "approved", "evidence": "CAB approves"})
					require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
					assertChangeAuthorizationAudit(t, f, "cab_decision", f.client.ProcessApprovalDecision.Query().OnlyX(f.ctx).ID)
				} else {
					require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx), "configured policy is not human approval")
					assertChangeAuthorizationAudit(t, f, "standard_policy", 0)
				}
				require.Equal(t, "approved", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
				if kind != "emergency" {
					complete("Activity_Schedule", f.actor, map[string]interface{}{"planned_start_date": time.Now().Add(-time.Hour).Format(time.RFC3339), "planned_end_date": time.Now().Add(time.Hour).Format(time.RFC3339)})
				}
				complete("Activity_Implement", f.actor, map[string]interface{}{})
				complete("Activity_Verify", f.actor, map[string]interface{}{"outcome": outcome, "evidence": "observed implementation result", "actual_end_date": time.Now().Format(time.RFC3339Nano)})
				pir := f.client.ChangePIR.Create().SetChangeID(f.c.ID).SetTenantID(f.tenant.ID).SetReviewerID(f.actor.ID).SetOverallResult(outcome).SetSuccessSummary("validated result").SetReviewDate(time.Now()).SaveX(f.ctx)
				complete("Activity_Review", f.actor, map[string]interface{}{"pir_id": pir.ID, "evidence": "PIR reviewed"})
				complete("Activity_Close", f.actor, map[string]interface{}{"pir_id": pir.ID, "evidence": "reviewed record closed"})
				if faultTarget != "" {
					require.True(t, retryChecked, "committed callback retry was exercised")
				}
				require.Equal(t, "completed", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
				require.Equal(t, "completed", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
				require.Equal(t, outcome, f.client.Change.GetX(f.ctx, f.c.ID).Outcome)
				if kind != "standard" {
					require.Equal(t, fmt.Sprint(f.c.WorkItemID), f.client.ProcessApprovalDecision.Query().OnlyX(f.ctx).BusinessID)
				}
			})
		}
	}
}

func TestWorkItemChangeLifecycleCallbackDeniesStaleAndRevokedActor(t *testing.T) {
	for _, invalid := range []string{"stale_version", "revoked_actor", "missing_evidence"} {
		t.Run(invalid, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			h := f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler)
			h.SetChangeService(f.owner)
			xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
			require.NoError(t, err)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			f.apply(t, f.command("submit", "submit"))
			var failReceipt atomic.Bool
			failReceipt.Store(true)
			f.runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if failReceipt.Load() {
						return nil, errors.New("receipt unavailable")
					}
					return next.Mutate(ctx, m)
				})
			})
			task := f.client.ProcessTask.Query().OnlyX(f.ctx)
			vars := map[string]interface{}{"version": 2, "evidence": "observed risk"}
			if invalid == "stale_version" {
				vars["version"] = 1
			}
			if invalid == "missing_evidence" {
				delete(vars, "evidence")
			}
			require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, vars))
			row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
			require.Equal(t, "pending", row.Status)
			require.Equal(t, f.actor.ID, row.ActorID)
			require.Equal(t, "workflow", row.ActorSource)
			require.Equal(t, vars["version"], bpmn.GetIntFromVars(row.Variables, "version"))
			failReceipt.Store(false)
			if invalid == "revoked_actor" {
				f.actor.Update().SetActive(false).ExecX(f.ctx)
			}
			f.client.ProcessCallbackOutbox.UpdateOneID(row.ID).SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
			_, err = f.engine.ProcessPendingCallbacks(f.ctx, "deny-worker", 10)
			require.Error(t, err)
			require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
			require.Equal(t, 0, f.client.AuditLog.Query().Where(auditlog.OperationID(row.ExecutionKey)).CountX(f.ctx))
			require.Equal(t, vars["version"], bpmn.GetIntFromVars(f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Variables, "version"), "never substitute current version")
		})
	}
}

// The task-start/completion producers still have native-tenant equality (A4c2).
// Seed the durable workflow identity here, then exercise the actual restricted
// worker and owning command authorization; never claim this is an MSP HTTP E2E.
func TestWorkItemChangeLifecycleDurableServiceCallbackMSP(t *testing.T) {
	for _, access := range []string{"allocated", "revoked", "foreign", "competing", "revoked_replay"} {
		t.Run(access, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
			provider := f.client.Tenant.Create().SetCode("callback-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
			actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("Provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
			allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
			permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Change write").SetResource("change").SetAction("write").SaveX(f.ctx)
			f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
			authorization.InvalidateAllPermissionCaches()
			t.Cleanup(authorization.InvalidateAllPermissionCaches)
			f.apply(t, f.command("submit", "native-submit"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			xml := []byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="msp" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:sequenceFlow id="startflow" sourceRef="start" targetRef="assess"/><bpmn:serviceTask id="assess"><bpmn:extensionElements><bpmn:metaData name="service_task_type">change_task</bpmn:metaData><bpmn:metaData name="action">assess_risk</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="endflow" sourceRef="assess" targetRef="end"/></bpmn:process></bpmn:definitions>`)
			f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML(xml).ExecX(f.ctx)
			instance.Update().SetInitiator(fmt.Sprint(actor.ID)).SetCurrentActivityID("assess").ExecX(f.ctx)
			row := f.client.ProcessCallbackOutbox.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetExecutionKey("msp-assess").SetCallbackKind("service_task").SetHandlerID("change_service_handler").SetTaskType("change_task").SetElementID("assess").SetAction("assess_risk").SetVariables(map[string]interface{}{"version": 2, "evidence": "verified MSP assessment"}).SaveX(f.ctx)
			f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetChangeService(f.owner)
			if access == "revoked" {
				allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			}
			if access == "foreign" {
				actor.Update().ClearMspRole().ExecX(f.ctx)
			}
			if access == "revoked_replay" {
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if mutation, ok := m.(*ent.ProcessInstanceMutation); ok {
							if activity, exists := mutation.CurrentActivityID(); exists && activity == "end" {
								return nil, errors.New("continuation unavailable")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
				_, err := f.engine.ProcessPendingCallbacks(f.ctx, "msp-first", 10)
				require.Error(t, err)
				require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
				allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
				f.client.ProcessCallbackOutbox.UpdateOneID(row.ID).SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
				_, err = f.engine.ProcessPendingCallbacks(f.ctx, "msp-replay", 10)
				require.Error(t, err)
				require.Equal(t, "pending", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
				require.Equal(t, "handler_error", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).LastErrorClass, "revoked authority denies replay before continuation")
				require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationID(row.ExecutionKey)).CountX(f.ctx))
				return
			}
			var err error
			if access == "competing" {
				f.client.ProcessCallbackOutbox.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetExecutionKey("competing-assess").SetCallbackKind("service_task").SetHandlerID("change_service_handler").SetTaskType("change_task").SetElementID("assess").SetAction("assess_risk").SetVariables(map[string]interface{}{"version": 2, "evidence": "second assessment"}).SaveX(f.ctx)
				var workers sync.WaitGroup
				for i := 0; i < 2; i++ {
					workers.Add(1)
					go func(i int) {
						defer workers.Done()
						_, _ = f.engine.ProcessPendingCallbacks(f.ctx, fmt.Sprint("competing-", i), 1)
					}(i)
				}
				workers.Wait()
				require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
				require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationIDIn(row.ExecutionKey, "competing-assess")).CountX(f.ctx))
				require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Status("completed")).CountX(f.ctx))
				return
			}
			_, err = f.engine.ProcessPendingCallbacks(f.ctx, "msp-worker", 10)

			if access != "allocated" {
				require.Error(t, err)
				require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
				require.Zero(t, f.client.AuditLog.Query().Where(auditlog.OperationID(row.ExecutionKey)).CountX(f.ctx))
				return
			}
			require.NoError(t, err)
			require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			require.Equal(t, actor.ID, f.client.Change.GetX(f.ctx, f.c.ID).AssessedBy)
			require.Equal(t, actor.ID, f.client.AuditLog.Query().Where(auditlog.OperationID(row.ExecutionKey)).OnlyX(f.ctx).UserID)
			require.Equal(t, "completed", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
		})
	}
}

func assertChangeAuthorizationAudit(t *testing.T, f *changeLifecycleFixture, kind string, decisionID int) {
	t.Helper()
	receipt := f.client.AuditLog.Query().Where(auditlog.TenantID(f.tenant.ID), auditlog.Action("change.authorize"), auditlog.Path(fmt.Sprint(f.c.WorkItemID))).OnlyX(f.ctx)
	var facts map[string]any
	require.NotNil(t, receipt.RequestBody)
	require.NoError(t, json.Unmarshal([]byte(*receipt.RequestBody), &facts))
	require.Equal(t, kind, facts["authorizationKind"])
	require.EqualValues(t, decisionID, facts["approvalDecisionId"])
	if kind == "cab_decision" {
		require.NotContains(t, facts, "standardPolicy")
	} else {
		require.Contains(t, facts, "standardPolicy")
	}
}
