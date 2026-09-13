//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/service"
)

// Reuses the established private candidate RLS fixture. Intake created the
// professional source through its real application service before this call.
func verifyCandidateIncidentEmailRoles(t *testing.T, ctx context.Context, owner, runtime, system *ent.Client, runtimeDB, systemDB *sql.DB, scopeID string, tenantID, actorID, incidentID int, transport string) {
	t.Helper()
	ctx = service.WithIncidentAlertActor(tenantctx.WithTenantID(ctx, tenantID), actorID, "user", "candidate-mail-roles-"+transport)
	for _, check := range []struct {
		client               *sql.DB
		bypass, writeTickets bool
	}{{runtimeDB, false, true}, {systemDB, true, false}} {
		rows, err := check.client.QueryContext(ctx, `SELECT rolsuper,rolbypassrls,has_table_privilege(current_user,'tickets','UPDATE') FROM pg_roles WHERE rolname=current_user`)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var super, bypass, writeTickets bool
		require.NoError(t, rows.Scan(&super, &bypass, &writeTickets))
		require.NoError(t, rows.Close())
		require.False(t, super)
		require.Equal(t, check.bypass, bypass)
		require.Equal(t, check.writeTickets, writeTickets)
	}
	receiver := newCandidateIncidentMailTransport(t, tenantID, scopeID, transport)
	makePolicy := func(enabled bool) *database.ExecutionPolicy {
		cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenantID, ScopeID: scopeID}}}
		if receiver.target != nil {
			cfg.ConnectorTargets = []config.ConnectorTargetConfig{*receiver.target}
		}
		if enabled {
			cfg.Capabilities = map[string]string{"outbox": "scoped"}
		}
		policy, err := database.NewExecutionPolicy(cfg)
		require.NoError(t, err)
		return policy
	}
	disabled, enabled := makePolicy(false), makePolicy(true)
	mailer := receiver.mailer(t, ctx, disabled)
	producer := service.NewIncidentAlertingService(runtime, zap.NewNop().Sugar(), disabled)
	producer.SetEmailService(mailer)
	actor := owner.User.GetX(ctx, actorID)
	alert, err := producer.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{IncidentID: incidentID, AlertType: "monitoring", AlertName: "candidate mail", Message: "candidate private body", Severity: "high", Channels: []string{"email", "in_app"}, Recipients: []string{actor.Email}}, tenantID)
	require.NoError(t, err)
	require.Zero(t, receiver.calls.Load())
	event := owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("incident_alert_delivery"), outboxevent.TenantIDEQ(tenantID), outboxevent.AggregateIDEQ(strconv.Itoa(alert.ID))).OnlyX(ctx)
	before, auditsBefore := incidentMailPersistenceSnapshot(t, ctx, owner, event.ID)
	// The creation event belongs to its existing specialised consumer. This
	// protocol test leaves it pending and verifies it is not modified.
	otherEvents := func() string {
		rows, err := owner.QueryContext(ctx, `SELECT coalesce(json_agg(o ORDER BY id),'[]'::json)::text FROM outbox_events o WHERE id<>$1`, event.ID)
		require.NoError(t, err)
		defer rows.Close()
		require.True(t, rows.Next())
		var result string
		require.NoError(t, rows.Scan(&result))
		return result
	}
	othersBefore := otherEvents()
	worker := func(policy *database.ExecutionPolicy) *service.OutboxDeliveryWorker {
		boundMailer := receiver.mailer(t, ctx, policy)
		registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewIncidentAlertDeliveryHandler(runtime, policy, boundMailer)}, "incident.created")
		require.NoError(t, err)
		worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(system, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 3 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
		require.NoError(t, err)
		return worker
	}
	require.ErrorIs(t, worker(disabled).DispatchOnce(ctx), executionscope.ErrDenied)
	after, auditsAfter := incidentMailPersistenceSnapshot(t, ctx, owner, event.ID)
	require.JSONEq(t, before, after)
	require.JSONEq(t, auditsBefore, auditsAfter)
	require.Zero(t, receiver.calls.Load())
	activeWorker := worker(enabled)
	require.NoError(t, activeWorker.DispatchOnce(ctx))
	require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, event.ID).Status)
	require.Equal(t, int32(1), receiver.accepted.Load())
	receiver.verify(t, actor.Email)
	receipt := owner.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivered"), auditlog.RequestIDEQ("candidate-mail-roles-"+transport)).OnlyX(ctx)
	require.Equal(t, tenantID, receipt.TenantID)
	require.Equal(t, actorID, receipt.UserID)
	require.NotNil(t, receipt.OperationID)
	require.Equal(t, "incident_alert_deliver:"+event.EventID, *receipt.OperationID)
	require.Equal(t, "incident_alert", receipt.Resource)
	require.Equal(t, "outbox://"+event.EventID, receipt.Path)
	require.Equal(t, "POST", receipt.Method)
	require.Equal(t, 200, receipt.StatusCode)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	require.NotNil(t, receipt.RequestDigest)
	require.Equal(t, incidentMailTestDigest(t, payload), *receipt.RequestDigest)
	require.NotNil(t, receipt.ResultStatus)
	require.Equal(t, "delivered", *receipt.ResultStatus)
	require.Nil(t, receipt.ResultVersion)
	publishedBefore, receiptsBefore := incidentMailPersistenceSnapshot(t, ctx, owner, event.ID)
	callsBeforeRepeat := receiver.calls.Load()
	require.NoError(t, activeWorker.DispatchOnce(ctx))
	require.Equal(t, int32(1), receiver.accepted.Load())
	require.Equal(t, callsBeforeRepeat, receiver.calls.Load(), "repeat scan must not acquire tokens or resend")
	publishedAfter, receiptsAfter := incidentMailPersistenceSnapshot(t, ctx, owner, event.ID)
	require.JSONEq(t, publishedBefore, publishedAfter)
	require.JSONEq(t, receiptsBefore, receiptsAfter)
	require.JSONEq(t, othersBefore, otherEvents())
}
