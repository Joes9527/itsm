package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service/bpmn"
	"strconv"
	"testing"
	"time"
)

func TestIncidentCallbackWorkerPersistsLifecycleBeforeContinuation(t *testing.T) {
	testIncidentCallbackWorkerContinuation(t, newBPMNAuthorizationFixture)
}

func testIncidentCallbackWorkerContinuation(t *testing.T, newFixture func(*testing.T) *bpmnAuthorizationFixture) {
	for _, kind := range []string{"service_task", "user_task_callback"} {
		for _, retry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retry_%v", kind, retry), func(t *testing.T) {
				f := newFixture(t)
				f.actor.Update().SetRole("super_admin").ExecX(f.userCtx)
				now := time.Now().UTC().Truncate(time.Second)
				setCallbackTestClock(f.engine, &now)
				task := f.seedNonParticipantApprovalTask(t, "incident-chain")
				task = task.Update().SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
				instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
				item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("chain").SetTicketNumber("INC-CHAIN").SetRecordClass("incident").SetStatus("new").SaveX(f.userCtx)
				inc := f.client.Incident.Create().SetWorkItemID(item.ID).SetSeverity("high").SaveX(f.userCtx)
				first := task.TaskDefinitionKey
				firstTag := "userTask"
				if kind == "service_task" {
					firstTag = "serviceTask"
				}
				xml := fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="chain" isExecutable="true">
<bpmn:startEvent id="start"/>
<bpmn:%s id="%s"><bpmn:extensionElements><bpmn:metaData name="service_task_type">incident_task</bpmn:metaData><bpmn:metaData name="action">acknowledge_incident</bpmn:metaData></bpmn:extensionElements></bpmn:%s>
<bpmn:serviceTask id="next"><bpmn:extensionElements><bpmn:metaData name="service_task_type">incident_task</bpmn:metaData><bpmn:metaData name="action">start_incident</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask>
<bpmn:serviceTask id="resolve"><bpmn:extensionElements><bpmn:metaData name="service_task_type">incident_task</bpmn:metaData><bpmn:metaData name="action">resolve_incident</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask>
<bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="%s"/><bpmn:sequenceFlow id="b" sourceRef="%s" targetRef="next"/><bpmn:sequenceFlow id="c" sourceRef="next" targetRef="resolve"/><bpmn:sequenceFlow id="d" sourceRef="resolve" targetRef="end"/></bpmn:process></bpmn:definitions>`, firstTag, first, firstTag, first, first)
				f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML([]byte(xml)).ExecX(f.userCtx)
				instance = instance.Update().SetBusinessType("incident").SetBusinessID(item.ID).SetInitiator(strconv.Itoa(f.actor.ID)).SetCurrentActivityID(first).SetVariables(map[string]interface{}{"incident_id": inc.ID, "version": item.Version, "resolution": "observed service restoration", "keep": "source"}).SaveX(f.userCtx)
				h := bpmn.NewIncidentServiceTaskHandler(f.client, zap.NewNop().Sugar())
				h.SetIncidentService(NewIncidentService(f.client, zap.NewNop().Sugar()))
				f.engine.CallbackRegistry().RegisterHandler(h)
				if retry {
					failNextCallbackTokenAdvance(f.client, "next", errors.New("continuation fault"))
				}
				if kind == "user_task_callback" {
					require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"version": item.Version}))
				} else {
					keys := []string{}
					scheduler := f.engine.forClient(f.client, &keys)
					require.NoError(t, scheduler.enqueueServiceTaskCallback(f.userCtx, instance, h, h.GetTaskType(), first, "acknowledge_incident", "", instance.Variables, false))
					now = now.Add(time.Second)
					_, err := f.engine.ProcessPendingCallbacks(context.Background(), "incident-worker", 20)
					if retry {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
				}
				if retry {
					require.Equal(t, item.Version+1, f.client.Ticket.GetX(f.userCtx, item.ID).Version)
					persisted := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
					require.Equal(t, item.Version, bpmn.GetIntFromVars(persisted.Variables, "version"))
					require.Equal(t, first, persisted.CurrentActivityID)
					require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.userCtx))
				}
				for i := 0; i < 5; i++ {
					now = now.Add(time.Minute)
					_, err := f.engine.ProcessPendingCallbacks(context.Background(), "incident-worker", 20)
					require.NoError(t, err)
				}
				final := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
				require.Equal(t, "end", final.CurrentActivityID)
				require.Equal(t, item.Version+3, bpmn.GetIntFromVars(final.Variables, "version"))
				require.Equal(t, "resolved", final.Variables["status"])
				require.Equal(t, "source", final.Variables["keep"])
				require.Equal(t, item.ID, final.BusinessID)
				require.Equal(t, "incident", final.BusinessType)
				require.Equal(t, item.Version+3, f.client.Ticket.GetX(f.userCtx, item.ID).Version)
				require.Equal(t, 3, f.client.IncidentEvent.Query().CountX(f.userCtx))
				require.Equal(t, 3, f.client.OutboxEvent.Query().CountX(f.userCtx))
				require.Equal(t, 3, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Status("completed")).CountX(f.userCtx))
			})
		}
	}
}

func TestIncidentLifecycleOutputRejectsIdentityAndArbitraryMerge(t *testing.T) {
	h := bpmn.NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	row := &ent.ProcessCallbackOutbox{Action: "start_incident", Variables: map[string]interface{}{"version": 1}}
	instance := &ent.ProcessInstance{BusinessType: "incident", BusinessID: 7}
	effect := bpmn.AppliedEffect("untyped", map[string]interface{}{"version": 2, "work_item_id": 99})
	_, err := callbackContinuationOutputs(h, row, instance, effect)
	require.Error(t, err)
	for _, result := range []workitemmutation.Result{{WorkItemID: 99, Version: 2, Status: "in_progress"}, {WorkItemID: 7, Version: 3, Status: "in_progress"}, {WorkItemID: 7, Version: 2}} {
		effect = &bpmn.CallbackEffect{Status: bpmn.CallbackEffectApplied, LifecycleResult: &result}
		_, err = callbackContinuationOutputs(h, row, instance, effect)
		require.Error(t, err)
	}
}
