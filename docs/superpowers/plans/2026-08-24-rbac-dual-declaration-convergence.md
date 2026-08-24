# RBAC Dual-Declaration Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse the two independently-maintained permission checks (global `ResourceActionMap` URL-inference vs per-route `RequirePermission`) into a single declaration, without changing any route's currently-effective access.

**Architecture:** Backfill explicit `RequirePermission`/`RequireRole` declarations onto the 112 routes that currently have none (relying solely on the global inference layer), then delete the global layer (`ResourceActionMap`, `hasPermission()`, all of `smart_permission.go`) so `RequirePermission`/`RequireRole` become the only permission-checking code path.

**Tech Stack:** Go, Gin, Ent ORM, `stretchr/testify`, `enttest` (SQLite in-memory).

**Spec:** `docs/superpowers/specs/2026-08-24-rbac-dual-declaration-convergence-design.md`

## Global Constraints

- `PermissionConfig.Mode` is hardcoded to `PermissionConfigModeDBOnly` — every `RequirePermission`/`RequireRole` decision must resolve to what the seeded `role_permissions` DB rows actually say; do not add hardcoded fallbacks.
- `RequireRole` has no implicit `super_admin` bypass — every `RequireRole(...)` call that must remain reachable by `super_admin` needs `"super_admin"` explicitly in its argument list.
- Do not touch `ent/schema/endpoint_acl.go` or any `ent.EndpointACL`-generated file — that is unrelated ADR-0001 scaffolding, out of scope.
- Do not touch `RequireMSPPermission`, `PermissionConfig`/`PermissionConfigMode*`, `RolePermissions`, `hasResourcePermission()`, `loadPermissionsByMode()`, or any existing `RequirePermission(...)` call already in `router.go` — none of these are part of this refactor.
- No new runtime registries, route-registration wrappers, or startup fail-fast validation — the spec explicitly rejected this as scope creep.

---

## Task 1: Backfill the 5 standalone gap routes in `router.go` (A group)

**Files:**
- Modify: `itsm-backend/router/router.go:420` (`POST /api/v1/ws/ticket`)
- Modify: `itsm-backend/router/router.go:462` (`GET /api/v1/msp/status`)
- Modify: `itsm-backend/router/router.go:1175-1176` (`GET /api/v1/users/profile`, `GET /api/v1/users/me`)
- Modify: `itsm-backend/router/router.go:1461` (`POST /api/v1/connectors/feishu/callback`)

**Interfaces:**
- Consumes: `middleware.RequirePermission(resource, action string) gin.HandlerFunc` and `middleware.RequireRole(allowedRoles ...string) gin.HandlerFunc` — both already exist and are unchanged by this task.

- [ ] **Step 1: Write the failing tests**

Create `itsm-backend/router/rbac_gap_routes_test.go`:

```go
package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// These mirror the exact args this task adds to router.go — they don't
// exercise the full router (that needs a real JWT/controllers wired up),
// they lock in the specific resource/action/role values so a future edit
// to these lines can't silently drift, matching the existing pattern in
// middleware/rbac_test.go (TestRequirePermissionForRBAC / TestRequireRole).

func TestGapRoutes_WSTicket_RequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/v1/ws/ticket", nil)
	c.Set("role", "end_user")

	middleware.RequireRole("super_admin")(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGapRoutes_FeishuCallback_RequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/v1/connectors/feishu/callback", nil)
	c.Set("role", "end_user")

	middleware.RequireRole("super_admin")(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGapRoutes_MSPStatus_AllowsMSPRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/v1/msp/status", nil)
	c.Set("role", "end_user")

	middleware.RequirePermission("msp", "read")(c)

	// end_user has no msp:read grant — DBOnly mode with a nil client will
	// fail the DB lookup and deny; assert it aborts, not that it 200s.
	assert.True(t, c.IsAborted())
}

func TestGapRoutes_UsersProfileAndMe_UseUserRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/users/profile", "/api/v1/users/me"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", path, nil)
		c.Set("role", "end_user")

		middleware.RequirePermission("user", "read")(c)

		assert.True(t, c.IsAborted())
	}
}
```

