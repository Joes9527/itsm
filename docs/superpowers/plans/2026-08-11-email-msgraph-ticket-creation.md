# 邮件建单（MS Graph）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Inbound mail to a shared mailbox automatically becomes an ITSM ticket, via a new MS Graph (app-only OAuth) connector that polls the mailbox on a timer, resolves the sender to a registered user, gets an AI priority suggestion, creates the ticket, leaves an audit comment, and sends a confirmation reply — all wired into the existing connector provisioning lifecycle.

**Architecture:** A new, self-contained `connector/builtin/msgraph` package (HTTP client + `connector.Connector` implementation + polling coordinator) that depends only on small locally-defined interfaces (`TicketStore`, `Triager`) — never on `ent`, `dto`, or `service` directly. A new `internal/bootstrap/email_msgraph_wiring.go` file provides the concrete adapters wrapping `*ent.Client`/`*service.TicketService`/`*service.TriageService` that satisfy those interfaces, and wires the coordinator into `ConnectorController` (which gains one optional dependency + two call sites). One ent schema field (`Ticket.external_message_id`) is added for idempotency; two DTO/repository fields (`CreatorEmail`, `ExternalMessageID`) are threaded through the existing ticket-creation path.

**Tech Stack:** Go, Ent ORM (PostgreSQL in prod, SQLite in-memory via `ent/enttest` for tests), Gin, `go.uber.org/zap`, `stretchr/testify`, `net/http/httptest` for HTTP client tests.

## Global Constraints

- Design source of truth: `docs/superpowers/specs/2026-08-11-email-msgraph-ticket-creation-design.md` — every task below implements a specific decision from that document; do not deviate without checking it first.
- Do not modify or register `connector/builtin/email/` (the old IMAP connector) — it stays untouched and unregistered (design decision 8).
- Do not modify `service/marketplace/service.go` (design decision 4).
- `TriageResult.Category` / the coordinator's suggested category must never be written to `Ticket.CategoryID` — only surfaced as an advisory comment (design decision 6). `Priority` is the only AI field auto-applied.
- Follow CLAUDE.md's DTO/mapper rules: controllers never return Ent models directly (not touched by this plan, but the new fields must flow through the existing DTO/mapper boundary, not bypass it).
- Backend Go files: `snake_case.go`. Verification order per task: narrow package test first (`go test ./connector/builtin/msgraph/...` etc.), then `go test ./...` at the end of the plan.

---

## File Structure

| File | Responsibility |
|---|---|
| `ent/schema/ticket.go` (modify) | Add `external_message_id` field + composite index |
| `dto/ticket_dto.go` (modify) | Add `CreatorEmail`, `ExternalMessageID` to `CreateTicketRequest` |
| `repository/ticket/model.go` (modify) | Add same two fields to `CreateParams` |
| `repository/ticket/repository_impl.go` (modify) | Persist the two new fields in `Create()` |
| `service/ticket_service.go` (modify) | Pass the two new fields from DTO into `CreateParams` |
| `connector/builtin/msgraph/client.go` (new) | Graph/AAD HTTP client: token acquisition+cache, delta polling with pagination, sendMail |
| `connector/builtin/msgraph/connector.go` (new) | `connector.Connector` implementation, manifest, registration |
| `connector/builtin/msgraph/coordinator.go` (new) | `TicketStore`/`Triager` interfaces, per-tenant polling goroutines, message-handling pipeline |
| `connector/builtin/msgraph/reply_template.go` (new) | Confirmation-reply text template + body-cleaning helper (ported from the old email connector) |
| `internal/bootstrap/email_msgraph_wiring.go` (new) | Concrete `TicketStore`/`Triager` adapters over ent/service; wiring function |
| `controller/connector_controller.go` (modify) | Optional coordinator dependency + Provision/Revoke hook |
| `internal/bootstrap/app.go` (modify) | Blank-import `msgraph`, call the wiring function |

---

### Task 1: Ent schema — `Ticket.external_message_id`

**Files:**
- Modify: `ent/schema/ticket.go` (Fields() ~line 17-138, Indexes() ~line 163-178)
- Test: `ent/ticket_extra_test.go` (new)

**Interfaces:**
- Produces: `ticket.FieldExternalMessageID`, `(*ent.TicketCreate).SetExternalMessageID(string)`, `ticket.ExternalMessageIDEQ(string) predicate.Ticket` — all ent-generated, consumed by Task 2 and Task 6's bootstrap adapter.

- [ ] **Step 1: Write the failing test**

Create `ent/ticket_extra_test.go`:

```go
package ent_test

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"
)

func TestTicket_ExternalMessageID_Dedup(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ticket_extmsg_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test").SetDomain("test.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("alice").SetEmail("alice@test.com").SetName("Alice").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Ticket.Create().
		SetTitle("From email").
		SetDescription("body").
		SetType("incident").
		SetPriority("medium").
		SetTicketNumber("TCK-EXT-0001").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		SetStatus("new").
		SetCreatorEmail("alice@test.com").
		SetExternalMessageID("<msg-1@contoso.com>").
		Save(ctx)
	require.NoError(t, err)

	exists, err := client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenant.ID), ticket.ExternalMessageIDEQ("<msg-1@contoso.com>")).
		Exist(ctx)
	require.NoError(t, err)
	require.True(t, exists, "expected ticket to be found by external_message_id")

	notExists, err := client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenant.ID), ticket.ExternalMessageIDEQ("<msg-2@contoso.com>")).
		Exist(ctx)
	require.NoError(t, err)
	require.False(t, notExists, "unrelated message id must not match")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./ent/... -run TestTicket_ExternalMessageID_Dedup -v`
