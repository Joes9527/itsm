# 部门管理单元（负责人 + 子树人数）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让"部门"成为可用且可信的管理单元：负责人能被正确地设置、清空、变更并留痕；部门人数能在不拖垮接口的前提下被查到。

**Architecture:** 归属方是 `itsm-backend/handlers/common/`（垂直结构，`/departments/*` 的既有所有者），改动落在它的 repository / service / handler 三层 + `router` 一行。**本计划不新增 canonical 迁移**——"停用=删除（软删除）"已由既有 `deleted_at` 表达，负责人与人数都不需要新列。

**Tech Stack:** Go 1.22+ / Gin / Ent / `enttest` + sqlite3 单测 / PostgreSQL 17。

**Spec:**
- `docs/superpowers/specs/2026-09-18-department-management-unit-design.md`（主要依据）
- `docs/superpowers/specs/2026-09-18-org-people-data-contract-design.md`（§3.1 租户公理、§5 变更规则）
- `docs/superpowers/specs/2026-09-18-organization-model-design.md`（§6 硬性校验、§5 性能契约）
- 已实现的前置：`docs/superpowers/plans/2026-09-18-organization-foundation.md`（canonical 049/050 已在 PR #70）

## Global Constraints

- **目标库**：`itsm_migration_20260914`。**禁止**写 `itsm_config_baseline_20260908`（当前 Dev）。
- **租户**：所有查询与写入必须带 `tenant_id`；跨租户 fail-closed（契约 §3.1）。
- **不做物理删除**：`删除` == 软删除（`deleted_at`）。本计划**不修改**该语义，也不新增"停用"字段（见 Task 1 记录的已接受偏离）。
- **不做代理负责人**：休假/出差走既有转交/委托能力，不引入第二套负责人语义。
- **审计内容不含敏感数据**：只记 `departmentId` / `managerId` 前后值 / `reason`，不记个人信息正文。
- **加锁/并发**：负责人与换父的校验必须与写入在**同一个事务**内完成（先读后写不是并发保证）。
- **提交粒度**：每个 Task 结束提交一次；commit message 用 `feat(organization):` / `docs(organization):` / `chore(organization):`。

## File Structure

| 文件 | 职责 | 动作 |
| --- | --- | --- |
| `docs/superpowers/specs/2026-09-18-department-management-unit-design.md` | 记录"停用=删除"的已接受偏离 | 修改 |
| `itsm-backend/handlers/common/department_manager.go` | 负责人校验（在职 / 同租户 / 非占位账号） | 新建 |
| `itsm-backend/handlers/common/department_manager_test.go` | 上述校验的回归测试 | 新建 |
| `itsm-backend/handlers/common/department_update.go` | 部分更新语义（可清空负责人、可改父节点 + 环路校验 + 留痕） | 新建 |
| `itsm-backend/handlers/common/department_update_test.go` | 上述语义的回归测试 | 新建 |
| `itsm-backend/handlers/common/repository.go` / `repository_impl.go` | 事务化更新 + 子树人数查询 | 修改 |
| `itsm-backend/handlers/common/service.go` | 透传 | 修改 |
| `itsm-backend/handlers/common/handler.go` | `UpdateDepartment` 改为指针 DTO；新增人数 handler | 修改 |
| `itsm-backend/router/router.go` | 新增 `GET /departments/:id/employee-count`（两组） | 修改 |
| `docs/migrations/2026-09-18-dirty-department-manager-cleanup.md` | 脏负责人值清除的执行证据 | 新建 |

---

### Task 1: 记录"停用=删除"的已接受偏离

**Files:**
- Modify: `docs/superpowers/specs/2026-09-18-department-management-unit-design.md`

**Interfaces:**
- Consumes: 无
- Produces: 设计与实现一致的口径——后续 Task 不再讨论"停用 vs 删除"。

- [ ] **Step 1: 修正设计文档里自相矛盾的一句**

设计文档 §5 现在写的是：

