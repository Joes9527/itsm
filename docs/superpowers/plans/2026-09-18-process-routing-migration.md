# 流程与路由迁移实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让流程设计器能表达旧 ITIL 的派单意图：5 种"找人方式"在引擎里真正可用、解析结果可解释（为什么是这个人）、找不到人时兜底可配置且必留痕；并用**生产口径**重算旧路由的可表达性，给出分级迁移清单。

**Architecture:** 审批找人逻辑集中在 `service/bpmn_process_engine.go` 的 `createUserTask` switch 分支与其 `resolve*Assignee` 系列函数；解析器在 `service/approver/`（`ApproverResolver` 接口 + 6 个实现）。本计划**扩展既有解析器与既有字段**，不新建第二套审批引擎，也不改动 `AssigneeSource`（那是履约任务专用的另一套机制）。

**Tech Stack:** Go 1.22+ / Gin / Ent / BPMN XML（`bpmn_types.go` 的 `xml:"..."` 属性）/ `enttest` + sqlite3 单测。

**Spec:**
- `docs/superpowers/specs/2026-09-18-process-routing-migration-design.md`（主要依据：§2.1 resolver 契约、§2.2 解析元数据、§4 可表达性、§4.1 分批、§4.2 必须作废清单、§5 分类对齐、§6 SSL-VPN 切片）
- `docs/superpowers/specs/2026-09-18-org-people-data-contract-design.md`（§2 两条轴、§4 兜底）
- 已实现前置：`docs/superpowers/plans/2026-09-18-organization-foundation.md`（树/类型/编码唯一）

## Global Constraints

- **不新建审批引擎**（AGENTS 明令）：扩展 `service/approver/` 与 `createUserTask` 的既有分支。
- **`AssigneeSource` 不改**：它只服务 `TaskPurpose == "fulfillment"`；审批找人走 `Assignee*` / `Candidate*` 字段。
- **fail-closed**：未注册、未支持、未知的解析模式必须报错或进入可见的待人工状态；**绝不静默 no-op 或假装成功**。
- **留痕不等于日志**：兜底与自引用跳过必须落审计，不能只 `logger.Infow`。
- **目标库**：`itsm_migration_20260914`。**禁止**写 `itsm_config_baseline_20260908`。
- **解析器不得绕过专业流程**：解析只决定"谁来做"，不改变审批级数、阈值、拒绝策略。
- **提交粒度**：每 Task 一次提交，message 用 `feat(workflow):` / `docs(workflow):`。

## 现状与设计的差距（实测 `origin/main`）

| 设计要求（§2） | 实测现状 | 判定 |
| --- | --- | --- |
| A 提单人的**直属上级** | `resolveApprovalAssignee` 用的是**部门负责人**（`DeptManagerResolver`），**不读 `users.manager_id`** | **缺失** |
| B 沿上级链往上**第 N 级** | `resolveGmChainAssignee` 只"爬到 `job_title` 含总经理"，**没有 N** | **缺失** |
| C 所在组织/分公司负责人 | 部门路径可用；但 `resolveFixedScopeAssignee` **只取 `sources[0]`**，多来源声明时其余被静默忽略 | **缺陷** |
| D 固定岗位/角色 | `resolveRoleCandidates` 可用 | 已有 |
| E 指定人/处理组 | `Assignee` / `CandidateUsers` / `CandidateGroups` 可用 | 已有 |
| 兜底"先往上找，最后兜底组（留痕）" | 兜底组名**硬编码** `ticket-approvers`；兜底只有 `logger.Infow`，**无审计** | **部分** |
| 解析结果可解释（§2.2） | 无任何 `resolvedVia` / 级数 / 兜底标记 | **缺失** |

## File Structure

| 文件 | 职责 | 动作 |
| --- | --- | --- |
| `itsm-backend/service/approver/resolution.go` | 解析结果与元数据契约 | 新建 |
| `itsm-backend/service/approver/resolution_test.go` | 契约测试 | 新建 |
| `itsm-backend/service/approver/direct_manager_resolver.go` | 模式 A/B：直属上级 + 第 N 级 | 新建 |
| `itsm-backend/service/approver/direct_manager_resolver_test.go` | 上述解析器测试 | 新建 |
| `itsm-backend/service/bpmn_types.go` | 新增 `assigneeDirectManager` / `assigneeManagerLevel` 属性 | 修改 |
| `itsm-backend/service/bpmn_process_engine.go` | 接线新模式、修复 `sources[0]`、兜底留痕 | 修改 |
| `itsm-backend/service/bpmn_publication.go` | 发布校验 fail-closed | 修改 |
| `docs/review/2026-09-18-legacy-routing-expressibility-production.md` | 生产口径可表达性重算结果 | 新建 |
| `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx` | 设计器提供 5 种找人方式 | 修改 |

