# 组织基础（迁移前置 + 组织树）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让组织树成为可信、可安全重跑的数据基础：克隆来源可证、结构一致、seed 数据隔离、编码唯一、节点自报类型、大树接口不再全量下发。

**Architecture:** 结构变更走 canonical migration（`itsm-backend/migration/migrations.go` 注册 + `itsm-backend/migrations/*.sql`），不覆盖历史 SQL、不用 Ent overlay。组织树接口的归属方是 `itsm-backend/handlers/common/`（垂直结构），编码与节点类型的权威字段在 Ent schema。数据导入脚本改为按 `(tenant_id, code)` 幂等 upsert，禁止无条件 Create。

**Tech Stack:** Go 1.22+ / Gin / Ent（`go generate ./ent`，feature `sql/upsert,sql/execquery`）/ PostgreSQL 17 / `enttest` + sqlite3 单测 / `python3 -m scripts.migration`（只读核验）。

**Spec:**
- `docs/superpowers/specs/2026-09-18-organization-model-design.md`（本计划的主要依据）
- `docs/superpowers/specs/2026-09-18-org-people-data-contract-design.md`（§3.1 租户公理、§4.1 快照、§7 落地前提）
- `docs/review/2026-09-18-legacy-ehr-new-itsm-data-reconciliation.md`（seed/测试数据盘点在第 2 节）

## Global Constraints

- **目标库**：`itsm_migration_20260914`（同实例 `itsm-postgres-dev`，用户 `itsm_user`）。所有写入只允许发生在该库；**禁止**写 `itsm_config_baseline_20260908`（当前 Dev）。
- **旧源口径**：生产 `keas-itsm.gazellio.com`（2026-09-18 只读抽取）。基于旧 **test** 环境的证据与 profile 不作为依据。
- **不迁移**：旧 ticket、历史审批、评论、附件、流程实例、旧 BPMN。
- **结构目标**：canonical migration 链；当前链最高为 `048_cti_governance`（定义在 `itsm-backend/migration/cti_governance.go`），本计划新增 `049` / `050`。
- **禁止**：编辑历史 SQL 或校验和、补造收据、用 Ent overlay 代替 canonical migration、给历史 WorkItem 补登记。
- **租户**：所有查询必须带 `tenant_id`；跨租户 fail-closed（契约 §3.1）。
- **数据库写入需单独授权**：Task 1、Task 2 含写操作（建库/标记），执行前必须确认已授权并具备备份恢复准备；Task 3、Task 4、Task 5 只改代码与迁移文件，**不在本计划内对任何库执行迁移**。
- **提交粒度**：每个 Task 结束提交一次，commit message 用 `feat:` / `chore:` / `docs:` 前缀。

## File Structure

| 文件 | 职责 | 动作 |
| --- | --- | --- |
| `itsm-backend/migrations/049_department_code_tenant_unique.sql` | 建立 `(tenant_id, code)` 唯一索引 | 新建 |
| `itsm-backend/migrations/049_department_code_tenant_unique_verify.sql` | 索引存在性校验 | 新建 |
| `itsm-backend/migration/department_identity.go` | 049 版本常量 + SQL 读取 | 新建 |
| `itsm-backend/migration/migrations.go` | 注册 049 / 050，暴露 SQL | 修改 |
| `itsm-backend/cmd/sync_ehr_master_data/department_upsert.go` | 按 `(tenant_id, code)` 幂等 upsert 部门 | 新建 |
| `itsm-backend/cmd/sync_ehr_master_data/department_upsert_test.go` | 幂等回归测试 | 新建 |
| `itsm-backend/cmd/sync_ehr_master_data/main.go` | 部门创建改为调用 upsert | 修改 |
| `itsm-backend/migrations/050_department_node_type.sql` | 新增 `node_type` 列 + 取值约束 | 新建 |
| `itsm-backend/migration/department_node_type.go` | 050 版本常量 + SQL 读取 | 新建 |
| `itsm-backend/ent/schema/department.go` | 增加 `node_type` 字段与租户内编码唯一索引 | 修改 |
| `itsm-backend/handlers/common/entity.go` | `Department` 增加 `NodeType` | 修改 |
| `itsm-backend/handlers/common/repository.go` | 新增 `ListDepartmentChildren` 接口 | 修改 |
| `itsm-backend/handlers/common/repository_impl.go` | 轻量投影 + 按父节点查询实现 | 修改 |
| `itsm-backend/handlers/common/service.go` | 透传新方法 | 修改 |
| `itsm-backend/handlers/common/handler.go` | 新 handler `ListDepartmentChildren` | 修改 |
| `itsm-backend/router/router.go` | 注册 `GET /departments/children` | 修改 |

