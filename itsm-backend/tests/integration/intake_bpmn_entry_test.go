package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"testing"
	"time"
)

func TestIntakeBPMNCreationReplaysAfterFailure(t *testing.T) {
	for _, kind := range []string{"incident", "change"} {
		for _, stage := range []string{"source_acknowledgement", "professional_persistence"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				f := newUnifiedIntakeFixture(t)
				ctx := context.Background()
				source, err := f.app.Create(ctx, f.identity, f.command)
				require.NoError(t, err)
				engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
				if kind == "incident" {
					engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetCreationApplication(f.app, f.client)
				} else {
					engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetCreationApplication(f.app, f.client)
				}
				deployment := f.client.ProcessDeployment.Create().SetTenantID(f.identity.TenantID).SetDeploymentID("creation").SetDeploymentName("Creation").SaveX(ctx)
				xml := []byte(fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="creation" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:serviceTask id="create"><bpmn:extensionElements><bpmn:metaData name="service_task_type">%s_task</bpmn:metaData><bpmn:metaData name="action">create_%s</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="create"/><bpmn:sequenceFlow id="b" sourceRef="create" targetRef="end"/></bpmn:process></bpmn:definitions>`, kind, kind))
				definition := f.client.ProcessDefinition.Create().SetTenantID(f.identity.TenantID).SetDeploymentID(deployment.ID).SetKey("creation").SetName("Creation").SetBpmnXML(xml).SetIsActive(true).SaveX(ctx)
				failed := false
				if stage == "professional_persistence" {
					hook := func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							if !failed && m.Op().Is(ent.OpCreate) {
								failed = true
								return nil, errors.New("injected professional persistence failure")
							}
							return next.Mutate(ctx, m)
						})
					}
					if kind == "incident" {
						f.client.Incident.Use(hook)
					} else {
						f.client.Change.Use(hook)
					}
				}
				f.client.ProcessCallbackOutbox.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if typed, ok := m.(*ent.ProcessCallbackOutboxMutation); ok && !failed && stage == "source_acknowledgement" {
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
				variables := map[string]any{"title": "Created by process", "description": "Process requested work", "priority": "high"}
				if kind == "change" {
					variables["justification"] = "Apply reviewed service configuration"
					variables["impact_scope"] = "low"
					variables["risk_level"] = "medium"
					variables["implementation_plan"] = "Back up configuration, apply and verify"
					variables["rollback_plan"] = "Restore previous configuration and verify"
				}
				instance, err := engine.StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), fmt.Sprintf("ticket:%d", source.WorkItemID), "generic", source.WorkItemID, variables, "source-start")
				require.NoError(t, err)
				require.True(t, failed, "actual child creation must reach the injected failure")
				expected := 2
				if stage == "professional_persistence" {
					expected = 1
					require.Zero(t, f.client.Incident.Query().CountX(ctx))
					require.Zero(t, f.client.Change.Query().CountX(ctx))
					require.Zero(t, f.client.OutboxEvent.Query().CountX(ctx))
					require.Zero(t, f.client.FieldValue.Query().CountX(ctx))
					require.Equal(t, 1, f.client.AuditLog.Query().CountX(ctx))
				}
				require.Equal(t, expected, f.client.Ticket.Query().CountX(ctx))
				require.Equal(t, expected, f.client.IntakeRequest.Query().CountX(ctx))
				require.Equal(t, expected, f.client.IntakeResolutionSnapshot.Query().CountX(ctx))
				require.Equal(t, int64(expected), f.client.WorkItemNumberSequence.Query().OnlyX(ctx).LastValue)
				row := f.client.ProcessCallbackOutbox.Query().OnlyX(ctx)
				require.NotEqual(t, "completed", row.Status)
				executionKey := row.ExecutionKey
				require.Equal(t, instance.ID, row.ProcessInstanceID)
				require.Equal(t, f.identity.TenantID, row.TenantID)
				require.Equal(t, "create", row.ElementID)
				require.Equal(t, "create", f.client.ProcessInstance.GetX(ctx, instance.ID).CurrentActivityID)
				row.Update().SetNextAttemptAt(time.Now().Add(-time.Second)).SaveX(ctx)
				_, err = engine.ProcessPendingCallbacks(ctx, "entry-test", 10)
				require.NoError(t, err)
				require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
				row = f.client.ProcessCallbackOutbox.GetX(ctx, row.ID)
				require.Equal(t, "completed", row.Status)
				require.Equal(t, executionKey, row.ExecutionKey)
				require.Equal(t, instance.ID, row.ProcessInstanceID)
				require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
				require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().CountX(ctx))
				require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))
				require.Equal(t, int64(2), f.client.WorkItemNumberSequence.Query().OnlyX(ctx).LastValue)
				target := f.client.Ticket.Query().Where(ticket.IDNEQ(source.WorkItemID)).OnlyX(ctx)
				require.Equal(t, fmt.Sprintf("TKT-%s-%06d", target.CreatedAt.UTC().Format("200601"), 2), target.TicketNumber)
				if kind == "incident" {
					require.Equal(t, target.ID, f.client.Incident.Query().OnlyX(ctx).WorkItemID)
				} else {
					require.Equal(t, target.ID, f.client.Change.Query().OnlyX(ctx).WorkItemID)
				}
				reloaded := f.client.ProcessInstance.GetX(ctx, instance.ID)
				require.Equal(t, source.WorkItemID, reloaded.BusinessID)
				require.Equal(t, "generic", reloaded.BusinessType)
				require.Equal(t, fmt.Sprint(f.identity.ActorID), reloaded.Initiator)
				require.Contains(t, reloaded.Variables, "created_work_item_id")
				require.Contains(t, reloaded.Variables, "created_"+kind+"_id")
			})
		}
	}
}

func TestIntakeBPMNIncidentSourcePolicy(t *testing.T) {
	for _, requested := range []string{"", "system", "monitoring", "manual", "user"} {
		name := requested
		if name == "" {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			source, err := f.app.Create(ctx, f.identity, f.command)
			require.NoError(t, err)
			engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
			engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetCreationApplication(f.app, f.client)
			deployment := f.client.ProcessDeployment.Create().SetTenantID(f.identity.TenantID).SetDeploymentID("source-policy").SetDeploymentName("Source policy").SaveX(ctx)
			xml := []byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="creation" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:serviceTask id="create"><bpmn:extensionElements><bpmn:metaData name="service_task_type">incident_task</bpmn:metaData><bpmn:metaData name="action">create_incident</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="create"/><bpmn:sequenceFlow id="b" sourceRef="create" targetRef="end"/></bpmn:process></bpmn:definitions>`)
			definition := f.client.ProcessDefinition.Create().SetTenantID(f.identity.TenantID).SetDeploymentID(deployment.ID).SetKey("source-policy").SetName("Source policy").SetBpmnXML(xml).SetIsActive(true).SaveX(ctx)
			ctx = service.WithTrustedBPMNTenantContext(ctx, f.identity.TenantID)
			ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.identity.ActorID)
			variables := map[string]any{"title": "Incident source policy", "description": "Created by process", "priority": "high"}
			if requested != "" {
				variables["source"] = requested
			}
			instance, err := engine.StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), fmt.Sprintf("ticket:%d", source.WorkItemID), "generic", source.WorkItemID, variables, "source-policy-start")
			require.NoError(t, err)
			callback := f.client.ProcessCallbackOutbox.Query().OnlyX(ctx)
			if requested == "" || requested == "system" {
				require.Equal(t, "completed", callback.Status)
				child := f.client.Ticket.Query().Where(ticket.IDNEQ(source.WorkItemID)).OnlyX(ctx)
				require.Equal(t, "system", child.Source)
				require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))
				require.Equal(t, 1, f.client.Incident.Query().CountX(ctx))
			} else {
				require.Equal(t, "blocked", callback.Status)
				require.Equal(t, "create", f.client.ProcessInstance.GetX(ctx, instance.ID).CurrentActivityID)
				require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
				require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
				require.Equal(t, 1, f.client.IntakeResolutionSnapshot.Query().CountX(ctx))
				require.Equal(t, 1, f.client.AuditLog.Query().CountX(ctx))
				require.Zero(t, f.client.Incident.Query().CountX(ctx))
				require.Zero(t, f.client.OutboxEvent.Query().CountX(ctx))
			}
		})
	}
}
