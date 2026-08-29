package problem

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func strPtr(s string) *string {
	return &s
}

func setupProblemHTTPHandlerTest(t *testing.T) (*gin.Engine, *Handler, *Service, *ent.Client) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:problem-http-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	repo := NewEntRepository(client)
	service := NewService(repo, zaptest.NewLogger(t).Sugar())
	handler := NewHandler(service, client)

	r := gin.New()
	r.Use(gin.Recovery())

	// Context middleware injects tenant_id and user_id from headers
	r.Use(func(c *gin.Context) {
		if tid := c.GetHeader("X-Tenant-ID"); tid != "" {
			if id, err := strconv.Atoi(tid); err == nil {
				c.Set("tenant_id", id)
				c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: id})
			}
		}
		if uid := c.GetHeader("X-User-ID"); uid != "" {
			if id, err := strconv.Atoi(uid); err == nil {
				c.Set("user_id", id)
			}
		}
		if role := c.GetHeader("X-User-Role"); role != "" {
			c.Set("role", role)
		}
		if customer := c.GetHeader("X-MSP-Customer-ID"); customer != "" {
			if customerID, err := strconv.Atoi(customer); err == nil {
				mspCtx := &middleware.MSPContext{
					IsMSP:            true,
					MSPUserID:        c.GetInt("user_id"),
					CustomerTenantID: &customerID,
				}
				if allowed := c.GetHeader("X-MSP-Allowed-Customer-ID"); allowed != "" {
					if allowedID, err := strconv.Atoi(allowed); err == nil {
						mspCtx.AllowedCustomers = []int{allowedID}
					}
				}
				c.Set(middleware.MSPContextKey, mspCtx)
			}
		}
		c.Next()
	})

	api := r.Group("/api/v1/problems")
	{
		api.POST("", handler.Create)
		api.GET("", handler.List)
		api.GET("/stats", handler.GetStats)
		api.GET("/:id", handler.Get)
		api.PUT("/:id", handler.Update)
		api.DELETE("/:id", handler.Delete)
		api.GET("/:id/associations", handler.GetAssociations)
		api.POST("/:id/associations", handler.AddAssociation)
		api.DELETE("/:id/associations", handler.RemoveAssociation)
		api.POST("/:id/investigate", handler.InvestigateProblem)
		api.POST("/:id/root-cause", handler.UpdateRootCause)
		api.POST("/:id/solution", handler.UpdateSolution)
		api.POST("/:id/close", handler.CloseProblem)
	}

	return r, handler, service, client
}

