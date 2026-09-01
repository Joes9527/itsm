package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/middleware"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap/zaptest"
)

type fakeProblemConversionService struct {
	result      *problemDomain.Problem
	err         error
	tenantID    int
	incidentID  int
	actorUserID int
	req         dto.ConvertIncidentToProblemRequest
	called      bool
}

func (f *fakeProblemConversionService) CreateFromIncident(
	_ context.Context,
	tenantID, incidentID, actorUserID int,
	req dto.ConvertIncidentToProblemRequest,
) (*problemDomain.Problem, error) {
	f.called = true
	f.tenantID = tenantID
	f.incidentID = incidentID
	f.actorUserID = actorUserID
	f.req = req
	return f.result, f.err
}

func setupTestIncidentController(t *testing.T) (*gin.Engine, *IncidentController) {
	gin.SetMode(gin.TestMode)

	// 创建内存数据库
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")

	// 创建 logger
	logger := zaptest.NewLogger(t).Sugar()

	// 创建服务
	incidentService := service.NewIncidentService(client, logger, workitemnumber.NewPostgreSQLAllocator())

	// 创建控制器
	incidentController := NewIncidentController(incidentService, nil, nil, nil, nil, &fakeProblemConversionService{}, logger)

	// 创建路由
	r := gin.New()
	r.Use(gin.Recovery())

	// 注册路由
	r.GET("/api/v1/incidents", incidentController.ListIncidents)

	return r, incidentController
}

