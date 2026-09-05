package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/service/bpmn"
)

func numericGatewayXML(condition string) []byte {
	return []byte(fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="numeric" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:exclusiveGateway id="choose"/><bpmn:userTask id="accepted" name="Accepted"/><bpmn:userTask id="fallback" name="Fallback"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="accepted-end" sourceRef="accepted" targetRef="end"/><bpmn:sequenceFlow id="fallback-end" sourceRef="fallback" targetRef="end"/><bpmn:sequenceFlow id="begin" sourceRef="start" targetRef="choose"/><bpmn:sequenceFlow id="match" sourceRef="choose" targetRef="accepted"><bpmn:conditionExpression><![CDATA[%s]]></bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="other" sourceRef="choose" targetRef="fallback"/></bpmn:process></bpmn:definitions>`, condition))
}

func withFrozenStartVariables(t *testing.T, event *ent.OutboxEvent, extra map[string]any) *ent.OutboxEvent {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.UseNumber()
	var payload map[string]any
	require.NoError(t, decoder.Decode(&payload))
	for k, v := range extra {
		payload["variables"].(map[string]any)[k] = v
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	copied := *event
	copied.Payload = raw
	return &copied
}

func TestWorkflowStartFrozenNumericFirstGateway(t *testing.T) {
	cases := []struct {
		name, expression string
		values           map[string]any
	}{
		{"quantity", "quantity > 1", map[string]any{"quantity": json.Number("2")}},
		{"leading decimal", "quantity > .5", map[string]any{"quantity": json.Number("2")}},
		{"unary leading decimal", "-quantity < -.5", map[string]any{"quantity": json.Number("2")}},
		{"nested form", `variables["form_values"]["resource"]["cores"] >= 2`, map[string]any{"form_values": map[string]any{"resource": map[string]any{"cores": json.Number("2")}}}},
		{"amount precision", "amount > 9007199254740993.12", map[string]any{"amount": json.Number("9007199254740993.125")}},
		{"amount equality", "amount == 9007199254740993.125", map[string]any{"amount": json.Number("9007199254740993.125")}},
		{"amount arithmetic", "amount + 0.005 > 9007199254740993.129", map[string]any{"amount": json.Number("9007199254740993.125")}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f, event := workflowStartFixture(t, numericGatewayXML(tt.expression))
			event = withFrozenStartVariables(t, event, tt.values)
			handler := NewWorkflowStartOutboxHandler(f.client, f.engine)
			require.NoError(t, handler.Deliver(context.Background(), event))
			first := f.client.ProcessInstance.Query().OnlyX(context.Background())
			task := f.client.ProcessTask.Query().OnlyX(context.Background())
			require.Equal(t, "accepted", task.TaskDefinitionKey)
			require.NoError(t, handler.Deliver(context.Background(), event))
			require.Equal(t, first.ID, f.client.ProcessInstance.Query().OnlyX(context.Background()).ID)
			require.Equal(t, 1, f.client.ProcessTask.Query().CountX(context.Background()))
		})
	}
}

func TestWorkflowStartFrozenNumericAssignmentCallback(t *testing.T) {
	xml := strings.Replace(string(startProcessServiceTaskXML("ticket_task")), `</bpmn:extensionElements>`, `<bpmn:metaData name="action">assign</bpmn:metaData></bpmn:extensionElements>`, 1)
	f, event := workflowStartFixture(t, []byte(xml))
	ctx := context.Background()
	assigneeID := f.outsider.ID
	handler := f.engine.CallbackRegistry().GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler)
	handler.SetNotificationService(NewTicketNotificationService(f.client, zap.NewNop().Sugar()))
	event = withFrozenStartVariables(t, event, map[string]any{"assignee_id": json.Number(fmt.Sprint(assigneeID))})
	deliver := NewWorkflowStartOutboxHandler(f.client, f.engine)
	require.NoError(t, deliver.Deliver(ctx, event))
	callback := f.client.ProcessCallbackOutbox.Query().OnlyX(ctx)
	require.Equal(t, bpmnCallbackStatusCompleted, callback.Status, "%s", callback.LastErrorClass)
	item := f.client.Ticket.Query().OnlyX(ctx)
	require.Equal(t, assigneeID, item.AssigneeID)
	require.Equal(t, "assigned", item.Status)
	notifications := f.client.Notification.Query().CountX(ctx)
	require.Positive(t, notifications)
	require.NoError(t, deliver.Deliver(ctx, event))
	require.Equal(t, notifications, f.client.Notification.Query().CountX(ctx))
	require.Equal(t, item.UpdatedAt, f.client.Ticket.GetX(ctx, item.ID).UpdatedAt)
	require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().CountX(ctx))
}

func TestWorkflowStartRejectsInvalidAssignmentNumbers(t *testing.T) {
	for _, value := range []any{json.Number("1.5"), json.Number("1e40"), "malformed"} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			xml := strings.Replace(string(startProcessServiceTaskXML("ticket_task")), `</bpmn:extensionElements>`, `<bpmn:metaData name="action">assign</bpmn:metaData></bpmn:extensionElements>`, 1)
			f, event := workflowStartFixture(t, []byte(xml))
			event = withFrozenStartVariables(t, event, map[string]any{"assignee_id": value})
			require.NoError(t, NewWorkflowStartOutboxHandler(f.client, f.engine).Deliver(context.Background(), event))
			row := f.client.ProcessCallbackOutbox.Query().OnlyX(context.Background())
			require.Equal(t, bpmnCallbackStatusBlocked, row.Status)
			require.Equal(t, "handler_contract", row.LastErrorClass)
			require.Zero(t, f.client.Ticket.Query().OnlyX(context.Background()).AssigneeID)
			require.Zero(t, f.client.Notification.Query().CountX(context.Background()))
		})
	}
}

func TestWorkflowStartNumericEvaluationErrorDoesNotSelectFallback(t *testing.T) {
	f, event := workflowStartFixture(t, numericGatewayXML("amount % 0.1 == 0"))
	event = withFrozenStartVariables(t, event, map[string]any{"amount": json.Number("1.2")})
	err := NewWorkflowStartOutboxHandler(f.client, f.engine).Deliver(context.Background(), event)
	require.ErrorContains(t, err, "unsupported exact decimal operator")
	require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
	require.Zero(t, f.client.ProcessTask.Query().CountX(context.Background()))
}

func TestDefinitionStartFrozenIncidentAssignment(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := startProcessContext(f)
	xml := strings.Replace(string(startProcessServiceTaskXML("incident_task")), `</bpmn:extensionElements>`, `<bpmn:metaData name="action">assign_incident</bpmn:metaData></bpmn:extensionElements>`, 1)
	definition := f.client.ProcessDefinition.UpdateOneID(f.definition.ID).SetBpmnXML([]byte(xml)).SaveX(ctx)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("incident").SetTitle("Incident").SetTicketNumber("INC-work-item").SetStatus("new").SaveX(ctx)
	f.client.Incident.Create().SetWorkItemID(item.ID).SetIncidentNumber(item.TicketNumber).SaveX(ctx)
	f.engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetIncidentService(&IncidentService{client: f.client, logger: zap.NewNop().Sugar()})
	vars := map[string]any{"assignee_id": json.Number(fmt.Sprint(f.outsider.ID))}
	first, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(definition), fmt.Sprintf("incident:%d", item.ID), "incident", item.ID, vars, "incident-assignment")
	require.NoError(t, err)
	require.Equal(t, bpmnCallbackStatusCompleted, f.client.ProcessCallbackOutbox.Query().OnlyX(ctx).Status)
	assigned := f.client.Ticket.GetX(ctx, item.ID)
	require.Equal(t, f.outsider.ID, assigned.AssigneeID)
	require.Equal(t, "assigned", assigned.Status)
	replay, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(definition), fmt.Sprintf("incident:%d", item.ID), "incident", item.ID, vars, "incident-assignment")
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	require.Equal(t, assigned.Version, f.client.Ticket.GetX(ctx, item.ID).Version)
}

func TestWorkflowStartNumericFalseConditionSelectsFallback(t *testing.T) {
	f, event := workflowStartFixture(t, numericGatewayXML("amount > 9007199254740993.13"))
	event = withFrozenStartVariables(t, event, map[string]any{"amount": json.Number("9007199254740993.125")})
	require.NoError(t, NewWorkflowStartOutboxHandler(f.client, f.engine).Deliver(context.Background(), event))
	require.Equal(t, "fallback", f.client.ProcessTask.Query().OnlyX(context.Background()).TaskDefinitionKey)
}
