# 工单执行分配"审批后置"改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把"工单该由谁执行"这件事，从工单创建时同步、无差别的自动分配，改成审批通过后由 BPMN
`taskPurpose="fulfillment"` 节点驱动、限定在目标团队内按工作负载均衡挑人，并顺手修掉"无处理人就广播全租户"
的通知兜底 bug。

**Architecture:** 新增 `TeamWorkloadResolver`（`service/approver/`），跟既有
`DeptManagerResolver`/`PersonalManagerResolver`/`TeamLeaderResolver` 同一个家族，通过 `teams.code` 定位
目标团队、在团队成员里挑工单负载最低的人。BPMN 引擎的 `createUserTask` 新增
`taskPurpose="fulfillment"` 分支调用它，解析成功后同步回写 `ent.Ticket.assignee_id`/`status`；处理人的
"你被分配了"通知交给紧跟其后的 `notify_handler` 声明式 ServiceTask 节点（复用已有 handler，无需新 Go 代码）。
删除工单创建时那段无差别自动分配的旧逻辑，管理员手动触发的"智能分配"入口改为委托给同一个新 resolver。

**Tech Stack:** Go + Gin + Ent ORM（后端），Next.js + TypeScript + bpmn-js（前端 BPMN 设计器），PostgreSQL。

**Spec:** `docs/superpowers/specs/2026-08-20-fulfillment-team-assignment-design.md`

## Global Constraints

- 团队匹配用 `teams.code`，不用 `teams.name`（spec"范围"一节）。
- `fulfillmentTeamCode` 节点未声明时 fallback 到常量 `defaultFulfillmentTeamCode`，写法对齐既有的
  `approvalFallbackCandidateGroup` 兜底模式（`bpmn_process_engine.go:773`）。
- `TeamWorkloadResolver` 候选池为空时只记 `Warnw` 日志，不落候选组兜底、不发任何通知。
- `NotifyTicketCreated` 不再有"广播全体/admin"分支；处理人分配通知完全由新增的 `notify_handler`
  ServiceTask 节点负责，不是 `NotifyTicketCreated`/`NotifyTicketAssigned` 的职责。
- `incident_emergency_flow`（含 `_v1.1`/`_cn`）三个变体本次不改动。
- `assigneeTeamId`/`TeamLeaderResolver`（团队负责人当审批人）不受影响，命名和实现都不复用给本次的
  "团队成员池当执行人"场景。

---

## Task 1: `TeamWorkloadResolver`

**Files:**
- Modify: `service/approver/resolver.go`（给 `ApproverContext` 加 `TeamCode` 字段）
- Create: `service/approver/team_workload_resolver.go`
- Test: `service/approver/team_workload_resolver_test.go`

**Interfaces:**
- Produces: `approver.NewTeamWorkloadResolver() *TeamWorkloadResolver`，实现
  `Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error)`
  （跟 `DeptManagerResolver`/`PersonalManagerResolver` 同一个接口）。`ApproverContext.TeamCode string`
  是新增字段，`Resolve` 只用 `TenantID`+`TeamCode`，不用 `RequesterID`/`DepartmentID`。

- [ ] **Step 1: 给 `ApproverContext` 加 `TeamCode` 字段**

在 `service/approver/resolver.go` 找到 `ApproverContext` 结构体（当前有 `TenantID`/`TicketID`/
`RequesterID`/`DepartmentID`/`TeamID`/`ProjectID`/`Amount`/`Variables` 字段），加一行：

```go
	TeamCode     string                 `json:"teamCode,omitempty"`
```

放在 `TeamID` 字段下面（`TeamID` 是既有的、给 `TeamLeaderResolver` 用的数字 ID，语义不同，两个字段并存）。

- [ ] **Step 2: 写失败的测试**

创建 `service/approver/team_workload_resolver_test.go`：

```go
package approver

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTeamWorkloadTest(t *testing.T) (*ent.Client, context.Context, *ent.Tenant) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:team_workload_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("TWR Tenant").SetCode("twr").SetDomain("twr.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	return client, ctx, tenant
}

func createTeam(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, name, code string) *ent.Team {
	t.Helper()
	tm, err := client.Team.Create().SetName(name).SetCode(code).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return tm
}

func createTeamMember(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, username string, teamID int) *ent.User {
	t.Helper()
	// User 的 ent schema 没有声明反向的 team 边（team_users 外键只在 Team 侧用
	// edge.To("users", User.Type) 声明），所以没有 UserCreate.SetTeamXxx 这种方法——
	// 必须先建好 User，再从 Team 一侧用 AddUserIDs 把这个外键关系接上。
	u, err := client.User.Create().
		SetUsername(username).SetEmail(username + "@twr.example.com").SetName(username).
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Team.UpdateOneID(teamID).AddUserIDs(u.ID).Save(ctx)
	require.NoError(t, err)
	return u
}

func createOpenTicket(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, requesterID, assigneeID int) {
	t.Helper()
	_, err := client.Ticket.Create().
		SetTitle("load").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("TWR-" + strconvItoa(assigneeID) + "-" + strconvItoa(requesterID)).
		SetRequesterID(requesterID).SetAssigneeID(assigneeID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestTeamWorkloadResolver_PicksLeastBusyMember(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)
	team := createTeam(t, client, ctx, tenant.ID, "服务台-L1", "服务台-l1")

	busy := createTeamMember(t, client, ctx, tenant.ID, "busy_agent", team.ID)
	idle := createTeamMember(t, client, ctx, tenant.ID, "idle_agent", team.ID)
	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@twr.example.com").SetName("requester").
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	createOpenTicket(t, client, ctx, tenant.ID, requester.ID, busy.ID)
	createOpenTicket(t, client, ctx, tenant.ID, requester.ID, busy.ID)

	resolver := NewTeamWorkloadResolver()
	result, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "服务台-l1"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, idle.ID, result[0].UserID, "零负载的 idle_agent 应该胜出")
}

func TestTeamWorkloadResolver_EmptyTeam_ReturnsError(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)
	createTeam(t, client, ctx, tenant.ID, "服务台-L1", "服务台-l1")

	resolver := NewTeamWorkloadResolver()
	_, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "服务台-l1"})
	assert.Error(t, err)
}

func TestTeamWorkloadResolver_UnknownTeamCode_ReturnsError(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)

	resolver := NewTeamWorkloadResolver()
	_, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "不存在的团队"})
	assert.Error(t, err)
}

var _ = ticket.FieldAssigneeID // 占位保留 import，Step 4 会真正用到分组查询
```

- [ ] **Step 2b: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/approver/... -run TestTeamWorkloadResolver -v`
Expected: FAIL，`NewTeamWorkloadResolver` 未定义（编译错误，Step 3 才会实现）。

- [ ] **Step 3: 实现 `TeamWorkloadResolver`**

创建 `service/approver/team_workload_resolver.go`：

```go
package approver

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/team"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
)

// TeamWorkloadResolver 把 taskPurpose="fulfillment" 任务的 assignee 解析为指定团队里当前工单
// 负载最低的 active 成员。跟审批用的 DeptManagerResolver/PersonalManagerResolver/TeamLeaderResolver
// 语义不同：那三个解析的是"谁有资格批"，这个解析的是"谁来干活"，所以不看 RequesterID/DepartmentID，
// 只看目标团队现在谁最闲。设计详见
// docs/superpowers/specs/2026-08-20-fulfillment-team-assignment-design.md。
type TeamWorkloadResolver struct{}