func performProblemRequest(r http.Handler, method, path string, body interface{}, tenantID, userID int) *httptest.ResponseRecorder {
	var reqBody []byte
	if body != nil {
		reqBody, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	if tenantID > 0 {
		req.Header.Set("X-Tenant-ID", strconv.Itoa(tenantID))
	}
	if userID > 0 {
		req.Header.Set("X-User-ID", strconv.Itoa(userID))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func performProblemRequestWithRole(r http.Handler, method, path string, body interface{}, tenantID, userID int, role string) *httptest.ResponseRecorder {
	var reqBody []byte
	if body != nil {
		reqBody, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	if tenantID > 0 {
		req.Header.Set("X-Tenant-ID", strconv.Itoa(tenantID))
	}
	if userID > 0 {
		req.Header.Set("X-User-ID", strconv.Itoa(userID))
	}
	if role != "" {
		req.Header.Set("X-User-Role", role)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestProblemHTTPHandlerCreateGetList(t *testing.T) {
	r, _, _, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenant := createProblemHandlerTenant(t, ctx, client, "http-cgl")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "http-cgl")

	// 1. Create Problem - Valid
	createReq := dto.CreateProblemRequest{
		Title:       "Memory Overuse in Service X",
		Description: "Pod restarted due to OOM",
		Priority:    "high",
		Category:    "backend",
		Impact:      "medium",
	}
	w := performProblemRequest(r, "POST", "/api/v1/problems", createReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, w.Code)

	var res common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, 0, res.Code)

	dataMap := res.Data.(map[string]interface{})
	probID := int(dataMap["id"].(float64))
	assert.Equal(t, "Memory Overuse in Service X", dataMap["title"])
	assert.Equal(t, "open", dataMap["status"])

	// 2. Create Problem - Invalid Params
	wBad := performProblemRequest(r, "POST", "/api/v1/problems", "invalid json", tenant.ID, user.ID)
	require.Equal(t, http.StatusBadRequest, wBad.Code)
	var resBad common.Response
	require.NoError(t, json.Unmarshal(wBad.Body.Bytes(), &resBad))
	assert.Equal(t, common.ParamErrorCode, resBad.Code)

	// 3. Get Problem - Valid
	wGet := performProblemRequestWithRole(r, "GET", fmt.Sprintf("/api/v1/problems/%d", probID), nil, tenant.ID, user.ID, "super_admin")
	require.Equal(t, http.StatusOK, wGet.Code)
	var resGet common.Response
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &resGet))
	assert.Equal(t, 0, resGet.Code)

	// 4. Get Problem - Invalid ID / Not Found
	wNotFound := performProblemRequestWithRole(r, "GET", "/api/v1/problems/99999", nil, tenant.ID, user.ID, "super_admin")
	require.Equal(t, http.StatusNotFound, wNotFound.Code)
	var resNotFound common.Response
	require.NoError(t, json.Unmarshal(wNotFound.Body.Bytes(), &resNotFound))
	assert.Equal(t, common.NotFoundErrorCode, resNotFound.Code)

	wInvalidID := performProblemRequest(r, "GET", "/api/v1/problems/abc", nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusBadRequest, wInvalidID.Code)
	var resInvalidID common.Response
	require.NoError(t, json.Unmarshal(wInvalidID.Body.Bytes(), &resInvalidID))
	assert.Equal(t, common.ParamErrorCode, resInvalidID.Code)

	// 5. List Problems
	wList := performProblemRequest(r, "GET", "/api/v1/problems?page=1&size=10&category=backend", nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wList.Code)
	var resList common.Response
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &resList))
	assert.Equal(t, 0, resList.Code)
}

func TestProblemHTTPHandlerGetUsesResolvedMSPTenant(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	homeTenant := createProblemHandlerTenant(t, ctx, client, "msp-home")
	customerTenant := createProblemHandlerTenant(t, ctx, client, "msp-customer")
	user := createProblemHandlerUser(t, ctx, client, homeTenant.ID, "msp-agent")
	customerCreator := createProblemHandlerUser(t, ctx, client, customerTenant.ID, "msp-customer-creator")
	p := createProblemHandlerProblem(t, ctx, service, customerTenant.ID, customerCreator.ID)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/problems/%d", p.ID), nil)
	req.Header.Set("X-Tenant-ID", strconv.Itoa(homeTenant.ID))
	req.Header.Set("X-User-ID", strconv.Itoa(user.ID))
	req.Header.Set("X-User-Role", "super_admin")
	req.Header.Set("X-MSP-Customer-ID", strconv.Itoa(customerTenant.ID))
	req.Header.Set("X-MSP-Allowed-Customer-ID", strconv.Itoa(customerTenant.ID))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var res common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, common.SuccessCode, res.Code)
	data := res.Data.(map[string]interface{})
	require.Equal(t, float64(customerTenant.ID), data["tenantId"])

	wSameTenant := performProblemRequestWithRole(r, http.MethodGet, fmt.Sprintf("/api/v1/problems/%d", p.ID), nil, customerTenant.ID, user.ID, "super_admin")
	require.Equal(t, http.StatusOK, wSameTenant.Code)
}

func TestProblemHTTPHandlerGetDeniesUnauthorizedMSPTenant(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	homeTenant := createProblemHandlerTenant(t, ctx, client, "msp-denied-home")
	customerTenant := createProblemHandlerTenant(t, ctx, client, "msp-denied-customer")
	allowedTenant := createProblemHandlerTenant(t, ctx, client, "msp-denied-allowed")
	user := createProblemHandlerUser(t, ctx, client, homeTenant.ID, "msp-denied-agent")
	customerCreator := createProblemHandlerUser(t, ctx, client, customerTenant.ID, "msp-denied-customer-creator")
	p := createProblemHandlerProblem(t, ctx, service, customerTenant.ID, customerCreator.ID)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/problems/%d", p.ID), nil)
	req.Header.Set("X-Tenant-ID", strconv.Itoa(homeTenant.ID))
	req.Header.Set("X-User-ID", strconv.Itoa(user.ID))
	req.Header.Set("X-User-Role", "super_admin")
	req.Header.Set("X-MSP-Customer-ID", strconv.Itoa(customerTenant.ID))
	req.Header.Set("X-MSP-Allowed-Customer-ID", strconv.Itoa(allowedTenant.ID))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)

	var res common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, 2003, res.Code)
}

