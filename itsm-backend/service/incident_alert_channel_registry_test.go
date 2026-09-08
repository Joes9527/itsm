package service

import (
	"context"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type recordingIncidentAlertCreator struct {
	requests []*dto.CreateIncidentAlertRequest
}

func (r *recordingIncidentAlertCreator) CreateIncidentAlert(_ context.Context, req *dto.CreateIncidentAlertRequest, _ int) (*dto.IncidentAlertResponse, error) {
	r.requests = append(r.requests, req)
	return &dto.IncidentAlertResponse{}, nil
}

func TestIncidentRuleEngineRejectsUnregisteredNotificationChannelAtParse(t *testing.T) {
	engine := &IncidentRuleEngine{}
	_, err := engine.parseNotificationAction(map[string]interface{}{
		"channels": []interface{}{"sms"}, "recipients": []interface{}{"operator@example.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported alert channel: sms")
}

func TestIncidentEscalationRuleRejectsUnsupportedChannelEvenWhenDisabled(t *testing.T) {
	service := NewIncidentEscalationService(nil)
	_, err := service.CreateEscalationRule(context.Background(), dto.CreateIncidentEscalationRuleRequest{
		NotificationConfig: map[string]interface{}{"webhook": false},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported alert channel: webhook")
}

func TestIncidentEscalationRuleRequiresEmailRecipientsAtSave(t *testing.T) {
	service := NewIncidentEscalationService(nil)
	_, err := service.CreateEscalationRule(context.Background(), dto.CreateIncidentEscalationRuleRequest{
		NotificationConfig: map[string]interface{}{"email": true},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email alert recipient is required")
}

func TestSLAAlertRuleRejectsUnsupportedChannelBeforePersistence(t *testing.T) {
	service := NewSLAAlertService(nil, zap.NewNop().Sugar())
	_, err := service.CreateAlertRule(context.Background(), &dto.CreateSLAAlertRuleRequest{NotificationChannels: []string{"sms"}}, 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported alert channel: sms")
}

func TestNotificationRuleActionUsesAuthoritativeAlertCreator(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "notification-action")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "notification-action")
	require.NoError(t, err)
	incident := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "notification-action")
	creator := NewIncidentAlertingService(client, zap.NewNop().Sugar())
	action := &NotificationAction{Channels: []string{"email"}, Recipients: []string{actor.Email}, Message: "act", Severity: "high", alertCreator: creator, client: client}
	require.NoError(t, action.Execute(ctx, incident, tenant.ID))
	require.Equal(t, incident.ID, client.IncidentAlert.Query().OnlyX(ctx).IncidentID)
	require.Equal(t, "pending", client.OutboxEvent.Query().OnlyX(ctx).Status)
}
func TestNotificationRuleActionRejectsIndependentAlertCreator(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	action := &NotificationAction{client: client, alertCreator: &recordingIncidentAlertCreator{}}
	require.ErrorContains(t, action.Execute(ctx, &ent.Incident{ID: 42}, 7), "not configured")
	require.Zero(t, client.IncidentAlert.Query().CountX(ctx))
}