func NewTeamWorkloadResolver() *TeamWorkloadResolver {
	return &TeamWorkloadResolver{}
}

func (r *TeamWorkloadResolver) GetType() string {
	return "team_workload"
}

func (r *TeamWorkloadResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	if appCtx.TeamCode == "" {
		return nil, fmt.Errorf("team_code is required for team_workload resolver")
	}

	teamEntity, err := client.Team.Query().
		Where(team.CodeEQ(appCtx.TeamCode), team.TenantID(appCtx.TenantID), team.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("team not found: code=%s", appCtx.TeamCode)
		}
		return nil, fmt.Errorf("failed to query team: %w", err)
	}

	candidates, err := teamEntity.QueryUsers().
		Where(user.TenantIDEQ(appCtx.TenantID), user.ActiveEQ(true)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query team members: %w", err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no active member found in team: code=%s", appCtx.TeamCode)
	}

	candidateIDs := make([]int, 0, len(candidates))
	byID := make(map[int]*ent.User, len(candidates))
	for _, u := range candidates {
		candidateIDs = append(candidateIDs, u.ID)
		byID[u.ID] = u
	}

	// 批量聚合查询全体候选人的当前活跃工单数，一次 SQL 往返而不是每个候选人一次——
	// 这是本轮会话在 TicketAssignmentService 里已经验证过、能把 O(候选人数) 查询次数
	// 压成 O(1) 的写法，这里从一开始就用对，不留性能债。
	type assigneeCount struct {
		AssigneeID int `json:"assignee_id"`
		Count      int `json:"count"`
	}
	var counts []assigneeCount
	if err := client.Ticket.Query().
		Where(ticket.AssigneeIDIn(candidateIDs...), ticket.StatusIn("open", "in_progress", "pending")).
		GroupBy(ticket.FieldAssigneeID).
		Aggregate(ent.Count()).
		Scan(ctx, &counts); err != nil {
		return nil, fmt.Errorf("failed to count active tickets: %w", err)
	}

	loadByUser := make(map[int]int, len(candidateIDs))
	for _, c := range counts {
		loadByUser[c.AssigneeID] = c.Count
	}

	bestID := candidateIDs[0]
	bestLoad := loadByUser[bestID]
	for _, id := range candidateIDs[1:] {
		if loadByUser[id] < bestLoad {
			bestID = id
			bestLoad = loadByUser[id]
		}
	}

	best := byID[bestID]
	return []*ApproverInfo{{
		UserID:    best.ID,
		UserName:  best.Username,
		UserEmail: best.Email,
		Role:      "team_workload",
		Source:    fmt.Sprintf("team:%s", appCtx.TeamCode),
	}}, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/approver/... -run TestTeamWorkloadResolver -v`
Expected: PASS（3 个用例全过）。

- [ ] **Step 5: 清理测试里的占位行，提交**

删掉 Step 2 测试文件末尾的 `var _ = ticket.FieldAssigneeID // 占位保留 import...` 这一行（Step 3
实现完成后 `ticket` 包已经被真正用到，不再需要占位）。

```bash
cd itsm-backend
go vet ./service/approver/...
git add service/approver/resolver.go service/approver/team_workload_resolver.go service/approver/team_workload_resolver_test.go
git commit -m "feat(approver): 新增 TeamWorkloadResolver，按团队工作负载解析执行分配人"
```

---

## Task 2: BPMN 引擎接入 `taskPurpose="fulfillment"`

**Files:**
- Modify: `service/bpmn_types.go`
- Modify: `service/bpmn_process_engine.go`
- Test: `service/bpmn_process_engine_fulfillment_test.go`（新建，跟既有
  `bpmn_process_engine_approval_assignment_test.go` 同一种夹具风格，分开成独立文件方便以后单独维护）

**Interfaces:**
- Consumes: Task 1 的 `approver.NewTeamWorkloadResolver()`、`approver.ApproverContext{TenantID, TeamCode}`。
- Produces: `BPMNUserTask.FulfillmentTeamCode string`（XML 属性 `fulfillmentTeamCode`）；
  `defaultFulfillmentTeamCode` 常量（`package service`，Task 3 会直接引用）；`createUserTask` 对
  `taskPurpose="fulfillment"` 节点的完整处理（解析 assignee + 回写 `ent.Ticket`）。

- [ ] **Step 1: 给 `BPMNUserTask` 加 `FulfillmentTeamCode` 字段**

在 `service/bpmn_types.go` 找到 `AssigneeGmChain` 字段（第 138 行附近），紧接着加一行：

```go
	FulfillmentTeamCode     string `xml:"fulfillmentTeamCode,attr"`
```

- [ ] **Step 2: 写失败的测试**

创建 `service/bpmn_process_engine_fulfillment_test.go`（复用
`bpmn_process_engine_approval_assignment_test.go` 里已经有的 `approvalAssignmentFixture`/
`createUser`/`createInstance`/`getCreatedTask` 这些 helper，同一个 package，不用重新声明）：

```go
package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fulfillmentTask(id, name, teamCode string) *BPMNUserTask {
	return &BPMNUserTask{ID: id, Name: name, TaskPurpose: "fulfillment", FulfillmentTeamCode: teamCode}
}

func (f *approvalAssignmentFixture) createOpenTicket(t *testing.T, requesterID int) *ent.Ticket {
	t.Helper()
	tk, err := f.client.Ticket.Create().
		SetTitle("待执行工单").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("FF-" + strconv.Itoa(requesterID)).
		SetRequesterID(requesterID).SetTenantID(f.tenant.ID).
		Save(f.ctx)
	require.NoError(t, err)
	return tk
}

func TestCreateUserTask_Fulfillment_AssignsLeastBusyTeamMemberAndSyncsTicket(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	team, err := fx.client.Team.Create().SetName("服务台-L1").SetCode("服务台-l1").SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
	idle, err := fx.client.User.Create().
		SetUsername("l1_idle").SetEmail("l1_idle@example.com").SetName("l1_idle").
		SetPasswordHash("hash").SetActive(true).SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.Team.UpdateOneID(team.ID).AddUserIDs(idle.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", "服务台-l1"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, strconv.Itoa(idle.ID), task.Assignee)

	updated, err := fx.client.Ticket.Get(fx.ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, idle.ID, updated.AssigneeID, "fulfillment 解析成功后必须同步回写 ticket.assignee_id")
	assert.Equal(t, common.TicketStatusAssigned, updated.Status)
}

func TestCreateUserTask_Fulfillment_EmptyTeam_LeavesTicketUnassigned(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	_, err := fx.client.Team.Create().SetName("服务台-L1").SetCode("服务台-l1").SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester2", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-empty", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", "服务台-l1"))
	require.NoError(t, err, "候选池为空不应该让节点创建本身失败")

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, "", task.Assignee)

	updated, err := fx.client.Ticket.Get(fx.ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.AssigneeID, "候选池为空时不应该误写 ticket.assignee_id")
}

func TestCreateUserTask_Fulfillment_NoTeamCodeDeclared_UsesDefault(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	team, err := fx.client.Team.Create().
		SetName("服务台-L1").SetCode(defaultFulfillmentTeamCode).SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
	member, err := fx.client.User.Create().
		SetUsername("default_team_member").SetEmail("dtm@example.com").SetName("dtm").
		SetPasswordHash("hash").SetActive(true).SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.Team.UpdateOneID(team.ID).AddUserIDs(member.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester3", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-default", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	// FulfillmentTeamCode 留空，验证 fallback 到 defaultFulfillmentTeamCode 常量。
	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", ""))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, strconv.Itoa(member.ID), task.Assignee)
}
```

