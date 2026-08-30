# WorkItem 详情页能力对齐 · Phase 1：RequireWorkItemRecordClassPermission 中间件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the static `RequirePermission("ticket", action)` guard on the 13 WorkItem-shared
routes (`/tickets/:id/{comments,attachments,history,relations,sla}` and their sub-paths) with a new
`RequireWorkItemRecordClassPermission(action)` middleware that resolves the RBAC resource name from
the actual `tickets.record_class` of the row being accessed, so a Problem/Change/Incident viewer
without `ticket:read` isn't wrongly 403'd on their own domain's shared WorkItem data.

**Architecture:** One new middleware function in `itsm-backend/middleware/` that duplicates
`RequirePermission`'s existing auth-context extraction (role/tenant_id/client from `gin.Context`,
same as the function it replaces), adds a tenant-scoped `tickets` lookup by `:id`, maps
`record_class` → RBAC resource name via a small pure function, then delegates to the existing
unexported `hasResourcePermission`. Router wiring changes only the middleware argument on the 13
routes already listed in the design doc — controllers, paths, and methods are untouched.

**Tech Stack:** Go, Gin, Ent ORM, `stretchr/testify`, SQLite via `ent/enttest` for tests (existing
project convention in `middleware/rbac_test.go`).

**Spec:** `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md` §3.4, §4.1

## Global Constraints

- Only the 13 named routes change middleware; every other `tickets.*` route keeps
  `RequirePermission("ticket", ...)` unchanged (spec §4.1).
- `record_class` → resource mapping: `"incident"` → `"incident"`, `"problem"` → `"problem"`,
  `"change_request"` → `"change"`, everything else (`"generic"`, `"service_request_item"`,
  `"catalog_task"`, any other/未来值) → `"ticket"` (spec §4.1 step 3).
- A `:id` that doesn't resolve to a tenant-scoped `tickets` row must return **404**, not 403 — do
  not let the response distinguish "wrong tenant" from "doesn't exist" (spec §4.1 step 5, prevents
  cross-tenant ID enumeration).
- Do not touch `controller/`, `dto/`, or any handler referenced by the 13 routes — this phase is
  middleware + router wiring only.
- `super_admin` bypass and the existing `PermissionConfig.Mode` permission-loading modes
  (`hasResourcePermission` → `loadPermissionsByMode` → `checkPermissionMatch`) are inherited as-is;
  do not reimplement or fork them.

---

## Task 1: `resourceForRecordClass` mapping function

**Files:**
- Create: `itsm-backend/middleware/workitem_rbac.go`
- Test: `itsm-backend/middleware/workitem_rbac_test.go`

**Interfaces:**
- Produces: `resourceForRecordClass(recordClass string) string` — pure function, no I/O. Used by
  Task 2's middleware and mirrored on the frontend in Phase 3 (`toTargetType` in
  `WorkItemComments.tsx`) as the same `record_class` → domain mapping, so keep the four literal
  branches (`incident`/`problem`/`change_request`/default) exactly as specified below — a later
  reader matching frontend and backend mappings side by side depends on the branch names lining up.

- [ ] **Step 1: Write the failing test**

Create `itsm-backend/middleware/workitem_rbac_test.go`:

```go
package middleware

import "testing"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./middleware/... -run TestResourceForRecordClass -v`
Expected: FAIL (build error) — `resourceForRecordClass` is undefined.

- [ ] **Step 3: Write minimal implementation**

Create `itsm-backend/middleware/workitem_rbac.go`:

```go
package middleware

// resourceForRecordClass 把 tickets.record_class 映射到 RBAC 资源名，供
// RequireWorkItemRecordClassPermission 使用。除 incident/problem/change_request 三个专业域外，
// 其余 record_class（generic/service_request_item/catalog_task，以及未来任何新值）统一映射到
// "ticket"——这三个是本设计新引入的专业资源名，其余都是 Ticket 自己的记录类型。
func resourceForRecordClass(recordClass string) string {
	switch recordClass {
	case "incident":
		return "incident"
	case "problem":
		return "problem"
	case "change_request":
		return "change"
	default:
		return "ticket"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./middleware/... -run TestResourceForRecordClass -v`