---

### Task 1: 证明克隆来源并把结构拉齐到与 Dev 一致

**Files:**
- 使用（不修改）: `scripts/clone_itsm_migration_db.sh`
- 产出证据: `docs/migrations/2026-09-18-clone-identity-evidence.md`

**Interfaces:**
- Consumes: 无
- Produces: 目标库 `itsm_migration_20260914` 具备 `_migration_clone_manifest`，且 `schema_migrations` 最高版本与 Dev 一致。

> **执行前置**：本 Task 会 DROP/重建数据库对象，必须先取得维护者授权并确认备份可恢复。

- [ ] **Step 1: 只读记录现状（不改任何东西）**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT to_regclass('public._migration_clone_manifest') AS manifest,
       (SELECT count(*) FROM schema_migrations) AS receipts,
       (SELECT max(version) FROM schema_migrations) AS head,
       (SELECT count(*) FROM departments) AS depts,
       (SELECT count(*) FROM users) AS users;"
```

Expected（2026-09-18 实测）：`manifest` 为空、`receipts=14`、`head=019_kaf_execution_integrity_rls`、`depts=7975`、`users=7834`。

- [ ] **Step 2: 从当前 Dev 重建克隆（显式指定源与目标）**

```bash
cd /home/administrator/project/itsm
RECREATE_INCOMPLETE=1 bash scripts/clone_itsm_migration_db.sh \
  itsm_migration_20260914 itsm_config_baseline_20260908
```

Expected：脚本校验 8 张表行数一致后写入 `_migration_clone_manifest` 并退出码 0；任一表不一致则非零退出且**保持失败**（不许重试掩盖）。

- [ ] **Step 3: 验证克隆清单与来源**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT * FROM _migration_clone_manifest;"
```

Expected：出现清单行，含来源库名、时间与各表行数；`departments=7975`。

- [ ] **Step 4: 拉齐结构到与 Dev 相同的 canonical 链**

