//go:build candidate_scope

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
	"itsm-backend/service"
)

type candidateIncidentMailTransport struct {
	cfg             service.EmailConfig
	target          *config.ConnectorTargetConfig
	calls, accepted *atomic.Int32
	verify          func(*testing.T, string)
}

func (r candidateIncidentMailTransport) mailer(t *testing.T, ctx context.Context, policy *database.ExecutionPolicy) *service.EmailService {
	t.Helper()
	var manager *connector.Manager
	if r.target != nil {
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return msgraph.New() })
		manager = connector.NewManager(registry, nil, policy)
		t.Cleanup(manager.CloseAll)
		before := r.calls.Load()
		require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:graph-activation", "private candidate target")))
		require.Equal(t, before, r.calls.Load(), "activation must not call token, health or polling endpoints")
	}
	mailer := service.NewEmailService(r.cfg, zap.NewNop().Sugar())
	mailer.SetDeliveryTargetDependencies(manager, policy)
	mailer.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) {
		t.Error("candidate bound delivery must not query live provider")
		return nil, "", false
	})
	return mailer
}

func newCandidateIncidentMailTransport(t *testing.T, tenantID int, scopeID, transport string) candidateIncidentMailTransport {
	t.Helper()
	if transport == "smtp" {
		cfg, calls, accepted, received := startIncidentMailReceiver(t)
		return candidateIncidentMailTransport{cfg: cfg, calls: calls, accepted: accepted, verify: func(t *testing.T, recipient string) {
			proof := <-received
			require.Equal(t, "RCPT TO:<"+recipient+">", proof.Recipient)
			require.Contains(t, proof.Data, base64.StdEncoding.EncodeToString([]byte("candidate private body")))
		}}
	}
	require.Equal(t, "graph", transport)
	calls, accepted := &atomic.Int32{}, &atomic.Int32{}
	type request struct{ path, body, auth string }
	received := make(chan request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		received <- request{r.URL.Path, string(body), r.Header.Get("Authorization")}
		if r.Method == http.MethodPost && r.URL.Path == "/private-tenant/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"private-token","expires_in":3600}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/graph/users/private-mailbox@example.invalid/sendMail" {
			http.Error(w, "unexpected route", 400)
			return
		}
		accepted.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	cfg := connector.Config{TenantID: tenantID, Name: "msgraph-email", Provider: "microsoft", Settings: map[string]interface{}{"azure_tenant_id": "private-tenant", "mailbox": "private-mailbox@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL + "/graph"}, Credentials: map[string]string{"azure_client_id": "private-app", "azure_client_secret": "private-secret"}}
	digest, err := msgraph.New().DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	target := config.ConnectorTargetConfig{TenantID: tenantID, ScopeID: scopeID, Name: cfg.Name, Provider: cfg.Provider, Settings: cfg.Settings, Credentials: cfg.Credentials, DestinationDigest: digest, Capabilities: []string{"outbox"}}
	return candidateIncidentMailTransport{cfg: service.EmailConfig{DeliveryTransport: "graph"}, target: &target, calls: calls, accepted: accepted, verify: func(t *testing.T, recipient string) {
		require.Equal(t, int32(2), calls.Load())
		token, mail := <-received, <-received
		values, err := url.ParseQuery(token.body)
		require.NoError(t, err)
		require.Equal(t, "private-app", values.Get("client_id"))
		require.Equal(t, "private-secret", values.Get("client_secret"))
		require.Equal(t, "Bearer private-token", mail.auth)
		var payload struct {
			Message struct {
				Subject string `json:"subject"`
				Body    struct {
					Content string `json:"content"`
				} `json:"body"`
				ToRecipients []struct {
					EmailAddress struct {
						Address string `json:"address"`
					} `json:"emailAddress"`
				} `json:"toRecipients"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal([]byte(mail.body), &payload))
		require.Equal(t, "[ITSM Alert] candidate mail", payload.Message.Subject)
		require.Equal(t, "candidate private body", payload.Message.Body.Content)
		require.Len(t, payload.Message.ToRecipients, 1)
		require.Equal(t, recipient, payload.Message.ToRecipients[0].EmailAddress.Address)
	}}
}