- [ ] **Step 2b: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestCreateUserTask_Fulfillment -v`
Expected: FAIL（`FulfillmentTeamCode`/`defaultFulfillmentTeamCode` 未定义，或 fulfillment 分支没有实现，
断言拿到空字符串）。

- [ ] **Step 3: 在 `createUserTask` 里接入 fulfillment 分支**

在 `service/bpmn_process_engine.go` 顶部（`approvalFallbackCandidateGroup` 常量定义，第 773 行）附近，加：

```go
// defaultFulfillmentTeamCode 是 taskPurpose="fulfillment" 节点没有声明 fulfillmentTeamCode 时的
// 兜底团队 code，跟 approvalFallbackCandidateGroup 是同一种"节点未声明就用常量兜底"写法。
const defaultFulfillmentTeamCode = "服务台-l1"
```

在文件顶部 import 块加 `"itsm-backend/ent/ticket"`（`ticket` 包目前没被这个文件直接引用，只有
`ticketassignmentrule` 已经导入）。

找到 `createUserTask` 里 `if task.TaskPurpose == "approval" { switch { ... } }` 这段（约第 806-887
行），在它的 `if` 分支结束、`else` 分支（约 871 行 `} else { ... assignee = getUserID("requester_id") ...
}`）之前，插入一个并列的 `else if`：

```go
	} else if task.TaskPurpose == "fulfillment" {
		teamCode := task.FulfillmentTeamCode
		if teamCode == "" {
			teamCode = defaultFulfillmentTeamCode
		}
		approvers, resolveErr := approver.NewTeamWorkloadResolver().Resolve(ctx, e.client, &approver.ApproverContext{
			TenantID: instance.TenantID,
			TeamCode: teamCode,
		})
		if resolveErr != nil || len(approvers) == 0 {
			e.logger.Warnw("执行任务未在目标团队解析到可用处理人，工单暂不分配",
				"taskID", task.ID, "instanceID", instance.ID, "teamCode", teamCode, "error", resolveErr)
			// 不落候选组兜底，不触发任何通知——留空 assignee，等团队有真实成员后由人工在
			// 工单列表里认领，或后续补一个定时扫描重试（不在本次范围）。
		} else {
			resolvedUserID := approvers[0].UserID
			assignee = strconv.Itoa(resolvedUserID)
			// fulfillment 任务的 assignee 就是工单的处理人，必须同步写回 ticket.assignee_id/
			// status——BPMN 引擎对 taskPurpose="approval" 节点从来不碰 ent.Ticket（审批人不是
			// 处理人，不该动工单行），但这里是例外：处理人分配的通知交给紧跟其后的
			// notify_handler ServiceTask 节点（读这里刚写好的 assignee_id），引擎本身不直接
			// 调用通知服务，沿用"通知走声明式 ServiceTask 节点"的既有模式
			// （Activity_NotifyRequester/Activity_RejectNotify 都是这样做的）。
			businessTicketID, convErr := strconv.Atoi(getUserID("business_id"))
			if convErr != nil || businessTicketID <= 0 {
				e.logger.Warnw("fulfillment 任务已解析出 assignee，但没有有效的 business_id，跳过 ticket 回写",
					"taskID", task.ID, "assignee", assignee)
			} else if updateErr := e.client.Ticket.UpdateOneID(businessTicketID).
				Where(ticket.TenantIDEQ(instance.TenantID)).
				SetAssigneeID(resolvedUserID).
				SetStatus(common.TicketStatusAssigned).
				Exec(ctx); updateErr != nil {
				e.logger.Warnw("fulfillment 任务已解析出 assignee，但回写 ticket 失败",
					"taskID", task.ID, "assignee", assignee, "ticketID", businessTicketID, "error", updateErr)
			}
		}
	} else {
```

（把原来紧接着的 `} else {` 这一行删掉，因为上面这段结尾已经带上了它——插入位置是把原本的
`if ... { approval switch } else { requester_id 兜底 }` 改成
`if approval { ... } else if fulfillment { ... } else { requester_id 兜底 }`，第三段原样保留不动。）

在文件顶部 import 里确认已经有 `"itsm-backend/service/approver"`（`resolveApprovalAssignee`/
`resolveGmChainAssignee` 已经在用这个包，不用新加）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestCreateUserTask_Fulfillment -v`
Expected: PASS（3 个用例全过）。

- [ ] **Step 5: 跑一遍既有的审批相关测试，确认没有回归**

Run: `cd itsm-backend && go test ./service/... -run "TestCreateUserTask_Approval|TestCreateUserTask_Fulfillment" -v`
Expected: 全部 PASS——`else if` 分支的插入不能影响原有 `taskPurpose="approval"` 分支和默认分支的行为。

- [ ] **Step 6: 提交**

```bash
cd itsm-backend
go build ./...
git add service/bpmn_types.go service/bpmn_process_engine.go service/bpmn_process_engine_fulfillment_test.go
git commit -m "feat(bpmn): 接入 taskPurpose=fulfillment，按团队工作负载解析执行分配并回写工单"
```

---

## Task 3: 管理员手动"智能分配"改为委托给 `TeamWorkloadResolver`；删除旧的无差别自动分配算法

**Files:**
- Modify: `service/ticket_assignment_service.go`
- Modify: `service/ticket_assignment_service_test.go`

**Interfaces:**
- Consumes: Task 1 的 `approver.NewTeamWorkloadResolver()`；Task 2 的 `defaultFulfillmentTeamCode`
  常量（同一个 `package service`，直接可见，不用 import）。
- Produces: `TicketAssignmentService.autoAssignTicket` 保留原有签名
  `(ctx context.Context, req *AssignmentRequest) (*AssignmentResponse, error)`，内部实现完全重写。

**背景**：前端 `TicketAssignmentApi.autoAssign(ticketId)`（`admin/tickets/assignment-rules` 页面、
`SmartAssignmentModal.tsx` 的"智能分配"选项）会打到 `AssignTicket` 接口的 `AutoAssign:true` 分支，
最终调用 `autoAssignTicket`——这个方法不能整个删掉，只能重写内部实现，否则会让一个真实在用的管理员功能
编译失败/消失。

- [ ] **Step 1: 写失败的测试（替换旧的性能回归测试）**

打开 `service/ticket_assignment_service_test.go`，删除整个
`TestTicketAssignmentService_AutoAssign_QueryCountIsBounded` 函数（本次会话早前为验证 `getAvailableUsers`
性能新增的测试——它测的是即将被删除的内部实现，对应的"批量查询不退化成 O(N)"这个保证已经在 Task 1 的
`TeamWorkloadResolver` 里用同样手法实现，测试职责搬过去了，这里不需要重复）。

在文件里 `TestTicketAssignmentService_AssignTicket` 函数下方新增：

```go
func TestTicketAssignmentService_AutoAssign_DelegatesToTeamWorkloadResolver(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()
	testTenant, err := client.Tenant.Create().
		SetName("Auto Delegate Tenant").SetCode("ad").SetDomain("ad.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	team, err := client.Team.Create().
		SetName("服务台-L1").SetCode(defaultFulfillmentTeamCode).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)
	idle, err := client.User.Create().
		SetUsername("idle_agent").SetEmail("idle_agent@ad.example.com").SetName("idle_agent").
		SetPasswordHash("hash").SetActive(true).SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Team.UpdateOneID(team.ID).AddUserIDs(idle.ID).Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@ad.example.com").SetName("requester").
		SetPasswordHash("hash").SetActive(true).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	testTicket, err := client.Ticket.Create().
		SetTitle("待自动分配").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("AD-001").SetRequesterID(requester.ID).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	resp, err := assignmentService.AssignTicket(ctx, &AssignmentRequest{
		TicketID:   testTicket.ID,
		TenantID:   testTenant.ID,
		Priority:   "medium",
		AutoAssign: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.AssignedTo)
	assert.Equal(t, idle.ID, *resp.AssignedTo)
	assert.Equal(t, "auto", resp.AssignmentType)

	updated, err := client.Ticket.Get(ctx, testTicket.ID)
	require.NoError(t, err)
	assert.Equal(t, idle.ID, updated.AssigneeID)
}

func TestTicketAssignmentService_AutoAssign_EmptyTeam_ReturnsNoAssignee(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()
	testTenant, err := client.Tenant.Create().
		SetName("Auto Delegate Empty Tenant").SetCode("ade").SetDomain("ade.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester2").SetEmail("requester2@ade.example.com").SetName("requester2").
		SetPasswordHash("hash").SetActive(true).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	testTicket, err := client.Ticket.Create().
		SetTitle("待自动分配-无团队成员").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("ADE-001").SetRequesterID(requester.ID).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	resp, err := assignmentService.AssignTicket(ctx, &AssignmentRequest{
		TicketID:   testTicket.ID,
		TenantID:   testTenant.ID,
		Priority:   "medium",
		AutoAssign: true,
	})
	require.NoError(t, err)
	assert.Nil(t, resp.AssignedTo)
	assert.Equal(t, "没有可用的处理人", resp.Reason)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestTicketAssignmentService_AutoAssign -v`
Expected: FAIL（旧的 `autoAssignTicket` 实现会把没有 team 过滤的候选人池全体拿来打分，测试断言的
`idle.ID` 大概率选不中，或者干脆因为候选人评分实现还在旧代码路径上而得到不同结果）。

- [ ] **Step 3: 重写 `autoAssignTicket`，删除 `getAvailableUsers` 及专用私有方法**

打开 `service/ticket_assignment_service.go`。

先删除以下整个方法（这些是本次会话早前为修 O(N) 性能问题新增/存在的，候选人池现在完全由
`TeamWorkloadResolver` 负责，不再需要）：
- `getAvailableUsers`
- `assigneeAggRow`（类型定义）
- `batchGetUserWorkloads`
- `calculateUserScore`
- `calculateUserScoreWithPrecomputed`
- `batchCalculateCategoryExperienceScores`
- `batchCalculatePerformanceScores`
- `calculateSkillScore`
- `calculateWorkloadScore`
- `calculateCategoryExperienceScore`
- `calculatePerformanceScore`
- `checkUserSkills`
- `checkUserCategoryAccess`
- `getMaxActiveTickets`
- `getAlternativeUserIDs`
- `CalculateUserScore`（公开包装方法，没有任何调用方，一并删除）

再把 `autoAssignTicket` 方法整个替换成：

```go
// autoAssignTicket 自动分配工单——委托给 TeamWorkloadResolver（跟 BPMN taskPurpose="fulfillment"
// 节点用的是同一个 resolver），候选人限定在目标团队内，不再是"全体 active 用户"。这里没有 BPMN
// 节点上下文可读 fulfillmentTeamCode，固定用 defaultFulfillmentTeamCode（跟 BPMN 侧节点未声明
// fulfillmentTeamCode 时的默认值保持一致）。
func (s *TicketAssignmentService) autoAssignTicket(ctx context.Context, req *AssignmentRequest) (*AssignmentResponse, error) {
	approvers, err := approver.NewTeamWorkloadResolver().Resolve(ctx, s.client, &approver.ApproverContext{
		TenantID: req.TenantID,
		TeamCode: defaultFulfillmentTeamCode,
	})
	if err != nil || len(approvers) == 0 {
		s.logger.Infow("自动分配未在目标团队解析到可用处理人", "ticketID", req.TicketID, "error", err)
		return &AssignmentResponse{
			TicketID:       req.TicketID,
			AssignmentType: "auto",
			Reason:         "没有可用的处理人",
		}, nil
	}

	assigneeID := approvers[0].UserID
	if err := s.client.Ticket.UpdateOneID(req.TicketID).
		Where(ticket.TenantIDEQ(req.TenantID)).
		SetAssigneeID(assigneeID).
		SetStatus(common.TicketStatusAssigned).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("分配工单失败: %w", err)
	}

	return &AssignmentResponse{
		TicketID:       req.TicketID,
		AssignedTo:     &assigneeID,
		AssignmentType: "auto",
		Reason:         "按团队工作负载自动分配",
	}, nil
}
```

更新文件顶部 import：加 `"itsm-backend/common"` 和 `"itsm-backend/service/approver"`；删掉
`"sort"`（原来 `calculateUserScore` 那批方法用它排序，删完就没人用了，`go build` 会报未使用的 import，
到时候按报错删）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestTicketAssignmentService -v`
Expected: 全部 PASS（含没有改动过的 `GetUserWorkload`/`GetTeamWorkload`/`ReassignTicket`/
`GetTicketsByAssignee`/`AssignTickets`/`CalculateSkillScore`/`CalculateWorkloadScore`/
`GetMaxActiveTickets` 相关测试——等等，这几个测试对应的方法这一步已经删了，运行会报编译错误）。

删除 `service/ticket_assignment_service_test.go` 里以下整个测试函数（对应刚删掉的私有方法，测试和被测
代码要一起删，不能留下测不存在的方法的死测试）：
- `TestTicketAssignmentService_CalculateSkillScore`
- `TestTicketAssignmentService_CalculateWorkloadScore`
- `TestTicketAssignmentService_GetMaxActiveTickets`

删完后重新跑：

Run: `cd itsm-backend && go build ./... && go test ./service/... -run TestTicketAssignmentService -v`
Expected: `go build` 干净，测试全部 PASS。

- [ ] **Step 5: 提交**

```bash
cd itsm-backend
git add service/ticket_assignment_service.go service/ticket_assignment_service_test.go
git commit -m "refactor(ticket): 自动分配改为委托 TeamWorkloadResolver，删除无团队过滤的旧打分算法"
```

---

## Task 4: 删除工单创建时的同步自动分配

**Files:**
- Modify: `service/ticket_service.go:192-199`
- Test: 复用既有 `service/ticket_service_test.go`（如果没有覆盖 `CreateTicket` 不再自动分配的用例，
  这一步补一个）

**Interfaces:**
- Consumes: 无新接口——纯删除代码。

- [ ] **Step 1: 确认现状（写一个会失败的测试，证明现在确实会自动分配）**

先看 `service/ticket_service_test.go` 有没有已经存在的、断言"创建工单后 AssigneeID 被自动填充"的用例：

Run: `cd itsm-backend && grep -n "AssigneeID" service/ticket_service_test.go`

如果有这样的用例，记下它的函数名——Step 3 删完代码后这个用例的断言需要反过来（改成断言创建后
`AssigneeID` 为空）。如果没有，跳到 Step 2 直接补一个新用例。

在 `service/ticket_service_test.go` 里补（或修改已有用例为）：

```go
func TestTicketService_CreateTicket_DoesNotAutoAssign(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	tenant, err := client.Tenant.Create().
		SetName("No AutoAssign Tenant").SetCode("naa").SetDomain("naa.example.com").SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("naa_requester").SetEmail("naa@example.com").SetName("naa_requester").
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).Save(context.Background())
	require.NoError(t, err)

	svc := NewTicketService(TicketServiceConfig{Client: client, Logger: logger})
	created, err := svc.CreateTicket(context.Background(), &dto.CreateTicketRequest{
		Title:       "不应该被自动分配的工单",
		Description: "x",
		Priority:    "medium",
		RequesterID: requester.ID,
	}, tenant.ID)
	require.NoError(t, err)

	assert.Nil(t, created.AssigneeID, "创建工单不应该再触发无差别自动分配——分配现在只在 BPMN fulfillment 节点触发")
}
```

（`NewTicketService`/`TicketServiceConfig` 的确切构造方式以 `service/ticket_service_test.go` 里现有用例
为准——如果现有用例用的字段名/构造函数签名跟这里不同，照抄现有用例的构造方式，不要凭空编。）

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestTicketService_CreateTicket_DoesNotAutoAssign -v`
Expected: FAIL（`created.AssigneeID` 非空，因为旧代码还在）。