Expected: FAIL — compile error, `SetExternalMessageID` / `ticket.ExternalMessageIDEQ` undefined (field doesn't exist yet).

- [ ] **Step 3: Add the field to the schema**

In `ent/schema/ticket.go`, inside `Fields()`, immediately after the existing `creator_email` field (the one commented `"创建人邮箱（邮件开单时记录，非注册用户也可创建）"`), add:

```go
		field.String("external_message_id").
			Comment("外部消息ID（如邮件 internetMessageId），用于同一来源消息的建单去重判断").
			Optional(),
```

In `Indexes()`, add one line alongside the existing `index.Fields("tenant_id", "status")` etc.:

```go
		index.Fields("tenant_id", "external_message_id"),
```

- [ ] **Step 4: Regenerate ent code**

Run: `cd itsm-backend && go generate ./ent/...`
Expected: regenerates `ent/ticket.go`, `ent/ticket_create.go`, `ent/ticket_update.go`, `ent/ticket/*.go`, `ent/migrate/schema.go`, `ent/mutation.go` with `ExternalMessageID` support (mirroring the existing `CreatorEmail` generated code). No manual edits to generated files.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd itsm-backend && go test ./ent/... -run TestTicket_ExternalMessageID_Dedup -v`
Expected: PASS

- [ ] **Step 6: Full package build check**

Run: `cd itsm-backend && go build ./...`
Expected: succeeds (confirms the regenerated ent code didn't break any existing caller).

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add ent/schema/ticket.go ent/ticket.go ent/ticket_create.go ent/ticket_update.go ent/ticket/ ent/migrate/schema.go ent/mutation.go ent/ticket_extra_test.go
git commit -m "feat(ticket): add external_message_id field for inbound-channel dedup"
```

---

### Task 2: Thread `CreatorEmail` / `ExternalMessageID` through ticket creation

**Files:**
- Modify: `dto/ticket_dto.go:17-35` (`CreateTicketRequest`)
- Modify: `repository/ticket/model.go:213-229` (`CreateParams`)
- Modify: `repository/ticket/repository_impl.go:69-126` (`Create`)
- Modify: `service/ticket_service.go:144-153` (`params := &ticket.CreateParams{...}` inside `CreateTicket`)
- Test: `repository/ticket/repository_test.go` (add a test function)

**Interfaces:**
- Consumes: `ticket.FieldExternalMessageID`, `SetExternalMessageID`/`SetCreatorEmail` from Task 1.
- Produces: `dto.CreateTicketRequest.CreatorEmail string`, `dto.CreateTicketRequest.ExternalMessageID string`, `ticket.CreateParams.CreatorEmail string`, `ticket.CreateParams.ExternalMessageID string` — consumed by Task 6's bootstrap `TicketStore` adapter, which builds a `dto.CreateTicketRequest` and calls `TicketService.CreateTicket`.

- [ ] **Step 1: Write the failing test**

Add to `repository/ticket/repository_test.go` (same file as the existing `newRepoFixture` helper, so no new imports needed beyond what's already there):

```go
func TestRepository_Create_PersistsCreatorEmailAndExternalMessageID(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	params := &CreateParams{
		Title:             "From email",
		Description:       "body",
		Priority:          PriorityMedium,
		Type:              TypeIncident,
		RequesterID:       fx.user.ID,
		Source:            "email",
		CreatorEmail:      "alice@test.com",
		ExternalMessageID: "<msg-1@contoso.com>",
	}

	tkt, err := fx.repo.Create(fx.ctx, params, fx.tenant.ID)
	require.NoError(t, err)

	stored, err := fx.client.Ticket.Get(fx.ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@test.com", stored.CreatorEmail)
	assert.Equal(t, "<msg-1@contoso.com>", stored.ExternalMessageID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./repository/ticket/... -run TestRepository_Create_PersistsCreatorEmailAndExternalMessageID -v`
Expected: FAIL — compile error, `CreateParams` has no field `CreatorEmail`/`ExternalMessageID`.

- [ ] **Step 3: Add fields to `CreateParams`**

In `repository/ticket/model.go`, inside the `CreateParams` struct (right before the closing `}` at line 229, after the `Source` field and its comment):

```go
	// CreatorEmail 创建人邮箱：邮件建单时记录原始发件邮箱，便于人工核对
	CreatorEmail string
	// ExternalMessageID 外部消息ID（如邮件 internetMessageId），用于建单去重判断
	ExternalMessageID string
```

- [ ] **Step 4: Persist the fields in `Create()`**

In `repository/ticket/repository_impl.go`, inside `Create()`, right after the existing block:

```go
		if params.Source != "" {
			builder.SetSource(params.Source)
		}
```

add:

```go
		if params.CreatorEmail != "" {
			builder.SetCreatorEmail(params.CreatorEmail)
		}
		if params.ExternalMessageID != "" {
			builder.SetExternalMessageID(params.ExternalMessageID)
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd itsm-backend && go test ./repository/ticket/... -run TestRepository_Create_PersistsCreatorEmailAndExternalMessageID -v`
Expected: PASS

- [ ] **Step 6: Thread the fields from DTO through the service layer**

In `dto/ticket_dto.go`, inside `CreateTicketRequest` (right after the existing `Source` field, line ~23):

```go
	CreatorEmail          string                 `json:"creatorEmail,omitempty"`      // 创建人邮箱（邮件建单等非交互式来源记录原始发件邮箱）
	ExternalMessageID     string                 `json:"externalMessageId,omitempty"` // 外部消息ID（如邮件 internetMessageId），用于建单去重
```

In `service/ticket_service.go`, inside `CreateTicket()`, in the `params := &ticket.CreateParams{...}` literal (line ~144-153), add two lines alongside the existing `Source: req.Source,`:

```go
		Source:            req.Source,
		CreatorEmail:      req.CreatorEmail,
		ExternalMessageID: req.ExternalMessageID,
```

(This is a plain field-literal addition — `gofmt` will realign the `:` column; don't hand-align, just add the lines and run `gofmt -w service/ticket_service.go` in Step 8.)

- [ ] **Step 7: Add a service-level round-trip test**

`service/ticket_service_ext_test.go` already has exactly the fixture needed: `newTicketFixture(t) *ticketFixture` (defined at the top of that file), which builds an in-memory ent client via `NewTicketServiceForTest(client, logger)` (defined in `service/ticket_service.go:96`) plus a tenant/requester/agent. Its `tenant`/`user`/`agent` fields are typed `interface{ GetID() int }` (wrapping a small `entAdapter{id: ...}` — not plain structs), so use `.GetID()`, not `.ID`. Add to `service/ticket_service_ext_test.go`:

```go
func TestTicketService_CreateTicket_PersistsCreatorEmailAndExternalMessageID(t *testing.T) {
	fx := newTicketFixture(t)
	defer fx.client.Close()

	req := &dto.CreateTicketRequest{
		Title:             "From email",
		Description:       "body",
		Priority:          "medium",
		RequesterID:       fx.user.GetID(),
		Source:            "email",
		CreatorEmail:      "alice@test.com",
		ExternalMessageID: "<msg-1@contoso.com>",
	}

	tkt, err := fx.svc.CreateTicket(fx.ctx, req, fx.tenant.GetID())
	require.NoError(t, err)

	stored, err := fx.client.Ticket.Get(fx.ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@test.com", stored.CreatorEmail)
	assert.Equal(t, "<msg-1@contoso.com>", stored.ExternalMessageID)
}
```

- [ ] **Step 8: Format and run tests**

Run: `cd itsm-backend && gofmt -w service/ticket_service.go dto/ticket_dto.go repository/ticket/model.go repository/ticket/repository_impl.go && go test ./repository/ticket/... ./service/... -run "CreatorEmail|ExternalMessageID" -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
cd itsm-backend
git add dto/ticket_dto.go repository/ticket/model.go repository/ticket/repository_impl.go service/ticket_service.go repository/ticket/repository_test.go service/ticket_service_ext_test.go
git commit -m "feat(ticket): thread creatorEmail/externalMessageId from request to persistence"
```

---

### Task 3: MS Graph HTTP client

**Files:**
- Create: `connector/builtin/msgraph/client.go`
- Test: `connector/builtin/msgraph/client_test.go`

**Interfaces:**
- Produces: `msgraph.NewClient(tenantID, clientID, clientSecret, aadBaseURL, graphBaseURL string) *Client`, `(*Client).Token(ctx) (string, error)`, `(*Client).PollDelta(ctx, mailbox, deltaLink string) (messages []Message, nextDeltaLink string, err error)`, `(*Client).SendMail(ctx, mailbox, toAddress, subject, body string) error`, `msgraph.Message{ID, InternetMessageID, Subject, BodyContentType, BodyContent, FromAddress string; ReceivedDateTime time.Time}`. Consumed by Task 4 (`connector.go`) and Task 5 (`coordinator.go`, via the connector's exposed client).

- [ ] **Step 1: Write the failing test for token acquisition and caching**

Create `connector/builtin/msgraph/client_test.go`:

```go
package msgraph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Token_FetchesAndCaches(t *testing.T) {
	calls := 0
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/test-tenant/oauth2/v2.0/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.FormValue("grant_type"))
		assert.Equal(t, "https://graph.microsoft.com/.default", r.FormValue("scope"))
		assert.Equal(t, "test-client-id", r.FormValue("client_id"))
		assert.Equal(t, "test-secret", r.FormValue("client_secret"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok-123",
			"expires_in":   3599,
			"token_type":   "Bearer",
		})
	}))
	defer aad.Close()

	c := NewClient("test-tenant", "test-client-id", "test-secret", aad.URL, "")
	ctx := context.Background()

	tok, err := c.Token(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tok-123", tok)
	assert.Equal(t, 1, calls)

	tok2, err := c.Token(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tok-123", tok2)
	assert.Equal(t, 1, calls, "second call within TTL must not hit the network")
}

func TestClient_Token_ErrorResponse(t *testing.T) {
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "invalid_client",
			"error_description": "AADSTS7000215: Invalid client secret",
		})
	}))
	defer aad.Close()

	c := NewClient("test-tenant", "bad-id", "bad-secret", aad.URL, "")
	_, err := c.Token(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_client")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: FAIL — package `msgraph` / `NewClient` doesn't exist yet.

- [ ] **Step 3: Implement the client's token handling**

Create `connector/builtin/msgraph/client.go`:

```go
// Package msgraph implements an ITSM connector backed by Microsoft Graph API
// (app-only OAuth2 client-credentials flow) for a shared mailbox: polling
// inbound mail via delta queries and sending replies via sendMail.
//
// Docs:
//   - App-only auth: https://learn.microsoft.com/en-us/graph/auth-v2-service
//   - Delta query on mail: https://learn.microsoft.com/en-us/graph/api/message-delta
//   - Send mail: https://learn.microsoft.com/en-us/graph/api/user-sendmail
package msgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	DefaultAADBaseURL   = "https://login.microsoftonline.com"
	DefaultGraphBaseURL = "https://graph.microsoft.com/v1.0"
)

// Client is an MS Graph HTTP client scoped to a single Azure AD app
// registration (app-only / client_credentials).
type Client struct {
	aadBaseURL   string
	graphBaseURL string
	tenantID     string
	clientID     string
	clientSecret string
	logger       *zap.SugaredLogger
	hc           *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

// NewClient constructs a Graph client. aadBaseURL/graphBaseURL may be empty
// to use the real Microsoft endpoints — non-empty values are for tests
// (httptest.Server URLs).
func NewClient(tenantID, clientID, clientSecret, aadBaseURL, graphBaseURL string) *Client {
	if aadBaseURL == "" {
		aadBaseURL = DefaultAADBaseURL
	}
	if graphBaseURL == "" {
		graphBaseURL = DefaultGraphBaseURL
	}
	return &Client{
		aadBaseURL:   aadBaseURL,
		graphBaseURL: graphBaseURL,
		tenantID:     tenantID,
		clientID:     clientID,
		clientSecret: clientSecret,
		logger:       zap.S().Named("connector.msgraph"),
		hc:           &http.Client{Timeout: 15 * time.Second},
	}
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// Token returns a cached access token, refreshing it if it's missing or
// within 2 minutes of expiry.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.exp) > 2*time.Minute {
		return c.token, nil
	}

	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
		"grant_type":    {"client_credentials"},
	}
	tokenURL := fmt.Sprintf("%s/%s/oauth2/v2.0/token", c.aadBaseURL, c.tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("msgraph: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("msgraph: token request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out tokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("msgraph: decode token response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("msgraph: token error: %s - %s", out.Error, out.ErrorDesc)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("msgraph: empty access token in response (status %d)", resp.StatusCode)
	}

	c.token = out.AccessToken
	c.exp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return c.token, nil
}

// getJSON issues an authenticated GET against an absolute Graph URL.
func (c *Client) getJSON(ctx context.Context, absoluteURL string, out interface{}) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absoluteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msgraph: GET %s: %w", absoluteURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("msgraph: GET %s: status %d: %s", absoluteURL, resp.StatusCode, string(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("msgraph: decode response from %s: %w", absoluteURL, err)
		}
	}
	return nil
}

// postJSON issues an authenticated POST with a JSON body against a
// Graph-relative path (appended to graphBaseURL).
func (c *Client) postJSON(ctx context.Context, path string, payload interface{}) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("msgraph: encode request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msgraph: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("msgraph: POST %s: status %d: %s", path, resp.StatusCode, string(raw))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -run TestClient_Token -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for delta polling (with pagination) and sendMail**

Append to `connector/builtin/msgraph/client_test.go`:

```go
func tokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok-123",
			"expires_in":   3599,
		})
	}
}

func TestClient_PollDelta_FirstCallNoLink(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/users/support@contoso.com/mailFolders('inbox')/messages/delta", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-1",
			"value": []map[string]interface{}{
				{
					"id":                "msg-1",
					"internetMessageId": "<abc@contoso.com>",
					"subject":           "Help needed",
					"receivedDateTime":  "2026-08-11T10:00:00Z",
					"from": map[string]interface{}{
						"emailAddress": map[string]interface{}{"address": "Alice@Contoso.com"},
					},
					"body": map[string]interface{}{
						"contentType": "text",
						"content":     "Something is broken.",
					},
				},
			},
		})
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	msgs, next, err := c.PollDelta(context.Background(), "support@contoso.com", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "<abc@contoso.com>", msgs[0].InternetMessageID)
	assert.Equal(t, "Help needed", msgs[0].Subject)
	assert.Equal(t, "alice@contoso.com", msgs[0].FromAddress, "from address must be lowercased")
	assert.Equal(t, "Something is broken.", msgs[0].BodyContent)
	assert.Equal(t, "https://graph.example/delta-link-1", next)
}

func TestClient_PollDelta_FollowsNextLinkUntilDeltaLink(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	pageTwoURL := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/users/support@contoso.com/mailFolders('inbox')/messages/delta", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.nextLink": pageTwoURL,
			"value": []map[string]interface{}{
				{"id": "msg-1", "internetMessageId": "<p1@contoso.com>", "subject": "Page 1",
					"from": map[string]interface{}{"emailAddress": map[string]interface{}{"address": "a@contoso.com"}},
					"body": map[string]interface{}{"contentType": "text", "content": "p1"}},
			},
		})
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-final",
			"value": []map[string]interface{}{
				{"id": "msg-2", "internetMessageId": "<p2@contoso.com>", "subject": "Page 2",
					"from": map[string]interface{}{"emailAddress": map[string]interface{}{"address": "b@contoso.com"}},
					"body": map[string]interface{}{"contentType": "text", "content": "p2"}},
			},
		})
	})
	graph := httptest.NewServer(mux)
	defer graph.Close()
	pageTwoURL = graph.URL + "/page2"

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	msgs, next, err := c.PollDelta(context.Background(), "support@contoso.com", "")
	require.NoError(t, err)
	require.Len(t, msgs, 2, "must accumulate messages across pages")
	assert.Equal(t, "<p1@contoso.com>", msgs[0].InternetMessageID)
	assert.Equal(t, "<p2@contoso.com>", msgs[1].InternetMessageID)
	assert.Equal(t, "https://graph.example/delta-link-final", next)
}

func TestClient_SendMail(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	var captured map[string]interface{}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/users/support@contoso.com/sendMail", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	err := c.SendMail(context.Background(), "support@contoso.com", "alice@contoso.com", "Re: Help", "We got it, ticket #123")
	require.NoError(t, err)

	message := captured["message"].(map[string]interface{})
	assert.Equal(t, "Re: Help", message["subject"])
	recipients := message["toRecipients"].([]interface{})
	require.Len(t, recipients, 1)
	addr := recipients[0].(map[string]interface{})["emailAddress"].(map[string]interface{})["address"]
	assert.Equal(t, "alice@contoso.com", addr)
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: FAIL — `PollDelta`/`SendMail`/`Message` type don't exist yet.

- [ ] **Step 7: Implement delta polling and sendMail**

Append to `connector/builtin/msgraph/client.go`:

```go
// Message is a parsed inbound email, ready for ticket creation.
type Message struct {
	ID                string
	InternetMessageID string
	Subject           string
	BodyContentType   string
	BodyContent       string
	FromAddress       string
	ReceivedDateTime  time.Time
}

type deltaMessage struct {
	ID                string    `json:"id"`
	InternetMessageID string    `json:"internetMessageId"`
	Subject           string    `json:"subject"`
	ReceivedDateTime  time.Time `json:"receivedDateTime"`
	From              struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

type deltaResponse struct {
	NextLink  string         `json:"@odata.nextLink"`
	DeltaLink string         `json:"@odata.deltaLink"`
	Value     []deltaMessage `json:"value"`
}

// PollDelta fetches new/changed messages in the mailbox's inbox since the
// given deltaLink (pass "" for the very first call, which returns a full
// snapshot of current unprocessed state per Graph's delta semantics).
// It follows @odata.nextLink pages until it reaches @odata.deltaLink, and
// returns the accumulated messages plus the new deltaLink to persist for
// the next poll.
func (c *Client) PollDelta(ctx context.Context, mailbox, deltaLink string) ([]Message, string, error) {
	link := deltaLink
	if link == "" {
		link = fmt.Sprintf("%s/users/%s/mailFolders('inbox')/messages/delta", c.graphBaseURL, url.PathEscape(mailbox))
	}

	var messages []Message
	for {
		var resp deltaResponse
		if err := c.getJSON(ctx, link, &resp); err != nil {
			return nil, "", err
		}
		for _, v := range resp.Value {
			messages = append(messages, Message{
				ID:                v.ID,
				InternetMessageID: v.InternetMessageID,
				Subject:           v.Subject,
				BodyContentType:   v.Body.ContentType,
				BodyContent:       v.Body.Content,
				FromAddress:       strings.ToLower(v.From.EmailAddress.Address),
				ReceivedDateTime:  v.ReceivedDateTime,
			})
		}
		if resp.DeltaLink != "" {
			return messages, resp.DeltaLink, nil
		}
		if resp.NextLink == "" {
			return messages, "", fmt.Errorf("msgraph: delta response missing both nextLink and deltaLink")
		}
		link = resp.NextLink
	}
}

// SendMail sends a plain-text email from the shared mailbox.
func (c *Client) SendMail(ctx context.Context, mailbox, toAddress, subject, body string) error {
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "Text",
				"content":     body,
			},
			"toRecipients": []map[string]interface{}{
				{"emailAddress": map[string]string{"address": toAddress}},
			},
		},
		"saveToSentItems": "false",
	}
	path := fmt.Sprintf("/users/%s/sendMail", url.PathEscape(mailbox))
	return c.postJSON(ctx, path, payload)
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: PASS (all `TestClient_*` tests)

- [ ] **Step 9: Commit**

```bash
cd itsm-backend
git add connector/builtin/msgraph/client.go connector/builtin/msgraph/client_test.go
git commit -m "feat(connector): add MS Graph HTTP client (app-only auth, delta polling, sendMail)"
```

---

### Task 4: MS Graph connector type

**Files:**
- Create: `connector/builtin/msgraph/connector.go`
- Test: `connector/builtin/msgraph/connector_test.go`

**Interfaces:**
- Consumes: `msgraph.NewClient` from Task 3.
- Produces: `msgraph.New() *GraphConnector`, `(*GraphConnector)` implements `connector.Connector`, `(*GraphConnector).Mailbox() string`, `(*GraphConnector).GraphClient() *Client`. Registered under manifest name `"msgraph-email"`. Consumed by Task 5 (coordinator uses `Mailbox()`/`GraphClient()`) and Task 7 (controller type-asserts `connector.Connector` to `*msgraph.GraphConnector`).

- [ ] **Step 1: Write the failing test**

Create `connector/builtin/msgraph/connector_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -run TestGraphConnector -v`
Expected: FAIL — `New`, `GraphConnector` undefined.

- [ ] **Step 3: Implement the connector**

Create `connector/builtin/msgraph/connector.go`:

```go
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
	client  *Client
	mailbox string
	cfg     connector.Config
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
	}
}

func (g *GraphConnector) Init(_ context.Context, cfg connector.Config) error {
	tenantID, _ := cfg.Settings["azure_tenant_id"].(string)
	mailbox, _ := cfg.Settings["mailbox"].(string)
	clientID := cfg.Credentials["azure_client_id"]
	clientSecret := cfg.Credentials["azure_client_secret"]
	if tenantID == "" || mailbox == "" || clientID == "" || clientSecret == "" {
		return fmt.Errorf("msgraph: settings.azure_tenant_id, settings.mailbox, credentials.azure_client_id and credentials.azure_client_secret are required")
	}
	aadBaseURL, _ := cfg.Settings["aad_base_url"].(string)
	graphBaseURL, _ := cfg.Settings["graph_base_url"].(string)
	g.client = NewClient(tenantID, clientID, clientSecret, aadBaseURL, graphBaseURL)
	g.mailbox = mailbox
	g.cfg = cfg
	return nil
}

// Send delivers msg.Content as a plain-text email to msg.Channel (the
// recipient address), from the configured shared mailbox.
func (g *GraphConnector) Send(ctx context.Context, msg *connector.Message) error {
	if g.client == nil {
		return fmt.Errorf("msgraph: connector not initialized")
	}
	return g.client.SendMail(ctx, g.mailbox, msg.Channel, msg.Title, msg.Content)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: PASS (all tests in the package, including Task 3's)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add connector/builtin/msgraph/connector.go connector/builtin/msgraph/connector_test.go
git commit -m "feat(connector): register msgraph-email connector (manifest, init, send, health)"
```

---

### Task 5: Reply template + body cleaning (ported from old email connector)

**Files:**
- Create: `connector/builtin/msgraph/reply_template.go`
- Test: `connector/builtin/msgraph/reply_template_test.go`

**Interfaces:**
- Produces: `msgraph.cleanEmailBody(body string) string`, `msgraph.renderReplyTemplate(ticketNumber, title, status string) string`. Consumed by Task 6 (`coordinator.go`).

- [ ] **Step 1: Write the failing test**

Create `connector/builtin/msgraph/reply_template_test.go`:

```go
package msgraph

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanEmailBody_StripsQuotedRepliesAndSignature(t *testing.T) {
	body := "Please help, my laptop won't boot.\n" +
		"> On Mon, someone wrote:\n" +
		"> previous message\n" +
		"--\n" +
		"Sent from my iPhone"
	got := cleanEmailBody(body)
	assert.Equal(t, "Please help, my laptop won't boot.", got)
}

func TestCleanEmailBody_StripsForwardedHeader(t *testing.T) {
	body := "See below.\n发件人: someone@example.com\n原始邮件内容"
	got := cleanEmailBody(body)
	assert.Equal(t, "See below.", got)
}

func TestRenderReplyTemplate_IncludesTicketNumberAndTitle(t *testing.T) {
	got := renderReplyTemplate("TCK-0001", "打印机无法使用", "新建")
	assert.True(t, strings.Contains(got, "TCK-0001"))
	assert.True(t, strings.Contains(got, "打印机无法使用"))
	assert.True(t, strings.Contains(got, "新建"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -run "TestCleanEmailBody|TestRenderReplyTemplate" -v`
Expected: FAIL — functions undefined.

- [ ] **Step 3: Implement (ported from `connector/builtin/email/service.go` and `email.go`, adapted)**

Create `connector/builtin/msgraph/reply_template.go`:

```go
package msgraph

import (
	"fmt"
	"strings"
)

// cleanEmailBody strips quoted replies and trailing signatures from an
// inbound email body before it becomes a ticket description. Ported from
// connector/builtin/email/service.go's cleanEmailBody (same logic, kept
// package-local per the design's decision not to import the old package).
func cleanEmailBody(body string) string {
	lines := strings.Split(body, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "---") {
			break
		}
		if strings.HasPrefix(trimmed, "发件人:") || strings.HasPrefix(trimmed, "From:") {
			break
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

const replyTemplate = `您好，

您的服务请求已收到，我们会尽快处理。

工单编号：%s
标题：%s
状态：%s

如有疑问，请回复此邮件。

--
KEAS Service Desk (自动回复)
`

// renderReplyTemplate builds the confirmation-reply body sent after a
// ticket is created from an inbound email.
func renderReplyTemplate(ticketNumber, title, status string) string {
	return fmt.Sprintf(replyTemplate, ticketNumber, title, status)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: PASS (all tests in the package so far)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add connector/builtin/msgraph/reply_template.go connector/builtin/msgraph/reply_template_test.go
git commit -m "feat(connector): port email body cleaning and reply template to msgraph package"
```

---

### Task 6: `EmailPollingCoordinator`

**Files:**
- Create: `connector/builtin/msgraph/coordinator.go`
- Test: `connector/builtin/msgraph/coordinator_test.go`

**Interfaces:**
- Consumes: `msgraph.Message`, `(*GraphConnector).Mailbox()`, `(*GraphConnector).GraphClient()`, `(*GraphConnector).PollIntervalSeconds()` (Task 3/4); `cleanEmailBody`, `renderReplyTemplate` (Task 5).
- Produces: `msgraph.InboundTicketRequest`, `msgraph.TriageSuggestion`, `msgraph.Triager` interface, `msgraph.TicketStore` interface, `msgraph.NewEmailPollingCoordinator(store TicketStore, triage Triager, logger *zap.SugaredLogger) *EmailPollingCoordinator`, `(*EmailPollingCoordinator).Start(ctx context.Context, tenantID int, conn *GraphConnector)`, `(*EmailPollingCoordinator).Stop(tenantID int)`. Consumed by Task 7 (controller) and Task 8 (bootstrap, which supplies the concrete `TicketStore`/`Triager` adapters).

- [ ] **Step 1: Write the failing test**

Create `connector/builtin/msgraph/coordinator_test.go`:

```go
package msgraph

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// fakeStore is an in-memory TicketStore for coordinator tests.
type fakeStore struct {
	mu                 sync.Mutex
	usersByEmail       map[string]int // email -> userID (present = found)
	existingExternalID map[string]bool
	created            []InboundTicketRequest
	comments           []string
	nextTicketID       int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByEmail:       map[string]int{},
		existingExternalID: map[string]bool{},
		nextTicketID:       1,
	}
}

func (f *fakeStore) FindActiveUserByEmail(_ context.Context, _ int, email string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.usersByEmail[email]
	return id, ok, nil
}

func (f *fakeStore) TicketExistsForExternalMessage(_ context.Context, _ int, externalMessageID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existingExternalID[externalMessageID], nil
}

func (f *fakeStore) CreateTicket(_ context.Context, _ int, req InboundTicketRequest) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextTicketID
	f.nextTicketID++
	f.created = append(f.created, req)
	f.existingExternalID[req.ExternalMessageID] = true
	return id, fmt.Sprintf("TCK-%04d", id), nil
}

func (f *fakeStore) PostSystemComment(_ context.Context, _, _, _ int, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, content)
	return nil
}

// fakeTriager returns a fixed suggestion.
type fakeTriager struct{ suggestion TriageSuggestion }

func (f fakeTriager) Suggest(_ context.Context, _ int, _, _ string) TriageSuggestion {
	return f.suggestion
}

func newTestGraphServer(t *testing.T, messages []map[string]interface{}) (*httptest.Server, *httptest.Server) {
	t.Helper()
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3599}`))
	}))
	polled := false
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if polled {
			_, _ = w.Write([]byte(`{"@odata.deltaLink":"` + graphDeltaLinkPlaceholder + `","value":[]}`))
			return
		}
		polled = true
		body := map[string]interface{}{
			"@odata.deltaLink": graphDeltaLinkPlaceholder,
			"value":            messages,
		}
		_ = writeJSON(w, body)
	}))
	return aad, graph
}