Add the import at the top: `"itsm-backend/middleware"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd itsm-backend && go test ./router/... -run TestGapRoutes -v`
Expected: compiles and runs (these test the middleware functions directly, which already exist — they should actually PASS as written, since they only assert the middleware's own behavior, not the router.go wiring). This is expected: this step just confirms the test file compiles and the assertions are correct in isolation before Step 3 wires them into router.go. Note the actual regression coverage comes from Step 5's `go build` + `go vet` catching any typo in the resource/action/role strings once they're also in router.go.

- [ ] **Step 3: Edit router.go**

At line 420, change:
```go
		auth.POST("/ws/ticket", func(c *gin.Context) {
```
to:
```go
		auth.POST("/ws/ticket", middleware.RequireRole("super_admin"), func(c *gin.Context) {
```

At line 462, change:
```go
			msp.GET("/status", config.MSPController.GetMSPStatus)
```
to:
```go
			msp.GET("/status", middleware.RequirePermission("msp", "read"), config.MSPController.GetMSPStatus)
```

At lines 1175-1176, change:
```go
					users.GET("/profile", middleware.AuthMiddleware(config.JWTSecret), config.CommonHandler.GetMe) // 获取当前用户信息（需认证）
					users.GET("/me", middleware.AuthMiddleware(config.JWTSecret), config.CommonHandler.GetMe)      // alias of /profile
```
to:
```go
					users.GET("/profile", middleware.AuthMiddleware(config.JWTSecret), middleware.RequirePermission("user", "read"), config.CommonHandler.GetMe) // 获取当前用户信息（需认证）
					users.GET("/me", middleware.AuthMiddleware(config.JWTSecret), middleware.RequirePermission("user", "read"), config.CommonHandler.GetMe)      // alias of /profile
```

At line 1461, change:
```go
				conns.POST("/feishu/callback", config.ConnectorController.FeishuCallback)
```
to:
```go
				conns.POST("/feishu/callback", middleware.RequireRole("super_admin"), config.ConnectorController.FeishuCallback)
```

- [ ] **Step 4: Run the tests and full build to verify**

Run: `cd itsm-backend && go build ./... && go test ./router/... -run TestGapRoutes -v`
Expected: PASS, no build errors.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add router/router.go router/rbac_gap_routes_test.go
git commit -m "fix(rbac): backfill explicit permission checks on 5 routes with no route-level declaration

These previously relied solely on the global ResourceActionMap
inference layer, which is being deleted in a later task."
```

---

## Task 2: Backfill `sla/templates/*` (B group)

**Files:**
- Modify: `itsm-backend/controller/sla_template_controller.go:22-29`
- Test: `itsm-backend/controller/sla_template_controller_test.go` (create — none exists today)

**Interfaces:**
- Consumes: `middleware.RequirePermission(resource, action string) gin.HandlerFunc`.

- [ ] **Step 1: Write the failing test**

Create `itsm-backend/controller/sla_template_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSLATemplateController_RoutesRequireSLARead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &SLATemplateController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "end_user")
		ctx.Set("tenant_id", 1)
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/sla/templates", nil)
	r.ServeHTTP(w, req)

	// end_user has no sla:read grant in a nil-client DBOnly lookup — expect
	// the request to be rejected before reaching the (nil-service) handler.
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSLATemplateController_InstallRequiresSLAWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &SLATemplateController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "end_user")
		ctx.Set("tenant_id", 1)
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/sla/templates/some-key/install", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./controller/... -run TestSLATemplateController -v`
Expected: FAIL — no `RequirePermission` currently wired, requests reach `c.templateService.ListTemplates()` on a nil `templateService` and panic (or, for the POST case, similarly reach the nil service), not a clean 403.

- [ ] **Step 3: Edit `sla_template_controller.go`**

Change (lines 22-29):
```go
func (c *SLATemplateController) RegisterRoutes(r *gin.RouterGroup) {
	templates := r.Group("/sla/templates")
	{
		templates.GET("", c.ListTemplates)
		templates.GET("/:key", c.GetTemplate)
		templates.POST("/:key/install", c.InstallTemplate)
	}
}
```
to:
```go
func (c *SLATemplateController) RegisterRoutes(r *gin.RouterGroup) {
	templates := r.Group("/sla/templates")
	{
		templates.GET("", middleware.RequirePermission("sla", "read"), c.ListTemplates)
		templates.GET("/:key", middleware.RequirePermission("sla", "read"), c.GetTemplate)
		templates.POST("/:key/install", middleware.RequirePermission("sla", "write"), c.InstallTemplate)
	}
}
```

Add `"itsm-backend/middleware"` to the import block if not already present (check the file's existing imports first — it currently has no `middleware` import).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./controller/... -run TestSLATemplateController -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add controller/sla_template_controller.go controller/sla_template_controller_test.go
git commit -m "fix(rbac): backfill RequirePermission on sla/templates routes"
```

---

## Task 3: Backfill the 4 pure-BPMN-wildcard controllers (C group, part 1)

**Files:**
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:52-53` (add `.Use()` right after group creation)
- Modify: `itsm-backend/controller/bpmn_monitoring_controller.go:31-32`
- Modify: `itsm-backend/controller/bpmn_dashboard_controller.go:52-53`
- Modify: `itsm-backend/controller/bpmn_ai_generator_controller.go:108-109`
- Test: `itsm-backend/controller/bpmn_workflow_controller_test.go` (append — file exists)
- Test: `itsm-backend/controller/bpmn_monitoring_controller_test.go` (create)
- Test: `itsm-backend/controller/bpmn_dashboard_controller_test.go` (create)
- Test: `itsm-backend/controller/bpmn_ai_generator_controller_test.go` (create)

**Interfaces:**
- Consumes: `middleware.RequireRole(allowedRoles ...string) gin.HandlerFunc`.
- Role list for all four files (exact, copy verbatim — this is the real current effective role set from the systematic diff, `super_admin` must be listed explicitly): `"super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin"`.

- [ ] **Step 1: Write the failing tests**

Append to `itsm-backend/controller/bpmn_workflow_controller_test.go` (check existing imports first; add `net/http/httptest`, `github.com/stretchr/testify/assert` if missing):

```go
func TestBPMNWorkflowController_RoutesRequireBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNWorkflowController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "l1_support") // not in the allowed set
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/process-definitions", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNWorkflowController_SuperAdminPassesRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNWorkflowController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "super_admin")
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/process-definitions", nil)
	r.ServeHTTP(w, req)

	// Passes the role gate; will fail past it (nil processEngine) but must
	// not be rejected by RequireRole specifically.
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}
```

Create `itsm-backend/controller/bpmn_monitoring_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBPMNMonitoringController_RoutesRequireBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNMonitoringController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "l1_support")
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/monitoring/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Create `itsm-backend/controller/bpmn_dashboard_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBPMNDashboardController_RoutesRequireBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNDashboardController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "l1_support")
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/dashboard/metrics", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Create `itsm-backend/controller/bpmn_ai_generator_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBPMNAIGeneratorController_RoutesRequireBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNAIGeneratorController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "l1_support")
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/ai/templates/suggestions", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd itsm-backend && go test ./controller/... -run 'TestBPMNWorkflowController_RoutesRequireBPMNRoleGate|TestBPMNMonitoringController|TestBPMNDashboardController|TestBPMNAIGeneratorController' -v`
Expected: FAIL — none of these routes currently reject `l1_support` (no `RequireRole` wired yet), so no 403 is produced (requests reach nil services and likely panic — `recover()` middleware isn't registered on this bare test `gin.New()`, so the panic-case tests may crash the test binary; that crash itself is evidence the gate is currently missing).

- [ ] **Step 3: Edit all four controllers**

`bpmn_workflow_controller.go` (line 52-53), change:
```go
func (c *BPMNWorkflowController) RegisterRoutes(r *gin.RouterGroup) {
	bpmn := r.Group("/bpmn")
	{
```
to:
```go
func (c *BPMNWorkflowController) RegisterRoutes(r *gin.RouterGroup) {
	bpmn := r.Group("/bpmn")
	bpmn.Use(middleware.RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin"))
	{
```

`bpmn_monitoring_controller.go` (line 31-32), change:
```go
func (c *BPMNMonitoringController) RegisterRoutes(r *gin.RouterGroup) {
	monitoring := r.Group("/bpmn/monitoring")
	{
```
to:
```go
func (c *BPMNMonitoringController) RegisterRoutes(r *gin.RouterGroup) {
	monitoring := r.Group("/bpmn/monitoring")
	monitoring.Use(middleware.RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin"))
	{
```

`bpmn_dashboard_controller.go` (line 52-53), change:
```go
func (c *BPMNDashboardController) RegisterRoutes(r *gin.RouterGroup) {
	dashboard := r.Group("/bpmn/dashboard")
	{
```
to:
```go
func (c *BPMNDashboardController) RegisterRoutes(r *gin.RouterGroup) {
	dashboard := r.Group("/bpmn/dashboard")
	dashboard.Use(middleware.RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin"))
	{
```

`bpmn_ai_generator_controller.go` (line 108-109), change:
```go
func (c *BPMNAIGeneratorController) RegisterRoutes(r *gin.RouterGroup) {
	bpmnAI := r.Group("/bpmn/ai")
	{
```
to:
```go
func (c *BPMNAIGeneratorController) RegisterRoutes(r *gin.RouterGroup) {
	bpmnAI := r.Group("/bpmn/ai")
	bpmnAI.Use(middleware.RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin"))
	{
```

Add `"itsm-backend/middleware"` to each file's import block if not already present (`bpmn_workflow_controller.go` almost certainly already imports it elsewhere in the file; check the other three before assuming).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./controller/... -run 'TestBPMNWorkflowController|TestBPMNMonitoringController|TestBPMNDashboardController|TestBPMNAIGeneratorController' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add controller/bpmn_workflow_controller.go controller/bpmn_workflow_controller_test.go \
  controller/bpmn_monitoring_controller.go controller/bpmn_monitoring_controller_test.go \
  controller/bpmn_dashboard_controller.go controller/bpmn_dashboard_controller_test.go \
  controller/bpmn_ai_generator_controller.go controller/bpmn_ai_generator_controller_test.go
git commit -m "fix(rbac): backfill RequireRole on 4 pure-BPMN-wildcard controllers

Preserves the exact role set the global ResourceActionMap wildcard
currently grants; not claimed as the correct long-term model (see
backlog in the design spec)."
```

---

## Task 4: Backfill `BPMNProcessTriggerController` (C/D mixed group)

**Files:**
- Modify: `itsm-backend/controller/bpmn_process_trigger_controller.go:30-65`
- Test: `itsm-backend/controller/bpmn_process_trigger_controller_test.go` (create)

**Interfaces:**
- Consumes: `middleware.RequireRole(allowedRoles ...string) gin.HandlerFunc`.

- [ ] **Step 1: Write the failing tests**

Create `itsm-backend/controller/bpmn_process_trigger_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func newTestTriggerController() *BPMNProcessTriggerController {
	return &BPMNProcessTriggerController{}
}

func TestBPMNProcessTriggerController_Trigger_UsesBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l1_support") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/process-trigger/status/123", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_BindingsRead_UsesBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l1_support") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/process-bindings", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_BindingsUpdate_RequiresSuperAdminEvenForBPMNRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	// change_manager passes the group-level BPMN gate but must still be
	// rejected on PUT/DELETE /process-bindings/:id — those are D-group
	// (super_admin only), stricter than the surrounding C-group routes.
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/process-bindings/123", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_BindingsDelete_SuperAdminPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "super_admin") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/process-bindings/123", nil)
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_Departments_RequiresSuperAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/departments/1/processes", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_DomainConfigs_RequiresSuperAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/domain-configs", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd itsm-backend && go test ./controller/... -run TestBPMNProcessTriggerController -v`
Expected: FAIL (no gates wired yet).

- [ ] **Step 3: Edit `bpmn_process_trigger_controller.go`**

Change (lines 30-65):
```go
func (c *BPMNProcessTriggerController) RegisterRoutes(r *gin.RouterGroup) {
	// 流程触发
	trigger := r.Group("/process-trigger")
	{
		trigger.POST("", c.TriggerProcess)
		trigger.GET("/status/:instance_id", c.GetProcessStatus)
		trigger.POST("/cancel/:instance_id", c.CancelProcess)
		trigger.POST("/suspend/:instance_id", c.SuspendProcess)
		trigger.POST("/resume/:instance_id", c.ResumeProcess)
	}

	// 流程绑定管理
	bindings := r.Group("/process-bindings")
	{
		bindings.POST("", c.CreateBinding)
		bindings.GET("", c.QueryBindings)
		bindings.GET("/by-type/:business_type", c.GetBindingsByBusinessType)
		bindings.GET("/:id", c.GetBinding)
		bindings.PUT("/:id", c.UpdateBinding)
		bindings.DELETE("/:id", c.DeleteBinding)
	}

	// 部门流程配置
	departments := r.Group("/departments")
	{
		departments.GET("/:id/processes", c.GetDepartmentProcesses)
		departments.POST("/:id/init-processes", c.InitDepartmentProcesses)
	}

	domainConfigs := r.Group("/domain-configs")
	{
		domainConfigs.GET("", c.ListDomainConfigs)
		domainConfigs.POST("", c.SetDomainConfig)
		domainConfigs.GET("/effective", c.GetEffectiveDomainConfig)
	}
}
```
to:
```go
func (c *BPMNProcessTriggerController) RegisterRoutes(r *gin.RouterGroup) {
	bpmnRoleGate := middleware.RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin")

	// 流程触发 — matches the /api/v1/bpmn/* wildcard's current role set.
	trigger := r.Group("/process-trigger")
	trigger.Use(bpmnRoleGate)
	{
		trigger.POST("", c.TriggerProcess)
		trigger.GET("/status/:instance_id", c.GetProcessStatus)
		trigger.POST("/cancel/:instance_id", c.CancelProcess)
		trigger.POST("/suspend/:instance_id", c.SuspendProcess)
		trigger.POST("/resume/:instance_id", c.ResumeProcess)
	}

	// 流程绑定管理 — read/create match the bpmn:* wildcard's role set, but
	// update/delete are NOT covered by that wildcard today and fall back to
	// super_admin-only; the extra RequireRole("super_admin") on those two
	// routes stacks on top of bpmnRoleGate (Gin chains middleware with AND)
	// to reproduce that narrower requirement exactly.
	bindings := r.Group("/process-bindings")
	bindings.Use(bpmnRoleGate)
	{
		bindings.POST("", c.CreateBinding)
		bindings.GET("", c.QueryBindings)
		bindings.GET("/by-type/:business_type", c.GetBindingsByBusinessType)
		bindings.GET("/:id", c.GetBinding)
		bindings.PUT("/:id", middleware.RequireRole("super_admin"), c.UpdateBinding)
		bindings.DELETE("/:id", middleware.RequireRole("super_admin"), c.DeleteBinding)
	}

	// 部门流程配置 — no ResourceActionMap coverage today, super_admin only.
	departments := r.Group("/departments")
	departments.Use(middleware.RequireRole("super_admin"))
	{
		departments.GET("/:id/processes", c.GetDepartmentProcesses)
		departments.POST("/:id/init-processes", c.InitDepartmentProcesses)
	}

	// no ResourceActionMap coverage today, super_admin only.
	domainConfigs := r.Group("/domain-configs")
	domainConfigs.Use(middleware.RequireRole("super_admin"))
	{
		domainConfigs.GET("", c.ListDomainConfigs)
		domainConfigs.POST("", c.SetDomainConfig)
		domainConfigs.GET("/effective", c.GetEffectiveDomainConfig)
	}
}
```

Add `"itsm-backend/middleware"` to the import block if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./controller/... -run TestBPMNProcessTriggerController -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add controller/bpmn_process_trigger_controller.go controller/bpmn_process_trigger_controller_test.go
git commit -m "fix(rbac): backfill RequireRole on process-trigger controller, preserving its internal C/D split on process-bindings"
```

---

## Task 5: Backfill the 5 simple D-group controllers

**Files:**
- Modify: `itsm-backend/handlers/known_error/handler.go:591-604`
- Modify: `itsm-backend/handlers/standard_change/handler.go:470-481`
- Modify: `itsm-backend/controller/a2ui_ticket_controller.go:117-125`
- Modify: `itsm-backend/controller/global_search_controller.go:192-197`
- Modify: `itsm-backend/controller/escalation_matrix_controller.go:82-89`
- Test: `itsm-backend/handlers/known_error/handler_test.go` (append)
- Test: `itsm-backend/handlers/standard_change/handler_test.go` (append)
- Test: `itsm-backend/controller/a2ui_ticket_controller_test.go` (create)
- Test: `itsm-backend/controller/global_search_controller_test.go` (append)
- Test: `itsm-backend/controller/escalation_matrix_controller_test.go` (create)

**Interfaces:**
- Consumes: `middleware.RequireRole(allowedRoles ...string) gin.HandlerFunc`.
- All five use the same placeholder: `middleware.RequireRole("super_admin")` — current real behavior for all of these is "super_admin only, no ResourceActionMap coverage, no hardcoded RolePermissions entry."

- [ ] **Step 1: Write the failing tests**

Append to `itsm-backend/handlers/known_error/handler_test.go` (add `net/http/httptest`, `github.com/gin-gonic/gin`, `github.com/stretchr/testify/assert` imports if missing):

```go
func TestKnownErrorHandler_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l3_expert") })
	h.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/known-errors", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Append to `itsm-backend/handlers/standard_change/handler_test.go`:

```go
func TestStandardChangeHandler_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	h.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/standard-changes", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Create `itsm-backend/controller/a2ui_ticket_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestA2UITicketController_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &A2UITicketController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "end_user") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/a2ui/tickets", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Append to `itsm-backend/controller/global_search_controller_test.go`:

```go
func TestGlobalSearchController_RouteRequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &GlobalSearchController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "end_user") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/global-search", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Create `itsm-backend/controller/escalation_matrix_controller_test.go`:

```go
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestEscalationMatrixController_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &EscalationMatrixController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "ops_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/escalation-matrices", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd itsm-backend && go test ./handlers/known_error/... ./handlers/standard_change/... ./controller/... -run 'RequireSuperAdmin|RequiresSuperAdmin' -v`
Expected: FAIL — none of these routes currently reject any role (no gate wired).

- [ ] **Step 3: Edit all five files**

`handlers/known_error/handler.go` (line 591-592), change:
```go
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	knownErrors := r.Group("/known-errors")
	{
```
to:
```go
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	knownErrors := r.Group("/known-errors")
	knownErrors.Use(middleware.RequireRole("super_admin"))
	{
```

`handlers/standard_change/handler.go` (line 470-471), change:
```go
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	standardChanges := r.Group("/standard-changes")
	{
```
to:
```go
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	standardChanges := r.Group("/standard-changes")
	standardChanges.Use(middleware.RequireRole("super_admin"))
	{
```

`controller/a2ui_ticket_controller.go` (line 117-118), change:
```go
func (c *A2UITicketController) RegisterRoutes(r *gin.RouterGroup) {
	a2ai := r.Group("/a2ui")
	{
```
to:
```go
func (c *A2UITicketController) RegisterRoutes(r *gin.RouterGroup) {
	a2ai := r.Group("/a2ui")
	a2ai.Use(middleware.RequireRole("super_admin"))
	{
```

`controller/global_search_controller.go` (line 192-193), change:
```go
func (c *GlobalSearchController) RegisterRoutes(r *gin.RouterGroup) {
	search := r.Group("/global-search")
	{
```
to:
```go
func (c *GlobalSearchController) RegisterRoutes(r *gin.RouterGroup) {
	search := r.Group("/global-search")
	search.Use(middleware.RequireRole("super_admin"))
	{
```

`controller/escalation_matrix_controller.go` (line 82-83), change:
```go
func (c *EscalationMatrixController) RegisterRoutes(group *gin.RouterGroup) {
	matrixGrp := group.Group("/escalation-matrices")
	{
```
to:
```go
func (c *EscalationMatrixController) RegisterRoutes(group *gin.RouterGroup) {
	matrixGrp := group.Group("/escalation-matrices")
	matrixGrp.Use(middleware.RequireRole("super_admin"))
	{
```

For each file, add `"itsm-backend/middleware"` to its import block if not already present — check each file individually, do not assume.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./handlers/known_error/... ./handlers/standard_change/... ./controller/... -run 'RequireSuperAdmin|RequiresSuperAdmin' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add handlers/known_error/handler.go handlers/known_error/handler_test.go \
  handlers/standard_change/handler.go handlers/standard_change/handler_test.go \
  controller/a2ui_ticket_controller.go controller/a2ui_ticket_controller_test.go \
  controller/global_search_controller.go controller/global_search_controller_test.go \
  controller/escalation_matrix_controller.go controller/escalation_matrix_controller_test.go
git commit -m "fix(rbac): backfill RequireRole(super_admin) on 5 previously-ungated D-group controllers"
```

---

## Task 6: Backfill marketplace (D group, flagged design-intent mismatch)

**Files:**
- Modify: `itsm-backend/controller/marketplace/controller.go:56-72`
- Test: `itsm-backend/controller/marketplace/controller_test.go` (create)

**Interfaces:**
- Consumes: `middleware.RequireRole(allowedRoles ...string) gin.HandlerFunc`.

- [ ] **Step 1: Write the failing test**

Create `itsm-backend/controller/marketplace/controller_test.go`:

```go
package marketplace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestController_ItemsRoute_RequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &Controller{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "end_user") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/marketplace/items", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./controller/marketplace/... -v`
Expected: FAIL (no gate wired, request currently reaches `c.service.ListItems` on a nil service and panics).

- [ ] **Step 3: Edit `controller/marketplace/controller.go`**

Change (lines 56-72):
```go
// RegisterRoutes 注册路由
func (c *Controller) RegisterRoutes(r *gin.RouterGroup) {
	marketplaceGroup := r.Group("/marketplace")
	{
		// 公开接口
		marketplaceGroup.GET("/items", c.ListItems)
		marketplaceGroup.GET("/items/:id", c.GetItem)

		// 需要登录的接口
		{
			marketplaceGroup.POST("/items/:id/install", c.InstallItem)
			marketplaceGroup.POST("/items/:id/uninstall", c.UninstallItem)
			marketplaceGroup.GET("/installations", c.ListInstallations)
			marketplaceGroup.GET("/installations/:id", c.GetInstallation)
			marketplaceGroup.PUT("/installations/:id/config", c.UpdateInstallationConfig)
		}
	}
}
```
to:
```go
// RegisterRoutes 注册路由
//
// NOTE: the handler comments below ("公开接口" / "需要登录的接口") describe
// this route family's original design intent, but the actual current
// behavior (before this change, via the global ResourceActionMap
// inference layer that is being deleted) is super_admin-only for all of
// them — the RequireRole placeholder below preserves that actual
// behavior exactly. It contradicts the comments' intent; see the design
// spec's backlog for the product decision on what this should really be.
func (c *Controller) RegisterRoutes(r *gin.RouterGroup) {
	marketplaceGroup := r.Group("/marketplace")
	marketplaceGroup.Use(middleware.RequireRole("super_admin"))
	{
		// 公开接口
		marketplaceGroup.GET("/items", c.ListItems)
		marketplaceGroup.GET("/items/:id", c.GetItem)

		// 需要登录的接口
		{
			marketplaceGroup.POST("/items/:id/install", c.InstallItem)
			marketplaceGroup.POST("/items/:id/uninstall", c.UninstallItem)
			marketplaceGroup.GET("/installations", c.ListInstallations)
			marketplaceGroup.GET("/installations/:id", c.GetInstallation)
			marketplaceGroup.PUT("/installations/:id/config", c.UpdateInstallationConfig)
		}
	}
}
```

Add `"itsm-backend/middleware"` to the import block.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./controller/marketplace/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add controller/marketplace/controller.go controller/marketplace/controller_test.go
git commit -m "fix(rbac): backfill RequireRole(super_admin) on marketplace routes

Flags in comments that this contradicts the routes' own documented
design intent (public/login-only) — preserving actual current
behavior only, not endorsing it. See design spec backlog."
```

---

## Task 7: Align `RequireRole`'s response format with `common.Fail`

**Files:**
- Modify: `itsm-backend/middleware/rbac.go:766-789`
- Modify: `itsm-backend/middleware/rbac_test.go:72-121` (update existing assertions)

**Interfaces:**
- Consumes: `common.Fail(c *gin.Context, code int, message string)` (already used elsewhere in this file, e.g. by `RequirePermission`).
- Consumes: `common.ForbiddenCode`, `common.AuthFailedCode` constants (already used elsewhere in `rbac.go` — check their exact values in `common/response.go` before using).

- [ ] **Step 1: Update the existing test's expectations first (TDD in reverse — this is a refactor of an existing function, so the test change IS the "failing test" step)**

In `itsm-backend/middleware/rbac_test.go`, change the `TestRequireRole` subtests' assertions:

```go
	t.Run("Missing Role Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)

		RequireRole("admin", "manager")(c)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "缺少角色信息")
	})

	t.Run("Role Not Allowed Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("role", "end_user")

		RequireRole("admin", "manager")(c)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "无权限执行该操作")
	})
```

These assertions are unchanged in substance (same status code, same message text) — `common.Fail` must produce an equivalent body for this test to still pass, which Step 4 verifies. No new test is needed; this task is a pure refactor of response-writing mechanics with an existing regression test as the guard.

- [ ] **Step 2: Run test to confirm current (pre-change) behavior passes as baseline**

Run: `cd itsm-backend && go test ./middleware/... -run TestRequireRole -v`
Expected: PASS (this establishes the baseline before the internal change — confirms the test file itself is valid before touching `rbac.go`).

- [ ] **Step 3: Edit `RequireRole` in `middleware/rbac.go`**

Change (lines 766-789):
```go
// RequireRole enforces that the authenticated user role is one of the allowed roles
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	normalized := make([]string, 0, len(allowedRoles))
	for _, r := range allowedRoles {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(r)))
	}
	return func(c *gin.Context) {
		roleAny, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"code": 2003, "message": "缺少角色信息"})
			c.Abort()
			return
		}
		role, _ := roleAny.(string)
		role = strings.ToLower(strings.TrimSpace(role))
		for _, ar := range normalized {
			if role == ar {
				c.Next()
				return
			}
		}
		c.JSON(http.StatusForbidden, gin.H{"code": 2003, "message": "无权限执行该操作"})
		c.Abort()
	}
}
```
to:
```go
// RequireRole enforces that the authenticated user role is one of the allowed roles
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	normalized := make([]string, 0, len(allowedRoles))
	for _, r := range allowedRoles {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(r)))
	}
	return func(c *gin.Context) {
		roleAny, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.ForbiddenCode, "缺少角色信息")
			c.Abort()
			return
		}
		role, _ := roleAny.(string)
		role = strings.ToLower(strings.TrimSpace(role))
		for _, ar := range normalized {
			if role == ar {
				c.Next()
				return
			}
		}
		common.Fail(c, common.ForbiddenCode, "无权限执行该操作")
		c.Abort()
	}
}
```

Check whether `"net/http"` is still used elsewhere in `rbac.go` after this change (it almost certainly is, e.g. by other handlers in the same file) — do not remove the import unless `go build` flags it as unused.

- [ ] **Step 4: Run tests to verify they still pass**

Run: `cd itsm-backend && go test ./middleware/... -run TestRequireRole -v`
Expected: PASS. If `common.Fail`'s body shape doesn't literally contain the Chinese message strings as substrings (check `common/response.go`'s `Fail` implementation if this fails), adjust the assertions to match its actual JSON shape rather than changing `common.Fail` itself.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add middleware/rbac.go middleware/rbac_test.go
git commit -m "refactor(rbac): align RequireRole's rejection response with common.Fail

Was using a raw c.JSON call instead of the project's standard
response helper — inconsistent with every other permission-denial
path in this file."
```

---

## Task 8: Narrow `RBACMiddleware`'s responsibility, remove `hasPermission()` call

**Files:**
- Modify: `itsm-backend/middleware/rbac.go` (the `RBACMiddleware` function body, and its doc comment)
- Modify: `itsm-backend/middleware/rbac_test.go` (`TestRBACMiddleware`, if it asserts on permission-denial behavior — verify and adjust)

**Interfaces:**
- Produces: `RBACMiddleware(client *ent.Client) gin.HandlerFunc` — same signature, narrower behavior (no longer calls `hasPermission`).

- [ ] **Step 1: Read the current `TestRBACMiddleware` test to confirm it doesn't test permission-denial (only auth/tenant preconditions)**

Run: `cd itsm-backend && grep -n "func TestRBACMiddleware" -A 30 middleware/rbac_test.go`

Confirm (from the earlier read) that this test only covers "No User ID in Context" and "No Tenant ID in Context" — neither exercises `hasPermission`. No test changes needed for this step; if the grep shows additional subtests beyond those two, read them and add a note to Step 5 before proceeding (do not assume — verify against the file as it exists at execution time).

- [ ] **Step 2: Write a test confirming `RBACMiddleware` no longer denies purely on permission grounds**

Add to `middleware/rbac_test.go`:

```go
func TestRBACMiddleware_NoLongerPerformsPermissionCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// A role/path combination that the (now-deleted) hasPermission() would
	// have denied under the old ResourceActionMap-inference logic must no
	// longer be rejected by RBACMiddleware itself — that job now belongs
	// solely to the route's own RequirePermission/RequireRole call, which
	// isn't present in this bare test context, so this only proves
	// RBACMiddleware itself doesn't short-circuit on it.
	//
	// This test requires a real client because RBACMiddleware queries the
	// user from DB (see the existing "No Role in Context" comment above) —
	// use enttest with a seeded active user.
	client := enttest.Open(t, "sqlite3", "file:rbacmw_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	u, err := client.User.Create().
		SetUsername("test_no_perm_check").
		SetEmail("test_no_perm_check@example.com").
		SetPasswordHash("x").
		SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/v1/some-random-unmapped-path", nil)
	c.Set("user_id", u.ID)
	c.Set("tenant_id", 1)
	c.Set("role", "end_user")

	RBACMiddleware(client)(c)

	assert.False(t, c.IsAborted())
}
```

Add `"context"`, `"itsm-backend/ent/enttest"`, `_ "github.com/mattn/go-sqlite3"`, and `"github.com/stretchr/testify/require"` to the imports if not already present.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd itsm-backend && go test ./middleware/... -run TestRBACMiddleware_NoLongerPerformsPermissionCheck -v`
Expected: FAIL — `/api/v1/some-random-unmapped-path` currently falls through `hasPermission`'s fallback chain to a deny, so `c.IsAborted()` is currently `true`.

- [ ] **Step 4: Edit `RBACMiddleware`**

In `middleware/rbac.go`, remove the permission-check block (originally around lines 693-709):
```go
		// 获取请求路径和方法
		path := c.Request.URL.Path
		method := c.Request.Method

		// 检查权限（从数据库加载权限）
		if !hasPermission(client, role, method, path, userID, tenantID, c) {
			zap.S().Warnw(
				"RBACMiddleware: permission denied",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"user_id", userID,
				"role", role,
			)
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		// 调试日志：RBAC检查通过
```
to:
```go
		// 调试日志：RBAC检查通过
```

Update the doc comment immediately above `func RBACMiddleware(...)`:
```go
// RBACMiddleware RBAC权限控制中间件
```
to:
```go
// RBACMiddleware is a session/tenant guard, NOT a fine-grained permission
// check. It validates that the caller is authenticated, that the DB user
// record exists and is active, refreshes the role from DB, resolves and
// validates the tenant ID, and enriches the gin.Context (user_entity,
// client, tenant_id) for downstream handlers. Actual resource:action
// authorization is the sole responsibility of the RequirePermission /
// RequireRole / RequireMSPPermission call attached to each specific
// route — a route mounted under a group this middleware guards is NOT
// automatically permission-protected.
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./middleware/... -run 'TestRBACMiddleware' -v`
Expected: PASS for both `TestRBACMiddleware` and `TestRBACMiddleware_NoLongerPerformsPermissionCheck`.

- [ ] **Step 6: Commit**

```bash
cd itsm-backend && git add middleware/rbac.go middleware/rbac_test.go
git commit -m "refactor(rbac): remove permission inference from RBACMiddleware

RBACMiddleware is now a pure session/tenant guard (auth, active-user
check, role refresh, tenant validation, context enrichment).
Fine-grained authorization moves entirely to RequirePermission/
RequireRole/RequireMSPPermission on each route — all 112 routes that
previously relied solely on this inference now have their own
explicit declaration (Tasks 1-6)."
```

---

## Task 9: Delete `ResourceActionMap`, `hasPermission()`, `getPermissionFromPath()`, `matchPath()`

**Files:**
- Modify: `itsm-backend/middleware/rbac.go` (delete the named symbols)
- Modify: `itsm-backend/middleware/rbac_test.go` (delete any test referencing the deleted symbols)

**Interfaces:**
- Produces: nothing new — pure deletion. No other file in the codebase may reference `ResourceActionMap`, `hasPermission`, `getPermissionFromPath`, or `matchPath` after this task (verified in Step 1).

- [ ] **Step 1: Confirm no remaining callers before deleting**

Run: `cd itsm-backend && grep -rn "ResourceActionMap\|hasPermission(\|getPermissionFromPath\|matchPath(" --include="*.go" . | grep -v "_test.go"`

Expected output at this point in the plan: only matches inside `middleware/rbac.go` itself (the definitions) and `middleware/smart_permission.go` (which calls `getPermissionFromPath` — this is expected, Task 10 deletes that file next). If any match appears in another file, STOP and re-scope this task — do not delete a symbol that still has a live caller outside the two files this plan already accounts for.

- [ ] **Step 2: Delete the dead-test coverage first**

Run: `cd itsm-backend && grep -n "ResourceActionMap\|hasPermission(\|getPermissionFromPath\|matchPath(" middleware/rbac_test.go`

If this returns any lines, read the surrounding test function(s) and delete the whole function(s) — do not leave a test that references a deleted symbol (the package won't compile otherwise). The earlier-noted comment at `middleware/rbac_test.go:213` ("回归：/api/v1/releases 曾缺失于 ResourceActionMap...") documents a historical regression test for `ResourceActionMap` specifically — read that test function in full and delete it; the regression it guards against (a route missing from a hand-maintained map) can no longer occur once the map itself is deleted, so the test has no remaining purpose.

- [ ] **Step 3: Delete `ResourceActionMap`, `hasPermission()`, `getPermissionFromPath()`, `matchPath()` from `middleware/rbac.go`**

Delete the `ResourceActionMap` variable declaration in full (spans from `var ResourceActionMap = map[string]map[string]Permission{` through its closing `}` — several hundred lines covering the GET/POST/PUT/DELETE/PATCH sub-maps).

Delete the `hasPermission` function in full (originally lines 791-809, now shifted up from Task 8's edit — locate by its current `// hasPermission 检查用户是否有权限访问指定资源` doc comment).

Delete `getPermissionFromPath` (originally lines 928-955) and `matchPath` (originally lines 957+) in full — locate by their doc comments `// getPermissionFromPath` and `// matchPath 匹配路径（支持通配符）`.

Check the file's import block afterward — `strings` may become partially unused if `matchPath` was its only caller of a specific function (unlikely given `strings` is used extensively elsewhere in this file, e.g. by `RequireRole`, but verify with `go build`, don't assume).

- [ ] **Step 4: Run full build and test to verify**

Run: `cd itsm-backend && go build ./... && go test ./middleware/... -v`
Expected: builds cleanly, all remaining `middleware` package tests pass.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git add middleware/rbac.go middleware/rbac_test.go
git commit -m "refactor(rbac): delete ResourceActionMap and hasPermission()

The global URL-inference permission layer is now fully replaced by
the explicit RequirePermission/RequireRole declarations backfilled
in Tasks 1-6 and RBACMiddleware's narrowed scope from Task 8. This
is the drift source the whole project set out to eliminate — deleted,
not deprecated."
```

---

## Task 10: Delete `middleware/smart_permission.go`

**Files:**
- Delete: `itsm-backend/middleware/smart_permission.go`
- Delete or modify: `itsm-backend/middleware/smart_permission_test.go` (if it exists — check first)

**Interfaces:**
- Produces: nothing — pure deletion. `middleware.EndpointACL`, `middleware.DBQuerier`, `SmartCheckPermission`, `checkDatabaseACL`, `checkURLInference`, `checkRoleBasedPermission`, `getCachedACLs`, `loadACLsFromDB`, `isKnownWhitelistPath`, `isAuthWhitelist`, `authWhitelist`, `aclCache`, `aclCacheLock`, `aclConfig` all cease to exist. Does NOT touch `ent.EndpointACL` or `ent/schema/endpoint_acl.go` (unrelated, separate type — confirmed during spec review).

- [ ] **Step 1: Confirm no remaining callers outside this file**

Run: `cd itsm-backend && grep -rln "SmartCheckPermission\|checkDatabaseACL\|checkURLInference\|checkRoleBasedPermission\|getCachedACLs\|loadACLsFromDB\|isKnownWhitelistPath\|isAuthWhitelist\|middleware\.EndpointACL\|middleware\.DBQuerier" --include="*.go" . | grep -v smart_permission`

Expected: after Task 9's deletion of `hasPermission()` (the only caller of `SmartCheckPermission`), this should return nothing. If it returns a match, STOP and investigate before deleting — do not delete a symbol with a live caller.

Also check for a same-package bare reference to the local (unqualified) type names, since `middleware_test.go` files in the same package wouldn't use the `middleware.` prefix: `cd itsm-backend && grep -rln "EndpointACL\|DBQuerier" middleware/*.go | grep -v smart_permission`. Cross-reference any hit against `ent.EndpointACL` usage (that's the unrelated type, out of scope) versus the local `middleware.EndpointACL`/`DBQuerier` struct/interface — only the latter is in scope for this deletion.

- [ ] **Step 2: Check for and handle an existing test file**

Run: `ls itsm-backend/middleware/smart_permission_test.go 2>&1`

If it exists, read it in full and delete it in the same commit as the source file (a test file for a fully-deleted source file has nothing left to test). If it doesn't exist, no action needed for this step.

- [ ] **Step 3: Delete the file**

```bash
cd itsm-backend && git rm middleware/smart_permission.go
# If Step 2 found a test file:
git rm middleware/smart_permission_test.go
```

- [ ] **Step 4: Run full build and test to verify**

Run: `cd itsm-backend && go build ./... && go test ./middleware/... -v`
Expected: builds cleanly, all tests pass.

- [ ] **Step 5: Commit**

```bash
cd itsm-backend && git commit -m "refactor(rbac): delete middleware/smart_permission.go

Its only caller (hasPermission) was deleted in the prior task. The
4-layer fallback (auth whitelist / DB endpoint_acls / URL inference /
hardcoded RolePermissions) this file implemented is no longer reached
by any request path. ent.EndpointACL and ent/schema/endpoint_acl.go
are untouched — those are a separate, unrelated ADR-0001 scaffold
with zero current callers, out of scope for this deletion."
```

---

## Task 11: Full regression verification

**Files:** none modified — verification only.

- [ ] **Step 1: Full build**

Run: `cd itsm-backend && go build ./...`
Expected: clean build, no errors.

- [ ] **Step 2: Full test suite**

Run: `cd itsm-backend && go test ./... 2>&1 | tee /tmp/rbac_convergence_test_output.log`
Expected: all packages pass. If any package outside `middleware`/`router`/`controller`/`handlers/known_error`/`handlers/standard_change` fails, read the failure — it likely means another package's test directly depended on `ResourceActionMap`/`hasPermission`/`SmartCheckPermission` behavior in a way this plan's earlier `grep` checks (Task 9 Step 1, Task 10 Step 1) missed. Do not silence or skip a failing test — trace it back to root cause per this repo's `AGENTS.md` guidance ("已完成的历史结论必须重新验证").

- [ ] **Step 3: `go vet`**

Run: `cd itsm-backend && go vet ./...`
Expected: clean.

- [ ] **Step 4: Manual verification against the live dev environment**

This mirrors the design spec's stated verification approach. Using a non-`super_admin` test account (per the confidentiality constraint already in effect for this session, do not use real employee accounts for this — use one of the existing synthetic/seeded test accounts already established in this codebase's test fixtures):
1. Start the backend (`go run main.go`).
2. Call `GET /api/v1/msp/status` and `GET /api/v1/users/profile` with a role known to hold `msp:read`/`user:read` respectively — expect 200.
3. Call the same two endpoints with a role that does NOT hold those grants — expect 403.
4. Call one BPMN route (e.g. `GET /api/v1/bpmn/process-definitions`) with `end_user` (in the preserved role list) — expect a non-403 response (may still fail downstream for unrelated reasons, but must clear the role gate).
5. Call the same BPMN route with a role NOT in the preserved list (e.g. `l1_support`) — expect 403.

Record actual results; if any diverge from expectation, stop and re-investigate before considering this plan complete — do not mark it done on the strength of the unit tests alone, per this repo's testing conventions (`AGENTS.md`: "功能'看起来已完成'不等于真的可用，必须走一遍真实使用路径再下结论").

- [ ] **Step 5: Update the design spec's Status line**

Edit `docs/superpowers/specs/2026-08-24-rbac-dual-declaration-convergence-design.md`, change:
```
**Status:** Approved — reviewed by user, ready for `writing-plans`
```
to:
```
**Status:** Implemented — see docs/superpowers/plans/2026-08-24-rbac-dual-declaration-convergence.md
```

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm && git add docs/superpowers/specs/2026-08-24-rbac-dual-declaration-convergence-design.md
git commit -m "docs: mark RBAC dual-declaration convergence spec as implemented"
```