- [ ] **Step 3: 删除同步自动分配逻辑**

打开 `service/ticket_service.go`，删除第 192-199 行这一整段：

```go
	if tkt.AssigneeID == nil && s.assignmentSmartService != nil {
		assignment, err := s.assignmentSmartService.AutoAssign(ctx, tkt.ID, tenantID)
		if err != nil {
			s.logger.Warnw("Automatic ticket assignment failed", "error", err, "ticket_id", tkt.ID)
		} else {
			tkt.AssigneeID = assignment.AssignedTo
		}
	}
```

删除后确认 `TicketService` 结构体里的 `assignmentSmartService` 字段、`NewTicketService` 里对应的赋值
是否还有其他地方引用（Run: `grep -n "assignmentSmartService" service/ticket_service.go`）——如果删完这
一段之后这个字段再没有别的用途，把字段声明和构造赋值也一并删掉（`go vet`/`go build` 会通过未使用字段
的编译提示帮你确认，Go 不会因为字段未读而报错，所以要手动 grep 确认，不能只靠编译器）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run TestTicketService_CreateTicket -v`
Expected: `go build` 干净，`TestTicketService_CreateTicket_DoesNotAutoAssign` PASS，其他
`TestTicketService_CreateTicket*` 用例不受影响（如果 Step 1 发现了旧的"断言自动分配成功"的用例并且
已经反转了断言，这里应该也 PASS）。

- [ ] **Step 5: 提交**

```bash
cd itsm-backend
git add service/ticket_service.go service/ticket_service_test.go
git commit -m "refactor(ticket): 删除工单创建时的同步自动分配，分配改由 BPMN fulfillment 节点触发"
```

---

## Task 5: `NotifyTicketCreated` 去掉广播兜底

**Files:**
- Modify: `service/ticket_notification_service.go:207-261`
- Test: `service/ticket_notification_service_test.go`

**Interfaces:**
- Consumes: 无新接口。
- Produces: `NotifyTicketCreated` 签名不变（`ctx context.Context, ticket *ent.Ticket) error`），内部逻辑
  重写。

- [ ] **Step 1: 写失败的测试**

打开 `service/ticket_notification_service_test.go`，如果已经有测试覆盖"只有申请人、没有处理人时广播
全体"的旧行为，先记下函数名（Step 3 完成后要反转断言或删除）。新增：

```go
func TestTicketNotificationService_NotifyTicketCreated_NoAssignee_OnlyNotifiesRequester(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Notify Tenant").SetCode("nt").SetDomain("nt.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("nt_requester").SetEmail("nt_requester@example.com").SetName("nt_requester").
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// 另建 3 个跟这张工单毫无关系的用户，验证他们不会被拉进收件人列表——
	// 这是本次要修的 bug 本身：旧代码会把除申请人外的全部用户都拉进来。
	for i := 0; i < 3; i++ {
		_, err := client.User.Create().
			SetUsername(fmt.Sprintf("bystander_%d", i)).
			SetEmail(fmt.Sprintf("bystander_%d@example.com", i)).
			SetName(fmt.Sprintf("bystander_%d", i)).
			SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, err)
	}

	tk, err := client.Ticket.Create().
		SetTitle("待审批工单").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("NT-001").SetRequesterID(requester.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	var capturedUserIDs []int
	svc := NewTicketNotificationService(client, logger)
	svc.notifySender = func(ctx context.Context, ticketID int, req *dto.SendTicketNotificationRequest, tenantID int) error {
		capturedUserIDs = req.UserIDs
		return nil
	}

	err = svc.NotifyTicketCreated(ctx, tk)
	require.NoError(t, err)
	assert.Equal(t, []int{requester.ID}, capturedUserIDs, "没有 assignee 时只应该通知申请人本人，不广播")
}
```

（`svc.notifySender` 这个可替换的钩子现在多半不存在——`SendNotification` 目前很可能是
`TicketNotificationService` 自己的方法，不是可注入的字段。如果 Step 1 运行发现编译错误说
`notifySender` 未定义，改成直接断言真实收件人：给
`SendNotification`/底层邮件发送打桩不现实的话，改成读 Step 3 实现里最终调用
`s.SendNotification(...)` 时传入的 `UserIDs` ——具体做法是在 `TicketNotificationService` 里补一个可选的
测试专用 hook 字段，或者更简单：直接读 `SendNotification` 实际调用后落到 `notifications` 表的行数据
断言收件人，用真实的 DB 断言代替 mock。选哪种以 `service/ticket_notification_service_test.go` 里其他
既有测试用的方式为准，保持风格一致，不要新发明一种测试方式。）

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestTicketNotificationService_NotifyTicketCreated -v`
Expected: FAIL（旧代码的广播分支会把 3 个 bystander 也拉进收件人）。