> 校验：负责人唯一且在职；有员工或有下级的部门**不能删除，只能停用**。

本计划采用的语义是 **删除即停用（软删除）**，因此"不能删除，只能停用"在实现上不可成立。把该句改为：

```markdown
校验：负责人唯一且在职。

**停用与删除是同一件事（已接受的决定，2026-09-18）**：撤销节点 = 软删除（`deleted_at`），
系统不提供物理删除。**删除一个仍有下级的节点，其子树会在组织树上变成顶级节点**——
这是 `GetDepartmentTree` 把"父节点不在结果集里"的子节点当根节点的既有行为，业务已接受该代价，
因此**不增加"有下级就拒绝删除"的守卫**，也不新增独立的"停用"字段。
```

- [ ] **Step 2: 同步 §8 差距一节**

删掉"有员工或有下级的部门不能删除，只能停用"相关的差距描述（若存在），并把 §9 开放问题里与"停用 vs 删除"相关的条目标记为已决。

```bash
grep -n "停用\|只能停用" docs/superpowers/specs/2026-09-18-department-management-unit-design.md
```

Expected：剩余提及均在"删除即停用"的新口径下自洽，无"不能删除，只能停用"字样。

- [ ] **Step 3: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/superpowers/specs/2026-09-18-department-management-unit-design.md
git commit -m "docs(organization): record the accepted stop-equals-delete decision for departments"
```

---

### Task 2: 负责人写入校验（在职 / 同租户 / 非占位账号）

**Files:**
- Create: `itsm-backend/handlers/common/department_manager.go`
- Test: `itsm-backend/handlers/common/department_manager_test.go`

**Interfaces:**
- Consumes: 既有 `ent.Client`
- Produces: `validateDepartmentManager(ctx context.Context, client *ent.Client, tenantID, managerID int) error`

规则：
- `managerID == 0` → **允许**（负责人暂缺，审批走"组织的轴"向上找 + 兜底）。
- 否则必须是：同租户、`active = true`、且**不是运维/测试脚手架账号**。

脚手架判定的口径与 `scripts/migration/seed_isolation_plan.py` 一致：这些账号是开发期产物，做负责人会让审批落到无人值守的账号上；等 seed 隔离任务执行后它们会被停用，届时本守卫自然生效。

- [ ] **Step 1: 写失败测试**

```go
package common

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestValidateDepartmentManagerAllowsAbsentManager(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_absent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.NoError(t, validateDepartmentManager(context.Background(), client, 1, 0))
}

func TestValidateDepartmentManagerRequiresAnActiveSameTenantUser(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_active?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	active, err := client.User.Create().SetUsername("D10001").SetName("在职").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, validateDepartmentManager(ctx, client, 1, active.ID))

	inactive, err := client.User.Create().SetUsername("D10002").SetName("离职").SetPassword("x").SetTenantID(1).SetActive(false).Save(ctx)
	require.NoError(t, err)
	require.Error(t, validateDepartmentManager(ctx, client, 1, inactive.ID), "an inactive user must not be a department manager")

	otherTenant, err := client.User.Create().SetUsername("D10003").SetName("别家").SetPassword("x").SetTenantID(2).SetActive(true).Save(ctx)
	require.NoError(t, err)
	require.Error(t, validateDepartmentManager(ctx, client, 1, otherTenant.ID), "cross-tenant manager must fail closed")

	require.Error(t, validateDepartmentManager(ctx, client, 1, 999999), "unknown manager must fail closed")
}