func TestProblemHTTPHandlerUpdateAndLifecycle(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenant := createProblemHandlerTenant(t, ctx, client, "http-lc")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "http-lc")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	// Update Problem
	updateReq := dto.UpdateProblemRequest{
		Title: strPtr("Updated Title HTTP"),
	}
	w := performProblemRequest(r, "PUT", fmt.Sprintf("/api/v1/problems/%d", p.ID), updateReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, w.Code)

	// Investigate Problem
	wInv := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/investigate", p.ID), nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wInv.Code)
	var resInv common.Response
	require.NoError(t, json.Unmarshal(wInv.Body.Bytes(), &resInv))
	assert.Equal(t, 0, resInv.Code)

	// Update Root Cause
	rcReq := dto.UpdateProblemRootCauseRequest{
		RootCause: "Network driver deadlock",
	}
	wRC := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/root-cause", p.ID), rcReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wRC.Code)

	// Update Solution
	solReq := dto.UpdateProblemResolutionRequest{
		Workaround: "Restart driver service",
		Resolution: "Patched kernel driver",
	}
	wSol := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/solution", p.ID), solReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wSol.Code)

	statusResolved := "resolved"
	wResolved := performProblemRequest(r, "PUT", fmt.Sprintf("/api/v1/problems/%d", p.ID), dto.UpdateProblemRequest{
		Status: &statusResolved,
	}, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wResolved.Code)

	// Close Problem
	closeReq := dto.CloseProblemRequest{
		Resolution: "Verified resolution in staging",
	}
	wClose := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/close", p.ID), closeReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wClose.Code)

	// Get Stats
	wStats := performProblemRequest(r, "GET", "/api/v1/problems/stats", nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wStats.Code)

	// Delete Problem
	wDel := performProblemRequest(r, "DELETE", fmt.Sprintf("/api/v1/problems/%d", p.ID), nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wDel.Code)
}

func TestProblemHTTPHandlerAssociations(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenant := createProblemHandlerTenant(t, ctx, client, "http-assoc")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "http-assoc")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	ticket1, err := client.Ticket.Create().
		SetTitle("T1").SetTicketNumber("T-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	// Add Association
	assocReq := dto.ProblemAssociationRequest{
		RelatedType: "ticket",
		RelatedIDs:  []int{ticket1.ID},
	}
	w := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/associations", p.ID), assocReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, w.Code)

	// Get Associations
	wGet := performProblemRequest(r, "GET", fmt.Sprintf("/api/v1/problems/%d/associations", p.ID), nil, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wGet.Code)

	// Remove Association
	remReq := dto.ProblemRemoveAssociationRequest{
		RelatedType: "ticket",
		RelatedID:   ticket1.ID,
	}
	wRem := performProblemRequest(r, "DELETE", fmt.Sprintf("/api/v1/problems/%d/associations", p.ID), remReq, tenant.ID, user.ID)
	require.Equal(t, http.StatusOK, wRem.Code)
}