Expected: PASS (8 subtests)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add middleware/workitem_rbac.go middleware/workitem_rbac_test.go
git commit -m "feat(rbac): add record_class-to-resource mapping for WorkItem shared routes"
```

---

## Task 2: `RequireWorkItemRecordClassPermission` middleware

**Files:**
- Modify: `itsm-backend/middleware/workitem_rbac.go`
- Modify: `itsm-backend/middleware/workitem_rbac_test.go`

**Interfaces:**
- Consumes: `resourceForRecordClass(string) string` (Task 1); `hasResourcePermission(client
  *ent.Client, role, resource, action string, tenantID int) bool` (existing, unexported, same
  package, `itsm-backend/middleware/rbac.go:577`); `common.Fail(c *gin.Context, code int, message
  string)` (existing, `itsm-backend/common/response.go:48`, already calls `c.Abort()` internally);
  ent predicates `ticket.ID(int)` / `ticket.TenantID(int)` from `itsm-backend/ent/ticket`.
- Produces: `RequireWorkItemRecordClassPermission(action string) gin.HandlerFunc` — same signature
  shape as `RequirePermission(resource, action string) gin.HandlerFunc` minus the static `resource`
  arg, for router.go to drop in.

Gin context keys consumed (set upstream by `RBACMiddleware`, same as `RequirePermission`):
`"role"` (string), `"tenant_id"` (int), `"client"` (`*ent.Client`).

- [ ] **Step 1: Write the failing tests**

Append to `itsm-backend/middleware/workitem_rbac_test.go`:

```go
package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

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
	tk, err := client.Ticket.Create().
		SetTitle("test ticket").
		SetType("incident").
		SetRecordClass(recordClass).
		SetPriority("medium").
		SetTicketNumber("TKT-TEST-" + recordClass).
		SetRequesterID(1).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return tk
}

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
```

Add `"fmt"` to the test file's import block (used by the `itoa` helper above).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./middleware/... -run TestRequireWorkItemRecordClassPermission -v`
Expected: FAIL (build error) — `RequireWorkItemRecordClassPermission` is undefined.

- [ ] **Step 3: Write minimal implementation**

Append to `itsm-backend/middleware/workitem_rbac.go` (add the needed imports to the file's import
block: `"strconv"`, `"itsm-backend/common"`, `"itsm-backend/ent"`, `"itsm-backend/ent/ticket"`,
`"github.com/gin-gonic/gin"`):

```go
package middleware

import (
	"strconv"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"github.com/gin-gonic/gin"
)

// resourceForRecordClass ...（Task 1 已写）

// RequireWorkItemRecordClassPermission 按路径参数 :id 对应 tickets 行的实际 record_class
// 动态解析资源名，再复用现有的 hasResourcePermission。用于 WorkItem 级共享接口
// （comments/attachments/history/relations/sla），因为同一条路由现在可能服务 Ticket、
// Incident、Problem 或 Change 四种专业域，静态 RequirePermission("ticket", action) 会
// 错误地要求非 Ticket 域的查看者也必须有 ticket 权限。
//
// 认证上下文提取（role/tenant_id/client）与 RequirePermission 完全一致，是刻意复制而不是
// 提取公共函数——两者都要在各自的错误分支里返回不同的错误信息，抽出来反而增加间接层。
func RequireWorkItemRecordClassPermission(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "用户角色信息缺失")
			c.Abort()
			return
		}

		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "租户信息缺失")
			c.Abort()
			return
		}
		tenantID := tenantIDInterface.(int)

		clientInterface, exists := c.Get("client")
		if !exists {
			common.Fail(c, common.InternalErrorCode, "客户端缺失")
			c.Abort()
			return
		}
		client := clientInterface.(*ent.Client)

		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			common.Fail(c, common.ParamErrorCode, "无效的工单ID")
			c.Abort()
			return
		}

		t, err := client.Ticket.Query().
			Where(ticket.ID(id), ticket.TenantID(tenantID)).
			Only(c.Request.Context())
		if err != nil {
			// 查不到该 ticket（不存在或跨租户）统一返回 404，不是 403——避免让响应差异
			// 变成一个可以探测其它租户 ID 是否存在的信号。
			common.Fail(c, common.NotFoundCode, "工单不存在")
			c.Abort()
			return
		}

		resource := resourceForRecordClass(t.RecordClass)
		if !hasResourcePermission(client, role.(string), resource, action, tenantID) {
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./middleware/... -run TestRequireWorkItemRecordClassPermission -v`
Expected: PASS (all subtests across the three test functions — 8 matrix cases + 3 not-found cases +
2 missing-auth-context cases)

