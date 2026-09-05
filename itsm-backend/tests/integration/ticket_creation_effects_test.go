package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/connector"
	"itsm-backend/ent"
	repositoryticket "itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

func configuredCreationTicketOwner(client *ent.Client, logger *zap.SugaredLogger) *service.TicketService {
	return configuredCreationTicketOwnerWithConnector(client, logger, nil)
}
func configuredCreationTicketOwnerWithConnector(client *ent.Client, logger *zap.SugaredLogger, manager *connector.Manager) *service.TicketService {
	notifications := service.NewTicketNotificationService(client, logger)
	notifications.SetEmailService(service.NewEmailService(service.EmailConfig{}, logger))
	assignment := service.NewTicketAssignmentService(client, logger)
	rules := service.NewTicketAutomationRuleService(client, logger)
	rules.SetAssignmentService(assignment)
	rules.SetNotificationService(notifications)
	return service.NewTicketService(&service.TicketServiceConfig{Client: client, Logger: logger, ConnectorManager: manager, Repository: repositoryticket.NewEntRepository(client, logger, workitemnumber.NewPostgreSQLAllocator()), NotificationService: notifications, AutomationRuleService: rules})
}
func TestIntakeGenericCreationUsesConfiguredEffectsAtomically(t *testing.T) {
	f := newUnifiedIntakeFixture(t, configuredCreationTicketOwner)
	ctx := context.Background()
	assignee := f.client.User.Create().SetTenantID(f.identity.TenantID).SetUsername("handler").SetName("Handler").SetEmail("handler@example.test").SetPasswordHash("unused").SetRole("agent").SaveX(ctx)
	rule := f.client.TicketAutomationRule.Create().SetTenantID(f.identity.TenantID).SetCreatedBy(f.identity.ActorID).SetName("route").SetConditions([]map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "set_priority", "priority": "critical"}, {"type": "assign", "user_id": assignee.ID}, {"type": "set_status", "status": "pending"}, {"type": "send_notification", "content": "Frozen rule content"}}).SaveX(ctx)

	sla := f.client.SLADefinition.Create().SetTenantID(f.identity.TenantID).SetName("Critical SLA").SetResponseTime(15).SetResolutionTime(60).SaveX(ctx)
	deployment := f.client.ProcessDeployment.Create().SetTenantID(f.identity.TenantID).SetDeploymentID("rule-workflow").SetDeploymentName("Rule workflow").SaveX(ctx)
	definition := f.client.ProcessDefinition.Create().SetTenantID(f.identity.TenantID).SetDeploymentID(deployment.ID).SetKey("ruleflow").SetName("Rule flow").SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte("<definitions/>")).SaveX(ctx)
	f.client.ProcessBinding.Create().SetTenantID(f.identity.TenantID).SetBusinessType("ticket").SetProcessDefinitionKey("ruleflow").SetPriority(100).SetConditions(map[string]interface{}{"priority": "critical", "status": "pending", "assignee_id": assignee.ID}).SetSLAPolicyID(strconv.Itoa(sla.ID)).SaveX(ctx)
	first, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	item := f.client.Ticket.GetX(ctx, first.WorkItemID)
	require.Equal(t, "critical", item.Priority)
	require.Equal(t, "pending", item.Status)
	require.Equal(t, assignee.ID, item.AssigneeID)

	require.Equal(t, sla.ID, item.SLADefinitionID)
	require.False(t, item.SLAResponseDeadline.IsZero())
	require.False(t, item.SLAResolutionDeadline.IsZero())
	snapshot := f.client.IntakeResolutionSnapshot.Query().OnlyX(ctx)
	require.Equal(t, &sla.ID, snapshot.SLADefinitionID)
	require.Equal(t, &definition.ID, snapshot.WorkflowDefinitionID)
	require.Equal(t, "ruleflow", snapshot.WorkflowDefinitionKey)
	event := f.client.OutboxEvent.Query().OnlyX(ctx)
	var payload struct {
		Variables map[string]interface{} `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	require.Equal(t, "critical", payload.Variables["priority"])
	require.Equal(t, "pending", payload.Variables["status"])
	require.EqualValues(t, assignee.ID, payload.Variables["assignee_id"])
	require.Equal(t, 1, f.client.TicketAutomationRule.GetX(ctx, rule.ID).ExecutionCount)
	require.Equal(t, 8, f.client.TicketNotification.Query().CountX(ctx), "two events to requester+assignee, each in-app+email")
	replay, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, 1, f.client.TicketAutomationRule.GetX(ctx, rule.ID).ExecutionCount)
	require.Equal(t, 8, f.client.TicketNotification.Query().CountX(ctx))
}
func TestIntakeGenericCreationRejectsMalformedRulesAndRollsBackEffects(t *testing.T) {
	for _, fault := range []string{"unknown action", "notification write"} {
		t.Run(fault, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t, configuredCreationTicketOwner)
			ctx := context.Background()
			actions := []map[string]interface{}{{"type": "set_priority", "priority": "high"}}
			if fault == "unknown action" {
				actions = append(actions, map[string]interface{}{"type": "unknown"})
			} else {
				f.client.TicketNotification.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
						return nil, errors.New("injected creation notification failure")
					})
				})
			}
			rule := f.client.TicketAutomationRule.Create().SetTenantID(f.identity.TenantID).SetCreatedBy(f.identity.ActorID).SetName("rule").SetConditions([]map[string]interface{}{}).SetActions(actions).SaveX(ctx)
			_, err := f.app.Create(ctx, f.identity, f.command)
			require.Error(t, err)
			assertNoEntryGraph(t, f.client)
			require.Zero(t, f.client.TicketAutomationRule.GetX(ctx, rule.ID).ExecutionCount)
			require.Zero(t, f.client.TicketNotification.Query().CountX(ctx))
		})
	}
}

func TestIntakeGenericAssignmentRuleUsesCurrentTenantAndOneExecution(t *testing.T) {
	f := newUnifiedIntakeFixture(t, configuredCreationTicketOwner)
	ctx := context.Background()
	target := f.client.User.Create().SetTenantID(f.identity.TenantID).SetUsername("assigned").SetName("Assigned").SetEmail("assigned@example.test").SetPasswordHash("unused").SetRole("agent").SaveX(ctx)
	rule := f.client.TicketAssignmentRule.Create().SetTenantID(f.identity.TenantID).SetName("on-create").SetConditions([]map[string]interface{}{{"field": "priority", "operator": "equals", "value": "medium"}}).SetActions(map[string]interface{}{"type": "user", "value": target.ID}).SaveX(ctx)
	first, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	require.Equal(t, target.ID, f.client.Ticket.GetX(ctx, first.WorkItemID).AssigneeID)
	_, err = f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	require.Equal(t, 1, f.client.TicketAssignmentRule.GetX(ctx, rule.ID).ExecutionCount)
	f.client.TicketAssignmentRule.UpdateOneID(rule.ID).SetActions(map[string]interface{}{"type": "round_robin", "value": "malformed"}).ExecX(ctx)
	f.command.IdempotencyKey = "second"
	_, err = f.app.Create(ctx, f.identity, f.command)
	require.Error(t, err)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
}

func TestIntakeGenericConfiguredRulesRequireTheirOwner(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.TicketAutomationRule.Create().SetTenantID(f.identity.TenantID).SetCreatedBy(f.identity.ActorID).SetName("configured").SetConditions([]map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "set_priority", "priority": "high"}}).SaveX(ctx)
	_, err := f.app.Create(ctx, f.identity, f.command)
	require.Error(t, err)
	assertNoEntryGraph(t, f.client)
}