- [ ] **Step 3: 重写 `NotifyTicketCreated`**

打开 `service/ticket_notification_service.go`，把第 207-261 行整个函数体替换成：

```go
func (s *TicketNotificationService) NotifyTicketCreated(ctx context.Context, ticket *ent.Ticket) error {
	var userIDs []int
	if ticket.AssigneeID > 0 {
		userIDs = append(userIDs, ticket.AssigneeID)
	}
	if ticket.RequesterID > 0 && ticket.RequesterID != ticket.AssigneeID {
		userIDs = append(userIDs, ticket.RequesterID)
	}
	if len(userIDs) == 0 {
		return nil
	}

	// 不再有"只有申请人就广播全体/admin"的分支——工单刚创建、还没走到 BPMN fulfillment 节点时
	// 没有 assignee 现在是正常状态（审批还没走完），不是需要额外通知谁的异常。处理人分配时的
	// "你被分配了"通知由 BPMN 里紧跟 fulfillment 节点之后的 notify_handler 服务任务节点负责，
	// 不是这个函数的职责。
	content := fmt.Sprintf("新工单已创建：%s (#%s)", ticket.Title, ticket.TicketNumber)
	return s.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "ticket_created",
		Content:   content,
	}, ticket.TenantID)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run TestTicketNotificationService -v`
