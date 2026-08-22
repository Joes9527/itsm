# 真实组织数据落地：基于个人汇报链的"总经理"审批人动态解析 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 BPMN "总经理审批"环节能顺着提交人自己的真实汇报链（`user.manager_id`）动态解析到正确的人，替代在矩阵组织下结构性不够用的部门节点回填方案，并用嘉里物流真实 eHR 数据（`tenant_id=1`）跑通"Copilot 采购申请"端到端场景。

**Architecture:** 新增 `users.job_title` 字段并让 `sync_ehr_master_data` 回填（此前 `employee_post` 解析出来但从未落库）；新增 `PersonalManagerResolver`（`service/approver/`），复用已有的 `ApproverContext.RequesterID`，沿 `user.manager_id` 向上爬，职位头衔命中"总经理"关键字即停，带环路检测（比照已有 `DeptManagerResolver` 的模式）；BPMN 引擎新增 `assigneeGmChain` 路由方式接入这个 resolver；BPMN 设计器 UI 暴露这个新选项；隔离 14 个跟客户组织无关的产品默认种子部门；最后端到端验证。

**Tech Stack:** Go 1.25 + Gin + Ent ORM + PostgreSQL（后端），Next.js 15 + TypeScript + Ant Design 5 + bpmn-js（前端）。

**Spec:** `docs/superpowers/specs/2026-08-20-personal-manager-chain-approval-design.md`

## Global Constraints

- 后端 DTO 响应字段一律 camelCase，Ent Schema/数据库字段 snake_case（CLAUDE.md 字段命名规范）。
- Go 文件改动后跑 `gofmt -w`；每个任务改完跑对应包的 `go test`。
- 前端改动跑 `npm run type-check` 和 `npx eslint`。
- 不改动 `middleware/rbac.go`——本次全部改动属于 BPMN 审批候选人解析范畴，不影响 RBAC 权限判定。
- `DeptManagerResolver`/`assigneeDeptId` 机制本身保留、不废弃，只是不再是"总经理"审批的首选路径。
- 不处理本次范围外的两项：10 个真实孤儿部门节点的父级修复、`is_leader` 字段回填。
- 数据库连接信息（本机 dev 环境）：`DB_HOST=localhost DB_PORT=5432 DB_USER=itsm_user DB_PASSWORD=dev123 DB_NAME=itsm`。

---

## Task 1: `users` 表新增 `job_title` 字段

**Files:**
- Modify: `itsm-backend/ent/schema/user.go`
- Create: `itsm-backend/migrations/20260820_add_user_job_title.sql`
- Generated (通过 `go generate` 产生，不要手改): `itsm-backend/ent/user.go`、`itsm-backend/ent/user/*.go`、`itsm-backend/ent/user_create.go`、`itsm-backend/ent/user_update.go`、`itsm-backend/ent/mutation.go`、`itsm-backend/ent/runtime.go`
- Test: `itsm-backend/ent/user_job_title_test.go`（新建）

**Interfaces:**
- 新增 `ent.User.JobTitle string`（对应 DB 列 `job_title`，nullable），`ent.UserCreate.SetJobTitle(string)` / `ent.UserUpdateOne.SetJobTitle(string)`（ent 生成，供 Task 2/3 使用）。

- [ ] **Step 1: 在 schema 里加字段**

在 `itsm-backend/ent/schema/user.go` 的 `Fields()` 里，紧接着 `manager_id` 字段（第 84-89 行）后面加：

```go
		field.String("job_title").
			Comment("职位头衔，来自HR系统 employee_post 字段，用于 PersonalManagerResolver 按" +
				"关键字识别审批层级（如"总经理"）。跟 function_line（职能条线）是两个不同维度：" +
				"job_title 是这个人自己的头衔，function_line 是这个人所属的横向业务分组。").
			Optional(),
```

- [ ] **Step 2: 重新生成 ent 代码**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend/ent
gofmt -w schema/user.go
go generate ./...
```
Expected: 无报错退出；`git status` 能看到 `ent/user.go`、`ent/user/user.go`、`ent/user/where.go`、`ent/user_create.go`、`ent/user_update.go`、`ent/mutation.go`、`ent/runtime.go` 等文件被修改。

- [ ] **Step 3: 写迁移 SQL 文件**

创建 `itsm-backend/migrations/20260820_add_user_job_title.sql`：
```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS job_title VARCHAR(255);
```

- [ ] **Step 4: 对本机 dev 库执行迁移**

Run:
```bash
export PGPASSWORD=dev123
psql -h localhost -p 5432 -U itsm_user -d itsm -f /home/administrator/project/itsm/itsm-backend/migrations/20260820_add_user_job_title.sql
```
Expected: `ALTER TABLE` 输出，无报错。

- [ ] **Step 5: 写验证测试**

创建 `itsm-backend/ent/user_job_title_test.go`：
```go
package ent_test

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestUser_JobTitle_PersistsAndDefaultsEmpty(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:user_job_title_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tenant, err := client.Tenant.Create().
		SetName("JobTitle Tenant").
		SetCode("job-title").
		SetDomain("job-title.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	withoutTitle, err := client.User.Create().
		SetUsername("no_title_user").
		SetEmail("no-title@job-title.test").
		SetName("No Title").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	require.Empty(t, withoutTitle.JobTitle)

	withTitle, err := client.User.Create().
		SetUsername("gm_user").
		SetEmail("gm@job-title.test").
		SetName("GM User").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		SetJobTitle("财务管理总经理").
		Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "财务管理总经理", withTitle.JobTitle)
}
```

- [ ] **Step 6: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./ent/... -run TestUser_JobTitle_PersistsAndDefaultsEmpty -v
```
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/ent/ itsm-backend/migrations/20260820_add_user_job_title.sql
git commit -m "$(cat <<'EOF'
feat(users): 新增职位头衔字段(job_title)