按 [数据迁移验证 Runbook](../../migrations/runbook-data-migration-validation.md) 与 [开发环境契约](../../development-environment.md#selected-schema-target-047) 的授权范围，使用 canonical Migrator 依次补齐到 Dev 的最高版本。

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -t -A -c "
SELECT max(version) FROM schema_migrations;"
```

Expected：与 Step 1 记录的 Dev 值一致（2026-09-18 实测 Dev 为 `048_cti_governance`）。**不一致就是不通过，不许改库名或改标签掩盖。**

- [ ] **Step 5: 写证据文件并提交**

在 `docs/migrations/2026-09-18-clone-identity-evidence.md` 记录：源库、目标库、执行时间、克隆前/后行数、清单表内容摘要、结构最高版本、执行人、回滚方式（从源库重建）。**只写计数与标识，不写姓名/邮箱/工号。**

```bash
git add docs/migrations/2026-09-18-clone-identity-evidence.md
git commit -m "docs(migration): record the clone provenance and schema alignment evidence"
```

---

### Task 2: 隔离新 ITSM seed / 测试数据

**Files:**
- 新建: `scripts/migration/seed_isolation_plan.py`
- 新建: `scripts/__tests__/test_seed_isolation_plan.py`
- 产出: `docs/migrations/2026-09-18-seed-isolation-inventory.md`

**Interfaces:**
- Consumes: Task 1 的 `itsm_migration_20260914`
- Produces: 待隔离对象清单（JSON）与 dry-run 校验；**不执行删除**

- [ ] **Step 1: 写失败测试**

```python
# scripts/__tests__/test_seed_isolation_plan.py
from scripts.migration.seed_isolation_plan import classify_seed_objects

def test_classifies_product_seed_departments_and_test_accounts():
    rows = {
        "departments": [
            {"id": 1, "code": "IT", "name": "信息技术部"},
            {"id": 15, "code": "1", "name": "组织架构"},
        ],
        "users": [
            {"username": "admin", "role": "super_admin", "active": True},
            {"username": "qa_manager_ops", "role": "ops_manager", "active": True},
            {"username": "D12345", "role": "end_user", "active": True},
        ],
        "groups": [{"name": "ticket-approvers"}, {"name": "dept_manager"}],
    }
    plan = classify_seed_objects(rows)
    assert plan["departments"] == [1]
    assert plan["users"] == ["admin", "qa_manager_ops"]
    assert plan["groups"] == ["dept_manager", "ticket-approvers"]

def test_end_users_are_never_classified_as_seed():
    plan = classify_seed_objects({"departments": [], "users": [{"username": "D1", "role": "end_user", "active": True}], "groups": []})
    assert plan["users"] == []
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm
python3 -m pytest scripts/__tests__/test_seed_isolation_plan.py -q
```

Expected: FAIL，`ModuleNotFoundError: scripts.migration.seed_isolation_plan`。

- [ ] **Step 3: 实现分类器（纯函数，不连库）**

```python
# scripts/migration/seed_isolation_plan.py
"""把新 ITSM 自带的 seed / 测试对象与新迁入数据分开。只做分类，不写库。"""

PRODUCT_SEED_DEPARTMENT_IDS = frozenset(range(1, 15))  # 14 个产品种子部门

NON_END_USER_ROLES = frozenset({
    "super_admin", "dept_manager", "network_eng", "it_director",
    "l1_support", "ops_manager", "ops_engineer", "kaf_automation", "guest", "sysadmin",
})

TEST_GROUP_NAMES = frozenset({"ticket-approvers", "dept_manager", "network_eng"})


def classify_seed_objects(rows):
    """rows: {"departments": [...], "users": [...], "groups": [...]}"""
    departments = sorted(
        d["id"] for d in rows.get("departments", []) if d["id"] in PRODUCT_SEED_DEPARTMENT_IDS
    )
    users = sorted(
        u["username"] for u in rows.get("users", []) if u.get("role") in NON_END_USER_ROLES
    )
    groups = sorted(
        g["name"] for g in rows.get("groups", []) if g.get("name") in TEST_GROUP_NAMES
    )
    return {"departments": departments, "users": users, "groups": groups}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm
python3 -m pytest scripts/__tests__/test_seed_isolation_plan.py -q
python3 -m scripts.migration self-test
```

Expected: 两条都 PASS（第二条确认既有工具包未被破坏）。

- [ ] **Step 5: 只读盘点目标库并生成清单**

```bash
docker exec -e PGPASSWORD=dev123 itsm-postgres-dev psql -U itsm_user -d itsm_migration_20260914 -P pager=off -c "
SELECT 'seed_depts='||count(*) FROM departments WHERE id BETWEEN 1 AND 14;
SELECT 'non_end_user='||count(*) FROM users WHERE role <> 'end_user';
SELECT 'test_catalogs='||count(*) FROM service_catalogs WHERE name ~ '验证|E2E|测试';
SELECT 'test_tickets='||count(*) FROM tickets;"
```

Expected（2026-09-18 实测）：`seed_depts=14`、`non_end_user=8`、`test_catalogs>=4`、`test_tickets=18`。

- [ ] **Step 6: 写隔离清单文档并提交**

`docs/migrations/2026-09-18-seed-isolation-inventory.md` 必须逐类给出：对象类型、标识、判定理由、处置方式（**隔离/打标，不物理删除**）、影响面（是否被历史单据引用）。引用面未确认前不得删除。

```bash
git add scripts/migration/seed_isolation_plan.py scripts/__tests__/test_seed_isolation_plan.py docs/migrations/2026-09-18-seed-isolation-inventory.md
git commit -m "chore(migration): classify new-ITSM seed objects for isolation"
```

---

### Task 3: 部门编码租户内唯一 + 导入幂等

**Files:**
- 新建: `itsm-backend/migrations/049_department_code_tenant_unique.sql`
- 新建: `itsm-backend/migrations/049_department_code_tenant_unique_verify.sql`
- 新建: `itsm-backend/migration/department_identity.go`
- 修改: `itsm-backend/migration/migrations.go`
- 新建: `itsm-backend/cmd/sync_ehr_master_data/department_upsert.go`
- 新建: `itsm-backend/cmd/sync_ehr_master_data/department_upsert_test.go`
- 修改: `itsm-backend/cmd/sync_ehr_master_data/main.go:245-300`
- 修改: `itsm-backend/ent/schema/department.go`

**Interfaces:**
- Consumes: 无
- Produces: `upsertDepartment(ctx context.Context, client *ent.Client, in departmentUpsertInput) (*ent.Department, error)`；`departmentUpsertInput{Name, Code, Description, AreaName, OrgType string; ManagerID, ParentID, TenantID int}`

- [ ] **Step 1: 写失败测试**

```go
// itsm-backend/cmd/sync_ehr_master_data/department_upsert_test.go
package main

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestUpsertDepartmentIsIdempotentByTenantAndCode(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptupsert?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	in := departmentUpsertInput{
		Name: "信息技术部", Code: "IT", Description: "EHR UniqueID: u1",
		AreaName: "中国", OrgType: "department", TenantID: 1,
	}

	first, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err)

	in.Name = "信息技术部（改名）"
	second, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID, "re-running the import must not create a second row")
	require.Equal(t, "信息技术部（改名）", second.Name)

	count, err := client.Department.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestUpsertDepartmentKeepsSameCodeInAnotherTenantSeparate(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptupsert2?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	base := departmentUpsertInput{Name: "信息技术部", Code: "IT", OrgType: "department"}
	base.TenantID = 1
	_, err := upsertDepartment(ctx, client, base)
	require.NoError(t, err)

	base.TenantID = 2
	_, err = upsertDepartment(ctx, client, base)
	require.NoError(t, err)

	count, err := client.Department.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./cmd/sync_ehr_master_data/ -run TestUpsertDepartment -v
```

Expected: FAIL，`undefined: upsertDepartment` / `undefined: departmentUpsertInput`。

- [ ] **Step 3: 实现 upsert**

```go
// itsm-backend/cmd/sync_ehr_master_data/department_upsert.go
package main

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
)

