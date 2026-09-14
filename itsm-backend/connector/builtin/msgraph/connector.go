package msgraph

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/connector"
)

// GraphConnector is the connector.Connector implementation backed by the
// MS Graph client in client.go.
type GraphConnector struct {
	client      *Client
	mailbox     string
	destination string
	cfg         connector.Config
}

func init() {
	connector.MustRegister(func() connector.Connector { return &GraphConnector{} })
}

func New() *GraphConnector { return &GraphConnector{} }

func (g *GraphConnector) Manifest() connector.Manifest {
	return connector.Manifest{
		Name:        "msgraph-email",
		Version:     "1.0.0",
		Title:       "邮件（Microsoft Graph）",
		Provider:    "microsoft",
		Type:        connector.TypeEmail,
		Description: "通过 Microsoft Graph API（app-only）读写共享邮箱：定时轮询收信自动建单 + 发送确认回信。",
		Capabilities: []connector.Capability{
			connector.CapSendMessage,
			connector.CapReceiveMessage,
			connector.CapCreateTicket,
		},
		Tags:                []string{"email", "microsoft", "graph", "azure"},
		Homepage:            "https://learn.microsoft.com/en-us/graph/api/resources/mail-api-overview",
		IsOfficial:          true,
		RequiredPermissions: []string{"connector:write", "ticket:write"},
		// Init only parses and captures local config; token acquisition, health
		// checks and polling remain explicit execution operations.
		InitializationBehavior: connector.InitializationLocalOnly,
	}
}

func (g *GraphConnector) Init(_ context.Context, cfg connector.Config) error {
	if g.client != nil {
		return fmt.Errorf("msgraph: connector already initialized")
	}
	destination, err := parseGraphDestination(cfg)
	if err != nil {
		return err
	}
	clientSecret := cfg.Credentials["azure_client_secret"]
	if clientSecret == "" {
		return fmt.Errorf("msgraph: client secret is required")
	}
	g.client = NewClient(destination.AzureTenantID, destination.ClientID, clientSecret, destination.AADBaseURL, destination.GraphBaseURL)
	g.mailbox = destination.Mailbox
	g.destination = destination.digest()
	g.cfg = cfg
	return nil
}

// Send delivers msg.Content as a plain-text email to msg.Channel (the
// recipient address), from the configured shared mailbox.
func (g *GraphConnector) Send(ctx context.Context, msg *connector.Message) error {
	if g.client == nil {
		return fmt.Errorf("msgraph: connector not initialized")
	}
	return g.client.SendMail(ctx, g.mailbox, msg.Channel, msg.Title, msg.Content, "")
}

// SendMail exposes the original mail client through its captured connector
// identity. A caller cannot substitute another mailbox on this bound sender.
func (g *GraphConnector) SendMail(ctx context.Context, mailbox, to, subject, body, deliveryID string) error {
	if g.client == nil || mailbox != g.mailbox {
		return &deliveryOutcomeError{stage: "target", outcome: "not_accepted", err: fmt.Errorf("msgraph: delivery identity mismatch")}
	}
	return g.client.SendMail(ctx, g.mailbox, to, subject, body, deliveryID)
}

func (g *GraphConnector) HealthCheck(ctx context.Context) connector.HealthStatus {
	if g.client == nil {
		return connector.HealthStatus{OK: false, Message: "not initialized", CheckedAt: time.Now()}
	}
	start := time.Now()
	if _, err := g.client.Token(ctx); err != nil {
		return connector.HealthStatus{OK: false, Message: err.Error(), CheckedAt: time.Now()}
	}
	return connector.HealthStatus{
		OK:        true,
		Message:   "token acquired",
		LatencyMs: time.Since(start).Milliseconds(),
		CheckedAt: time.Now(),
	}
}

func (g *GraphConnector) Close() error { return nil }

// Mailbox returns the configured shared mailbox address, used by the
// polling coordinator (Task 5/6).
func (g *GraphConnector) Mailbox() string { return g.mailbox }

// GraphClient exposes the underlying HTTP client, used by the polling
// coordinator (Task 5/6) to call PollDelta directly.
func (g *GraphConnector) GraphClient() *Client { return g.client }

// PollIntervalSeconds reads settings.poll_interval_seconds, defaulting to 60.
func (g *GraphConnector) PollIntervalSeconds() int {
	if v, ok := g.cfg.Settings["poll_interval_seconds"].(float64); ok && v > 0 {
		return int(v)
	}
	return 60
}

var _ connector.Connector = (*GraphConnector)(nil)
