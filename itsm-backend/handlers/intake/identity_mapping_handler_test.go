package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/externalidentity"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type identityMappingFixture struct {
	client   *ent.Client
	handler  *IdentityMappingHandler
	tenantID int
	actorID  int
	userID   int
}

func newIdentityMappingFixture(t *testing.T) *identityMappingFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:identity-mapping-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("Mapping Tenant").SetCode(fmt.Sprintf("MAP-%d", time.Now().UnixNano())).SetDomain(fmt.Sprintf("map-%d.test", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().SetUsername(fmt.Sprintf("mapping-admin-%d", time.Now().UnixNano())).SetEmail(fmt.Sprintf("mapping-admin-%d@test", time.Now().UnixNano())).SetName("Mapping Admin").SetPasswordHash("x").SetRole("sysadmin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	target, err := client.User.Create().SetUsername(fmt.Sprintf("mapping-target-%d", time.Now().UnixNano())).SetEmail(fmt.Sprintf("mapping-target-%d@test", time.Now().UnixNano())).SetName("Mapping Target").SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	return &identityMappingFixture{client: client, handler: NewIdentityMappingHandler(client), tenantID: tenant.ID, actorID: actor.ID, userID: target.ID}
}

func mappingContext(f *identityMappingFixture, method, path string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(payload))
	c.Set("tenant_id", f.tenantID)
	c.Set("user_id", f.actorID)
	c.Set("request_id", "mapping-request")
	return c, response
}

func createMapping(t *testing.T, f *identityMappingFixture, provider, workspace, subject string, userID int) (*httptest.ResponseRecorder, ExternalIdentityMappingResponse) {
	t.Helper()
	c, response := mappingContext(f, http.MethodPost, "/api/v1/intake/external-identities", CreateExternalIdentityMappingRequest{
		Provider: provider, Workspace: workspace, Subject: subject, UserID: userID,
	})
	f.handler.Create(c)
	var envelope struct {
		Data ExternalIdentityMappingResponse `json:"data"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	return response, envelope.Data
}

func TestIdentityMappingLifecycleIsTenantScopedAndAudited(t *testing.T) {
	f := newIdentityMappingFixture(t)
	createdResponse, created := createMapping(t, f, "microsoft", "workspace-a", "subject-a", f.userID)
	require.Equal(t, http.StatusCreated, createdResponse.Code, createdResponse.Body.String())
	require.Equal(t, 1, created.Version)
	require.True(t, created.Active)

	duplicate, _ := createMapping(t, f, "microsoft", "workspace-a", "subject-a", f.userID)
	require.Equal(t, http.StatusConflict, duplicate.Code)
	require.Contains(t, duplicate.Body.String(), "IDENTITY_MAPPING_EXISTS")

	listContext, listResponse := mappingContext(f, http.MethodGet, "/api/v1/intake/external-identities", nil)
	f.handler.List(listContext)
	require.Equal(t, http.StatusOK, listResponse.Code)
	require.Contains(t, listResponse.Body.String(), "workspace-a")
	require.Contains(t, listResponse.Body.String(), "subject-a")
	require.NotContains(t, listResponse.Body.String(), "signature")

	disableContext, disableResponse := mappingContext(f, http.MethodPost, fmt.Sprintf("/api/v1/intake/external-identities/%d/disable", created.ID), DisableExternalIdentityMappingRequest{Version: created.Version})
	disableContext.Params = gin.Params{{Key: "id", Value: fmt.Sprint(created.ID)}}
	f.handler.Disable(disableContext)
	require.Equal(t, http.StatusOK, disableResponse.Code, disableResponse.Body.String())
	require.Contains(t, disableResponse.Body.String(), `"active":false`)
	require.Contains(t, disableResponse.Body.String(), `"version":2`)

	staleContext, staleResponse := mappingContext(f, http.MethodPost, fmt.Sprintf("/api/v1/intake/external-identities/%d/disable", created.ID), DisableExternalIdentityMappingRequest{Version: created.Version})
	staleContext.Params = gin.Params{{Key: "id", Value: fmt.Sprint(created.ID)}}
	f.handler.Disable(staleContext)
	require.Equal(t, http.StatusConflict, staleResponse.Code)
	require.Contains(t, staleResponse.Body.String(), "IDENTITY_MAPPING_VERSION_CONFLICT")

	createdAudits, err := f.client.AuditLog.Query().Where(auditlog.ActionEQ("intake.identity_mapping.created")).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, createdAudits)
	disabledAudits, err := f.client.AuditLog.Query().Where(auditlog.ActionEQ("intake.identity_mapping.disabled")).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, disabledAudits)
}

func TestIdentityMappingRejectsCrossTenantUserAndRows(t *testing.T) {
	f := newIdentityMappingFixture(t)
	ctx := context.Background()
	foreignTenant, err := f.client.Tenant.Create().SetName("Foreign Mapping Tenant").SetCode(fmt.Sprintf("FOREIGN-%d", time.Now().UnixNano())).SetDomain(fmt.Sprintf("foreign-%d.test", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	foreignUser, err := f.client.User.Create().SetUsername(fmt.Sprintf("foreign-%d", time.Now().UnixNano())).SetEmail(fmt.Sprintf("foreign-%d@test", time.Now().UnixNano())).SetName("Foreign").SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(foreignTenant.ID).Save(ctx)
	require.NoError(t, err)

	response, _ := createMapping(t, f, "microsoft", "foreign", "foreign-subject", foreignUser.ID)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Contains(t, response.Body.String(), "TARGET_USER_NOT_FOUND")

	foreignMapping, err := f.client.ExternalIdentity.Create().SetTenantID(foreignTenant.ID).SetProvider("microsoft").SetWorkspace("foreign").SetSubject("subject").SetUserID(foreignUser.ID).Save(ctx)
	require.NoError(t, err)
	disableContext, disableResponse := mappingContext(f, http.MethodPost, "/disable", DisableExternalIdentityMappingRequest{Version: 1})
	disableContext.Params = gin.Params{{Key: "id", Value: fmt.Sprint(foreignMapping.ID)}}
	f.handler.Disable(disableContext)
	require.Equal(t, http.StatusNotFound, disableResponse.Code)

	visible, err := f.client.ExternalIdentity.Query().Where(externalidentity.TenantIDEQ(f.tenantID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, visible)
}
