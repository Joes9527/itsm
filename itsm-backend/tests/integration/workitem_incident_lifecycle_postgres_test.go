//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

func incidentLifecycleFixture(t *testing.T) *incidentEffectsFixture {
	f := newIncidentEffectsFixture(t)
	for _, name := range []string{"032_workitem_sla_cycle", "033_incident_status_events"} {
		_, err := f.db.ExecContext(f.ctx, migration.GetMigrationSQL(name))
		require.NoError(t, err)
	}
	f.actor.Update().SetRole("super_admin").ExecX(f.ctx)
	f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetStatus("in_progress").ExecX(f.ctx)
	return f
}

func TestWorkItemIncidentLifecycleRuleStatusRejectsAndRollsBack(t *testing.T) {
	for _, failure := range []string{"permission", "evidence", "receipt"} {
		t.Run(failure, func(t *testing.T) {
			f := incidentLifecycleFixture(t)
			before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
			action := map[string]interface{}{"type": "change_status", "status": "resolved", "resolution": "service recovery verified"}
			if failure == "permission" {
				f.actor.Update().SetRole("unknown-no-permissions").ExecX(f.ctx)
			}
			if failure == "evidence" {
				delete(action, "resolution")
			}
			f.rule(action)
			if failure == "receipt" {
				f.client.IncidentRuleActionReceipt.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, err := next.Mutate(ctx, m); err != nil {
							return nil, err
						}
						return nil, errors.New("rule receipt failure")
					})
				})
			}
			require.Error(t, f.engine.Deliver(f.ctx, f.event))
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Status, after.Status)
			require.True(t, after.ResolvedAt.IsZero())
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentEvent.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx))
		})
	}
}

func TestWorkItemIncidentLifecycleReopenSLAAndRollback(t *testing.T) {
	f := incidentLifecycleFixture(t)
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("Incident SLA").SetResponseTime(30).SetResolutionTime(90).SaveX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, item, &policy.ID))
	require.NoError(t, tx.Commit())
	cmd := incidentPGCommand(f, "resolve-cycle")
	resolved, err := f.svc.ApplyIncidentCommand(f.ctx, cmd)
	require.NoError(t, err)
	closeCmd := cmd
	closeCmd.Action = "close"
	closeCmd.Reason = "confirmed stable"
	closeCmd.Meta.ExpectedVersion = resolved.Version
	closeCmd.Meta.OperationID = "close-cycle"
	closed, err := f.svc.ApplyIncidentCommand(f.ctx, closeCmd)
	require.NoError(t, err)
	reopen := cmd
	reopen.Action = "reopen"
	reopen.Meta.ExpectedVersion = closed.Version
	reopen.Meta.OperationID = "reopen-cycle"
	before := f.client.Ticket.GetX(f.ctx, item.ID)
	fail := true
	f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			action, _ := m.Field("action")
			if fail && action == "incident.reopen" {
				return nil, errors.New("reopen audit unavailable")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = f.svc.ApplyIncidentCommand(f.ctx, reopen)
	require.ErrorContains(t, err, "reopen audit unavailable")
	after := f.client.Ticket.GetX(f.ctx, item.ID)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.SLACycleNumber, after.SLACycleNumber)
	require.Equal(t, "closed", after.Status)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).CountX(f.ctx))
	fail = false
	reopened, err := f.svc.ApplyIncidentCommand(f.ctx, reopen)
	require.NoError(t, err)
	require.Equal(t, "in_progress", reopened.Status)
	after = f.client.Ticket.GetX(f.ctx, item.ID)
	require.Equal(t, before.SLACycleNumber+1, after.SLACycleNumber)
	require.True(t, after.FirstResponseAt.IsZero())
	require.True(t, after.ResolvedAt.IsZero())
	require.Nil(t, after.ClosedAt)
	require.WithinDuration(t, after.SLACycleStartedAt.Add(30*time.Minute), after.SLAResponseDeadline, time.Second)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).CountX(f.ctx))
	replay, err := f.svc.ApplyIncidentCommand(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, resolved.Version, replay.Version)
	require.Equal(t, after.SLACycleNumber, f.client.Ticket.GetX(f.ctx, item.ID).SLACycleNumber)
}

func incidentPGCommand(f *incidentEffectsFixture, key string) dto.IncidentCommand {
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	return dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: key}, IncidentID: f.inc.ID, Action: "resolve", Resolution: "workaround restored service; validation passed"}
}