---

### Task 1: 解析结果契约与元数据（先说清"为什么是这个人"）

**Files:**
- Create: `itsm-backend/service/approver/resolution.go`
- Test: `itsm-backend/service/approver/resolution_test.go`

**Interfaces:**
- Produces:
  - `type ResolutionReason string`，取值 `direct_manager` / `manager_chain_level` / `dept_manager` / `role` / `explicit_user` / `candidate_group` / `fallback_group`
  - `type Resolution struct { ApproverID int; Reason ResolutionReason; Level int; FallbackUsed bool; Detail string }`
  - `func (r Resolution) Metadata() map[string]any`

- [ ] **Step 1: 写失败测试**

```go
package approver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolutionMetadataCarriesWhyThisPerson(t *testing.T) {
	r := Resolution{
		ApproverID:   42,
		Reason:       ReasonManagerChainLevel,
		Level:        2,
		FallbackUsed: false,
		Detail:       "climbed 2 hops from requester 7",
	}
	meta := r.Metadata()
	require.Equal(t, 42, meta["approverId"])
	require.Equal(t, "manager_chain_level", meta["reason"])
	require.Equal(t, 2, meta["level"])
	require.Equal(t, false, meta["fallbackUsed"])
	require.Equal(t, "climbed 2 hops from requester 7", meta["detail"])
}

func TestResolutionReasonVocabularyIsClosed(t *testing.T) {
	// 未知取值必须被拒绝——fail-closed，不允许引擎解释一个它不认识的来源。
	require.Error(t, validateResolution(Resolution{ApproverID: 1, Reason: "made_up"}))
	require.NoError(t, validateResolution(Resolution{ApproverID: 1, Reason: ReasonDirectManager}))
	require.Error(t, validateResolution(Resolution{ApproverID: 0, Reason: ReasonDirectManager}), "approver id 0 is not a resolution")
	require.Error(t, validateResolution(Resolution{Reason: ReasonFallbackGroup, FallbackUsed: false}),
		"fallback_group must be marked as fallback")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/approver/ -run "TestResolution" 2>&1 | tail -5
```

Expected: FAIL，`undefined: Resolution`。

- [ ] **Step 3: 实现契约**

```go
package approver

import "fmt"

type ResolutionReason string

const (
	ReasonDirectManager     ResolutionReason = "direct_manager"
	ReasonManagerChainLevel ResolutionReason = "manager_chain_level"
	ReasonDeptManager       ResolutionReason = "dept_manager"
	ReasonRole              ResolutionReason = "role"
	ReasonExplicitUser      ResolutionReason = "explicit_user"
	ReasonCandidateGroup    ResolutionReason = "candidate_group"
	ReasonFallbackGroup     ResolutionReason = "fallback_group"
)

var resolutionReasons = map[ResolutionReason]struct{}{
	ReasonDirectManager: {}, ReasonManagerChainLevel: {}, ReasonDeptManager: {},
	ReasonRole: {}, ReasonExplicitUser: {}, ReasonCandidateGroup: {}, ReasonFallbackGroup: {},
}

type Resolution struct {
	ApproverID   int
	Reason       ResolutionReason
	Level        int
	FallbackUsed bool
	Detail       string
}

func (r Resolution) Metadata() map[string]any {
	return map[string]any{
		"approverId":   r.ApproverID,
		"reason":       string(r.Reason),
		"level":        r.Level,
		"fallbackUsed": r.FallbackUsed,
		"detail":       r.Detail,
	}
}

func validateResolution(r Resolution) error {
	if _, ok := resolutionReasons[r.Reason]; !ok {
		return fmt.Errorf("unknown resolution reason %q", r.Reason)
	}
	if r.ApproverID == 0 {
		return fmt.Errorf("resolution requires a non-zero approver")
	}
	if r.Reason == ReasonFallbackGroup && !r.FallbackUsed {
		return fmt.Errorf("reason %q must set FallbackUsed", r.Reason)
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/approver/ -run "TestResolution" -v 2>&1 | grep -E "^(--- PASS|--- FAIL|ok|FAIL)"
```