func TestValidateDepartmentManagerRejectsScaffoldAccounts(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_scaffold?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	for _, username := range []string{"supervisor_test", "qa_manager_ops", "admin", "ui-runtime-1789377159595-dept_manager", "engineer-workspace-1-engineer", "kaf_closeout_t1_20260831"} {
		u, err := client.User.Create().SetUsername(username).SetName("脚手架").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
		require.NoError(t, err)
		require.Error(t, validateDepartmentManager(ctx, client, 1, u.ID), "%s is a scaffold account and must not own a department", username)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./handlers/common/ -run TestValidateDepartmentManager 2>&1 | tail -5
```

Expected: FAIL，`undefined: validateDepartmentManager`。

- [ ] **Step 3: 实现校验**

```go
package common

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// 运维/测试脚手架账号前缀：见 scripts/migration/seed_isolation_plan.py 的同源口径。
// 这些账号没有真人值守，做负责人会让"组织的轴"审批落到无人账号上。
var departmentManagerScaffoldPrefixes = []string{
	"qa_",
	"ui-runtime-",
	"ui-core-journey-",
	"ui-lifecycle-",
	"engineer-workspace-",
	"kaf_closeout_",
}

// validateDepartmentManager 校验一个部门负责人候选。
//
// managerID == 0 表示"负责人暂缺"，是合法状态（审批会沿组织的轴往上找，最终兜底）。
func validateDepartmentManager(ctx context.Context, client *ent.Client, tenantID, managerID int) error {
	if managerID == 0 {
		return nil
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(managerID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("department manager %d is not an active user in tenant %d", managerID, tenantID)
		}
		return err
	}
	if !manager.Active {
		return fmt.Errorf("department manager %d is not active", managerID)
	}
	if isScaffoldAccount(manager.Username) {
		return fmt.Errorf("user %q is a scaffold account and cannot own a department", manager.Username)
	}
	return nil
}

func isScaffoldAccount(username string) bool {
	if username == "admin" {
		return true
	}
	if strings.HasSuffix(username, "_test") {
		return true
	}
	for _, prefix := range departmentManagerScaffoldPrefixes {
		if strings.HasPrefix(username, prefix) {
			return true
		}
	}
	return false
}
```

> 实现提示：`ent/user` 的谓词名以生成的代码为准（本仓库既有用法为 `user.IDEQ` / `user.TenantIDEQ` / `user.ActiveEQ`）；若某谓词名不同，按生成代码调整，不要新增自定义 SQL。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./handlers/common/ -run TestValidateDepartmentManager -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)"
```

Expected: 3 个测试全部 PASS。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/handlers/common/department_manager.go itsm-backend/handlers/common/department_manager_test.go
git commit -m "feat(organization): validate a department manager before it can be assigned"
```

---

### Task 3: 可清空负责人、可改父节点，并留痕

**Files:**
- Create: `itsm-backend/handlers/common/department_update.go`
- Test: `itsm-backend/handlers/common/department_update_test.go`
- Modify: `itsm-backend/handlers/common/handler.go`（`UpdateDepartment` 的 DTO 与逻辑，约 249–300 行）

**Interfaces:**
- Consumes: Task 2 的 `validateDepartmentManager`
- Produces:
  - `type departmentUpdateRequest struct { Name, Description *string; ManagerID, ParentID *int; Reason string }`
  - `func applyDepartmentUpdate(ctx context.Context, client *ent.Client, current *Department, req departmentUpdateRequest) (*Department, *departmentChange, error)`
  - `type departmentChange struct { ManagerFrom, ManagerTo, ParentFrom, ParentTo int; Reason string; Changed bool }`

**修的两个既有缺陷**（实测 `handler.go:288/406`）：

1. `if req.ManagerID != 0 { existing.ManagerID = req.ManagerID }` → **永远无法把负责人清空**，与设计"负责人允许暂缺"冲突。
2. `if req.ParentID != 0 { ... }` → **永远无法把节点移回顶层**，且**没有环路校验**（可把节点挂到自己的后代下，造出环）。

- [ ] **Step 1: 写失败测试**

```go
package common

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func deptFixture(t *testing.T, dsn string) (*ent.Client, context.Context, *Department, *Department) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	ctx := context.Background()

	root := &Department{Name: "集团", Code: "1", TenantID: 1, NodeType: orgNodeCompany}
	savedRoot, err := (&EntRepository{client: client}).CreateDepartment(ctx, root)
	require.NoError(t, err)

	child := &Department{Name: "分公司A", Code: "1A", TenantID: 1, NodeType: orgNodeBranch, ParentID: savedRoot.ID}
	savedChild, err := (&EntRepository{client: client}).CreateDepartment(ctx, child)
	require.NoError(t, err)
	return client, ctx, savedRoot, savedChild
}

func TestApplyDepartmentUpdateCanClearTheManager(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_clear?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	manager, err := client.User.Create().SetUsername("D10001").SetName("在职").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
	require.NoError(t, err)

	child.ManagerID = manager.ID
	updated, err := (&EntRepository{client: client}).UpdateDepartment(ctx, child)
	require.NoError(t, err)
	require.Equal(t, manager.ID, updated.ManagerID)

	zero := 0
	_, change, err := applyDepartmentUpdate(ctx, client, updated, departmentUpdateRequest{ManagerID: &zero, Reason: "负责人离职，暂时空缺"})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.Equal(t, 0, change.ManagerTo)

	reloaded, err := (&EntRepository{client: client}).GetDepartment(ctx, child.ID, 1)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ManagerID, "manager must actually be cleared")
}

