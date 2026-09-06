package standard_change

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
)

// newTestClient spins up an isolated in-memory SQLite-backed ent client for each test.
func newTestClient(t *testing.T) *ent.Client {
	dbName := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { client.Close() })
	return client
}

// setupTestRouter wires the handler into a gin engine with a mock auth middleware
// that injects the given user/tenant context (mirrors the real tenant middleware).
func setupTestRouter(t *testing.T, client *ent.Client, userID, tenantID int) (*gin.Engine, *Handler) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t).Sugar()
	h := NewHandler(client, logger)
	return setupRouterForHandler(t, h, userID, tenantID)
}

// setupInstantiationRouter adds the real shared Intake application only for
// Standard Change instantiation. Template definition CRUD remains independent
// from WorkItem creation.
func setupInstantiationRouter(t *testing.T, client *ent.Client, userID, tenantID int) (*gin.Engine, *Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t).Sugar()
	changeService := changedomain.NewService(nil, client, logger)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(changeService))
	resolver := intake.NewResolver(
		service_catalog.NewService(nil, client, logger),
		service.NewProcessBindingService(client),
		service.NewConfigurationItemService(client, logger, nil, nil),
		service.NewTicketCategoryService(client),
	)
	h := NewHandler(client, logger)
	h.SetCreationApplication(intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{}))
	return setupRouterForHandler(t, h, userID, tenantID)
}

func setupRouterForHandler(t *testing.T, h *Handler, userID, tenantID int) (*gin.Engine, *Handler) {
	t.Helper()
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("tenant_id", tenantID)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenantID})
		// RegisterRoutes now gates this group behind RequireRole("super_admin");
		// the handler tests below exercise business logic, not RBAC, so grant the
		// role here. RBAC gating itself is covered separately by
		// TestStandardChangeHandler_RoutesRequireSuperAdmin below.
		c.Set("role", "super_admin")
		c.Next()
	})
	h.RegisterRoutes(r.Group("/api/v1"))
	return r, h
}

// createTemplate inserts a StandardChange template directly via ent, so tests can
// exercise the read/update/delete/instantiate paths without going through the HTTP layer.
func createTemplate(t *testing.T, client *ent.Client, tenantID, userID int, mutate func(*ent.StandardChangeCreate)) *ent.StandardChange {
	builder := client.StandardChange.Create().
		SetTitle("默认模板").
		SetJustification("实施该标准变更以保持服务稳定").
		SetImplementationPlan("实施计划步骤").
		SetRollbackPlan("回滚计划步骤").
		SetCategory("general").
		SetRiskLevel("low").
		SetImpactScope("low").
		SetCreatedBy(userID).
		SetTenantID(tenantID)
	if mutate != nil {
		mutate(builder)
	}
	sc, err := builder.Save(context.Background())
	require.NoError(t, err)
	return sc
}

// decodeResponse unmarshals the unified response envelope into a Response + its Data map.
func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) (common.Response, map[string]interface{}) {
	var resp common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data, _ := resp.Data.(map[string]interface{})
	return resp, data
}

func doRequest(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	return doRequestWithIdempotencyKey(r, method, path, body, "")
}

func doRequestWithIdempotencyKey(r *gin.Engine, method, path string, body interface{}, key string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		require.NoError(nil, json.NewEncoder(&buf).Encode(body))
	}
	req, _ := http.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeCreationResponse(t *testing.T, w *httptest.ResponseRecorder) (int, creation.CreateWorkItemResult) {
	t.Helper()
	var response struct {
		Code int                           `json:"code"`
		Data creation.CreateWorkItemResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), "response body=%s", w.Body.String())
	return response.Code, response.Data
}

func createInstantiationIdentity(t *testing.T, client *ent.Client, suffix string) (*ent.Tenant, *ent.User) {
	t.Helper()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Standard Change Tenant").SetCode("standard-change-" + suffix).
		SetDomain("standard-change-" + suffix + ".example.com").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("standard-change-" + suffix).SetEmail("standard-change-" + suffix + "@example.com").
		SetName("Standard Change User").SetPasswordHash("hash").SetRole("super_admin").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	return tenant, actor
}

