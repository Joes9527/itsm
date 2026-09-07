//go:build integration_postgres

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalogdomain "itsm-backend/handlers/service_catalog"
	requestdomain "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

// Signed exchange, current mapping and the real unified transaction own provenance.
// A dedicated PG schema is created and removed by the existing owned fixture.
func TestPostgresRequestedItemTrustedSourceAndReplay(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	logger := zap.NewNop().Sugar()
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Agent").SaveX(f.ctx)
	for _, resource := range []string{"ticket", "service_request", "service_catalog"} {
		for _, action := range []string{"read", "write"} {
			p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":" + action).SetName(action).SetResource(resource).SetAction(action).SaveX(f.ctx)
			f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(f.ctx)
		}
	}
	mapping := f.client.ExternalIdentity.Create().SetTenantID(f.tenant.ID).SetUserID(f.actor.ID).SetProvider("kaf").SetWorkspace("source-workspace").SetSubject("source-subject").SaveX(f.ctx)
	identity := creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID, Role: "agent", Channel: "http"}
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(requestdomain.NewService(nil, f.client, logger, service.NewApprovalChainResolver(f.client, logger))))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, f.client, logger, nil), service.NewProcessBindingService(f.client), service.NewConfigurationItemService(f.client, logger, nil, nil), service.NewTicketCategoryService(f.client))
	app := intake.NewService(f.client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})
	uf := &unifiedIntakeFixture{client: f.client, app: app, identity: identity, command: creation.CreateWorkItemCommand{Confirmation: "confirmed", Title: "Requested item origin", IdempotencyKey: "trusted-source"}}
	entryDefinition(t, uf, "source-process", f.tenant.ID, "")
	command := entryCatalogCommand(t, uf, "service_request_item", "source-process")
	sessions := authorization.NewSessionReader(f.client, sameTransactionDirectory{})
	repo := intake.NewIdentityRepository(f.client, f.client, sessions)
	nonceServer := miniredis.RunT(t)
	nonceClient := redis.NewClient(&redis.Options{Addr: nonceServer.Addr()})
	t.Cleanup(func() { require.NoError(t, nonceClient.Close()) })
	exchange := intake.NewIdentityExchangeService(intake.IdentityExchangeConfig{Providers: map[string]intake.IdentityProvider{"kaf": {Secret: "test-only-exchange", Channels: []string{"kaf_web"}, Purposes: []string{"create"}}}, MaxAge: time.Minute, FutureSkew: 5 * time.Second, TokenTTL: time.Minute}, intake.NewRedisNonceStore(nonceClient), repo, "test-only-jwt")
	assertion := intake.IdentityAssertion{Version: 2, Audience: "itsm-intake", Purpose: "create", Provider: "kaf", Workspace: "source-workspace", Subject: "source-subject", Channel: "kaf_web", EventID: "source-event", IssuedAt: time.Now().Unix(), Nonce: uuid.NewString()}
	assertion.Signature = signIntakeAssertion(assertion)
	credential, err := exchange.Exchange(f.ctx, assertion, "create")
	require.NoError(t, err)
	require.Equal(t, "intake:create", credential.Scope)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	intake.NewHandler(exchange, app).RegisterRoutes(router.Group("/api/v1"))
	post := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/intake/work-items", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+credential.Token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	raw, err := json.Marshal(command)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	for _, field := range []string{"source", "channel", "requesterId", "actorId"} {
		body[field] = "service_catalog"
		forged, err := json.Marshal(body)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, post(forged).Code, field)
		delete(body, field)
	}
	require.Zero(t, f.client.ServiceRequest.Query().CountX(f.ctx))
	created := post(raw)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		Data creation.CreateWorkItemResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	result := response.Data
	for n := 0; n < 3; n++ {
		replay := post(raw)
		require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
		var decoded struct {
			Data creation.CreateWorkItemResult `json:"data"`
		}
		require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &decoded))
		require.True(t, decoded.Data.Replayed)
		require.Equal(t, result.WorkItemID, decoded.Data.WorkItemID)
	}
	item := f.client.Ticket.GetX(f.ctx, result.WorkItemID)
	require.Equal(t, "kaf_web", item.Source)
	require.Equal(t, f.actor.ID, item.RequesterID)
	require.Equal(t, f.actor.ID, item.OpenedByID)
	require.Equal(t, f.tenant.ID, item.TenantID)
	receipt := f.client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(item.ID)).OnlyX(f.ctx)
	require.Equal(t, "kaf_web", receipt.Channel)
	require.Equal(t, f.actor.ID, receipt.ActorID)
	require.Equal(t, f.actor.ID, receipt.RequesterID)
	snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(item.ID)).OnlyX(f.ctx)
	require.Equal(t, "kaf_web", snapshot.Channel)
	require.Equal(t, "kaf", snapshot.SourceProvider)
	require.Equal(t, 1, f.client.Ticket.Query().Where(ticket.RecordClassEQ("service_request_item")).CountX(f.ctx))
	require.Equal(t, 1, f.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(item.ID)).CountX(f.ctx))
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested")).OnlyX(f.ctx)
	var payload struct {
		ActorID    int `json:"actorId"`
		WorkItemID int `json:"workItemId"`
	}
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	require.Equal(t, f.actor.ID, payload.ActorID)
	require.Equal(t, item.ID, payload.WorkItemID)
	// Native catalog retains its source even with a separate stable key.
	command.IdempotencyKey = "native-source"
	native, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, "service_catalog", f.client.Ticket.GetX(f.ctx, native.WorkItemID).Source)
	require.Equal(t, "http", f.client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(native.WorkItemID)).OnlyX(f.ctx).Channel)
	f.client.ExternalIdentity.UpdateOneID(mapping.ID).SetActive(false).ExecX(f.ctx)
	require.Equal(t, http.StatusUnauthorized, post(raw).Code, "revoked mapping must reject even stable-key replay")
	require.Equal(t, 2, f.client.ServiceRequest.Query().CountX(f.ctx))
}