type departmentUpsertInput struct {
	Name        string
	Code        string
	Description string
	AreaName    string
	OrgType     string
	ManagerID   int
	ParentID    int
	TenantID    int
}

// upsertDepartment 按 (tenant_id, code) 幂等写入部门。
// 组织树的权威编码来自旧 ITIL/eHR 对齐结果，重跑导入不得新增第二行。
func upsertDepartment(ctx context.Context, client *ent.Client, in departmentUpsertInput) (*ent.Department, error) {
	existing, err := client.Department.Query().
		Where(department.TenantIDEQ(in.TenantID), department.CodeEQ(in.Code)).
		Only(ctx)
	if err == nil {
		update := client.Department.UpdateOneID(existing.ID).
			SetName(in.Name).
			SetDescription(in.Description).
			SetAreaName(in.AreaName).
			SetOrgType(in.OrgType)
		if in.ParentID > 0 {
			update = update.SetParentID(in.ParentID)
		}
		if in.ManagerID > 0 {
			update = update.SetManagerID(in.ManagerID)
		}
		return update.Save(ctx)
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}

	create := client.Department.Create().
		SetName(in.Name).
		SetCode(in.Code).
		SetDescription(in.Description).
		SetAreaName(in.AreaName).
		SetOrgType(in.OrgType).
		SetTenantID(in.TenantID)
	if in.ParentID > 0 {
		create = create.SetParentID(in.ParentID)
	}
	if in.ManagerID > 0 {
		create = create.SetManagerID(in.ManagerID)
	}
	return create.Save(ctx)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./cmd/sync_ehr_master_data/ -run TestUpsertDepartment -v
```

Expected: 两个测试都 PASS。

- [ ] **Step 5: 把 main.go 的无条件 Create 改成 upsert**

`itsm-backend/cmd/sync_ehr_master_data/main.go` 第 245–300 行的两处 `client.Department.Create()...Save(ctx)` 都改为调用 `upsertDepartment`，`ManagerID` 首轮传 0（保持现有"第二轮再 SetManagerID"的两段式逻辑不变）。

```bash
cd /home/administrator/project/itsm/itsm-backend
go build ./... && go test ./cmd/sync_ehr_master_data/ -v
```

Expected: 编译通过，测试 PASS。

- [ ] **Step 6: 增加租户内编码唯一索引（canonical 049）**

```sql
-- itsm-backend/migrations/049_department_code_tenant_unique.sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_departments_tenant_code
    ON departments (tenant_id, code)
    WHERE deleted_at IS NULL;
```

```sql
-- itsm-backend/migrations/049_department_code_tenant_unique_verify.sql
SELECT 1
FROM pg_indexes
WHERE schemaname = 'public'
  AND indexname = 'idx_departments_tenant_code';
```

在 `itsm-backend/ent/schema/department.go` 增加同名索引声明，使 `enttest` 单测也受约束：

```go
import "entgo.io/ent/schema/index"

func (Department) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "code").Unique(),
	}
}
```

然后生成代码：

```bash
cd /home/administrator/project/itsm/itsm-backend
go generate ./ent
go build ./...
```

Expected: 生成成功，`ent/department` 出现索引相关代码，`go build` 无错误。

- [ ] **Step 7: 注册迁移并跑迁移单测**

```go
// itsm-backend/migration/department_identity.go
package migration