func configureInstantiationWorkflow(client *ent.Client, tenantID int) {
	client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType("change").SetIsDefault(true).
		SetProcessDefinitionKey("none").SetConditions(map[string]interface{}{"no_process": true}).SaveX(context.Background())
}

func createInstantiationCI(t *testing.T, client *ent.Client, tenantID int, name string) *ent.ConfigurationItem {
	t.Helper()
	ctx := context.Background()
	ciType := client.CIType.Create().SetTenantID(tenantID).SetName(name + " type").SaveX(ctx)
	return client.ConfigurationItem.Create().SetTenantID(tenantID).SetName(name).SetCiTypeID(ciType.ID).SaveX(ctx)
}

// ===================== toResponse conversion =====================

func TestToResponse_Nil(t *testing.T) {
	assert.Nil(t, toResponse(nil))
}

func TestToResponse_MapsAllFields(t *testing.T) {
	sc := createTemplate(t, newTestClient(t), 1, 7, func(b *ent.StandardChangeCreate) {
		b.SetDescription("desc").
			SetJustification("reason").
			SetExpectedDuration(45).
			SetApprovalRequired(true).
			SetAffectedCis([]string{"web", "db"}).
			SetPrerequisites([]string{"backup"}).
			SetRemarks("note").
			SetIsActive(false)
	})

	resp := toResponse(sc)
	require.NotNil(t, resp)
	assert.Equal(t, sc.ID, resp.ID)
	assert.Equal(t, "默认模板", resp.Title)
	assert.Equal(t, "desc", resp.Description)
	assert.Equal(t, "reason", resp.Justification)
	assert.Equal(t, "general", resp.Category)
	assert.Equal(t, "low", resp.RiskLevel)
	assert.Equal(t, 45, resp.ExpectedDuration)
	assert.True(t, resp.ApprovalRequired)
	assert.Equal(t, []string{"web", "db"}, resp.AffectedCis)
	assert.Equal(t, []string{"backup"}, resp.Prerequisites)
	assert.Equal(t, 7, resp.CreatedBy)
	assert.Equal(t, 1, resp.TenantID)
	assert.False(t, resp.IsActive)
}

// ===================== CreateStandardChange =====================

func TestCreateStandardChange_SuccessWithDefaults(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)

	req := dto.CreateStandardChangeRequest{
		Title:              "日常发布模板",
		ImplementationPlan: "步骤1；步骤2",
		RollbackPlan:       "回滚步骤",
	}
	w := doRequest(r, "POST", "/api/v1/standard-changes", req)

	resp, data := decodeResponse(t, w)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, common.SuccessCode, resp.Code)

	// Defaults applied by the handler
	assert.Equal(t, "general", data["category"])
	assert.Equal(t, "low", data["riskLevel"])
	assert.Equal(t, "low", data["impactScope"])
	assert.Equal(t, float64(1), data["createdBy"])
	assert.Equal(t, float64(1), data["tenantId"])
	assert.Equal(t, true, data["isActive"])
	// Omitted expected_duration falls back to the schema default (30 minutes)
	// instead of being persisted as the JSON zero value 0.
	assert.Equal(t, float64(30), data["expectedDuration"])
}

func TestCreateStandardChange_SuccessWithExplicitValues(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 3, 2)

	req := dto.CreateStandardChangeRequest{
		Title:              "网络变更模板",
		Description:        "描述",
		ImplementationPlan: "计划",
		RollbackPlan:       "回滚",
		Category:           "network",
		RiskLevel:          "high",
		ImpactScope:        "medium",
		ExpectedDuration:   120,
		ApprovalRequired:   true,
		AffectedCis:        []string{"switch"},
		Prerequisites:      []string{"审批单"},
	}
	w := doRequest(r, "POST", "/api/v1/standard-changes", req)

	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, "network", data["category"])
	assert.Equal(t, "high", data["riskLevel"])
	assert.Equal(t, "medium", data["impactScope"])
	assert.Equal(t, float64(120), data["expectedDuration"])
	assert.Equal(t, true, data["approvalRequired"])
	assert.Equal(t, []interface{}{"switch"}, data["affectedCis"])
	assert.Equal(t, float64(3), data["createdBy"])
	assert.Equal(t, float64(2), data["tenantId"])
}

