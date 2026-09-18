# 人员与汇报线实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让"上级"成为一条可信的线：能设置、能清空、不能自环或成环、跨租户 fail-closed；并让历史数据里的自引用与错上级被真实清掉。同时把员工归属从"分公司级"落到"最细的组"。

**Architecture:** 汇报线的**权威**是 `service/user_service.go` 的 `UserService`（`CreateUser` / `UpdateUser`），DTO 已用 `*int` 指针且语义正确（`nil`=不改、`0`=清空）——**不新建第二套用户更新实现**。校验放在 UserService 里，导入脚本复用同一个校验函数，避免两条路径两种口径。

**Tech Stack:** Go 1.22+ / Gin / Ent / `enttest` + sqlite3 单测 / PostgreSQL 17。

**Spec:**
- `docs/superpowers/specs/2026-09-18-people-reporting-line-design.md`（主要依据）
- `docs/superpowers/specs/2026-09-18-org-people-data-contract-design.md`（§2 两条独立的轴、§3.1 租户公理、§5 变更规则）
- `docs/superpowers/specs/2026-09-18-organization-model-design.md`（§6 硬性校验）
- 同批前置：`docs/superpowers/plans/2026-09-18-organization-foundation.md`（树/类型/编码唯一）、`docs/superpowers/plans/2026-09-18-department-management-unit.md`（部门负责人与人数）

## Global Constraints

- **目标库**：`itsm_migration_20260914`。**禁止**写 `itsm_config_baseline_20260908`（当前 Dev）。
- **两条轴不可混用**（契约 §2）：`users.manager_id` = **汇报线（个人级）**；`departments.manager_id` = **岗位负责人（组织级）**。本计划**只**改前者，绝不把两者互相回填。
- **上级暂缺是合法状态**：`manager_id = 0` 必须被接受；提单/审批不因上级为空而失败，走"组织的轴"向上找 + 兜底组（契约 §4）。
- **一人一上级**：不做多上级、不做代理上级。
- **租户**：所有查询与写入带 `tenant_id`；跨租户 fail-closed。
- **不碰入职/离职采集**：已决定由 KAF 处理，见 Task 6 的依赖记录。
- **提交粒度**：每个 Task 结束提交一次；message 用 `feat(organization):` / `docs(organization):` / `chore(organization):`。

## File Structure

| 文件 | 职责 | 动作 |
| --- | --- | --- |
| `itsm-backend/service/user_manager_line.go` | 上级写入校验（自引用 / 环 / 同租户 / 在职） | 新建 |
| `itsm-backend/service/user_manager_line_test.go` | 上述校验的回归测试 | 新建 |
| `itsm-backend/service/user_service.go` | 在 `UpdateUser` / `CreateUser` 里调用校验（权威点） | 修改 |
| `itsm-backend/cmd/sync_ehr_master_data/main.go` | 导入时跳过非法上级并计数，而不是整批失败 | 修改 |
| `itsm-backend/cmd/sync_ehr_master_data/manager_link_test.go` | 导入侧跳过/计数的回归测试 | 新建 |
| `docs/migrations/2026-09-18-reporting-line-inventory.md` | 上级与归属的只读盘点证据 | 新建 |
| `docs/migrations/2026-09-18-reporting-line-backfill.md` | 上级迁移执行证据 | 新建 |
| `docs/migrations/2026-09-18-employee-placement.md` | 归属落位执行证据 | 新建 |

---

### Task 1: 上级写入校验（自引用 / 成环 / 跨租户 / 非在职）

**Files:**
- Create: `itsm-backend/service/user_manager_line.go`
- Test: `itsm-backend/service/user_manager_line_test.go`
- Modify: `itsm-backend/service/user_service.go`（`UpdateUser` 的 `if req.ManagerID != nil` 分支，约 331–333 行；`CreateUser` 的对应分支）

**Interfaces:**
- Consumes: 既有 `s.client *ent.Client`
- Produces: `func validateUserManager(ctx context.Context, client *ent.Client, tenantID, userID, managerID int) error`

**实测缺陷**：`service/user_service.go` 的 `UpdateUser` 目前是

