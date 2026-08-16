# Track4：变更审批状态机迁移到 BPMN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handlers/change` 包的变更审批（提交审批 → CAB 审批通过/驳回）真正由 BPMN 流程引擎驱动，删除包内自己维护的、独立于 BPMN 的 `ApprovalRecord`/`ApprovalChain` 状态机和 P0-1 追认补丁。

**Architecture:** `SubmitChange` 触发 `change_normal_flow`/`change_emergency_flow` BPMN 流程实例（复用 `ProcessTriggerService`，参照 `IncidentService`/`TicketService` 已有模式）。`TransitionStatus` 的 approve/reject 分支改成查找该变更对应的运行中流程实例的待办用户任务并调用 `CompleteTask`，完成 CAB 审批后在同一次调用里级联完成紧接着路由到的排期/驳回节点（避免孤儿任务）。审批历史改成读 BPMN 的 `ProcessApprovalDecision` 审计表。`start`/`complete`/`rollback`/`cancel` 这四个非审批状态转换、`Activity_Implement`/`Verify`/`Close` 这些实施阶段节点不在这次范围内。

**Tech Stack:** Go/Gin/Ent，`nitram509` 系风格的自研 BPMN 引擎（`CustomProcessEngine`），`stretchr/testify` + `enttest`。

**Spec:** `docs/superpowers/specs/2026-08-13-approval-bpmn-convergence-completion-design.md`（剩余工作④章节）——本计划对该章节两处描述做了订正，见下方"跟 spec 的偏差"。

## 跟 spec 的偏差（写计划前调研发现，执行时以本计划为准）

- spec 说"`CreateChange`/`SubmitChange` 改成调用 `ProcessTriggerService`"——实际上只有 `SubmitChange` 触发审批（`CreateChange` 只建草稿，没有任何状态转换）。这次只改 `SubmitChange`。
- spec 假设"删除 `handlers/change` 包内自己的 `ApprovalRecord`/`ApprovalChain` 类型"是这次收尾能做的全部——实际调研发现 `ChangeServiceTaskHandler`（BPMN 回调，`service/bpmn/change_handler.go`）写入的状态值（`pending_approval` 等）不在 `dto.ChangeStatus` 规范集合里、且完全绕过 `IsValidChangeStatusTransition`，这是一个必须先修的阻塞性 bug，不修的话 BPMN 节点一完成就会把 `Change` 写成非法状态。加进了 Task 1。
- CAB 审批节点（`Activity_CABApproval`）完成后，网关会路由到 `Activity_Schedule`（approve）或 `Activity_Reject`（reject）——这两个也是 `userTask`，目前没有任何机制会完成它们，会变成永远挂起的孤儿任务。最初设想"把这两个节点改成 `serviceTask` 让引擎自动执行"不可行——`serviceTask`（`service/bpmn_types.go` 的 `BPMNServiceTask`）没有 `service_task_type`/`action` 这类 `extensionElements`/`metaData`，走的是 `serviceRef`（ID/Class/Implementation/DelegateExpression 里取一个当 handler 查找 key）+ `handler.Execute(ctx, nil, taskVariables)`（第二个参数是 `nil`，不是真实 task），跟 `userTask` 的声明式 `service_task_type`/`action` 机制完全不兼容，强行复用会有摩擦。改成在 `TransitionStatus` 里显式级联完成（Task 4），不改引擎核心代码。

## Global Constraints

- 不删除 `change_approvals`/`change_approval_chains` 两张物理表——这次只让代码不再读写它们，历史数据允许保留查询。DROP TABLE 是破坏性操作，不在这次范围内。
- `start`/`complete`/`rollback`/`cancel` 这四个 `TransitionStatus` 动作、以及 `Activity_Implement`/`Activity_Verify`/`Activity_Close`/`Activity_Assessment` 这几个 BPMN 节点，都不在这次范围——继续保留现状，不接入/不依赖 BPMN 任务完成。CAB 审批通过后，流程实例合理地停在新生成的 `Activity_Implement` 待办任务上，这是预期行为，不是要修的 bug。
- `BPMNApprovalBridge`（`service/bpmn_approval_bridge_service.go`）这个文件和类型本身不能删——`service/ticket_workflow_service.go`、`service/release_service.go` 还在用它。只删 `handlers/change/service.go` 里对它的字段/构造/调用（P0-1 补丁）。
- `GET /changes/:id/approvals`（前端 `ChangeDetail.tsx` 在用）返回的 `ApprovalRecord` 字段形状（`id`/`changeId`/`approverId`/`approverName`/`status`/`comment`/`approvedAt`/`createdAt`）必须保持不变，数据源换成 `ProcessApprovalDecision` 之后前端不需要改。
- `POST /changes/:id/approvals`（`SubmitApproval`，前端完全没调用，是绕过 chain 机制的第二条不一致审批路径）和 `ConfigureWorkflow`（没有路由，真正的死代码）直接删除，不迁移。
- 状态机权威定义是 `service/change_service.go` 的 `IsValidChangeStatusTransition`（不是 `handlers/change` 包自己重新发明的规则）——`pending` 会被归一化成 `submitted` 再判断，`submitted → approved`/`submitted → rejected` 都是合法转换。
- `CompleteTask`（`service/bpmn_process_engine.go:278`）不接受 actor 作为参数，必须通过 `context.WithValue(ctx, bpmn.BPMNUserIDContextKey, userID)` 注入；`recordApprovalDecision` 在 `variables["approvalAction"]` 非空但 `actorID <= 0` 时会返回 error 并导致整个 `CompleteTask` 失败——所有调用点必须先注入这个 context。

---

### Task 1：修复 `ChangeServiceTaskHandler` 的状态写入合法性

**Files:**
- Modify: `itsm-backend/service/bpmn/change_handler.go`
- Test: `itsm-backend/service/bpmn/change_handler_test.go`（如果不存在就新建）

**Interfaces:**
- Consumes: `service.IsValidChangeStatusTransition(currentStatus, newStatus, changeType string) bool`（已存在，`service/change_service.go:116`）
- Produces: `ChangeServiceTaskHandler.Execute` 对 `approve_change`/`schedule_change`/`reject_change` 三个 action 的处理逻辑，供 Task 4 依赖（Task 4 会调用 `CompleteTask` 触发这几个 action 对应的回调，需要它们写入合法状态）

当前 `service/bpmn/change_handler.go` 里 `Execute` 对 `approve_change` 这个 action（`Activity_CABApproval` 节点自己声明的固定 action）的处理是 `approveChange`，把 `Change.Status` 写成 `"pending_approval"`——这个值不在 `dto.ChangeStatus` 规范集合里，且没有过 `IsValidChangeStatusTransition` 校验。真正的终态判定应该发生在 CAB 审批**之后**紧接着路由到的 `schedule_change`（approve 分支）/`reject_change`（reject 分支）这两个 action 上，`approve_change` 这个 action 本身不应该改变 `Change.Status`（因为它在 approve 和 reject 两种情况下都会被触发，节点自己的 action 是固定的，不代表审批结果）。

- [ ] **Step 1: 写失败的测试**

在 `itsm-backend/service/bpmn/change_handler_test.go` 里添加（如果文件不存在，先看 `service/bpmn/` 目录下其他 `*_handler_test.go` 的 import/setup 写法保持风格一致，这里给出完整内容）：

```go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

func newChangeHandlerTestClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:change_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	return client
}

func createTestChangeForHandler(t *testing.T, client *ent.Client, tenantID int, status string) *ent.Change {
	t.Helper()
	c, err := client.Change.Create().
		SetTitle("测试变更").
		SetType("normal").
		SetStatus(status).
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetTenantID(tenantID).
		SetCreatedBy(1).
		Save(context.Background())
	require.NoError(t, err)
	return c
}

func TestChangeServiceTaskHandler_ApproveChangeAction_DoesNotWriteInvalidStatus(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "approve_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status,
		"approve_change 这个 action 在 CAB 审批节点和排期节点都会触发，本身不代表审批结果，不应该改变 Change.Status")
}

func TestChangeServiceTaskHandler_ScheduleChangeAction_WritesApproved(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "schedule_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status)
}

func TestChangeServiceTaskHandler_RejectChangeAction_WritesRejected(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "reject_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status)
}

func TestChangeServiceTaskHandler_ScheduleChangeAction_InvalidTransitionRejected(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	// rejected 是终态，不允许再转成 approved —— IsValidChangeStatusTransition 必须真的被遵守。
	c := createTestChangeForHandler(t, client, 1, "rejected")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "schedule_change",
		"business_id": float64(c.ID),
	})
	require.Error(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status, "非法转换必须被拒绝，不能静默写入")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestChangeServiceTaskHandler_ -v`
Expected: `ApproveChangeAction_DoesNotWriteInvalidStatus` 和 `InvalidTransitionRejected` 两条 FAIL（当前 `approveChange` 写 `pending_approval` 且不做状态机校验），`ScheduleChangeAction_WritesApproved`/`RejectChangeAction_WritesRejected` 可能也 FAIL（取决于当前 `scheduleChange`/`rejectChange` 是否已经写了别的值——不管当前状态是什么，先跑一遍确认这四条测试反映的是修复前的真实行为，不要假设）。