// TestCreateStandardChange_ExplicitZeroDurationDefaultsTo30 locks the fix for the
// bug where an omitted/zero expected_duration was persisted as 0 instead of the
// schema default (30). A client sending 0 explicitly must also get the default.
func TestCreateStandardChange_ExplicitZeroDurationDefaultsTo30(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)

	req := dto.CreateStandardChangeRequest{
		Title:              "零工期模板",
		ImplementationPlan: "步骤1",
		RollbackPlan:       "回滚步骤",
		ExpectedDuration:   0,
	}
	w := doRequest(r, "POST", "/api/v1/standard-changes", req)

	resp, data := decodeResponse(t, w)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(30), data["expectedDuration"],
		"explicit 0 must fall back to the schema default of 30, not persist 0")
}

func TestCreateStandardChange_MissingRequiredField(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)

	// Missing ImplementationPlan (binding:"required")
	req := map[string]interface{}{
		"title":        "缺字段模板",
		"rollbackPlan": "回滚",
	}
	w := doRequest(r, "POST", "/api/v1/standard-changes", req)

	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, common.ParamErrorCode, resp.Code)
}

// ===================== ListStandardChanges =====================

func TestListStandardChanges_Basic(t *testing.T) {
	client := newTestClient(t)
	createTemplate(t, client, 1, 1, nil)
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
		b.SetTitle("第二个模板").SetCategory("database")
	})
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(2), data["total"])
	templates := data["templates"].([]interface{})
	assert.Len(t, templates, 2)
}

func TestListStandardChanges_Pagination(t *testing.T) {
	client := newTestClient(t)
	for i := 0; i < 5; i++ {
		createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
			b.SetTitle("模板" + strconv.Itoa(i))
		})
	}
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes?page=2&pageSize=2", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(5), data["total"])
	templates := data["templates"].([]interface{})
	assert.Len(t, templates, 2)
	assert.Equal(t, float64(2), data["page"])
	assert.Equal(t, float64(2), data["pageSize"])
}

func TestListStandardChanges_FilterByCategory(t *testing.T) {
	client := newTestClient(t)
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("network") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("database") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("database") })
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes?category=database", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(2), data["total"])
}

func TestListStandardChanges_Search(t *testing.T) {
	client := newTestClient(t)
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetTitle("数据库扩容") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetTitle("网络割接") })
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes?search=数据库", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(1), data["total"])
}

func TestListStandardChanges_ActiveOnly(t *testing.T) {
	client := newTestClient(t)
	createTemplate(t, client, 1, 1, nil)
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
		b.SetTitle("已停用").SetIsActive(false)
	})
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes?active_only=true", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, float64(1), data["total"])
}

// ===================== GetStandardChange =====================

func TestGetStandardChange_ByID(t *testing.T) {
	client := newTestClient(t)
	sc := createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
		b.SetTitle("唯一模板").SetCategory("network")
	})
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes/"+strconv.Itoa(sc.ID), nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, "唯一模板", data["title"])
	assert.Equal(t, "network", data["category"])
}

func TestGetStandardChange_InvalidID(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	for _, id := range []string{"abc", "0", "-5"} {
		w := doRequest(r, "GET", "/api/v1/standard-changes/"+id, nil)
		resp, _ := decodeResponse(t, w)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, common.ParamErrorCode, resp.Code)
	}
}

func TestGetStandardChange_NotFound(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	w := doRequest(r, "GET", "/api/v1/standard-changes/99999", nil)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)
}

// ===================== UpdateStandardChange =====================

