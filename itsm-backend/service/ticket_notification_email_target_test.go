package service

import (
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
)

func TestNotificationEmailTargetOriginalTransactionAndWorker(t *testing.T) {
	for _, scenario := range []string{"stable", "missing owner", "rebound", "legacy empty"} {
		t.Run(scenario, func(t *testing.T) {
			client, svc, ctx := setupTicketNotificationTest(t)
			defer client.Close()
			tenant, recipient, item := createNotifTestData(t, client, ctx)
			ctx = tenantctx.WithTenantID(ctx, tenant.ID)
			svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zap.NewNop().Sugar()))
			client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(true).SetEmailEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
			cfg := startOutcomeSMTPServer(t, "250 accepted", false)
			cfg.DeliveryTransport = "smtp"
			mailer := NewEmailService(cfg, zap.NewNop().Sugar())
			mailer.SetDeliveryTargetDependencies(nil, svc.execution)
			if scenario != "missing owner" {
				svc.SetEmailService(mailer)
			}
			svc.SetDeliveryQueueClient(client)
			if scenario == "legacy empty" {
				client.TicketNotification.Create().SetTenantID(tenant.ID).SetTicketID(item.ID).SetUserID(recipient.ID).SetType("ticket_updated").SetChannel("email").SetContent("legacy").SetDeliveryKey("legacy-empty").SetStatus(ticketNotificationStatusPending).SetNextAttemptAt(svc.clock()).SaveX(ctx)
			} else {
				result, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "bound original transaction", DeliveryKey: "bound-original"}, tenant.ID)
				if scenario == "missing owner" {
					require.Error(t, err)
					require.Zero(t, client.TicketNotification.Query().CountX(ctx), "including in-app writes must roll back")
					require.Zero(t, client.Notification.Query().CountX(ctx))
					return
				}
				require.NoError(t, err)
				require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
				rows := client.TicketNotification.Query().AllX(ctx)
				for _, row := range rows {
					if row.Channel == "email" {
						require.NotNil(t, row.TargetProtocolVersion)
						require.Equal(t, 2, *row.TargetProtocolVersion)
						require.NotNil(t, row.TargetTransport)
						require.Equal(t, "smtp", *row.TargetTransport)
						require.NotNil(t, row.TargetDestinationDigest)
					}
				}
			}
			if scenario == "rebound" {
				mailer.config.From = "changed@example.invalid"
			}
			n, err := svc.ProcessPendingDeliveries(ctx, "bound-original", 10)
			if scenario == "stable" {
				require.NoError(t, err)
				require.Equal(t, 1, n)
			} else {
				require.ErrorIs(t, err, executionscope.ErrDenied)
				require.Zero(t, n)
			}
			for _, row := range client.TicketNotification.Query().AllX(ctx) {
				if row.Channel == "email" {
					if scenario == "stable" {
						require.Equal(t, ticketNotificationStatusSent, row.Status)
						require.False(t, row.SentAt.IsZero())
					} else {
						require.Equal(t, ticketNotificationStatusFailed, row.Status)
						require.Equal(t, "delivery_target_invalid", row.LastErrorClass)
						require.True(t, row.SentAt.IsZero())
					}
				}
			}
		})
	}
}

