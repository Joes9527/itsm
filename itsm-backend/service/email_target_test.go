package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
)

type emailTargetDescriber interface {
	SetDeliveryTargetDependencies(*connector.Manager, *database.ExecutionPolicy)
	DescribeDeliveryTarget(context.Context, *ent.Tx, int, string) (EmailTarget, error)
}

func TestEmailTargetDescribesBothTrustedGraphSources(t *testing.T) {
	for _, mode := range []string{"candidate", "standard"} {
		t.Run(mode, func(t *testing.T) {
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:email-target-%s-%d?mode=memory&cache=shared&_fk=1", mode, time.Now().UnixNano()))
			defer client.Close()
			ctx := tenantctx.WithTenantID(context.Background(), 1)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			settings := map[string]interface{}{"azure_tenant_id": "test", "mailbox": "original@example.invalid"}
			credentials := map[string]string{"azure_client_id": "app"}
			digest, err := msgraph.New().DescribeDeliveryDestination(connector.Config{Settings: settings, Credentials: credentials})
			require.NoError(t, err)
			cfg := config.ExecutionConfig{Mode: mode, DeploymentID: "email-target-test"}
			if mode == "candidate" {
				scope := "149ff1af-a27c-47c7-827f-103271130bb9"
				cfg.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}
				cfg.ConnectorTargets = []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: "msgraph-email", Provider: "microsoft", Capabilities: []string{"notification"}, DestinationDigest: digest, Settings: settings, Credentials: credentials}}
			} else {
				rawSettings, _ := json.Marshal(settings)
				rawCredentials, _ := json.Marshal(credentials)
				_, err = tx.ConnectorConfig.Create().SetTenantID(1).SetName("msgraph-email").SetProvider("microsoft").SetEnabled(true).SetSettings(string(rawSettings)).SetCredentials(string(rawCredentials)).Save(ctx)
				require.NoError(t, err)
			}
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return msgraph.New() })
			manager := connector.NewManager(registry, nil, policy)
			defer manager.CloseAll()
			svc := NewEmailService(EmailConfig{}, zap.NewNop().Sugar())
			svc.SetGraphProvider(func(int) (GraphMailSender, string, bool) {
				t.Error("description must not resolve live sender")
				return nil, "", false
			})
			describe, ok := any(svc).(emailTargetDescriber)
			require.True(t, ok, "EmailService requires the typed target description contract")
			describe.SetDeliveryTargetDependencies(manager, policy)
			target, err := describe.DescribeDeliveryTarget(ctx, tx, 1, "notification")
			require.NoError(t, err)
			require.Equal(t, EmailTarget{ProtocolVersion: 2, Transport: "graph", ConnectorName: "msgraph-email", ConnectorProvider: "microsoft", DestinationDigest: digest}, target)
			require.Empty(t, manager.ListByTenant(1))
			_, err = describe.DescribeDeliveryTarget(ctx, nil, 1, "notification")
			require.ErrorIs(t, err, executionscope.ErrDenied)
			_, err = describe.DescribeDeliveryTarget(tenantctx.WithTenantID(ctx, 2), tx, 1, "notification")
			require.ErrorIs(t, err, executionscope.ErrDenied)
			manager.CloseAll()
			svc.config = EmailConfig{DeliveryTransport: "graph", Host: "localhost", Port: 2525, Username: "sender", From: "sender@example.invalid"}
			target, err = describe.DescribeDeliveryTarget(ctx, tx, 1, "notification")
			require.ErrorIs(t, err, executionscope.ErrDenied)
			require.Empty(t, target, "Graph failure must not select configured SMTP")
		})
	}
}

func TestEmailTargetSMTPFreezesRoutingWithoutSecret(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:smtp-target-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "smtp-target"})
	require.NoError(t, err)
	cfg := EmailConfig{DeliveryTransport: "smtp", Host: "localhost", Port: 2525, Username: "sender", From: "sender@example.invalid", Password: "private-password"}
	describe := func(cfg EmailConfig) (EmailTarget, error) {
		svc := NewEmailService(cfg, zap.NewNop().Sugar())
		svc.SetDeliveryTargetDependencies(nil, policy)
		svc.SetGraphProvider(func(int) (GraphMailSender, string, bool) {
			t.Error("explicit SMTP description must not consult Graph")
			return nil, "", false
		})
		return svc.DescribeDeliveryTarget(ctx, tx, 1, "notification")
	}
	original, err := describe(cfg)
	require.NoError(t, err)
	require.Equal(t, 2, original.ProtocolVersion)
	require.Equal(t, "smtp", original.Transport)
	require.Empty(t, original.ConnectorName)
	require.Empty(t, original.ConnectorProvider)
	require.Regexp(t, `^[0-9a-f]{64}$`, original.DestinationDigest)
	for _, field := range []string{"host", "port", "username", "from", "password"} {
		t.Run(field, func(t *testing.T) {
			changed := cfg
			switch field {
			case "host":
				changed.Host = "other.example.invalid"
			case "port":
				changed.Port++
			case "username":
				changed.Username = "other"
			case "from":
				changed.From = "other@example.invalid"
			case "password":
				changed.Password = ""
			}
			target, err := describe(changed)
			require.NoError(t, err)
			if field == "password" {
				require.Equal(t, original, target)
			} else {
				require.NotEqual(t, original.DestinationDigest, target.DestinationDigest)
			}
		})
	}
}

func TestEmailTargetRejectsInvalidSMTPConfiguration(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:invalid-smtp-target-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "invalid-smtp"})
	require.NoError(t, err)
	for _, scenario := range []string{"transport", "host", "port", "username", "from"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := EmailConfig{DeliveryTransport: "smtp", Host: "localhost", Port: 2525, Username: "sender", From: "sender@example.invalid"}
			switch scenario {
			case "transport":
				cfg.DeliveryTransport = "auto"
			case "host":
				cfg.Host = "http://secret.invalid"
			case "port":
				cfg.Port = 65536
			case "username":
				cfg.Username = ""
			case "from":
				cfg.From = "Display Name <sender@example.invalid>"
			}
			svc := NewEmailService(cfg, zap.NewNop().Sugar())
			svc.SetDeliveryTargetDependencies(nil, policy)
			target, err := svc.DescribeDeliveryTarget(ctx, tx, 1, "notification")
			require.ErrorIs(t, err, executionscope.ErrDenied)
			require.Empty(t, target)
		})
	}
}