Expected: 干净通过。如果之前存在测试旧广播行为的用例，需要同步删除或反转断言。

- [ ] **Step 5: 提交**

```bash
cd itsm-backend
git add service/ticket_notification_service.go service/ticket_notification_service_test.go
git commit -m "fix(notification): 删除 NotifyTicketCreated 的全租户广播兜底分支"
```

---

## Task 6: `service_request_flow.bpmn` / `service_request_urgent_flow.bpmn` 接入 fulfillment

**Files:**
- Modify: `service/bpmn/service_request_flow.bpmn`
- Modify: `service/bpmn/service_request_urgent_flow.bpmn`

**Interfaces:** 无 Go 代码接口，纯 BPMN XML 结构改动，被 Task 2 的解析/引擎逻辑消费。

- [ ] **Step 1: 改 `service_request_flow.bpmn`**

把：

```xml
    <!-- 执行服务 -->
    <bpmn:userTask id="Activity_Execute" name="执行服务">
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:incoming>Flow_Approved</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:userTask>
```

改成：

```xml
    <!-- 执行服务 -->
    <bpmn:userTask id="Activity_Execute" name="执行服务" taskPurpose="fulfillment">
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:incoming>Flow_Approved</bpmn:incoming>
      <bpmn:outgoing>Flow_ExecuteNotify</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 通知处理人 -->
    <bpmn:serviceTask id="Activity_NotifyHandler" name="通知处理人" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">notify_handler</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_ExecuteNotify</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:serviceTask>
```

把：

```xml
    <bpmn:sequenceFlow id="Flow_6" sourceRef="Activity_Execute" targetRef="Activity_Confirm" />
```

改成：

```xml
    <bpmn:sequenceFlow id="Flow_ExecuteNotify" sourceRef="Activity_Execute" targetRef="Activity_NotifyHandler" />
    <bpmn:sequenceFlow id="Flow_6" sourceRef="Activity_NotifyHandler" targetRef="Activity_Confirm" />
```

- [ ] **Step 2: 改 `service_request_urgent_flow.bpmn`**

结构跟 `service_request_flow.bpmn` 完全一致（本次调研已确认两个文件的 `Activity_Execute` 节点位置、
入边/出边完全相同），照抄 Step 1 的改法，节点 ID/Flow ID 保持一致命名（`Activity_NotifyHandler`/
`Flow_ExecuteNotify`）。

- [ ] **Step 3: 校验 XML 良构**

Run: `cd itsm-backend && python3 -c "import xml.etree.ElementTree as ET; ET.parse('service/bpmn/service_request_flow.bpmn'); ET.parse('service/bpmn/service_request_urgent_flow.bpmn'); print('OK')"`
Expected: 输出 `OK`，没有 XML 解析错误。

- [ ] **Step 4: 提交**

```bash
cd itsm-backend
git add service/bpmn/service_request_flow.bpmn service/bpmn/service_request_urgent_flow.bpmn
git commit -m "feat(bpmn): 服务请求流程 Activity_Execute 接入 fulfillment 自动分配 + 处理人通知"
```

---

## Task 7: `ticket_general_flow.bpmn` 删除 `Activity_Assign`，`Activity_Handle` 接入 fulfillment

**Files:**
- Modify: `service/bpmn/ticket_general_flow.bpmn`

- [ ] **Step 1: 删除 `Activity_Assign` 节点，`Flow_1` 直连 `Gateway_Approval`**

把：

```xml
    <!-- 任务分配 -->
    <bpmn:userTask id="Activity_Assign" name="任务分配" instantiate="false">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">assign</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_ApprovalCheck</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 审批判断网关：统一决策是否需要审批 -->
    <bpmn:exclusiveGateway id="Gateway_Approval" name="是否需要审批?">
      <bpmn:incoming>Flow_ApprovalCheck</bpmn:incoming>
```

改成：

```xml
    <!-- 审批判断网关：统一决策是否需要审批 -->
    <bpmn:exclusiveGateway id="Gateway_Approval" name="是否需要审批?">
      <bpmn:incoming>Flow_1</bpmn:incoming>
```

把：

```xml
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Assign" />

    <!-- 所有任务分配后都先进入审批判断网关 -->
    <bpmn:sequenceFlow id="Flow_ApprovalCheck" sourceRef="Activity_Assign" targetRef="Gateway_Approval" />
```

改成：

```xml
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Gateway_Approval" />
```

- [ ] **Step 2: `Activity_Handle` 加 `taskPurpose="fulfillment"`，接一个通知处理人节点**

把：

```xml
    <!-- 处理任务 -->
    <bpmn:userTask id="Activity_Handle" name="工单处理">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">update_status</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_ApprovalNo</bpmn:incoming>
      <bpmn:incoming>Flow_Approved</bpmn:incoming>
      <bpmn:incoming>Flow_Reject</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:userTask>
```

改成：

```xml
    <!-- 处理任务 -->
    <bpmn:userTask id="Activity_Handle" name="工单处理" taskPurpose="fulfillment">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">update_status</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_ApprovalNo</bpmn:incoming>
      <bpmn:incoming>Flow_Approved</bpmn:incoming>
      <bpmn:incoming>Flow_Reject</bpmn:incoming>
      <bpmn:outgoing>Flow_HandleNotify</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 通知处理人 -->
    <bpmn:serviceTask id="Activity_NotifyHandler" name="通知处理人" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">notify_handler</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_HandleNotify</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:serviceTask>
```

把：

```xml
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Activity_Handle" targetRef="Gateway_Escalate" />
```

改成：

```xml
    <bpmn:sequenceFlow id="Flow_HandleNotify" sourceRef="Activity_Handle" targetRef="Activity_NotifyHandler" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Activity_NotifyHandler" targetRef="Gateway_Escalate" />
```

- [ ] **Step 3: 确认没有别的地方硬编码引用 `Activity_Assign`**

Run: `cd itsm-backend && grep -rn "Activity_Assign" --include=*.go . ; cd ../itsm-frontend && grep -rn "Activity_Assign" --include=*.ts --include=*.tsx src`
Expected: 没有命中，或者只命中测试文件里跟 `ticket_general_flow` 完全无关的字符串——如果命中了真实
引用这个节点 ID 的代码（比如某个测试断言"这个流程的第一个 UserTask 是 Activity_Assign"），需要单独
处理那个引用，不能盲目往下走。

- [ ] **Step 4: 校验 XML 良构**

Run: `cd itsm-backend && python3 -c "import xml.etree.ElementTree as ET; ET.parse('service/bpmn/ticket_general_flow.bpmn'); print('OK')"`
Expected: 输出 `OK`。

