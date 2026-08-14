package msgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"itsm-backend/connector"
)

func TestGraphConnector_Manifest(t *testing.T) {
	g := New()
	m := g.Manifest()
	assert.Equal(t, "msgraph-email", m.Name)
	assert.Equal(t, connector.TypeEmail, m.Type)
	assert.Contains(t, m.RequiredPermissions, "connector:write")
	assert.Contains(t, m.RequiredPermissions, "ticket:write")
}

func TestGraphConnector_Init_RequiresAllFields(t *testing.T) {
	g := New()
	err := g.Init(context.Background(), connector.Config{
		Settings: map[string]interface{}{"azure_tenant_id": "t"},
	})
	require.Error(t, err, "missing mailbox/client credentials must fail init")
}

func TestGraphConnector_Init_Success(t *testing.T) {
	g := New()
	err := g.Init(context.Background(), connector.Config{
		Settings: map[string]interface{}{
			"azure_tenant_id": "test-tenant",
			"mailbox":         "support@contoso.com",
		},
		Credentials: map[string]string{
			"azure_client_id":     "id",
			"azure_client_secret": "secret",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "support@contoso.com", g.Mailbox())
	assert.NotNil(t, g.GraphClient())
}

func TestGraphConnector_HealthCheck_NotInitialized(t *testing.T) {
	g := New()
	h := g.HealthCheck(context.Background())
	assert.False(t, h.OK)
}

func TestGraphConnector_RegisteredInDefaultRegistry(t *testing.T) {
	// connector.go's init() registers into connector.Default() as a package
	// side effect — this test just confirms it happened once this package
	// is imported (which it is, being the same package under test).
	_, ok := connector.Default().Get("msgraph-email")
	assert.True(t, ok)
}