const graphDeltaLinkPlaceholder = "https://graph.example/delta-link"

func TestCoordinator_HandleMessage_CreatesTicketAndReplies(t *testing.T) {
	store := newFakeStore()
	store.usersByEmail["alice@contoso.com"] = 42
	triager := fakeTriager{suggestion: TriageSuggestion{Category: "network", Priority: "high", Confidence: 0.8, Explanation: "mentions VPN"}}

	var sentMail map[string]interface{}
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3599}`))
	}))
	defer aad.Close()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = decodeJSON(r, &sentMail)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer graph.Close()

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", aad.URL, graph.URL)))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		ID:                "m1",
		InternetMessageID: "<abc@contoso.com>",
		Subject:            "VPN broken",
		BodyContent:        "Cannot connect to VPN.",
		FromAddress:         "alice@contoso.com",
	})

	require.Len(t, store.created, 1)
	assert.Equal(t, "VPN broken", store.created[0].Title)
	assert.Equal(t, "Cannot connect to VPN.", store.created[0].Description)
	assert.Equal(t, "high", store.created[0].Priority, "AI priority must be auto-applied")
	assert.Equal(t, 42, store.created[0].RequesterID)
	assert.Equal(t, "email", store.created[0].Source)
	assert.Equal(t, "<abc@contoso.com>", store.created[0].ExternalMessageID)
	assert.Equal(t, "alice@contoso.com", store.created[0].CreatorEmail)

	require.Len(t, store.comments, 1)
	assert.Contains(t, store.comments[0], "network", "category must be recorded as advisory, not applied to the ticket")
	assert.Contains(t, store.comments[0], "80%")

	require.NotNil(t, sentMail, "confirmation reply must be sent")
	message := sentMail["message"].(map[string]interface{})
	assert.Contains(t, message["subject"], "TCK-0001")
}

func TestCoordinator_HandleMessage_SkipsDuplicateExternalMessageID(t *testing.T) {
	store := newFakeStore()
	store.usersByEmail["alice@contoso.com"] = 42
	store.existingExternalID["<abc@contoso.com>"] = true
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", "http://unused", "http://unused")))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		InternetMessageID: "<abc@contoso.com>",
		Subject:           "Duplicate",
		FromAddress:       "alice@contoso.com",
	})

	assert.Empty(t, store.created, "must not create a second ticket for an already-processed message")
}

func TestCoordinator_HandleMessage_SkipsUnregisteredSender(t *testing.T) {
	store := newFakeStore() // no users registered
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", "http://unused", "http://unused")))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		InternetMessageID: "<xyz@contoso.com>",
		Subject:           "From nobody",
		FromAddress:       "unknown@contoso.com",
	})

	assert.Empty(t, store.created, "must not create a ticket for a sender not found in this tenant")
}

func TestCoordinator_StartStop_CancelsPolling(t *testing.T) {
	store := newFakeStore()
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}
	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())

	aad, graph := newTestGraphServer(t, nil)
	defer aad.Close()
	defer graph.Close()

	conn := New()
	cfg := connectorConfigFor("support@contoso.com", aad.URL, graph.URL)
	cfg.Settings["poll_interval_seconds"] = float64(60)
	require.NoError(t, conn.Init(context.Background(), cfg))

	coord.Start(context.Background(), 7, conn)
	time.Sleep(50 * time.Millisecond) // let the immediate first poll run
	coord.Stop(7)

	coord.mu.Lock()
	_, stillRunning := coord.cancels[7]
	coord.mu.Unlock()
	assert.False(t, stillRunning)
}
```

