package controller

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 重写原有弱测试：注册真实路由、注入租户/用户上下文、断言响应。
// 覆盖通知的创建（参数校验 + 持久化）与读取（租户作用域）主链路。
func setupNotificationController(t *testing.T) (*gin.Engine, *ent.Client, int, int) {
	gin.SetMode(gin.TestMode)
	dsn := "file:" + filepath.Join(t.TempDir(), "notification_test.db") + "?_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)

	tenantID, userID := seedTenantUser(t, client)
	svc := service.NewNotificationService(client)
	ctrl := NewNotificationController(svc)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(withTestAuth(tenantID, userID))
	r.GET("/api/v1/notifications", ctrl.GetNotifications)
	r.GET("/api/v1/notifications/unread-count", ctrl.GetUnreadCount)
	r.PUT("/api/v1/notifications/:id/read", ctrl.MarkNotificationRead)
	r.PUT("/api/v1/notifications/read-all", ctrl.MarkAllNotificationsRead)
	r.DELETE("/api/v1/notifications/:id", ctrl.DeleteNotification)
	r.POST("/api/v1/notifications", ctrl.CreateNotification)
	return r, client, tenantID, userID
}

func TestNotificationController_CreateNotification(t *testing.T) {
	r, _, tenantID, userID := setupNotificationController(t)

	t.Run("成功创建通知", func(t *testing.T) {
		body := dto.CreateNotificationRequest{
			Title:    "部署完成",
			Message:  "生产环境已发布 v1.2",
			Type:     "success",
			UserID:   userID,
			TenantID: tenantID,
		}
		resp := doReq(t, r, "POST", "/api/v1/notifications", body, false)
		assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
		data := resp.Data.(map[string]interface{})
		assert.Equal(t, "部署完成", data["title"])
	})

	t.Run("缺少标题应返回参数错误", func(t *testing.T) {
		body := dto.CreateNotificationRequest{
			Message: "x", Type: "info", UserID: userID, TenantID: tenantID,
		}
		resp := doReq(t, r, "POST", "/api/v1/notifications", body, false)
		assert.Equal(t, common.ParamErrorCode, resp.Code, "body=%s", mustString(resp))
	})

	t.Run("类型非法应返回参数错误", func(t *testing.T) {
		body := dto.CreateNotificationRequest{
			Title: "x", Message: "x", Type: "bogus", UserID: userID, TenantID: tenantID,
		}
		resp := doReq(t, r, "POST", "/api/v1/notifications", body, false)
		assert.Equal(t, common.ParamErrorCode, resp.Code, "body=%s", mustString(resp))
	})
}

func TestNotificationController_GetNotifications(t *testing.T) {
	r, _, _, _ := setupNotificationController(t)

	t.Run("列表返回成功", func(t *testing.T) {
		resp := doReq(t, r, "GET", "/api/v1/notifications", nil, false)
		assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
		data := resp.Data.(map[string]interface{})
		assert.Contains(t, data, "notifications")
		assert.Contains(t, data, "total")
	})
}

func TestNotificationController_GetUnreadCount(t *testing.T) {
	r, _, _, _ := setupNotificationController(t)
	resp := doReq(t, r, "GET", "/api/v1/notifications/unread-count", nil, false)
	assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
}

func TestNotificationController_MarkAllRead(t *testing.T) {
	r, _, _, _ := setupNotificationController(t)
	resp := doReq(t, r, "PUT", "/api/v1/notifications/read-all", nil, false)
	assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
}

func TestNotificationController_DeleteNotification(t *testing.T) {
	r, _, tenantID, userID := setupNotificationController(t)

	created := doReq(t, r, "POST", "/api/v1/notifications", dto.CreateNotificationRequest{
		Title: "待删除通知", Message: "m", Type: "warning", UserID: userID, TenantID: tenantID,
	}, false)
	require.Equal(t, common.SuccessCode, created.Code)
	id := int(created.Data.(map[string]interface{})["id"].(float64))

	resp := doReq(t, r, "DELETE", "/api/v1/notifications/"+strconv.Itoa(id), nil, false)
	assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
}

// --- tenant-context regression guard -------------------------------------------------------
//
// Production enforces tenant scoping in the RLS driver (database/rls): every statement must run
// with a context that carries tenantctx, otherwise it fails closed with
// "rls: no tenant_id in context and system bypass not set". The notification controller used to
// pass the bare *gin.Context to the service, which hid the tenant from that driver and turned
// every notification request into a 500. sqlite has no RLS, so the tests above could not observe
// that requirement; this guard records the same invariant at the ent driver boundary, where the
// query context is still visible.

type tenantGuardDriver struct {
	inner      dialect.Driver
	violations *[]string
}