- [ ] **Step 3: 修复 `Execute`**

打开 `itsm-backend/service/bpmn/change_handler.go`，找到 `approveChange`/`scheduleChange`/`rejectChange`（或者等价的、`Execute` 里 switch 到这三个 action 分支调用的方法）。改成：

```go
func (h *ChangeServiceTaskHandler) approveChange(ctx context.Context, businessID int, variables map[string]interface{}) error {
	// approve_change 这个 action 在 CAB 审批节点（Activity_CABApproval）本身触发，
	// 不管审批结果是 approve 还是 reject 都会走到这里（节点自己的 action 是固定的，
	// 不代表审批结果）——真正的终态判定在 schedule_change/reject_change。
	// 这里不改 Change.Status，只做一次存在性确认，避免 business_id 无效时静默成功。
	_, err := h.client.Change.Get(ctx, businessID)
	if err != nil {
		return fmt.Errorf("变更不存在: %w", err)
	}
	return nil
}

func (h *ChangeServiceTaskHandler) scheduleChange(ctx context.Context, businessID int, variables map[string]interface{}) error {
	return h.transitionChangeStatus(ctx, businessID, "approved")
}

func (h *ChangeServiceTaskHandler) rejectChange(ctx context.Context, businessID int, variables map[string]interface{}) error {
	return h.transitionChangeStatus(ctx, businessID, "rejected")
}

// transitionChangeStatus 统一做状态机校验后写入，任何调用点都不能绕过
// IsValidChangeStatusTransition —— BPMN 回调跟 handlers/change 自己的
// TransitionStatus 必须遵守同一套状态机规则，不能各自为政。
func (h *ChangeServiceTaskHandler) transitionChangeStatus(ctx context.Context, businessID int, targetStatus string) error {
	c, err := h.client.Change.Get(ctx, businessID)
	if err != nil {
		return fmt.Errorf("变更不存在: %w", err)
	}
	if !service.IsValidChangeStatusTransition(c.Status, targetStatus, c.Type) {
		return fmt.Errorf("无效的状态转换: 从 %q 到 %q", c.Status, targetStatus)
	}
	_, err = h.client.Change.UpdateOneID(businessID).SetStatus(targetStatus).Save(ctx)
	if err != nil {
		return fmt.Errorf("更新变更状态失败: %w", err)
	}
	return nil
}
```

保留原有从 `variables["business_id"]` 解析 `businessID`（`float64`/`int`/`string` 兼容转换）的既有逻辑不变，只替换这三个方法体内部的状态写入部分。如果 `service/bpmn` 包引入 `itsm-backend/service` 会造成循环 import（先检查：`grep -n "^import\|\"itsm-backend/service\"" service/bpmn/change_handler.go` 和 `grep -rn "\"itsm-backend/service/bpmn\"" service/*.go` 两边看是否已经互相引用），如果确实循环，把 `IsValidChangeStatusTransition` 这个纯函数原样复制一份到 `service/bpmn` 包内部（不要整体重构成共享包，那是超出这次范围的重构）并在注释里注明"跟 `service/change_service.go` 的同名函数保持规则一致，两边修改要同步"。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestChangeServiceTaskHandler_ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 跑一下已有的 BPMN 回调测试确认没有破坏**

Run: `cd itsm-backend && go test ./service/... -run 'TestUserTaskWithServiceTaskTypeMetadataTriggersCallback|TestCABApproval' -v`
Expected: 全部 PASS（这两条测试之前断言的是 `Change.Status` 变成 `pending_approval`/检查 `CurrentActivityID`，如果第一条测试断言了具体状态值，需要跟着这次修复更新断言——找到断言 `pending_approval` 的那一行，改成断言 `Task 1` 修复后的正确值；如果只是检查流程流转而不检查具体状态值，不需要改）。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/service/bpmn/change_handler.go itsm-backend/service/bpmn/change_handler_test.go itsm-backend/service/bpmn_usertask_callback_test.go
git commit -m "fix(bpmn): change 审批回调写入合法状态值，过状态机校验