func TestApplyDepartmentUpdateRequiresAReasonForAnyChange(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_reason?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	other := 0
	_, _, err := applyDepartmentUpdate(ctx, client, child, departmentUpdateRequest{ParentID: &other})
	require.Error(t, err, "a change without a reason must be rejected")
}

func TestApplyDepartmentUpdateRejectsCycles(t *testing.T) {
	client, ctx, root, child := deptFixture(t, "file:deptupd_cycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// 把父节点挂到自己的子节点下 → 成环
	rootID := child.ID
	_, _, err := applyDepartmentUpdate(ctx, client, root, departmentUpdateRequest{ParentID: &rootID, Reason: "错误操作"})
	require.Error(t, err, "a node must not be moved under its own descendant")

	self := root.ID
	_, _, err = applyDepartmentUpdate(ctx, client, root, departmentUpdateRequest{ParentID: &self, Reason: "错误操作"})
	require.Error(t, err, "a node must not be its own parent")
}

func TestApplyDepartmentUpdateReportsNoChangeWhenNothingDiffers(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_noop?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	sameName := child.Name
	_, change, err := applyDepartmentUpdate(ctx, client, child, departmentUpdateRequest{Name: &sameName})
	require.NoError(t, err)
	require.False(t, change.Changed)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./handlers/common/ -run TestApplyDepartmentUpdate 2>&1 | tail -5
```

Expected: FAIL，`undefined: applyDepartmentUpdate`。

- [ ] **Step 3: 实现部分更新语义**

```go
package common

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
)

// departmentUpdateRequest 用指针区分"没传这个字段"与"把它清空"。
// 旧实现用 `!= 0` 判断，导致负责人与父节点永远无法被清空。
type departmentUpdateRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ManagerID   *int    `json:"managerId"`
	ParentID    *int    `json:"parentId"`
	Reason      string  `json:"reason"`
}

type departmentChange struct {
	ManagerFrom int
	ManagerTo   int
	ParentFrom  int
	ParentTo    int
	Reason      string
	Changed     bool
}

