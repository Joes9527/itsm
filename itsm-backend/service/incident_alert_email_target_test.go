package service

import (
	"context"
	"net/smtp"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestIncidentAlertMissingEmailTargetRollsBackOriginalTransaction(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "alert-missing-target")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "alert-missing-target")
	require.NoError(t, err)
	item := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "INC-MISSING-MAIL-TARGET")
	ctx = WithIncidentAlertActor(tenantctx.WithTenantID(ctx, tenant.ID), actor.ID, "user", "missing-target")
	svc := NewIncidentAlertingService(client, zap.NewNop().Sugar(), executionfixture.Standard())
	beforeAlert := client.IncidentAlert.Query().CountX(ctx)
	beforeNotification := client.Notification.Query().CountX(ctx)
	beforeOutbox := client.OutboxEvent.Query().CountX(ctx)
	_, err = svc.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{IncidentID: item.ID, AlertType: "monitoring", AlertName: "missing target", Message: "must roll back", Severity: "high", Channels: []string{"email", "in_app"}, Recipients: []string{actor.Email}}, tenant.ID)
	require.Error(t, err)
	require.Equal(t, beforeAlert, client.IncidentAlert.Query().CountX(ctx))
	require.Equal(t, beforeNotification, client.Notification.Query().CountX(ctx))
	require.Equal(t, beforeOutbox, client.OutboxEvent.Query().CountX(ctx))
}

// configureIncidentAlertProducerTarget supplies only a local descriptor. Any
// accidental send fails the test before network access.
func configureIncidentAlertProducerTarget(t *testing.T, ctx context.Context, owner *IncidentAlertingService, tenantID int) context.Context {
	t.Helper()
	mail := NewEmailService(EmailConfig{DeliveryTransport: "smtp", Host: "127.0.0.1", Port: 2525, Username: "fixture", From: "fixture@example.invalid"}, zap.NewNop().Sugar())
	mail.SetDeliveryTargetDependencies(nil, owner.execution)
	mail.smtpSend = func(context.Context, string, smtp.Auth, string, []string, []byte) error {
		t.Error("producer must not send email")
		return nil
	}
	owner.SetEmailService(mail)
	return tenantctx.WithTenantID(ctx, tenantID)
}

func TestIncidentAlertCreationRequiresExplicitActiveTenantActor(t *testing.T) {
	for _, scenario := range []string{"missing", "negative", "zero system", "blank source", "foreign", "inactive", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			client, _, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "actor-check")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "actor-check")
			require.NoError(t, err)
			item := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "INC-ACTOR-CHECK")
			owner := NewIncidentAlertingService(client, zap.NewNop().Sugar(), executionfixture.Standard())
			ctx = configureIncidentAlertProducerTarget(t, ctx, owner, tenant.ID)
			switch scenario {
			case "negative":
				ctx = WithIncidentAlertActor(ctx, -1, "user", "actor-check")
			case "zero system":
				ctx = WithIncidentAlertActor(ctx, 0, "system", "actor-check")
			case "blank source":
				ctx = WithIncidentAlertActor(ctx, actor.ID, " ", "actor-check")
			case "foreign":
				other, err := createIncidentTestTenant(ctx, client, "actor-other")
				require.NoError(t, err)
				actor.Update().SetTenantID(other.ID).ExecX(ctx)
				ctx = WithIncidentAlertActor(ctx, actor.ID, "user", "actor-check")
			case "inactive":
				actor.Update().SetActive(false).ExecX(ctx)
				ctx = WithIncidentAlertActor(ctx, actor.ID, "user", "actor-check")
			case "valid":
				ctx = WithIncidentAlertActor(ctx, actor.ID, "user", "actor-check")
			}
			_, err = owner.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{IncidentID: item.ID, AlertType: "monitoring", AlertName: "actor check", Message: "actor check", Severity: "high", Channels: []string{"email", "in_app"}, Recipients: []string{actor.Email}}, tenant.ID)
			if scenario == "valid" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Zero(t, client.IncidentAlert.Query().CountX(ctx))
			require.Zero(t, client.Notification.Query().CountX(ctx))
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
		})
	}
}