Note: `writeJSON`/`decodeJSON`/`connectorConfigFor` are small test-only helpers — add them at the bottom of the same file:

```go
func writeJSON(w http.ResponseWriter, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func connectorConfigFor(mailbox, aadURL, graphURL string) connector.Config {
	return connector.Config{
		Settings: map[string]interface{}{
			"azure_tenant_id": "test-tenant",
			"mailbox":         mailbox,
			"aad_base_url":    aadURL,
			"graph_base_url":  graphURL,
		},
		Credentials: map[string]string{
			"azure_client_id":     "id",
			"azure_client_secret": "secret",
		},
	}
}
```

And add the two extra imports (`encoding/json`, `itsm-backend/connector`) to the test file's import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -run TestCoordinator -v`
Expected: FAIL — `InboundTicketRequest`, `TriageSuggestion`, `TicketStore`, `Triager`, `NewEmailPollingCoordinator` undefined.

- [ ] **Step 3: Implement the coordinator**

Create `connector/builtin/msgraph/coordinator.go`:

```go
package msgraph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// InboundTicketRequest is the minimal ticket-creation request the
// coordinator issues. Kept local (not itsm-backend/dto) so this package
// has zero dependency on ent/dto/service — see the design doc's decision
// to keep the connector package testable without a database.
type InboundTicketRequest struct {
	Title             string
	Description       string
	Priority          string // one of: low, medium, high, critical
	RequesterID       int
	CreatorEmail      string
	Source            string
	ExternalMessageID string
}

// TriageSuggestion is the AI classification result the coordinator applies
// (Priority) or records as advisory (Category) — see design decision 6.
type TriageSuggestion struct {
	Category    string
	Priority    string
	Confidence  float64
	Explanation string
}

// Triager produces a triage suggestion for a new inbound ticket. Backed by
// service.TriageService in production (adapter in
// internal/bootstrap/email_msgraph_wiring.go), stubbed in tests.
type Triager interface {
	Suggest(ctx context.Context, tenantID int, title, description string) TriageSuggestion
}

// TicketStore is everything the coordinator needs from the ticket system.
// Backed by *ent.Client + *service.TicketService in production (adapter in
// internal/bootstrap/email_msgraph_wiring.go), stubbed in tests.
type TicketStore interface {
	FindActiveUserByEmail(ctx context.Context, tenantID int, email string) (userID int, found bool, err error)
	TicketExistsForExternalMessage(ctx context.Context, tenantID int, externalMessageID string) (bool, error)
	CreateTicket(ctx context.Context, tenantID int, req InboundTicketRequest) (ticketID int, ticketNumber string, err error)
	PostSystemComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error
}

// EmailPollingCoordinator polls one MS Graph mailbox per tenant (one
// goroutine per tenant, keyed by tenantID) and turns new inbound mail into
// tickets.
type EmailPollingCoordinator struct {
	store  TicketStore
	triage Triager
	logger *zap.SugaredLogger

	mu      sync.Mutex
	cancels map[int]context.CancelFunc // key: tenantID
}

func NewEmailPollingCoordinator(store TicketStore, triage Triager, logger *zap.SugaredLogger) *EmailPollingCoordinator {
	return &EmailPollingCoordinator{
		store:   store,
		triage:  triage,
		logger:  logger,
		cancels: make(map[int]context.CancelFunc),
	}
}

// Start begins polling for the given tenant using the given connector
// instance. If polling is already running for this tenant, it is stopped
// first (handles "admin changed the config" without leaking goroutines).
func (c *EmailPollingCoordinator) Start(ctx context.Context, tenantID int, conn *GraphConnector) {
	c.Stop(tenantID)

	pollCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancels[tenantID] = cancel
	c.mu.Unlock()

	interval := time.Duration(conn.PollIntervalSeconds()) * time.Second

	go func() {
		deltaLink := ""
		c.pollOnce(pollCtx, tenantID, conn, &deltaLink)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				c.pollOnce(pollCtx, tenantID, conn, &deltaLink)
			}
		}
	}()
}

// Stop cancels polling for a tenant, if running. Safe to call when nothing
// is running for that tenant.
func (c *EmailPollingCoordinator) Stop(tenantID int) {
	c.mu.Lock()
	cancel, ok := c.cancels[tenantID]
	delete(c.cancels, tenantID)
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

func (c *EmailPollingCoordinator) pollOnce(ctx context.Context, tenantID int, conn *GraphConnector, deltaLink *string) {
	messages, next, err := conn.GraphClient().PollDelta(ctx, conn.Mailbox(), *deltaLink)
	if err != nil {
		c.logger.Warnw("msgraph poll failed", "tenant_id", tenantID, "error", err)
		return
	}
	if next != "" {
		*deltaLink = next
	}
	for _, m := range messages {
		c.handleMessage(ctx, tenantID, conn, m)
	}
}

func (c *EmailPollingCoordinator) handleMessage(ctx context.Context, tenantID int, conn *GraphConnector, m Message) {
	if m.InternetMessageID == "" {
		c.logger.Warnw("msgraph message missing internetMessageId, skipping", "tenant_id", tenantID, "graph_id", m.ID)
		return
	}

	exists, err := c.store.TicketExistsForExternalMessage(ctx, tenantID, m.InternetMessageID)
	if err != nil {
		c.logger.Errorw("msgraph dedup check failed", "tenant_id", tenantID, "error", err)
		return
	}
	if exists {
		return
	}

	userID, found, err := c.store.FindActiveUserByEmail(ctx, tenantID, m.FromAddress)
	if err != nil {
		c.logger.Errorw("msgraph requester lookup failed", "tenant_id", tenantID, "from", m.FromAddress, "error", err)
		return
	}
	if !found {
		c.logger.Warnw("msgraph inbound email from unregistered sender, skipping", "tenant_id", tenantID, "from", m.FromAddress)
		return
	}

	subject := m.Subject
	if subject == "" {
		subject = fmt.Sprintf("邮件工单 - %s - %s", m.FromAddress, m.ReceivedDateTime.Format("2006-01-02 15:04"))
	}
	body := cleanEmailBody(m.BodyContent)
	if body == "" {
		body = "(无正文)"
	}

	suggestion := c.triage.Suggest(ctx, tenantID, subject, body)
	priority := suggestion.Priority
	if priority == "" {
		priority = "medium"
	}

	ticketID, ticketNumber, err := c.store.CreateTicket(ctx, tenantID, InboundTicketRequest{
		Title:             subject,
		Description:       body,
		Priority:          priority,
		RequesterID:       userID,
		CreatorEmail:      m.FromAddress,
		Source:            "email",
		ExternalMessageID: m.InternetMessageID,
	})
	if err != nil {
		c.logger.Errorw("msgraph create ticket failed", "tenant_id", tenantID, "from", m.FromAddress, "error", err)
		return
	}
	c.logger.Infow("msgraph ticket created", "tenant_id", tenantID, "ticket_id", ticketID, "ticket_number", ticketNumber, "from", m.FromAddress)

	comment := fmt.Sprintf(
		"AI 分派参考：建议分类=%s（未自动应用，请人工确认），优先级=%s（已应用），置信度=%.0f%%，理由=%s",
		suggestion.Category, priority, suggestion.Confidence*100, suggestion.Explanation,
	)
	if err := c.store.PostSystemComment(ctx, tenantID, ticketID, userID, comment); err != nil {
		c.logger.Warnw("msgraph failed to post triage comment", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}

	replyBody := renderReplyTemplate(ticketNumber, subject, "新建")
	replySubject := fmt.Sprintf("Re: [%s] %s", ticketNumber, subject)
	if err := conn.GraphClient().SendMail(ctx, conn.Mailbox(), m.FromAddress, replySubject, replyBody); err != nil {
		c.logger.Warnw("msgraph failed to send confirmation reply", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./connector/builtin/msgraph/... -v`
