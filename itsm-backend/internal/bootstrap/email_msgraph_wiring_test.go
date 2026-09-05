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
	creation "itsm-backend/handlers/common/workitemcreation"
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

	app := &emailApplicationRecorder{}
	adapter := newTicketStoreAdapter(client, app)

	id, found, err := adapter.FindActiveUserByEmail(context.Background(), tenant.ID, "alice@test.com")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, user.ID, id)

	_, found, err = adapter.FindActiveUserByEmail(context.Background(), tenant.ID, "nobody@test.com")
	require.NoError(t, err)
	assert.False(t, found)
}

type emailApplicationRecorder struct {
	identity creation.Identity
	command  creation.CreateWorkItemCommand
}

func (a *emailApplicationRecorder) Create(_ context.Context, identity creation.Identity, command creation.CreateWorkItemCommand) (*creation.CreateWorkItemResult, error) {
	a.identity = identity
	a.command = command
	return &creation.CreateWorkItemResult{WorkItemID: 23, Number: "TKT-202609-000023"}, nil
}
func TestTicketStoreAdapter_ForwardsVerifiedEmailToIntake(t *testing.T) {
	client, tenant, actor := newWiringFixture(t)
	defer client.Close()
	app := &emailApplicationRecorder{}
	adapter := newTicketStoreAdapter(client, app)
	req := msgraphInboundTicketRequestFixture(actor.ID)
	req.Mailbox = "support@test.com"
	req.GraphMessageID = "graph-1"
	req.HasAttachments = true
	req.TriageComment = "advisory"
	req.ConversationID = "conversation"
	id, number, err := adapter.CreateTicket(context.Background(), tenant.ID, req)
	require.NoError(t, err)
	require.Equal(t, 23, id)
	require.Equal(t, "TKT-202609-000023", number)
	require.Equal(t, creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "email", Provider: "msgraph_email"}, app.identity)
	require.Equal(t, "graph-1", app.command.Email.GraphMessageID)
	require.True(t, app.command.Email.HasAttachments)
	require.Equal(t, "advisory", app.command.Email.TriageComment)
	require.Equal(t, req.ExternalMessageID, app.command.SourceReference.EventID)
	require.Zero(t, client.Ticket.Query().CountX(context.Background()), "adapter cannot add an independent creation transaction")
	require.Zero(t, client.AuditLog.Query().CountX(context.Background()))
	req.CreatorEmail = "foreign@test.com"
	_, _, err = adapter.CreateTicket(context.Background(), tenant.ID, req)
	require.Error(t, err)
}
func TestTicketStoreAdapter_AmbiguousEmailFailsClosed(t *testing.T) {
	client, tenant, _ := newWiringFixture(t)
	defer client.Close()
	client.User.Create().SetTenantID(tenant.ID).SetUsername("other").SetName("Other").SetEmail("alice@test.com").SetPasswordHash("hash").SetRole("end_user").SaveX(context.Background())
	_, found, err := newTicketStoreAdapter(client, &emailApplicationRecorder{}).FindActiveUserByEmail(context.Background(), tenant.ID, "alice@test.com")
	require.Error(t, err)
	require.False(t, found)
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

	app := &emailApplicationRecorder{}
	logger := zaptest.NewLogger(t).Sugar()
	triageService := service.NewTriageServiceWithSugaredLogger(nil, logger)

	reg := connector.NewRegistry()
	mgr := connector.NewManager(reg, logger)
	mkt := connectorMarketplace.New()
	connCtrl := controller.NewConnectorController(mgr, reg, mkt, logger, nil, nil)

	// Must not panic even though this is a from-scratch registry/controller —
	// that's the behavior under test.
	wireEmailMsgraphConnector(client, app, triageService, connCtrl, logger)
}