import "itsm-backend/migrations"

const DepartmentCodeTenantUniqueVersion = "049_department_code_tenant_unique"

//go:embed 路径不在此文件；SQL 通过 migrations 包常量读取
var _ = migrations.DepartmentCodeTenantUniqueSQL
```

> 实现时在 `itsm-backend/migrations/assets.go` 增加
> `//go:embed 049_department_code_tenant_unique.sql`
> `var DepartmentCodeTenantUniqueSQL string`，
> 并在 `migration/migrations.go` 的 `RegisteredMigrations` 末尾（`WorkItemRetireVersion` 之前）加入
> `{Version: DepartmentCodeTenantUniqueVersion, Description: "Enforce department code uniqueness per tenant"}`，
> 同时在 `MigrationSQL` 的 switch 中返回该 SQL 与 verify SQL。

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./migration/... -run Test -v 2>&1 | tail -20
```

Expected: 既有迁移测试全部 PASS，新增版本被注册且可读取 SQL。

- [ ] **Step 8: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/cmd/sync_ehr_master_data itsm-backend/ent itsm-backend/migration itsm-backend/migrations
git commit -m "feat(organization): make the EHR department import idempotent and enforce tenant-scoped code uniqueness"
```

---

### Task 4: 组织节点自带类型（node_type）

**Files:**
- 新建: `itsm-backend/migrations/050_department_node_type.sql`
- 新建: `itsm-backend/migration/department_node_type.go`
- 修改: `itsm-backend/migration/migrations.go`
- 修改: `itsm-backend/ent/schema/department.go`
- 修改: `itsm-backend/handlers/common/entity.go:26-39`
- 修改: `itsm-backend/handlers/common/repository_impl.go`（`toDeptDomain`）
- 新建: `itsm-backend/handlers/common/department_node_type_test.go`

**Interfaces:**
- Consumes: Task 3 的 `Department` schema
- Produces: `Department.NodeType`（取值 `company` / `branch` / `department` / `team`，空串表示未分类）