Expected: PASS (every test in the package)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add connector/builtin/msgraph/coordinator.go connector/builtin/msgraph/coordinator_test.go
git commit -m "feat(connector): add EmailPollingCoordinator (dedupe, triage-priority-only, create, comment, reply)"
```

---

### Task 7: Wire `ConnectorController` to the coordinator

**Files:**
- Modify: `controller/connector_controller.go:1-39` (imports, struct, constructor), `:101-154` (`Provision`, `Revoke`)
- Test: `controller/connector_controller_test.go` (add test functions)

**Interfaces:**
- Consumes: `*msgraph.GraphConnector`, `*msgraph.EmailPollingCoordinator` (Tasks 4/6) — only the method signatures `Start(ctx, tenantID int, conn *msgraph.GraphConnector)` / `Stop(tenantID int)`, via a small unexported interface so tests can substitute a fake.
- Produces: `(*ConnectorController).SetEmailCoordinator(coord emailPollingCoordinator)`. Consumed by Task 8's bootstrap wiring.

- [ ] **Step 1: Write the failing test**

Add to `controller/connector_controller_test.go` (same file, so it can reuse `setupConnectorController`'s style, `withTestAuth`, `doReq` from `release_controller_test.go`):

```go
type fakeEmailCoordinator struct {
	mu       sync.Mutex
	started  []int // tenantIDs Start was called for
	stopped  []int // tenantIDs Stop was called for
}