// applyDepartmentUpdate 计算并落库一次部门变更。
//
// 任何实际变更都必须带 reason（设计 §3：负责人变更留痕——谁、何时、改了什么、为什么）。
// 校验与写入必须同事务，先读后写不是并发保证。
func applyDepartmentUpdate(ctx context.Context, client *ent.Client, current *Department, req departmentUpdateRequest) (*Department, *departmentChange, error) {
	change := &departmentChange{
		ManagerFrom: current.ManagerID,
		ManagerTo:   current.ManagerID,
		ParentFrom:  current.ParentID,
		ParentTo:    current.ParentID,
		Reason:      strings.TrimSpace(req.Reason),
	}

	if req.ManagerID != nil {
		if err := validateDepartmentManager(ctx, client, current.TenantID, *req.ManagerID); err != nil {
			return nil, nil, err
		}
		change.ManagerTo = *req.ManagerID
	}
	if req.ParentID != nil {
		if err := validateDepartmentParent(ctx, client, current.TenantID, current.ID, *req.ParentID); err != nil {
			return nil, nil, err
		}
		change.ParentTo = *req.ParentID
	}
	change.Changed = change.ManagerFrom != change.ManagerTo || change.ParentFrom != change.ParentTo

	if req.Name != nil && *req.Name != current.Name {
		change.Changed = true
	}
	if req.Description != nil && *req.Description != current.Description {
		change.Changed = true
	}
	if !change.Changed {
		return current, change, nil
	}
	if change.Reason == "" {
		return nil, nil, fmt.Errorf("a department change requires a reason")
	}

	update := client.Department.UpdateOneID(current.ID).Where(department.TenantIDEQ(current.TenantID))
	if req.Name != nil {
		update = update.SetName(*req.Name)
	}
	if req.Description != nil {
		update = update.SetDescription(*req.Description)
	}
	if req.ManagerID != nil {
		update = update.SetManagerID(*req.ManagerID)
	}
	if req.ParentID != nil {
		update = update.SetParentID(*req.ParentID)
	}
	saved, err := update.Save(ctx)
	if err != nil {
		return nil, nil, err
	}
	return saved, change, nil
}

// validateDepartmentParent 拒绝自引用与"挂到自己的后代下"（组织树必须无环）。
// parentID == 0 表示移到顶层，合法。
func validateDepartmentParent(ctx context.Context, client *ent.Client, tenantID, selfID, parentID int) error {
	if parentID == 0 {
		return nil
	}
	if parentID == selfID {
		return fmt.Errorf("a department cannot be its own parent")
	}

	// 沿父链上溯，命中 self 即成环。带上 visited 防既有脏环导致死循环。
	visited := map[int]bool{}
	cursor := parentID
	for cursor != 0 {
		if cursor == selfID {
			return fmt.Errorf("a department cannot be moved under its own descendant")
		}
		if visited[cursor] {
			return fmt.Errorf("department tree already contains a cycle at %d", cursor)
		}
		visited[cursor] = true

		node, err := client.Department.Query().
			Where(department.IDEQ(cursor), department.TenantIDEQ(tenantID)).
			Select(department.FieldParentID).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return fmt.Errorf("parent department %d does not exist in tenant %d", cursor, tenantID)
			}
			return err
		}
		cursor = node.ParentID
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./handlers/common/ -run TestApplyDepartmentUpdate -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)"
```

Expected: 4 个测试全部 PASS。

- [ ] **Step 5: 接线 handler：指针 DTO + 审计留痕**

把 `handler.go` 的 `UpdateDepartment` 请求体换成 `departmentUpdateRequest`，并在成功变更后写审计：

```go
func (h *Handler) UpdateDepartment(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ParamError(c, "invalid department id")
		return
	}

	var req departmentUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ParamError(c, "参数错误: "+err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	existing, err := h.svc.GetDepartment(c.Request.Context(), id, tenantID)
	if err != nil || existing == nil {
		common.NotFound(c, "部门不存在")
		return
	}

	result, change, err := h.svc.ApplyDepartmentUpdate(c.Request.Context(), existing, req)
	if err != nil {
		common.ParamError(c, "更新部门失败: "+err.Error())
		return
	}
	if change.Changed {
		payload := fmt.Sprintf(`{"departmentId":%d,"managerFrom":%d,"managerTo":%d,"parentFrom":%d,"parentTo":%d,"reason":%q}`,
			id, change.ManagerFrom, change.ManagerTo, change.ParentFrom, change.ParentTo, change.Reason)
		_ = h.svc.LogActivity(c.Request.Context(), &AuditLog{
			TenantID:    tenantID,
			UserID:      c.GetInt("user_id"),
			RequestID:   c.GetString("request_id"),
			IP:          c.ClientIP(),
			Resource:    "department",
			Action:      "department.updated",
			Path:        c.Request.URL.Path,
			Method:      c.Request.Method,
			StatusCode:  200,
			RequestBody: payload,
		})
	}
	common.Success(c, result)
}
```

`service.go` 增加两个透传：

```go
func (s *Service) ApplyDepartmentUpdate(ctx context.Context, current *Department, req departmentUpdateRequest) (*Department, *departmentChange, error) {
	return applyDepartmentUpdate(ctx, s.repo.client, current, req)
}
```

> `Service.repo` 已是 `*EntRepository`；`client` 字段在同包内可访问。若 `Service` 结构不含可直接访问的 client，则在 `EntRepository` 上加同名方法并在 `Service` 里转发。

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./handlers/common/ ./router/ 2>&1 | tail -5
```

