package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/intake"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap/zaptest"
)

type conversionTestAllocator struct{ next int }

func (a *conversionTestAllocator) Allocate(_ context.Context, _ *ent.Client, _ int, _ time.Time) (string, error) {
	a.next++
	return fmt.Sprintf("TKT-CONVERSION-%06d", a.next), nil
}

func setupTestIncidentController(t *testing.T) (*gin.Engine, *IncidentController) {
	gin.SetMode(gin.TestMode)

	// 创建内存数据库
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")

	// 创建 logger
	logger := zaptest.NewLogger(t).Sugar()

	// 创建服务
	incidentService := service.NewIncidentService(client, logger)

	// 创建控制器
	incidentController := NewIncidentController(incidentService, nil, nil, nil, nil, logger)

	// 创建路由
	r := gin.New()
	r.Use(gin.Recovery())

	// 注册路由
	r.GET("/api/v1/incidents", incidentController.ListIncidents)

	return r, incidentController
}

type conversionControllerFixture struct {
	client    *ent.Client
	router    *gin.Engine
	tenant    *ent.Tenant
	actor     *ent.User
	requester *ent.User
	incident  *ent.Incident
	provider  *ent.Tenant
}

func newConversionControllerFixture(t *testing.T, msp bool) *conversionControllerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:conversion_%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := zaptest.NewLogger(t).Sugar()
	tenant := client.Tenant.Create().SetName("Conversion customer").SetCode("conversion-customer").SetStatus("active").SaveX(ctx)
	actorTenant := tenant
	actorRole := "agent"
	if msp {
		client.Tenant.UpdateOneID(tenant.ID).SetType("msp_customer").ExecX(ctx)
		actorTenant = client.Tenant.Create().SetName("Conversion provider").SetCode("conversion-provider").SetType("msp_provider").SetStatus("active").SaveX(ctx)
		actorRole = "admin"
	}
	actorCreate := client.User.Create().SetTenantID(actorTenant.ID).SetUsername("conversion-actor").SetName("Conversion Actor").SetEmail("conversion-actor@example.test").SetPasswordHash("test").SetRole(actorRole).SetActive(true)
	if msp {
		actorCreate.SetMspRole("provider_agent")
	}
	actor := actorCreate.SaveX(ctx)
	requester := actor
	if msp {
		client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(tenant.ID).SetRole("primary").SaveX(ctx)
		requester = client.User.Create().SetTenantID(tenant.ID).SetUsername("customer-requester").SetName("Customer Requester").SetEmail("customer-requester@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(ctx)
	}
	effectiveRole := "agent"
	if msp {
		effectiveRole = "msp_tech"
	}
	role := client.Role.Create().SetTenantID(tenant.ID).SetCode(effectiveRole).SetName("Conversion role").SetIsActive(true).SaveX(ctx)
	for _, permission := range []struct{ resource, action string }{{"problem", "read"}, {"problem", "write"}, {"incident", "read"}, {"incident", "write"}} {
		p := client.Permission.Create().SetTenantID(tenant.ID).SetCode(permission.resource + ":" + permission.action).SetName(permission.resource + ":" + permission.action).SetResource(permission.resource).SetAction(permission.action).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(ctx)
	}
	if msp {
		p := client.Permission.Create().SetTenantID(tenant.ID).SetCode("problem:create_on_behalf").SetName("problem:create_on_behalf").SetResource("problem").SetAction("create_on_behalf").SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(ctx)
	}
	client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType("problem").SetIsDefault(true).SetIsActive(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	sourceItem := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(requester.ID).SetOpenedByID(requester.ID).SetTitle("VPN incident").SetDescription("VPN unavailable").SetTicketNumber("INC-CONVERSION").SetRecordClass("incident").SetType("incident").SetStatus("new").SetPriority("high").SaveX(ctx)
	incident := client.Incident.Create().SetWorkItemID(sourceItem.ID).SetIncidentNumber("INC-CONVERSION").SetSeverity("high").SetImpact("high").SetDetectedAt(time.Now()).SaveX(ctx)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(problemDomain.NewService(problemDomain.NewEntRepository(client), logger)))
	resolver := intake.NewResolver(service_catalog.NewService(nil, client, logger), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	app := intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(&conversionTestAllocator{}), sameTransactionDirectory{})
	controller := NewIncidentController(nil, nil, nil, nil, nil, logger)
	controller.SetCreationApplication(app)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenant.ID)
		c.Set("user_id", actor.ID)
		c.Set("role", effectiveRole)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenant.ID, Tenant: tenant})
		c.Next()
	})
	router.POST("/api/v1/incidents/:id/convert-to-problem", controller.ConvertToProblem)
	return &conversionControllerFixture{client: client, router: router, tenant: tenant, actor: actor, requester: requester, incident: incident, provider: actorTenant}
}