func (f *fakeEmailCoordinator) Start(_ context.Context, tenantID int, _ *msgraphpkg.GraphConnector) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, tenantID)
}

func (f *fakeEmailCoordinator) Stop(tenantID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, tenantID)
}

func TestConnectorController_Provision_StartsEmailCoordinatorForMsgraphEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t).Sugar()
	reg := connector.NewRegistry()
	// msgraph-email only auto-registers into connector.Default() via its
	// package init(); this test uses an isolated registry (matching this
	// file's existing pattern of not depending on global registration
	// order), so it must register the factory explicitly.
	reg.Register(func() connector.Connector { return msgraphpkg.New() })
	mgr := connector.NewManager(reg, logger)
	mkt := marketplace.New()
	ctrl := NewConnectorController(mgr, reg, mkt, logger)
	fake := &fakeEmailCoordinator{}
	ctrl.SetEmailCoordinator(fake)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(withTestAuth(9, 1))
	r.POST("/api/v1/connectors/configs", ctrl.Provision)

	body := dto.ProvisionConnectorRequest{
		Name:     "msgraph-email",
		Provider: "microsoft",
		Enabled:  true,
		Settings: map[string]interface{}{
			"azure_tenant_id": "t",
			"mailbox":         "support@contoso.com",
		},
		Credentials: map[string]string{
			"azure_client_id":     "id",
			"azure_client_secret": "secret",
		},
	}
	resp := doReq(t, r, "POST", "/api/v1/connectors/configs", body, false)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, []int{9}, fake.started, "Start must be called for the provisioning tenant")
	assert.Empty(t, fake.stopped)
}