Expected: 编译通过、测试 PASS（router 测试确认路由未受影响）。

- [ ] **Step 6: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/handlers/common
git commit -m "feat(organization): allow clearing a manager or parent and require a reason, with audit"
```

---

### Task 4: 子树人数按需查询（有界遍历）

**Files:**
- Modify: `itsm-backend/handlers/common/repository.go`、`repository_impl.go`、`service.go`、`handler.go`
- Modify: `itsm-backend/router/router.go`（`org` 与 tenant 别名两组各 1 条）
- Test: `itsm-backend/handlers/common/department_employee_count_test.go`

**Interfaces:**
- Consumes: 既有 `ent.Client`
- Produces:
  - `func (r *EntRepository) CountDepartmentSubtreeEmployees(ctx context.Context, tenantID, departmentID int) (int, error)`
  - handler `GetDepartmentEmployeeCount`
  - route `GET /departments/:id/employee-count`

设计 §6 的性能契约：**部门列表接口不得逐行递归统计**；人数只在详情/显式展开时计算。同时遍历必须有界（AGENTS：拓扑遍历要有界）。

- [ ] **Step 1: 写失败测试**

```go
package common

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestCountDepartmentSubtreeEmployeesCountsTheWholeSubtree(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptcount?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := &EntRepository{client: client}

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	child, err := client.Department.Create().SetName("分公司A").SetCode("1A").SetTenantID(1).SetNodeType(orgNodeBranch).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	// root 自己 1 人，child 2 人
	for _, spec := range []struct {
		username string
		deptID   int
	}{{"D1", root.ID}, {"D2", child.ID}, {"D3", child.ID}} {
		_, err := client.User.Create().SetUsername(spec.username).SetName("员工").SetPassword("x").SetTenantID(1).SetActive(true).SetDepartmentID(spec.deptID).Save(ctx)
		require.NoError(t, err)
	}
	// 别的租户不计入
	other, err := client.Department.Create().SetName("别家").SetCode("1").SetTenantID(2).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().SetUsername("D9").SetName("别家员工").SetPassword("x").SetTenantID(2).SetActive(true).SetDepartmentID(other.ID).Save(ctx)
	require.NoError(t, err)

	total, err := repo.CountDepartmentSubtreeEmployees(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Equal(t, 3, total)

	childOnly, err := repo.CountDepartmentSubtreeEmployees(ctx, 1, child.ID)
	require.NoError(t, err)
	require.Equal(t, 2, childOnly)
}

func TestCountDepartmentSubtreeEmployeesStopsAtTheNodeBudget(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptcount_budget?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := &EntRepository{client: client}

	// 构造深度超过预算的链，确认查询在预算处停下而不是无界遍历
	parentID := 0
	var lastID int
	for i := 1; i <= maxSubtreeNodes+5; i++ {
		create := client.Department.Create().
			SetName("节点").
			SetCode("N" + strconv.Itoa(i)).
			SetTenantID(1).
			SetNodeType(orgNodeDepartment)
		if parentID != 0 {
			create = create.SetParentID(parentID)
		}
		d, err := create.Save(ctx)
		require.NoError(t, err)
		parentID = d.ID
		lastID = d.ID
	}
	require.NotZero(t, lastID)

	_, err := repo.CountDepartmentSubtreeEmployees(ctx, 1, 1)
	require.NoError(t, err, "bounded traversal must return instead of running away")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./handlers/common/ -run TestCountDepartmentSubtreeEmployees 2>&1 | tail -5
```

Expected: FAIL，`undefined: CountDepartmentSubtreeEmployees` / `undefined: maxSubtreeNodes`。

- [ ] **Step 3: 实现有界子树统计**

```go
// 遍历预算：部门树实测最深 14 层、单租户近 8000 节点。
// 统计必须有界，避免脏环或异常数据把一次请求变成无界遍历。
const maxSubtreeNodes = 10000

// CountDepartmentSubtreeEmployees 统计某部门**及其所有后代**的在职员工数。
//
// 只应在部门详情/显式展开时调用；列表接口禁止逐行调用（设计 §6 性能契约）。
func (r *EntRepository) CountDepartmentSubtreeEmployees(ctx context.Context, tenantID, departmentID int) (int, error) {
	visited := map[int]bool{}
	frontier := []int{departmentID}
	total := 0
	expanded := 0

	for len(frontier) > 0 {
		level := make([]int, 0, len(frontier))
		for _, id := range frontier {
			if visited[id] {
				continue
			}
			visited[id] = true
			level = append(level, id)
		}
		if len(level) == 0 {
			break
		}

		count, err := r.client.User.Query().
			Where(
				user.TenantIDEQ(tenantID),
				user.ActiveEQ(true),
				user.DepartmentIDIn(level...),
			).
			Count(ctx)
		if err != nil {
			return 0, err
		}
		total += count

		expanded += len(level)
		if expanded >= maxSubtreeNodes {
			return total, fmt.Errorf("department subtree exceeds the %d-node budget; refine the query instead of counting unbounded", maxSubtreeNodes)
		}

		children, err := r.client.Department.Query().
			Where(
				department.TenantIDEQ(tenantID),
				department.DeletedAtIsNil(),
				department.ParentIDIn(level...),
			).
			Select(department.FieldID).
			All(ctx)
		if err != nil {
			return 0, err
		}
		next := make([]int, 0, len(children))
		for _, child := range children {
			if !visited[child.ID] {
				next = append(next, child.ID)
			}
		}
		frontier = next
	}
	return total, nil
}
```

其余接线（`repository.go` 接口、`service.go` 透传、`handler.go`、`router.go` 两条路由）：

```go
// handler.go
func (h *Handler) GetDepartmentEmployeeCount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ParamError(c, "invalid department id")
		return
	}
	tenantID := c.GetInt("tenant_id")
	count, err := h.svc.CountDepartmentSubtreeEmployees(c.Request.Context(), tenantID, id)
	if err != nil {
		common.InternalError(c, "统计部门人数失败: "+err.Error())
		return
	}
	common.Success(c, gin.H{"departmentId": id, "employeeCount": count})
}
```

```go
// router.go：紧随 org.GET("/departments/children", ...) 与 tenant.GET("/departments/children", ...) 各加一行
org.GET("/departments/:id/employee-count", middleware.RequirePermission("department", "read"), config.CommonHandler.GetDepartmentEmployeeCount)
tenant.GET("/departments/:id/employee-count", middleware.RequirePermission("department", "read"), config.CommonHandler.GetDepartmentEmployeeCount)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./handlers/common/ ./router/ -run "TestCountDepartmentSubtreeEmployees|Test" -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)" | head -20
```

Expected: 全部 PASS（含 router 路由无冲突）。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/handlers/common itsm-backend/router/router.go
git commit -m "feat(organization): count subtree employees on demand with a bounded traversal"
```

