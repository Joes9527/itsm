package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/google/uuid"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	entpermission "itsm-backend/ent/permission"
	entprocessbinding "itsm-backend/ent/processbinding"
	entrole "itsm-backend/ent/role"
	entrolepermission "itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// setupTestTicketController wires the ticket controller with a middleware that
// injects tenant_id and user_id from headers (X-Test-Tenant, X-Test-User),
// defaulting to 1. To simulate a missing tenant or user, send 0.
func setupTestTicketController(t *testing.T) (*gin.Engine, *ent.Client, *TicketController) {
	gin.SetMode(gin.TestMode)

	// 每个测试用例使用独立的内存库名（同 test_helper.go 的 SetupTestDB 约定），避免多个测试函数
	// 共享同一个 sqlite shared-cache 内存库时，tickets.ticket_number 的全局唯一索引跨租户碰撞——
	// 号码生成器按 tenant 维度查询"当月最大序号"，但唯一约束是全局的，一旦某个较早测试的租户已经
	// 占用了当月的 000001 号，后面任何新租户创建的第一张工单都会确定性撞号重试耗尽失败。
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:ticketctrl_%s_%d?mode=memory&cache=shared&_fk=1", t.Name(), mathrand.Int31()))
	// authorization.HasResourcePermission 缓存的 key 是 role+tenantID，每个测试都是全新的内存库、
	// tenant 自增 ID 又会从 1 重新开始，不同测试用同一个 role 名字时会撞进同一个缓存 key，
	// 读到另一个测试早前缓存下来的权限集。每个测试用例开始前先清一次，保证互不影响。
	authorization.InvalidateAllPermissionCaches()

	logger := zaptest.NewLogger(t).Sugar()

	ticketService := service.NewTicketServiceForTest(client, logger)
	var ticketDependencyService *service.TicketDependencyService

	ticketController := NewTicketController(ticketService, ticketDependencyService, nil, client, logger)

	// Wire the real shared Intake application: production TicketController.CreateTicket
	// now always goes through Resolve->Prepare->CreateExtension, never a direct
	// service Create method.
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(ticketService))
	resolver := intake.NewResolver(service_catalog.NewService(nil, client, logger), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	ticketController.SetCreationApplication(intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator())))

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		tenantID := 1
		if h := c.GetHeader("X-Test-Tenant"); h != "" {
			if v, err := strconv.Atoi(h); err == nil {
				tenantID = v
			}
		}
		userID := 1
		if h := c.GetHeader("X-Test-User"); h != "" {
			if v, err := strconv.Atoi(h); err == nil {
				userID = v
			}
		}
		// 阻断8：测试中间件需注入 role，否则 DataScope 会按空角色收窄到
		// OwnedOrAssigned 并使用默认 userID=1，导致列表测试返回 0 条。
		role := "admin"
		if h := c.GetHeader("X-Test-Role"); h != "" {
			role = h
		}
		c.Set("tenant_id", tenantID)
		c.Set("user_id", userID)
		c.Set("role", role)
		// Required by the shared Intake HTTP boundary
		// (middleware.ResolveRequestTenantID), not the legacy plain "tenant_id" key.
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenantID})
		c.Next()
	})

	r.POST("/api/v1/tickets", ticketController.CreateTicket)
	r.GET("/api/v1/tickets", ticketController.ListTickets)
	r.GET("/api/v1/tickets/:id", ticketController.GetTicket)
	r.PUT("/api/v1/tickets/:id", ticketController.UpdateTicket)
	r.DELETE("/api/v1/tickets/:id", ticketController.DeleteTicket)

	return r, client, ticketController
}

