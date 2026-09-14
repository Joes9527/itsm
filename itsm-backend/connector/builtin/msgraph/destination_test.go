package msgraph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/connector"
)

type graphDestinationDescriber interface {
	DescribeDeliveryDestination(connector.Config) (string, error)
}

func graphDestinationConfig() connector.Config {
	return connector.Config{Settings: map[string]interface{}{"azure_tenant_id": "test-tenant", "mailbox": "original@example.invalid"}, Credentials: map[string]string{"azure_client_id": "test-app", "azure_client_secret": "private-secret"}}
}

func TestGraphDestinationDescribesWithoutActivation(t *testing.T) {
	g := New()
	describe, ok := any(g).(graphDestinationDescriber)
	require.True(t, ok, "Graph must describe a configured target without activating it")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	cfg := graphDestinationConfig()
	cfg.Settings["aad_base_url"], cfg.Settings["graph_base_url"] = server.URL, server.URL+"/graph"
	digest, err := describe.DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	require.Regexp(t, `^[0-9a-f]{64}$`, digest)
	require.Nil(t, g.GraphClient())
	require.Zero(t, calls.Load())
	require.NoError(t, g.Init(context.Background(), cfg))
	bound, ok := any(g).(connector.DeliveryDestination)
	require.True(t, ok)
	require.Equal(t, digest, bound.DeliveryDestinationIdentity())
	cfg.Settings["mailbox"] = "changed@example.invalid"
	cfg.Credentials["azure_client_id"] = "changed-app"
	require.Equal(t, digest, bound.DeliveryDestinationIdentity())
	require.Equal(t, "original@example.invalid", g.Mailbox())
	require.Equal(t, "test-app", g.GraphClient().clientID)
	require.Error(t, g.Init(context.Background(), cfg), "reinitialization cannot replace an active destination")
	require.Equal(t, digest, bound.DeliveryDestinationIdentity())
	require.Zero(t, calls.Load())
}

func TestGraphDestinationCoversActualRoutingIdentity(t *testing.T) {
	describe, ok := any(New()).(graphDestinationDescriber)
	require.True(t, ok)
	original, err := describe.DescribeDeliveryDestination(graphDestinationConfig())
	require.NoError(t, err)
	for _, field := range []string{"azure_tenant_id", "mailbox", "azure_client_id", "aad_base_url", "graph_base_url"} {
		t.Run(field, func(t *testing.T) {
			cfg := graphDestinationConfig()
			switch field {
			case "azure_client_id":
				cfg.Credentials[field] = "other-app"
			case "mailbox":
				cfg.Settings[field] = "other@example.invalid"
			case "azure_tenant_id":
				cfg.Settings[field] = "other-tenant"
			default:
				cfg.Settings[field] = "https://other.example.invalid"
			}
			changed, err := describe.DescribeDeliveryDestination(cfg)
			require.NoError(t, err)
			require.NotEqual(t, original, changed)
		})
	}
	cfg := graphDestinationConfig()
	cfg.Credentials["azure_client_secret"] = "rotated-secret"
	changed, err := describe.DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	require.Equal(t, original, changed, "secret rotation is not a new delivery destination")
	delete(cfg.Credentials, "azure_client_secret")
	changed, err = describe.DescribeDeliveryDestination(cfg)
	require.NoError(t, err, "pure identity read does not need access to the secret")
	require.Equal(t, original, changed)
	cfg.Settings["aad_base_url"] = DefaultAADBaseURL
	cfg.Settings["graph_base_url"] = DefaultGraphBaseURL
	changed, err = describe.DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	require.Equal(t, original, changed, "explicit and effective defaults identify the same route")
}

func TestGraphDestinationRejectsMalformedIdentity(t *testing.T) {
	for _, field := range []string{"mailbox", "azure_tenant_id", "aad_base_url", "graph_base_url"} {
		cfg := graphDestinationConfig()
		cfg.Settings[field] = 123
		digest, err := New().DescribeDeliveryDestination(cfg)
		require.Error(t, err)
		require.Empty(t, digest)
	}
	for _, endpoint := range []string{"relative", "https://secret:password@example.invalid", "https://example.invalid?secret=value", "https://example.invalid#fragment"} {
		cfg := graphDestinationConfig()
		cfg.Settings["graph_base_url"] = endpoint
		digest, err := New().DescribeDeliveryDestination(cfg)
		require.Error(t, err)
		require.Empty(t, digest)
		require.NotContains(t, err.Error(), endpoint)
	}
}

func TestGraphRegistryDescriptionWithoutActivation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	cfg := graphDestinationConfig()
	cfg.Name, cfg.Provider = "msgraph-email", "microsoft"
	cfg.Settings["aad_base_url"], cfg.Settings["graph_base_url"] = server.URL, server.URL+"/graph"
	delete(cfg.Credentials, "azure_client_secret")
	expected, err := New().DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	type result struct {
		digest string
		err    error
	}
	results := make(chan result, 16)
	for i := 0; i < 16; i++ {
		go func() { d, e := connector.Default().DescribeDeliveryDestination(cfg); results <- result{d, e} }()
	}
	for i := 0; i < 16; i++ {
		r := <-results
		require.NoError(t, r.err)
		require.Equal(t, expected, r.digest)
	}
	require.Zero(t, requests.Load())
}