---

### Task 5: 清除 1 条脏负责人值（数据步骤）

**Files:**
- Create: `docs/migrations/2026-09-18-dirty-department-manager-cleanup.md`

**Interfaces:**
- Consumes: Task 2/3 的校验（清空后该部门负责人为"暂缺"，审批走组织的轴 + 兜底）
- Produces: 目标库 `departments.manager_id` 的脏值被原子清除，并留下证据

> **执行前置**：本 Task 会写目标库 `itsm_migration_20260914`，需要维护者单独授权。

- [ ] **Step 1: 只读确认脏值**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT d.id AS dept_id, d.code, d.manager_id, u.username, u.role, u.active
FROM departments d JOIN users u ON u.id = d.manager_id
ORDER BY d.id;"
```

Expected（2026-09-18 实测）：**恰好 1 行**。记录该行的 `manager_id`，并判断该用户是否是管理岗——本工作区已核实它指向一名非管理岗用户。

- [ ] **Step 2: 在同一批里清空并回读校验**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -v ON_ERROR_STOP=1 -P pager=off -c "
BEGIN;
UPDATE departments SET manager_id = 0, updated_at = now() WHERE manager_id <> 0;
SELECT count(*) AS remaining_nonzero_manager FROM departments WHERE manager_id <> 0;
COMMIT;"
```