来自HR系统 employee_post 字段，此前只在 sync_ehr_master_data 里解析出来
就丢弃、从未落库。PersonalManagerResolver（下个任务）需要按这个字段的
关键字识别汇报链上谁是"总经理"级别的审批人。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `sync_ehr_master_data` 回填 `job_title`，并补齐既有用户的幂等更新路径

**Files:**
- Modify: `itsm-backend/cmd/sync_ehr_master_data/main.go`

**Interfaces:**
- 无新增导出接口，纯 CLI 内部改动。

**背景（写给实施者）**：这个脚本目前对用户只有"创建"没有"已存在则更新"的分支——`client.User.Create()` 在用户名已存在时会因为 `users.username` 唯一约束失败，被 `log.Printf` 记下来跳过。这意味着**这个脚本目前重跑一次，对本机 dev 库里已经导入过的 7826 个用户完全无效**（每个都会创建失败），包括本任务要回填的 `job_title`，也包括脚本本身第二遍扫描的"直属上级"链接（因为 `empIDToUserID` 这个 map 只在创建成功的分支里被写入，重跑时几乎全员创建失败，这个 map 里就没有已存在用户的记录，供职位上级链接用的第二遍扫描也会大面积失效）。本任务顺带修好这个问题——给已存在用户加一条"按用户名查到就更新，而不是创建失败"的路径，这是让 `job_title` 真正回填到已有数据所必需的，不是范围外的顺手清理。

- [ ] **Step 1: 加已存在用户的预加载查询**

在 `itsm-backend/cmd/sync_ehr_master_data/main.go` 顶部 import 里加 `"itsm-backend/ent/user"`：
```go
import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent/user"

	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)
```

在 "5. Import Active Users & Link Departments" 这一段（`hashedPassword, err := bcrypt.GenerateFromPassword(...)` 之后，`seenUsernames := make(map[string]bool)` 之前）加一段预加载：

```go
	// 预加载已存在用户（按 username=EmpID 索引），让本脚本可以安全重跑：已存在的用户走
	// 更新分支（目前只更新 job_title，避免覆盖管理员在admin界面手工改过的其他字段），
	// 不会因为 username 唯一约束而创建失败——之前没有这层判断，重跑对已导入用户完全无效
	// （包括下面要回填的 job_title，也包括"直属上级"第二遍扫描要用的 empIDToUserID 映射）。
	existingUsers, err := client.User.Query().
		Where(user.TenantIDEQ(tenantID)).
		All(ctx)
	if err != nil {
		log.Fatalf("Failed to query existing users: %v", err)
	}
	existingUsernameToID := make(map[string]int, len(existingUsers))
	for _, u := range existingUsers {
		existingUsernameToID[u.Username] = u.ID
	}
	log.Printf("Loaded %d existing users from DB for idempotent re-run", len(existingUsers))
```

- [ ] **Step 2: 已存在用户走更新分支，新用户走创建分支并写入 job_title**

找到当前创建用户的代码块：
```go
		c := client.User.Create().
			SetUsername(username).
			SetName(name).
			SetEmail(email).
			SetPhone(phone).
			SetRole("end_user").
			SetPasswordHash(passwordHashStr).
			SetActive(true).
			SetTenantID(tenantID)

		if deptID > 0 {
			c.SetDepartmentID(deptID)
			c.SetDepartment(deptName)
			deptLinkedCount++
		}

		created, err := c.Save(ctx)
		if err != nil {
			log.Printf("[%d/%d] Failed to create user %s (%s): %v", idx+1, len(activePersons), username, email, err)
		} else {
			insertedUserCount++
			empIDToUserID[p.EmpID] = created.ID
		}
```

改成：
```go
		if existingID, ok := existingUsernameToID[username]; ok {
			// 已存在：只更新 job_title（本任务范围），不碰其他字段——避免覆盖运营人员在
			// admin 界面手工改过的角色/部门/激活状态等。empIDToUserID 仍然要填，否则下面
			// 第二遍扫描"直属上级"时，已存在用户的汇报线永远链接不上（这个 map 是那段代码
			// 唯一的 EmpID -> DB ID 查找来源）。
			empIDToUserID[p.EmpID] = existingID
			if p.Post != "" {
				if err := client.User.UpdateOneID(existingID).SetJobTitle(p.Post).Exec(ctx); err != nil {
					log.Printf("[%d/%d] Failed to update job_title for existing user %s: %v", idx+1, len(activePersons), username, err)
				} else {
					updatedJobTitleCount++
				}
			}
		} else {
			c := client.User.Create().
				SetUsername(username).
				SetName(name).
				SetEmail(email).
				SetPhone(phone).
				SetRole("end_user").
				SetPasswordHash(passwordHashStr).
				SetActive(true).
				SetTenantID(tenantID)

			if deptID > 0 {
				c.SetDepartmentID(deptID)
				c.SetDepartment(deptName)
				deptLinkedCount++
			}
			if p.Post != "" {
				c.SetJobTitle(p.Post)
			}

			created, err := c.Save(ctx)
			if err != nil {
				log.Printf("[%d/%d] Failed to create user %s (%s): %v", idx+1, len(activePersons), username, email, err)
			} else {
				insertedUserCount++
				empIDToUserID[p.EmpID] = created.ID
			}
		}
```

