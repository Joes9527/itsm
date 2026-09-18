package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/systemconfig"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type ctiGovernanceControllerFixture struct {
	controller *SystemConfigController
	client     *ent.Client
	ctx        context.Context
	tenantID   int
	actorID    int
}

func newCTIGovernanceControllerFixture(t *testing.T) ctiGovernanceControllerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("CTI").SetCode(t.Name()).SetStatus("active").SaveX(ctx)
	actor := client.User.Create().
		SetTenantID(tenant.ID).
		SetUsername("cti-admin").
		SetName("CTI Admin").
		SetRole("admin").
		SetEmail("cti-admin@example.test").
		SetPasswordHash("unused").
		SaveX(ctx)
	logger := zaptest.NewLogger(t).Sugar()
	return ctiGovernanceControllerFixture{
		controller: NewSystemConfigController(service.NewSystemConfigService(client, logger), logger),
		client:     client,
		ctx:        ctx,
		tenantID:   tenant.ID,
		actorID:    actor.ID,
	}
}

func (f ctiGovernanceControllerFixture) put(tenantID, actorID int, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/system-configs/governance/cti", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if tenantID > 0 {
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenantID})
	}
	if actorID > 0 {
		c.Set("user_id", actorID)
	}
	return c, recorder
}

func (f ctiGovernanceControllerFixture) governanceRows() int {
	return f.client.SystemConfig.Query().
		Where(systemconfig.TenantIDEQ(f.tenantID), systemconfig.KeyEQ(service.CTIGovernanceConfigKey)).
		CountX(f.ctx)
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) common.Response {
	t.Helper()
	var response common.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

// Tenant and actor must come from the trusted context, never from the body.
func TestSetCTIGovernanceRequiresTrustedTenantAndActor(t *testing.T) {
	fixture := newCTIGovernanceControllerFixture(t)

	for _, test := range []struct {
		name              string
		tenantID, actorID int
	}{
		{"no_tenant_context", 0, fixture.actorID},
		{"no_actor", fixture.tenantID, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := fixture.put(test.tenantID, test.actorID, `{"completionEnforced":true}`)

			fixture.controller.SetCTIGovernance(c)

			response := decodeEnvelope(t, recorder)
			assert.Equal(t, common.UnauthorizedCode, response.Code, "body=%s", recorder.Body.String())
		})
	}
	assert.Equal(t, 0, fixture.governanceRows(), "rejected requests must not write governance state")
}

func TestSetCTIGovernanceRejectsMalformedBody(t *testing.T) {
	fixture := newCTIGovernanceControllerFixture(t)
	c, recorder := fixture.put(fixture.tenantID, fixture.actorID, `{"completionEnforced":`)

	fixture.controller.SetCTIGovernance(c)

	response := decodeEnvelope(t, recorder)
	assert.Equal(t, common.ParamErrorCode, response.Code, "body=%s", recorder.Body.String())
	assert.Equal(t, 0, fixture.governanceRows())
}

func TestSetCTIGovernanceAppliesAndReturnsEffectiveCutoff(t *testing.T) {
	fixture := newCTIGovernanceControllerFixture(t)
	c, recorder := fixture.put(fixture.tenantID, fixture.actorID, `{"catalogEnforced":true,"completionEnforced":true}`)

	fixture.controller.SetCTIGovernance(c)

	response := decodeEnvelope(t, recorder)
	require.Equal(t, common.SuccessCode, response.Code, "body=%s", recorder.Body.String())
	payload, ok := response.Data.(map[string]interface{})
	require.True(t, ok, "governance response must be an object, got %T", response.Data)
	assert.Equal(t, true, payload["catalogEnforced"])
	assert.Equal(t, true, payload["completionEnforced"])
	assert.Equal(t, true, payload["applied"])
	assert.NotEmpty(t, payload["effectiveFrom"], "the first enable must derive an immutable cutoff")
	assert.Equal(t, 1, fixture.governanceRows())
}

// Replaying the same state is idempotent: one record, unchanged cutoff, applied=false.
func TestSetCTIGovernanceReplayKeepsCutoffAndSingleRecord(t *testing.T) {
	fixture := newCTIGovernanceControllerFixture(t)

	firstCall, firstRecorder := fixture.put(fixture.tenantID, fixture.actorID, `{"completionEnforced":true}`)
	fixture.controller.SetCTIGovernance(firstCall)
	first := decodeEnvelope(t, firstRecorder)
	require.Equal(t, common.SuccessCode, first.Code, "body=%s", firstRecorder.Body.String())

	secondCall, secondRecorder := fixture.put(fixture.tenantID, fixture.actorID, `{"completionEnforced":true}`)
	fixture.controller.SetCTIGovernance(secondCall)
	second := decodeEnvelope(t, secondRecorder)
	require.Equal(t, common.SuccessCode, second.Code, "body=%s", secondRecorder.Body.String())

	firstPayload := first.Data.(map[string]interface{})
	secondPayload := second.Data.(map[string]interface{})
	assert.Equal(t, false, secondPayload["applied"], "an unchanged replay must not claim to have applied")
	assert.Equal(t, firstPayload["effectiveFrom"], secondPayload["effectiveFrom"], "the cutoff is immutable once written")
	assert.Equal(t, 1, fixture.governanceRows(), "the reserved key keeps a single row per tenant")
}