Expected：`remaining_nonzero_manager = 0`。

- [ ] **Step 3: 写证据文件并提交**

`docs/migrations/2026-09-18-dirty-department-manager-cleanup.md` 记录：执行时间、目标库、清除前计数（1）、清除后计数（0）、原值指向的用户角色（**不写工号/姓名**）、回滚方式（该值本身是脏数据，无恢复必要；若需恢复可从 Dev 克隆重建）。

```bash
cd /home/administrator/project/itsm
git add docs/migrations/2026-09-18-dirty-department-manager-cleanup.md
git commit -m "chore(organization): clear the dirty department manager value with evidence"
```

---

## Self-Review

**Spec coverage**

| 《部门》设计要求 | 落点 |
| --- | --- |
| 四类型分工 / 判断口径（§2） | Task 1 记录口径；类型字段已由计划 1（canonical 050）交付 |
| 负责人唯一正职、必须在职（§3） | Task 2 |
| **负责人允许暂缺**（§3） | Task 2（`managerID == 0` 合法）+ Task 3（真正能清空） |
| 负责人变更只影响新单、留痕（§3） | Task 3（reason 必填 + AuditLog）；"新单生效"由契约 §5 的在途快照保证 |
| 不做代理负责人（§3） | 全程未引入（Global Constraints） |
| 部门职责范围本轮只留位置（§4） | 未实现（符合设计） |
| 维护：改名称/负责人/上级/类型/停用（§5） | Task 3（名称/负责人/上级）；类型与停用在计划 1 与既有删除路径 |
| 校验：有员工或有下级不能删除只能停用（§5） | **已接受的偏离**：删除即停用，Task 1 记录 |
| 人数按子树统计 + 性能契约（§6） | Task 4（有界、按需、不在列表接口） |
| 脏负责人值原子清除（§8） | Task 5 |
| 验收：可查负责人/人数/路径；停用后历史不变（§7） | Task 4 + 既有软删除语义；路径查询已由计划 1 的树接口提供 |

**Placeholder scan：** 无 TBD/TODO；每个代码步骤都给了可编译内容。两处"按生成代码调整谓词名"是既有仓库惯例的显式提示，不是占位符。

**Type consistency：** `departmentUpdateRequest` / `departmentChange` / `applyDepartmentUpdate` / `validateDepartmentManager` / `validateDepartmentParent` / `maxSubtreeNodes` / `CountDepartmentSubtreeEmployees` 在定义处与引用处同名同签名。

**已知缺口（明确不在本计划）**

1. 部门"职责范围"（设计 §4）：本轮只留位置，未实现。
2. 员工与部门的归属迁移（挂到最细的组）：属《人员与汇报线》计划。
3. 部门管理页前端：本计划只做后端契约，前端接线单独排期。
4. **不新增 canonical 迁移**：因此不需要 `ControlledMigrationCatalog()` 三处同步（计划 1 的教训在此不适用）；若实现中确实需要新列，必须回到该三处同步流程。