```go
if req.ManagerID != nil {
	update = update.SetManagerID(*req.ManagerID)
}
```

**零校验**——可以把自己设成自己的上级、可以把上级设成别的租户的人、可以造出 A→B→A。数据里已有 31 条自引用。

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func managerLineFixture(t *testing.T, dsn string) (*enttest.Client, context.Context, int, int, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	ctx := context.Background()

	mk := func(username string, tenantID int, active bool) int {
		u, err := client.User.Create().
			SetUsername(username).SetName(username).SetPassword("x").
			SetTenantID(tenantID).SetActive(active).Save(ctx)
		require.NoError(t, err)
		return u.ID
	}
	boss := mk("D20001", 1, true)
	staff := mk("D20002", 1, true)
	outsider := mk("D20003", 2, true)
	return client, ctx, boss, staff, outsider
}

func TestValidateUserManagerAllowsAnAbsentManager(t *testing.T) {
	client, ctx, _, staff, _ := managerLineFixture(t, "file:mgr_absent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.NoError(t, validateUserManager(ctx, client, 1, staff, 0), "上级暂缺是合法状态")
}

func TestValidateUserManagerRejectsSelfReference(t *testing.T) {
	client, ctx, _, staff, _ := managerLineFixture(t, "file:mgr_self?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.Error(t, validateUserManager(ctx, client, 1, staff, staff), "不能把自己设为上级")
}

func TestValidateUserManagerRejectsCrossTenantAndInactive(t *testing.T) {
	client, ctx, _, staff, outsider := managerLineFixture(t, "file:mgr_tenant?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.Error(t, validateUserManager(ctx, client, 1, staff, outsider), "跨租户上级必须 fail-closed")

	inactive, err := client.User.Create().SetUsername("D20004").SetName("离职").SetPassword("x").SetTenantID(1).SetActive(false).Save(ctx)
	require.NoError(t, err)
	require.Error(t, validateUserManager(ctx, client, 1, staff, inactive.ID), "非在职不能当上级")

	require.Error(t, validateUserManager(ctx, client, 1, staff, 999999), "不存在的上级必须 fail-closed")
}

func TestValidateUserManagerRejectsCycles(t *testing.T) {
	client, ctx, boss, staff, _ := managerLineFixture(t, "file:mgr_cycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// staff 的上级是 boss
	require.NoError(t, validateUserManager(ctx, client, 1, staff, boss))
	_, err := client.User.UpdateOneID(staff).SetManagerID(boss).Save(ctx)
	require.NoError(t, err)

	// 再把 boss 的上级设成 staff → 成环
	require.Error(t, validateUserManager(ctx, client, 1, boss, staff), "不能造出 A→B→A")
}

func TestValidateUserManagerToleratesAPreExistingCycleFurtherUp(t *testing.T) {
	client, ctx, boss, staff, _ := managerLineFixture(t, "file:mgr_dirty_cycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// 造一条既有脏环：boss 的上级是他自己（模拟历史数据）
	_, err := client.User.UpdateOneID(boss).SetManagerID(boss).Save(ctx)
	require.NoError(t, err)

	// 给 staff 设 boss 为上级时不应死循环，也不应因此报错——
	// 脏环本身由 Task 3 的数据步骤清理，这里只要求"不会跑飞"。
	require.NoError(t, validateUserManager(ctx, client, 1, staff, boss))
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/ -run TestValidateUserManager 2>&1 | tail -5
```

Expected: FAIL，`undefined: validateUserManager`。

- [ ] **Step 3: 实现校验**

```go
package service

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// validateUserManager 校验"某人的直属上级"是否可以设置。
//
// managerID == 0 表示"上级暂缺"，是合法状态（审批沿组织的轴往上找，最终兜底）。
// 走的是**汇报线**（users.manager_id），与 departments.manager_id 那条组织级的轴无关。
func validateUserManager(ctx context.Context, client *ent.Client, tenantID, userID, managerID int) error {
	if managerID == 0 {
		return nil
	}
	if managerID == userID {
		return fmt.Errorf("a user cannot be their own manager")
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(managerID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("manager %d is not a user in tenant %d", managerID, tenantID)
		}
		return err
	}
	if !manager.Active {
		return fmt.Errorf("manager %d is not active", managerID)
	}

	// 沿上级链上溯，命中 userID 即成环。
	// visited 保护：历史数据里可能已有脏环，不能让校验自己跑飞。
	visited := map[int]bool{userID: true}
	cursor := manager.ID
	for cursor != 0 {
		if cursor == userID {
			return fmt.Errorf("setting manager %d would create a reporting cycle", managerID)
		}
		if visited[cursor] {
			// 往上撞到一条既有脏环：停止上溯。脏环由数据步骤清理，
			// 这里不因为别人的脏数据挡住一次合法写入。
			return nil
		}
		visited[cursor] = true

		next, err := client.User.Query().
			Where(user.IDEQ(cursor), user.TenantIDEQ(tenantID)).
			Select(user.FieldManagerID).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil
			}
			return err
		}
		cursor = next.ManagerID
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/ -run TestValidateUserManager -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)"
```

Expected: 5 个测试全部 PASS。

- [ ] **Step 5: 接到权威点（UserService）**

`service/user_service.go` 里 `ManagerID` 的两个分支都改为先校验：

```go
	if req.ManagerID != nil {
		if err := validateUserManager(ctx, s.client, tenantID, id, *req.ManagerID); err != nil {
			return nil, err
		}
		update = update.SetManagerID(*req.ManagerID)
	}
```

`CreateUser` 里若有 `ManagerID` 写入，同样在校验后设置（新用户此时还没有 ID，自引用不可能成立，但仍需同租户/在职/环路校验）。

- [ ] **Step 6: 全量编译与相关测试**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./service/ ./handlers/common/ ./router/ 2>&1 | tail -6
```

Expected: 编译通过、测试 PASS。

- [ ] **Step 7: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/user_manager_line.go itsm-backend/service/user_manager_line_test.go itsm-backend/service/user_service.go
git commit -m "feat(organization): validate a reporting line before it can be set"
```

---

### Task 2: 导入侧跳过非法上级并计数（不整批失败）

**Files:**
- Modify: `itsm-backend/cmd/sync_ehr_master_data/main.go`（第二遍回填，`SetManagerID` 在约 453 行）
- Test: `itsm-backend/cmd/sync_ehr_master_data/manager_link_test.go`

**Interfaces:**
- Consumes: Task 1 的 `validateUserManager`（同 module，直接调用）
- Produces: 导入结束时打印 `skipped_manager_lines` 计数；非法上级被跳过，其余导入照常提交

> 为什么不是"直接失败"：数据里**已有 31 条自引用**。若导入遇到非法值就整批失败，导入将永远跑不完。正确行为是**跳过 + 计数 + 可见**，脏值留给 Task 3 的数据步骤清理。

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

// linkManagers 是第二遍回填的可测内核：给 (self, manager) 配对，返回被跳过的数量。
func TestLinkManagersSkipsSelfReferenceInsteadOfCreatingIt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:linkmgr_self?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	u, err := client.User.Create().SetUsername("D30001").SetName("甲").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
	require.NoError(t, err)

	// 上级指向自己：必须被跳过，且不能写库
	skipped, err := linkManagers(ctx, client, 1, map[string]managerLink{
		"D30001": {SelfID: u.ID, ManagerID: u.ID},
	})
	require.NoError(t, err)
	require.Equal(t, 1, skipped)

	reloaded, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ManagerID, "self-reference must not be persisted")
}

func TestLinkManagersLinksTheValidOnesAndCountsTheRest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:linkmgr_mixed?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	boss, err := client.User.Create().SetUsername("D30010").SetName("上司").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
	require.NoError(t, err)
	staff, err := client.User.Create().SetUsername("D30011").SetName("下属").SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
	require.NoError(t, err)

	skipped, err := linkManagers(ctx, client, 1, map[string]managerLink{
		"D30011": {SelfID: staff.ID, ManagerID: boss.ID},   // 合法
		"D30010": {SelfID: boss.ID, ManagerID: boss.ID},    // 自引用
		"D39999": {SelfID: 424242, ManagerID: boss.ID},     // 人不存在
	})
	require.NoError(t, err)
	require.Equal(t, 2, skipped)

	reloaded, err := client.User.Get(ctx, staff.ID)
	require.NoError(t, err)
	require.Equal(t, boss.ID, reloaded.ManagerID, "the valid link must be persisted")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./cmd/sync_ehr_master_data/ -run TestLinkManagers 2>&1 | tail -5
```

Expected: FAIL，`undefined: linkManagers` / `undefined: managerLink`。

- [ ] **Step 3: 抽出可测内核并接进导入**

在 `cmd/sync_ehr_master_data/` 下新增 `manager_link.go`：

```go
package main

import (
	"context"

	"itsm-backend/ent"
)

// managerLink 是"给 selfID 设 managerID 为上级"的一条待回填意图。
type managerLink struct {
	SelfID    int
	ManagerID int
}

// linkManagers 逐条校验并回填上级，返回被跳过的条数。
//
// 非法值（自引用 / 跨租户 / 非在职 / 不存在的上级）一律**跳过并计数**，
// 绝不让一次导入因为历史脏数据整批失败；脏值由数据步骤单独清理。
func linkManagers(ctx context.Context, client *ent.Client, tenantID int, links map[string]managerLink) (int, error) {
	skipped := 0
	for _, link := range links {
		if link.SelfID == 0 || link.ManagerID == 0 {
			continue
		}
		if err := validateUserManager(ctx, client, tenantID, link.SelfID, link.ManagerID); err != nil {
			skipped++
			continue
		}
		if err := client.User.UpdateOneID(link.SelfID).SetManagerID(link.ManagerID).Exec(ctx); err != nil {
			return skipped, err
		}
	}
	return skipped, nil
}
```

把 `main.go` 第二遍回填（约 453 行的 `SetManagerID(managerID)`）改为收集成 `map[string]managerLink` 后调用 `linkManagers`，并打印 `skipped_manager_lines`。

> `validateUserManager` 在 `package service`。若 `cmd` 包不能直接引用（无循环依赖则应可引用），把该校验函数下沉到 `service/approver` 或一个被双方共享的包；**不要复制一份实现**。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./cmd/sync_ehr_master_data/ -run TestLinkManagers -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)"
```

Expected: 2 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/cmd/sync_ehr_master_data
git commit -m "feat(organization): skip and count invalid reporting lines during import"
```

---

### Task 3: 上级与归属的只读盘点（迁移前）

**Files:**
- Create: `docs/migrations/2026-09-18-reporting-line-inventory.md`

**Interfaces:**
- Consumes: 无
- Produces: 四类处置的**实际条数**与样本，作为 Task 4/5 的执行依据

- [ ] **Step 1: 跑只读盘点**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT
  count(*) FILTER (WHERE manager_id = id)                                  AS self_reference,
  count(*) FILTER (WHERE manager_id <> 0 AND manager_id = id)              AS self_reference_nonzero,
  count(*) FILTER (WHERE manager_id = 0)                                   AS missing_manager,
  count(*) FILTER (WHERE manager_id <> 0 AND active IS NOT TRUE)           AS manager_not_active,
  count(*)                                                                 AS total_users
FROM users WHERE deleted_at IS NULL;"
```

- [ ] **Step 2: 盘员工归属落位差距**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT d.node_type, count(u.id) AS staff
FROM users u JOIN departments d ON d.id = u.department_id
WHERE u.deleted_at IS NULL
GROUP BY d.node_type ORDER BY staff DESC;"
```

Expected：绝大多数员工挂在 `company`/`branch`/空 `node_type` 上，而不是最细的 `team`。

- [ ] **Step 3: 写盘点证据并提交**

`docs/migrations/2026-09-18-reporting-line-inventory.md` 记录：执行时间、目标库、上述两组计数、`node_type` 分布。**只写计数与 `sha256:<前8位>`，不写姓名/工号/邮箱**。

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/migrations/2026-09-18-reporting-line-inventory.md
git commit -m "docs(organization): inventory the reporting lines and placements before migration"
```

---

### Task 4: 上级迁移执行（四类处置）

**Files:**
- Create: `docs/migrations/2026-09-18-reporting-line-backfill.md`

**Interfaces:**
- Consumes: Task 3 的盘点计数
- Produces: 目标库 `users.manager_id` 的四类处置完成，并有原子证据

> **执行前置**：本 Task 写目标库 `itsm_migration_20260914`，需维护者单独授权。**顺序不可颠倒**：先清自引用，再按旧 ITIL 覆盖，最后补空。

- [ ] **Step 1: 清自引用（必须先做）**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -v ON_ERROR_STOP=1 -P pager=off -c "
BEGIN;
UPDATE users SET manager_id = 0, updated_at = now() WHERE manager_id = id;
SELECT count(*) AS remaining_self_reference FROM users WHERE manager_id = id;
COMMIT;"
```

Expected：`remaining_self_reference = 0`。

- [ ] **Step 2: 以旧 ITIL 为准覆盖既有上级**

对 Task 3 盘点出的"已确认应覆盖"清单执行更新（清单以映射表形式落盘在证据文件里，逐条可追）。执行后回读校验：

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT count(*) AS still_missing FROM users WHERE deleted_at IS NULL AND active IS TRUE AND manager_id = 0;"
```

- [ ] **Step 3: 补空（只在旧 ITIL 有权威值时补）**

补空后必须复跑 Task 1 的校验口径，确认没有引入环：

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
WITH RECURSIVE chain AS (
  SELECT id, manager_id, id AS origin, 1 AS depth FROM users WHERE manager_id <> 0
  UNION ALL
  SELECT u.id, u.manager_id, c.origin, c.depth + 1
  FROM users u JOIN chain c ON u.id = c.manager_id
  WHERE c.depth < 50
)
SELECT count(*) AS cycle_hits FROM chain WHERE id = origin AND depth > 1;"
```

Expected：`cycle_hits = 0`。

- [ ] **Step 4: 写执行证据并提交**

`docs/migrations/2026-09-18-reporting-line-backfill.md` 记录：四类的处置动作与前后计数、环检测结果、以及**1261 条"待 HR 裁定"**的处置（留空或按裁定值），并注明这些人的上级为空**不影响提单**（走组织的轴 + 兜底）。

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/migrations/2026-09-18-reporting-line-backfill.md
git commit -m "chore(organization): backfill the reporting lines with cycle evidence"
```

---

### Task 5: 员工归属落位到最细的组

**Files:**
- Create: `docs/migrations/2026-09-18-employee-placement.md`

**Interfaces:**
- Consumes: `departments.code`（旧 ITIL 编码，已由计划 1 的 canonical 049 保证租户内唯一）
- Produces: 员工 `department_id` 从分公司级改挂到最细的组，且有映射与证据

> **执行前置**：写目标库，需维护者单独授权。**依赖**：`departments.node_type` 必须已赋值到 `team`（计划 1 只建了字段，赋值待业务分流表）——若类型尚未赋值，本 Task 无法可靠判断"最细的组"，**必须停下**。

- [ ] **Step 1: 确认类型已就位（不满足就停）**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT node_type, count(*) FROM departments WHERE deleted_at IS NULL GROUP BY node_type ORDER BY 2 DESC;"
```

Expected：存在非空 `team` 行，且 `''`（未分类）占比已知。**若 `team` 为 0，停止本 Task**，先完成类型赋值。

- [ ] **Step 2: 生成映射表并落盘**

映射规则：旧 ITIL 的员工所属组织编码 → 新库同编码的 `team` 节点。
把映射导出成 CSV 落到 `docs/migrations/` 之外的临时路径（含工号，**不进 git**），只把**计数**写进证据文件。

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT count(*) AS movable
FROM users u
JOIN departments to_dept ON to_dept.code = u.department_code_legacy
WHERE to_dept.node_type = 'team' AND to_dept.deleted_at IS NULL AND u.department_id <> to_dept.id;"
```

> 列名 `department_code_legacy` 以实际库为准；若不存在，则映射来源改为旧 ITIL 导出的组织编码（Task 3 的旧库连接）。**不要凭空假设列名**，先查当轮真实结构。

- [ ] **Step 3: 原子执行落位**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -v ON_ERROR_STOP=1 -P pager=off -c "
BEGIN;
UPDATE users u SET department_id = d.id, updated_at = now()
FROM departments d
WHERE d.code = u.department_code_legacy
  AND d.node_type = 'team' AND d.deleted_at IS NULL
  AND u.department_id <> d.id;
SELECT count(*) AS still_not_leaf FROM users u JOIN departments d ON d.id = u.department_id WHERE d.node_type <> 'team';
COMMIT;"
```

Expected：`still_not_leaf` 显著下降；剩余部分（无对应组、类型未赋值）记录在案，**不强行挂**。

- [ ] **Step 4: 写证据并提交**

`docs/migrations/2026-09-18-employee-placement.md` 记录：落位前后按 `node_type` 的分布、无法落位的计数与原因分类、回滚方式。

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/migrations/2026-09-18-employee-placement.md
git commit -m "chore(organization): place employees on their leaf group with evidence"
```

---

### Task 6: 记录 KAF 上游依赖（不实现）

**Files:**
- Modify: `docs/superpowers/specs/2026-09-18-people-reporting-line-design.md`

**Interfaces:**
- Consumes: 无
- Produces: 上游缺字段这件事被显式记录为**前置条件**，而不是被默默当成"已完成"

- [ ] **Step 1: 在设计文档的开放问题里补一条硬记录**

```markdown
### KAF 上游必须补充"直属上级"字段（前置条件，非本合同可解）

实测 KAF EHR 的人员表（`md_ehr_person`）**没有上级字段**，因此新 ITSM 无法从上游获得汇报线。
本设计已确定的自身能力（Task 1 的校验、Task 2 的导入跳过计数）**不能替代上游数据**。

- 影响：上级为空期间，审批走"组织的轴"向上找 + 兜底组（契约 §4），**不阻塞提单**。
- 前置动作：向 HR/KAF 提出字段补充需求；在此之前，汇报线由 ITSM 的手工/批量维护承担。
- 已决定的边界：入职/离职采集由 KAF 处理，本设计不实现。
```

- [ ] **Step 2: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/superpowers/specs/2026-09-18-people-reporting-line-design.md
git commit -m "docs(organization): record the KAF reporting-line field as a hard prerequisite"
```

---

## Self-Review

**Spec coverage**

| 《人员与汇报线》设计要求 | 落点 |
| --- | --- |
| 员工挂到最细的组 | Task 5 |
| 一人一上级 | 全程未引入多上级（Global Constraints） |
| 上级同租户、在职、非自己、不成环 | Task 1 |
| 上级允许为空、不阻塞提单 | Task 1（`managerID == 0` 合法）+ Task 4 Step 4 明示 |
| 迁移四类处置（614 / 404 / 31 / 1261） | Task 3 盘点 + Task 4 执行 |
| 归属变更留历史快照 | 契约 §5 已定义（在途单据用快照）；本计划只做数据落位，不改快照机制 |
| 防御性非自引用约束 | Task 1（服务层）+ Task 4 Step 1（数据层清存量） |
| KAF 缺上级字段的长期治理 | Task 6 |
| 不在范围：入职/离职采集 | Task 6 明示由 KAF 处理 |

**Placeholder scan：** 无 TBD/TODO。Task 5 Step 2 的"列名以实际库为准、先查真实结构"是**显式的先决检查**，不是占位符。

**Type consistency：** `validateUserManager` / `managerLink` / `linkManagers` 在定义与引用处同名同签名；`managerLink` 用 `map[string]managerLink`（key = username）与导入脚本既有的 `existingUsernameToID` 风格一致。

**已知缺口（明确不在本计划）**

1. `users.is_leader` 100% false：已核实**只有管理界面读取**，不参与审批解析，本轮不处理。
2. `users.function_line`（职能条线）与 `job_title` 的对齐：属数据口径问题，待 HR 输入。
3. 部门负责人（组织级的轴）：属计划 2/4，本计划不重复实现。
4. 前端人员管理页：本计划只做后端契约。
5. **不新增 canonical 迁移**：本计划无新列、无新表；若实现中确需新列，必须回到计划 1 的"三处同步"流程。
