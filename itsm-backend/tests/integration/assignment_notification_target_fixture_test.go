//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/service"
)

// assignmentGraphTargetFixture exercises the persisted durable target contract
// against a local Graph endpoint. It never consults the legacy provider selector.
func assignmentGraphTargetFixture(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, policy *database.ExecutionPolicy, sender service.GraphMailSender) *service.EmailService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"fixture-token","expires_in":3600}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/users/support@example.test/sendMail" {
			t.Errorf("unexpected bound Graph request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected target", http.StatusBadRequest)
			return
		}
		var payload struct {
			Message struct {
				ToRecipients           []struct{ EmailAddress struct{ Address string } }
				Subject                string
				Body                   struct{ Content string }
				InternetMessageHeaders []struct{ Name, Value string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Message.ToRecipients) != 1 {
			t.Error("invalid Graph mail payload")
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		deliveryID := ""
		for _, header := range payload.Message.InternetMessageHeaders {
			if strings.EqualFold(header.Name, "X-ITSM-Delivery-ID") {
				deliveryID = header.Value
			}
		}
		if deliveryID == "" {
			t.Error("bound Graph mail lacks durable delivery identity")
			http.Error(w, "missing delivery identity", http.StatusBadRequest)
			return
		}
		// HTTP does not carry Go context values. The recorder runs at this
		// fixture's tenant-bound Graph destination, using the ID sent on the wire.
		deliveryCtx := tenantctx.WithTenantID(r.Context(), tenantID)
		if err := sender.SendMail(deliveryCtx, "support@example.test", payload.Message.ToRecipients[0].EmailAddress.Address, payload.Message.Subject, payload.Message.Body.Content, deliveryID); err != nil {
			// The existing recorder models acceptance followed by lost response.
			// A 503 would instead assert definitive provider rejection.
			connection, _, hijackErr := w.(http.Hijacker).Hijack()
			if hijackErr != nil {
				t.Errorf("cannot simulate lost Graph response: %v", hijackErr)
				return
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	cfg := connector.Config{TenantID: tenantID, Name: "msgraph-email", Provider: "microsoft", Enabled: true, Settings: map[string]interface{}{"azure_tenant_id": "fixture", "mailbox": "support@example.test", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "fixture-app", "azure_client_secret": "fixture-secret"}}
	settings, err := json.Marshal(cfg.Settings)
	require.NoError(t, err)
	credentials, err := json.Marshal(cfg.Credentials)
	require.NoError(t, err)
	client.ConnectorConfig.Create().SetTenantID(tenantID).SetName(cfg.Name).SetProvider(cfg.Provider).SetEnabled(true).SetSettings(string(settings)).SetCredentials(string(credentials)).SaveX(ctx)
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return msgraph.New() })
	manager := connector.NewManager(registry, nil, policy)
	t.Cleanup(func() { manager.CloseAll() })
	require.NoError(t, manager.Provision(ctx, cfg))
	mailer := service.NewEmailService(service.EmailConfig{DeliveryTransport: "graph"}, zap.NewNop().Sugar())
	mailer.SetDeliveryTargetDependencies(manager, policy)
	mailer.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) {
		t.Error("durable target consulted legacy selector")
		return nil, "", false
	})
	return mailer
}