func TestConvertToProblemControllerUsesConversionServiceAndPublicMapper(t *testing.T) {
	workItemID := 912
	fake := &fakeProblemConversionService{result: &problemDomain.Problem{
		ID:          41,
		Title:       "Mapped domain problem",
		Description: "Mapped description",
		Status:      "open",
		Priority:    "high",
		Category:    "platform",
		RootCause:   "Mapped root cause",
		Impact:      "high",
		CreatedBy:   73,
		TenantID:    19,
		CreatedAt:   time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 8, 28, 10, 1, 0, 0, time.UTC),
		WorkItemID:  &workItemID,
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, fake, zaptest.NewLogger(t).Sugar())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 19)
		c.Set("user_id", 73)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 19})
		c.Next()
	})
	router.POST("/api/v1/incidents/:id/convert-to-problem", controller.ConvertToProblem)

	body := []byte(`{"title":"Custom problem","description":"Custom description","rootCause":"Hypothesis"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/27/convert-to-problem", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 0, response.Code)
	data, ok := response.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Mapped domain problem", data["title"])
	assert.EqualValues(t, workItemID, data["workItemId"])
	assert.Equal(t, "Mapped root cause", data["rootCause"])
	assert.NotContains(t, data, "work_item_id", "controller must return the public Problem DTO, not an Ent model")
	assert.Equal(t, 19, fake.tenantID)
	assert.Equal(t, 27, fake.incidentID)
	assert.Equal(t, 73, fake.actorUserID)
	assert.Equal(t, "Custom problem", fake.req.Title)
}

func TestConvertToProblemControllerUsesAuthorizedMSPCustomerTenant(t *testing.T) {
	workItemID := 913
	fake := &fakeProblemConversionService{result: &problemDomain.Problem{
		ID: 42, Title: "Customer problem", Status: "open", Priority: "high",
		CreatedBy: 73, TenantID: 200, WorkItemID: &workItemID,
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, fake, zaptest.NewLogger(t).Sugar())
	router := gin.New()
	customerTenantID := 200
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 500)
		c.Set("user_id", 73)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 500})
		c.Set(middleware.MSPContextKey, &middleware.MSPContext{
			IsMSP:            true,
			MSPUserID:        73,
			CustomerTenantID: &customerTenantID,
			AllowedCustomers: []int{200, 300},
		})
		c.Next()
	})
	router.POST("/api/v1/incidents/:id/convert-to-problem", controller.ConvertToProblem)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/28/convert-to-problem", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, fake.called)
	assert.Equal(t, customerTenantID, fake.tenantID)
	assert.Equal(t, 28, fake.incidentID)
}

func TestConvertToProblemControllerDeniesUnauthorizedMSPCustomerTenant(t *testing.T) {
	workItemID := 914
	fake := &fakeProblemConversionService{result: &problemDomain.Problem{
		ID: 43, Title: "Must not be created", Status: "open", Priority: "high",
		CreatedBy: 73, TenantID: 999, WorkItemID: &workItemID,
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, fake, zaptest.NewLogger(t).Sugar())
	router := gin.New()
	deniedTenantID := 999
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 500)
		c.Set("user_id", 73)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 500})
		c.Set(middleware.MSPContextKey, &middleware.MSPContext{
			IsMSP:            true,
			MSPUserID:        73,
			CustomerTenantID: &deniedTenantID,
			AllowedCustomers: []int{200, 300},
		})
		c.Next()
	})
	router.POST("/api/v1/incidents/:id/convert-to-problem", controller.ConvertToProblem)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/29/convert-to-problem", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, fake.called, "tenant denial must happen before conversion service invocation")
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, 2003, response.Code)
	assert.Equal(t, "MSP员工无权访问该客户租户", response.Message)
}

func TestIncidentController_ListIncidents(t *testing.T) {
	r, _ := setupTestIncidentController(t)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
	}{
		{
			name:           "成功获取事件列表",
			queryParams:    "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "带分页参数",
			queryParams:    "page=1&pageSize=10",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/incidents"
			if tt.queryParams != "" {
				path += "?" + tt.queryParams
			}

			req, err := http.NewRequest("GET", path, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("tenant_id", 1)

			r.ServeHTTP(w, req)
		})
	}
}

func TestIncidentDetailHasActionsButListDoesNot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:incident_detail_actions?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("detail").SetCode("detail").SetDomain("detail.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("detail-user").SetEmail("detail@example.com").SetName("Detail User").
		SetPasswordHash("x").SetRole("super_admin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().
		SetTitle("Detail WorkItem").SetTicketNumber("TKT-DETAIL").SetStatus("open").SetPriority("high").
		SetRequesterID(user.ID).SetTenantID(tenant.ID).SetRecordClass("incident").Save(ctx)
	require.NoError(t, err)
	incidentEntity, err := client.Incident.Create().SetIncidentNumber("INC-DETAIL").
		SetWorkItemID(workItem.ID).Save(ctx)
	require.NoError(t, err)

	controller := NewIncidentController(service.NewIncidentService(client, zaptest.NewLogger(t).Sugar(), workitemnumber.NewPostgreSQLAllocator()), nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("role", "super_admin")
		c.Set("tenant_id", tenant.ID)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenant.ID, Tenant: tenant})
		c.Next()
	})
	router.GET("/api/v1/incidents", controller.ListIncidents)
	router.GET("/api/v1/incidents/:id", controller.GetIncident)

	detail := performIncidentRequest(t, router, http.MethodGet, "/api/v1/incidents/"+strconv.Itoa(incidentEntity.ID))
	require.Equal(t, http.StatusOK, detail.Code)
	detailData := responseDataMap(t, detail.Body.Bytes())
	require.Contains(t, detailData, "actions")

	list := performIncidentRequest(t, router, http.MethodGet, "/api/v1/incidents")
	require.Equal(t, http.StatusOK, list.Code)
	listData := responseDataMap(t, list.Body.Bytes())
	incidents, ok := listData["incidents"].([]any)
	require.True(t, ok)
	require.Len(t, incidents, 1)
	listIncident, ok := incidents[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, listIncident, "actions")
}

func TestGetIncidentRequiresAuthenticatedActionActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:incident_detail_actor?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant, err := client.Tenant.Create().SetName("actor").SetCode("actor").SetDomain("actor.test").SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	controller := NewIncidentController(service.NewIncidentService(client, zaptest.NewLogger(t).Sugar(), workitemnumber.NewPostgreSQLAllocator()), nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())

	for _, testCase := range []struct {
		name   string
		userID int
		role   string
	}{{name: "missing user", role: "super_admin"}, {name: "missing role", userID: 9}} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("user_id", testCase.userID)
				c.Set("role", testCase.role)
				c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenant.ID, Tenant: tenant})
				c.Next()
			})
			router.GET("/api/v1/incidents/:id", controller.GetIncident)
			response := performIncidentRequest(t, router, http.MethodGet, "/api/v1/incidents/1")
			require.Equal(t, http.StatusUnauthorized, response.Code)
		})
	}
}

func performIncidentRequest(t *testing.T, router http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func responseDataMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var response struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.NotNil(t, response.Data)
	return response.Data
}