// seedTicketRolePermission grants roleCode a resource:action permission in tenantID,
// needed now that TicketController's write-path handlers call service.CanXxx (which
// queries role_permissions via authorization.HasResourcePermission) instead of allowing
// any authenticated caller through unconditionally. Idempotent per (tenantID, roleCode)
// / (tenantID, resource, action) so callers can grant several actions to the same role.
func seedTicketRolePermission(t *testing.T, client *ent.Client, tenantID int, roleCode, resource, action string) {
	t.Helper()
	ctx := context.Background()
	role, err := client.Role.Query().Where(entrole.TenantIDEQ(tenantID), entrole.CodeEQ(roleCode)).First(ctx)
	if ent.IsNotFound(err) {
		role, err = client.Role.Create().
			SetCode(roleCode).
			SetName(roleCode).
			SetTenantID(tenantID).
			Save(ctx)
	}
	require.NoError(t, err)

	perm, err := client.Permission.Query().Where(entpermission.TenantIDEQ(tenantID), entpermission.CodeEQ(resource+":"+action)).First(ctx)
	if ent.IsNotFound(err) {
		perm, err = client.Permission.Create().
			SetCode(resource + ":" + action).
			SetName(resource + ":" + action).
			SetResource(resource).
			SetAction(action).
			SetTenantID(tenantID).
			Save(ctx)
	}
	require.NoError(t, err)

	if !client.RolePermission.Query().Where(entrolepermission.TenantIDEQ(tenantID), entrolepermission.RoleIDEQ(role.ID), entrolepermission.PermissionIDEQ(perm.ID)).ExistX(ctx) {
		_, err = client.RolePermission.Create().
			SetRoleID(role.ID).
			SetPermissionID(perm.ID).
			SetTenantID(tenantID).
			Save(ctx)
		require.NoError(t, err)
	}
}

// seedTicketNoProcessBinding provisions an unconditional no-process workflow
// binding for the "ticket" (generic) business type: real Intake creation now
// always resolves an active workflow binding, and these controller tests are
// not exercising BPMN routing.
func seedTicketNoProcessBinding(t *testing.T, client *ent.Client, tenantID int) {
	t.Helper()
	ctx := context.Background()
	if client.ProcessBinding.Query().Where(entprocessbinding.TenantIDEQ(tenantID), entprocessbinding.BusinessTypeEQ("ticket")).ExistX(ctx) {
		return
	}
	client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType("ticket").SetIsDefault(true).
		SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
}

func createTestTenantAndUserForTicket(t *testing.T, client *ent.Client) (*ent.Tenant, *ent.User) {
	ctx := context.Background()
	uniqueID := uniqueTestID()

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("TEST" + uniqueID).
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("testuser" + uniqueID).
		SetEmail("test" + uniqueID + "@example.com").
		SetPasswordHash("hashedpassword").
		SetName("Test User").
		SetDepartment("IT").
		SetPhone("1234567890").
		SetActive(true).
		SetRole("end_user").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return tenant, user
}

// apiResp decodes the standard {code,message,data} envelope without coupling
// to a specific success DTO shape.
type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// doJSONRequest runs a request through the router and decodes the envelope. It
// fails the test if the body cannot be parsed. The HTTP status is returned so
// individual cases can assert both code + status if needed. (Renamed from
// doRequest to avoid collision with bpmn_workflow_controller_test.go.)
func doJSONRequest(t *testing.T, r http.Handler, req *http.Request) (apiResp, int) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp apiResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body=%s", w.Body.String())
	return resp, w.Code
}