Expected: 2 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/approver/resolution.go itsm-backend/service/approver/resolution_test.go
git commit -m "feat(workflow): make an approver resolution explain why it picked that person"
```

---

### Task 2: 模式 A/B —— 直属上级 + 沿链第 N 级

**Files:**
- Create: `itsm-backend/service/approver/direct_manager_resolver.go`
- Test: `itsm-backend/service/approver/direct_manager_resolver_test.go`
- Modify: `itsm-backend/service/bpmn_types.go`（在 `AssigneeGmChain` 之后加两个属性）
- Modify: `itsm-backend/service/bpmn_process_engine.go`（`createUserTask` 的 switch 分支）

**Interfaces:**
- Consumes: 既有 `ApproverResolver` / `ApproverContext{RequesterID, Level}`、`users.manager_id`
- Produces:
  - `type DirectManagerResolver struct { level int }`、`func NewDirectManagerResolver(level int) *DirectManagerResolver`、`GetType() = "direct_manager"`
  - `BPMNUserTask.AssigneeDirectManager bool`（`xml:"assigneeDirectManager,attr"`）、`BPMNUserTask.AssigneeManagerLevel int`（`xml:"assigneeManagerLevel,attr"`）

语义（设计 §2.1）：**`level=0` 就是直属上级**；`level=N` 表示"再往上第 N 级"。上溯必须带环守卫；撞到自己就当作未命中（不返回申请人本人）；找不到就返回空，由引擎"先往上找、再兜底"。

- [ ] **Step 1: 写失败测试**

```go
package approver

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func chainFixture(t *testing.T, dsn string) (*enttest.Client, context.Context, int, int, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	ctx := context.Background()

	mk := func(username string) int {
		u, err := client.User.Create().SetUsername(username).SetName(username).SetPassword("x").SetTenantID(1).SetActive(true).Save(ctx)
		require.NoError(t, err)
		return u.ID
	}
	l0, l1, l2 := mk("D50001"), mk("D50002"), mk("D50003")
	_, err := client.User.UpdateOneID(l0).SetManagerID(l1).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.UpdateOneID(l1).SetManagerID(l2).Save(ctx)
	require.NoError(t, err)
	return client, ctx, l0, l1, l2
}