- [ ] **Step 1: 写失败测试**

```go
// itsm-backend/handlers/common/department_node_type_test.go
package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDepartmentNodeTypeAcceptsTheFourTypes(t *testing.T) {
	for _, want := range []string{"company", "branch", "department", "team"} {
		got, err := normalizeDepartmentNodeType(want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestNormalizeDepartmentNodeTypeRejectsUnknownValues(t *testing.T) {
	for _, bad := range []string{"集团", "DIVISION", " company"} {
		_, err := normalizeDepartmentNodeType(bad)
		require.Error(t, err, "unknown node type must fail closed: %q", bad)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./handlers/common/ -run TestNormalizeDepartmentNodeType -v
```

Expected: FAIL，`undefined: normalizeDepartmentNodeType`。

- [ ] **Step 3: 实现校验函数**

```go
// itsm-backend/handlers/common/department_node_type.go
package common

import "fmt"

// 组织节点类型。类型写在节点上，不靠"处在第几层"判断。
const (
	orgNodeCompany    = "company"
	orgNodeBranch     = "branch"
	orgNodeDepartment = "department"
	orgNodeTeam       = "team"
)

var departmentNodeTypes = map[string]struct{}{
	orgNodeCompany:    {},
	orgNodeBranch:     {},
	orgNodeDepartment: {},
	orgNodeTeam:       {},
}

// normalizeDepartmentNodeType 只接受四种已知取值；未知取值 fail-closed，
// 不静默归类，否则组织树会长出无法解释的类型。
func normalizeDepartmentNodeType(raw string) (string, error) {
	if _, ok := departmentNodeTypes[raw]; !ok {
		return "", fmt.Errorf("unsupported department node type: %q", raw)
	}
	return raw, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./handlers/common/ -run TestNormalizeDepartmentNodeType -v
```

Expected: PASS。

- [ ] **Step 5: 落库字段与迁移**

```sql
-- itsm-backend/migrations/050_department_node_type.sql
ALTER TABLE departments
    ADD COLUMN IF NOT EXISTS node_type varchar NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'departments_node_type_value_check'
          AND conrelid = 'departments'::regclass
    ) THEN
        ALTER TABLE departments
            ADD CONSTRAINT departments_node_type_value_check
            CHECK (node_type IN ('', 'company', 'branch', 'department', 'team'));
    END IF;
END $$;
```

`itsm-backend/ent/schema/department.go` 增加：

```go
field.String("node_type").
	Comment("节点类型: company=公司, branch=分公司, department=部门, team=组；空串=未分类（与 org_type 的仓库维度并存）").
	Optional().
	Default(""),
```

`itsm-backend/migration/department_node_type.go` + `migrations/assets.go` 按 Task 3 Step 7 的同一模式注册 `050_department_node_type`。

```bash
cd /home/administrator/project/itsm/itsm-backend
go generate ./ent && go build ./... && go test ./migration/... ./handlers/common/ -run Test -v 2>&1 | tail -20
```

Expected: 编译与测试全部 PASS。

- [ ] **Step 6: 在组织树投影里暴露该字段**

`handlers/common/entity.go` 的 `Department` 增加 `NodeType string \`json:"nodeType"\``；`repository_impl.go` 的 `toDeptDomain` 一并赋值。

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./handlers/common/ -v 2>&1 | tail -20
```

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/handlers/common itsm-backend/ent itsm-backend/migration itsm-backend/migrations
git commit -m "feat(organization): let each organisation node carry its own type"
```

> **本 Task 不负责给 5202 个节点赋值**：类型归属需要业务分流表（《组织》设计第 9 节开放问题）。字段与校验先就位，赋值在业务确认后的独立数据任务里做。

---

### Task 5: 组织树轻量投影与按父节点懒加载

**Files:**
- 修改: `itsm-backend/handlers/common/repository.go:20` 附近
- 修改: `itsm-backend/handlers/common/repository_impl.go:257-300` 附近
- 修改: `itsm-backend/handlers/common/service.go:205` 附近
- 修改: `itsm-backend/handlers/common/handler.go:186` 附近
- 修改: `itsm-backend/router/router.go:1249-1253`
- 新建: `itsm-backend/handlers/common/department_children_test.go`

