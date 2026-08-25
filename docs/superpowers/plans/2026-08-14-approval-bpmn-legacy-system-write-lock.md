# 组件④ — 旧审批系统写锁下线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给旧的 `ApprovalWorkflow` 配置 CRUD 加一个租户级写锁开关：管理员确认某个租户已经跑完批量迁移（组件③的 `cmd/migrate_legacy_approvals`）后，通过一个独立的 ops CLI 把该租户标记为"锁定"，之后该租户对 `CreateWorkflow`/`UpdateWorkflow`/`PatchWorkflow`/`DeleteWorkflow` 的调用统一返回明确的"已下线"错误；`/admin/approvals` 前端页面据此禁用对应按钮。历史数据查看（`ListWorkflows`/`GetWorkflow`/`GetApprovalRecords`）和存量待审批的提交（`SubmitApproval`）不受影响。

**Architecture:** 复用已有的 `SystemConfig`（`ent/schema/systemconfig.go`，`tenant_id`+`key` 唯一约束式查询）作为存储，不新增 ent schema、不新增数据库迁移。`ApprovalService` 直接查询这张表（不引入新的服务间依赖），返回一个 sentinel error；`ApprovalController` 把这个 sentinel error 映射成 403。设置开关本身走一个独立的 ops CLI（镜像 `cmd/migrate_legacy_approvals/main.go` 的既有模式），不新增 HTTP 写入端点。

**Tech Stack:** Go/Gin/Ent（后端），Next.js/TypeScript/antd（前端），复用现有的 `SystemConfig` ent 实体和 `SystemConfigAPI` 前端客户端（只读部分）。

**Spec:** `docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md`（组件④章节，2026-08-14 订正过两处：`migrated_to_bpmn_at` 字段实际不存在；写锁只需要覆盖 `ApprovalWorkflow` 配置 CRUD 四个端点，不需要锁 `SubmitApproval`，因为 `TriggerApproval` 已经零调用点）

## Global Constraints

- 不新增 ent schema / 不跑新的数据库迁移——复用现成的 `SystemConfig`（`tenant_id`+`key`）。
- 写锁默认关闭（`SystemConfig` 找不到对应 key 时视为未锁定）——不能因为这次改动让任何现有租户意外被锁。
- 只锁 `CreateWorkflow`/`UpdateWorkflow`/`PatchWorkflow`/`DeleteWorkflow` 四个写入端点；`ListWorkflows`/`GetWorkflow`/`GetApprovalRecords`/`SubmitApproval` 行为在这次改动前后必须完全不变（这四个的回归测试是本计划的强制项，不是可选项）。
- `router/router.go` 里 `approval-workflows` 和 `approvals` 两套重复路由组都指向同一个 controller 方法，所以后端的锁检查放在 controller 调用的 service 方法里（`ApprovalService.CreateWorkflow`/`UpdateWorkflow`/`DeleteWorkflow`），不要在 controller 每个方法里单独判断——这样两套路由组自动都被覆盖，不用改路由注册代码。
- `PatchWorkflow`（controller）内部直接调用 `ApprovalService.UpdateWorkflow`（`controller/approval_controller.go:273`），所以给 `UpdateWorkflow` 加锁检查会自动覆盖 PUT 和 PATCH 两条路由，不需要给 `PatchWorkflow` 单独加检查。
- 不做物理删除旧代码（`approval_controller.go`/`approval_service.go`/`legacy_approval_migration_service.go` 等）——这是当前设计明确的非目标，见 spec 的"非目标"章节。
- 错误码用 `common.ForbiddenCode`（`itsm-backend/common/response.go:23`，值 2003），不要用 `common.InternalErrorCode`——这是"功能已下线"，不是意外的内部错误。
- CLI 工具遵循 `cmd/migrate_legacy_approvals/main.go` 的既有模式（`config.LoadConfig()` + `database.InitDatabaseWithRLS` + `tenantctx.SystemContext`），不要发明新的启动方式。

---

### Task 1: `ApprovalService` 写锁检查

**Files:**
- Modify: `itsm-backend/service/approval_service.go`（在 `CreateWorkflow`/`UpdateWorkflow`/`DeleteWorkflow` 开头各加一次检查；文件顶部加 sentinel error 定义）
- Test: `itsm-backend/service/approval_service_write_lock_test.go`（新文件）

**Interfaces:**
- Produces: `service.ErrLegacyApprovalWriteLocked`（导出的 sentinel error 变量，`controller` 包用 `errors.Is` 匹配它）；`ApprovalService` 的私有方法 `isLegacyApprovalWriteLocked(ctx context.Context, tenantID int) (bool, error)`（仅本文件内部使用，不导出）。

- [ ] **Step 1: 写失败的测试——锁定的租户创建工作流应该返回 `ErrLegacyApprovalWriteLocked`**