approve_change（CAB 审批节点自身固定 action，approve/reject 都会触发）
不再直接写 Change.Status=pending_approval（不在 dto.ChangeStatus 规范
集合里）。真正的终态判定移到 schedule_change/reject_change 这两个动作
（对应 CAB 审批后紧邻的排期/驳回节点），且都过
IsValidChangeStatusTransition 校验，不再绕过状态机。"
```

---

### Task 2：`handlers/change` 接入 `ProcessTriggerService`，`SubmitChange` 触发 BPMN 流程

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`
- Modify: `itsm-backend/handlers/change/repository.go`
- Modify: `itsm-backend/handlers/change/repository_impl.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Test: `itsm-backend/handlers/change/service_test.go`（如果不存在就新建，检查 `handler_test.go`/`service_bpmn_bridge_test.go` 的 fixture 写法保持风格一致）

**Interfaces:**
- Consumes: `service.ProcessTriggerServiceInterface`（`service/bpmn_process_trigger_interface.go`，已存在）；`dto.ProcessTriggerRequest`/`dto.BusinessType`（`dto` 包，已存在，需要确认 `dto.BusinessType` 有没有 `change` 这个值，没有就加一个 `BusinessTypeChange = "change"`，先跑 `grep -n "BusinessTypeChange" itsm-backend/dto/*.go` 确认——已经在 `handlers/change/service.go:564` 见到 `string(dto.BusinessTypeChange)` 被使用，说明这个常量已经存在，不需要新增）
- Produces: `Service.SetProcessTriggerService(svc service.ProcessTriggerServiceInterface)`（新增的 setter，供 `internal/bootstrap/app.go` 调用）；`Repository.MarkSubmittedForApproval(ctx, changeID, tenantID int) error`（新增的、只做 draft→pending 状态转换的 repository 方法，供 Task 4/Task 6 知道这是接下来读写 `change_approvals`/`change_approval_chains` 之外的唯一状态转换入口）

`SubmitChange`（`handlers/change/service.go:106-148`）目前调用 `s.repo.SubmitForApproval`，这个方法在同一个事务里做三件事：改 `changes.status`、写 `change_approvals`、写 `change_approval_chains`（`handlers/change/repository_impl.go:317-362`）。这次要把"改状态"和"写这两张表"拆开——继续保留"改状态"，不再写这两张表（Task 6 会连着这两张表的其余 repository 方法一起删，这里先不动那些方法本身，只是不再从 `SubmitChange` 调用到写表的部分）。

- [ ] **Step 1: 写失败的测试——`SubmitChange` 应该触发 BPMN 流程**

在 `itsm-backend/handlers/change/service_test.go` 里添加（这个测试需要一个能被注入的 mock `ProcessTriggerServiceInterface`——先检查 `service/bpmn_process_trigger_interface.go` 的完整接口定义，确认要 mock 哪些方法，下面假设接口只有 `TriggerProcess`/`TriggerByBusinessType` 等少数方法，实现一个最小 mock）：

```go
package change

import (
	"context"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

type mockProcessTriggerService struct {
	triggerCalls []*dto.ProcessTriggerRequest
	triggerErr   error
	existingRunningInstance bool
}

func (m *mockProcessTriggerService) TriggerProcess(ctx context.Context, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error) {
	m.triggerCalls = append(m.triggerCalls, req)
	if m.triggerErr != nil {
		return nil, m.triggerErr
	}
	return &dto.ProcessTriggerResponse{ProcessInstanceID: 1, ProcessDefinitionKey: req.ProcessDefinitionKey, BusinessKey: "change:1", Status: "running"}, nil
}

func newChangeServiceTestClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:change_svc_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	return client
}

func TestSubmitChange_TriggersBPMNProcess_Normal(t *testing.T) {
	client := newChangeServiceTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	tenant, err := client.Tenant.Create().SetName("T").SetCode("t1").SetDomain("t1.example.com").SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername("requester").SetEmail("r@example.com").SetName("Requester").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(context.Background())
	require.NoError(t, err)
	c, err := repo.Create(context.Background(), &Change{Title: "普通变更", Type: "normal", Status: "draft", TenantID: tenant.ID, CreatedBy: user.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(context.Background(), c.ID, tenant.ID, user.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{user.ID}})
	require.NoError(t, err)

	require.Len(t, trigger.triggerCalls, 1)
	assert.Equal(t, "change_normal_flow", trigger.triggerCalls[0].ProcessDefinitionKey)
	assert.Equal(t, dto.BusinessTypeChange, trigger.triggerCalls[0].BusinessType)
	assert.Equal(t, c.ID, trigger.triggerCalls[0].BusinessID)
}

func TestSubmitChange_TriggersBPMNProcess_Emergency(t *testing.T) {
	client := newChangeServiceTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	tenant, err := client.Tenant.Create().SetName("T2").SetCode("t2").SetDomain("t2.example.com").SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername("requester2").SetEmail("r2@example.com").SetName("Requester2").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(context.Background())
	require.NoError(t, err)
	c, err := repo.Create(context.Background(), &Change{Title: "紧急变更", Type: "emergency", Status: "draft", TenantID: tenant.ID, CreatedBy: user.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(context.Background(), c.ID, tenant.ID, user.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{user.ID}})
	require.NoError(t, err)

	require.Len(t, trigger.triggerCalls, 1)
	assert.Equal(t, "change_emergency_flow", trigger.triggerCalls[0].ProcessDefinitionKey)
}

func TestSubmitChange_NoLongerWritesLegacyApprovalTables(t *testing.T) {
	client := newChangeServiceTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	svc.SetProcessTriggerService(&mockProcessTriggerService{})

	tenant, err := client.Tenant.Create().SetName("T3").SetCode("t3").SetDomain("t3.example.com").SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername("requester3").SetEmail("r3@example.com").SetName("Requester3").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(context.Background())
	require.NoError(t, err)
	c, err := repo.Create(context.Background(), &Change{Title: "变更", Type: "normal", Status: "draft", TenantID: tenant.ID, CreatedBy: user.ID})
	require.NoError(t, err)

	updated, err := svc.SubmitChange(context.Background(), c.ID, tenant.ID, user.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{user.ID}})
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status)

	var count int
	require.NoError(t, client.QueryContext(context.Background()).Scan(&count)) // 占位——实际改成下面这样：
}
```

上面最后一个测试的 `QueryContext` 那行是占位、写错了，删掉，改成直接用原生 SQL 查表确认没有写入（`change_approvals`/`change_approval_chains` 不是 Ent 表，不能用 Ent client 查，要用 `client.DB()` 之类拿到底层 `*sql.DB` 或者直接在 repository 层加一个测试专用的计数方法）。查一下 `handlers/change/repository_impl.go` 顶部 `Repository` struct 有没有暴露底层 `*sql.DB`（比如字段名 `db`），如果有私有字段拿不到，就直接跳过这条"表真的没被写入"的断言，只保留"状态确实变成了 pending"这条断言——因为 Task 6 会把这两张表的写入方法整个删掉，届时编译期就能保证不会再被调用，不需要在这一步专门测"没写入"这件事。删掉 `TestSubmitChange_NoLongerWritesLegacyApprovalTables` 这个测试，只保留前两条。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestSubmitChange_TriggersBPMNProcess -v`
Expected: 编译失败（`SetProcessTriggerService` 未定义）。

- [ ] **Step 3: 实现**

在 `itsm-backend/handlers/change/service.go` 的 `Service` struct（第 20-26 行）加一个字段：

```go
type Service struct {
	repo             Repository
	logger           *zap.SugaredLogger
	entClient        *ent.Client
	pirService       *service.ChangePIRService
	approvalBridge   *service.BPMNApprovalBridge
	processTriggerService service.ProcessTriggerServiceInterface
}
```

紧接着 `NewService` 函数（第 28-41 行）之后加：

```go
// SetProcessTriggerService 注入 BPMN 流程触发服务——参照 IncidentService/TicketService
// 的 SetProcessTriggerService 模式（internal/bootstrap/app.go 里同样的 setter 注入方式），
// 不是构造函数参数，避免循环依赖初始化顺序问题。
func (s *Service) SetProcessTriggerService(svc service.ProcessTriggerServiceInterface) {
	s.processTriggerService = svc
}
```

把 `SubmitChange`（`service.go:106-148`）里这一段：

```go
	if err := s.repo.SubmitForApproval(ctx, changeID, tenantID, req.ApproverIDs, req.Comment); err != nil {
		s.logger.Warnw("Failed to atomically submit change", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("提交变更审批失败: %w", err)
	}
```

改成：

```go
	if err := s.repo.MarkSubmittedForApproval(ctx, changeID, tenantID); err != nil {
		s.logger.Warnw("Failed to mark change as submitted", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("提交变更审批失败: %w", err)
	}

	if s.processTriggerService != nil {
		processDefKey := "change_normal_flow"
		if c.Type == "emergency" {
			processDefKey = "change_emergency_flow"
		}
		// 幂等保护：同一个 change 不应该有两个并行的运行中流程实例（比如重复点击提交、
		// 或者前端重试）。businessKey 的约定跟 BPMNApprovalBridge.findPendingApprovalTask
		// 保持一致（"change:{id}"），查询逻辑复用 ent 直接查，不新增一层抽象。
		businessKey := fmt.Sprintf("change:%d", changeID)
		exists, err := s.entClient.ProcessInstance.Query().
			Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查是否已有运行中审批流程失败: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("该变更已经有一个正在进行的审批流程，不能重复提交")
		}

		_, err = s.processTriggerService.TriggerProcess(ctx, &dto.ProcessTriggerRequest{
			BusinessType:         dto.BusinessTypeChange,
			BusinessID:           changeID,
			ProcessDefinitionKey: processDefKey,
			Variables: map[string]interface{}{
				"approval_required": true,
				"requester_id":      float64(submitterID),
			},
			TriggeredBy: fmt.Sprintf("%d", submitterID),
			TenantID:    tenantID,
		})
		if err != nil {
			s.logger.Errorw("SubmitChange: failed to trigger BPMN process", "error", err, "change_id", changeID)
			return nil, fmt.Errorf("启动审批流程失败: %w", err)
		}
	}
```

`ent/processinstance` 需要加进 import 块（`"itsm-backend/ent/processinstance"`）。`variables["approval_required"]` 这里硬编码 `true`——`change_normal_flow`/`change_emergency_flow` 的 `Gateway_Approval` 网关读的就是这个变量决定要不要走 CAB 审批，变更这个业务域目前没有"免审批"的产品需求（跟 ticket/service_request 不一样，那两个域有 `need_approval`/`approval_required` 可能为 false 的场景），所以这里固定传 `true`，不留一个"以后可能要读 change 自己某个字段决定"的抽象——YAGNI。

在 `itsm-backend/handlers/change/repository.go` 的 `Repository` 接口里，把：

```go
	SubmitForApproval(ctx context.Context, changeID, tenantID int, approverIDs []int, comment string) error
```

改成额外加一个新方法（先不删旧的，Task 6 再删）：

```go
	SubmitForApproval(ctx context.Context, changeID, tenantID int, approverIDs []int, comment string) error
	MarkSubmittedForApproval(ctx context.Context, changeID, tenantID int) error
```

在 `itsm-backend/handlers/change/repository_impl.go` 里，紧挨着 `SubmitForApproval` 方法（第 317 行附近）之前加一个新方法：

```go
// MarkSubmittedForApproval 只做 draft -> pending 的状态转换，不写
// change_approvals/change_approval_chains（这两张表的写入路径正在被
// Track4 迁移到 BPMN，见 handlers/change/service.go 的 SubmitChange）。
// 用跟 SubmitForApproval 相同的乐观守卫：要求恰好 1 行受影响，否则说明
// change 已经不是 draft 状态了。
func (r *Repository) MarkSubmittedForApproval(ctx context.Context, changeID, tenantID int) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE changes SET status = 'pending', updated_at = NOW() WHERE id = $1 AND tenant_id = $2 AND status = 'draft'`,
		changeID, tenantID)
	if err != nil {
		return fmt.Errorf("更新变更状态失败: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取受影响行数失败: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("change is not an editable draft")
	}
	return nil
}
```

（具体 SQL 方言/参数占位符风格要跟 `SubmitForApproval` 现有实现保持一致——先读一遍 `SubmitForApproval` 完整实现确认是 `$1`/`$2`（Postgres 风格）还是别的，上面按 Postgres 风格写的，如果不对就改成跟现有代码一致的风格。）

在 `itsm-backend/internal/bootstrap/app.go` 里找到 `changeServiceDomain := change.NewService(changeRepo, client, sugar)` 这一行（大约第 512 行），紧接着加：

```go
	changeServiceDomain.SetProcessTriggerService(processTriggerService)
```

（`processTriggerService` 这个变量名要跟文件里 `ticketService.SetProcessTriggerService(processTriggerService)`/`incidentService.SetProcessTriggerService(processTriggerService)` 用的是同一个变量，确认这行在 `changeServiceDomain` 构造之后、且 `processTriggerService` 已经初始化完成的位置——照抄 ticket/incident 那两行的相对位置。）

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestSubmitChange_TriggersBPMNProcess -v`
Expected: 两条 PASS。

- [ ] **Step 5: 跑 handlers/change 全部测试 + 整个后端编译**

Run: `cd itsm-backend && go build ./... && go test ./handlers/change/... -v 2>&1 | tail -100`
Expected: 编译通过；已有的 `TestSubmitChange` 相关测试（如果有断言 `change_approvals` 表被写入的旧测试）可能需要更新或删除——如果发现有测试直接断言 `SubmitForApproval` 写了这两张表的行为，在这一步先不动它（`SubmitForApproval` 方法本身还没删，Task 6 才删，这一步这些旧测试应该仍然通过，因为只是新增了 `MarkSubmittedForApproval` 和触发调用，没有删除旧方法本身）。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/repository.go itsm-backend/handlers/change/repository_impl.go itsm-backend/handlers/change/service_test.go itsm-backend/internal/bootstrap/app.go
git commit -m "feat(change): SubmitChange 触发 BPMN 审批流程

参照 IncidentService/TicketService 的 SetProcessTriggerService 注入模式。
提交审批时按 Type==emergency 选择 change_emergency_flow，否则
change_normal_flow。加了重复提交的幂等保护（同一 businessKey 已有运行中
实例时拒绝）。draft->pending 状态转换拆成新的 MarkSubmittedForApproval，
不再写 change_approvals/change_approval_chains（旧方法 SubmitForApproval
暂时保留，Task 6 统一清理）。"
```

---

### Task 3：CAB 审批完成 + 级联完成排期/驳回节点（避免孤儿任务）

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`
- Test: `itsm-backend/handlers/change/service_bpmn_bridge_test.go`（已存在，扩展它，不新建文件——这个文件已经是"TransitionStatus ↔ BPMN 桥接集成测试"的地方）

**Interfaces:**
- Consumes: `CustomProcessEngine.CompleteTask(ctx, taskID string, variables map[string]interface{}) error`（`service/bpmn_process_engine.go:278`，通过 `Service.entClient` 拿不到，需要新加一个 `ProcessEngine` 依赖）；`bpmn.BPMNUserIDContextKey`/`bpmn.BPMNTenantIDContextKey`（`service/bpmn` 包）
- Produces: `Service.SetProcessEngine(engine service.ProcessEngine)`（新增 setter）；`Service.completeChangeApprovalTask(ctx, tenantID, actorUserID, changeID int, action, comment string) error`（私有方法，Task 4 直接在 `TransitionStatus` 里调用）

这一步不改 `TransitionStatus` 本身（那是 Task 4），先把"完成 CAB 审批任务 + 级联完成下一个节点"这个核心逻辑实现出来并测试到位，降低 Task 4 的复杂度。

- [ ] **Step 1: 写失败的测试**

在 `itsm-backend/handlers/change/service_bpmn_bridge_test.go` 里加（这个文件已经有 `newChangeBridgeEntClient`/`setupChangeBridgeActor`/`createChangeBridgeProcessFixture` 这些 helper，复用它们；但 `createChangeBridgeProcessFixture` 用的是单节点合成 fixture，这次需要真实部署 `change_normal_flow` 模板才能测到 `Gateway_ApprovalResult` 之后的级联，所以新测试用 `BPMNTemplateService.LoadAndDeployTemplates` 而不是那个合成 fixture）：

```go
func TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_approve")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant").SetCode("cab-approve").SetDomain("cab-approve.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm1").SetEmail("cm1@example.com").SetName("CM1").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err) // 如果 UserRole 这个 ent 类型/角色赋予方式不对，改成 grep "createUserWithRole" 找现有测试怎么给用户赋角色，照抄那个写法

	c, err := client.Change.Create().SetTitle("测试变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(cmUser.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger) // grep 一下现有测试文件里怎么构造 *CustomProcessEngine 或者拿到 service.ProcessEngine 接口实现，照抄那个 helper，不要自己发明构造方式
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType: dto.BusinessTypeChange, BusinessID: c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(cmUser.ID)},
		TenantID:              tenant.ID,
	})
	require.NoError(t, err)

	// 完成"变更评估"这一步，推进到 CAB 审批
	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.CompleteChangeApprovalTaskForTest(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good")
	require.NoError(t, err)

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status, "CAB 通过后应该级联完成排期节点，Change 状态应该变成 approved")

	instances, err := client.ProcessInstance.Query().Where(processinstance.BusinessID(fmt.Sprintf("%d", c.ID))).All(ctx)
	// 如果 ProcessInstance 没有直接的 business_id 字段查询方式，改用
	// processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)) 查，参照 Task 2 用的同一个约定
	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "Activity_Implement", instances[0].CurrentActivityID,
		"级联完成排期节点后，流程应该停在变更实施这个新任务上——这是 Track4 范围边界之外的预期停留点，不是 bug")
}

func TestCompleteChangeApprovalTask_RejectEndsProcess(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_reject")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant Reject").SetCode("cab-reject").SetDomain("cab-reject.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm2").SetEmail("cm2@example.com").SetName("CM2").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-驳回").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(cmUser.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType: dto.BusinessTypeChange, BusinessID: c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(cmUser.ID)},
		TenantID:              tenant.ID,
	})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "reject", "风险过高，驳回")
	require.NoError(t, err)

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", instance.Status, "驳回节点走 Flow_End 直接结束流程实例，不应该卡在 running")
}

func TestCompleteChangeApprovalTask_WrongActorRejected(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_wrong_actor")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant WrongActor").SetCode("cab-wrong-actor").SetDomain("cab-wrong-actor.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	// cmUser 有 change_manager 角色，用来推进流程到 CAB 审批节点；outsider 没有该角色。
	cmUser, err := client.User.Create().SetUsername("cm3").SetEmail("cm3@example.com").SetName("CM3").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err)
	outsider, err := client.User.Create().SetUsername("outsider").SetEmail("outsider@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-越权").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(cmUser.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType: dto.BusinessTypeChange, BusinessID: c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(cmUser.ID)},
		TenantID:              tenant.ID,
	})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, outsider.ID, c.ID, "approve", "我批准")
	require.Error(t, err, "authorizeTaskActor 应该拒绝没有 change_manager 角色的用户")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "越权调用失败后 Change.Status 不应该被改动")
}
```

上面的测试代码里有两处需要实现者自己先调研确认再落笔（已经在注释里标出）：`newTestBPMNEngine` 这个 helper 怎么构造（照抄 `bpmn_process_engine_approval_assignment_test.go` 或者 `service_bpmn_bridge_test.go` 里已有的构造方式，不要自己发明）、`client.UserRole.Create().SetUserID(...).SetTenantID(...).SetRoleName("change_manager")` 这个赋角色写法要跟 `bpmn_process_engine_approval_assignment_test.go` 的 `createUserWithRole` helper 实际使用的 ent 调用方式核对一致，如果字段名/方法名不同，以现有代码为准替换掉这里的写法，不要自己发明新的赋角色路径。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestCompleteChangeApprovalTask -v`
Expected: 编译失败（`SetProcessEngine`/`CompleteChangeApprovalTaskForTest` 未定义）。

- [ ] **Step 3: 实现**

在 `itsm-backend/handlers/change/service.go` 的 `Service` struct 里再加一个字段：

```go
	processEngine service.ProcessEngine
```

加 setter：

```go
func (s *Service) SetProcessEngine(engine service.ProcessEngine) {
	s.processEngine = engine
}
```

加核心方法（放在 `TransitionStatus` 之前，因为 Task 4 会从 `TransitionStatus` 调用它）：

```go
// completeChangeApprovalTask 完成一个变更的 CAB 审批任务，并在通过/驳回后级联完成
// 紧邻的排期/驳回节点（Activity_Schedule/Activity_Reject）——这两个节点在 BPMN 图里
// 是 userTask，没有任何机制会自动完成它们，不级联完成会变成永远挂起的孤儿任务。
// 级联到此为止：Activity_Schedule 完成后流程会走到 Activity_Implement，那是
// Track4 范围之外的下一阶段，故意让它停在那里，不继续级联。
func (s *Service) completeChangeApprovalTask(ctx context.Context, tenantID, actorUserID, changeID int, action, comment string) error {
	if s.processEngine == nil {
		return fmt.Errorf("流程引擎未初始化")
	}

	businessKey := fmt.Sprintf("change:%d", changeID)
	instance, err := s.entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("该变更没有正在运行的审批流程")
		}
		return fmt.Errorf("查询审批流程实例失败: %w", err)
	}

	task, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
			processtask.TaskType("user_task"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("该变更没有待处理的审批任务")
		}
		return fmt.Errorf("查询待办审批任务失败: %w", err)
	}

	approvalResult := "rejected"
	if action == "approve" {
		approvalResult = "approved"
	}

	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorUserID)
	actorCtx = context.WithValue(actorCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	if err := s.processEngine.CompleteTask(actorCtx, task.TaskID, map[string]interface{}{
		"approvalAction":  action,
		"approvalResult":  approvalResult,
		"approvalComment": comment,
	}); err != nil {
		return fmt.Errorf("完成审批任务失败: %w", err)
	}

	// 级联完成排期/驳回节点。这一步用系统身份（不注入 actorUserID），因为它不是
	// 一个新的、独立的人工决定，是上面那次审批决定的自动延伸——如果这里也要求
	// actorUserID 通过 assignee/candidateUsers 校验，会因为 Activity_Schedule/
	// Activity_Reject 没有声明 assigneeRole 而始终失败。
	nextTask, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
			processtask.TaskType("user_task"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 没有下一个任务——理论上不应该发生（Gateway_ApprovalResult 总是路由到
			// Activity_Schedule 或 Activity_Reject 之一），但不要因为找不到就整体失败，
			// 上面那次真正的审批决定已经生效了。
			s.logger.Warnw("completeChangeApprovalTask: 未找到审批后紧邻的任务，跳过级联完成", "change_id", changeID)
			return nil
		}
		return fmt.Errorf("查询审批后续任务失败: %w", err)
	}
	if err := s.processEngine.CompleteTask(ctx, nextTask.TaskID, map[string]interface{}{}); err != nil {
		return fmt.Errorf("级联完成审批后续任务失败: %w", err)
	}
	return nil
}

// CompleteChangeApprovalTaskForTest 是 completeChangeApprovalTask 的导出包装，
// 仅供本包测试使用（同包测试其实可以直接调小写方法，这个包装是为了让测试文件
// 显式表达"这是在测公开行为，不是在测内部实现细节"——如果 Go 静态检查工具认为
// 这个包装多余，直接删掉，测试里改成调 svc.completeChangeApprovalTask）。
func (s *Service) CompleteChangeApprovalTaskForTest(ctx context.Context, tenantID, actorUserID, changeID int, action, comment string) error {
	return s.completeChangeApprovalTask(ctx, tenantID, actorUserID, changeID, action, comment)
}
```

`processtask`/`processinstance` 两个 ent 包需要确认已经在 import 块里（`processinstance` 应该 Task 2 已经加过，`processtask` 这次新加：`"itsm-backend/ent/processtask"`）。`bpmn` 包（`"itsm-backend/service/bpmn"`，给 `bpmn.BPMNUserIDContextKey`/`BPMNTenantIDContextKey` 用）也要确认已 import。

注：`CompleteChangeApprovalTaskForTest` 这个包装方法命名不符合 CLAUDE.md 的常规约定（不应该为了测试专门导出一个方法），实现者应该优先选择更干净的做法——测试文件如果跟 `service.go` 在同一个 `package change` 下（`service_bpmn_bridge_test.go` 顶部 `package change`，已确认是同包），可以直接调用小写的 `completeChangeApprovalTask`，不需要这个包装。上面 Step 1 的测试代码和这里都保留了这个包装写法只是防止实现者没注意到同包这件事，实现时应该删掉 `CompleteChangeApprovalTaskForTest`，测试直接调 `svc.completeChangeApprovalTask(...)`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestCompleteChangeApprovalTask -v`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/service_bpmn_bridge_test.go
git commit -m "feat(change): 完成 CAB 审批任务时级联完成排期/驳回节点

Activity_Schedule/Activity_Reject 是 CAB 审批网关路由到的下一个 userTask，
没有机制会自动完成它们，不级联完成会变成孤儿任务、流程实例永远卡在
running。级联用系统身份完成（不校验 actor，因为这不是独立的人工决定，
是审批结果的自动延伸），级联到 Activity_Implement 为止——那是 Track4
范围之外的下一阶段，故意停在那里。"
```

---

### Task 4：`TransitionStatus` 的 approve/reject 改成走 BPMN，移除 P0-1 桥接

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`
- Test: `itsm-backend/handlers/change/service_bpmn_bridge_test.go`

**Interfaces:**
- Consumes: `Service.completeChangeApprovalTask`（Task 3 产出）
- Produces: 无新符号——这一步是替换 `TransitionStatus` 内部实现，对外签名不变

`TransitionStatus`（`service.go:526-624`）目前 approve/reject 分支做的事：① 扫 `ApprovalHistory` 确认 userID 有一条 pending 记录（业务侧校验）；② 调 `approvalBridge.CompleteBusinessApprovalTask`（P0-1 桥接，静默 no-op 语义）；③ 手动更新 `ApprovalRecord`；④ 手动更新 `Change.Status`。这次要把 ①②③④ 全部替换成一次 `completeChangeApprovalTask` 调用——审批人校验交给 BPMN 的 `authorizeTaskActor`（基于 `assigneeRole="change_manager"` 解析出的候选人），状态写入交给 Task 1 修好的 `ChangeServiceTaskHandler` 回调，不再有业务侧的平行状态机。

- [ ] **Step 1: 写失败的测试**

在 `itsm-backend/handlers/change/service_bpmn_bridge_test.go` 里加（复用 Task 3 已经写好的 fixture 搭建方式）：

```go
// setupChangeForTransitionStatusTest 是本任务三条测试共用的 fixture 搭建：建
// tenant/change_manager 用户/change，部署 BPMN 模板，触发 change_normal_flow，
// 完成变更评估任务，推进到 CAB 审批节点。返回 client/svc/tenant/cmUser/change 供
// 各测试按需使用。dbName 必须每个测试唯一，避免 sqlite 内存库互相污染。
func setupChangeForTransitionStatusTest(t *testing.T, dbName string) (*ent.Client, *Service, *ent.Tenant, *ent.User, *ent.Change) {
	t.Helper()
	client := newChangeBridgeEntClient(t, dbName)
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("TransitionStatus Tenant").SetCode(dbName).SetDomain(dbName + ".example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername(dbName + "-cm").SetEmail(dbName + "-cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("TransitionStatus 测试变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(cmUser.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType: dto.BusinessTypeChange, BusinessID: c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(cmUser.ID)},
		TenantID:              tenant.ID,
	})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := NewRepository(client)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)
	svc.SetProcessTriggerService(trigger)

	return client, svc, tenant, cmUser, c
}

func TestTransitionStatus_Approve_UsesCompleteChangeApprovalTask(t *testing.T) {
	client, svc, tenant, cmUser, c := setupChangeForTransitionStatusTest(t, "transition_approve")
	ctx := context.Background()

	_, err := svc.TransitionStatus(ctx, c.ID, tenant.ID, cmUser.ID, "approved", "looks good")
	require.NoError(t, err, "不再要求 ApprovalHistory 里有一条 pending 记录——审批人校验完全交给 BPMN authorizeTaskActor")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status, "状态由 BPMN 回调写入，不是 TransitionStatus 自己手动 set 的")
}

func TestTransitionStatus_Reject_RequiresComment(t *testing.T) {
	client, svc, tenant, cmUser, c := setupChangeForTransitionStatusTest(t, "transition_reject_comment")
	ctx := context.Background()

	_, err := svc.TransitionStatus(ctx, c.ID, tenant.ID, cmUser.ID, "rejected", "")
	require.Error(t, err, "驳回必须填写意见，跟 SubmitTaskDecision 的既有约束保持一致")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "comment 为空时应该在调用 BPMN 之前就被拒绝，状态不应该变")
}

func TestTransitionStatus_Approve_WrongActorRejected(t *testing.T) {
	client, svc, tenant, _, c := setupChangeForTransitionStatusTest(t, "transition_wrong_actor")
	ctx := context.Background()

	outsider, err := client.User.Create().SetUsername("transition-outsider").SetEmail("transition-outsider@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	_, err = svc.TransitionStatus(ctx, c.ID, tenant.ID, outsider.ID, "approved", "我批准")
	require.Error(t, err, "非 change_manager 角色的用户不应该能通过 authorizeTaskActor 校验")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "越权调用失败后不应该残留任何状态变化")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestTransitionStatus_Approve -v`
Expected: FAIL（当前实现走的还是 `ApprovalHistory` 校验 + P0-1 桥接，测试 fixture 没有造 `ApprovalHistory` 记录，会在"用户不是该变更的审批人"这一步就失败）。

- [ ] **Step 3: 实现**

把 `TransitionStatus`（`service.go:526-624`）里这一段：

```go
	// For approval actions, verify user is the approver
	if targetStatus == "approved" || targetStatus == "rejected" {
		history, err := s.repo.GetApprovalHistory(ctx, id, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to get approval history")
		}
		// Find if this user has a pending approval
		isApprover := false
		for _, h := range history {
			if h.ApproverID == userID && h.Status == "pending" {
				isApprover = true
				break
			}
		}
		if !isApprover {
			return nil, fmt.Errorf("用户不是该变更的审批人，无权执行此操作")
		}

		// P0-1：审批先桥接完成对应的 BPMN 待办任务（以流程任务为权威审批来源）。
		// 无关联运行中流程实例时回退为纯业务审批；若存在待办流程任务但完成失败，
		// 则中止业务审批，避免变更状态与流程状态分叉。
		if s.approvalBridge != nil {
			action := "approve"
			if targetStatus == "rejected" {
				action = "reject"
			}
			if _, bridgeErr := s.approvalBridge.CompleteBusinessApprovalTask(
				ctx, tenantID, userID, string(dto.BusinessTypeChange), id, action, comment,
			); bridgeErr != nil {
				return nil, fmt.Errorf("同步流程审批任务失败: %w", bridgeErr)
			}
		}
	}

	// For approve action, update the approval record to approved
	if targetStatus == "approved" {
		history, err := s.repo.GetApprovalHistory(ctx, id, tenantID)
		if err != nil {
			s.logger.Warnw("TransitionStatus: failed to get approval history for record update", "error", err)
		} else {
			for _, h := range history {
				if h.ApproverID == userID && h.Status == "pending" {
					approvedStatus := "approved"
					if _, err := s.repo.UpdateApprovalRecord(ctx, &ApprovalRecord{
						ID:       h.ID,
						TenantID: tenantID,
						Status:   approvedStatus,
					}); err != nil {
						s.logger.Warnw("TransitionStatus: failed to update approval record", "error", err, "record_id", h.ID)
					}
					break
				}
			}
		}
	}
```

整段替换成：

```go
	// For approval actions, delegate entirely to BPMN: actor authorization is
	// authorizeTaskActor (assigneeRole=change_manager candidates), and the
	// terminal status write happens in ChangeServiceTaskHandler's callback
	// (Task 1), not here. This replaces both the legacy ApprovalHistory-based
	// actor check and the P0-1 bridge — there is no longer a business-side
	// shadow state machine to keep in sync.
	if targetStatus == "approved" || targetStatus == "rejected" {
		if targetStatus == "rejected" && strings.TrimSpace(comment) == "" {
			return nil, fmt.Errorf("驳回变更时必须填写意见")
		}
		action := "approve"
		if targetStatus == "rejected" {
			action = "reject"
		}
		if err := s.completeChangeApprovalTask(ctx, tenantID, userID, id, action, comment); err != nil {
			return nil, err
		}
		// 状态已经由 BPMN 回调写入，重新读一次返回给调用方，不要用调用前的 c（陈旧）。
		return s.repo.Get(ctx, id, tenantID)
	}
```

`strings` 包需要确认已经在 import 块（`handlers/change/service.go` 目前应该还没 import `strings`，检查一下加上）。

紧跟着的这一段（`service.go:593-620`，"H-2 / C-2 修复：终态需要事务化"）处理的是 `rejected`/`completed`/`cancelled`/`rolled_back` 这几个终态——现在 `rejected` 已经在上面的分支里 `return` 掉了，不会再走到这里，这段代码保持原样不用动（`completed`/`cancelled`/`rolled_back` 三个不属于这次范围，继续用原来的逻辑）。确认改完之后这段代码里判断 `isTerminal` 的条件 `targetStatus == "rejected" || ...` 虽然 `rejected` 这个分支已经在上面提前 return、实际上永远不会在这里为 true，但不要删掉这个条件本身（删了容易在以后有人调整 approve/reject 分支顺序时引入新 bug，保留这个条件本身没有副作用，只是死代码路径——如果 CLAUDE.md 风格要求不留死分支，改成加一行注释说明"rejected 已经在上面提前返回，这里保留条件是为了防御式编程，理论上不会命中"，不要删除这个 case）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestTransitionStatus_ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 跑 handlers/change 全部现有测试**

Run: `cd itsm-backend && go test ./handlers/change/... -v 2>&1 | tail -150`
Expected: 除了明确要在 Task 6 才删除/更新的旧测试（比如 `handler_test.go` 里针对 `SubmitApproval`/`ConfigureWorkflow` 的测试）之外全部 PASS。如果这一步发现有旧的 approve/reject 相关测试断言了 `ApprovalHistory`/P0-1 桥接的具体行为（比如 `TestTransitionStatus_BridgeFailClosed`），先读一遍那条测试在断言什么——如果它断言的是"BPMN 侧校验失败时 approve 整体失败"这个语义，这次改动之后应该仍然成立（只是实现路径变了），更新测试内部的 fixture/mock 方式让它继续通过，不要删掉这条测试锁定的行为；如果它断言的是已经被这次改动整体替换掉的旧机制细节（比如具体检查 `approvalBridge` 字段是否被调用），删掉这条测试，在 commit message 里说明为什么。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/service_bpmn_bridge_test.go
git commit -m "refactor(change): TransitionStatus 的 approve/reject 完全交给 BPMN

移除 ApprovalHistory 平行审批人校验和 P0-1 追认桥接——审批人校验现在
完全是 authorizeTaskActor（基于 assigneeRole=change_manager 候选人），
终态写入完全是 ChangeServiceTaskHandler 的 BPMN 回调（Task 1），
TransitionStatus 只负责调用 completeChangeApprovalTask（Task 3）然后
重新读取最新状态返回。reject 要求必须填写意见，跟 SubmitTaskDecision
的既有约束保持一致。"
```

---

### Task 5：审批历史读 BPMN `ProcessApprovalDecision`

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`
- Modify: `itsm-backend/handlers/change/repository.go`
- Modify: `itsm-backend/handlers/change/repository_impl.go`
- Test: `itsm-backend/handlers/change/service_test.go`

**Interfaces:**
- Consumes: `ent.ProcessApprovalDecision`（`ent/schema/process_approval_decision.go`，已存在，字段：`ProcessInstanceID`/`ProcessTaskID`/`ProcessInstanceKey`/`TaskID`/`ProcessDefinitionKey`/`NodeKey`/`BusinessType`/`BusinessID`/`ActorID`/`ActorName`/`Action`/`Decision`/`Comment`/`VariablesSnapshot`/`TenantID`/`CreatedAt`）
- Produces: `Repository.GetApprovalHistory` 的实现切换数据源，方法签名/返回类型 `[]*ApprovalRecord` 不变（`GetApprovals`/`GetApprovalHistory` 这两个 handler/service 方法完全不用改，只改 `repository_impl.go` 内部实现）

`GET /changes/:id/approvals` 前端在用，返回扁平的 `ApprovalRecord` 数组（`id`/`changeId`/`approverId`/`approverName`/`status`/`comment`/`approvedAt`/`createdAt`）。这次把数据源从 `change_approvals` 表切换成 `ent.ProcessApprovalDecision`（按 `business_type="change"` + `business_id=<changeID 字符串>` + `tenant_id` 过滤），映射规则：`ApproverID`←`ActorID`，`ApproverName`←`ActorName`，`Status`←`Decision`，`Comment`←`Comment`（指针，`Comment` 为空字符串时映射成 `nil`），`ApprovedAt`←`CreatedAt`（指针），`CreatedAt`←`CreatedAt`。`ID`用 `ProcessApprovalDecision.ID`。

- [ ] **Step 1: 写失败的测试**

```go
func TestGetApprovalHistory_ReadsFromProcessApprovalDecision(t *testing.T) {
	client := newChangeServiceTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("T").SetCode("t-history").SetDomain("t-history.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().SetUsername("cm").SetEmail("cm@example.com").SetName("CM User").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	_, err = client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-test-1").SetTaskID("TASK-test-1").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actor.ID).SetActorName(actor.Name).
		SetAction("approve").SetDecision("approved").SetComment("looks good").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := NewRepository(client)
	history, err := repo.GetApprovalHistory(ctx, 42, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actor.ID, history[0].ApproverID)
	assert.Equal(t, actor.Name, history[0].ApproverName)
	assert.Equal(t, "approved", history[0].Status)
	require.NotNil(t, history[0].Comment)
	assert.Equal(t, "looks good", *history[0].Comment)
}

func TestGetApprovalHistory_TenantIsolation(t *testing.T) {
	client := newChangeServiceTestClient(t)
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().SetName("Tenant A").SetCode("t-iso-a").SetDomain("t-iso-a.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("Tenant B").SetCode("t-iso-b").SetDomain("t-iso-b.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actorA, err := client.User.Create().SetUsername("cm-a").SetEmail("cm-a@example.com").SetName("CM A").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)
	actorB, err := client.User.Create().SetUsername("cm-b").SetEmail("cm-b@example.com").SetName("CM B").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenantB.ID).Save(ctx)
	require.NoError(t, err)

	// 两个租户各自一条 ProcessApprovalDecision，business_id 相同（都是 42）——
	// 这是租户隔离测试的关键：如果查询漏了 tenant_id 过滤，会把两条都返回。
	_, err = client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-iso-a").SetTaskID("TASK-iso-a").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actorA.ID).SetActorName(actorA.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant a").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(2).SetProcessTaskID(2).
		SetProcessInstanceKey("PI-iso-b").SetTaskID("TASK-iso-b").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actorB.ID).SetActorName(actorB.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant b").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantB.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := NewRepository(client)
	history, err := repo.GetApprovalHistory(ctx, 42, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actorA.ID, history[0].ApproverID)
	assert.Equal(t, "tenant a", *history[0].Comment)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestGetApprovalHistory_ -v`
Expected: FAIL（当前实现读的是 `change_approvals` 表，没有这条数据）。

- [ ] **Step 3: 实现**

在 `itsm-backend/handlers/change/repository_impl.go` 里找到现有的 `GetApprovalHistory` 方法（读 `change_approvals` 的那个），替换成：

```go
func (r *Repository) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	decisions, err := r.entClient.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType("change"),
			processapprovaldecision.BusinessID(fmt.Sprintf("%d", changeID)),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询审批历史失败: %w", err)
	}

	records := make([]*ApprovalRecord, 0, len(decisions))
	for _, d := range decisions {
		var comment *string
		if d.Comment != "" {
			c := d.Comment
			comment = &c
		}
		createdAt := d.CreatedAt
		records = append(records, &ApprovalRecord{
			ID:           d.ID,
			ChangeID:     changeID,
			TenantID:     tenantID,
			ApproverID:   d.ActorID,
			ApproverName: d.ActorName,
			Status:       d.Decision,
			Comment:      comment,
			ApprovedAt:   &createdAt,
			CreatedAt:    createdAt,
		})
	}
	return records, nil
}
```

需要确认 `Repository` struct 有没有一个持有 `*ent.Client` 的字段（目前这个 repository 主要用 `*sql.DB`，先 `grep -n "type Repository struct" -A 10 itsm-backend/handlers/change/repository_impl.go` 确认；如果没有 `entClient` 字段，在构造函数 `NewRepository` 里加一个参数/字段，检查 `NewRepository` 目前的签名，`handlers/change/service.go` 里 `NewService(repo, entClient, logger)` 已经拿到了 `entClient`，如果 `NewRepository` 目前不接收 `*ent.Client`，改成接收，`internal/bootstrap/app.go` 里 `NewRepository(...)` 的调用点要跟着改传参）。import 块加 `"itsm-backend/ent/processapprovaldecision"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestGetApprovalHistory_ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 跑 handler_test.go 里既有的 approvals 端点测试**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestHandler_GetApprovals -v` （具体测试函数名先 `grep -n "func Test.*Approval" handlers/change/handler_test.go` 确认）
Expected: PASS 或者需要更新——如果现有测试是拿 `change_approvals` 表数据造的 fixture，改成用 `ProcessApprovalDecision` 造 fixture。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/repository.go itsm-backend/handlers/change/repository_impl.go itsm-backend/handlers/change/service_test.go itsm-backend/handlers/change/handler_test.go
git commit -m "refactor(change): GetApprovalHistory 改读 ProcessApprovalDecision

数据源从 change_approvals 表切换成 BPMN 引擎的审批决策审计表，DTO 字段
形状不变（GET /changes/:id/approvals 前端 ChangeDetail.tsx 不用改）。"
```

---

### Task 6：清理独立状态机死代码

**Files:**
- Modify: `itsm-backend/handlers/change/entity.go`
- Modify: `itsm-backend/handlers/change/service.go`
- Modify: `itsm-backend/handlers/change/repository.go`
- Modify: `itsm-backend/handlers/change/repository_impl.go`
- Modify: `itsm-backend/handlers/change/handler.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-frontend/src/lib/api/change-api.ts`
- Test: `itsm-backend/handlers/change/handler_test.go`、`itsm-backend/handlers/change/service_test.go`

**Interfaces:**
- Consumes: 无新依赖
- Produces: 无——这一步纯删除

**这一步开始前，先确认 Task 1-5 全部完成且测试全绿**（`go test ./handlers/change/... ./service/bpmn/... -v`），这是删除代码前的安全网。

- [ ] **Step 1: 删除 `handlers/change/entity.go` 里的死类型**

删除 `ApprovalChain`（第 44-55 行）和 `ApprovalRecord`（第 58-68 行）两个 struct——**先确认没有别的地方还在用它们**：

```bash
cd itsm-backend
grep -rn "change\.ApprovalChain\|change\.ApprovalRecord" --include="*.go" . | grep -v "_test.go"
```

如果这个 grep 有输出（除了 `handlers/change` 包内部自己），先处理那些引用点，不要直接删。预期应该是空输出，因为 Task 5 已经把 `ApprovalRecord` 的产出方式换成了从 `ProcessApprovalDecision` 映射，`ApprovalRecord` 这个类型本身作为返回值的容器还需要保留（`GetApprovalHistory` 返回的就是它）——**不要删除 `ApprovalRecord`，只删除 `ApprovalChain`**（`ApprovalChain` 才是真正不再需要的，`ApprovalRecord` 继续作为 DTO 容器使用）。

- [ ] **Step 2: 删除 `checkAndTransitionChange`/`ConfigureWorkflow`/`GetApprovalSummary`**

在 `itsm-backend/handlers/change/service.go` 里删除：
- `checkAndTransitionChange`（第 241-295 行）——确认没有调用点：`grep -n "checkAndTransitionChange" handlers/change/*.go`，删除前应该只有定义本身。
- `ConfigureWorkflow`（第 297-311 行）——已确认没有路由，`grep -n "ConfigureWorkflow" handlers/change/*.go router/router.go` 确认只有定义和它自己的单测引用它，删除单测里对应的测试用例（`handler_test.go:741` 附近，先读一下这条测试在测什么，确认删除范围）。
- `GetApprovalSummary`（第 313-329 行，返回 `{chain, history}`）——`ApprovalChain` 已经删了，这个方法编译不过，一并删除。`handler.go` 里调用它的 `GetApprovalSummary` handler 方法（返回给 `GET /changes/:id/approval-summary`）也删除，router.go 里这条路由删除（前端已确认零调用，见 Task 0 的调研结论）。

同时删除 `ProcessApproval` 方法（`service.go:177-239`）——它是 `checkAndTransitionChange` 的唯一调用方，也是走 `ApprovalRecord`/`change_approvals` 表的旧路径，前面调研已确认没有 handler/路由调用它（只有 `SubmitApproval`/`TransitionStatus` 是真正对外的入口，`ProcessApproval` 是死代码，跟 `checkAndTransitionChange` 一起删）。删除前一样先 grep 确认：`grep -n "\.ProcessApproval(" handlers/change/*.go`。

- [ ] **Step 3: 删除 `SubmitApproval`**

删除 `handlers/change/service.go` 的 `SubmitApproval` 方法（第 151-175 行）、`handlers/change/handler.go` 对应的 `SubmitApproval` handler 方法、`router/router.go` 里 `POST /changes/:id/approvals` 这条路由注册。删除 `itsm-frontend/src/lib/api/change-api.ts` 里如果有对应的方法（Task 0 调研已确认前端没有调用这个端点的方法，这一步应该是确认性的，不需要真的改前端文件——跑一下 `grep -rn "changes/:id/approvals\|/approvals'" itsm-frontend/src` 或者更准确地 `grep -rn "POST.*approvals\|approvals.*POST" itsm-frontend/src/lib/api/change-api.ts` 二次确认没有遗漏,如果真的什么都没有,这一步不用改前端文件)。

- [ ] **Step 4: 删除 `repository_impl.go` 里操作 `change_approvals`/`change_approval_chains` 的方法**

删除：`SubmitForApproval`（Task 2 已经不再从 `SubmitChange` 调用它了，现在删除定义本身）、`CreateApprovalRecord`、`UpdateApprovalRecord`（Task 4 已经不再需要更新审批记录，BPMN 回调直接管 `Change.Status`）、`GetApprovalChain`、`CreateApprovalChain`、`ReplaceApprovalChain`、`DeleteApprovalChain`。对应地在 `itsm-backend/handlers/change/repository.go` 接口定义里删除这些方法签名。

删除前统一跑一次 grep 确认调用点清空：

```bash
cd itsm-backend
for m in SubmitForApproval CreateApprovalRecord UpdateApprovalRecord GetApprovalChain CreateApprovalChain ReplaceApprovalChain DeleteApprovalChain; do
  echo "=== $m ==="
  grep -rn "\.$m(" --include="*.go" . | grep -v "_test.go" | grep -v "handlers/change/repository"
done
```

预期全部为空（除了 `repository.go`/`repository_impl.go` 自身的定义，已经被排除在 grep 之外）。如果任何一个方法名还有其他调用点，先处理那个调用点，不要删除方法本身。

- [ ] **Step 5: 删除 `handlers/change/service.go` 里的 P0-1 桥接残留**

删除 `Service` struct 的 `approvalBridge` 字段、`NewService` 构造函数里对应的初始化代码（第 36-39 行的 `if entClient != nil { svc.approvalBridge = ... }`）。**不要删除 `service.BPMNApprovalBridge` 这个类型本身**（`ticket_workflow_service.go`/`release_service.go` 还在用，见 Global Constraints）。

- [ ] **Step 6: 运行完整测试确认清理没有破坏任何东西**

Run: `cd itsm-backend && go build ./... && go test ./... 2>&1 | tail -150`
Expected: 编译通过，所有包全绿。如果发现某个包因为引用了刚删除的符号编译失败，回去确认是不是遗漏了某个调用点（不应该发生，因为每一步删除前都做了 grep 确认，但如果确实漏了，说明前面某一步的 grep 范围不够全，补上遗漏的调用点处理，不要为了让编译通过就把已经删除的方法加回来）。

- [ ] **Step 7: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/ itsm-backend/router/router.go
git commit -m "refactor(change): 删除独立审批状态机死代码

ApprovalChain 类型、checkAndTransitionChange、ConfigureWorkflow（本来
就没有路由）、ProcessApproval、GetApprovalSummary（chain 概念不存在了，
前端零调用）、SubmitApproval（前端零调用，绕过 chain 机制的第二条不
一致审批路径）、repository_impl.go 里所有操作 change_approvals/
change_approval_chains 两张表的方法，以及 handlers/change/service.go
里的 P0-1 approvalBridge 字段/构造（BPMNApprovalBridge 类型本身保留，
ticket_workflow_service.go/release_service.go 还在用）。

不删除 change_approvals/change_approval_chains 物理表——只是代码不再
读写，历史数据保留查询可能性，DROP TABLE 不在这次范围内。"
```

---

### Task 7：端到端回归测试

**Files:**
- Test: `itsm-backend/handlers/change/change_bpmn_e2e_test.go`（新建）

**Interfaces:**
- Consumes: Task 1-6 的全部产出

- [ ] **Step 1: 写完整的端到端测试**

```go
package change_test

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/handlers/change"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

// TestChangeApprovalE2E_FullApproveFlow 完整走一遍：提交审批 -> 触发 BPMN ->
// 完成变更评估 -> CAB 审批通过 -> 断言 Change.Status/流程实例状态/审批历史
// 全部符合预期，不留孤儿任务。
func TestChangeApprovalE2E_FullApproveFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_e2e_approve?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant").SetCode("e2e-approve").SetDomain("e2e-approve.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester").SetEmail("requester@example.com").SetName("Requester").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm").SetEmail("cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// 给 cmUser 赋 change_manager 角色——照抄
	// service/bpmn_process_engine_approval_assignment_test.go 里 createUserWithRole
	// 的具体 ent 调用方式。

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	// 照抄 Task 3 测试里构造 *CustomProcessEngine/ProcessEngine 接口实现的方式
	var engine service.ProcessEngine // = newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewRepository(client) // 如果 Task 5 改了 NewRepository 签名（加了 entClient 参数），这里跟着改
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更", Type: "normal", Status: "draft", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	afterSubmit, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", afterSubmit.Status)

	// 完成变更评估，推进到 CAB 审批
	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, assessmentTasks, 1)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	// CAB 审批通过
	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "approved", "e2e 测试通过")
	require.NoError(t, err)

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", final.Status)

	history, err := svc.GetApprovalHistory(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "approved", history[0].Status)
	assert.Equal(t, cmUser.ID, history[0].ApproverID)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", created.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "running", instance.Status, "流程实例应该还在运行，停在 Activity_Implement——这是预期，不是 bug")
	assert.Equal(t, "Activity_Implement", instance.CurrentActivityID)

	// 重复提交应该被拒绝（当前状态已经是 approved，不是 draft，SubmitChange 本身就会拒绝；
	// 这条断言同时验证了业务层的旧校验依然生效，没有被这次改动破坏）
	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.Error(t, err)
}

// TestChangeApprovalE2E_FullRejectFlow 同样结构，走驳回分支：断言 Change.Status=="rejected"，
// ProcessInstance.Status=="completed"（驳回节点走 Flow_End 直接结束，流程实例正确终止，
// 不会像 approve 分支那样停在 running）。
func TestChangeApprovalE2E_FullRejectFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_e2e_reject?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant Reject").SetCode("e2e-reject").SetDomain("e2e-reject.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester2").SetEmail("requester2@example.com").SetName("Requester2").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-reject").SetEmail("cm-reject@example.com").SetName("CM Reject").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err)

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewRepository(client)
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更-驳回", Type: "normal", Status: "draft", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, assessmentTasks, 1)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	// comment 为空必须被拒绝，且不改变状态
	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "rejected", "")
	require.Error(t, err)

	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "rejected", "风险评估不通过")
	require.NoError(t, err)

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", final.Status)

	history, err := svc.GetApprovalHistory(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "rejected", history[0].Status)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", created.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", instance.Status, "驳回节点走 Flow_End 直接结束，流程实例应该正确终止，不会像 approve 分支那样停在 running")
}

// TestChangeApprovalE2E_NonCMUserCannotApprove 断言非 change_manager 角色的用户
// 调用 TransitionStatus approve 会失败，Change.Status 不变。
func TestChangeApprovalE2E_NonCMUserCannotApprove(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_e2e_wrong_actor?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant WrongActor").SetCode("e2e-wrong-actor").SetDomain("e2e-wrong-actor.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester3").SetEmail("requester3@example.com").SetName("Requester3").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-wa").SetEmail("cm-wa@example.com").SetName("CM WA").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(cmUser.ID).SetTenantID(tenant.ID).SetRoleName("change_manager").Save(ctx)
	require.NoError(t, err)
	outsider, err := client.User.Create().SetUsername("outsider-e2e").SetEmail("outsider-e2e@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewRepository(client)
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更-越权", Type: "normal", Status: "draft", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, assessmentTasks, 1)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, outsider.ID, "approved", "我批准")
	require.Error(t, err, "outsider 没有 change_manager 角色，authorizeTaskActor 应该拒绝")

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", final.Status, "越权调用失败后状态不应该变化")
}
```

- [ ] **Step 2: 运行测试，先确认能编译，再确认全部通过**

Run: `cd itsm-backend && go test ./handlers/change/... -run TestChangeApprovalE2E -v`
Expected: 编译时会暴露上面标注的几处"照抄现有 helper"没有真正落实的地方（`createUserWithRole`/`newTestBPMNEngine`/`NewRepository` 签名）——实现者需要先去对应的现有测试文件里确认这些 helper 的真实签名，替换掉注释占位，不是简单删掉那些调用。全部实现完之后三条测试应该全部 PASS。

- [ ] **Step 3: 跑整个 change 包 + BPMN 相关包的完整测试，确认没有回归**

Run: `cd itsm-backend && go test ./handlers/change/... ./service/... ./service/bpmn/... -v 2>&1 | tail -200`
Expected: 全绿。

- [ ] **Step 4: 跑全量后端测试 + 前端 type-check**

Run:
```bash
cd itsm-backend && go build ./... && go test ./... 2>&1 | tail -100
cd ../itsm-frontend && npm run type-check
```
Expected: 全部通过。

- [ ] **Step 5: 手动验证一次真实的 change 详情页**（如果本地开发环境可用——参照 `docs/runbooks/legacy-approval-write-lock.md` 里"端到端验证"章节的做法，建一个真实变更、提交审批、用 CAB 审批人身份通过，确认 `/changes/:id` 详情页的审批历史正确展示）。如果开发环境不可用，跳过这一步，在报告里注明是文档验证还是手工验证。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm/.claude/worktrees/change-approval-bpmn-migration
git add itsm-backend/handlers/change/change_bpmn_e2e_test.go
git commit -m "test(change): 变更审批 BPMN 迁移端到端回归测试

完整覆盖 SubmitChange 触发流程 -> 变更评估 -> CAB 审批通过/驳回 ->
Change.Status 正确、流程实例状态正确（approve 分支停在 Activity_Implement，
reject 分支正确终止）、审批历史正确、非 CAB 成员无法审批、状态已终止后
不能重复提交。"
```

---

## 测试计划

- Task 1：`ChangeServiceTaskHandler` 三个 action 各自写入合法状态值，过状态机校验，非法转换被拒绝。
- Task 2：`SubmitChange` 按 `Type` 正确选择 `change_normal_flow`/`change_emergency_flow`，重复提交（已有运行中实例）被拒绝，不再写 `change_approvals`/`change_approval_chains`。
- Task 3：CAB 审批完成后正确级联完成排期/驳回节点，approve 分支流程实例停在 `Activity_Implement`，reject 分支流程实例正确终止；审批人权限校验（`authorizeTaskActor`）在这条新路径上生效。
- Task 4：`TransitionStatus` 的 approve/reject 完全通过 BPMN 生效，不再依赖 `ApprovalHistory`；reject 缺 comment 被拒绝；非法 actor 被拒绝。
- Task 5：`GetApprovalHistory` 正确从 `ProcessApprovalDecision` 映射出 `ApprovalRecord`，租户隔离。
- Task 6：全部删除操作前先 grep 确认零调用点，删除后全量编译+测试通过。
- Task 7：端到端覆盖 approve/reject 两条完整路径 + 权限拒绝 + 重复提交拒绝。

## 非目标（本次不做）

- `start`/`complete`/`rollback`/`cancel` 四个非审批状态转换——继续走 `handlers/change` 自己的状态机，不接入 BPMN。
- `Activity_Assessment`/`Activity_Implement`/`Activity_Verify`/`Activity_Close` 这几个实施生命周期节点——不纳入这次范围，`Activity_Assessment` 继续需要外部（测试里是模拟的）手动 `CompleteTask` 才能推进（生产环境这一步目前没有对应的业务入口，这是一个已知的、不在这次范围内的缺口，值得记录到后续 backlog，但不在这次实现）。
- 删除 `change_approvals`/`change_approval_chains` 物理表——只让代码不再读写，不做 `DROP TABLE`。
- 给引擎新增通用的"userTask 自动完成"机制——Task 3 采用的是 `handlers/change` 包自己显式级联调用 `CompleteTask` 的方案，不修改 `service/bpmn_process_engine.go` 的核心调度逻辑，也不修改 `serviceTask` 的处理方式。
- `service/release_service.go` 里同样存在但未接线的 `approvalBridge` 使用情况——不在这次范围内，那是 release 域自己的问题。