func TestTicketController_CreateTicket(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)
	seedTicketRolePermission(t, client, tenant.ID, "end_user", "ticket", "write")
	seedTicketRolePermission(t, client, tenant.ID, "end_user", "ticket", "read")
	seedTicketNoProcessBinding(t, client, tenant.ID)

	tests := []struct {
		name         string
		request      dto.CreateTicketRequest
		tenantHeader string // "" uses middleware default (1); "0" simulates missing
		userHeader   string
		expectedCode int
	}{
		{
			name: "成功创建工单",
			request: dto.CreateTicketRequest{
				Title:       "测试工单",
				Description: "这是一个测试工单的详细描述",
				Priority:    "medium",
			},
			tenantHeader: strconv.Itoa(tenant.ID),
			userHeader:   strconv.Itoa(user.ID),
			expectedCode: common.SuccessCode,
		},
		{
			name: "标题为空",
			request: dto.CreateTicketRequest{
				Title:       "",
				Description: "描述",
				Priority:    "medium",
				Category:    "incident",
			},
			tenantHeader: strconv.Itoa(tenant.ID),
			userHeader:   strconv.Itoa(user.ID),
			expectedCode: common.ParamErrorCode,
		},
		{
			name: "描述为空",
			request: dto.CreateTicketRequest{
				Title:       "标题",
				Description: "",
				Priority:    "medium",
				Category:    "incident",
			},
			tenantHeader: strconv.Itoa(tenant.ID),
			userHeader:   strconv.Itoa(user.ID),
			expectedCode: common.ParamErrorCode,
		},
		{
			name: "缺少租户ID",
			request: dto.CreateTicketRequest{
				Title:       "测试工单",
				Description: "描述",
				Priority:    "medium",
				Category:    "incident",
			},
			tenantHeader: "0",
			userHeader:   strconv.Itoa(user.ID),
			expectedCode: common.AuthFailedCode,
		},
		{
			name: "缺少用户ID",
			request: dto.CreateTicketRequest{
				Title:       "测试工单",
				Description: "描述",
				Priority:    "medium",
				Category:    "incident",
			},
			tenantHeader: strconv.Itoa(tenant.ID),
			userHeader:   "0",
			expectedCode: common.AuthFailedCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.request)
			require.NoError(t, err)

			req, err := http.NewRequest("POST", "/api/v1/tickets", bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", uuid.NewString())
			req.Header.Set("X-Test-Tenant", tt.tenantHeader)
			req.Header.Set("X-Test-User", tt.userHeader)
			req.Header.Set("X-Test-Role", "end_user")

			resp, _ := doJSONRequest(t, r, req)
			assert.Equal(t, tt.expectedCode, resp.Code, "message=%s", resp.Message)

			// For success cases, the data envelope must contain the durable
			// creation receipt (workItemId/number/...), not the legacy Ticket
			// response DTO.
			if tt.expectedCode == common.SuccessCode {
				var created creation.CreateWorkItemResult
				require.NoError(t, json.Unmarshal(resp.Data, &created), "data=%s", string(resp.Data))
				assert.Positive(t, created.WorkItemID, "created ticket should have a positive WorkItemID")
			}
		})
	}
}

// TestTicketController_CreateTicket_IgnoresClientSuppliedSource proves the public
// POST /tickets endpoint cannot be spoofed into self-reporting
// source=service_catalog (a value that's supposed to be set only by the trusted
// internal service_request.Service.Create -> ticketSvc.CreateTicket call path).
// The real Intake creation contract (controller/ticket_creation.go
// ticketCreationCommand) now explicitly rejects any non-"manual" public source
// value rather than silently overwriting it — a stricter, fail-closed version of
// the same guarantee: a forged source can never reach persistence, and the
// legitimate (omitted/"manual") path still persists the ent schema default.
func TestTicketController_CreateTicket_IgnoresClientSuppliedSource(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)
	seedTicketRolePermission(t, client, tenant.ID, "end_user", "ticket", "write")
	seedTicketRolePermission(t, client, tenant.ID, "end_user", "ticket", "read")
	seedTicketNoProcessBinding(t, client, tenant.ID)

	forgedBody, err := json.Marshal(dto.CreateTicketRequest{
		Title:       "伪造来源的工单",
		Description: "尝试自报 source=service_catalog",
		Priority:    "medium",
		Source:      "service_catalog",
	})
	require.NoError(t, err)

	forgedReq, err := http.NewRequest("POST", "/api/v1/tickets", bytes.NewReader(forgedBody))
	require.NoError(t, err)
	forgedReq.Header.Set("Content-Type", "application/json")
	forgedReq.Header.Set("Idempotency-Key", uuid.NewString())
	forgedReq.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
	forgedReq.Header.Set("X-Test-User", strconv.Itoa(user.ID))
	forgedReq.Header.Set("X-Test-Role", "end_user")

	forgedResp, forgedStatus := doJSONRequest(t, r, forgedReq)
	assert.Equal(t, http.StatusBadRequest, forgedStatus)
	assert.Equal(t, common.ParamErrorCode, forgedResp.Code, "message=%s", forgedResp.Message)
	assert.Zero(t, client.Ticket.Query().CountX(context.Background()), "a forged source must never reach persistence")

	genuineBody, err := json.Marshal(dto.CreateTicketRequest{
		Title:       "真实手动创建的工单",
		Description: "不携带 source 字段",
		Priority:    "medium",
	})
	require.NoError(t, err)

	genuineReq, err := http.NewRequest("POST", "/api/v1/tickets", bytes.NewReader(genuineBody))
	require.NoError(t, err)
	genuineReq.Header.Set("Content-Type", "application/json")
	genuineReq.Header.Set("Idempotency-Key", uuid.NewString())
	genuineReq.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
	genuineReq.Header.Set("X-Test-User", strconv.Itoa(user.ID))
	genuineReq.Header.Set("X-Test-Role", "end_user")

	genuineResp, genuineStatus := doJSONRequest(t, r, genuineReq)
	require.Equal(t, http.StatusCreated, genuineStatus, "message=%s", genuineResp.Message)
	require.Equal(t, common.SuccessCode, genuineResp.Code, "message=%s", genuineResp.Message)

	var created creation.CreateWorkItemResult
	require.NoError(t, json.Unmarshal(genuineResp.Data, &created))
	stored, err := client.Ticket.Get(context.Background(), created.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "manual", stored.Source, "the legitimate path must persist the ent schema default(\"manual\")")
}