创建 `itsm-backend/service/approval_service_write_lock_test.go`：

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupWriteLockTest(t *testing.T) (*ent.Client, *ApprovalService, context.Context) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1&_busy_timeout=5000")
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewApprovalService(client, logger)
	ctx := context.Background()
	return client, svc, ctx
}

func lockTenant(t *testing.T, client *ent.Client, ctx context.Context, tenantID int) {
	t.Helper()
	_, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
}

func TestApprovalService_CreateWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-create")
	require.NoError(t, err)
	lockTenant(t, client, ctx, tenant.ID)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "应该被拒绝的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	count, err := client.ApprovalWorkflow.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "锁定状态下不应该真的创建出工作流记录")
}

func TestApprovalService_CreateWorkflow_UnlockedTenantSucceeds(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "unlocked-create")
	require.NoError(t, err)
	// 不调用 lockTenant——没有对应的 SystemConfig 行，必须视为未锁定（安全默认值）。

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "正常应该成功的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestApprovalService_UpdateWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-update")
	require.NoError(t, err)

	// 先在未锁定状态下创建一条，再锁定，确认锁定只挡"新的写操作"，不影响已存在的数据。
	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "先创建后锁定",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	created, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)

	lockTenant(t, client, ctx, tenant.ID)

	newName := "锁定后不应该改名成功"
	updateReq := &dto.UpdateApprovalWorkflowRequest{Name: &newName}
	resp, err := svc.UpdateWorkflow(ctx, created.ID, updateReq, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	// 确认真的没改成功。
	stillOldName, err := client.ApprovalWorkflow.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "先创建后锁定", stillOldName.Name)
}

func TestApprovalService_DeleteWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-delete")
	require.NoError(t, err)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "不应该被删除的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	created, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)

	lockTenant(t, client, ctx, tenant.ID)

	err = svc.DeleteWorkflow(ctx, created.ID, tenant.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	stillExists, err := client.ApprovalWorkflow.Query().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, created.ID, stillExists.ID)
}

func TestApprovalService_LockIsPerTenant(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	lockedTenant, err := createApprovalTestTenant(ctx, client, "lock-scope-locked")
	require.NoError(t, err)
	otherTenant, err := createApprovalTestTenant(ctx, client, "lock-scope-other")
	require.NoError(t, err)
	lockTenant(t, client, ctx, lockedTenant.ID)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "别的租户不应该被连带锁定",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, otherTenant.ID)
	require.NoError(t, err, "锁定是租户级的，不应该影响别的租户")
	require.NotNil(t, resp)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestApprovalService_CreateWorkflow_LockedTenantAborts|TestApprovalService_UpdateWorkflow_LockedTenantAborts|TestApprovalService_DeleteWorkflow_LockedTenantAborts|TestApprovalService_LockIsPerTenant' -v`

Expected: 编译失败（`ErrLegacyApprovalWriteLocked` 未定义）——这是预期的"红灯"，先确认失败原因是"缺实现"，不是测试本身写错。

- [ ] **Step 3: 实现写锁检查**

在 `itsm-backend/service/approval_service.go` 顶部 import 块加一个：

```go
"errors"
```

（放在 `"context"` 后面，跟其他标准库 import 保持字母序）

在 `itsm-backend/service/approval_service.go` 的 `ApprovalService` struct 定义之前（大约第 20 行之前）加：

```go
// ErrLegacyApprovalWriteLocked 表示这个租户的旧审批工作流配置已经被标记为只读——
// 管理员确认过批量迁移（cmd/migrate_legacy_approvals）跑完之后，用 cmd/lock_legacy_approvals
// 手动锁定。锁定只挡 CreateWorkflow/UpdateWorkflow/DeleteWorkflow 这三个写入配置的入口，
// 不影响历史数据查看（ListWorkflows/GetWorkflow/GetApprovalRecords）或者已有 pending
// 审批的提交（SubmitApproval）——那些不受这个 sentinel error 影响。
var ErrLegacyApprovalWriteLocked = errors.New("旧审批工作流系统已下线，请使用 BPMN 流程设计器")
```

在 `itsm-backend/service/approval_service.go` 顶部 import 块里加 `"itsm-backend/ent/systemconfig"`（跟 `"itsm-backend/ent/approvalworkflow"` 放一起，按字母序）。

在 `ApprovalService` 的方法里（比如紧跟 `NewApprovalService` 构造函数之后）加这个私有辅助方法：

