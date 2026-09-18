package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func referenceHTTPFixture(t *testing.T) (*resolverFixture, *ReadService, func(string, ...creation.Identity) *httptest.ResponseRecorder) {
	t.Helper()
	f := newResolverFixture(t)
	cfg, a, n := assertionFixture()
	m := f.client.ExternalIdentity.Create().SetTenantID(f.actor.TenantID).SetUserID(f.actor.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(context.Background())
	sessions := authorization.NewSessionReader(f.client, sameTransactionDirectory{})
	ex := NewIdentityExchangeService(cfg.config, n, NewIdentityRepository(f.client, f.client, sessions), "test-jwt-key")
	h := NewHandler(ex, f.app)
	s := NewReadService(sessions, nil, "cursor-secret", ReferenceReadOptions{FrontendURL: "https://support.example.test", PageSize: 2, Lifecycle: NewRequesterLifecycleReader(referenceLifecycleOwners())})
	h.SetReaders(s)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/v1"))
	return f, s, func(path string, identities ...creation.Identity) *httptest.ResponseRecorder {
		i := f.actor
		if len(identities) > 0 {
			i = identities[0]
		}
		mapping := m
		if i.ActorID != f.actor.ActorID {
			mapping = f.client.ExternalIdentity.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(fmt.Sprint(i.ActorID)).SaveX(context.Background())
		}
		token, err := authentication.GenerateIntakeToken(authentication.IntakeClaims{UserID: i.ActorID, TenantID: i.TenantID, Role: i.Role, TokenType: "intake", Scope: []string{"intake:catalog:read", "intake:workitem:read"}, Provider: a.Provider, Channel: a.Channel, EventID: a.EventID, MappingID: mapping.ID, MappingVersion: mapping.Version}, "test-jwt-key", time.Minute)
		require.NoError(t, err)
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
}

func TestWorkItemReferenceHTTPExactScopeAndValidation(t *testing.T) {
	f, _, call := referenceHTTPFixture(t)
	ctx := context.Background()
	item := f.client.Ticket.Create().SetTenantID(f.actor.TenantID).SetRequesterID(f.actor.ActorID).SetTitle("private title").SetDescription("private detail").SetTicketNumber("REQ-000037").SetStatus("closed").SaveX(ctx)
	w := call("/api/v1/intake/work-item-references?number=REQ-000037")
	require.Equal(t, 200, w.Code, w.Body.String())
	var out struct{ Data map[string]any }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, map[string]any{"workItemId": float64(item.ID), "number": "REQ-000037", "status": "closed", "url": fmt.Sprintf("https://support.example.test/tickets/%d", item.ID)}, out.Data)
	other := f.client.User.Create().SetTenantID(f.actor.TenantID).SetUsername("other-ref").SetName("Other").SetEmail("other-ref@example.test").SetPasswordHash("test").SaveX(ctx)
	f.client.Ticket.Create().SetTenantID(f.actor.TenantID).SetRequesterID(other.ID).SetTitle("Other").SetTicketNumber("OTHER-OWNER").SetStatus("open").SaveX(ctx)
	tenant := f.client.Tenant.Create().SetName("other-ref").SetCode("other-ref").SaveX(ctx)
	f.client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(f.actor.ActorID).SetTitle("Other tenant").SetTicketNumber("REQ-000037").SetStatus("open").SaveX(ctx)
	f.client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(f.actor.ActorID).SetTitle("Other tenant").SetTicketNumber("OTHER-TENANT").SetStatus("open").SaveX(ctx)
	for _, number := range []string{"OTHER-OWNER", "OTHER-TENANT", "REQ-0000", fmt.Sprint(item.ID), "absent"} {
		require.Equal(t, 404, call("/api/v1/intake/work-item-references?number="+number).Code, number)
	}
	require.Equal(t, 200, call("/api/v1/intake/work-item-references?number=REQ-000037").Code)
	for _, q := range []string{"number=", "number=%20", "number=REQ-000037&cursor=x", "number=REQ-000037&cursor=", "number=x&number=y", "cursor=x&cursor=y", "requesterId=1", "unknown=x", "number=x;cursor=y", "number=%ZZ"} {
		require.Equal(t, 400, call("/api/v1/intake/work-item-references?"+q).Code, q)
	}
	f.client.RolePermission.Delete().Where(rolepermission.TenantIDEQ(f.actor.TenantID)).ExecX(ctx)
	require.Equal(t, 404, call("/api/v1/intake/work-item-references?number=REQ-000037").Code)
}

func TestWorkItemReferenceHTTPPaginationScansFilteredRows(t *testing.T) {
	f, s, call := referenceHTTPFixture(t)
	ctx := context.Background()
	f.client.RolePermission.Delete().Where(rolepermission.TenantIDEQ(f.actor.TenantID)).ExecX(ctx)
	actorRole := f.client.Role.Query().Where(role.TenantIDEQ(f.actor.TenantID), role.CodeEQ(f.actor.Role)).OnlyX(ctx)
	readPermission := f.client.Permission.Create().SetTenantID(f.actor.TenantID).SetCode("reference-ticket-read").SetName("Ticket read").SetResource("ticket").SetAction("read").SaveX(ctx)
	f.client.RolePermission.Create().SetTenantID(f.actor.TenantID).SetRoleID(actorRole.ID).SetPermissionID(readPermission.ID).SaveX(ctx)

	seed := func(number, status, class string) {
		f.client.Ticket.Create().SetTenantID(f.actor.TenantID).SetRequesterID(f.actor.ActorID).SetTitle("private").SetTicketNumber(number).SetStatus(status).SetRecordClass(class).SaveX(ctx)
	}
	seed("DONE-1", "closed", "generic")
	seed("DONE-2", "resolved", "generic")
	seed("DENIED", "open", "problem")
	seed("OPEN-1", "open", "generic")
	seed("DONE-3", "cancelled", "generic")
	seed("OPEN-2", "pending", "generic")
	seed("DONE-4", "closed", "generic")
	seed("OPEN-3", "assigned", "generic")
	page := func(path string) WorkItemReferencePage {
		w := call(path)
		require.Equal(t, 200, w.Code, w.Body.String())
		var out struct{ Data WorkItemReferencePage }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out.Data
	}
	first := page("/api/v1/intake/work-item-references")
	require.Len(t, first.Items, 2)
	require.Equal(t, "OPEN-1", first.Items[0].Number)
	require.Equal(t, "OPEN-2", first.Items[1].Number)
	require.NotNil(t, first.NextCursor)
	second := page("/api/v1/intake/work-item-references?cursor=" + *first.NextCursor)
	require.Len(t, second.Items, 1)
	require.Equal(t, "OPEN-3", second.Items[0].Number)
	require.Nil(t, second.NextCursor)
	require.Equal(t, 400, call("/api/v1/intake/work-item-references?cursor="+*first.NextCursor+"x").Code)
	for _, value := range []referenceCursor{
		{TenantID: f.actor.TenantID + 1, ActorID: f.actor.ActorID, Mode: "unfinished", After: 1},
		{TenantID: f.actor.TenantID, ActorID: f.actor.ActorID, Mode: "exact", After: 1},
	} {
		require.Equal(t, 400, call("/api/v1/intake/work-item-references?cursor="+s.encodeReferenceCursor(value)).Code)
	}

	require.Equal(t, 400, call("/api/v1/intake/work-item-references?cursor="+s.encodeCursor(catalogCursor{TenantID: f.actor.TenantID, ActorID: f.actor.ActorID, After: 1})).Code)
	other := f.client.User.Create().SetTenantID(f.actor.TenantID).SetUsername("cursor-user").SetName("Other").SetEmail("cursor-user@example.test").SetRole(f.actor.Role).SetPasswordHash("test").SaveX(ctx)
	identity := f.actor
	identity.ActorID = other.ID
	identity.RequesterID = other.ID
	require.Equal(t, 400, call("/api/v1/intake/work-item-references?cursor="+*first.NextCursor, identity).Code)
	f.client.RolePermission.Delete().Where(rolepermission.TenantIDEQ(f.actor.TenantID)).ExecX(ctx)
	revoked := page("/api/v1/intake/work-item-references?cursor=" + *first.NextCursor)
	require.Empty(t, revoked.Items)
	require.Nil(t, revoked.NextCursor)
}

func TestWorkItemReferenceHTTPFailuresAreNotEmptyPages(t *testing.T) {
	for _, mode := range []string{"storage", "lifecycle", "unregistered", "origin", "page-size", "catalog-task"} {
		t.Run(mode, func(t *testing.T) {
			f, s, call := referenceHTTPFixture(t)
			class := "generic"
			if mode == "catalog-task" {
				class = "catalog_task"
			}
			item := f.client.Ticket.Create().SetTenantID(f.actor.TenantID).SetRequesterID(f.actor.ActorID).SetRecordClass(class).SetTitle("Own").SetTicketNumber("FAIL").SetStatus("open").SaveX(context.Background())
			switch mode {
			case "storage":
				f.client.Ticket.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
					return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, errors.New("offline") })
				}))
			case "lifecycle":
				f.client.Ticket.UpdateOneID(item.ID).SetStatus("unknown").SaveX(context.Background())
			case "unregistered":
				s.references.Lifecycle = NewRequesterLifecycleReader(nil)
			case "origin":
				s.references.FrontendURL = ""
			case "page-size":
				s.references.PageSize = 0
			}
			require.Equal(t, 503, call("/api/v1/intake/work-item-references").Code)
			if mode == "storage" || mode == "origin" {
				require.Equal(t, 503, call("/api/v1/intake/work-item-references?number=FAIL").Code)
			}
		})
	}
}