func TestTicketController_GetTicket(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)

	ctx := context.Background()
	ticket, err := client.Ticket.Create().
		SetTicketNumber("TKT-001").
		SetTitle("测试工单").
		SetDescription("描述").
		SetPriority("medium").
		SetStatus("open").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tests := []struct {
		name         string
		ticketID     string
		expectedCode int
	}{
		{
			name:         "成功获取工单",
			ticketID:     strconv.Itoa(ticket.ID),
			expectedCode: common.SuccessCode,
		},
		{
			name:         "无效的工单ID",
			ticketID:     "invalid",
			expectedCode: common.ParamErrorCode,
		},
		{
			name:         "不存在的工单ID",
			ticketID:     "999999",
			expectedCode: common.NotFoundCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/api/v1/tickets/"+tt.ticketID, nil)
			require.NoError(t, err)
			req.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))

			resp, _ := doJSONRequest(t, r, req)
			assert.Equal(t, tt.expectedCode, resp.Code, "message=%s", resp.Message)
		})
	}
}

func TestTicketController_ListTickets(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)

	ctx := context.Background()
	uniqueID := uniqueTestID()
	for i := 0; i < 3; i++ {
		_, err := client.Ticket.Create().
			SetTicketNumber(fmt.Sprintf("TKT-%s-%03d", uniqueID, i+1)).
			SetTitle(fmt.Sprintf("测试工单 %c", 'A'+i)).
			SetDescription(fmt.Sprintf("描述 %c", 'A'+i)).
			SetPriority("medium").
			SetStatus("open").
			SetRequesterID(user.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	tests := []struct {
		name         string
		queryParams  string
		expectedCode int
	}{
		{
			name:         "成功获取工单列表",
			queryParams:  "",
			expectedCode: common.SuccessCode,
		},
		{
			name:         "带分页参数",
			queryParams:  "page=1&pageSize=10",
			expectedCode: common.SuccessCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/tickets"
			if tt.queryParams != "" {
				path += "?" + tt.queryParams
			}

			req, err := http.NewRequest("GET", path, nil)
			require.NoError(t, err)
			req.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))

			resp, _ := doJSONRequest(t, r, req)
			assert.Equal(t, tt.expectedCode, resp.Code, "message=%s", resp.Message)

			// Verify the data is a paginated list, not an error envelope.
			var listResp dto.ListTicketsResponse
			require.NoError(t, json.Unmarshal(resp.Data, &listResp), "data=%s", string(resp.Data))
			assert.GreaterOrEqual(t, listResp.Total, 3, "should list all 3 seeded tickets")
			assert.GreaterOrEqual(t, len(listResp.Tickets), 3, "items slice should match total")
		})
	}
}