func TestWorkItemIncidentLifecycleConcurrentCommandsAndFrozenEvent(t *testing.T) {
	f := incidentLifecycleFixture(t)
	// An unresolved related Problem must not gate service restoration.
	problemItem := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("problem").SetTicketNumber("PRB-OPEN").SetTitle("investigating cause").SetStatus("open").SetPriority("high").SaveX(f.ctx)
	f.client.Problem.Create().SetWorkItemID(problemItem.ID).SaveX(f.ctx)
	f.client.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(f.inc.WorkItemID).SetTargetWorkItemID(problemItem.ID).SetRelationType("investigated_by").SetCreatedByID(f.actor.ID).SaveX(f.ctx)
	cmd := incidentPGCommand(f, "resolve-a")
	other := cmd
	other.Meta.OperationID = "resolve-b"
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range []dto.IncidentCommand{cmd, other} {
		wg.Add(1)
		go func(c dto.IncidentCommand) {
			defer wg.Done()
			_, err := f.svc.ApplyIncidentCommand(f.ctx, c)
			errs <- err
		}(c)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, cmd.Meta.ExpectedVersion+1, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).CountX(f.ctx))
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventType("incident.status_changed")).OnlyX(f.ctx)
	f.rule(metricAction("status")).Update().SetConditions(map[string]interface{}{"event_type": []string{"incident.status_changed"}}).ExecX(f.ctx)
	f.rule(metricAction("creation-only"))
	consumer := service.NewIncidentStatusDeliveryHandler(f.engine)
	require.NoError(t, consumer.Deliver(f.ctx, event))
	require.NoError(t, consumer.Deliver(f.ctx, event))
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	// Existing creation provenance still works after 033.
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	require.Equal(t, 2, f.client.IncidentMetric.Query().CountX(f.ctx))
}

func TestWorkItemIncidentLifecycleHTTPCallbackParity(t *testing.T) {
	for _, entry := range []string{"http", "callback"} {
		t.Run(entry, func(t *testing.T) {
			f := incidentLifecycleFixture(t)
			cmd := incidentPGCommand(f, "resolve-entry")
			if entry == "http" {
				gin.SetMode(gin.TestMode)
				router := gin.New()
				handler := controller.NewIncidentController(f.svc, f.engine, nil, nil, nil, zap.NewNop().Sugar())
				router.POST("/incidents/:id/resolve", func(c *gin.Context) {
					c.Set("tenant_id", f.tenant.ID)
					c.Set("user_id", f.actor.ID)
					handler.ResolveIncident(c)
				})
				body, _ := json.Marshal(dto.IncidentCommandRequest{Version: cmd.Meta.ExpectedVersion, OperationID: cmd.Meta.OperationID, Resolution: cmd.Resolution})
				request := httptest.NewRequest("POST", fmt.Sprintf("/incidents/%d/resolve", f.inc.ID), strings.NewReader(string(body)))
				request.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, request)
				require.Equal(t, 200, rec.Code, rec.Body.String())
			} else {
				dep := f.client.ProcessDeployment.Create().SetDeploymentID("incident-entry").SetDeploymentName("incident-entry").SetTenantID(f.tenant.ID).SaveX(f.ctx)
				def := f.client.ProcessDefinition.Create().SetKey("incident-entry").SetName("incident-entry").SetBpmnXML([]byte("<definitions/>")).SetDeploymentID(dep.ID).SetTenantID(f.tenant.ID).SaveX(f.ctx)
				instance := f.client.ProcessInstance.Create().SetProcessInstanceID("incident-entry").SetProcessDefinitionKey(def.Key).SetProcessDefinitionID(def.ID).SetTenantID(f.tenant.ID).SetBusinessType("incident").SetBusinessID(f.inc.WorkItemID).SetInitiator(fmt.Sprint(f.actor.ID)).SetCurrentActivityID("resolve").SaveX(f.ctx)
				row := f.client.ProcessCallbackOutbox.Create().SetExecutionKey(cmd.Meta.OperationID).SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetCallbackKind("service_task").SetHandlerID("incident_service_handler").SetTaskType("incident_task").SetElementID("resolve").SetAction("resolve_incident").SetStatus("processing").SetVariables(map[string]interface{}{"version": cmd.Meta.ExpectedVersion, "resolution": cmd.Resolution}).SaveX(f.ctx)
				ctx := context.WithValue(f.ctx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
				ctx = bpmn.WithBPMNCallbackExecutionKey(ctx, row.ExecutionKey)
				h := bpmn.NewIncidentServiceTaskHandler(f.client, zap.NewNop().Sugar())
				h.SetIncidentService(f.svc)
				result, err := h.Execute(ctx, nil, map[string]interface{}{"action": "resolve_incident", "actor_id": 999, "tenant_id": 999})
				require.NoError(t, err)
				require.Equal(t, bpmn.CallbackEffectApplied, result.Status)
				replay, err := h.Execute(ctx, nil, map[string]interface{}{"action": "resolve_incident"})
				require.NoError(t, err)
				require.Equal(t, bpmn.CallbackEffectIdempotent, replay.Status)
			}
			item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
			require.Equal(t, "resolved", item.Status)
			require.Equal(t, cmd.Meta.ExpectedVersion+1, item.Version)
			require.False(t, item.ResolvedAt.IsZero())
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).CountX(f.ctx))
			require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
		})
	}
}

