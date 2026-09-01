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
	creator := &recordingIncidentAlertCreator{}
	action := &NotificationAction{Channels: []string{"email"}, Recipients: []string{"operator@example.com"}, Message: "act", Severity: "high", alertCreator: creator}
	require.NoError(t, action.Execute(context.Background(), &ent.Incident{ID: 42}, 7))
	require.Len(t, creator.requests, 1)
	assert.Equal(t, 42, creator.requests[0].IncidentID)
}