func TestUpdateStandardChange_PartialUpdate(t *testing.T) {
	client := newTestClient(t)
	sc := createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
		b.SetCategory("general").SetRiskLevel("low")
	})
	r, _ := setupTestRouter(t, client, 1, 1)

	req := dto.UpdateStandardChangeRequest{
		Title:     strPtr("改名后的模板"),
		RiskLevel: strPtr("high"),
	}
	w := doRequest(r, "PUT", "/api/v1/standard-changes/"+strconv.Itoa(sc.ID), req)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	assert.Equal(t, "改名后的模板", data["title"])
	assert.Equal(t, "high", data["riskLevel"])
	// Untouched fields are preserved
	assert.Equal(t, "general", data["category"])
}

func TestUpdateStandardChange_NotFound(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	req := dto.UpdateStandardChangeRequest{Title: strPtr("x")}
	w := doRequest(r, "PUT", "/api/v1/standard-changes/99999", req)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)
}

func TestUpdateStandardChange_InvalidID(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	req := dto.UpdateStandardChangeRequest{Title: strPtr("x")}
	w := doRequest(r, "PUT", "/api/v1/standard-changes/abc", req)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, common.ParamErrorCode, resp.Code)
}

// ===================== DeleteStandardChange (soft delete) =====================

func TestDeleteStandardChange_SoftDelete(t *testing.T) {
	client := newTestClient(t)
	sc := createTemplate(t, client, 1, 1, nil)
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "DELETE", "/api/v1/standard-changes/"+strconv.Itoa(sc.ID), nil)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)

	// Soft delete must only deactivate, not physically remove.
	refreshed, err := client.StandardChange.Get(context.Background(), sc.ID)
	require.NoError(t, err)
	assert.False(t, refreshed.IsActive, "deleted template should be deactivated, not removed")
}

func TestDeleteStandardChange_NotFound(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	w := doRequest(r, "DELETE", "/api/v1/standard-changes/99999", nil)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)
}

func TestDeleteStandardChange_InvalidID(t *testing.T) {
	r, _ := setupTestRouter(t, newTestClient(t), 1, 1)
	w := doRequest(r, "DELETE", "/api/v1/standard-changes/0", nil)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, common.ParamErrorCode, resp.Code)
}

// ===================== GetCategories =====================

func TestGetCategories_Distinct(t *testing.T) {
	client := newTestClient(t)
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("network") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("network") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("database") })
	createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) { b.SetCategory("general") })
	r, _ := setupTestRouter(t, client, 1, 1)

	w := doRequest(r, "GET", "/api/v1/standard-changes/categories", nil)
	resp, data := decodeResponse(t, w)
	assert.Equal(t, common.SuccessCode, resp.Code)
	cats := data["categories"].([]interface{})
	assert.ElementsMatch(t, []interface{}{"network", "database", "general"}, cats)
}

// ===================== InstantiateStandardChange =====================

