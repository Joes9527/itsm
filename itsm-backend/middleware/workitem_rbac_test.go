package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestResourceForRecordClass(t *testing.T) {
	cases := []struct {
		recordClass string
		want        string
	}{
		{"incident", "incident"},
		{"problem", "problem"},
		{"change_request", "change"},
		{"generic", "ticket"},
		{"service_request_item", "ticket"},
		{"catalog_task", "ticket"},
		{"", "ticket"},
		{"some_future_value", "ticket"},
	}
	for _, tc := range cases {
		t.Run(tc.recordClass, func(t *testing.T) {
			got := resourceForRecordClass(tc.recordClass)
			if got != tc.want {
				t.Errorf("resourceForRecordClass(%q) = %q, want %q", tc.recordClass, got, tc.want)
			}
		})
	}
}

// withHardcodedPermissions 临时切到 PermissionConfigModeHardcodeOnly 并注入指定角色的权限，
// 测试结束后恢复原有全局配置——PermissionConfig/RolePermissions 都是包级变量，直接改，
// 用 t.Cleanup 保证不泄漏到其它测试。
func withHardcodedPermissions(t *testing.T, role string, perms []Permission) {
	t.Helper()
	prevMode := PermissionConfig.Mode
	prevPerms, hadPrev := RolePermissions[role]
	PermissionConfig.Mode = PermissionConfigModeHardcodeOnly
	RolePermissions[role] = perms
	t.Cleanup(func() {
		PermissionConfig.Mode = prevMode
		if hadPrev {
			RolePermissions[role] = prevPerms
		} else {
			delete(RolePermissions, role)
		}
	})
}

func setupWorkItemRBACTestTicket(t *testing.T, client *ent.Client, tenantID int, recordClass string) *ent.Ticket {
	t.Helper()
	ctx := context.Background()
	// ticket.requester_id 是到 users 表的外键（ent/schema/ticket.go 的 requester edge），
	// users.tenant_id 又是到 tenants 表的外键（ent/schema/user.go 的 tenant edge）；sqlite DSN
	// 带 _fk=1 强制外键校验，所以必须先建真实的 tenant + user 两行，不能像 brief 里那样硬编码
	// SetRequesterID(1)——同一模式在 ent/ticket_extra_test.go、
	// cmd/check_work_item_integrity/main_test.go 里已经这么做。这里建的 tenant/user 只是用来
	// 满足外键约束的占位数据，与 ticket 本身落在哪个 tenantID（测试真正断言的对象）无关——
	// RBAC 中间件只按 ticket.tenant_id 过滤，不关心 requester 所属的 tenant。用自增序号保证
	// tenant.code / user.username / user.email 在同一个 client 上重复调用时不会撞唯一索引。
	seq := atomic.AddInt64(&workItemRBACTestUserSeq, 1)
	requesterTenant, err := client.Tenant.Create().
		SetName(fmt.Sprintf("WorkItem RBAC Test Tenant %d", seq)).
		SetCode(fmt.Sprintf("workitem-rbac-test-tenant-%d", seq)).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername(fmt.Sprintf("workitem_rbac_test_user_%d", seq)).
		SetEmail(fmt.Sprintf("workitem_rbac_test_user_%d@test.local", seq)).
		SetName("WorkItem RBAC Test User").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(requesterTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tk, err := client.Ticket.Create().
		SetTitle("test ticket").
		SetType("incident").
		SetRecordClass(recordClass).
		SetPriority("medium").
		SetTicketNumber("TKT-TEST-" + recordClass).
		SetRequesterID(requester.ID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return tk
}

// workItemRBACTestUserSeq 让 setupWorkItemRBACTestTicket 里临时创建的 tenant/user 占位数据
// 拥有全局唯一的 code/username/email，避免同一个 client 上多次调用时撞唯一索引。
var workItemRBACTestUserSeq int64

func newWorkItemRBACTestContext(t *testing.T, client *ent.Client, ticketID, tenantID int, role string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/v1/tickets/x/comments", nil)
	c.Params = gin.Params{{Key: "id", Value: itoa(ticketID)}}
	c.Set("client", client)
	c.Set("tenant_id", tenantID)
	c.Set("role", role)
	return c, w
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func TestRequireWorkItemRecordClassPermission_RecordClassMatrix(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitem_rbac_matrix?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	const tenantID = 1

	incidentTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "incident")
	problemTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "problem")
	changeTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "change_request")
	genericTicket := setupWorkItemRBACTestTicket(t, client, tenantID, "generic")

	cases := []struct {
		name       string
		ticketID   int
		role       string
		perms      []Permission
		wantStatus int
	}{
		{"incident viewer reads incident ticket", incidentTicket.ID, "incident_reader", []Permission{{Resource: "incident", Action: "read"}}, http.StatusOK},
		{"incident viewer blocked on problem ticket", problemTicket.ID, "incident_reader", []Permission{{Resource: "incident", Action: "read"}}, http.StatusForbidden},
		{"problem viewer reads problem ticket", problemTicket.ID, "problem_reader", []Permission{{Resource: "problem", Action: "read"}}, http.StatusOK},
		{"problem viewer blocked on change ticket", changeTicket.ID, "problem_reader", []Permission{{Resource: "problem", Action: "read"}}, http.StatusForbidden},
		{"change viewer reads change ticket", changeTicket.ID, "change_reader", []Permission{{Resource: "change", Action: "read"}}, http.StatusOK},
		{"change viewer blocked on generic ticket", genericTicket.ID, "change_reader", []Permission{{Resource: "change", Action: "read"}}, http.StatusForbidden},
		{"ticket viewer reads generic ticket", genericTicket.ID, "ticket_reader", []Permission{{Resource: "ticket", Action: "read"}}, http.StatusOK},
		{"ticket-only viewer blocked on incident ticket", incidentTicket.ID, "ticket_reader", []Permission{{Resource: "ticket", Action: "read"}}, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withHardcodedPermissions(t, tc.role, tc.perms)
			c, w := newWorkItemRBACTestContext(t, client, tc.ticketID, tenantID, tc.role)
			RequireWorkItemRecordClassPermission("read")(c)
			require.Equal(t, tc.wantStatus, w.Code)
			require.Equal(t, tc.wantStatus != http.StatusOK, c.IsAborted())
		})
	}
}

