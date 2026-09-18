package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
	"itsm-backend/ent/enttest"
)

type boundEmailSender interface {
	SendToTarget(context.Context, int, string, EmailTarget, *EmailMessage) error
}

func TestEmailTargetDeliveryRejectsGraphRebinding(t *testing.T) {
	var requests atomic.Int32
	var mu sync.Mutex
	var onSend func()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"private-test-token","expires_in":3600}`)
			return
		}
		mu.Lock()
		callback := onSend
		mu.Unlock()
		if callback != nil {
			callback()
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:bound-mail-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	ctx, cancel := context.WithTimeout(tenantctx.WithTenantID(context.Background(), 1), 10*time.Second)
	defer cancel()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "bound-mail", Capabilities: map[string]string{"notification": "enabled"}})
	require.NoError(t, err)
	cfg := connector.Config{TenantID: 1, Name: "msgraph-email", Provider: "microsoft", Enabled: true, Settings: map[string]interface{}{"azure_tenant_id": "test", "mailbox": "original@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "app", "azure_client_secret": "private-secret"}}
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return msgraph.New() })
	manager := connector.NewManager(registry, nil, policy)
	defer manager.CloseAll()
	require.NoError(t, manager.Provision(ctx, cfg))
	rawSettings, _ := json.Marshal(cfg.Settings)
	rawCredentials, _ := json.Marshal(cfg.Credentials)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ConnectorConfig.Create().SetTenantID(1).SetName(cfg.Name).SetProvider(cfg.Provider).SetEnabled(true).SetSettings(string(rawSettings)).SetCredentials(string(rawCredentials)).Save(ctx)
	require.NoError(t, err)
	svc := NewEmailService(EmailConfig{DeliveryTransport: "graph"}, zap.NewNop().Sugar())
	svc.SetDeliveryTargetDependencies(manager, policy)
	svc.SetGraphProvider(func(int) (GraphMailSender, string, bool) {
		t.Error("bound delivery must not consult live route selector")
		return nil, "", false
	})
	target, err := svc.DescribeDeliveryTarget(ctx, tx, 1, "notification")
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	sender, ok := any(svc).(boundEmailSender)
	require.True(t, ok, "durable delivery must accept the recorded EmailTarget")
	message := &EmailMessage{To: []string{"recipient@example.invalid"}, Subject: "bound", BodyText: "private body", DeliveryID: "bound-test"}
	require.NoError(t, sender.SendToTarget(ctx, 1, "notification", target, message))
	before := requests.Load()

	for _, field := range []string{"cc", "attachment", "html only"} {
		unsupported := *message
		switch field {
		case "cc":
			unsupported.CC = []string{"copy@example.invalid"}
		case "attachment":
			unsupported.Attachments = []EmailAttachment{{Filename: "note.txt", Data: []byte("private")}}
		case "html only":
			unsupported.BodyText = ""
			unsupported.Body = "<p>body</p>"
		}
		err := sender.SendToTarget(ctx, 1, "notification", target, &unsupported)
		require.Error(t, err)
		require.Equal(t, emailNotAccepted, emailTransportOutcomeOf(err))
		require.Equal(t, before, requests.Load(), "unsupported Graph fields must be rejected before IO")
	}
	changed := cfg
	changed.Settings = map[string]interface{}{"azure_tenant_id": "test", "mailbox": "changed@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL}
	require.NoError(t, manager.Provision(ctx, changed))
	require.ErrorIs(t, sender.SendToTarget(ctx, 1, "notification", target, message), executionscope.ErrDenied)
	require.Equal(t, before, requests.Load(), "rebound target must not receive token or mail request")
	require.NoError(t, manager.Provision(ctx, cfg))
	mu.Lock()
	onSend = func() {
		if err := manager.Provision(ctx, cfg); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	err = sender.SendToTarget(ctx, 1, "notification", target, message)
	require.Error(t, err)
	require.Equal(t, emailAcceptanceUnknown, emailTransportOutcomeOf(err), "in-flight generation change must not be reported sent or retried")
}

type partialTargetGraphSender struct{ calls int }

func (s *partialTargetGraphSender) SendMail(context.Context, string, string, string, string, string) error {
	s.calls++
	if s.calls == 1 {
		return nil
	}
	return newEmailTransportError("graph", "rejected", emailNotAccepted, fmt.Errorf("second recipient rejected"))
}

func TestEmailTargetGraphPartialAcceptanceIsUnknown(t *testing.T) {
	svc := NewEmailService(EmailConfig{}, zap.NewNop().Sugar())
	sender := &partialTargetGraphSender{}
	err := svc.sendViaGraph(context.Background(), sender, "sender@example.invalid", &EmailMessage{To: []string{"first@example.invalid", "second@example.invalid"}, Subject: "partial", BodyText: "body"})
	require.Error(t, err)
	require.Equal(t, emailAcceptanceUnknown, emailTransportOutcomeOf(err))
	require.Equal(t, 2, sender.calls)
}

func TestEmailTargetSMTPDeliveryUsesRecordedRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(tenantctx.WithTenantID(context.Background(), 1), 5*time.Second)
	defer cancel()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "bound-smtp", Capabilities: map[string]string{"notification": "enabled"}})
	require.NoError(t, err)
	cfg := startOutcomeSMTPServer(t, "250 accepted", false)
	cfg.DeliveryTransport = "graph" // Current producer default cannot redirect a recorded SMTP intent.
	svc := NewEmailService(cfg, zap.NewNop().Sugar())
	svc.SetDeliveryTargetDependencies(nil, policy)
	svc.SetGraphProvider(func(int) (GraphMailSender, string, bool) {
		t.Error("recorded SMTP delivery must not consult Graph")
		return nil, "", false
	})
	target, err := svc.describeSMTPTarget()
	require.NoError(t, err)
	message := &EmailMessage{To: []string{"recipient@example.invalid"}, Subject: "SMTP bound", BodyText: "body"}
	require.NoError(t, svc.SendToTarget(ctx, 1, "notification", target, message))
	calls := 0
	svc.smtpSend = func(context.Context, string, smtp.Auth, string, []string, []byte) error {
		calls++
		return newEmailTransportError("smtp", "connect", emailNotAccepted, fmt.Errorf("private test rejection"))
	}
	for _, field := range []string{"host", "port", "username", "from"} {
		svc.config = cfg
		switch field {
		case "host":
			svc.config.Host = "changed.example.invalid"
		case "port":
			svc.config.Port++
		case "username":
			svc.config.Username = "changed"
		case "from":
			svc.config.From = "changed@example.invalid"
		}
		require.ErrorIs(t, svc.SendToTarget(ctx, 1, "notification", target, message), executionscope.ErrDenied, field)
		require.Zero(t, calls)
	}
	svc.config = cfg
	svc.config.Password = "rotated-private-test-password"
	err = svc.SendToTarget(ctx, 1, "notification", target, message)
	require.Error(t, err)
	require.Equal(t, emailNotAccepted, emailTransportOutcomeOf(err))
	require.Equal(t, 1, calls, "credential rotation preserves identity; durable owner controls retries")

	require.ErrorIs(t, svc.SendToTarget(context.Background(), 1, "notification", target, message), executionscope.ErrDenied)
	require.ErrorIs(t, svc.SendToTarget(ctx, 2, "notification", target, message), executionscope.ErrDenied)
	require.Equal(t, 1, calls, "invalid caller must not reach transport")
}

func TestEmailTargetValidationRejectsIncompleteIdentity(t *testing.T) {
	valid := EmailTarget{ProtocolVersion: 2, Transport: "graph", ConnectorName: "msgraph-email", ConnectorProvider: "microsoft", DestinationDigest: strings.Repeat("a", 64)}
	require.NoError(t, valid.Validate())
	smtpTarget := valid
	smtpTarget.Transport, smtpTarget.ConnectorName, smtpTarget.ConnectorProvider = "smtp", "", ""
	require.NoError(t, smtpTarget.Validate())
	for _, field := range []string{"version", "transport", "name", "provider", "digest missing", "digest uppercase", "digest nonhex", "smtp connector"} {
		invalid := valid
		switch field {
		case "version":
			invalid.ProtocolVersion = 1
		case "transport":
			invalid.Transport = "unknown"
		case "name":
			invalid.ConnectorName = ""
		case "provider":
			invalid.ConnectorProvider = "other"
		case "digest missing":
			invalid.DestinationDigest = ""
		case "digest uppercase":
			invalid.DestinationDigest = strings.Repeat("A", 64)
		case "digest nonhex":
			invalid.DestinationDigest = strings.Repeat("z", 64)
		case "smtp connector":
			invalid.Transport = "smtp"
		}
		require.ErrorIs(t, invalid.Validate(), executionscope.ErrDenied, field)
	}
}