// Uses persisted configuration for production and the real registered Graph
// connector against a private receiver for delivery, not the legacy selector.
func verifyNotificationGraphTargetQueue(t *testing.T, change string) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	var calls atomic.Int32
	var callbackMu sync.Mutex
	var onSend func()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"private-token","expires_in":3600}`)
			return
		}
		callbackMu.Lock()
		callback := onSend
		callbackMu.Unlock()
		if callback != nil {
			callback()
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return msgraph.New() })
	manager := connector.NewManager(registry, nil, svc.execution)
	defer manager.CloseAll()
	cfg := connector.Config{TenantID: tenant.ID, Name: "msgraph-email", Provider: "microsoft", Enabled: true, Settings: map[string]interface{}{"azure_tenant_id": "private", "mailbox": "original@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "private-app", "azure_client_secret": "private-secret"}}
	settings, err := json.Marshal(cfg.Settings)
	require.NoError(t, err)
	credentials, err := json.Marshal(cfg.Credentials)
	require.NoError(t, err)
	client.ConnectorConfig.Create().SetTenantID(tenant.ID).SetName(cfg.Name).SetProvider(cfg.Provider).SetEnabled(true).SetSettings(string(settings)).SetCredentials(string(credentials)).SaveX(ctx)
	require.NoError(t, manager.Provision(ctx, cfg))
	mailer := NewEmailService(EmailConfig{DeliveryTransport: "graph"}, zap.NewNop().Sugar())
	mailer.SetDeliveryTargetDependencies(manager, svc.execution)
	svc.SetEmailService(mailer)
	svc.SetDeliveryQueueClient(client)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zap.NewNop().Sugar()))
	client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(false).SetEmailEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	result, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "frozen graph queue", DeliveryKey: "graph-queue"}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
	before := client.TicketNotification.Query().OnlyX(ctx)
	require.NotNil(t, before.TargetTransport)
	require.Equal(t, "graph", *before.TargetTransport)
	require.NotNil(t, before.TargetDestinationDigest)
	require.Zero(t, calls.Load(), "production must not request tokens or send mail")
	switch change {
	case "mailbox":
		cfg.Settings["mailbox"] = "changed@example.invalid"
	case "graph endpoint":
		cfg.Settings["graph_base_url"] = server.URL + "/changed"
	case "aad endpoint":
		cfg.Settings["aad_base_url"] = server.URL + "/changed"
	case "client identity":
		cfg.Credentials["azure_client_id"] = "changed-app"
	}
	if change == "in flight" {
		callbackMu.Lock()
		onSend = func() {
			if err := manager.Provision(ctx, cfg); err != nil {
				t.Error(err)
			}
		}
		callbackMu.Unlock()
	}
	if change != "stable" && change != "in flight" {
		require.NoError(t, manager.Provision(ctx, cfg))
	}
	n, err := svc.ProcessPendingDeliveries(ctx, "graph-queue", 10)
	after := client.TicketNotification.Query().OnlyX(ctx)
	require.Equal(t, before.TargetDestinationDigest, after.TargetDestinationDigest)
	if change == "stable" {
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, int32(2), calls.Load())
		require.Equal(t, ticketNotificationStatusSent, after.Status)
	} else if change == "in flight" {
		require.Error(t, err)
		require.Zero(t, n)
		require.Equal(t, int32(2), calls.Load())
		require.Equal(t, ticketNotificationStatusFailed, after.Status)
		require.Equal(t, "delivery_unknown", after.LastErrorClass)
		n, err = svc.ProcessPendingDeliveries(ctx, "graph-queue-again", 10)
		require.NoError(t, err)
		require.Zero(t, n)
		require.Equal(t, int32(2), calls.Load())
	} else {
		require.ErrorIs(t, err, executionscope.ErrDenied)
		require.Zero(t, n)
		require.Zero(t, calls.Load())
		require.Equal(t, ticketNotificationStatusFailed, after.Status)
		require.Equal(t, "delivery_target_invalid", after.LastErrorClass)
		require.True(t, after.SentAt.IsZero())
	}
}

// Queue contract tests use an explicit SMTP configuration and a local transport
// probe. Real SMTP and Graph acceptance are covered by the loopback tests above.
func configureNotificationSMTPProbe(svc *TicketNotificationService) (*EmailService, *[]string) {
	calls := []string{}
	mailer := NewEmailService(EmailConfig{DeliveryTransport: "smtp", Host: "smtp.example.invalid", Port: 587, Username: "private-test", From: "sender@example.invalid"}, zap.NewNop().Sugar())
	mailer.SetDeliveryTargetDependencies(nil, svc.execution)
	mailer.smtpSend = func(_ context.Context, _ string, _ smtp.Auth, _ string, to []string, _ []byte) error {
		calls = append(calls, to...)
		return nil
	}
	svc.SetEmailService(mailer)
	return mailer, &calls
}

func configureDurableGraphQueue(t *testing.T, f *durableNotificationFixture, probe *durableNotificationGraphSender) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"private-token","expires_in":3600}`)
			return
		}
		if r.URL.Path != "/users/sender@example.test/sendMail" {
			t.Errorf("unexpected bound Graph path: %s", r.URL.Path)
			http.Error(w, "target", 400)
			return
		}
		var payload struct {
			Message struct {
				ToRecipients []struct{ EmailAddress struct{ Address string } }
				Subject      string
				Body         struct{ Content string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Message.ToRecipients) != 1 {
			t.Error("invalid mail payload")
			http.Error(w, "payload", 400)
			return
		}
		if err := probe.SendMail(r.Context(), "sender@example.test", payload.Message.ToRecipients[0].EmailAddress.Address, payload.Message.Subject, payload.Message.Body.Content, ""); err != nil {
			http.Error(w, "private rejection", 503)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	cfg := connector.Config{TenantID: f.tenant.ID, Name: "msgraph-email", Provider: "microsoft", Enabled: true, Settings: map[string]interface{}{"azure_tenant_id": "private", "mailbox": "sender@example.test", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "private-app", "azure_client_secret": "private-secret"}}
	settings, err := json.Marshal(cfg.Settings)
	require.NoError(t, err)
	credentials, err := json.Marshal(cfg.Credentials)
	require.NoError(t, err)
	f.client.ConnectorConfig.Create().SetTenantID(f.tenant.ID).SetName(cfg.Name).SetProvider(cfg.Provider).SetEnabled(true).SetSettings(string(settings)).SetCredentials(string(credentials)).SaveX(f.ctx)
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return msgraph.New() })
	manager := connector.NewManager(registry, nil, f.notifications.execution)
	t.Cleanup(func() { manager.CloseAll() })
	require.NoError(t, manager.Provision(f.ctx, cfg))
	mailer := NewEmailService(EmailConfig{DeliveryTransport: "graph"}, zap.NewNop().Sugar())
	mailer.SetDeliveryTargetDependencies(manager, f.notifications.execution)
	mailer.SetGraphProvider(func(int) (GraphMailSender, string, bool) {
		t.Error("durable target consulted legacy selector")
		return nil, "", false
	})
	f.notifications.SetEmailService(mailer)
	f.workflow.notifications.SetEmailService(mailer)
}
