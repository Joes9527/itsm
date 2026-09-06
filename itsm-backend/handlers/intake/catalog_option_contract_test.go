package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/intakerequest"
	creation "itsm-backend/handlers/common/workitemcreation"
	catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityCatalogOptionPublishedRoundTripAndStableReplay(t *testing.T) {
	client, app, i, _, _, _ := intakeFixture(t)
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	app.registry = NewCreatorRegistry()
	require.NoError(t, app.registry.Register(service.NewTicketServiceForTest(client, logger)))
	client.ProcessBinding.Create().SetTenantID(i.TenantID).SetBusinessType("ticket").SetProcessDefinitionKey("none").SetIsDefault(true).SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	owner := catalog.NewService(catalog.NewEntRepository(client), client, logger, sameTransactionDirectory{})
	owner.SetCreatorRegistry(app.registry)
	options := []any{map[string]any{"label": "Text", "value": "east"}, map[string]any{"label": "Same spelling text", "value": "9007199254740993"}, map[string]any{"label": "Exact integer", "value": json.Number("9007199254740993")}, map[string]any{"label": "Adjacent integer", "value": json.Number("9007199254740992")}}
	published, err := owner.Create(ctx, i.TenantID, dto.CreateServiceCatalogRequest{Name: "Typed choices", Category: "IT", TargetClass: "generic", Status: "enabled", Fields: []map[string]any{{"name": "choice", "label": "Choice", "type": "select", "required": true, "options": options}, {"name": "many", "label": "Many", "type": "multiselect", "options": options}}})
	require.NoError(t, err)
	app.resolver = NewResolver(owner, service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	cfg, a, n := assertionFixture()
	m := client.ExternalIdentity.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(ctx)
	sessions := authorization.NewSessionReader(client, sameTransactionDirectory{})
	h := NewHandler(NewIdentityExchangeService(cfg.config, n, NewIdentityRepository(client, client, sessions), "test-jwt"), app)
	h.SetReaders(NewReadService(sessions, owner, "test-cursor"))
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/v1"))
	token := func(scopes []string) string {
		token, err := authentication.GenerateIntakeToken(authentication.IntakeClaims{UserID: i.ActorID, TenantID: i.TenantID, Role: i.Role, Provider: a.Provider, Channel: a.Channel, EventID: a.EventID, MappingID: m.ID, MappingVersion: 1, TokenType: "intake", Scope: scopes}, "test-jwt", time.Minute)
		require.NoError(t, err)
		return token
	}
	read, write := token([]string{"intake:catalog:read", "intake:workitem:read"}), token([]string{"intake:create"})
	call := func(method, path string, body any, credential string) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := call("GET", "/api/v1/intake/catalog-items", nil, read)
	require.Equal(t, 200, w.Code, w.Body.String())
	var page struct{ Data CatalogPage }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Data.Items, 1)
	require.Equal(t, published.ID, page.Data.Items[0].ID)
	w = call("GET", fmt.Sprintf("/api/v1/intake/catalog-items/%d", published.ID), nil, read)
	require.Equal(t, 200, w.Code, w.Body.String())
	var detail struct{ Data CatalogContract }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Equal(t, published.CatalogVersion, detail.Data.CatalogVersion)
	expectedKeys := []string{"option:v1:ImVhc3Qi", "option:v1:IjkwMDcxOTkyNTQ3NDA5OTMi", "option:v1:OTAwNzE5OTI1NDc0MDk5Mw", "option:v1:OTAwNzE5OTI1NDc0MDk5Mg"}
	require.Len(t, detail.Data.Fields, 2)
	for index, key := range expectedKeys {
		require.Equal(t, key, detail.Data.Fields[0].Options[index].Key)
	}
	expectedJSON := []string{`"east"`, `"9007199254740993"`, `9007199254740993`, `9007199254740992`}
	var original creation.CreateWorkItemCommand
	var receipt creation.CreateWorkItemResult
	for index, key := range expectedKeys {
		command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "catalog_item", Confirmation: "confirmed", Title: "Typed choice", CatalogItemID: &published.ID, CatalogVersion: detail.Data.CatalogVersion, FormSchemaVersion: detail.Data.FormSchemaVersion, IdempotencyKey: fmt.Sprintf("choice-%d", index), FormValues: map[string]any{"choice": key, "many": []any{expectedKeys[2], expectedKeys[3], expectedKeys[1]}}}
		before, _ := json.Marshal(command)
		w = call("POST", "/api/v1/intake/work-items", command, write)
		require.Equal(t, 201, w.Code, w.Body.String())
		var result struct{ Data creation.CreateWorkItemResult }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		rows := client.FieldValue.Query().Where(fieldvalue.EntityIDEQ(result.Data.WorkItemID)).AllX(ctx)
		require.Len(t, rows, 2)
		for _, row := range rows {
			if row.FieldName == "choice" {
				require.Equal(t, expectedJSON[index], string(row.Value))
			} else {
				require.Equal(t, `[9007199254740993,9007199254740992,"9007199254740993"]`, string(row.Value))
			}
		}
		after, _ := json.Marshal(command)
		require.Equal(t, before, after)
		if index == 2 {
			original = command
			receipt = result.Data
		}
	}
	invalid := original
	invalid.IdempotencyKey = "unknown-option"
	invalid.FormValues = map[string]any{"choice": "option:v1:OTk5"}
	w = call("POST", "/api/v1/intake/work-items", invalid, write)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "DomainValidationFailed")
	stored := client.IntakeRequest.Query().Where(intakerequest.IdempotencyKeyEQ(original.IdempotencyKey)).OnlyX(ctx)
	canonicalSource := original
	canonicalSource.SourceReference = &creation.SourceReference{Provider: a.Provider, EventID: a.EventID}
	_, digest, err := creation.CanonicalizeCommand(canonicalSource)
	require.NoError(t, err)
	require.Equal(t, digest, stored.RequestDigest)
	// Replace the published option definition: replay uses the original wire
	// digest/receipt before inspecting the now-incompatible option keys/version.
	_, err = owner.Update(ctx, i.TenantID, published.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: published.CatalogVersion, Fields: []map[string]any{{"name": "choice", "label": "Choice", "type": "select", "required": true, "options": []any{map[string]any{"label": "Replacement", "value": "replacement"}}}}})
	require.NoError(t, err)
	w = call("POST", "/api/v1/intake/work-items", original, write)
	require.Equal(t, 200, w.Code, w.Body.String())
	var replay struct{ Data creation.CreateWorkItemResult }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &replay))
	require.True(t, replay.Data.Replayed)
	require.Equal(t, receipt.WorkItemID, replay.Data.WorkItemID)
	original.IdempotencyKey = "fresh-after-change"
	w = call("POST", "/api/v1/intake/work-items", original, write)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "CatalogVersionConflict")
}