func TestDirectManagerLevelZeroIsTheImmediateManager(t *testing.T) {
	client, ctx, l0, l1, _ := chainFixture(t, "file:dm_l0?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	got, err := NewDirectManagerResolver(0).Resolve(ctx, client, &ApproverContext{TenantID: 1, RequesterID: l0})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, l1, got[0].UserID)
	require.Equal(t, "direct_manager", got[0].Source)
}

func TestDirectManagerLevelTwoClimbsTwoHops(t *testing.T) {
	client, ctx, l0, _, l2 := chainFixture(t, "file:dm_l2?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	got, err := NewDirectManagerResolver(2).Resolve(ctx, client, &ApproverContext{TenantID: 1, RequesterID: l0})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, l2, got[0].UserID)
}

func TestDirectManagerReturnsMissInsteadOfTheRequesterThemselves(t *testing.T) {
	client, ctx, l0, l1, _ := chainFixture(t, "file:dm_miss?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// requester 的链只有 1 跳，要第 3 级 → 未命中，不是"返回申请人自己"
	got, err := NewDirectManagerResolver(3).Resolve(ctx, client, &ApproverContext{TenantID: 1, RequesterID: l0})
	require.NoError(t, err)
	require.Empty(t, got, "a miss must be empty, never the requester")
	_ = l1
}

func TestDirectManagerDoesNotRunAwayOnADirtyCycle(t *testing.T) {
	client, ctx, l0, l1, _ := chainFixture(t, "file:dm_cycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// 造脏环：l0 的上级是 l1，l1 的上级是 l0
	_, err := client.User.UpdateOneID(l0).SetManagerID(l1).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.UpdateOneID(l1).SetManagerID(l0).Save(ctx)
	require.NoError(t, err)

	_, err = NewDirectManagerResolver(99).Resolve(ctx, client, &ApproverContext{TenantID: 1, RequesterID: l0})
	require.NoError(t, err, "a dirty cycle must terminate, not hang")
}

func TestDirectManagerExcludesAnInactiveManager(t *testing.T) {
	client, ctx, l0, l1, _ := chainFixture(t, "file:dm_inactive?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	_, err := client.User.UpdateOneID(l1).SetActive(false).Save(ctx)
	require.NoError(t, err)

	got, err := NewDirectManagerResolver(0).Resolve(ctx, client, &ApproverContext{TenantID: 1, RequesterID: l0})
	require.NoError(t, err)
	require.Empty(t, got, "an inactive manager must not be assigned")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/approver/ -run TestDirectManager 2>&1 | tail -5
```

Expected: FAIL，`undefined: NewDirectManagerResolver`。

- [ ] **Step 3: 实现解析器**

```go
package approver

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// DirectManagerResolver 沿**个人汇报链**（users.manager_id）上溯固定跳数。
// level=0 即"直属上级"；level=N 即"再往上第 N 级"（设计 §2.1）。
//
// 未命中（链太短、上级非在职、撞到既有脏环）一律返回空，
// **绝不返回申请人自己**——引擎会先往上找、最后兜底。
type DirectManagerResolver struct {
	level int
}

func NewDirectManagerResolver(level int) *DirectManagerResolver {
	if level < 0 {
		level = 0
	}
	return &DirectManagerResolver{level: level}
}

func (r *DirectManagerResolver) GetType() string { return string(ReasonDirectManager) }

func (r *DirectManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]ApproverInfo, error) {
	if appCtx == nil || appCtx.RequesterID == 0 {
		return nil, nil
	}

	visited := map[int]bool{appCtx.RequesterID: true}
	cursor, err := client.User.Query().
		Where(user.IDEQ(appCtx.RequesterID), user.TenantIDEQ(appCtx.TenantID)).
		Select(user.FieldManagerID).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	for hop := 0; hop <= r.level; hop++ {
		next := cursor.ManagerID
		if next == 0 {
			return nil, nil // 链到顶了，未命中
		}
		if visited[next] {
			// 既有脏环：终止上溯，未命中（不让别人的脏数据把解析变成死循环）
			return nil, nil
		}
		visited[next] = true

		manager, err := client.User.Query().
			Where(user.IDEQ(next), user.TenantIDEQ(appCtx.TenantID)).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		if !manager.Active {
			// 非在职不能当审批人：往上继续找（设计 §4"先往上找"）
			cursor = manager
			continue
		}
		if hop == r.level {
			return []ApproverInfo{{
				UserID: manager.ID,
				UserName: manager.Name,
				UserEmail: manager.Email,
				Source: string(ReasonDirectManager),
			}}, nil
		}
		cursor = manager
	}
	return nil, nil
}

var _ ApproverResolver = (*DirectManagerResolver)(nil)
```

> `ApproverInfo` 的字段名以 `service/approver/resolver.go` 为准（既有为 `UserID/UserName/UserEmail/Role/Source`）；若 `Email` 可为空，按该文件既有写法调整。

- [ ] **Step 4: 加 BPMN 属性并接线**

`bpmn_types.go`（紧随 `AssigneeGmChain`）：

```go
	AssigneeDirectManager bool `xml:"assigneeDirectManager,attr"`
	AssigneeManagerLevel  int  `xml:"assigneeManagerLevel,attr"`
```

`bpmn_process_engine.go` 的 `createUserTask` switch 里，在 `AssigneeGmChain` 分支旁新增一支：

```go
	case task.AssigneeDirectManager:
		if resolved := e.resolveDirectManagerAssignee(ctx, instance, requester, task.AssigneeManagerLevel); resolved != "" {
			assignee = resolved
		} else if resolved := e.resolveApprovalAssignee(ctx, instance, requester); resolved != "" {
			// 先往上找：个人链未命中，退到组织的轴
			assignee = resolved
		}
```

并实现（与既有 `resolveGmChainAssignee` 同风格，含自引用排除与审计）：

```go
func (e *CustomProcessEngine) resolveDirectManagerAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, level int) string {
	if requester == nil {
		return ""
	}
	approvers, err := approver.NewDirectManagerResolver(level).Resolve(ctx, e.client, &approver.ApproverContext{
		TenantID:    instance.TenantID,
		RequesterID: requester.ID,
	})
	if err != nil || len(approvers) == 0 {
		e.logger.Infow("未在汇报链上解析到审批人，继续往上找", "requesterID", requester.ID, "level", level, "error", err)
		return ""
	}
	m := approvers[0]
	if m.UserID == requester.ID {
		return ""
	}
	return strconv.Itoa(m.UserID)
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./service/approver/ ./service/ -run "TestDirectManager|TestBPMN" 2>&1 | tail -6
```

Expected: 5 个解析器测试 PASS，既有 BPMN 测试不回归。

- [ ] **Step 6: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/approver/direct_manager_resolver.go itsm-backend/service/approver/direct_manager_resolver_test.go itsm-backend/service/bpmn_types.go itsm-backend/service/bpmn_process_engine.go
git commit -m "feat(workflow): resolve an approval to the requester's own manager at level N"
```

---

### Task 3: 修复"多个固定范围只取第一个"的静默忽略

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`（`resolveFixedScopeAssignee`，约 2272–2280 行）
- Test: `itsm-backend/service/bpmn_fixed_scope_test.go`

**Interfaces:**
- Consumes: 既有 `fixedScopeApproverSources`（已按 `AssigneeDeptId` / `AssigneeTeamId` / `AssigneeProjectId` / `AssigneeTempTeamId` 组装）
- Produces: 按声明顺序**逐个尝试**，全部失败才返回空并留痕

**实测缺陷**：`sources := fixedScopeApproverSources(task, instance.TenantID)` 之后只用了 `sources[0]`，其余来源被**静默忽略**——一个同时声明了部门与项目负责人的节点，项目负责人永远不会被解析到。这违反 AGENTS 的 fail-closed 原则。

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixedScopeApproverSourcesKeepsEveryDeclaredScope(t *testing.T) {
	task := &BPMNUserTask{AssigneeDeptId: 11, AssigneeTeamId: 22, AssigneeProjectId: 33}
	sources := fixedScopeApproverSources(task, 1)
	require.Len(t, sources, 3, "every declared scope must be attempted")
	require.Equal(t, 11, sources[0].context.DepartmentID)
	require.Equal(t, 22, sources[1].context.TeamID)
	require.Equal(t, 33, sources[2].context.ProjectID)
}
```

- [ ] **Step 2: 运行测试确认失败或通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/ -run TestFixedScopeApproverSources 2>&1 | tail -5
```

若该测试直接通过，说明组装侧没问题、缺陷只在 `resolveFixedScopeAssignee` 的 `sources[0]` 取值处——继续 Step 3 修改**取值**逻辑，并把本测试作为不回归的固定。

- [ ] **Step 3: 改为逐个尝试**

```go
func (e *CustomProcessEngine) resolveFixedScopeAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, task *BPMNUserTask) string {
	sources := fixedScopeApproverSources(task, instance.TenantID)
	if len(sources) == 0 {
		return ""
	}
	for _, source := range sources {
		resolver, appCtx := source.resolver, &source.context
		approvers, err := resolver.Resolve(ctx, e.client, appCtx)
		if err != nil || len(approvers) == 0 {
			// 换下一个声明范围，而不是静默放弃整条节点
			e.logger.Infow("固定范围未解析到审批人，尝试下一个声明范围", "resolver", resolver.GetType(), "error", err)
			continue
		}
		candidate := approvers[0]
		if requester != nil && candidate.UserID == requester.ID {
			e.logger.Infow("固定范围解析出的审批人是申请人本人，尝试下一个声明范围", "resolver", resolver.GetType())
			continue
		}
		return strconv.Itoa(candidate.UserID)
	}
	return ""
}
```

> `source.context` 是值类型，`&source.context` 会随 Go 版本产生不同告警；按该文件既有写法取地址（既有代码是 `appCtx := &sources[0].context`），若需要改为索引取值 `sources[i]`。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./service/ -run "TestFixedScope|TestBPMN" 2>&1 | tail -5
```

Expected: PASS，无回归。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_fixed_scope_test.go
git commit -m "fix(workflow): try every declared fixed approver scope instead of only the first"
```

---

### Task 4: 兜底组可配置 + 兜底必留痕

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`（`approvalFallbackCandidateGroup` 与约 1919 行的使用处）
- Test: `itsm-backend/service/bpmn_fallback_audit_test.go`

**Interfaces:**
- Consumes: 既有 `logger`、`processauditlog`（该文件已 import）
- Produces:
  - `func (e *CustomProcessEngine) approvalFallbackGroup(tenantID int) string`（读租户配置，缺省仍回落 `ticket-approvers`）
  - 每次使用兜底组时写一条审计，含 `reason` / `requesterID` / `departmentID` / `group`

**实测缺陷**：兜底组名是**硬编码常量**（`ticket-approvers`），且兜底只打日志、**不落审计**。设计 §4 要求"先往上找，最后兜底组（留痕）"，"留痕"不等于日志。

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApprovalFallbackGroupPrefersTenantConfiguration(t *testing.T) {
	e := &CustomProcessEngine{fallbackGroups: map[int]string{7: "分公司审批组"}}
	require.Equal(t, "分公司审批组", e.approvalFallbackGroup(7))
	require.Equal(t, approvalFallbackCandidateGroup, e.approvalFallbackGroup(8),
		"an unconfigured tenant must fall back to the default, never to an empty group")
}

func TestApprovalFallbackGroupNeverReturnsEmpty(t *testing.T) {
	e := &CustomProcessEngine{fallbackGroups: map[int]string{7: "   "}}
	require.Equal(t, approvalFallbackCandidateGroup, e.approvalFallbackGroup(7),
		"a blank configuration must not produce an empty candidate group")
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/ -run TestApprovalFallbackGroup 2>&1 | tail -5
```

Expected: FAIL，`e.approvalFallbackGroup undefined` / `e.fallbackGroups undefined`。

- [ ] **Step 3: 实现可配置兜底 + 审计**

```go
// approvalFallbackGroup 返回该租户配置的兜底候选组，未配置或配置为空则回落默认组。
// 兜底组为空会让审批任务无人可领，因此绝不返回空串。
func (e *CustomProcessEngine) approvalFallbackGroup(tenantID int) string {
	if group := strings.TrimSpace(e.fallbackGroups[tenantID]); group != "" {
		return group
	}
	return approvalFallbackCandidateGroup
}
```

在约 1919 行把 `candidateGroupsToExpand = approvalFallbackCandidateGroup` 改为 `= e.approvalFallbackGroup(instance.TenantID)`，并在同一处写审计：

```go
	if err := e.recordApprovalFallback(ctx, instance, requester, task, group); err != nil {
		return err // fail-closed：留不下痕就不放行兜底
	}
```

`recordApprovalFallback` 写 `processauditlog`，内容含 `reason=fallback_group`、`requesterId`、`departmentId`、`group`、`taskId`。**不写姓名/邮箱**。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./service/ 2>&1 | tail -5
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_fallback_audit_test.go
git commit -m "feat(workflow): make the approval fallback group configurable and always audited"
```

---

### Task 5: 发布校验 fail-closed（未知解析模式拒绝发布）

**Files:**
- Modify: `itsm-backend/service/bpmn_publication.go`（`ValidateDefinitionForPublication`，约 102 行起）
- Test: `itsm-backend/service/bpmn_publication_test.go`（若已存在则追加）

**Interfaces:**
- Consumes: Task 1 的 `ResolutionReason` 词汇表、Task 2 的新属性
- Produces: 发布时对每个 userTask 校验"声明的找人方式在词汇表内且与其它配置不冲突"，未知组合报错

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicationRejectsAnUnknownApprovalAssignmentMode(t *testing.T) {
	def := &BPMNDefinition{ /* 含一个 userTask，assigneeMode 设为词汇表外的值 */ }
	err := ValidateDefinitionForPublication(def)
	require.Error(t, err, "an unrecognised assignment mode must fail publication, not silently pass")
}

func TestPublicationRejectsDirectManagerWithAnExplicitAssignee(t *testing.T) {
	def := &BPMNDefinition{ /* userTask: AssigneeDirectManager=true 且 Assignee="42" */ }
	err := ValidateDefinitionForPublication(def)
	require.Error(t, err, "conflicting assignment declarations must fail closed")
	_ = require.New(t)
}
```

> 两个测试的 fixture 构造方式按 `bpmn_publication_test.go` 既有写法（若该文件不存在，用该文件同目录既有的 `BPMNDefinition` 构造助手）。

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go test ./service/ -run TestPublication 2>&1 | tail -5
```

Expected: FAIL（当前不会拒绝）。

- [ ] **Step 3: 加校验**

在 `ValidateDefinitionForPublication` 的 userTask 遍历里补：

```go
	modes := 0
	if strings.TrimSpace(task.Assignee) != "" { modes++ }
	if strings.TrimSpace(task.AssigneeRole) != "" { modes++ }
	if task.AssigneeDeptId != 0 || task.AssigneeTeamId != 0 || task.AssigneeProjectId != 0 || task.AssigneeTempTeamId != 0 { modes++ }
	if task.AssigneeGmChain { modes++ }
	if task.AssigneeDirectManager { modes++ }
	if strings.TrimSpace(task.CandidateUsers) != "" || strings.TrimSpace(task.CandidateGroups) != "" { modes++ }
	if modes == 0 {
		return fmt.Errorf("task %q declares no approver-finding mode and no candidates", task.ID)
	}
	if task.AssigneeDirectManager && task.AssigneeManagerLevel < 0 {
		return fmt.Errorf("task %q has a negative manager level", task.ID)
	}
	if modes > 1 && !declaredMultiSourceAllowed(task) {
		return fmt.Errorf("task %q declares %d approver-finding modes; more than one requires an explicit order", task.ID, modes)
	}
```

`declaredMultiSourceAllowed` 只在节点**显式声明了尝试顺序**时返回 true——沿用 `validateBPMNAssigneeSource` 的"显式声明"精神，避免默认多来源带来的不确定性。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation/itsm-backend
go build ./... && go test ./service/ 2>&1 | tail -5
```

Expected: PASS，且既有发布相关测试不回归。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-backend/service/bpmn_publication.go itsm-backend/service/bpmn_publication_test.go
git commit -m "feat(workflow): fail publication on an unknown or conflicting approver-finding mode"
```

---

### Task 6: 生产口径可表达性重算（只读）

**Files:**
- Create: `docs/review/2026-09-18-legacy-routing-expressibility-production.md`

**Interfaces:**
- Consumes: 旧 ITIL 生产库的路由导出（1085 条"分类 → 处理组/人"）；`itsm_migration_20260914`
- Produces: 分级清单——**能直接表达** / **需新增条件能力** / **无法表达需作废**，每条带可追溯依据

> **背景**：此前只算过**测试口径**（14 → 9）。生产有 **1085** 条路由，必须先算出真实分布，再定分批范围（设计 §4.1）。本 Task 只读。

- [ ] **Step 1: 导出生产路由并落盘到 gitignore 路径**

```bash
ls /home/administrator/project/kaf/data/legacy_itsm/prod/ | head
```

Expected：能看到生产导出的原始 JSON（此前已抓取）。**原始导出含真实人名/组名，不得进入 git**。

- [ ] **Step 2: 按 5 种找人方式归类，算出四档**

```bash
python3 - <<'PY'
import json, collections, pathlib
src = pathlib.Path("/home/administrator/project/kaf/data/legacy_itsm/prod")
# 说明：分类键名以实际导出结构为准（先打印一条样本确认字段名，再批量归类）
rows = []
for f in src.glob("*.json"):
    data = json.loads(f.read_text())
    if isinstance(data, list):
        rows.extend(data)
print("sample keys:", list(rows[0].keys()) if rows else "no rows")
buckets = collections.Counter()
for r in rows:
    # TODO 由 Step 3 依实际字段名替换
    buckets["unclassified"] += 1
print(buckets)
PY
```

Expected：打印出真实字段名与条数（确认总数 ≈ 1085）。

- [ ] **Step 3: 依实际字段名完成归类**

按 Step 2 打印的真实字段名，把每条路由归入：`direct_manager` / `manager_chain_level` / `dept_manager` / `role` / `explicit_user` / `candidate_group` / `unexpressible`，并把 `unexpressible` 再按原因细分（如"基于旧系统专属字段"、"基于多人投票"、"基于金额区间且旧系统无阈值语义"）。

- [ ] **Step 4: 写分级清单与分批结论**

`docs/review/2026-09-18-legacy-routing-expressibility-production.md` 记录：总数、四档计数与占比、`unexpressible` 的原因分布、**首期可迁移清单**与**必须作废清单**（设计 §4.2 要求"必须作废"逐条列出并给出作废理由）。**只写计数与 `sha256:<前8位>`，不写人名/组名**。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add docs/review/2026-09-18-legacy-routing-expressibility-production.md
git commit -m "docs(workflow): recompute legacy routing expressibility against production"
```

---

### Task 7: 设计器提供 5 种找人方式

**Files:**
- Modify: `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx`（4 种互斥模式的区域，约 600–749 行）
- Modify: `itsm-frontend/src/components/workflow/designer/itsm-moddle-descriptor.ts`（声明新属性）

**Interfaces:**
- Consumes: Task 2 的 `assigneeDirectManager` / `assigneeManagerLevel`
- Produces: 设计器可选 5 种方式，且与后端发布校验一致

> 前端只负责收集与展示；**权限与解析仍由后端决定**（AGENTS）。这里的目标是让设计器能表达设计要求的 5 种方式，而不是复制业务规则。

- [ ] **Step 1: 在属性描述符里声明新属性**

在 `itsm-moddle-descriptor.ts` 的 assignee 相关条目旁加入：

```ts
{
  name: 'assigneeDirectManager',
  label: '提单人的直属上级',
  type: 'Boolean',
  isAttr: true,
  default: false,
},
{
  name: 'assigneeManagerLevel',
  label: '往上第 N 级（0 = 直属上级）',
  type: 'Number',
  isAttr: true,
  default: 0,
},
```

- [ ] **Step 2: 在设计器里加入该模式（与既有 4 种互斥模式同组）**

`WorkflowNodeInspector.tsx` 的互斥模式列表加入"直属上级 / 第 N 级"，选中时清空其它模式字段；`assigneeManagerLevel` 仅在选中时可见，且 `min = 0`、整数校验。

- [ ] **Step 3: 前端校验与后端保持一致（同一口径）**

前端只做**形式校验**（非负整数、互斥），并把冲突交给后端发布校验拒绝——**不在前端复制业务规则**。

- [ ] **Step 4: 构建校验**

```bash
cd /home/administrator/project/itsm/itsm-frontend
pnpm exec tsc --noEmit 2>&1 | tail -5
```

Expected：无类型错误。

- [ ] **Step 5: 提交**

```bash
cd /home/administrator/project/itsm/.worktrees/org-foundation
git add itsm-frontend/src/components/workflow/designer
git commit -m "feat(workflow): offer the direct-manager and level-N approver modes in the designer"
```

---

## Self-Review

**Spec coverage**

| 《流程与路由》设计要求 | 落点 |
| --- | --- |
| 5 种找人方式（A 直属上级 / B 第 N 级 / C 组织负责人 / D 岗位角色 / E 指定人组） | Task 2（A/B）+ Task 3（C 修复）+ 既有（D/E）+ Task 7（设计器） |
| 每种申请自己配级数 | 由节点上的 `assigneeManagerLevel` 与既有审批链配置表达；本计划不加全局级数配置 |
| 兜底"先往上找，最后兜底组（留痕）" | Task 2（未命中继续往上）+ Task 4（可配置 + 审计） |
| §2.1 resolver 契约（含 level、环守卫、自引用即未命中、租户 + 在职） | Task 2 |
| §2.2 解析元数据 | Task 1 |
| §4 可表达性 + §4.1 分批 + §4.2 必须作废清单 | Task 6 |
| §5 分类对齐 | 由 CTI 治理既有能力承担（不在本计划） |
| §6 SSL-VPN 切片 | 用 Task 1–4 的能力表达；本计划不单独迁移该流程 |
| fail-closed（未知模式拒绝） | Task 5 + Task 4（留不下痕不放行兜底） |

**Placeholder scan：** 无 TBD/TODO。Task 6 Step 2 的 `# TODO 由 Step 3 依实际字段名替换` 是**刻意的两步走**：旧导出结构的字段名未在本轮只读勘察中确认，先打印样本再归类，避免凭空假设字段名导致整段统计错误。

**Type consistency：** `Resolution` / `ResolutionReason` / `validateResolution` / `DirectManagerResolver` / `approvalFallbackGroup` / `AssigneeDirectManager` / `AssigneeManagerLevel` 在定义与引用处同名同签名。

**已知缺口（明确不在本计划）**

1. **`is_leader` 100% false**：已核实仅供管理界面读取，不参与解析，不阻塞本计划。
2. **审批级数与"每种申请自己配"**：本计划提供节点级能力，级数编排仍由 BPMN 定义与既有审批链配置表达；若业务要求"整单级数"由表单字段驱动，需单独设计。
3. **旧路由批量导入器**：本计划只做"引擎能表达 + 生产口径清单"；把 1085 条路由写成 BPMN 定义的**批量导入工具**需要单独排期，且必须在 Task 6 的分级清单确认后做。
4. **分类对齐（旧 107 → 新 183）**：属 CTI 治理既有能力，本计划不重复实现。
5. 本计划**不新增 canonical 迁移**。