Then run the full middleware package to confirm no regressions:
Run: `cd itsm-backend && go test ./middleware/... -v`
Expected: PASS (including pre-existing `TestRequirePermissionForRBAC`,
`TestRBACMiddleware_NoLongerPerformsPermissionCheck`, etc.)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add middleware/workitem_rbac.go middleware/workitem_rbac_test.go
git commit -m "feat(rbac): add RequireWorkItemRecordClassPermission middleware for shared WorkItem routes"
```

---

## Task 3: Swap the 13 routes in `router.go`

**Files:**
- Modify: `itsm-backend/router/router.go:555,573,576,577,587-590,595-599`

**Interfaces:**
- Consumes: `middleware.RequireWorkItemRecordClassPermission(action string) gin.HandlerFunc` (Task 2).

- [ ] **Step 1: Make the 13 replacements**

Each replacement only swaps `middleware.RequirePermission("ticket", X)` for
`middleware.RequireWorkItemRecordClassPermission(X)` on that exact line — method, path, and
controller reference are unchanged.

`router/router.go:555`:
```go
// before
tickets.GET("/:id/history", middleware.RequirePermission("ticket", "read"), config.TicketController.GetTicketActivity)
// after
tickets.GET("/:id/history", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketController.GetTicketActivity)
```

`router/router.go:573`:
```go
// before
tickets.GET("/:id/sla", middleware.RequirePermission("ticket", "read"), config.TicketController.GetTicketSLAInfo)
// after
tickets.GET("/:id/sla", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketController.GetTicketSLAInfo)
```

`router/router.go:576-577`:
```go
// before
tickets.GET("/:id/relations", middleware.RequirePermission("ticket", "read"), config.TicketController.GetTicketRelations)
tickets.GET("/:id/relations/stats", middleware.RequirePermission("ticket", "read"), config.TicketController.GetRelationStats)
// after
tickets.GET("/:id/relations", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketController.GetTicketRelations)
tickets.GET("/:id/relations/stats", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketController.GetRelationStats)
```

`router/router.go:587-590` (inside `if config.TicketCommentController != nil {`):
```go
// before
tickets.GET("/:id/comments", middleware.RequirePermission("ticket", "read"), config.TicketCommentController.ListTicketComments)
tickets.POST("/:id/comments", middleware.RequirePermission("ticket", "create"), config.TicketCommentController.CreateTicketComment)
tickets.PUT("/:id/comments/:comment_id", middleware.RequirePermission("ticket", "update"), config.TicketCommentController.UpdateTicketComment)
tickets.DELETE("/:id/comments/:comment_id", middleware.RequirePermission("ticket", "delete"), config.TicketCommentController.DeleteTicketComment)
// after
tickets.GET("/:id/comments", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketCommentController.ListTicketComments)
tickets.POST("/:id/comments", middleware.RequireWorkItemRecordClassPermission("create"), config.TicketCommentController.CreateTicketComment)
tickets.PUT("/:id/comments/:comment_id", middleware.RequireWorkItemRecordClassPermission("update"), config.TicketCommentController.UpdateTicketComment)
tickets.DELETE("/:id/comments/:comment_id", middleware.RequireWorkItemRecordClassPermission("delete"), config.TicketCommentController.DeleteTicketComment)
```

`router/router.go:595-599` (inside `if config.TicketAttachmentController != nil {`):
```go
// before
tickets.GET("/:id/attachments", middleware.RequirePermission("ticket", "read"), config.TicketAttachmentController.ListTicketAttachments)
tickets.POST("/:id/attachments", middleware.RequirePermission("ticket", "create"), config.TicketAttachmentController.UploadAttachment)
tickets.GET("/:id/attachments/:attachment_id", middleware.RequirePermission("ticket", "read"), config.TicketAttachmentController.DownloadAttachment)
tickets.GET("/:id/attachments/:attachment_id/preview", middleware.RequirePermission("ticket", "read"), config.TicketAttachmentController.PreviewAttachment)
tickets.DELETE("/:id/attachments/:attachment_id", middleware.RequirePermission("ticket", "delete"), config.TicketAttachmentController.DeleteAttachment)
// after
tickets.GET("/:id/attachments", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketAttachmentController.ListTicketAttachments)
tickets.POST("/:id/attachments", middleware.RequireWorkItemRecordClassPermission("create"), config.TicketAttachmentController.UploadAttachment)
tickets.GET("/:id/attachments/:attachment_id", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketAttachmentController.DownloadAttachment)
tickets.GET("/:id/attachments/:attachment_id/preview", middleware.RequireWorkItemRecordClassPermission("read"), config.TicketAttachmentController.PreviewAttachment)
tickets.DELETE("/:id/attachments/:attachment_id", middleware.RequireWorkItemRecordClassPermission("delete"), config.TicketAttachmentController.DeleteAttachment)
```

Double-check while editing: `router.go:826-828` (the Incident-specific `inc.GET/POST "/:id/comments"`
routes) and `router.go:1043-1044` (Knowledge article comments) are **not** part of this list and
must stay on `middleware.RequirePermission(...)` — they're different resources on different route
groups, not part of the 13 shared WorkItem routes.

- [ ] **Step 2: Verify the build compiles**

Run: `cd itsm-backend && go build ./...`
Expected: no errors.

- [ ] **Step 3: Verify exactly 13 lines changed and no unrelated routes were touched**

Run: `cd itsm-backend && git diff router/router.go | grep -c 'RequireWorkItemRecordClassPermission'`
Expected: `13`

Run: `cd itsm-backend && grep -n 'RequirePermission("ticket"' router/router.go | grep -c ':id/comments\|:id/attachments\|:id/history\|:id/relations\|:id/sla'`
Expected: `0` (none of the 13 target lines still reference the old middleware)

- [ ] **Step 4: Commit**

```bash
cd itsm-backend
git add router/router.go
git commit -m "refactor(router): route WorkItem shared endpoints through record_class-aware permission check"
```

---

## Task 4: Integration test — real HTTP round trip through the swapped routes

**Files:**
- Create: `itsm-backend/middleware/workitem_rbac_integration_test.go`

**Interfaces:**
- Consumes: `RequireWorkItemRecordClassPermission` (Task 2), `resourceForRecordClass` (Task 1), and
  three test helpers already defined in Task 2's `workitem_rbac_test.go` (same package, so directly
  callable from this file without re-importing): `setupWorkItemRBACTestTicket(t, client, tenantID,
  recordClass string) *ent.Ticket`, `withHardcodedPermissions(t, role string, perms []Permission)`,
  and `itoa(n int) string`.

This test mounts a minimal `gin.Engine` with the same route shape as `router.go`'s
`/:id/comments` registration (middleware + a trivial handler) and drives it via
`httptest.NewRecorder()` + `engine.ServeHTTP(...)`, so it exercises the middleware exactly as a real
request would — not just a direct function call — closing the gap between Task 2's unit tests and
an actual mounted route. Covers both Problem and Change (the two recordClasses the design doc's own
test plan names explicitly, spec §8) reading their own domain's comments route and being blocked
from each other's.

- [ ] **Step 1: Write the failing test**

Create `itsm-backend/middleware/workitem_rbac_integration_test.go`:

```go
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
// 完全一致的路由：真实中间件 + 一个只返回 200 的桩 handler。用于验证中间件在真实的
// gin 路由匹配（:id 参数解析、tenant/role/client 从上下文取值）链路里行为正确，
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./middleware/... -run TestWorkItemSharedRoute_IntegrationThroughRealRouter -v`
Expected: FAIL only if Task 2/3 aren't in place yet. Since this task runs after Tasks 1-3 are
already committed, this should actually compile immediately — run it once to confirm it currently
PASSES as a sanity check that the wiring from Task 2/3 is correct end-to-end, then treat step 4
below as the real gate.

- [ ] **Step 3: (no implementation step needed — this test exercises existing code from Tasks 1-3)**

- [ ] **Step 4: Run the full middleware package one more time**

Run: `cd itsm-backend && go test ./middleware/... -v`
Expected: PASS, all tests including this new integration test.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add middleware/workitem_rbac_integration_test.go
git commit -m "test(rbac): add router-mounted integration test for WorkItem record_class permission gate"
```

---

## Task 5: Full backend verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full backend test suite**

Run: `cd itsm-backend && go build ./... && go vet ./... && go test ./...`
Expected: build succeeds, vet is clean, all tests pass (no regressions in `controller/`,
`router/`, or other `middleware/` tests touched by the route swap).

- [ ] **Step 2: Manual smoke check (optional but recommended before merging)**

If a local dev DB is available: start the backend (`go run main.go`), log in as a user whose role
only has `problem:read` (no `ticket:read`), and confirm `GET /api/v1/tickets/:id/comments` for a
Problem's `workItemId` returns 200, while the same call for an Incident's `workItemId` returns 403.
This is the manual equivalent of Task 4's automated check, useful as a final sanity pass against
real seeded RBAC data rather than the hardcoded test permissions.

- [ ] **Step 3: Commit (only if Step 1 required fixes)**

If Step 1 surfaced any fix, commit it separately with a message describing what regression it
addressed — do not fold unrelated fixes into Task 1-4's commits.