func TestWorkItemIncidentLifecycleHTTPStartConflictsAndTrustedIdentity(t *testing.T) {
	f := incidentLifecycleFixture(t)
	item := f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetStatus("new").SaveX(f.ctx)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := controller.NewIncidentController(f.svc, f.engine, nil, nil, nil, zap.NewNop().Sugar())
	routes := map[string]gin.HandlerFunc{"acknowledge": handler.AcknowledgeIncident, "start": handler.StartIncident, "resolve": handler.ResolveIncident}
	for action, h := range routes {
		router.POST("/incidents/:id/"+action, func(c *gin.Context) { c.Set("tenant_id", f.tenant.ID); c.Set("user_id", f.actor.ID); h(c) })
	}
	request := func(action, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", fmt.Sprintf("/incidents/%d/%s", f.inc.ID, action), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	version := item.Version
	for _, action := range []string{"acknowledge", "start", "resolve"} {
		body := fmt.Sprintf(`{"version":%d,"operationId":%q,"resolution":"verified restored","actorId":99999,"tenantId":99999}`, version, action)
		rec := request(action, body)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		version++
		same := request(action, fmt.Sprintf(`{"version":%d,"operationId":%q,"resolution":"verified restored"}`, version, action+"-same"))
		require.Equal(t, 400, same.Code, same.Body.String())
		require.Equal(t, version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	}
	require.Equal(t, "resolved", f.client.Ticket.GetX(f.ctx, item.ID).Status)
	receipts := f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).AllX(f.ctx)
	require.Len(t, receipts, 3)
	for _, receipt := range receipts {
		require.Equal(t, f.actor.ID, receipt.UserID)
		require.Equal(t, f.tenant.ID, receipt.TenantID)
	}
	require.Equal(t, 409, request("resolve", fmt.Sprintf(`{"version":%d,"operationId":"stale","resolution":"verified restored"}`, item.Version)).Code)
	require.Equal(t, 409, request("resolve", fmt.Sprintf(`{"version":%d,"operationId":"resolve","resolution":"changed evidence"}`, version-1)).Code)
	require.Equal(t, 400, request("start", `{"operationId":"missing-version"}`).Code)
}

func runIncidentLifecycleCallback(f *incidentEffectsFixture, action, key string, vars map[string]interface{}) (*bpmn.CallbackEffect, error) {
	dep := f.client.ProcessDeployment.Create().SetDeploymentID(key).SetDeploymentName(key).SetTenantID(f.tenant.ID).SaveX(f.ctx)
	def := f.client.ProcessDefinition.Create().SetKey(key).SetName(key).SetBpmnXML([]byte("<definitions/>")).SetDeploymentID(dep.ID).SetTenantID(f.tenant.ID).SaveX(f.ctx)
	instance := f.client.ProcessInstance.Create().SetProcessInstanceID(key).SetProcessDefinitionKey(def.Key).SetProcessDefinitionID(def.ID).SetTenantID(f.tenant.ID).SetBusinessType("incident").SetBusinessID(f.inc.WorkItemID).SetInitiator(fmt.Sprint(f.actor.ID)).SetCurrentActivityID("effect").SaveX(f.ctx)
	row := f.client.ProcessCallbackOutbox.Create().SetExecutionKey(key).SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetCallbackKind("service_task").SetHandlerID("incident_service_handler").SetTaskType("incident_task").SetElementID("effect").SetAction(action + "_incident").SetStatus("processing").SetVariables(vars).SaveX(f.ctx)
	ctx := bpmn.WithBPMNCallbackExecutionKey(context.WithValue(f.ctx, bpmn.BPMNTenantIDContextKey, f.tenant.ID), row.ExecutionKey)
	h := bpmn.NewIncidentServiceTaskHandler(f.client, zap.NewNop().Sugar())
	h.SetIncidentService(f.svc)
	return h.Execute(ctx, nil, map[string]interface{}{"action": action + "_incident"})
}

func TestWorkItemIncidentLifecycleAssignmentEscalationCallbacks(t *testing.T) {
	for _, action := range []string{"assign", "escalate"} {
		for _, failure := range []string{"success", "closed", "resolved", "audit"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				f := incidentLifecycleFixture(t)
				status := "in_progress"
				if action == "assign" {
					status = "new"
				}
				if failure == "closed" || failure == "resolved" {
					status = failure
				}
				item := f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetStatus(status).SaveX(f.ctx)
				if failure == "audit" {
					f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("audit fault") })
					})
				}
				effect, err := runIncidentLifecycleCallback(f, action, "effect", map[string]interface{}{"version": item.Version, "assignee_id": f.actor.ID, "escalation_level": 1})
				after := f.client.Ticket.GetX(f.ctx, item.ID)
				if failure != "success" {
					require.Error(t, err)
					require.Equal(t, item.Version, after.Version)
					require.Equal(t, item.Status, after.Status)
					require.Zero(t, f.client.IncidentEvent.Query().CountX(f.ctx))
					require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx))
					return
				}
				require.NoError(t, err)
				require.Equal(t, bpmn.CallbackEffectApplied, effect.Status)
				require.Equal(t, item.Version+1, after.Version)
				require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
				require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
				require.Equal(t, 2, f.client.OutboxEvent.Query().CountX(f.ctx))
				if action == "assign" {
					require.Equal(t, "assigned", after.Status)
					require.Equal(t, f.actor.ID, after.AssigneeID)
				} else {
					require.Equal(t, "escalated", after.Status)
					require.Equal(t, 1, f.client.Incident.GetX(f.ctx, f.inc.ID).EscalationLevel)
				}
			})
		}
	}
}