func executeConversion(t *testing.T, f *conversionControllerFixture, key, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/convert-to-problem", f.incident.ID), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), w.Body.String())
	require.Equal(t, common.SuccessCode, response.Code, w.Body.String())
	data, ok := response.Data.(map[string]any)
	require.True(t, ok)
	return w.Code, data
}

func TestConvertToProblemControllerCreatesAndReplaysSharedReceipt(t *testing.T) {
	f := newConversionControllerFixture(t, false)
	status, first := executeConversion(t, f, "local-conversion", `{"title":"Custom problem","description":"Custom description","rootCause":"Hypothesis"}`)
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, "problem", first["recordClass"])
	assert.Equal(t, false, first["replayed"])
	status, replay := executeConversion(t, f, "local-conversion", `{"title":"Custom problem","description":"Custom description","rootCause":"Hypothesis"}`)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, replay["replayed"])
	assert.Equal(t, first["workItemId"], replay["workItemId"])
	assert.Equal(t, 1, f.client.Problem.Query().CountX(context.Background()))
	assert.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(context.Background()))
	assert.Equal(t, 2, f.client.Ticket.Query().CountX(context.Background()))
}

func TestConvertToProblemControllerAllowsExplicitNativeOnBehalfRequester(t *testing.T) {
	f := newConversionControllerFixture(t, false)
	ctx := context.Background()
	requester := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("native-requester").SetName("Native Requester").SetEmail("native-requester@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(ctx)
	role := f.client.Role.Query().OnlyX(ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("problem:create_on_behalf").SetName("problem:create_on_behalf").SetResource("problem").SetAction("create_on_behalf").SaveX(ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	body := fmt.Sprintf(`{"requesterId":%d,"title":"Native delegated problem"}`, requester.ID)
	status, result := executeConversion(t, f, "native-on-behalf", body)
	require.Equal(t, http.StatusCreated, status)
	created := f.client.Ticket.GetX(ctx, int(result["workItemId"].(float64)))
	assert.Equal(t, requester.ID, created.RequesterID)
	assert.Equal(t, f.actor.ID, created.OpenedByID)
}

func TestConvertToProblemControllerUsesExplicitMSPCustomerRequesterAndReplays(t *testing.T) {
	f := newConversionControllerFixture(t, true)
	body := fmt.Sprintf(`{"requesterId":%d,"title":"Customer problem"}`, f.requester.ID)
	status, first := executeConversion(t, f, "msp-conversion", body)
	require.Equal(t, http.StatusCreated, status)
	status, replay := executeConversion(t, f, "msp-conversion", body)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, replay["replayed"])
	assert.Equal(t, first["workItemId"], replay["workItemId"])
	created := f.client.Ticket.Query().Where(ticket.RecordClassEQ("problem")).OnlyX(context.Background())
	assert.Equal(t, f.tenant.ID, created.TenantID)
	assert.Equal(t, f.requester.ID, created.RequesterID)
	assert.Equal(t, f.actor.ID, created.OpenedByID)
	receipt := f.client.IntakeRequest.Query().OnlyX(context.Background())
	assert.Equal(t, f.provider.ID, receipt.ActorTenantID)
	assert.Equal(t, f.actor.ID, receipt.ActorID)
}

func TestConvertToProblemControllerRejectsNonPositiveRequester(t *testing.T) {
	for _, requesterID := range []int{0, -1} {
		t.Run(strconv.Itoa(requesterID), func(t *testing.T) {
			f := newConversionControllerFixture(t, true)
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/convert-to-problem", f.incident.ID), bytes.NewBufferString(fmt.Sprintf(`{"requesterId":%d}`, requesterID)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "invalid-requester")
			recorder := httptest.NewRecorder()
			f.router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), "invalid creation body")
			assert.Zero(t, f.client.IntakeRequest.Query().CountX(context.Background()))
			assert.Zero(t, f.client.Problem.Query().CountX(context.Background()))
		})
	}
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

	controller := NewIncidentController(service.NewIncidentService(client, zaptest.NewLogger(t).Sugar()), nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
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
	controller := NewIncidentController(service.NewIncidentService(client, zaptest.NewLogger(t).Sugar()), nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())

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
