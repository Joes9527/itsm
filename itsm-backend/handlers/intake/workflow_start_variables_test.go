package intake

import (
	"context"
	"encoding/json"
	"testing"

	"itsm-backend/ent/outboxevent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkflowStartFreezesPreparedVariables(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	definition := f.client.ProcessDefinition.Query().OnlyX(ctx)
	f.client.ProcessDefinition.UpdateOneID(definition.ID).SetBpmnXML([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="p" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="flow" sourceRef="start" targetRef="end"/></bpmn:process></bpmn:definitions>`)).ExecX(ctx)
	command := f.catalogCommand(t)
	command.ServiceRequest = &creation.ServiceRequestInput{Amount: json.Number("9007199254740993.125"), ContactName: "Original requester"}
	result, err := f.app.Create(ctx, f.actor, command)
	require.NoError(t, err)
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventType("workflow.start.requested")).OnlyX(ctx)
	require.Contains(t, string(event.Payload), `"amount":9007199254740993.125`)
	f.client.Ticket.UpdateOneID(result.WorkItemID).SetTitle("Edited after creation").SetPriority("low").ExecX(ctx)
	engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
	require.NoError(t, service.NewWorkflowStartOutboxHandler(f.client, engine).Deliver(ctx, event))
	instance := f.client.ProcessInstance.Query().OnlyX(ctx)
	require.Equal(t, "service_request", instance.BusinessType)
	require.Equal(t, command.Title, instance.Variables["title"])
	require.Equal(t, "medium", instance.Variables["priority"])
	require.Equal(t, "Original requester", instance.Variables["contact_name"])
	raw, err := f.client.ProcessInstance.Query().Select("variables").Strings(ctx)
	require.NoError(t, err)
	require.Contains(t, raw[0], `9007199254740993.125`)
}

func TestWorkflowStartRejectsMutatedDefinitionContent(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	_, err := f.app.Create(ctx, f.actor, f.catalogCommand(t))
	require.NoError(t, err)
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventType("workflow.start.requested")).OnlyX(ctx)
	definition := f.client.ProcessDefinition.Query().OnlyX(ctx)
	f.client.ProcessDefinition.UpdateOneID(definition.ID).SetBpmnXML([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="changed"><bpmn:process id="changed" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="flow" sourceRef="start" targetRef="end"/></bpmn:process></bpmn:definitions>`)).ExecX(ctx)
	engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
	require.ErrorContains(t, service.NewWorkflowStartOutboxHandler(f.client, engine).Deliver(ctx, event), "frozen")
	require.Zero(t, f.client.ProcessInstance.Query().CountX(ctx))
}