**Interfaces:**
- Consumes: Task 4 的 `Department.NodeType`
- Produces:
  - `type DepartmentNode struct { ID int; Code string; Name string; ParentID int; NodeType string; HasChildren bool }`
  - `ListDepartmentChildren(ctx context.Context, tenantID, parentID int) ([]*DepartmentNode, error)`

- [ ] **Step 1: 写失败测试**

```go
// itsm-backend/handlers/common/department_children_test.go
package common

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestListDepartmentChildrenReturnsProjectionWithHasChildren(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	mid, err := client.Department.Create().SetName("分公司A").SetCode("1A").SetTenantID(1).SetNodeType(orgNodeBranch).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.Department.Create().SetName("运维组").SetCode("1A01").SetTenantID(1).SetNodeType(orgNodeTeam).SetParentID(mid.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, "1A", children[0].Code)
	require.Equal(t, orgNodeBranch, children[0].NodeType)
	require.True(t, children[0].HasChildren, "a node with children must say so without loading them")

	leaves, err := repo.ListDepartmentChildren(ctx, 1, mid.ID)
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.False(t, leaves[0].HasChildren)

	// 根节点在库里 parent_id 是 NULL，parentId=0 必须能取到。
	roots, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.Equal(t, "1", roots[0].Code)
	require.True(t, roots[0].HasChildren)
}

func TestListDepartmentChildrenIsTenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren2?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	_, err = client.Department.Create().SetName("别家").SetCode("1").SetTenantID(2).SetNodeType(orgNodeCompany).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Empty(t, children, "cross-tenant children must never leak")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./handlers/common/ -run TestListDepartmentChildren -v
```

Expected: FAIL，`repo.ListDepartmentChildren undefined`。

- [ ] **Step 3: 实现投影查询**

```go
// itsm-backend/handlers/common/repository_impl.go（追加）
const maxDepartmentChildren = 500

// ListDepartmentChildren 只返回直接下级，且只带展示必需字段。
// 全树近 8000 个节点，禁止把完整实体一次性下发给前端。
//
// 注意 parent_id 是可空列：根节点在库里是 NULL 而不是 0，
// 所以 parentID==0 必须显式查 NULL，否则根节点一个都取不到。
func (r *EntRepository) ListDepartmentChildren(ctx context.Context, tenantID, parentID int) ([]*DepartmentNode, error) {
	parentPredicate := department.ParentIDEQ(parentID)
	if parentID == 0 {
		parentPredicate = department.Or(
			department.ParentIDIsNil(),
			department.ParentIDEQ(0),
		)
	}

	rows, err := r.client.Department.Query().
		Where(
			department.TenantID(tenantID),
			parentPredicate,
			department.DeletedAtIsNil(),
		).
		Order(ent.Asc(department.FieldCode)).
		Limit(maxDepartmentChildren + 1).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxDepartmentChildren {
		rows = rows[:maxDepartmentChildren]
	}

	parentIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		parentIDs = append(parentIDs, row.ID)
	}

	withChildren := map[int]struct{}{}
	if len(parentIDs) > 0 {
		childRows, err := r.client.Department.Query().
			Where(
				department.TenantID(tenantID),
				department.ParentIDIn(parentIDs...),
				department.DeletedAtIsNil(),
			).
			Select(department.FieldParentID).
			All(ctx)
		if err != nil {
			return nil, err
		}
		for _, child := range childRows {
			withChildren[child.ParentID] = struct{}{}
		}
	}

	result := make([]*DepartmentNode, 0, len(rows))
	for _, row := range rows {
		_, hasChildren := withChildren[row.ID]
		result = append(result, &DepartmentNode{
			ID:          row.ID,
			Code:        row.Code,
			Name:        row.Name,
			ParentID:    row.ParentID,
			NodeType:    row.NodeType,
			HasChildren: hasChildren,
		})
	}
	_ = ids
	return result, nil
}
```

