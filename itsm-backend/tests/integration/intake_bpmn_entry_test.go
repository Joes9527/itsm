package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"testing"
	"time"
)

func TestIntakeBPMNCreationReplaysAfterSourceAdvanceFailure(t *testing.T) {
	for _, kind := range []string{"incident", "change"} {
		t.Run(kind, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			source, err := f.app.Create(ctx, f.identity, f.command)
			require.NoError(t, err)
			engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
			if kind == "incident" {
				engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetCreationApplication(f.app)
			} else {
				engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetCreationApplication(f.app)
			}
			deployment := f.client.ProcessDeployment.Create().SetTenantID(f.identity.TenantID).SetDeploymentID("creation").SetDeploymentName("Creation").SaveX(ctx)
			xml := []byte(fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="creation" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:serviceTask id="create"><bpmn:extensionElements><bpmn:metaData name="service_task_type">%s_task</bpmn:metaData><bpmn:metaData name="action">create_%s</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="create"/><bpmn:sequenceFlow id="b" sourceRef="create" targetRef="end"/></bpmn:process></bpmn:definitions>`, kind, kind))
			definition := f.client.ProcessDefinition.Create().SetTenantID(f.identity.TenantID).SetDeploymentID(deployment.ID).SetKey("creation").SetName("Creation").SetBpmnXML(xml).SetIsActive(true).SaveX(ctx)
			failed := false
			f.client.ProcessCallbackOutbox.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if typed, ok := m.(*ent.ProcessCallbackOutboxMutation); ok && !failed {
						if status, ok := typed.Status(); ok && status == "completed" {
							failed = true
							return nil, errors.New("injected source acknowledgement failure")
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			ctx = service.WithTrustedBPMNTenantContext(ctx, f.identity.TenantID)
			ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.identity.ActorID)
			instance, err := engine.StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), fmt.Sprintf("ticket:%d", source.WorkItemID), "generic", source.WorkItemID, map[string]any{"title": "Created by process", "description": "Process requested work", "priority": "high"}, "source-start")
			require.NoError(t, err)
			require.True(t, failed, "actual child creation must reach source acknowledgement")
			require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
			row := f.client.ProcessCallbackOutbox.Query().OnlyX(ctx)
			require.NotEqual(t, "completed", row.Status)
			row.Update().SetNextAttemptAt(time.Now().Add(-time.Second)).SaveX(ctx)
			_, err = engine.ProcessPendingCallbacks(ctx, "entry-test", 10)
			require.NoError(t, err)
			require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
			row = f.client.ProcessCallbackOutbox.GetX(ctx, row.ID)
			require.Equal(t, "completed", row.Status)
			reloaded := f.client.ProcessInstance.GetX(ctx, instance.ID)
			require.Equal(t, source.WorkItemID, reloaded.BusinessID)
			require.Equal(t, "generic", reloaded.BusinessType)
			require.Equal(t, fmt.Sprint(f.identity.ActorID), reloaded.Initiator)
			require.Contains(t, reloaded.Variables, "created_work_item_id")
			require.Contains(t, reloaded.Variables, "created_"+kind+"_id")
		})
	}
}
