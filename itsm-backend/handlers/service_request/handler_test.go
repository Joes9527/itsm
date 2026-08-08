package service_request

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// srSeq 生成唯一后缀，避免跨用例命名冲突
var srSeq int64

func srUID() string {
	srSeq++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), srSeq)
}

// srAuth 注入服务请求 handler 依赖的 c.Get("tenant_id"/"user_id"/"role"/"department")
func srAuth(tid, uid int) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("tenant_id", tid)
		c.Set("user_id", uid)
		c.Set("role", "manager")
		c.Set("department", "IT")
		c.Next()
	}
}

func srDoReq(t *testing.T, r *gin.Engine, method, path string, body interface{}) *common.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return &resp
}

func srStr(resp *common.Response) string {
	b, _ := json.Marshal(resp)
	return string(b)
}

// srSetup 组装服务请求 handler，并播种一个租户 + 一个服务目录（CITypeID=0，避免关联 CI 分支）
func srSetup(t *testing.T) (*gin.Engine, *ent.Client, int, int, int) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "sr_test.db") + "?_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	logger := zaptest.NewLogger(t).Sugar()

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("SRTenant").
		SetCode("SR" + srUID()).
		SetDomain("sr.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	// 播种一个服务目录（无 CI 类型，走简单路径）
	scRepo := service_catalog.NewEntRepository(client)
	scSvc := service_catalog.NewService(scRepo, client, logger)
	cat, err := scSvc.Create(ctx, "SRCatalog-"+srUID(), "software", "for test", 0, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(repo, scRepo, cmdbRepo, client, logger, ticketSvc)
	h := NewHandler(svc)

	user, err := client.User.Create().
		SetUsername("sr-user-" + srUID()).
		SetEmail("sr-" + srUID() + "@example.com").
		SetName("SR User").
		SetPasswordHash("hash").
		SetRole("manager").
		SetDepartment("IT").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	uid := user.ID
	r := gin.New()
	r.Use(srAuth(tenant.ID, uid))
	r.POST("/api/v1/service-requests", h.Create)
	r.GET("/api/v1/service-requests", h.List)
	r.GET("/api/v1/service-requests/by-ticket/:ticketId", h.GetByTicket)
	r.GET("/api/v1/service-requests/:id", h.Get)
	r.PUT("/api/v1/service-requests/:id", h.Update)
	r.DELETE("/api/v1/service-requests/:id", h.Delete)
	return r, client, tenant.ID, uid, cat.ID
}

func srCreateOne(t *testing.T, r *gin.Engine, catalogID int) int {
	t.Helper()
	req := dto.CreateServiceRequestRequest{
		CatalogID:     catalogID,
		Title:         "Req-" + srUID(),
		Reason:        "need resource",
		ComplianceAck: true,
	}
	resp := srDoReq(t, r, "POST", "/api/v1/service-requests", req)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	data := resp.Data.(map[string]interface{})
	return int(data["id"].(float64))
}

func TestServiceRequestHandler_Create_Success(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	req := dto.CreateServiceRequestRequest{
		CatalogID:     catID,
		Title:         "NewServer-" + srUID(),
		Reason:        "capacity",
		ComplianceAck: true,
	}
	resp := srDoReq(t, r, "POST", "/api/v1/service-requests", req)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	data := resp.Data.(map[string]interface{})
	assert.EqualValues(t, catID, data["catalogId"])
	assert.Greater(t, data["ticketId"], float64(0), "Create 必须委托创建关联 Ticket 并回写 ticketId")
}

func TestHandler_Get_IncludesCustomFieldValues(t *testing.T) {
	// srSetup 返回 (r, client, tenantID, userID, catalogID)；这里不用它预置的那个
	// catalogID（没有字段定义），另外建一个带字段的 ServiceCatalog。
	r, client, tenantID, _, _ := srSetup(t)
	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(context.Background(), "云主机申请-"+srUID(), "software", "desc", 1, tenantID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}})
	require.NoError(t, err)

	createReq := dto.CreateServiceRequestRequest{
		CatalogID: catalog.ID, Title: "申请", Reason: "测试", ComplianceAck: true,
		FormData: map[string]interface{}{"environment": "staging"},
	}
	createResp := srDoReq(t, r, "POST", "/api/v1/service-requests", createReq)
	require.Equal(t, common.SuccessCode, createResp.Code, "body=%s", srStr(createResp))
	created := createResp.Data.(map[string]interface{})
	id := int(created["id"].(float64))

	getResp := srDoReq(t, r, "GET", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	require.Equal(t, common.SuccessCode, getResp.Code, "body=%s", srStr(getResp))
	data := getResp.Data.(map[string]interface{})
	customFields := data["customFields"].([]interface{})
	require.Len(t, customFields, 1)
	first := customFields[0].(map[string]interface{})
	assert.Equal(t, "environment", first["name"])
	assert.Equal(t, "环境", first["label"])
	assert.Equal(t, "staging", first["value"])
}