func (d *tenantGuardDriver) record(ctx context.Context, query string) {
	trimmed := strings.ToUpper(strings.TrimSpace(query))
	// Schema creation runs DDL that names these tables but touches no tenant-scoped rows.
	if strings.HasPrefix(trimmed, "CREATE") || strings.HasPrefix(trimmed, "ALTER") ||
		strings.HasPrefix(trimmed, "DROP") {
		return
	}
	if !strings.Contains(query, "notifications") {
		return
	}
	if tid, ok := tenantctx.TenantID(ctx); !ok || tid <= 0 {
		*d.violations = append(*d.violations, query)
	}
}

func (d *tenantGuardDriver) Exec(ctx context.Context, query string, args, v interface{}) error {
	d.record(ctx, query)
	return d.inner.Exec(ctx, query, args, v)
}

func (d *tenantGuardDriver) Query(ctx context.Context, query string, args, v interface{}) error {
	d.record(ctx, query)
	return d.inner.Query(ctx, query, args, v)
}

func (d *tenantGuardDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.inner.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &tenantGuardTx{Tx: tx, driver: d}, nil
}

func (d *tenantGuardDriver) Close() error    { return d.inner.Close() }
func (d *tenantGuardDriver) Dialect() string { return d.inner.Dialect() }

type tenantGuardTx struct {
	dialect.Tx
	driver *tenantGuardDriver
}

func (t *tenantGuardTx) Exec(ctx context.Context, query string, args, v interface{}) error {
	t.driver.record(ctx, query)
	return t.Tx.Exec(ctx, query, args, v)
}

func (t *tenantGuardTx) Query(ctx context.Context, query string, args, v interface{}) error {
	t.driver.record(ctx, query)
	return t.Tx.Query(ctx, query, args, v)
}

// withProdLikeTenantAuth mirrors middleware/auth.go: the gin keys the controller reads *and* the
// request context tenant that the RLS driver reads.
func withProdLikeTenantAuth(tid, uid int) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tid})
		c.Set("tenant_id", tid)
		c.Set("user_id", uid)
		c.Request = c.Request.WithContext(tenantctx.WithTenantID(c.Request.Context(), tid))
		c.Next()
	}
}

func setupNotificationControllerWithTenantGuard(t *testing.T) (*gin.Engine, *ent.Client, int, int, *[]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := "file:" + filepath.Join(t.TempDir(), "notification_tenant_test.db") + "?_fk=1"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	violations := &[]string{}
	guard := &tenantGuardDriver{inner: entsql.OpenDB(dialect.SQLite, db), violations: violations}
	client := ent.NewClient(ent.Driver(guard))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))

	tenantID, userID := seedTenantUser(t, client)
	ctrl := NewNotificationController(service.NewNotificationService(client))

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(withProdLikeTenantAuth(tenantID, userID))
	r.GET("/api/v1/notifications", ctrl.GetNotifications)
	r.GET("/api/v1/notifications/unread-count", ctrl.GetUnreadCount)
	return r, client, tenantID, userID, violations
}

func seedNotification(t *testing.T, client *ent.Client, tenantID, userID int, title string) {
	t.Helper()
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)
	_, err := client.Notification.Create().
		SetTenantID(tenantID).
		SetUserID(userID).
		SetTitle(title).
		SetMessage("m").
		SetType("info").
		SetRead(false).
		Save(ctx)
	require.NoError(t, err)
}

// Regression: the notification read path must hand the tenant-carrying request context to the
// persistence layer. Reverting the controller to pass the gin.Context makes this fail.
func TestNotificationController_ReadCarriesRequestTenantContext(t *testing.T) {
	r, client, tenantID, userID, violations := setupNotificationControllerWithTenantGuard(t)
	seedNotification(t, client, tenantID, userID, "租户上下文回归")

	resp := doReq(t, r, "GET", "/api/v1/notifications", nil, false)

	assert.Empty(t, *violations,
		"notification statement reached the driver without tenant context (controller must pass ctx.Request.Context())")
	assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
	data := resp.Data.(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
}

// The tenant predicate must stay in place: a row carrying the caller's user id but another
// tenant must never be returned (Postgres enforces the same rule through RLS).
func TestNotificationController_CrossTenantRowsStayHidden(t *testing.T) {
	r, client, tenantID, userID, _ := setupNotificationControllerWithTenantGuard(t)
	seedNotification(t, client, tenantID, userID, "本租户可见")
	seedNotification(t, client, tenantID+1, userID, "跨租户不可见")

	resp := doReq(t, r, "GET", "/api/v1/notifications", nil, false)

	assert.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
	data := resp.Data.(map[string]interface{})
	assert.Equal(t, float64(1), data["total"], "cross-tenant row leaked into the result set")
	assert.NotContains(t, mustString(resp), "跨租户不可见")
}