func TestConnectorController_Provision_IgnoresOtherConnectors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t).Sugar()
	reg := connector.NewRegistry()
	mgr := connector.NewManager(reg, logger)
	mkt := marketplace.New()
	ctrl := NewConnectorController(mgr, reg, mkt, logger)
	fake := &fakeEmailCoordinator{}
	ctrl.SetEmailCoordinator(fake)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(withTestAuth(9, 1))
	r.POST("/api/v1/connectors/configs", ctrl.Provision)

	// This registry has nothing registered at all, so provisioning any name
	// must fail cleanly (connector not registered) without touching the
	// coordinator — which is exactly what we're asserting: the coordinator
	// hook only fires for req.Name == "msgraph-email", never as a side
	// effect of Provision failing.
	body := dto.ProvisionConnectorRequest{Name: "not-msgraph", Provider: "x", Enabled: true}
	_ = doReq(t, r, "POST", "/api/v1/connectors/configs", body, false)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Empty(t, fake.started)
	assert.Empty(t, fake.stopped)
}

func TestConnectorController_Revoke_StopsEmailCoordinator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t).Sugar()
	reg := connector.NewRegistry()
	mgr := connector.NewManager(reg, logger)
	mkt := marketplace.New()
	ctrl := NewConnectorController(mgr, reg, mkt, logger)
	fake := &fakeEmailCoordinator{}
	ctrl.SetEmailCoordinator(fake)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(withTestAuth(9, 1))
	r.DELETE("/api/v1/connectors/configs/:name", ctrl.Revoke)

	resp := doReq(t, r, "DELETE", "/api/v1/connectors/configs/msgraph-email", nil, false)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, []int{9}, fake.stopped)
}
```

Add `"sync"`, `"itsm-backend/dto"` (if not already imported — check first), and a package-qualified import for the connector type:

```go
msgraphpkg "itsm-backend/connector/builtin/msgraph"
```

to `connector_controller_test.go`'s import block. (`dto`, `context`, `gin`, `zaptest`, `require`, `assert` are already imported per the existing file shown above — only add what's missing.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./controller/... -run TestConnectorController_Provision_StartsEmailCoordinatorForMsgraphEmail -v`
Expected: FAIL — `SetEmailCoordinator` undefined, and `TestGraphConnector_Init_Success`-style connector.Config for `msgraph-email` will fail with "connector not registered" until the blank import exists in the test binary. Since `connector/builtin/msgraph` is imported (as `msgraphpkg`) by the test file itself, its `init()` runs and registers `msgraph-email` automatically for this test binary — no separate blank import needed here.

- [ ] **Step 3: Add the optional dependency and wiring**

In `controller/connector_controller.go`, add the import (alongside the existing `"itsm-backend/connector/marketplace"` import at line 12):

```go
	msgraphpkg "itsm-backend/connector/builtin/msgraph"
```

Add this unexported interface right before the `ConnectorController` struct (line 30):

```go
// emailPollingCoordinator is the subset of *msgraph.EmailPollingCoordinator
// this controller depends on. Kept as a small interface (rather than the
// concrete type) purely so tests can substitute a fake without spinning up
// real polling goroutines.
type emailPollingCoordinator interface {
	Start(ctx context.Context, tenantID int, conn *msgraphpkg.GraphConnector)
	Stop(tenantID int)
}
```

Add a field to the struct (line 30-35):

```go
type ConnectorController struct {
	manager          *connector.Manager
	market           *marketplace.Market // optional
	registry         *connector.Registry
	logger           *zap.SugaredLogger
	emailCoordinator emailPollingCoordinator // optional; nil unless SetEmailCoordinator is called
}
```

Add the setter right after `NewConnectorController` (after line 39):

```go
// SetEmailCoordinator wires in the MS Graph email polling coordinator.
// Optional — if never called, provisioning "msgraph-email" still succeeds
// (config is stored via Manager like any other connector) but no polling
// starts. Called once from bootstrap after the coordinator's dependencies
// (TicketService, TriageService) are constructed.
func (c *ConnectorController) SetEmailCoordinator(coord emailPollingCoordinator) {
	c.emailCoordinator = coord
}
```