- [ ] **Step 5: 提交**

```bash
cd itsm-backend
git add service/bpmn/ticket_general_flow.bpmn
git commit -m "feat(bpmn): 通用工单流程删除审批前的 Activity_Assign，Activity_Handle 接入 fulfillment"
```

---

## Task 8: 新建 `copilot_procurement_flow.bpmn` 文件，接入 IT 总监审批后的执行环节

**背景**：这个流程目前只存在于数据库（`process_definitions.id=57`），没有对应的 `.bpmn` 源文件，
是本次会话手工测试时临时搭的 fixture，游离在 `service/bpmn/*.bpmn`（`//go:embed` 自动同步部署）这套
既有机制之外。这一步把它规范成文件，同时接入新的执行环节。

**Files:**
- Create: `service/bpmn/copilot_procurement_flow.bpmn`
- Modify: `service/bpmn_template_service.go`

- [ ] **Step 1: 创建 `.bpmn` 文件**

创建 `service/bpmn/copilot_procurement_flow.bpmn`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                 xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
                 xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
                 targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="copilot_procurement_flow" name="Copilot采购申请审批流程" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="提交申请">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>

    <bpmn:userTask id="Activity_DeptManagerApproval" name="部门负责人审批" taskPurpose="approval">
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:userTask id="Activity_GMApproval" name="总经理审批" taskPurpose="approval" assigneeGmChain="true">
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:userTask id="Activity_ITDirectorApproval" name="IT总监审批" taskPurpose="approval" assigneeRole="it_director">
      <bpmn:incoming>Flow_3</bpmn:incoming>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:userTask id="Activity_Execute" name="开通许可证账号" taskPurpose="fulfillment">
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:outgoing>Flow_ExecuteNotify</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:serviceTask id="Activity_NotifyHandler" name="通知处理人" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">notify_handler</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_ExecuteNotify</bpmn:incoming>
      <bpmn:outgoing>Flow_5</bpmn:outgoing>
    </bpmn:serviceTask>

    <bpmn:endEvent id="EndEvent_1" name="采购完成">
      <bpmn:incoming>Flow_5</bpmn:incoming>
    </bpmn:endEvent>

    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_DeptManagerApproval" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_DeptManagerApproval" targetRef="Activity_GMApproval" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Activity_GMApproval" targetRef="Activity_ITDirectorApproval" />
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Activity_ITDirectorApproval" targetRef="Activity_Execute" />
    <bpmn:sequenceFlow id="Flow_ExecuteNotify" sourceRef="Activity_Execute" targetRef="Activity_NotifyHandler" />
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Activity_NotifyHandler" targetRef="EndEvent_1" />
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="copilot_procurement_flow">
      <bpmndi:BPMNShape id="StartEvent_1_di" bpmnElement="StartEvent_1">
        <dc:Bounds x="180" y="150" width="36" height="36"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Activity_DeptManagerApproval_di" bpmnElement="Activity_DeptManagerApproval">
        <dc:Bounds x="270" y="128" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Activity_GMApproval_di" bpmnElement="Activity_GMApproval">
        <dc:Bounds x="430" y="128" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Activity_ITDirectorApproval_di" bpmnElement="Activity_ITDirectorApproval">
        <dc:Bounds x="590" y="128" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Activity_Execute_di" bpmnElement="Activity_Execute">
        <dc:Bounds x="750" y="128" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Activity_NotifyHandler_di" bpmnElement="Activity_NotifyHandler">
        <dc:Bounds x="910" y="128" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="EndEvent_1_di" bpmnElement="EndEvent_1">
        <dc:Bounds x="1072" y="150" width="36" height="36"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Flow_1_di" bpmnElement="Flow_1">
        <di:waypoint x="216" y="168"/>
        <di:waypoint x="270" y="168"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Flow_2_di" bpmnElement="Flow_2">
        <di:waypoint x="370" y="168"/>
        <di:waypoint x="430" y="168"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Flow_3_di" bpmnElement="Flow_3">
        <di:waypoint x="530" y="168"/>
        <di:waypoint x="590" y="168"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Flow_4_di" bpmnElement="Flow_4">
        <di:waypoint x="690" y="168"/>
        <di:waypoint x="750" y="168"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Flow_ExecuteNotify_di" bpmnElement="Flow_ExecuteNotify">
        <di:waypoint x="850" y="168"/>
        <di:waypoint x="910" y="168"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Flow_5_di" bpmnElement="Flow_5">
        <di:waypoint x="1010" y="168"/>
        <di:waypoint x="1072" y="168"/>
      </bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>
```

- [ ] **Step 2: 在 `bpmn_template_service.go` 的 `listTemplates` 里加一条 case**

打开 `service/bpmn_template_service.go`，找到 `case "release_approval_flow":`（约第 210 行）那段，在它
下面加：

```go
		case "copilot_procurement_flow":
			info.Name = "Copilot采购申请审批流程"
			info.Category = "service_request"
			info.Description = "Copilot/M365 Copilot 等 AI 工具采购三级审批（部门负责人→总经理→IT总监）"
```

- [ ] **Step 3: 校验 XML 良构**

Run: `cd itsm-backend && python3 -c "import xml.etree.ElementTree as ET; ET.parse('service/bpmn/copilot_procurement_flow.bpmn'); print('OK')"`
Expected: 输出 `OK`。

- [ ] **Step 4: `go build` 确认新 case 编译通过**

Run: `cd itsm-backend && go build ./...`
Expected: 无错误。

- [ ] **Step 5: 提交**

```bash
cd itsm-backend
git add service/bpmn/copilot_procurement_flow.bpmn service/bpmn_template_service.go
git commit -m "feat(bpmn): copilot_procurement_flow 落地为文件并接入执行环节，纳入模板自动同步机制"
```

---

## Task 9: 前端 BPMN 设计器暴露 `taskPurpose=fulfillment` / `fulfillmentTeamCode`

**Files:**
- Modify: `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`
- Modify: `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx`

- [ ] **Step 1: 声明新属性**

打开 `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`，在 `properties` 数组里
`{ name: 'assigneeGmChain', isAttr: true, type: 'Boolean' },` 这一行下面加：

```ts
      { name: 'fulfillmentTeamCode', isAttr: true, type: 'String' },
```

（`taskPurpose` 已经声明过了，不用改，`fulfillment` 只是这个既有 String 属性的一个新取值。）

- [ ] **Step 2: 加 `currentFulfillmentTeamCode` 读取**

打开 `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx`，在
`const currentAssigneeGmChain = Boolean(bo.assigneeGmChain);`（第 259 行）下面加：

```ts
  const currentFulfillmentTeamCode = (bo.fulfillmentTeamCode as string) || '';
```

- [ ] **Step 3: "审批语义" Select 加"执行任务"选项**

找到（约第 523-528 行）：

```tsx
              <Select
                value={currentTaskPurpose}
                onChange={value => apply({ taskPurpose: value })}
                options={[{ label: '普通人工任务', value: 'work' }, { label: '审批任务', value: 'approval' }]}
                className="w-full" size="small"
              />
```

改成：

```tsx
              <Select
                value={currentTaskPurpose}
                onChange={value => apply({ taskPurpose: value })}
                options={[
                  { label: '普通人工任务', value: 'work' },
                  { label: '审批任务', value: 'approval' },
                  { label: '执行任务（自动分配给团队成员）', value: 'fulfillment' },
                ]}
                className="w-full" size="small"
              />