func TestRequireWorkItemRecordClassPermission_NotFoundNotForbidden(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitem_rbac_notfound?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	withHardcodedPermissions(t, "any_role", []Permission{{Resource: "ticket", Action: "read"}})

	t.Run("nonexistent ticket id returns 404", func(t *testing.T) {
		c, w := newWorkItemRBACTestContext(t, client, 999999, 1, "any_role")
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.True(t, c.IsAborted())
	})

	t.Run("ticket exists but in a different tenant returns 404, not 403", func(t *testing.T) {
		other := setupWorkItemRBACTestTicket(t, client, 2, "incident")
		c, w := newWorkItemRBACTestContext(t, client, other.ID, 1, "any_role")
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.True(t, c.IsAborted())
	})

	t.Run("soft-deleted ticket returns 404, not reachable via RBAC gate even with permission", func(t *testing.T) {
		deleted := setupWorkItemRBACTestTicket(t, client, 1, "generic")
		_, err := client.Ticket.UpdateOneID(deleted.ID).SetDeletedAt(time.Now()).Save(context.Background())
		require.NoError(t, err)

		// "any_role" holds {Resource: "ticket", Action: "read"} (set up above), which would
		// pass hasResourcePermission for a live "generic" ticket — proving the 404 here comes
		// from the DeletedAtIsNil() filter, not from a permission failure.
		c, w := newWorkItemRBACTestContext(t, client, deleted.ID, 1, "any_role")
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.True(t, c.IsAborted())
	})

	t.Run("non-numeric id returns param error, not a panic", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets/abc/comments", nil)
		c.Params = gin.Params{{Key: "id", Value: "abc"}}
		c.Set("client", client)
		c.Set("tenant_id", 1)
		c.Set("role", "any_role")
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.True(t, c.IsAborted())
	})
}

func TestRequireWorkItemRecordClassPermission_MissingAuthContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing role", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets/1/comments", nil)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets/1/comments", nil)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Set("role", "any_role")
		RequireWorkItemRecordClassPermission("read")(c)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