func TestProblemHTTPHandlerCrossTenantIsolation(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenantA := createProblemHandlerTenant(t, ctx, client, "http-iso-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "http-iso-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "http-iso-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "http-iso-b")

	problemA := createProblemHandlerProblem(t, ctx, service, tenantA.ID, userA.ID)

	// Tenant B attempts GET Tenant A problem
	wGet := performProblemRequestWithRole(r, "GET", fmt.Sprintf("/api/v1/problems/%d", problemA.ID), nil, tenantB.ID, userB.ID, "super_admin")
	require.Equal(t, http.StatusNotFound, wGet.Code)
	var resGet common.Response
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &resGet))
	assert.Equal(t, common.NotFoundErrorCode, resGet.Code)

	// Tenant B attempts PUT Tenant A problem
	updateReq := dto.UpdateProblemRequest{Title: strPtr("Hacked")}
	wPut := performProblemRequest(r, "PUT", fmt.Sprintf("/api/v1/problems/%d", problemA.ID), updateReq, tenantB.ID, userB.ID)
	require.Equal(t, http.StatusInternalServerError, wPut.Code)
	var resPut common.Response
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &resPut))
	assert.Equal(t, common.InternalErrorCode, resPut.Code)

	// Tenant B attempts POST Investigate Tenant A problem
	wInv := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/investigate", problemA.ID), nil, tenantB.ID, userB.ID)
	require.Equal(t, http.StatusNotFound, wInv.Code)
	var resInv common.Response
	require.NoError(t, json.Unmarshal(wInv.Body.Bytes(), &resInv))
	assert.Equal(t, common.NotFoundErrorCode, resInv.Code)

	// Tenant B attempts DELETE Tenant A problem
	wDel := performProblemRequest(r, "DELETE", fmt.Sprintf("/api/v1/problems/%d", problemA.ID), nil, tenantB.ID, userB.ID)
	require.Equal(t, http.StatusInternalServerError, wDel.Code)
	var resDel common.Response
	require.NoError(t, json.Unmarshal(wDel.Body.Bytes(), &resDel))
	assert.Equal(t, common.InternalErrorCode, resDel.Code)
}

func TestProblemHTTPHandlerGetProjectsActionsAndFailsClosedWithoutActorIdentity(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenant := createProblemHandlerTenant(t, ctx, client, "http-actions")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "http-actions")
	prob := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	type problemEnvelope struct {
		Code int                 `json:"code"`
		Data dto.ProblemResponse `json:"data"`
	}

	t.Run("projects detail actions on get only", func(t *testing.T) {
		w := performProblemRequestWithRole(r, http.MethodGet, fmt.Sprintf("/api/v1/problems/%d", prob.ID), nil, tenant.ID, user.ID, "super_admin")
		require.Equal(t, http.StatusOK, w.Code)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		detailData, ok := raw["data"].(map[string]any)
		require.True(t, ok)
		require.Contains(t, detailData, "actions")

		var res problemEnvelope
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		require.True(t, res.Data.Actions["edit"].Allowed)
		require.True(t, res.Data.Actions["start_investigation"].Allowed)
		require.True(t, res.Data.Actions["resolve"].Allowed)
		require.False(t, res.Data.Actions["close"].Allowed)
		require.NotEmpty(t, res.Data.Actions["close"].Reason)
	})

	t.Run("list response stays free of actions", func(t *testing.T) {
		w := performProblemRequest(r, http.MethodGet, "/api/v1/problems?page=1&pageSize=10", nil, tenant.ID, user.ID)
		require.Equal(t, http.StatusOK, w.Code)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		data, ok := raw["data"].(map[string]any)
		require.True(t, ok)
		problems, ok := data["problems"].([]any)
		require.True(t, ok)
		require.Len(t, problems, 1)
		problemJSON, ok := problems[0].(map[string]any)
		require.True(t, ok)
		require.NotContains(t, problemJSON, "actions")

		var res struct {
			Code int `json:"code"`
			Data struct {
				Problems []dto.ProblemResponse `json:"problems"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		require.Len(t, res.Data.Problems, 1)
		require.Nil(t, res.Data.Problems[0].Actions)
	})

	for _, tc := range []struct {
		name     string
		tenantID int
		userID   int
		role     string
	}{
		{name: "missing role", tenantID: tenant.ID, userID: user.ID},
		{name: "missing user", tenantID: tenant.ID, role: "super_admin"},
		{name: "missing tenant", userID: user.ID, role: "super_admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := performProblemRequestWithRole(r, http.MethodGet, fmt.Sprintf("/api/v1/problems/%d", prob.ID), nil, tc.tenantID, tc.userID, tc.role)
			require.Equal(t, http.StatusUnauthorized, w.Code)

			var res common.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
			require.Equal(t, common.AuthErrorCode, res.Code)
		})
	}
}
