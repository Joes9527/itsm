package bootstrap

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/connector"
	connectorMarketplace "itsm-backend/connector/marketplace"
	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
)

func newWiringFixture(t *testing.T) (*ent.Client, *ent.Tenant, *ent.User) {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:wiring_test?mode=memory&cache=shared&_fk=1")

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test").SetDomain("test.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("alice").SetEmail("Alice@Test.com").SetName("Alice").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return client, tenant, user
}

// msgraphInboundTicketRequestFixture builds a minimal InboundTicketRequest
// (aliased locally as msgraphInboundTicketRequest — see email_msgraph_wiring.go)
// for adapter tests below.
func msgraphInboundTicketRequestFixture(requesterID int) msgraphInboundTicketRequest {
	return msgraphInboundTicketRequest{
		Title:             "From email",
		Description:       "body",
		Priority:          "medium",
		RequesterID:       requesterID,
		CreatorEmail:      "alice@test.com",
		Source:            "email",
		ExternalMessageID: "<abc@contoso.com>",
	}
}

func TestTicketStoreAdapter_FindActiveUserByEmail_CaseInsensitive(t *testing.T) {
	client, tenant, user := newWiringFixture(t)
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := service.NewTicketServiceForTest(client, logger)
	adapter := newTicketStoreAdapter(client, ticketService)

	id, found, err := adapter.FindActiveUserByEmail(context.Background(), tenant.ID, "alice@test.com")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, user.ID, id)

	_, found, err = adapter.FindActiveUserByEmail(context.Background(), tenant.ID, "nobody@test.com")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestTicketStoreAdapter_CreateTicket_AndDedup(t *testing.T) {
	client, tenant, user := newWiringFixture(t)
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := service.NewTicketServiceForTest(client, logger)
	adapter := newTicketStoreAdapter(client, ticketService)
	ctx := context.Background()

	existsBefore, err := adapter.TicketExistsForExternalMessage(ctx, tenant.ID, "<abc@contoso.com>")
	require.NoError(t, err)
	assert.False(t, existsBefore)

	ticketID, ticketNumber, err := adapter.CreateTicket(ctx, tenant.ID, msgraphInboundTicketRequestFixture(user.ID))
	require.NoError(t, err)
	assert.NotZero(t, ticketID)
	assert.NotEmpty(t, ticketNumber)

	existsAfter, err := adapter.TicketExistsForExternalMessage(ctx, tenant.ID, "<abc@contoso.com>")
	require.NoError(t, err)
	assert.True(t, existsAfter)
}

func TestTicketStoreAdapter_PostSystemComment(t *testing.T) {
	client, tenant, user := newWiringFixture(t)
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := service.NewTicketServiceForTest(client, logger)
	adapter := newTicketStoreAdapter(client, ticketService)
	ctx := context.Background()

	ticketID, _, err := adapter.CreateTicket(ctx, tenant.ID, msgraphInboundTicketRequestFixture(user.ID))
	require.NoError(t, err)

	err = adapter.PostSystemComment(ctx, tenant.ID, ticketID, user.ID, "AI 分派参考：测试评论")
	require.NoError(t, err)

	count, err := client.TicketComment.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestWireEmailMsgraphConnector_RegistersCoordinator(t *testing.T) {
	client, _, _ := newWiringFixture(t)
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := service.NewTicketServiceForTest(client, logger)
	triageService := service.NewTriageServiceWithSugaredLogger(nil, logger)

	reg := connector.NewRegistry()
	mgr := connector.NewManager(reg, logger)
	mkt := connectorMarketplace.New()
	connCtrl := controller.NewConnectorController(mgr, reg, mkt, logger)

	// Must not panic even though this is a from-scratch registry/controller —
	// that's the behavior under test.
	wireEmailMsgraphConnector(client, ticketService, triageService, connCtrl, logger)
}