`handlers/common/entity.go` 增加：

```go
// DepartmentNode 是组织树的轻量投影：只含展示与展开所需字段。
type DepartmentNode struct {
	ID          int    `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	ParentID    int    `json:"parentId"`
	NodeType    string `json:"nodeType"`
	HasChildren bool   `json:"hasChildren"`
}
```

`repository.go` 接口增加：

```go
ListDepartmentChildren(ctx context.Context, tenantID, parentID int) ([]*DepartmentNode, error)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./handlers/common/ -run TestListDepartmentChildren -v
```

Expected: 两个测试都 PASS（含跨租户不泄漏）。

- [ ] **Step 5: 接线 service / handler / route**

`service.go`：

```go
func (s *Service) ListDepartmentChildren(ctx context.Context, tenantID, parentID int) ([]*DepartmentNode, error) {
	return s.repo.ListDepartmentChildren(ctx, tenantID, parentID)
}
```

`handler.go`：

```go
func (h *Handler) ListDepartmentChildren(c *gin.Context) {
	tenantID := c.GetInt("tenant_id")
	parentID, err := strconv.Atoi(c.DefaultQuery("parentId", "0"))
	if err != nil || parentID < 0 {
		common.BadRequest(c, "parentId 必须是非负整数")
		return
	}
	children, err := h.svc.ListDepartmentChildren(c.Request.Context(), tenantID, parentID)
	if err != nil {
		common.InternalError(c, "获取下级部门失败: "+err.Error())
		return
	}
	common.Success(c, children)
}
```

`router/router.go`，紧邻既有 `/departments/tree` 两条注册（第 1249 与 1306 行）各加一行：

```go
org.GET("/departments/children", middleware.RequirePermission("department", "read"), config.CommonHandler.ListDepartmentChildren)
```

```bash
cd /home/administrator/project/itsm/itsm-backend
go build ./... && go test ./handlers/common/ -v 2>&1 | tail -20
```

Expected: 编译通过、测试 PASS。

- [ ] **Step 6: 提交**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/handlers/common itsm-backend/router/router.go
git commit -m "feat(organization): expose a lightweight lazy-loaded department tree endpoint"
```

---

## Self-Review

**Spec coverage**

| 《组织》设计要求 | 落点 |
| --- | --- |
| 节点自带类型（§2） | Task 4 |
| 允许少层 / 不靠层级（§2） | Task 4（无层级推导）+ Task 5（懒加载天然不分层） |
| 编码唯一不可变（§2、§6） | Task 3 |
| 树骨架对齐、员工落位（§3） | **不在本计划**——属《人员与汇报线》计划（归属迁移）与其后的组织数据任务 |
| 维护与留痕（§4） | **不在本计划**——属《部门》计划的负责人维护任务 |
| 查询与展示性能（§5） | Task 5 |
| 硬性校验（§6） | Task 3（唯一）、Task 4（类型 fail-closed）、Task 5（租户 fail-closed） |
| 第 0 优先级前置项（§8） | Task 1、Task 3 |
| seed 隔离（契约 §7.3） | Task 2 |

**Placeholder scan：** 无 TBD/TODO；每个代码步骤都给了可编译内容。

**Type consistency：** `DepartmentNode` 在 `entity.go` 定义、`repository.go` 接口、`repository_impl.go` 实现、测试四处同名同字段。`orgNodeBranch` 等常量在 Task 4 定义、Task 5 测试引用，保持一致。`upsertDepartment` / `departmentUpsertInput` 在 Task 3 内自洽。

**已知缺口（明确不在本计划）**

1. 员工归属从分公司级改到最细组、上级回填 614/覆盖 404/清理 31（属《人员与汇报线》计划）。
2. 部门负责人维护与脏值清除（属《部门》计划）。
3. 审批解析器、快照冻结、解析元数据、路由分批迁移（属《流程与路由》计划）。
4. 5202 个节点的类型赋值（依赖业务分流表）。
5. Task 1/2 的库写入需维护者授权后执行。