func TestTicketController_UpdateTicket(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)

	ctx := context.Background()
	ticket, err := client.Ticket.Create().
		SetTicketNumber("TKT-002").
		SetTitle("原始标题").
		SetDescription("原始描述").
		SetPriority("low").
		SetStatus("open").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 请求没有带 X-Test-Role，中间件默认用 "admin"——UpdateTicket 现在会用
	// service.CanEdit 二次校验 ticket:update，测试库是全新的，要先补上这条权限。
	seedTicketRolePermission(t, client, tenant.ID, "admin", "ticket", "update")

	tests := []struct {
		name         string
		ticketID     string
		request      dto.UpdateTicketRequest
		expectedCode int
	}{
		{
			name:     "成功更新工单",
			ticketID: strconv.Itoa(ticket.ID),
			request: dto.UpdateTicketRequest{
				Title:       "更新后的标题",
				Description: "更新后的详细描述内容足够长以通过校验",
				Priority:    "high",
			},
			expectedCode: common.SuccessCode,
		},
		{
			name:     "无效的工单ID格式",
			ticketID: "abc",
			request: dto.UpdateTicketRequest{
				Title: "更新后的标题",
			},
			expectedCode: common.ParamErrorCode,
		},
		{
			name:     "工单不存在",
			ticketID: "99999",
			request: dto.UpdateTicketRequest{
				Title: "更新后的标题",
			},
			expectedCode: common.NotFoundCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.request)
			require.NoError(t, err)

			req, err := http.NewRequest("PUT", "/api/v1/tickets/"+tt.ticketID, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
			req.Header.Set("X-Test-User", strconv.Itoa(user.ID))

			resp, _ := doJSONRequest(t, r, req)
			assert.Equal(t, tt.expectedCode, resp.Code, "message=%s", resp.Message)

			// For success cases, the returned ticket should reflect the update.
			if tt.expectedCode == common.SuccessCode {
				var updated dto.TicketResponse
				require.NoError(t, json.Unmarshal(resp.Data, &updated), "data=%s", string(resp.Data))
				assert.Equal(t, tt.request.Title, updated.Title)
				assert.Equal(t, tt.request.Priority, updated.Priority)
			}
		})
	}
}

func TestTicketController_DeleteTicket(t *testing.T) {
	r, client, _ := setupTestTicketController(t)
	defer client.Close()

	tenant, user := createTestTenantAndUserForTicket(t, client)

	ctx := context.Background()
	ticket, err := client.Ticket.Create().
		SetTicketNumber("TKT-003").
		SetTitle("待删除工单").
		SetDescription("将被删除").
		SetPriority("low").
		SetStatus("open").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 同 TestTicketController_UpdateTicket：默认角色 "admin" 需要先补上 ticket:delete，
	// DeleteTicket 现在会用 service.CanDelete 二次校验。
	seedTicketRolePermission(t, client, tenant.ID, "admin", "ticket", "delete")

	tests := []struct {
		name         string
		ticketID     string
		expectedCode int
	}{
		{
			name:         "成功删除工单",
			ticketID:     strconv.Itoa(ticket.ID),
			expectedCode: common.SuccessCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("DELETE", "/api/v1/tickets/"+tt.ticketID, nil)
			require.NoError(t, err)
			req.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
			req.Header.Set("X-Test-User", strconv.Itoa(user.ID))

			resp, _ := doJSONRequest(t, r, req)
			assert.Equal(t, tt.expectedCode, resp.Code, "message=%s", resp.Message)

			// Verify deletion: a follow-up GET should return NotFoundCode.
			verifyReq, err := http.NewRequest("GET", "/api/v1/tickets/"+tt.ticketID, nil)
			require.NoError(t, err)
			verifyReq.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
			verifyResp, _ := doJSONRequest(t, r, verifyReq)
			assert.Equal(t, common.NotFoundCode, verifyResp.Code, "deleted ticket should 404")
		})
	}
}