```go
// isLegacyApprovalWriteLocked 查这个租户有没有把 legacyApprovalWriteLocked 这个
// SystemConfig key 设成 "true"。找不到对应的行——不管是这个租户从来没设置过，还是
// SystemConfig 表里压根没这条记录——都视为未锁定（false），这是安全默认值：这次改动
// 之前所有租户都应该继续正常工作，只有管理员显式用 cmd/lock_legacy_approvals 锁过的
// 租户才会被挡。
func (s *ApprovalService) isLegacyApprovalWriteLocked(ctx context.Context, tenantID int) (bool, error) {
	cfg, err := s.client.SystemConfig.Query().
		Where(
			systemconfig.KeyEQ("legacyApprovalWriteLocked"),
			systemconfig.TenantIDEQ(tenantID),
			systemconfig.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("查询审批工作流写锁状态失败: %w", err)
	}
	return cfg.Value == "true", nil
}
```

在 `CreateWorkflow` 方法（`service/approval_service.go:43`）的第一行 `s.logger.Infow(...)` 之后加：

```go
	locked, err := s.isLegacyApprovalWriteLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrLegacyApprovalWriteLocked
	}
```

在 `UpdateWorkflow` 方法（`service/approval_service.go:80`）的第一行 `s.logger.Infow(...)` 之后加同样的检查（把 `req.Name` 之类的日志字段名对应改成 `UpdateWorkflow` 自己的，逻辑跟上面一模一样）：

```go
	locked, err := s.isLegacyApprovalWriteLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrLegacyApprovalWriteLocked
	}
```

在 `DeleteWorkflow` 方法（`service/approval_service.go:137`）的第一行 `s.logger.Infow(...)` 之后加（注意这个方法只返回 `error`，不是 `(*T, error)`）：

```go
	locked, err := s.isLegacyApprovalWriteLocked(ctx, tenantID)
	if err != nil {
		return err
	}
	if locked {
		return ErrLegacyApprovalWriteLocked
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestApprovalService_CreateWorkflow|TestApprovalService_UpdateWorkflow_LockedTenantAborts|TestApprovalService_DeleteWorkflow_LockedTenantAborts|TestApprovalService_LockIsPerTenant' -v`

Expected: 全部 PASS，包括 `TestApprovalService_CreateWorkflow_UnlockedTenantSucceeds`（回归：没设置锁的租户完全不受影响）。

- [ ] **Step 5: 跑一遍 service 包全部测试，确认没有破坏别的东西**

Run: `cd itsm-backend && go test ./service/... -v 2>&1 | tail -80`

Expected: 全绿，尤其关注 `TestApprovalService_` 开头、`TestLegacyApprovalMigrationService_` 开头的既有测试仍然 PASS（这次改动不应该影响迁移工具本身怎么读 `ApprovalWorkflow.Nodes`，迁移工具走的是查询路径，不走 CreateWorkflow/UpdateWorkflow）。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/sr-ticket-unification
git add itsm-backend/service/approval_service.go itsm-backend/service/approval_service_write_lock_test.go
git commit -m "feat(approval): add per-tenant write lock for legacy ApprovalWorkflow config

CreateWorkflow/UpdateWorkflow/DeleteWorkflow now check a SystemConfig
key (legacyApprovalWriteLocked, tenant-scoped) before mutating, returning
ErrLegacyApprovalWriteLocked when a tenant has been marked locked. No
SystemConfig row means unlocked (safe default -- existing tenants are
unaffected until an operator explicitly locks them). Migration reads,
history queries, and SubmitApproval on pre-existing pending records are
untouched."
```

---

### Task 2: `ApprovalController` 错误映射 + 只读端点回归测试

**Files:**
- Modify: `itsm-backend/controller/approval_controller.go`
- Test: `itsm-backend/controller/approval_controller_write_lock_test.go`（新文件）

**Interfaces:**
- Consumes: `service.ErrLegacyApprovalWriteLocked`（Task 1 产出）
- Produces: 无新的导出符号——这一层只是把已有的 sentinel error 映射成 HTTP 403。

- [ ] **Step 1: 写失败的测试——controller 层的锁定应该返回 403 + 明确文案**

创建 `itsm-backend/controller/approval_controller_write_lock_test.go`：

```go
package controller

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

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupWriteLockControllerTest(t *testing.T) (*gin.Engine, *ent.Client) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	logger := zaptest.NewLogger(t).Sugar()
	approvalService := service.NewApprovalService(client, logger)
	approvalController := NewApprovalController(approvalService)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", 1)
		c.Set("user_id", 1)
		c.Next()
	})
	r.POST("/api/v1/approval-workflows", approvalController.CreateWorkflow)
	r.PUT("/api/v1/approval-workflows/:id", approvalController.UpdateWorkflow)
	r.PATCH("/api/v1/approval-workflows/:id", approvalController.PatchWorkflow)
	r.DELETE("/api/v1/approval-workflows/:id", approvalController.DeleteWorkflow)
	r.GET("/api/v1/approval-workflows", approvalController.ListWorkflows)

	return r, client
}

func lockControllerTestTenant(t *testing.T, client *ent.Client, tenantID int) {
	t.Helper()
	_, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(context.Background())
	require.NoError(t, err)
}

