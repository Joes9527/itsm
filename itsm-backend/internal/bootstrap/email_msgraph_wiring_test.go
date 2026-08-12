package bootstrap

import (
	"context"
	"strings"
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

// TestTicketStoreAdapter_CreateTicket_WritesAuditLog is a regression test:
// tickets created through the normal HTTP API get an audit_log row via
// middleware.AuditMiddleware, but this adapter's CreateTicket is called
// from a background polling goroutine that never goes through HTTP — so
// without an explicit audit write here, connector-created tickets would be
// LESS auditable than manually-created ones. Per CLAUDE.md: "Any high-risk
// action triggered by AI, connector, workflow automation, or bulk operation
// must create an audit record."
func TestTicketStoreAdapter_CreateTicket_WritesAuditLog(t *testing.T) {
	client, tenant, user := newWiringFixture(t)
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := service.NewTicketServiceForTest(client, logger)
	adapter := newTicketStoreAdapter(client, ticketService)
	ctx := context.Background()

	countBefore, err := client.AuditLog.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, countBefore)

	ticketID, _, err := adapter.CreateTicket(ctx, tenant.ID, msgraphInboundTicketRequestFixture(user.ID))
	require.NoError(t, err)

	logs, err := client.AuditLog.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, logs, 1, "exactly one audit_log row must exist after a connector-created ticket")

	entry := logs[0]
	assert.Equal(t, "ticket", entry.Resource)
	assert.Equal(t, "create", entry.Action)
	assert.Equal(t, tenant.ID, entry.TenantID)
	assert.Equal(t, user.ID, entry.UserID)
	assert.Contains(t, entry.Path, "msgraph")
	_ = ticketID
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

// triageServiceSuggestForTenantSignature is a compile-time pin on
// TriageService.SuggestForTenant's parameter order: (ctx, title,
// description string, tenantID int). msgraph.Triager's own Suggest method
// deliberately uses a different order — (ctx, tenantID int, title,
// description string) — and triagerAdapter.Suggest is the one place that
// bridges the two. If SuggestForTenant's signature is ever changed (e.g.
// reordered to match the interface for "consistency"), this line stops
// compiling, forcing whoever makes that change to also revisit
// triagerAdapter.Suggest's argument order instead of it silently going
// stale.
var _ func(context.Context, string, string, int) service.TriageResult = (*service.TriageService)(nil).SuggestForTenant

func TestTriagerAdapter_Suggest_ForwardsToRealTriageServiceInCorrectRoles(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	// Nil gateway (and no guidance client) means TriageService falls back
	// to its own deterministic keyword-based classifier
	// (TriageService.keywordBasedSuggest), so this exercises real
	// classification logic end-to-end without needing an LLM.
	triageService := service.NewTriageServiceWithSugaredLogger(nil, logger)
	adapter := newTriagerAdapter(triageService)

	// title carries the keyword that drives category ("数据库" ->
	// "database", see keywordBasedSuggest's database branch, confidence
	// 0.7). description carries the keyword that drives priority
	// escalation ("紧急" -> escalates the default "medium" priority to
	// "high" and adds +0.1 confidence). The expected result below can
	// only come out this way if BOTH title's and description's text
	// actually reach TriageService.SuggestForTenant in their own roles —
	// if the adapter dropped, duplicated, or dead-code-swapped either
	// string (or passed tenantID where a string was expected — which
	// would fail to compile today, but is exactly the drift the
	// signature pin above guards against for the future), category
	// and/or priority would not match.
	suggestion := adapter.Suggest(context.Background(), 42, "数据库连接失败", "系统紧急，请尽快处理")

	assert.Equal(t, "database", suggestion.Category)
	assert.Equal(t, "high", suggestion.Priority)
	assert.InDelta(t, 0.8, suggestion.Confidence, 0.0001)
	assert.Equal(t, "keyword heuristic", suggestion.Explanation)
}

// fakeTitleLabelingLLMProvider lets TestTriagerAdapter_Suggest_DoesNotSwapTitleAndDescription
// inspect the actual prompt TriageService.llmClassify builds (it labels
// title and description with distinct "Title: " / "Description:\n"
// markers — see llmClassify in service/triage_service.go). Unlike the
// keyword-based fallback exercised above — which concatenates title and
// description together and is therefore symmetric with respect to which
// one is which — the LLM prompt path is genuinely order-sensitive, so it
// can actually catch a title/description swap in the adapter.
type fakeTitleLabelingLLMProvider struct{}

func (fakeTitleLabelingLLMProvider) Chat(ctx context.Context, model string, messages []service.LLMMessage) (string, error) {
	prompt := messages[len(messages)-1].Content
	if strings.Contains(prompt, "Title: TITLE_MARKER") {
		return `{"category":"database","priority":"high","confidence":0.9,"explanation":"marker seen in Title slot"}`, nil
	}
	return `{"category":"general","priority":"low","confidence":0.9,"explanation":"marker not seen in Title slot"}`, nil
}

func TestTriagerAdapter_Suggest_DoesNotSwapTitleAndDescription(t *testing.T) {
	logger := zaptest.NewLogger(t)
	gateway := service.NewLLMGateway(fakeTitleLabelingLLMProvider{}, nil, nil, "fake")
	triageService := service.NewTriageService(gateway, logger)
	adapter := newTriagerAdapter(triageService)

	// "TITLE_MARKER" only produces category "database" if it lands in the
	// prompt's "Title: " slot (per fakeTitleLabelingLLMProvider above) —
	// which only happens if triagerAdapter.Suggest forwards its title
	// argument to TriageService.SuggestForTenant's title parameter and not
	// its description parameter.
	suggestion := adapter.Suggest(context.Background(), 42, "TITLE_MARKER", "unrelated filler text")

	assert.Equal(t, "database", suggestion.Category)
	assert.Equal(t, "high", suggestion.Priority)
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
