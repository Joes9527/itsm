package incident

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupIncidentHandlerTest(t *testing.T) (*ent.Client, *Service, *Handler, context.Context) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:incident-handler-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	repo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	service := NewService(repo, logger)
	handler := NewHandler(service)
	return client, service, handler, context.Background()
}

func createIncidentHandlerTenant(t *testing.T, ctx context.Context, client *ent.Client, suffix string) *ent.Tenant {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("Incident Tenant " + suffix).
		SetCode("incident-" + suffix).
		SetDomain("incident-" + suffix + ".example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	return tenant
}

func createIncidentHandlerUser(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, suffix string) *ent.User {
	t.Helper()
	user, err := client.User.Create().
		SetUsername("incident-" + suffix).
		SetEmail("incident-" + suffix + "@example.com").
		SetName("Incident User").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func TestIncidentServiceLifecycleAndStatusMachine(t *testing.T) {
	client, service, _, ctx := setupIncidentHandlerTest(t)
	defer client.Close()

	tenant := createIncidentHandlerTenant(t, ctx, client, "lifecycle")
	user := createIncidentHandlerUser(t, ctx, client, tenant.ID, "lifecycle")

	// 1. 创建事件
	inc, err := service.Create(ctx, tenant.ID, &Incident{
		Title:       "Database latency spike",
		Description: "Queries are running slow in production",
		ReporterID:  user.ID,
		Priority:    "medium",
	})
	require.NoError(t, err)
	assert.Equal(t, "new", inc.Status)
	assert.NotEmpty(t, inc.IncidentNumber)
	assert.Equal(t, tenant.ID, inc.TenantID)

	// 2. 合法状态流转: new -> acknowledged -> in_progress -> resolved -> closed
	inc, err = service.Update(ctx, tenant.ID, inc.ID, &Incident{Status: "acknowledged"})
	require.NoError(t, err)
	assert.Equal(t, "acknowledged", inc.Status)

	inc, err = service.Update(ctx, tenant.ID, inc.ID, &Incident{Status: "in_progress"})
	require.NoError(t, err)
	assert.Equal(t, "in_progress", inc.Status)

	inc, err = service.Update(ctx, tenant.ID, inc.ID, &Incident{Status: "resolved"})
	require.NoError(t, err)
	assert.Equal(t, "resolved", inc.Status)
	require.NotNil(t, inc.ResolvedAt)

	inc, err = service.Update(ctx, tenant.ID, inc.ID, &Incident{Status: "closed"})
	require.NoError(t, err)
	assert.Equal(t, "closed", inc.Status)
	require.NotNil(t, inc.ClosedAt)

	// 3. 非法状态转换: 终态 closed 不允许流转
	_, err = service.Update(ctx, tenant.ID, inc.ID, &Incident{Status: "in_progress"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid incident status transition")

	// 4. 测试另一个非法流转: new 直接跳到 closed
	inc2, err := service.Create(ctx, tenant.ID, &Incident{
		Title:       "Invalid jump test",
		Description: "test",
		ReporterID:  user.ID,
		Priority:    "low",
	})
	require.NoError(t, err)
	_, err = service.Update(ctx, tenant.ID, inc2.ID, &Incident{Status: "closed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid incident status transition")

	// 5. 非法流转: new 直接跳到 resolved
	_, err = service.Update(ctx, tenant.ID, inc2.ID, &Incident{Status: "resolved"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid incident status transition")
}

func TestIncidentServiceTenantIsolation(t *testing.T) {
	client, service, _, ctx := setupIncidentHandlerTest(t)
	defer client.Close()

	tenantA := createIncidentHandlerTenant(t, ctx, client, "tenantA")
	tenantB := createIncidentHandlerTenant(t, ctx, client, "tenantB")
	userA := createIncidentHandlerUser(t, ctx, client, tenantA.ID, "userA")
	userB := createIncidentHandlerUser(t, ctx, client, tenantB.ID, "userB")

	// 在租户 A 下创建
	incA, err := service.Create(ctx, tenantA.ID, &Incident{
		Title:       "Tenant A Incident",
		Description: "Confidential outage",
		ReporterID:  userA.ID,
		Priority:    "high",
	})
	require.NoError(t, err)

	// 租户 B 尝试读取租户 A 的事件 -> 必须失败
	_, err = service.Get(ctx, incA.ID, tenantB.ID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))

	// 租户 B 尝试修改租户 A 的事件 -> 必须失败
	_, err = service.Update(ctx, tenantB.ID, incA.ID, &Incident{
		Title: "Hacked Title",
	})
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))

	// 租户 B 尝试升级租户 A 的事件 -> 必须失败
	_, err = service.Escalate(ctx, tenantB.ID, incA.ID, 2, "Escalate attempt")
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))

	// 列表隔离测试（直接插入带不同编号的事件以避免单一进程无Redis并发时的编号相同）
	incBEnt, err := client.Incident.Create().
		SetTitle("Tenant B Incident").
		SetDescription("Tenant B outage").
		SetStatus("new").
		SetPriority("low").
		SetSeverity("low").
		SetIncidentNumber("INC-TEST-TENANT-B-001").
		SetReporterID(userB.ID).
		SetTenantID(tenantB.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	listA, totalA, err := service.List(ctx, tenantA.ID, 1, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, totalA)
	assert.Equal(t, incA.ID, listA[0].ID)

	listB, totalB, err := service.List(ctx, tenantB.ID, 1, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, totalB)
	assert.Equal(t, incBEnt.ID, listB[0].ID)
}

func TestIncidentServiceEscalationAndEvents(t *testing.T) {
	client, service, _, ctx := setupIncidentHandlerTest(t)
	defer client.Close()

	tenant := createIncidentHandlerTenant(t, ctx, client, "escalate")
	user := createIncidentHandlerUser(t, ctx, client, tenant.ID, "escalate")

	inc, err := service.Create(ctx, tenant.ID, &Incident{
		Title:       "Network packet loss",
		Description: "Packet loss across routers",
		ReporterID:  user.ID,
		Priority:    "medium",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, inc.EscalationLevel)

	// 升级事件
	escalated, err := service.Escalate(ctx, tenant.ID, inc.ID, 2, "Management escalation required")
	require.NoError(t, err)
	assert.Equal(t, 2, escalated.EscalationLevel)
	require.NotNil(t, escalated.EscalatedAt)

	// 验证事件记录已创建
	events, err := service.repo.ListEvents(ctx, inc.ID, tenant.ID)
	require.NoError(t, err)
	assert.Len(t, events, 2) // 1 creation + 1 escalation
}

func TestIncidentHandlerHTTPRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client, _, handler, ctx := setupIncidentHandlerTest(t)
	defer client.Close()

	tenant := createIncidentHandlerTenant(t, ctx, client, "http")
	user := createIncidentHandlerUser(t, ctx, client, tenant.ID, "http")

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenant.ID)
		c.Set("user_id", user.ID)
		c.Set("client", client)
		c.Next()
	})

	r.POST("/incidents", handler.Create)
	r.GET("/incidents", handler.List)
	r.GET("/incidents/:id", handler.Get)
	r.PUT("/incidents/:id", handler.Update)
	r.POST("/incidents/:id/escalate", handler.Escalate)
	r.GET("/incidents/stats", handler.GetStats)

	// 1. POST /incidents
	createPayload := dto.CreateIncidentRequest{
		Title:       "API Gateway down",
		Description: "Gateway throwing 502",
		Priority:    "critical",
		Severity:    "critical",
		Source:      "manual",
	}
	body, _ := json.Marshal(createPayload)
	req := httptest.NewRequest(http.MethodPost, "/incidents", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var createResp struct {
		Code int                  `json:"code"`
		Data dto.IncidentResponse `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &createResp)
	require.NoError(t, err)
	assert.Equal(t, 0, createResp.Code)
	assert.Equal(t, "API Gateway down", createResp.Data.Title)
	incID := createResp.Data.ID

	// 2. GET /incidents/:id
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/incidents/%d", incID), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 3. GET /incidents (list)
	req = httptest.NewRequest(http.MethodGet, "/incidents?page=1&pageSize=10", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 4. PUT /incidents/:id (update title)
	newTitle := "API Gateway restored to 90%"
	updatePayload := dto.UpdateIncidentRequest{
		Title: &newTitle,
	}
	body, _ = json.Marshal(updatePayload)
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/incidents/%d", incID), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 5. POST /incidents/:id/escalate
	escalatePayload := dto.IncidentEscalationRequest{
		IncidentID:      incID,
		EscalationLevel: 2,
		Reason:          "Need SRE on-call",
	}
	body, _ = json.Marshal(escalatePayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/incidents/%d/escalate", incID), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 6. GET /incidents/stats
	req = httptest.NewRequest(http.MethodGet, "/incidents/stats", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
