package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"

	catalog "itsm-backend/handlers/service_catalog"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityReadCatalogContractCursorAndCurrentWorkItemScope(t *testing.T) {
	f := newResolverFixture(t)
	client := f.client
	ctx := context.Background()
	cfg, a, n := assertionFixture()
	m := client.ExternalIdentity.Create().SetTenantID(f.actor.TenantID).SetUserID(f.actor.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(ctx)
	sessions := authorization.NewSessionReader(client, sameTransactionDirectory{})
	repo := NewIdentityRepository(client, client, sessions)
	ex := NewIdentityExchangeService(cfg.config, n, repo, "test-jwt-key")
	h := NewHandler(ex, f.app)
	owner := catalog.NewService(nil, client, zap.NewNop().Sugar(), sameTransactionDirectory{})
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(&preparedCreator{}))
	owner.SetCreatorRegistry(registry)
	h.SetReaders(NewReadService(sessions, owner, "test-cursor-key"))
	client.ServiceCatalog.UpdateOneID(f.catalog.ID).SetTargetClass("generic").SetRequiresApproval(false).SaveX(ctx)
	client.ProcessBinding.Create().SetTenantID(f.actor.TenantID).SetBusinessType("ticket").SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/v1"))
	token, err := authentication.GenerateIntakeToken(authentication.IntakeClaims{UserID: f.actor.ActorID, TenantID: f.actor.TenantID, Role: f.actor.Role, TokenType: "intake", Scope: []string{"intake:catalog:read", "intake:workitem:read"}, Provider: a.Provider, Channel: a.Channel, EventID: a.EventID, MappingID: m.ID, MappingVersion: m.Version}, "test-jwt-key", time.Minute)
	require.NoError(t, err)
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		return w
	}
	w := call(fmt.Sprintf("/api/v1/intake/catalog-items/%d", f.catalog.ID))
	require.Equal(t, 200, w.Code, w.Body.String())
	var out struct {
		Data map[string]any `json:"data"`
	}
	out.Data = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 7)
	require.NotEmpty(t, out.Data["catalogVersion"])
	require.NotEmpty(t, out.Data["formSchemaVersion"])
	fields := out.Data["fields"].([]any)
	require.Len(t, fields, 1)
	require.Equal(t, false, fields[0].(map[string]any)["readOnly"])
	for index := 0; index < 51; index++ {
		client.ServiceCatalog.Create().SetTenantID(f.actor.TenantID).SetName(fmt.Sprintf("Visible %d", index)).SetTargetClass("generic").SetRequiresApproval(false).SaveX(ctx)
	}
	client.ServiceCatalog.Create().SetTenantID(f.actor.TenantID).SetName("Hidden").SetIsActive(false).SaveX(ctx)
	w = call("/api/v1/intake/catalog-items")
	require.Equal(t, 200, w.Code, w.Body.String())
	out.Data = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data["items"], 50)
	cursor := out.Data["nextCursor"].(string)
	require.Equal(t, 400, call("/api/v1/intake/catalog-items?q=other&cursor="+cursor).Code)
	w = call("/api/v1/intake/catalog-items?cursor=" + cursor)
	require.Equal(t, 200, w.Code, w.Body.String())
	out.Data = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data["items"], 2)
	require.Nil(t, out.Data["nextCursor"])
	item := client.Ticket.Create().SetTenantID(f.actor.TenantID).SetRequesterID(f.actor.ActorID).SetTitle("Own").SetTicketNumber("OWN-1").SetStatus("open").SaveX(ctx)
	w = call(fmt.Sprintf("/api/v1/intake/work-items/%d", item.ID))
	require.Equal(t, 200, w.Code, w.Body.String())
	out.Data = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 7)
	require.Equal(t, "open", out.Data["status"])
	require.Nil(t, out.Data["accessResult"])
	other := client.User.Create().SetTenantID(f.actor.TenantID).SetUsername("other").SetName("Other").SetEmail("other@example.test").SetPasswordHash("test").SaveX(ctx)
	client.Ticket.UpdateOneID(item.ID).SetRequesterID(other.ID).SaveX(ctx)
	require.Equal(t, 404, call(fmt.Sprintf("/api/v1/intake/work-items/%d", item.ID)).Code)
	client.RolePermission.Delete().Where(rolepermission.TenantIDEQ(f.actor.TenantID)).ExecX(ctx)
	require.Equal(t, 403, call(fmt.Sprintf("/api/v1/intake/catalog-items/%d", f.catalog.ID)).Code)
}

func TestIdentityWorkItemStorageFailureIsUnavailable(t *testing.T) {
	client, _, i, _, _, _ := intakeFixture(t)
	client.Ticket.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) { return nil, errors.New("database offline") })
	}))
	s := NewReadService(authorization.NewSessionReader(client, sameTransactionDirectory{}), nil, "test")
	_, err := s.WorkItem(tenantctx.WithTenantID(context.Background(), i.TenantID), i, 1)
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
}