func TestInstantiateStandardChange_Defaults(t *testing.T) {
	client := newTestClient(t)
	tenant, actor := createInstantiationIdentity(t, client, "defaults")
	configureInstantiationWorkflow(client, tenant.ID)
	ci := createInstantiationCI(t, client, tenant.ID, "web server")
	tmpl := createTemplate(t, client, tenant.ID, actor.ID, func(b *ent.StandardChangeCreate) {
		b.SetTitle("发布模板").SetRiskLevel("medium").SetImpactScope("high").
			SetAffectedCis([]string{strconv.Itoa(ci.ID)}).
			SetImplementationPlan("实施计划步骤").SetRollbackPlan("回滚计划步骤")
	})
	r, _ := setupInstantiationRouter(t, client, actor.ID, tenant.ID)

	body := dto.InstantiateStandardChangeRequest{}
	path := "/api/v1/standard-changes/" + strconv.Itoa(tmpl.ID) + "/instantiate"
	w := doRequestWithIdempotencyKey(r, "POST", path, body, "standard-change-defaults")
	code, result := decodeCreationResponse(t, w)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, common.SuccessCode, code)
	require.Positive(t, result.WorkItemID)
	require.NotEmpty(t, result.Number)
	require.Equal(t, creation.RecordClassChangeRequest, result.RecordClass)
	require.Equal(t, "change", result.ProfessionalReference.Type)
	require.Positive(t, result.ProfessionalReference.ID)

	// Creation returns the shared receipt. Inspect the authoritative persisted
	// WorkItem and Change extension separately for template expansion evidence.
	created, err := client.Change.Query().
		Where(change.ID(result.ProfessionalReference.ID), change.HasWorkItemWith(ticket.TenantID(tenant.ID))).
		WithWorkItem().
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, result.WorkItemID, created.Edges.WorkItem.ID)
	assert.Equal(t, "发布模板", created.Edges.WorkItem.Title)
	assert.Equal(t, "standard", created.Type)
	assert.Equal(t, "draft", created.Edges.WorkItem.Status)
	assert.Equal(t, "medium", created.Edges.WorkItem.Priority)
	assert.Equal(t, "high", created.ImpactScope)
	assert.Equal(t, "medium", created.RiskLevel)
	assert.Equal(t, actor.ID, created.Edges.WorkItem.OpenedByID)
	assert.Equal(t, []string{strconv.Itoa(ci.ID)}, created.AffectedCis)
	assert.Equal(t, "实施该标准变更以保持服务稳定", created.Justification)
	assert.Equal(t, "实施计划步骤", created.ImplementationPlan)
	assert.Equal(t, "回滚计划步骤", created.RollbackPlan)

	replayWriter := doRequestWithIdempotencyKey(r, "POST", path, body, "standard-change-defaults")
	replayCode, replay := decodeCreationResponse(t, replayWriter)
	require.Equal(t, http.StatusOK, replayWriter.Code, replayWriter.Body.String())
	require.Equal(t, common.SuccessCode, replayCode)
	require.True(t, replay.Replayed)
	require.Equal(t, result.WorkItemID, replay.WorkItemID)
	require.Equal(t, result.ProfessionalReference, replay.ProfessionalReference)
	require.Equal(t, 1, client.Ticket.Query().CountX(context.Background()))
	require.Equal(t, 1, client.Change.Query().CountX(context.Background()))
}

func TestInstantiateStandardChange_Overrides(t *testing.T) {
	client := newTestClient(t)
	tenant, actor := createInstantiationIdentity(t, client, "overrides")
	configureInstantiationWorkflow(client, tenant.ID)
	originalCI := createInstantiationCI(t, client, tenant.ID, "original server")
	newCI1 := createInstantiationCI(t, client, tenant.ID, "new server 1")
	newCI2 := createInstantiationCI(t, client, tenant.ID, "new server 2")
	tmpl := createTemplate(t, client, tenant.ID, actor.ID, func(b *ent.StandardChangeCreate) {
		b.SetTitle("原模板").SetAffectedCis([]string{strconv.Itoa(originalCI.ID)})
	})
	r, _ := setupInstantiationRouter(t, client, actor.ID, tenant.ID)

	req := dto.InstantiateStandardChangeRequest{
		Title:       "覆盖标题",
		AffectedCis: []string{strconv.Itoa(newCI1.ID), strconv.Itoa(newCI2.ID)},
	}
	w := doRequestWithIdempotencyKey(r, "POST", "/api/v1/standard-changes/"+strconv.Itoa(tmpl.ID)+"/instantiate", req, "standard-change-overrides")
	code, result := decodeCreationResponse(t, w)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, common.SuccessCode, code)
	require.Positive(t, result.WorkItemID)
	require.NotEmpty(t, result.Number)
	require.Equal(t, creation.RecordClassChangeRequest, result.RecordClass)
	require.Positive(t, result.ProfessionalReference.ID)

	created, err := client.Change.Query().
		Where(change.ID(result.ProfessionalReference.ID), change.HasWorkItemWith(ticket.TenantID(tenant.ID))).
		WithWorkItem().
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, result.WorkItemID, created.Edges.WorkItem.ID)
	assert.Equal(t, "覆盖标题", created.Edges.WorkItem.Title)
	assert.Equal(t, []string{strconv.Itoa(newCI1.ID), strconv.Itoa(newCI2.ID)}, created.AffectedCis)
	// Untouched fields fall back to the template's values (implementation plan is unchanged).
	assert.Equal(t, "实施计划步骤", created.ImplementationPlan)
}