func TestWorkItemIncidentLifecycleEscalationLevelDoesNotEmitStatusEvent(t *testing.T) {
	f := incidentLifecycleFixture(t)
	item := f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetStatus("escalated").SaveX(f.ctx)
	f.client.Incident.UpdateOneID(f.inc.ID).SetEscalationLevel(1).ExecX(f.ctx)
	effect, err := runIncidentLifecycleCallback(f, "escalate", "raise", map[string]interface{}{"version": item.Version, "escalation_level": 3})
	require.NoError(t, err)
	require.Equal(t, item.Version+1, effect.LifecycleResult.Version)
	require.Equal(t, 3, f.client.Incident.GetX(f.ctx, f.inc.ID).EscalationLevel)
	require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
	_, err = runIncidentLifecycleCallback(f, "escalate", "same", map[string]interface{}{"version": item.Version + 1, "escalation_level": 3})
	require.Error(t, err)
	_, err = runIncidentLifecycleCallback(f, "escalate", "lower", map[string]interface{}{"version": item.Version + 1, "escalation_level": 2})
	require.Error(t, err)
	require.Equal(t, item.Version+1, f.client.Ticket.GetX(f.ctx, item.ID).Version)
}

func TestWorkItemIncidentLifecycleSameStateRuleStopsEventChain(t *testing.T) {
	f := incidentLifecycleFixture(t)
	f.rule(map[string]interface{}{"type": "change_status", "status": "resolved", "resolution": "already restored"}).Update().SetConditions(map[string]interface{}{"event_type": []string{"incident.status_changed"}, "status": []string{"resolved"}}).ExecX(f.ctx)
	result, err := f.svc.ApplyIncidentCommand(f.ctx, incidentPGCommand(f, "restore"))
	require.NoError(t, err)
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventType("incident.status_changed")).OnlyX(f.ctx)
	require.Error(t, service.NewIncidentStatusDeliveryHandler(f.engine).Deliver(f.ctx, event))
	require.Equal(t, result.Version, f.client.Ticket.GetX(f.ctx, result.WorkItemID).Version)
	require.Equal(t, 1, f.client.OutboxEvent.Query().Where(outboxevent.EventType("incident.status_changed")).CountX(f.ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
}