```

- [ ] **Step 4: `taskPurpose="fulfillment"` 时显示团队 code 输入框**

紧跟着第 529-558 行 `{currentTaskPurpose === 'approval' && ( ... )}` 那个代码块之后（同一个
`<div className="mb-3 p-3 border ...">` 容器内），加一个并列的条件块：

```tsx
              {currentTaskPurpose === 'fulfillment' && (
                <div className="mt-2">
                  <Input
                    placeholder="团队 code（留空则默认服务台-L1，例如 服务台-l1）"
                    value={currentFulfillmentTeamCode}
                    onChange={e => apply({ fulfillmentTeamCode: e.target.value || undefined })}
                    size="small"
                  />
                  <Text type="secondary" className="text-xs mt-1 block">
                    到达此节点时，按工作负载自动分配给该团队里当前活跃工单最少的成员，不需要人工领取。
                    团队按 teams.code 匹配，不是团队名称。
                  </Text>
                </div>
              )}
```

（`Input`/`Text` 组件在这个文件里已经被大量使用过（第 542、610 行等），不需要新增 import。）

- [ ] **Step 5: 类型检查**

Run: `cd itsm-frontend && npm run type-check`
Expected: 无错误。

- [ ] **Step 6: 提交**

```bash
cd itsm-frontend
git add src/components/workflow/itsm-moddle-descriptor.ts src/components/workflow/designer/WorkflowNodeInspector.tsx
git commit -m "feat(bpmn-designer): 暴露执行任务(fulfillment)语义与 fulfillmentTeamCode 属性"
```

---

## Task 10: 全量验证

**Files:** 无新改动，纯验证。

- [ ] **Step 1: 后端全量测试**

Run: `cd itsm-backend && go build ./... && go test ./... 2>&1 | grep -v "^ok\|no test files"`
Expected: 无输出（`go build` 干净，所有测试通过，没有失败或跳过项被打印出来）。

- [ ] **Step 2: 前端类型检查**

Run: `cd itsm-frontend && npm run type-check`
Expected: 无错误。

- [ ] **Step 3: 重启后端，触发 BPMN 模板自动同步**

`internal/bootstrap/app.go:528` 在启动时会自动调用 `LoadAndDeployTemplates`，检测到 Task 6/7/8 改过的
`.bpmn` 文件内容跟 DB 里当前"最新版本"不一致，会自动发布新版本（`BPMNVersionService.CreateVersion`，
不会破坏运行中的旧实例）。

```bash
cd itsm-backend
go build -o /home/administrator/project/itsm/.pids/itsm-backend-dev main.go
kill $(cat /home/administrator/project/itsm/.pids/backend.pid) 2>/dev/null
nohup /home/administrator/project/itsm/.pids/itsm-backend-dev > logs/itsm.log 2>&1 &
echo $! > /home/administrator/project/itsm/.pids/backend.pid
sleep 2
curl -s -o /dev/null -w "health:%{http_code}\n" http://localhost:8090/api/v1/health
```

Expected: `health:200`。检查启动日志确认模板同步成功且没有报错：

```bash
grep -i "template\|copilot_procurement_flow\|ticket_general_flow\|service_request_flow" /home/administrator/project/itsm/itsm-backend/logs/itsm.log | tail -30
```

- [ ] **Step 4: 往服务台-L1 加一个真实测试成员**

```bash
export PGPASSWORD=dev123
psql -h localhost -U itsm_user -d itsm -c "select id, name, code from teams where tenant_id=1 and code ilike '%l1%';"
```

记下返回的服务台-L1 团队 `id`（下面用 `<L1_TEAM_ID>` 代替），挑一个不在本次审批链测试账号里的真实
用户加进去：

```bash
psql -h localhost -U itsm_user -d itsm -c "UPDATE users SET team_users=<L1_TEAM_ID> WHERE id=331;"
```

（复用之前测试用过的 331/陈雅芳当 L1 成员，纯粹是因为她已知是个真实、有效的账号，跟"部门负责人审批"
这个身份没有冲突——一个人可以同时是某个审批节点的固定审批人、又是某个团队的执行成员，两者是不同维度，
不冲突。）

- [ ] **Step 5: 端到端验证 Copilot 采购全链路（审批 + 执行分配 + 通知）**

用 `D22890`/`P@ssw0rd2026!`（孙玲）登录 `http://localhost:3010`，进入服务目录，提交一次新的
"Copilot采购申请"。

依次用 `D23285`（陈雅芳/部门负责人）、`D00756`（王东峰/总经理）、`D32219`（王青/IT总监）登录，在
"我的待办"里批准对应审批节点。

IT 总监批准后，验证：

```bash
export PGPASSWORD=dev123
psql -h localhost -U itsm_user -d itsm -c "
select t.id, t.ticket_number, t.assignee_id, t.status
from tickets t
where t.requester_id = 175
order by t.id desc limit 1;"
```

Expected: 最新那张工单的 `assignee_id` 是刚才加进服务台-L1 的用户 ID（331），`status` 是 `assigned`——
这是 Task 2 里 `createUserTask` 的 fulfillment 分支回写 `ent.Ticket` 生效的直接证据。

```bash
psql -h localhost -U itsm_user -d itsm -c "
select pi.id, pt.task_name, pt.status, pt.assignee
from process_instances pi join process_tasks pt on pt.process_instance_id = pi.id
where pi.process_definition_key = 'copilot_procurement_flow'
order by pi.id desc, pt.id desc limit 5;"
```

Expected: 能看到 `开通许可证账号`（`Activity_Execute`）任务，`assignee` 是 331。

Run: `grep "notify_handler\|Handler notified via BPMN" /home/administrator/project/itsm/itsm-backend/logs/itsm.log | tail -5`
Expected: 能看到 "Handler notified via BPMN" 日志（`ticket_handler.go:239`），证明 `notify_handler`
节点真的执行了、给处理人发了通知，不是"顺便带上"。

- [ ] **Step 6: 验证"无候选人"路径不再广播**

```bash
psql -h localhost -U itsm_user -d itsm -c "UPDATE users SET team_users=NULL WHERE id=331;"
```

重复 Step 5 的提交+审批流程（走到 IT 总监批准），这次服务台-L1 又变回空团队。验证：

```bash
grep "执行任务未在目标团队解析到可用处理人" /home/administrator/project/itsm/itsm-backend/logs/itsm.log | tail -3
```

Expected: 能看到这条警告日志。再确认没有任何邮件广播：

```bash
grep "Email sent via Graph" /home/administrator/project/itsm/itsm-backend/logs/itsm.log | tail -10
```

Expected: 只看到给这次提交人（孙玲）的一封"工单已创建"邮件，**没有**任何除她之外的收件人。

- [ ] **Step 7: 清理测试数据（可选，视是否要保留环境干净程度决定）**

本轮 Step 5/6 会在 `tickets`/`process_instances`/`process_tasks` 里留下新的测试记录，参照本次会话早前
清理孤儿数据的方式，如果需要保持环境干净，用同样的 `LEFT JOIN`/`NOT EXISTS` 方式核对后再删，不要盲目
`DELETE FROM tickets`。