func TestInstantiateStandardChange_InactiveTemplateNotFound(t *testing.T) {
	client := newTestClient(t)
	tenant, actor := createInstantiationIdentity(t, client, "inactive")
	tmpl := createTemplate(t, client, tenant.ID, actor.ID, func(b *ent.StandardChangeCreate) {
		b.SetIsActive(false)
	})
	r, _ := setupInstantiationRouter(t, client, actor.ID, tenant.ID)

	w := doRequestWithIdempotencyKey(r, "POST", "/api/v1/standard-changes/"+strconv.Itoa(tmpl.ID)+"/instantiate", dto.InstantiateStandardChangeRequest{}, "standard-change-inactive")
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)
}

func TestInstantiateStandardChange_NotFound(t *testing.T) {
	client := newTestClient(t)
	tenant, actor := createInstantiationIdentity(t, client, "missing")
	r, _ := setupInstantiationRouter(t, client, actor.ID, tenant.ID)
	w := doRequestWithIdempotencyKey(r, "POST", "/api/v1/standard-changes/99999/instantiate", dto.InstantiateStandardChangeRequest{}, "standard-change-missing")
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)
}

// ===================== Tenant isolation (multi-tenant core logic) =====================

func TestStandardChange_TenantIsolation(t *testing.T) {
	client := newTestClient(t)
	// Template belongs to tenant 1
	sc := createTemplate(t, client, 1, 1, func(b *ent.StandardChangeCreate) {
		b.SetTitle("租户1模板")
	})

	// Router scoped to tenant 2 must not see tenant 1's template
	r2, _ := setupTestRouter(t, client, 1, 2)

	w := doRequest(r2, "GET", "/api/v1/standard-changes/"+strconv.Itoa(sc.ID), nil)
	resp, _ := decodeResponse(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, common.NotFoundCode, resp.Code)

	// And it must not appear in tenant 2's list
	wList := doRequest(r2, "GET", "/api/v1/standard-changes", nil)
	_, data := decodeResponse(t, wList)
	assert.Equal(t, float64(0), data["total"])
}

// strPtr is a small helper for building optional-string update requests.
func strPtr(s string) *string { return &s }

// ===================== RBAC gating =====================

func TestStandardChangeHandler_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	h.RegisterRoutes(group)

	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/standard-changes"}, {"POST", "/api/v1/standard-changes/1/instantiate"}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"requesterId":2}`))
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	}
}

func TestInstantiateStandardChange_SuperAdminOnBehalf(t *testing.T) {
	client := newTestClient(t)
	tenant, actor := createInstantiationIdentity(t, client, "on-behalf")
	configureInstantiationWorkflow(client, tenant.ID)
	requester := client.User.Create().SetTenantID(tenant.ID).SetUsername("customer-requester").SetName("Customer requester").SetEmail("customer@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(context.Background())
	template := createTemplate(t, client, tenant.ID, actor.ID, nil)
	router, _ := setupInstantiationRouter(t, client, actor.ID, tenant.ID)
	path := "/api/v1/standard-changes/" + strconv.Itoa(template.ID) + "/instantiate"
	body := map[string]any{"requesterId": requester.ID}
	w := doRequestWithIdempotencyKey(router, "POST", path, body, "on-behalf")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	_, result := decodeCreationResponse(t, w)
	item := client.Ticket.GetX(context.Background(), result.WorkItemID)
	require.Equal(t, requester.ID, item.RequesterID)
	require.Equal(t, actor.ID, item.OpenedByID)
	w = doRequestWithIdempotencyKey(router, "POST", path, body, "on-behalf")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, client.Change.Query().CountX(context.Background()))
}