在 `insertedUserCount := 0` 旁边加一个新计数器：
```go
	insertedUserCount := 0
	deptLinkedCount := 0
	updatedJobTitleCount := 0
```

最后的汇总日志：
```go
	log.Printf("EHR Import Finished! Total Org: %d, Total Active Users: %d (Dept Linked: %d)", insertedDeptCount, insertedUserCount, deptLinkedCount)
```
改成：
```go
	log.Printf("EHR Import Finished! Total Org: %d, New Users: %d (Dept Linked: %d), Existing Users job_title Updated: %d", insertedDeptCount, insertedUserCount, deptLinkedCount, updatedJobTitleCount)
```

- [ ] **Step 3: 编译确认**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w cmd/sync_ehr_master_data/main.go
go build -o /tmp/sync_ehr cmd/sync_ehr_master_data/main.go
```
Expected: 无输出（编译成功）。

- [ ] **Step 4: Dry-run 预演**

Run:
```bash
/tmp/sync_ehr -dry-run -excel /mnt/d/SynologyDrive/kerry/service-support/ehr-data.xlsx
```
Expected: 打印 `[DRY RUN] Would import 7961 departments and <N> users. Exiting.`，不连接数据库、不写任何数据（dry-run 分支在读取 Excel 之后、连接 DB 之前就直接返回，本次改动没有改变这个提前退出的位置）。

- [ ] **Step 5: 正式执行，回填真实数据**

Run:
```bash
/tmp/sync_ehr -tenant-id 1 -excel /mnt/d/SynologyDrive/kerry/service-support/ehr-data.xlsx 2>&1 | tail -30
```
Expected：日志里 `New Users: 0`（或很小的个位数——正常情况下这次重跑不应该产生新用户，因为组织/人员数据没变，只是加了字段）、`Existing Users job_title Updated` 是一个大数（预期接近 4679，即嘉顺达物流有限公司子树里带 `employee_post` 的在职员工数，加上 eHR 数据里其余子树的同类记录，实际数字以真实运行结果为准）。部门这边预期 `Total Org` 跟之前一致（`insertedDeptCount` 逻辑本次未改动，重跑部门这块仍然会因为 `code` 唯一约束在已存在部门上报错跳过，属于现有行为，不在本任务范围内一并修）。

- [ ] **Step 6: 验证数据库结果**

Run:
```bash
export PGPASSWORD=dev123
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
SELECT count(*) AS total, count(*) FILTER (WHERE job_title IS NOT NULL AND job_title != '') AS has_job_title
FROM users WHERE tenant_id=1;
"
```
Expected: `has_job_title` 是一个远大于 0 的数字（不要求 100%，因为不是所有在职员工的 `employee_post` 都非空）。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/cmd/sync_ehr_master_data/main.go
git commit -m "$(cat <<'EOF'
feat(sync_ehr_master_data): 回填 job_title，补齐已存在用户的幂等更新路径

employee_post 之前解析出来就丢弃，从未落库。顺带修复脚本对已存在用户
只会创建失败、完全无法重跑生效的问题——已存在用户改走更新分支（只更新
job_title，不覆盖其他字段），并修好 empIDToUserID 映射在重跑时失效、
导致"直属上级"第二遍扫描对已有用户失效的连带问题。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `PersonalManagerResolver`

**Files:**
- Create: `itsm-backend/service/approver/personal_manager_resolver.go`
- Test: `itsm-backend/service/approver/approver_test.go`（追加用例）

**Interfaces:**
- `PersonalManagerResolver.GetType() string` 返回 `"personal_manager_gm"`。
- `PersonalManagerResolver.Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error)`——复用已有的 `ApproverContext.RequesterID`（不新增字段），沿 `user.manager_id` 爬，命中 `job_title` 包含"总经理"的人即返回。

- [ ] **Step 1: 写失败的测试**

在 `itsm-backend/service/approver/approver_test.go` 文件末尾追加：

```go
func TestPersonalManagerResolver_Resolve_ClimbsToGMTitle(t *testing.T) {
	fx := newApproverFixture(t)
	defer fx.client.Close()

	gm, err := fx.client.User.Create().
		SetUsername("gm_ceo").
		SetEmail("gm@personal-manager.test").
		SetName("GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetJobTitle("综合物流总经理").
		Save(fx.ctx)
	require.NoError(t, err)

	teamLead, err := fx.client.User.Create().
		SetUsername("team_lead").
		SetEmail("lead@personal-manager.test").
		SetName("Team Lead").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetJobTitle("操作主管").
		SetManagerID(gm.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	submitter, err := fx.client.User.Create().
		SetUsername("submitter").
		SetEmail("submitter@personal-manager.test").
		SetName("Submitter").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetJobTitle("客户服务专员").
		SetManagerID(teamLead.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	resolver := NewPersonalManagerResolver()
	approvers, err := resolver.Resolve(fx.ctx, fx.client, &ApproverContext{
		TenantID:    fx.tenant.ID,
		RequesterID: submitter.ID,
	})
	require.NoError(t, err)
	require.Len(t, approvers, 1)
	assert.Equal(t, gm.ID, approvers[0].UserID)
	assert.Equal(t, "personal_manager_gm", approvers[0].Role)
}

func TestPersonalManagerResolver_Resolve_MatrixOrgDisambiguatesByOwnChain(t *testing.T) {
	// 同一个"部门节点"概念在真实数据里可能有多条平级业务线的总经理（矩阵组织），
	// PersonalManagerResolver 按提交人自己的汇报链爬，两个不同业务线的提交人应该
	// 各自解析到自己业务线的总经理，不会串到另一条业务线上。
	fx := newApproverFixture(t)
	defer fx.client.Close()

	gmA, err := fx.client.User.Create().
		SetUsername("gm_iff").
		SetEmail("gm-iff@personal-manager.test").
		SetName("GM IFF").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetJobTitle("国际货代总经理 - 北京片区").
		Save(fx.ctx)
	require.NoError(t, err)

	gmB, err := fx.client.User.Create().
		SetUsername("gm_il").
		SetEmail("gm-il@personal-manager.test").
		SetName("GM IL").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetJobTitle("综合物流总经理-北京片区").
		Save(fx.ctx)
	require.NoError(t, err)

	submitterA, err := fx.client.User.Create().
		SetUsername("submitter_iff").
		SetEmail("submitter-iff@personal-manager.test").
		SetName("Submitter IFF").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetManagerID(gmA.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	submitterB, err := fx.client.User.Create().
		SetUsername("submitter_il").
		SetEmail("submitter-il@personal-manager.test").
		SetName("Submitter IL").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetManagerID(gmB.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	resolver := NewPersonalManagerResolver()

	approversA, err := resolver.Resolve(fx.ctx, fx.client, &ApproverContext{TenantID: fx.tenant.ID, RequesterID: submitterA.ID})
	require.NoError(t, err)
	require.Len(t, approversA, 1)
	assert.Equal(t, gmA.ID, approversA[0].UserID)

	approversB, err := resolver.Resolve(fx.ctx, fx.client, &ApproverContext{TenantID: fx.tenant.ID, RequesterID: submitterB.ID})
	require.NoError(t, err)
	require.Len(t, approversB, 1)
	assert.Equal(t, gmB.ID, approversB[0].UserID)
}

func TestPersonalManagerResolver_Resolve_NoGMInChain(t *testing.T) {
	fx := newApproverFixture(t)
	defer fx.client.Close()

	topOfChain, err := fx.client.User.Create().
		SetUsername("top_no_title").
		SetEmail("top@personal-manager.test").
		SetName("Top No Title").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	submitter, err := fx.client.User.Create().
		SetUsername("submitter_no_gm").
		SetEmail("submitter-no-gm@personal-manager.test").
		SetName("Submitter No GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetManagerID(topOfChain.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	resolver := NewPersonalManagerResolver()
	_, err = resolver.Resolve(fx.ctx, fx.client, &ApproverContext{TenantID: fx.tenant.ID, RequesterID: submitter.ID})
	assert.Error(t, err)
}

func TestPersonalManagerResolver_Resolve_CycleDetected(t *testing.T) {
	fx := newApproverFixture(t)
	defer fx.client.Close()

	userA, err := fx.client.User.Create().
		SetUsername("cycle_a").
		SetEmail("cycle-a@personal-manager.test").
		SetName("Cycle A").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	userB, err := fx.client.User.Create().
		SetUsername("cycle_b").
		SetEmail("cycle-b@personal-manager.test").
		SetName("Cycle B").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(fx.tenant.ID).
		SetManagerID(userA.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	// 手工造一个环：A 的 manager 指向 B，B 的 manager 指向 A，两者都没有"总经理"头衔。
	_, err = fx.client.User.UpdateOneID(userA.ID).SetManagerID(userB.ID).Save(fx.ctx)
	require.NoError(t, err)

	resolver := NewPersonalManagerResolver()
	_, err = resolver.Resolve(fx.ctx, fx.client, &ApproverContext{TenantID: fx.tenant.ID, RequesterID: userA.ID})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}
```

- [ ] **Step 2: 运行测试确认失败（因为 `PersonalManagerResolver` 还不存在，编译失败）**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./service/approver/... -run TestPersonalManagerResolver -v
```
Expected: 编译错误，`undefined: NewPersonalManagerResolver`。

- [ ] **Step 3: 实现 `PersonalManagerResolver`**

创建 `itsm-backend/service/approver/personal_manager_resolver.go`：

```go
package approver

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// PersonalManagerResolver 顺着申请人自己的真实汇报链（user.manager_id）向上爬，找到第一个
// 职位头衔（job_title）包含"总经理"的人作为审批人。矩阵组织下同一个部门节点常年并存多条
// 业务线的平级总经理，DeptManagerResolver（部门维度，同一个节点对所有人给出同一个 answer）
// 在这种场景下无法唯一定位；PersonalManagerResolver 按人（顺着提交人自己的真实关系链）解析，
// 天然按业务线区分，不需要额外的业务线匹配规则。设计详见
// docs/superpowers/specs/2026-08-20-personal-manager-chain-approval-design.md。
type PersonalManagerResolver struct{}

// NewPersonalManagerResolver creates a new PersonalManagerResolver
func NewPersonalManagerResolver() *PersonalManagerResolver {
	return &PersonalManagerResolver{}
}

// GetType returns the resolver type
func (r *PersonalManagerResolver) GetType() string {
	return "personal_manager_gm"
}

// Resolve resolves the nearest GM-titled person in the requester's personal reporting chain
func (r *PersonalManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	if appCtx.RequesterID == 0 {
		return nil, fmt.Errorf("requester_id is required for personal_manager_gm resolver")
	}
	return r.resolve(ctx, client, appCtx.RequesterID, appCtx.TenantID, make(map[int]bool))
}

// resolve 是 Resolve 的内部实现，多带一个 visited 集合用来检测汇报链里的环——理论上
// manager_id 不该成环，但 HR 数据录入错误可能造成环，原来的纯递归会无限循环到栈溢出。
// 比照 DeptManagerResolver 的环路检测模式（service/approver/dept_manager_resolver.go）。
func (r *PersonalManagerResolver) resolve(ctx context.Context, client *ent.Client, userID, tenantID int, visited map[int]bool) ([]*ApproverInfo, error) {
	if visited[userID] {
		return nil, fmt.Errorf("reporting chain cycle detected at user %d", userID)
	}
	visited[userID] = true

	u, err := client.User.Query().
		Where(user.IDEQ(userID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("user not found: %d", userID)
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if u.ManagerID == 0 {
		return nil, fmt.Errorf("no GM-level manager found in reporting chain starting at user %d", userID)
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(u.ManagerID), user.TenantIDEQ(tenantID), user.Active(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("manager user not found or inactive: %d", u.ManagerID)
		}
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	if strings.Contains(manager.JobTitle, "总经理") {
		return []*ApproverInfo{
			{
				UserID:    manager.ID,
				UserName:  manager.Name,
				UserEmail: manager.Email,
				Role:      "personal_manager_gm",
				Source:    fmt.Sprintf("reporting_chain:%d", userID),
			},
		}, nil
	}

	return r.resolve(ctx, client, manager.ID, tenantID, visited)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w service/approver/personal_manager_resolver.go service/approver/approver_test.go
go test ./service/approver/... -v -run TestPersonalManagerResolver
```
Expected: 全部 4 个新测试 PASS。

- [ ] **Step 5: 跑整个 approver 包，确认没有破坏既有用例**

Run:
```bash
go test ./service/approver/... 2>&1 | tail -20
```
Expected: `ok`，无 FAIL。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/service/approver/personal_manager_resolver.go itsm-backend/service/approver/approver_test.go
git commit -m "$(cat <<'EOF'
feat(approver): 新增 PersonalManagerResolver，沿个人汇报链解析总经理审批人

矩阵组织下同一个部门节点常年并存多条业务线的平级总经理，
DeptManagerResolver（部门维度）无法唯一定位。改为顺着提交人自己的
user.manager_id 汇报链向上爬，职位头衔命中"总经理"关键字即停，
天然按人区分、规避矩阵歧义。带环路检测。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: BPMN 引擎接入 `PersonalManagerResolver`（`assigneeGmChain`）

**Files:**
- Modify: `itsm-backend/service/bpmn_types.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn_process_engine_test.go`

**Interfaces:**
- `BPMNUserTask.AssigneeGmChain bool`（新增字段，`xml:"assigneeGmChain,attr"`）。
- `CustomProcessEngine.resolveGmChainAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string`（新增方法，签名比照已有的 `resolveApprovalAssignee`）。

- [ ] **Step 1: BPMN 解析结构体加字段**

`itsm-backend/service/bpmn_types.go` 里 `BPMNUserTask` 结构体（第 116-142 行），在 `AssigneeTempTeamId` 字段后面加：
```go
	AssigneeTempTeamId      int    `xml:"assigneeTempTeamId,attr"`
	AssigneeGmChain         bool   `xml:"assigneeGmChain,attr"`
```

- [ ] **Step 2: 写失败的集成测试**

在 `itsm-backend/service/bpmn_process_engine_test.go` 文件末尾追加（沿用文件里已有的 `enttest`/`zaptest` 触库测试模式，参照文件里已有的 `TestResolveRoleCandidates_MatchesPrimaryAndAdditionalRole` 用例风格）：

```go
func TestCreateUserTask_AssigneeGmChain_ResolvesSubmitterOwnChain(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:gm_chain_task?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("GM Chain Tenant").
		SetCode("gm-chain").
		SetDomain("gm-chain.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	gm, err := client.User.Create().
		SetUsername("branch_gm").
		SetEmail("gm@gm-chain.test").
		SetName("Branch GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetJobTitle("综合物流总经理").
		Save(ctx)
	require.NoError(t, err)

	submitter, err := client.User.Create().
		SetUsername("gm_chain_submitter").
		SetEmail("submitter@gm-chain.test").
		SetName("Submitter").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetManagerID(gm.ID).
		Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)

	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	assignee := engine.resolveGmChainAssignee(ctx, instance, submitter)
	assert.Equal(t, strconv.Itoa(gm.ID), assignee)
}

func TestCreateUserTask_AssigneeGmChain_SelfApprovalFallsBackEmpty(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:gm_chain_self?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("GM Chain Self Tenant").
		SetCode("gm-chain-self").
		SetDomain("gm-chain-self.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	gmSubmitter, err := client.User.Create().
		SetUsername("self_gm").
		SetEmail("self-gm@gm-chain-self.test").
		SetName("Self GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetJobTitle("综合物流总经理").
		Save(ctx)
	require.NoError(t, err)

	// 提交人自己没有更上级的总经理（manager_id=0），resolveGmChainAssignee 应该返回空串，
	// 而不是报错或者把提交人自己当成审批人。
	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)
	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	assignee := engine.resolveGmChainAssignee(ctx, instance, gmSubmitter)
	assert.Equal(t, "", assignee)
}
```

检查文件顶部 import（当前状态只有 `context`/`testing`/`itsm-backend/ent/enttest`/`sqlite3`/`assert`/`require`/`zaptest`，缺 `strconv` 和 `itsm-backend/ent` 这两个新用例要用到的包）：
```go
import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)
```

- [ ] **Step 3: 运行测试确认失败**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./service/... -run TestCreateUserTask_AssigneeGmChain -v
```
Expected: 编译错误，`engine.resolveGmChainAssignee undefined`。

- [ ] **Step 4: 实现 `resolveGmChainAssignee` 并接入 switch**

在 `itsm-backend/service/bpmn_process_engine.go` 的 `resolveApprovalAssignee` 函数（第 1015-1044 行）后面加一个新函数：

```go
// resolveGmChainAssignee 把申请人自己的个人汇报链（user.manager_id，非部门维度）向上爬到第一个
// job_title 命中"总经理"关键字的人，解析为审批任务的 assignee。矩阵组织下同一个部门节点可能
// 有多个平级总经理（不同业务线各自的负责人），PersonalManagerResolver 按人（顺着申请人自己的
// 真实汇报链）解析，天然避开这种部门维度无法区分的歧义——设计详见
// docs/superpowers/specs/2026-08-20-personal-manager-chain-approval-design.md。
// 解析失败，或者解析出的总经理正好是申请人自己，都返回空字符串，转候选组兜底。
func (e *CustomProcessEngine) resolveGmChainAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string {
	if requester == nil {
		return ""
	}
	approvers, err := approver.NewPersonalManagerResolver().Resolve(ctx, e.client, &approver.ApproverContext{
		TenantID:    instance.TenantID,
		RequesterID: requester.ID,
	})
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"审批任务未在申请人汇报链上解析到总经理，转候选组兜底",
			"requesterID", requester.ID, "error", err,
		)
		return ""
	}
	gm := approvers[0]
	if gm.UserID == requester.ID {
		e.logger.Infow(
			"汇报链解析出的总经理是申请人本人，转候选组兜底，避免自己审批自己",
			"requesterID", requester.ID,
		)
		return ""
	}
	return strconv.Itoa(gm.UserID)
}
```

在 `createUserTask` 的 switch 语句（第 816-862 行）里，`case task.AssigneeDeptId != 0 || task.AssigneeTeamId != 0 || task.AssigneeProjectId != 0 || task.AssigneeTempTeamId != 0:` 这个 case（第 849-858 行）后面、`default:` 前面加一个新 case：

```go
			case task.AssigneeGmChain:
				// 沿申请人自己的真实汇报链找总经理，矩阵组织下天然按人（业务线）区分，
				// 跟上面的固定组织范围路由是两种不同语义，互斥（BPMN 设计器保证不会
				// 同时声明 assigneeDeptId 和 assigneeGmChain）。
				assignee = e.resolveGmChainAssignee(ctx, instance, approvalRequester)
				if assignee == "" {
					assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
				}
```

- [ ] **Step 5: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w service/bpmn_types.go service/bpmn_process_engine.go service/bpmn_process_engine_test.go
go build ./... 2>&1 | grep -v "permission denied"
go test ./service/... -run TestCreateUserTask_AssigneeGmChain -v
```
Expected: `go build` 无输出；两个新测试 PASS。

- [ ] **Step 6: 跑整个 service 包确认没有破坏既有用例**

Run:
```bash
go test ./service/... ./service/approver/... 2>&1 | grep -Ev "^ok|permission denied|no test files"
```
Expected: 无 FAIL。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/service/bpmn_types.go itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_engine_test.go
git commit -m "$(cat <<'EOF'
feat(bpmn): 接入 PersonalManagerResolver，新增 assigneeGmChain 路由方式

BPMN UserTask 新增 assigneeGmChain 属性，声明后走"沿申请人自己汇报链
找总经理"这条路径，跟 assigneeDeptId 等固定组织范围路由互斥。解决
矩阵组织下同一个部门节点多个平级总经理、部门维度解析无法唯一定位的问题。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: BPMN 设计器 UI 暴露 `assigneeGmChain`

**Files:**
- Modify: `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`
- Modify: `itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts`
- Modify: `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx`

**Interfaces:**
- 不新增/修改任何后端接口——Task 4 已经让引擎支持 `assigneeGmChain`，本任务只是把这个能力接到设计器 UI 上。

- [ ] **Step 1: moddle descriptor 声明 `assigneeGmChain`**

`itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts` 里：
```ts
      { name: 'assigneeDeptId', isAttr: true, type: 'Integer' },
      { name: 'candidateUsers', isAttr: true, type: 'String' },
```
改成：
```ts
      { name: 'assigneeDeptId', isAttr: true, type: 'Integer' },
      { name: 'assigneeGmChain', isAttr: true, type: 'Boolean' },
      { name: 'candidateUsers', isAttr: true, type: 'String' },
```

- [ ] **Step 2: 补断言**

`itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts` 里：
```ts
      'assignee', 'assigneeRole', 'assigneeDeptId', 'candidateUsers', 'candidateGroups', 'taskPurpose',
```
改成：
```ts
      'assignee', 'assigneeRole', 'assigneeDeptId', 'assigneeGmChain', 'candidateUsers', 'candidateGroups', 'taskPurpose',
```

- [ ] **Step 3: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-frontend
npx jest src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts
```
Expected: PASS。

- [ ] **Step 4: `WorkflowNodeInspector.tsx` 加派生状态**

在（第 258 行附近）：
```ts
  const currentAssigneeDeptId = bo.assigneeDeptId ? Number(bo.assigneeDeptId) : undefined;
```
后面加一行：
```ts
  const currentAssigneeGmChain = Boolean(bo.assigneeGmChain);
```

- [ ] **Step 5: 加 UI 控件，四者互斥**

把现有 assignee/assigneeRole 的 `onChange`（第 600、628 行）都补上清空 `assigneeGmChain`：

第 600 行：
```tsx
                onChange={value => apply({ assignee: value || '', assigneeRole: '', assigneeDeptId: undefined })}
```
改成：
```tsx
                onChange={value => apply({ assignee: value || '', assigneeRole: '', assigneeDeptId: undefined, assigneeGmChain: undefined })}
```

第 628 行：
```tsx
                onChange={value => apply({ assigneeRole: value || '', assignee: '', assigneeDeptId: undefined })}
```
改成：
```tsx
                onChange={value => apply({ assigneeRole: value || '', assignee: '', assigneeDeptId: undefined, assigneeGmChain: undefined })}
```

"固定部门审批人"这块（第 642-671 行）的 `onChange`（第 657-659 行）：
```tsx
                onChange={value =>
                  apply({ assigneeDeptId: value ?? undefined, assignee: '', assigneeRole: '' })
                }
```
改成：
```tsx
                onChange={value =>
                  apply({ assigneeDeptId: value ?? undefined, assignee: '', assigneeRole: '', assigneeGmChain: undefined })
                }
```

在"固定部门审批人"这个 `<div className="mt-3">...</div>` 块（第 646-671 行）后面、"Candidate Users" 注释（第 673 行）前面，插入新控件：

```tsx
            {/* Assignee GM Chain — 沿申请人自己的真实汇报链（user.manager_id）找总经理，
                跟"固定部门审批人"的区别是：这里按提交人自己是谁给出不同人（矩阵组织下同一个
                部门节点常年并存多条业务线的平级总经理，固定部门审批人对所有提交人给出同一个
                answer，无法区分业务线；这里天然按人区分）。跟前三者互斥。 */}
            <div className="mt-3">
              <Text strong className="text-sm flex items-center mb-2">
                <Shield className="w-3.5 h-3.5 mr-1" />
                总经理审批（个人汇报链） (assigneeGmChain)
                <Tag color="green" className="ml-2 text-xs">矩阵组织</Tag>
              </Text>
              <Switch
                checked={currentAssigneeGmChain}
                onChange={checked =>
                  apply({ assigneeGmChain: checked || undefined, assignee: '', assigneeRole: '', assigneeDeptId: undefined })
                }
              />
              <Text type="secondary" className="text-xs mt-1 block">
                沿提交人自己的汇报链向上找职位头衔带"总经理"的人；适合矩阵组织（同一分公司/部门下有多条业务线各自的总经理），跟"固定部门审批人"互斥
              </Text>
            </div>

            {/* Candidate Users */}
```

确认文件顶部已经 import 了 `Switch`（Ant Design 组件）——如果没有，在现有的 antd import 语句里补上（文件里已经在用 `Switch` 处理 `allowDelegate`/`allowAddApprover` 等开关，第 552-554 行，大概率已经 import 过）。

- [ ] **Step 6: 类型检查 + lint**

Run:
```bash
cd /home/administrator/project/itsm/itsm-frontend
npm run type-check 2>&1 | tail -60
npx eslint src/components/workflow/designer/WorkflowNodeInspector.tsx src/components/workflow/itsm-moddle-descriptor.ts 2>&1 | tail -60
```
Expected: 两条命令都无错误输出。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts \
        itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts \
        itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx
git commit -m "$(cat <<'EOF'
feat(bpmn-designer): 暴露"总经理审批（个人汇报链）" (assigneeGmChain)

跟"固定部门审批人"并列、互斥。矩阵组织下同一个部门节点常年并存多条
业务线的平级总经理，固定部门审批人对所有提交人给出同一个 answer，
这个新选项按提交人自己的真实汇报链天然区分业务线。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 隔离 14 个产品默认种子部门

**Files:** 无代码改动，纯数据操作 + 验证。

- [ ] **Step 1: 再次确认零引用（防止两次会话之间数据发生变化）**

Run:
```bash
export PGPASSWORD=dev123
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
SELECT
  (SELECT count(*) FROM users WHERE department_id BETWEEN 1 AND 14) AS user_ref,
  (SELECT count(*) FROM tickets WHERE department_id BETWEEN 1 AND 14 OR department_tickets BETWEEN 1 AND 14) AS ticket_ref;
"
```
Expected: `user_ref` 和 `ticket_ref` 都是 0。如果不是 0，停下来跟人类确认，不要执行下一步。

- [ ] **Step 2: 打标记**

Run:
```bash
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
UPDATE departments
SET description = '[产品默认种子部门-非客户组织] ' || COALESCE(description, '')
WHERE id BETWEEN 1 AND 14 AND tenant_id = 1 AND description NOT LIKE '[产品默认种子部门-非客户组织]%';
"
```
Expected: `UPDATE 14`（如果重复执行这一步，`WHERE` 里的 `NOT LIKE` 保证第二次跑是 `UPDATE 0`，不会重复叠加前缀）。

- [ ] **Step 3: 验证**

Run:
```bash
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
SELECT id, name, description FROM departments WHERE id BETWEEN 1 AND 14 ORDER BY id;
"
```
Expected: 14 行的 `description` 都以 `[产品默认种子部门-非客户组织]` 开头。

(此任务无代码改动，不需要 commit。)

---

## Task 7: 端到端验证（"Copilot 采购申请"场景）

**Files:** 无代码改动，纯验证。依赖 Task 1-6 全部完成。

- [ ] **Step 1: 重启 backend/frontend**

Run:
```bash
pkill -f "itsm-backend-dev" 2>/dev/null; pkill -f "next dev --port 3010" 2>/dev/null; sleep 1
cd /home/administrator/project/itsm/itsm-backend
go build -o ../.pids/itsm-backend-dev main.go
DB_HOST=localhost DB_PORT=5432 DB_USER=itsm_user DB_PASSWORD=dev123 DB_NAME=itsm DB_SSLMODE=disable \
REDIS_HOST=localhost REDIS_PORT=6389 REDIS_PASSWORD= REDIS_DB=0 \
JWT_SECRET=dev-secret-key-change-in-production-minimum-32-characters-long \
GIN_MODE=debug SERVER_MODE=debug ENV=development LOG_LEVEL=debug DEPLOYMENT_MODE=private \
MINIO_ENDPOINT=http://localhost:9012 MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin123 MINIO_BUCKET=itsm-uploads \
ITSM_AUTO_MIGRATE=false ITSM_AUTO_SEED=false \
setsid nohup ../.pids/itsm-backend-dev > ../logs/backend.log 2>&1 < /dev/null &
disown
cd /home/administrator/project/itsm/itsm-frontend
ITSM_BACKEND_URL=http://localhost:8090 NODE_ENV=development \
setsid nohup ./node_modules/.bin/next dev --port 3010 > ../logs/frontend.log 2>&1 < /dev/null &
disown
sleep 6
curl -s http://localhost:8090/api/v1/health
curl -s http://localhost:3010/api/health
```
Expected: 两个健康检查都返回 `{"status":"ok",...}`。

- [ ] **Step 2: 找一个真实的矩阵场景测试账号**

从「嘉顺达物流有限公司」子树里挑一个提交人和它自己的汇报链，用 SQL 直接确认（不要凭空造测试账号，用真实回填后的数据验证）：
```bash
export PGPASSWORD=dev123
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
SELECT u.id, u.username, u.name, u.job_title, u.manager_id, m.name AS manager_name, m.job_title AS manager_job_title
FROM users u LEFT JOIN users m ON u.manager_id = m.id
WHERE u.tenant_id=1 AND u.department_id IN (
  SELECT id FROM departments WHERE code='11D030304' -- 北京分公司
) AND u.job_title IS NOT NULL AND u.job_title != ''
LIMIT 5;
"
```
从结果里选一个 `manager_job_title` 不含"总经理"的人（说明它的直属上级只是团队负责人，还需要 `PersonalManagerResolver` 继续往上爬），记下它的 `username`，登录时用它。

- [ ] **Step 3: 用 BPMN 设计器把"Copilot 采购申请"流程的"总经理审批"节点改成 `assigneeGmChain`**

打开 `http://localhost:3010/workflow/designer`，找到（或新建）"Copilot 采购申请"流程定义，把"总经理审批"这个 UserTask 节点的路由方式切换成 Task 5 新加的"总经理审批（个人汇报链）"开关。保存后重新打开，确认 BPMN XML 里 `assigneeGmChain="true"` 属性正确持久化（没有在导出/重新导入时丢失）。

- [ ] **Step 4: 提交申请，验证审批人解析正确**

用 Step 2 选定的测试账号登录，提交一个"Copilot 采购申请"。查询这个流程实例的"总经理审批"任务：
```bash
psql -h localhost -p 5432 -U itsm_user -d itsm -c "
SELECT pt.task_name, pt.assignee, u.name, u.job_title
FROM process_tasks pt LEFT JOIN users u ON pt.assignee = u.id::text
WHERE pt.tenant_id=1 AND pt.task_name LIKE '%总经理%'
ORDER BY pt.created_time DESC LIMIT 1;
"
```
Expected: `assignee` 解析出的 `u.job_title` 包含"总经理"字样，并且是 Step 2 里那个提交人自己汇报链上的人（不是同一部门节点下其他业务线的总经理）。

- [ ] **Step 5: 验证"IT 总监审批"环节不受影响**

确认流程走到"IT 总监审批"这一步时，候选人仍然是拥有 `it_director` 角色的用户（沿用 [[2026-08-19-org-role-gm-modeling-design]] 已验证过的 `assigneeRole` 机制，本次未改动这部分）。

- [ ] **Step 6: 向人类报告验证结果**

汇总以上 5 步的结果（通过/不通过 + 关键 SQL 查询结果或截图）。本任务无代码改动，不需要提交。

---

## Self-Review 记录

- **Spec 覆盖**：spec 的"设计一"（`job_title` 字段）对应 Task 1+2；"设计二"（`PersonalManagerResolver`）对应 Task 3+4+5；"设计三"（14 个种子部门隔离）对应 Task 6；"测试计划"里的"后端"对应 Task 3/4 的单测，"数据"对应 Task 2 的 dry-run/正式执行验证，"端到端"对应 Task 7。spec"风险与未决问题"里提到的"职位头衔关键字匹配精度"（宽松匹配"总经理"子串，不区分正副/助理）已经在 Task 3 的 resolver 实现里落实为 `strings.Contains`；"750 个断链根节点"的风险在 Task 3 的 `TestPersonalManagerResolver_Resolve_NoGMInChain` 用例里覆盖了"爬到链路顶端仍未命中"的行为（返回 error，调用方 Task 4 的 `resolveGmChainAssignee` 会转空串走候选组兜底）。
- **占位符扫描**：全文没有 TBD/TODO，所有代码块都是可直接使用的完整内容。
- **类型一致性**：`ApproverContext.RequesterID`（Task 3 使用，已有字段，未新增）与 `resolveGmChainAssignee`（Task 4）构造 `&approver.ApproverContext{TenantID: ..., RequesterID: requester.ID}` 的字段名一致；`PersonalManagerResolver.GetType()` 返回值 `"personal_manager_gm"` 与 `ApproverInfo.Role` 字段（Task 3 resolver 实现里）一致；`BPMNUserTask.AssigneeGmChain bool`（Task 4）与前端 `assigneeGmChain` moddle 属性（Task 5，`type: 'Boolean'`）在 XML 序列化层面类型匹配；`currentAssigneeGmChain`（Task 5）与 `bo.assigneeGmChain`（bpmn-js business object 上的运行时属性名）命名一致。