func TestServiceRequestHandler_Create_MissingCatalogID(t *testing.T) {
	r, _, _, _, _ := srSetup(t)
	// CatalogID=0 → handler 直接返回 1001
	req := dto.CreateServiceRequestRequest{Title: "X", ComplianceAck: true}
	resp := srDoReq(t, r, "POST", "/api/v1/service-requests", req)
	assert.EqualValues(t, 1001, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestHandler_Create_CatalogNotFound(t *testing.T) {
	r, _, _, _, _ := srSetup(t)
	// 不存在的 catalog → service 返回 NotFound → handler 映射 5001
	req := dto.CreateServiceRequestRequest{CatalogID: 999999, Title: "X", ComplianceAck: true}
	resp := srDoReq(t, r, "POST", "/api/v1/service-requests", req)
	assert.EqualValues(t, common.NotFoundErrorCode, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestHandler_Create_MissingComplianceAck(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	// ComplianceAck=false → service 返回 BadRequest → handler 映射 5001
	req := dto.CreateServiceRequestRequest{CatalogID: catID, Title: "X"}
	resp := srDoReq(t, r, "POST", "/api/v1/service-requests", req)
	assert.EqualValues(t, common.ParamErrorCode, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestCreateDefersNewCIUntilProvisioning(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "sr_deferred_ci.db") + "?_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()
	tenant, err := client.Tenant.Create().
		SetName("Deferred CI Tenant").SetCode("DEFER-" + srUID()).SetDomain("defer.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("defer-" + srUID()).SetEmail("defer-" + srUID() + "@example.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("agent").SetDepartment("IT").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	ciType, err := client.CIType.Create().SetName("Virtual Machine").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	scRepo := service_catalog.NewEntRepository(client)
	catalog, err := service_catalog.NewService(scRepo, client, logger).
		Create(ctx, "VM Request", "infrastructure", "Provision VM", 24, tenant.ID, "enabled", ciType.ID, 0, nil)
	require.NoError(t, err)
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	srSvc := NewService(NewEntRepository(client), scRepo, cmdb.NewEntRepository(client), client, logger, ticketSvc)
	expireAt := time.Now().Add(30 * 24 * time.Hour)

	created, err := srSvc.Create(ctx, tenant.ID, user.ID, catalog.ID, &ServiceRequest{
		ComplianceAck: true, DataClassification: "internal", ExpireAt: &expireAt,
		FormData: map[string]interface{}{"title": "Production VM"},
	})
	require.NoError(t, err)
	assert.Zero(t, created.CiID)
	ciCount, err := client.ConfigurationItem.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, ciCount, "request submission must not create an active CI before approval")
}

func TestServiceRequestHandler_Get_Success(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	id := srCreateOne(t, r, catID)
	resp := srDoReq(t, r, "GET", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	data := resp.Data.(map[string]interface{})
	assert.EqualValues(t, id, data["id"])
}

func TestServiceRequestHandler_Get_InvalidID(t *testing.T) {
	r, _, _, _, _ := srSetup(t)
	resp := srDoReq(t, r, "GET", "/api/v1/service-requests/abc", nil)
	assert.EqualValues(t, 1001, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestHandler_Get_NotFound(t *testing.T) {
	r, _, _, _, _ := srSetup(t)
	resp := srDoReq(t, r, "GET", "/api/v1/service-requests/999999", nil)
	assert.EqualValues(t, 404, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestHandler_GetByTicket(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	id := srCreateOne(t, r, catID)
	getResp := srDoReq(t, r, "GET", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	require.Equal(t, common.SuccessCode, getResp.Code, "body=%s", srStr(getResp))
	ticketID := int(getResp.Data.(map[string]interface{})["ticketId"].(float64))

	resp := srDoReq(t, r, "GET", "/api/v1/service-requests/by-ticket/"+strconv.Itoa(ticketID), nil)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	data := resp.Data.(map[string]interface{})
	assert.EqualValues(t, id, data["id"])
	assert.EqualValues(t, ticketID, data["ticketId"])
}

func TestServiceRequestHandler_GetByTicket_NotFound(t *testing.T) {
	r, _, _, _, _ := srSetup(t)
	resp := srDoReq(t, r, "GET", "/api/v1/service-requests/by-ticket/999999", nil)
	assert.EqualValues(t, 404, resp.Code, "body=%s", srStr(resp))
}

func TestServiceRequestHandler_List(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	srCreateOne(t, r, catID)
	resp := srDoReq(t, r, "GET", "/api/v1/service-requests?page=1&size=10", nil)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	data := resp.Data.(map[string]interface{})
	assert.Contains(t, data, "requests")
	assert.Contains(t, data, "total")
}

func TestServiceRequestHandler_PartialUpdatePreservesBooleanFields(t *testing.T) {
	r, _, _, _, catID := srSetup(t)
	create := srDoReq(t, r, "POST", "/api/v1/service-requests", dto.CreateServiceRequestRequest{
		CatalogID: catID, Title: "Public endpoint", ComplianceAck: true,
		NeedsPublicIP: true, SourceIPWhitelist: []string{"10.0.0.1"},
	})
	require.Equal(t, common.SuccessCode, create.Code, "body=%s", srStr(create))
	id := int(create.Data.(map[string]interface{})["id"].(float64))

	// Title is no longer part of UpdateServiceRequestRequest (it's ticket-owned, set only at
	// creation time) — send an update payload that only touches an unrelated field (FormData)
	// to prove the boolean fields are still preserved when omitted from the payload.
	update := srDoReq(t, r, "PUT", "/api/v1/service-requests/"+strconv.Itoa(id),
		dto.UpdateServiceRequestRequest{FormData: map[string]any{"note": "renamed via test"}})
	require.Equal(t, common.SuccessCode, update.Code, "body=%s", srStr(update))
	data := update.Data.(map[string]interface{})
	assert.Equal(t, true, data["needsPublicIp"])
	assert.Equal(t, true, data["complianceAck"])
}

func TestServiceRequestHandler_Delete(t *testing.T) {
	r, client, _, _, catID := srSetup(t)
	id := srCreateOne(t, r, catID)
	resp := srDoReq(t, r, "DELETE", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	require.Equal(t, common.SuccessCode, resp.Code, "body=%s", srStr(resp))
	// 删除后再查应 404
	resp2 := srDoReq(t, r, "GET", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	assert.EqualValues(t, 404, resp2.Code, "body=%s", srStr(resp2))
	stored, err := client.ServiceRequest.Get(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, stored.DeletedAt)
}