type createWorkflowBody struct {
	Name     string                    `json:"name"`
	IsActive bool                      `json:"isActive"`
	Nodes    []map[string]interface{} `json:"nodes"`
}

// 这些测试都走真实的 ctx.ShouldBindJSON，所以节点必须带上 dto.ApprovalNodeRequest
// 标了 binding:"required" 的三个字段（approverType/approvalMode/rejectAction，
// dto/ticket_approval_dto.go:24-39）——漏掉任何一个都会在到达锁检查之前就被参数校验
// 挡掉，返回 400 而不是这里要测的 403/200。

func TestApprovalController_CreateWorkflow_LockedTenantReturns403(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	body, _ := json.Marshal(createWorkflowBody{
		Name:     "应该被拒绝",
		IsActive: true,
		Nodes: []map[string]interface{}{
			{"level": 1, "name": "审批", "approverType": "user", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any", "rejectAction": "end"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval-workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2003, resp.Code)
	assert.Contains(t, resp.Message, "已下线")
}

func TestApprovalController_DeleteWorkflow_LockedTenantReturns403(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)

	// 先在未锁定状态下创建一条。
	body, _ := json.Marshal(createWorkflowBody{
		Name:     "先创建后锁定",
		IsActive: true,
		Nodes: []map[string]interface{}{
			{"level": 1, "name": "审批", "approverType": "user", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any", "rejectAction": "end"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval-workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var createResp struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))

	lockControllerTestTenant(t, client, 1)

	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/approval-workflows/%d", createResp.Data.ID), nil)
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)

	require.Equal(t, http.StatusForbidden, delW.Code)
}

func TestApprovalController_ListWorkflows_UnaffectedByLock(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/approval-workflows", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "锁定不应该影响只读的 ListWorkflows")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./controller/... -run 'TestApprovalController_CreateWorkflow_LockedTenantReturns403|TestApprovalController_DeleteWorkflow_LockedTenantReturns403|TestApprovalController_ListWorkflows_UnaffectedByLock' -v`

Expected: `TestApprovalController_ListWorkflows_UnaffectedByLock` 应该已经 PASS（这条不需要新代码），另外两条 FAIL——当前 controller 对 `ErrLegacyApprovalWriteLocked` 没有特殊处理，会走到已有的 `common.Fail(ctx, common.InternalErrorCode, ...)` 分支，返回 500 不是 403。

- [ ] **Step 3: 实现错误映射**

在 `itsm-backend/controller/approval_controller.go` 顶部 import 块加 `"errors"`（跟 `"strconv"`/`"strings"` 放一起，按字母序排在 `"strconv"` 前面）。

在 `getIntFromContext` 函数之后、`ApprovalController` struct 定义之前加一个共享的错误映射辅助函数：

```go
// failApprovalWorkflowWrite 统一处理 ApprovalWorkflow 写入端点（Create/Update/Patch/
// Delete）的错误响应：锁定状态映射成明确的 403，其它错误保持原来的 500 通用处理，
// 不要在四个方法里各自重复一遍 errors.Is 判断。
func failApprovalWorkflowWrite(ctx *gin.Context, action string, err error) {
	if errors.Is(err, service.ErrLegacyApprovalWriteLocked) {
		common.Fail(ctx, common.ForbiddenCode, service.ErrLegacyApprovalWriteLocked.Error())
		return
	}
	common.Fail(ctx, common.InternalErrorCode, action+"失败: "+err.Error())
}
```

把 `CreateWorkflow`（`controller/approval_controller.go:79-83`）里的：

```go
	response, err := c.approvalService.CreateWorkflow(ctx.Request.Context(), &req, tid)
	if err != nil {
		common.Fail(ctx, common.InternalErrorCode, "创建工作流失败: "+err.Error())
		return
	}
```

改成：

```go
	response, err := c.approvalService.CreateWorkflow(ctx.Request.Context(), &req, tid)
	if err != nil {
		failApprovalWorkflowWrite(ctx, "创建工作流", err)
		return
	}
```

把 `UpdateWorkflow`（`controller/approval_controller.go:118-122`）里的：

```go
	response, err := c.approvalService.UpdateWorkflow(ctx.Request.Context(), id, &req, tid)
	if err != nil {
		common.Fail(ctx, common.InternalErrorCode, "更新工作流失败: "+err.Error())
		return
	}
```

改成：

```go
	response, err := c.approvalService.UpdateWorkflow(ctx.Request.Context(), id, &req, tid)
	if err != nil {
		failApprovalWorkflowWrite(ctx, "更新工作流", err)
		return
	}
```

把 `DeleteWorkflow`（`controller/approval_controller.go:150-154`）里的：

```go
	err = c.approvalService.DeleteWorkflow(ctx.Request.Context(), id, tid)
	if err != nil {
		common.Fail(ctx, common.InternalErrorCode, "删除工作流失败: "+err.Error())
		return
	}
```

改成：

```go
	err = c.approvalService.DeleteWorkflow(ctx.Request.Context(), id, tid)
	if err != nil {
		failApprovalWorkflowWrite(ctx, "删除工作流", err)
		return
	}
```

把 `PatchWorkflow`（`controller/approval_controller.go:273-277`）里的：

```go
	response, err := c.approvalService.UpdateWorkflow(ctx.Request.Context(), id, &req, tid)
	if err != nil {
		common.Fail(ctx, common.InternalErrorCode, "更新工作流失败: "+err.Error())
		return
	}
```

改成：

```go
	response, err := c.approvalService.UpdateWorkflow(ctx.Request.Context(), id, &req, tid)
	if err != nil {
		failApprovalWorkflowWrite(ctx, "更新工作流", err)
		return
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./controller/... -run 'TestApprovalController_CreateWorkflow_LockedTenantReturns403|TestApprovalController_DeleteWorkflow_LockedTenantReturns403|TestApprovalController_ListWorkflows_UnaffectedByLock' -v`

Expected: 全部 PASS。

- [ ] **Step 5: 补一条 `SubmitApproval` 不受影响的回归测试**

在同一个新测试文件 `approval_controller_write_lock_test.go` 里追加（需要在 `setupWriteLockControllerTest` 的路由注册里加一行 `r.POST("/api/v1/approval/submit", approvalController.SubmitApproval)`）：

```go
func TestApprovalController_SubmitApproval_UnaffectedByLock(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	body, _ := json.Marshal(map[string]interface{}{
		"ticketId":   1,
		"approvalId": 999, // 不存在的 ID——这里只验证请求不会因为"锁定"被拦在半路，
		                    // 会正常往后走到 approvalService.SubmitApproval 内部报
		                    // "找不到审批记录" 之类的业务错误，不是被 403 拦截。
		"action":     "approve",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval/submit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusForbidden, w.Code, "锁定不应该拦截 SubmitApproval")
}
```

同时把 `setupWriteLockControllerTest` 里加一行路由注册：

```go
	r.POST("/api/v1/approval/submit", approvalController.SubmitApproval)
```

Run: `cd itsm-backend && go test ./controller/... -run TestApprovalController_SubmitApproval_UnaffectedByLock -v`

Expected: PASS。

- [ ] **Step 6: 跑一遍 controller 包全部测试**

Run: `cd itsm-backend && go test ./controller/... -v 2>&1 | tail -100`

Expected: 全绿，包括既有的 `TestApprovalController_*`/`TestApprovalChainController_*`/`TestMigrateWorkflowToBPMN_*` 测试（这次改动不应该影响 `ApprovalChain` 或迁移工具端点，见 Global Constraints）。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/sr-ticket-unification
git add itsm-backend/controller/approval_controller.go itsm-backend/controller/approval_controller_write_lock_test.go
git commit -m "feat(approval): map legacy write-lock to 403 across all 4 write endpoints

CreateWorkflow/UpdateWorkflow/PatchWorkflow/DeleteWorkflow now return a
clear 403 (ForbiddenCode) with the lock's message when ErrLegacyApprovalWriteLocked
propagates from the service layer, instead of the generic 500. SubmitApproval
and ListWorkflows verified unaffected by the lock via regression tests."
```

---

### Task 3: 独立 CLI 工具，用来锁定/解锁租户

**Files:**
- Create: `itsm-backend/cmd/lock_legacy_approvals/main.go`

**Interfaces:**
- Consumes: `ent.Client`（通过 `database.InitDatabaseWithRLS`，跟 `cmd/migrate_legacy_approvals/main.go` 完全一样的初始化方式）；`ent/systemconfig` 包（跟 Task 1 用的是同一张表、同一个 key）。
- Produces: 无（standalone binary，不被别的 Go 代码 import）。

- [ ] **Step 1: 实现 CLI**

创建 `itsm-backend/cmd/lock_legacy_approvals/main.go`：

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/systemconfig"

	"go.uber.org/zap"
)

const legacyApprovalWriteLockedKey = "legacyApprovalWriteLocked"

func main() {
	tenantID := flag.Int("tenant-id", 0, "要锁定/解锁的租户ID（必填）")
	unlock := flag.Bool("unlock", false, "解锁而不是锁定（默认锁定）")
	flag.Parse()

	if *tenantID <= 0 {
		fmt.Fprintln(os.Stderr, "必须传 -tenant-id，且必须大于 0")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		sugar.Fatalw("connect database", "error", err)
	}
	defer client.Close()

	ctx := tenantctx.SystemContext(
		context.Background(),
		"ops:lock_legacy_approvals",
		"toggle the legacy ApprovalWorkflow config write lock for one tenant",
	)

	value := "true"
	action := "锁定"
	if *unlock {
		value = "false"
		action = "解锁"
	}

	existing, err := client.SystemConfig.Query().
		Where(
			systemconfig.KeyEQ(legacyApprovalWriteLockedKey),
			systemconfig.TenantIDEQ(*tenantID),
			systemconfig.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		sugar.Fatalw("query existing lock state", "tenant_id", *tenantID, "error", err)
	}

	if existing != nil {
		_, err = existing.Update().
			SetValue(value).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			sugar.Fatalw("update lock state", "tenant_id", *tenantID, "error", err)
		}
	} else {
		_, err = client.SystemConfig.Create().
			SetKey(legacyApprovalWriteLockedKey).
			SetValue(value).
			SetValueType("boolean").
			SetCategory("approval").
			SetDescription("旧审批工作流配置（ApprovalWorkflow CRUD）是否已下线只读——true 表示 Create/Update/Patch/Delete 一律拒绝").
			SetTenantID(*tenantID).
			SetCreatedAt(time.Now()).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			sugar.Fatalw("create lock state", "tenant_id", *tenantID, "error", err)
		}
	}

	sugar.Infow("完成", "tenant_id", *tenantID, "action", action, "locked", !*unlock)
}
```

- [ ] **Step 2: 编译确认**

Run: `cd itsm-backend && go build ./cmd/lock_legacy_approvals/...`

Expected: 编译成功，无报错。

- [ ] **Step 3: 用 `go vet` 检查一遍**

Run: `cd itsm-backend && go vet ./cmd/lock_legacy_approvals/...`

Expected: 无输出（无警告）。

- [ ] **Step 4: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/sr-ticket-unification
git add itsm-backend/cmd/lock_legacy_approvals/main.go
git commit -m "feat(approval): add standalone CLI to lock/unlock a tenant's legacy approval writes

Mirrors cmd/migrate_legacy_approvals's init pattern. Upserts the
legacyApprovalWriteLocked SystemConfig row for one tenant -- no new HTTP
endpoint, this is an operator-run tool for after a tenant's migration has
been confirmed, not a self-service toggle (see spec's non-goals on
rollout timing)."
```

---

### Task 4: 前端 `/admin/approvals` 页面按锁定状态禁用写操作

**Files:**
- Modify: `itsm-frontend/src/app/(main)/admin/approvals/page.tsx`
- Test: `itsm-frontend/src/app/(main)/admin/approvals/__tests__/write-lock.test.tsx`（新文件——独立于已有的 `page.test.tsx`，避免两个测试文件互相踩 mock 状态）

**Interfaces:**
- Consumes: `SystemConfigAPI.getConfigByKey(key: string): Promise<SystemConfig>`（`itsm-frontend/src/lib/api/system-config-api.ts:21`，已存在，直接用，不改这个文件）。`SystemConfig.value: string`（`itsm-frontend/src/lib/api/api-config.ts:324`）。

- [ ] **Step 1: 写失败的测试——锁定时"新建工作流"按钮应该被禁用，编辑/删除按钮不渲染**

创建 `itsm-frontend/src/app/(main)/admin/approvals/__tests__/write-lock.test.tsx`：

```tsx
import { render, screen, waitFor } from '@/lib/test-utils';
import ApprovalManagement from '../page';
import { httpClient } from '@/lib/api/http-client';
import { WorkflowDefinitionApi } from '@/lib/api/workflow-definition-api';
import { SystemConfigAPI } from '@/lib/api/system-config-api';

jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    patch: jest.fn(),
    delete: jest.fn(),
  },
}));

jest.mock('@/lib/api/workflow-definition-api', () => ({
  WorkflowDefinitionApi: {
    getWorkflows: jest.fn(),
  },
}));

jest.mock('@/lib/api/system-config-api', () => ({
  SystemConfigAPI: {
    getConfigByKey: jest.fn(),
  },
}));

const mockGet = httpClient.get as jest.Mock;
const mockGetWorkflows = WorkflowDefinitionApi.getWorkflows as jest.Mock;
const mockGetConfigByKey = SystemConfigAPI.getConfigByKey as jest.Mock;

const oneWorkflow = {
  items: [
    {
      id: 1,
      name: '存量工作流',
      isActive: true,
      nodes: [],
    },
  ],
  total: 1,
};

describe('ApprovalManagement 写锁定状态', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue(oneWorkflow);
    mockGetWorkflows.mockResolvedValue({ workflows: [] });
  });

  it('锁定时禁用新建按钮，不渲染编辑/删除按钮', async () => {
    mockGetConfigByKey.mockResolvedValue({ id: 1, key: 'legacyApprovalWriteLocked', value: 'true', category: 'approval', createdAt: '', updatedAt: '' });

    render(<ApprovalManagement />);

    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await waitFor(() => expect(mockGetConfigByKey).toHaveBeenCalledWith('legacyApprovalWriteLocked'));

    const createButton = await screen.findByRole('button', { name: /新建工作流/ });
    await waitFor(() => expect(createButton).toBeDisabled());

    // Edit/Trash2 都是纯 icon 按钮，没有 accessible name，query 不到具体某一个，
    // 所以直接断言这一行渲染出来的操作列里，除了状态标签之外没有任何可点击的图标按钮。
    const row = (await screen.findByText('存量工作流')).closest('tr');
    expect(row).not.toBeNull();
    const iconButtons = row!.querySelectorAll('button.ant-btn-icon-only, button.ant-btn-circle');
    expect(iconButtons.length).toBe(0);
  });

  it('未锁定（配置不存在，接口 404）时新建按钮保持可用——回归', async () => {
    mockGetConfigByKey.mockRejectedValue(new Error('HTTP error! status: 404'));

    render(<ApprovalManagement />);

    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await waitFor(() => expect(mockGetConfigByKey).toHaveBeenCalledWith('legacyApprovalWriteLocked'));

    const createButton = await screen.findByRole('button', { name: /新建工作流/ });
    expect(createButton).not.toBeDisabled();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-frontend && npx jest --testPathPattern "admin/approvals/__tests__/write-lock.test.tsx" --coverage=false --forceExit`

Expected: 两条都 FAIL——当前代码根本没调用 `SystemConfigAPI.getConfigByKey`，"新建工作流"按钮永远可点击，操作列永远渲染图标按钮。

- [ ] **Step 3: 实现前端锁定状态读取和门禁**

在 `itsm-frontend/src/app/(main)/admin/approvals/page.tsx` 顶部 import 块加：

```tsx
import { SystemConfigAPI } from '@/lib/api/system-config-api';
```

在 `ApprovalManagement` 组件内、`const [bpmnWorkflows, setBpmnWorkflows] = useState...`（大约第 77 行）之后加一个新的 state：

```tsx
  const [writeLocked, setWriteLocked] = useState(false);
```

在 `loadBpmnWorkflows`（大约第 103-115 行）之后加一个新的加载函数：

```tsx
  // 加载写锁定状态——找不到对应的 SystemConfig（通常是 404）视为未锁定，这是安全默认值，
  // 跟后端 ApprovalService.isLegacyApprovalWriteLocked 的默认行为保持一致。
  const loadWriteLockStatus = useCallback(async () => {
    try {
      const cfg = await SystemConfigAPI.getConfigByKey('legacyApprovalWriteLocked');
      setWriteLocked(cfg.value === 'true');
    } catch {
      setWriteLocked(false);
    }
  }, []);
```

在 `useEffect(() => { loadWorkflows(); loadBpmnWorkflows(); }, [loadWorkflows, loadBpmnWorkflows]);`（大约第 118-121 行）里加上新函数：

```tsx
  useEffect(() => {
    loadWorkflows();
    loadBpmnWorkflows();
    loadWriteLockStatus();
  }, [loadWorkflows, loadBpmnWorkflows, loadWriteLockStatus]);
```

把"新建工作流"按钮（大约第 341-351 行）：

```tsx
          <Button
            type="primary"
            icon={<Plus size={16} />}
            onClick={() => {
              setSelectedWorkflow(null);
              form.setFieldsValue({ isActive: true, nodes: [defaultApprovalNode()] });
              setShowModal(true);
            }}
          >
            新建工作流
          </Button>
```

改成加上 `disabled`：

```tsx
          <Button
            type="primary"
            icon={<Plus size={16} />}
            disabled={writeLocked}
            title={writeLocked ? '旧审批工作流系统已下线，请使用 BPMN 流程设计器' : undefined}
            onClick={() => {
              setSelectedWorkflow(null);
              form.setFieldsValue({ isActive: true, nodes: [defaultApprovalNode()] });
              setShowModal(true);
            }}
          >
            新建工作流
          </Button>
```

把操作列的 `render`（大约第 255-278 行）：

```tsx
      render: (_: unknown, record: ApprovalWorkflow) => (
        <Space size="small">
          <Button
            type="text"
            icon={<Edit size={16} />}
            onClick={() => handleEdit(record)}
          />
          <Button
            type="text"
            onClick={() => handleToggleStatus(record)}
          >
            {record.isActive ? '停用' : '启用'}
          </Button>
          <Popconfirm
            title="确认删除"
            description={`确定要删除工作流"${record.name}"吗？`}
            onConfirm={() => handleDelete(record.id)}
            okText="确认"
            cancelText="取消"
          >
            <Button type="text" danger icon={<Trash2 size={16} />} />
          </Popconfirm>
        </Space>
      ),
```

改成锁定时不渲染任何可写操作，只显示一个"只读"提示：

```tsx
      render: (_: unknown, record: ApprovalWorkflow) => (
        writeLocked ? (
          <Tag>只读</Tag>
        ) : (
          <Space size="small">
            <Button
              type="text"
              icon={<Edit size={16} />}
              onClick={() => handleEdit(record)}
            />
            <Button
              type="text"
              onClick={() => handleToggleStatus(record)}
            >
              {record.isActive ? '停用' : '启用'}
            </Button>
            <Popconfirm
              title="确认删除"
              description={`确定要删除工作流"${record.name}"吗？`}
              onConfirm={() => handleDelete(record.id)}
              okText="确认"
              cancelText="取消"
            >
              <Button type="text" danger icon={<Trash2 size={16} />} />
            </Popconfirm>
          </Space>
        )
      ),
```

注意 `columns` 是一个 `const columns: ColumnsType<ApprovalWorkflow> = [...]` 数组，定义在组件函数体内部（依赖 `writeLocked` 这个 state），所以它已经会在每次渲染时重新求值，不需要额外加 `useMemo` 依赖数组——这个文件里 `columns` 本来就是每次渲染重新创建的普通变量，不是 `useMemo`，保持现状即可。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-frontend && npx jest --testPathPattern "admin/approvals/__tests__/write-lock.test.tsx" --coverage=false --forceExit`

Expected: 两条都 PASS。

- [ ] **Step 5: 跑一遍这个目录下所有测试，确认没有破坏已有的 `page.test.tsx`**

Run: `cd itsm-frontend && npx jest --testPathPattern "admin/approvals/__tests__" --coverage=false --forceExit`

Expected: `page.test.tsx`（组件③遗留的字段名回归测试）和这次新加的 `write-lock.test.tsx` 都 PASS。`page.test.tsx` 里 mock 的 `httpClient`/`WorkflowDefinitionApi` 没有 mock `SystemConfigAPI`，所以 `loadWriteLockStatus` 在那个测试文件里会真的调用未 mock 的 `SystemConfigAPI.getConfigByKey`（进而调用未 mock 的 `httpClient.get`，而 `httpClient.get` 本身在那个文件里已经被 mock 成 `jest.fn()`，调用后返回 `undefined`，不会真的发网络请求，但会被 `loadWriteLockStatus` 的 `try/catch` 安全吞掉，`writeLocked` 保持 `false`）——如果这一步测试失败，说明这个假设不成立，需要回到 Step 3 检查 `loadWriteLockStatus` 的错误处理是否真的够宽松，不能让这个新增的调用意外破坏已有测试。

- [ ] **Step 6: `npm run type-check`**

Run: `cd itsm-frontend && npm run type-check`

Expected: 无报错。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/sr-ticket-unification
git add "itsm-frontend/src/app/(main)/admin/approvals/page.tsx" "itsm-frontend/src/app/(main)/admin/approvals/__tests__/write-lock.test.tsx"
git commit -m "feat(approval): gate admin/approvals write actions on backend lock status

Reads the legacyApprovalWriteLocked SystemConfig via the existing
SystemConfigAPI on mount. When locked: disable 新建工作流, replace the
actions column with a 只读 tag instead of edit/toggle/delete buttons.
Missing config (404) defaults to unlocked, matching the backend's default."
```

---

## 测试计划

- Task 1：`CreateWorkflow`/`UpdateWorkflow`/`DeleteWorkflow` 在锁定租户上全部返回 `ErrLegacyApprovalWriteLocked` 且不产生任何数据变更；未锁定租户完全不受影响（回归）；锁定是租户级的，不会连带锁住别的租户。
- Task 2：锁定状态下四个写入端点都返回 HTTP 403 + `common.ForbiddenCode`（2003）+ 包含"已下线"的错误文案；`ListWorkflows`/`SubmitApproval` 不受锁定状态影响（回归，强制项）。
- Task 3：CLI 能对不存在的 SystemConfig 行创建、对已存在的行更新，`-unlock` 能反向解锁；编译通过、`go vet` 无警告。
- Task 4：锁定时"新建工作流"按钮 disabled、操作列不渲染任何可写按钮；未锁定/配置缺失（404）时页面行为跟这次改动之前完全一致（回归，强制项，覆盖既有的 `page.test.tsx`）。

## 非目标（沿用 spec 的边界，这次计划不做）

- 不物理删除 `controller/approval_controller.go`、`service/approval_service.go`、`legacy_approval_migration_service.go` 等旧代码——只加写锁机制，删代码是更晚的、独立的、需要先观察一段时间没有异常之后再做的操作。
- 不做"自动判断某个租户是否已经全部迁移完成"——锁定与否是运维手动决定、手动执行 CLI 的操作，不是自动化的。
- 不新增给普通租户管理员自助切换这个开关的前端 UI/HTTP 写入端点——这是运维操作，不是自服务功能。
- `ApprovalChain`（`/admin/approval-chains`）和 Change/CAB 原生 SQL 审批依然不在范围内，这次改动完全不碰这两块代码。