In `Provision` (currently lines 101-146), insert this block right after the existing `if err := c.manager.Provision(...)` check and before `common.Success(...)` (i.e., after line 144's closing `}`):

```go
	if req.Name == "msgraph-email" && c.emailCoordinator != nil {
		if !cfg.Enabled {
			c.emailCoordinator.Stop(tenantID)
		} else if conn, ok := c.manager.Get(tenantID, "msgraph-email"); ok {
			if gc, ok := conn.(*msgraphpkg.GraphConnector); ok {
				c.emailCoordinator.Start(ctx.Request.Context(), tenantID, gc)
			}
		}
	}
```

In `Revoke` (currently lines 149-154), insert right after `c.manager.Revoke(...)` and before `common.Success(...)`:

```go
	if name == "msgraph-email" && c.emailCoordinator != nil {
		c.emailCoordinator.Stop(tenantID)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./controller/... -run TestConnectorController -v`
Expected: PASS (all `TestConnectorController_*` tests, old and new)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add controller/connector_controller.go controller/connector_controller_test.go
git commit -m "feat(connector): start/stop msgraph email polling on provision/revoke"
```

---

### Task 8: Bootstrap wiring

**Files:**
- Create: `internal/bootstrap/email_msgraph_wiring.go`
- Modify: `internal/bootstrap/app.go:16-20` (blank imports), `:353` area (after `triageService` construction)
- Test: `internal/bootstrap/email_msgraph_wiring_test.go`

**Interfaces:**
- Consumes: `msgraph.TicketStore`, `msgraph.Triager`, `msgraph.InboundTicketRequest`, `msgraph.TriageSuggestion`, `msgraph.NewEmailPollingCoordinator` (Task 6); `*service.TicketService.CreateTicket`, `*service.TriageService.SuggestForTenant` (existing); `*ent.Client` (existing); `(*ConnectorController).SetEmailCoordinator` (Task 7).
- Produces: `bootstrap.newTicketStoreAdapter(client *ent.Client, ticketService *service.TicketService) msgraph.TicketStore`, `bootstrap.newTriagerAdapter(triageService *service.TriageService) msgraph.Triager`, `bootstrap.wireEmailMsgraphConnector(client *ent.Client, ticketService *service.TicketService, triageService *service.TriageService, connectorController *controller.ConnectorController, logger *zap.SugaredLogger)`. This is the final integration point — nothing downstream consumes it, `app.go` just calls it once.

- [ ] **Step 1: Write the failing test**

Create `internal/bootstrap/email_msgraph_wiring_test.go`:

```go
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
```

(`msgraphInboundTicketRequest` here is a local type alias defined in Step 3 to keep this test file from importing `connector/builtin/msgraph` just for a struct literal — see below.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./internal/bootstrap/... -run "TestTicketStoreAdapter|TestWireEmailMsgraphConnector" -v`
Expected: FAIL — `newTicketStoreAdapter`, `wireEmailMsgraphConnector`, `msgraphInboundTicketRequest` undefined.

- [ ] **Step 3: Implement the adapters and wiring function**

Create `internal/bootstrap/email_msgraph_wiring.go`:

```go
package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/service"
)

// msgraphInboundTicketRequest is a bootstrap-local alias so this package's
// tests can build a fixture without importing connector/builtin/msgraph
// just for a struct literal. It is structurally identical to
// msgraph.InboundTicketRequest by construction (see ticketStoreAdapter.CreateTicket).
type msgraphInboundTicketRequest = msgraph.InboundTicketRequest

// ticketStoreAdapter implements msgraph.TicketStore over the real ent
// client and TicketService, so connector/builtin/msgraph never needs to
// import ent/dto/service directly.
type ticketStoreAdapter struct {
	client        *ent.Client
	ticketService *service.TicketService
}

func newTicketStoreAdapter(client *ent.Client, ticketService *service.TicketService) *ticketStoreAdapter {
	return &ticketStoreAdapter{client: client, ticketService: ticketService}
}

func (a *ticketStoreAdapter) FindActiveUserByEmail(ctx context.Context, tenantID int, email string) (int, bool, error) {
	u, err := a.client.User.Query().
		Where(user.EmailEqualFold(email), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("lookup user by email: %w", err)
	}
	return u.ID, true, nil
}

func (a *ticketStoreAdapter) TicketExistsForExternalMessage(ctx context.Context, tenantID int, externalMessageID string) (bool, error) {
	return a.client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenantID), ticket.ExternalMessageIDEQ(externalMessageID)).
		Exist(ctx)
}

func (a *ticketStoreAdapter) CreateTicket(ctx context.Context, tenantID int, req msgraph.InboundTicketRequest) (int, string, error) {
	tkt, err := a.ticketService.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		RequesterID:       req.RequesterID,
		CreatorEmail:      req.CreatorEmail,
		Source:            req.Source,
		ExternalMessageID: req.ExternalMessageID,
	}, tenantID)
	if err != nil {
		return 0, "", err
	}
	return tkt.ID, tkt.TicketNumber, nil
}

// PostSystemComment writes an internal ticket comment directly via ent,
// bypassing TicketCommentService's permission gate (which requires the
// authoring user to hold an admin/agent/manager/technician/security role).
// This is a backend-automation write, not a simulated user action — same
// reasoning the BPMN engine and escalation service already use elsewhere
// for system-generated ticket state changes. authorUserID must be an
// existing user in the tenant (the ticket's requester, satisfying the
// comment's required FK) — the content text itself makes clear it's
// system-generated.
func (a *ticketStoreAdapter) PostSystemComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error {
	_, err := a.client.TicketComment.Create().
		SetTicketID(ticketID).
		SetUserID(authorUserID).
		SetContent("[系统 AI 分派] " + content).
		SetIsInternal(true).
		SetTenantID(tenantID).
		Save(ctx)
	return err
}

// triagerAdapter implements msgraph.Triager over the real TriageService.
type triagerAdapter struct {
	triageService *service.TriageService
}

func newTriagerAdapter(triageService *service.TriageService) *triagerAdapter {
	return &triagerAdapter{triageService: triageService}
}

func (a *triagerAdapter) Suggest(ctx context.Context, tenantID int, title, description string) msgraph.TriageSuggestion {
	result := a.triageService.SuggestForTenant(ctx, title, description, tenantID)
	return msgraph.TriageSuggestion{
		Category:    result.Category,
		Priority:    strings.ToLower(result.Priority),
		Confidence:  result.Confidence,
		Explanation: result.Explanation,
	}
}

// wireEmailMsgraphConnector constructs the MS Graph email polling
// coordinator and registers it with the connector controller. Call once
// during bootstrap, after ticketService and triageService are constructed.
// Safe to call even if no msgraph-email connector is ever provisioned by
// any tenant — SetEmailCoordinator just makes the capability available.
func wireEmailMsgraphConnector(
	client *ent.Client,
	ticketService *service.TicketService,
	triageService *service.TriageService,
	connectorController *controller.ConnectorController,
	logger *zap.SugaredLogger,
) {
	store := newTicketStoreAdapter(client, ticketService)
	triager := newTriagerAdapter(triageService)
	coordinator := msgraph.NewEmailPollingCoordinator(store, triager, logger.Named("connector.msgraph.coordinator"))
	connectorController.SetEmailCoordinator(coordinator)
}
```

Remove the placeholder test helper line mentioned in Step 1 (`msgraphInboundTicketRequestFixtureRequesterOnly`) if you pasted it — it was explicitly called out as "don't add"; only `msgraphInboundTicketRequestFixture` should exist in the test file.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./internal/bootstrap/... -run "TestTicketStoreAdapter|TestWireEmailMsgraphConnector" -v`
Expected: PASS

- [ ] **Step 5: Wire it into `app.go`**

In `internal/bootstrap/app.go`, add to the blank-import block (lines 16-20, alongside the existing five):

```go
	_ "itsm-backend/connector/builtin/msgraph"
```

After line 353 (`triageService := service.NewTriageServiceWithGuidanceAndSugaredLogger(llmGateway, guidanceClient, sugar)`), add:

```go
	wireEmailMsgraphConnector(client, ticketService, triageService, connectorController, sugar)
```

(`client`, `ticketService`, and `connectorController` are all already in scope at this point in `NewApplication` — `client` from the top of the function, `ticketService` from line 242, `connectorController` from line 234.)

- [ ] **Step 6: Full build and package tests**

Run: `cd itsm-backend && go build ./... && go test ./internal/bootstrap/... ./connector/... ./controller/... ./repository/ticket/... ./service/... ./ent/... -v`
Expected: everything PASSes, `go build` succeeds.

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add internal/bootstrap/email_msgraph_wiring.go internal/bootstrap/email_msgraph_wiring_test.go internal/bootstrap/app.go
git commit -m "feat(bootstrap): wire msgraph-email connector into ConnectorController"
```

---

### Task 9: Full test suite + manual verification path

**Files:** none (verification-only task)

- [ ] **Step 1: Run the full backend test suite**

Run: `cd itsm-backend && go test ./...`
Expected: all packages PASS. If any pre-existing unrelated test fails, verify via `git stash` + re-run that it fails identically on the pre-change tree before treating it as this plan's responsibility (per CLAUDE.md verification expectations — never assume a failure is pre-existing without checking).

- [ ] **Step 2: Manual smoke check that the connector is discoverable**

Start the backend (`go run main.go`), then as an authenticated admin:

```bash
curl -s http://localhost:8090/api/v1/connectors -b "$ITSM_COOKIE_JAR" | jq '.data.items[] | select(.name=="msgraph-email")'
```

Expected: one item with `name: "msgraph-email"`, `type: "email"`, `installed: false`, `lifecycle: "available"`.

- [ ] **Step 3: Manual smoke check of provisioning (requires a real or test Azure AD app registration)**

If a test Azure AD app + shared mailbox is available, provision via the `/admin/connectors` frontend page (no frontend changes needed — it renders the market/config form generically) with `azure_tenant_id`/`mailbox`/`azure_client_id`/`azure_client_secret`, enable it, and confirm:
1. `GET /api/v1/connectors/health` shows `msgraph-email` as healthy (token acquisition succeeded).
2. Sending a test email from a registered user's address to the mailbox results in a new ticket within one poll interval, with a `[系统 AI 分派]` internal comment and a confirmation reply in the sender's inbox.

If no test Azure AD app is available at implementation time, skip this step and note it explicitly as unverified in the task's completion notes — do not claim it as tested.

---

## Self-Review Notes

- **Spec coverage:** design decisions 1 (app-only auth) → Task 3/4; 2 (delta polling, not webhook) → Task 3; 3 (deltaLink in-memory only) → Task 6 (`deltaLink` is a local variable in the polling goroutine, never persisted); 4 (ConnectorController/Manager path, not marketplace) → Task 7/8; 5 (`external_message_id` + DTO field) → Task 1/2; 6 (Priority auto-applied, Category advisory-only) → Task 6's `handleMessage`, asserted explicitly in `TestCoordinator_HandleMessage_CreatesTicketAndReplies`; 7 (confirmation reply) → Task 6; 8 (new `msgraph` package, old `email` package untouched) → Task 3-6 create a new package, no task touches `connector/builtin/email/`.
- **Placeholder scan:** none of the `- [ ]` steps contain TBD/TODO; the one intentionally-flagged "if no test Azure AD app is available, skip and note" in Task 9 Step 3 is a legitimate conditional manual-verification step, not a placeholder for undefined work.
- **Type consistency check:** `InboundTicketRequest`/`TriageSuggestion` field names and the `TicketStore`/`Triager` method signatures are identical everywhere they appear (Task 6 definition, Task 6 test fakes, Task 8 adapter). `msgraphInboundTicketRequest` (Task 8) is a type alias to `msgraph.InboundTicketRequest`, not a redeclaration, so no drift is possible between them.
- **Scope check:** nine tasks, each independently buildable/testable, matches one design doc. Not split further because every task depends on the previous one's exported names (Task 3→4→5→6→7→8 is a strict dependency chain) — there's no independently-shippable subset smaller than "the whole design," which is what the design doc itself already scoped down to (excluding webhook subscriptions, marketplace-path reconciliation, category-ID mapping, and the separate 邮件通知 task).
