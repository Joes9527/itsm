package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// buildWorkItemTestRouter 挂载一条形状与 router.go 里 tickets.GET("/:id/comments", ...)
// 完全一致的路由:真实中间件 + 一个只返回 200 的桩 handler。用于验证中间件在真实的
// gin 路由匹配(:id 参数解析、tenant/role/client 从上下文取值)链路里行为正确,
// 不只是被直接函数调用。
func buildWorkItemTestRouter(client *ent.Client, tenantID int, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client", client)
		c.Set("tenant_id", tenantID)
		c.Set("role", role)
		c.Next()
	})
	r.GET("/api/v1/tickets/:id/comments", RequireWorkItemRecordClassPermission("read"), func(c *gin.Context) {
		common.Success(c, gin.H{"ok": true})
	})
	return r
}

func TestWorkItemSharedRoute_IntegrationThroughRealRouter(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitem_rbac_router_integration?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	const tenantID = 1

	problemTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "problem")
	incidentTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "incident")
	changeTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "change_request")

	withHardcodedPermissions(t, "problem_manager", []Permission{{Resource: "problem", Action: "read"}})
	withHardcodedPermissions(t, "change_manager", []Permission{{Resource: "change", Action: "read"}})

	problemRouter := buildWorkItemTestRouter(client, tenantID, "problem_manager")
	changeRouter := buildWorkItemTestRouter(client, tenantID, "change_manager")

	t.Run("problem-only role reads its own domain's comments route", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/v1/tickets/"+itoa(problemTicket.ID)+"/comments", nil)
		problemRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("problem-only role is forbidden from an incident's comments route", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/v1/tickets/"+itoa(incidentTicket.ID)+"/comments", nil)
		problemRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("change-only role reads its own domain's comments route", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/v1/tickets/"+itoa(changeTicket.ID)+"/comments", nil)
		changeRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("change-only role is forbidden from a problem's comments route", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/v1/tickets/"+itoa(problemTicket.ID)+"/comments", nil)
		changeRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
	})
}
