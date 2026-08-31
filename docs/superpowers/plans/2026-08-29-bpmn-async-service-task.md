# BPMN 异步 ServiceTask 执行语义 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 ITSM 自定义 BPMN 引擎的 `service_task` 节点在声明了异步 handler 时，能像 `user_task` 一样创建 `ProcessTask` 并暂停流程，直到外部显式调用完成才继续——为上游 KAF 委派设计的 `kaf_delegate` 节点提供底层执行语义。

**Architecture:** 给 `bpmn.ServiceTaskHandlerInterface` 新增一个可选能力接口 `AsyncServiceTaskHandler`（`IsAsync() bool`），现有 9 个同步 handler 零改动；`handleElement` 的 `serviceTask` 分支对异步 handler 转去新增的 `createDelegatedTask`（持久化 `ProcessTask`，不调用 `Execute`，不推进 `executeStep`）；恢复完全复用现有 `CompleteTask`，只在 `authorizeTaskActor` 里新增一个 `kaf_delegate` 专用鉴权分支。

**Tech Stack:** Go, ent ORM, sqlite（enttest，内存库），testify（assert/require）。

**Spec:** [docs/superpowers/specs/2026-08-29-bpmn-async-service-task-design.md](../specs/2026-08-29-bpmn-async-service-task-design.md)

## Global Constraints

- 现有 9 个 `ServiceTaskHandlerInterface` 实现（Ticket/Change/Incident/Generic/ServiceRequest/Notification/CC/Webhook/Release）不得修改一行；新增能力只能通过可选接口类型断言接入。
- `user_task` 的创建、完成鉴权与当时的完成后 handler 回调行为必须保持不变，每个改动点都要有明确回归证据。
- `authorizeTaskActor` 现有"无用户上下文即放行"分支只能继续对非 `kaf_delegate` 类型任务生效；新分支必须在到达这个放行逻辑之前拦截 `kaf_delegate` 任务。
- 不实现：Outbox 事件发布、上游委派设计 §4.1-4.3 的 HTTP API 契约（`GET kaf-context`/`POST actions`）、幂等键中间件、`AuditMiddleware` 挂载、Workflow Designer 前端。这些是独立条目，不在本计划范围。
- 每个任务完成后运行 `cd itsm-backend && go build ./...`，确保全仓库编译通过；涉及的包运行 `go test ./service/... -run <TestName> -v` 验证目标测试，另外完整跑一次 `go test ./service/...` 确认无回归。

---

### Task 1: ProcessTask schema 新增 correlation_id 字段

**Files:**
- Modify: `itsm-backend/ent/schema/process_task.go`
- Modify（ent 自动生成，不手写）: `itsm-backend/ent/process_task.go`、`itsm-backend/ent/process_task_create.go`、`itsm-backend/ent/process_task_update.go`、`itsm-backend/ent/mutation.go`、`itsm-backend/ent/runtime.go` 等
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Produces: `ent.ProcessTask.CorrelationID string`、`ProcessTaskCreate.SetCorrelationID(string)`、`ProcessTaskUpdateOne.SetCorrelationID(string)`（ent 生成）。

- [ ] **Step 1: 写失败测试**

在 `itsm-backend/service/bpmn_process_engine_ext_test.go` 文件末尾追加：

```go
func TestProcessTask_CorrelationIDRoundTrip(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := baseCtx

	_, taskID := createProcessFixture(t, engine, tenantID, "correlation1")

	updated, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCorrelationID("corr-abc-123").
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "corr-abc-123", updated.CorrelationID)

	reloaded, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "corr-abc-123", reloaded.CorrelationID)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestProcessTask_CorrelationIDRoundTrip -v`
Expected: 编译失败，`updated.SetCorrelationID undefined` / `CorrelationID undefined`（字段还不存在）。

- [ ] **Step 3: 加字段并重新生成 ent 代码**

在 `itsm-backend/ent/schema/process_task.go` 的 `Fields()` 里，在 `description`（第 75-77 行）和 `parent_task_id`（第 78-80 行）字段之间插入：

```go
		field.String("correlation_id").
			Comment("跨系统关联 ID（如 KAF session/Langfuse trace），用于委派任务的端到端追踪").
			Optional(),
```

然后执行：

```bash
cd itsm-backend && go generate ./ent
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestProcessTask_CorrelationIDRoundTrip -v`
Expected: PASS

- [ ] **Step 5: 全量编译确认**

Run: `cd itsm-backend && go build ./...`
Expected: 无错误。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend && git add ent/schema/process_task.go ent/ service/bpmn_process_engine_ext_test.go
git commit -m "feat(bpmn): add ProcessTask.correlation_id field"
```

---

### Task 2: AsyncServiceTaskHandler 接口 + 暂停路径（createDelegatedTask）

**Files:**
- Create: `itsm-backend/service/bpmn/async_handler.go`
- Modify: `itsm-backend/service/bpmn_types.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Produces: `bpmn.AsyncServiceTaskHandler`（接口，`IsAsync() bool`）、`bpmn.KafDelegateTaskType`（导出常量，值 `"kaf_delegate"`）、`(e *BPMNServiceTask) AllowedActions() string`、`(e *CustomProcessEngine) createDelegatedTask(ctx context.Context, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask) error`。
- Consumes: 无新依赖，复用 `findHandlerByTaskType`、`ExtensionElements.GetMetaData`。

- [ ] **Step 1: 写失败测试**

在 `itsm-backend/service/bpmn_process_engine_ext_test.go` 顶部 import 块加入 `"itsm-backend/dto"` 和 `"itsm-backend/ent/processtask"`：

```go
import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap/zaptest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

文件末尾追加测试替身和测试：

```go
// fakeAsyncServiceTaskHandler 是测试专用的最小异步 handler：实现
// ServiceTaskHandlerInterface + AsyncServiceTaskHandler，记录 Execute 被调用次数，
// 用于断言暂停时不执行、完成时只执行一次。
type fakeAsyncServiceTaskHandler struct {
	taskType  string
	handlerID string
	executed  int
}

func (h *fakeAsyncServiceTaskHandler) GetTaskType() string  { return h.taskType }
func (h *fakeAsyncServiceTaskHandler) GetHandlerID() string { return h.handlerID }
func (h *fakeAsyncServiceTaskHandler) IsAsync() bool        { return true }
func (h *fakeAsyncServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}
func (h *fakeAsyncServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	h.executed++
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*fakeAsyncServiceTaskHandler)(nil)
var _ bpmn.AsyncServiceTaskHandler = (*fakeAsyncServiceTaskHandler)(nil)

func TestHandleElement_AsyncServiceTask_PausesAndCreatesDelegatedTask(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	instanceID, _ := createProcessFixture(t, engine, tenantID, "async-pause-1")
	instance, err := engine.client.ProcessInstance.Get(ctx, instanceID)
	require.NoError(t, err)

	fakeHandler := &fakeAsyncServiceTaskHandler{taskType: "fake_async_task", handlerID: "fake_async_handler"}
	engine.callbackRegistry.RegisterHandler(fakeHandler)

	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{
				ID:   "Activity_KafDelegate",
				Name: "KAF 委派",
				ExtensionElements: &BPMNExtensionElements{
					MetaData: []BPMNMetaData{
						{Name: "service_task_type", Value: "fake_async_task"},
						{Name: "action", Value: "delegate"},
						{Name: "allowed_actions", Value: "resolve,update_progress"},
					},
				},
			},
		},
		EndEvents: []*BPMNEndEvent{{ID: "End_1", Name: "结束"}},
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "Flow_1", SourceRef: "Activity_KafDelegate", TargetRef: "End_1"},
		},
	}

	err = engine.handleElement(ctx, instance, process, "Activity_KafDelegate")
	require.NoError(t, err)

	assert.Equal(t, 0, fakeHandler.executed, "异步 handler 在暂停时不应该被 Execute")

	updatedInstance, err := engine.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_KafDelegate", updatedInstance.CurrentActivityID, "流程应该停在委派节点，不应该推进到结束节点")

	delegatedTask, err := engine.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_KafDelegate")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "fake_async_task", delegatedTask.TaskType)
	assert.Equal(t, "delegated", delegatedTask.Status)
	assert.Equal(t, "fake_async_task", delegatedTask.TaskVariables["service_task_type"])
	assert.Equal(t, "delegate", delegatedTask.TaskVariables["action"])
	assert.Equal(t, "resolve,update_progress", delegatedTask.TaskVariables["allowed_actions"])
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestHandleElement_AsyncServiceTask_PausesAndCreatesDelegatedTask -v`
Expected: 编译失败——`bpmn.AsyncServiceTaskHandler`、`ServiceTask.AllowedActions`、`allowed_actions` 断言等尚不存在。

- [ ] **Step 3: 新增 AsyncServiceTaskHandler 接口**

创建 `itsm-backend/service/bpmn/async_handler.go`：

```go
package bpmn

// AsyncServiceTaskHandler 是 ServiceTaskHandlerInterface 的可选扩展接口。
// 实现此接口且 IsAsync() 返回 true 的 handler，其对应的 serviceTask 节点
// 在流程到达时不会同步执行 Execute，而是创建 ProcessTask 并暂停，直到外部
// 通过 CompleteTask 显式完成——见 CustomProcessEngine.createDelegatedTask。
//
// 这是一个能力接口而不是对 ServiceTaskHandlerInterface 的必需扩展：现有的
// 9 个同步 handler（Ticket/Change/Incident/Generic/ServiceRequest/Notification/
// CC/Webhook/Release）不需要实现它，类型断言落空时自动走原有同步路径。
type AsyncServiceTaskHandler interface {
	IsAsync() bool
}

// KafDelegateTaskType 是 kaf_delegate 委派节点在 BPMN metaData 里声明的
// service_task_type 值，也是对应 ProcessTask.TaskType 的值。定义在这里
// （而不是 KafDelegateServiceTaskHandler 所在文件）是因为 CustomProcessEngine
// 的 authorizeTaskActor 也需要引用它，属于跨文件共享的常量。
const KafDelegateTaskType = "kaf_delegate"
```

- [ ] **Step 4: 新增 AllowedActions 访问器**

在 `itsm-backend/service/bpmn_types.go` 顶部常量块（第 10-13 行）加入第三个 key：

```go
const (
	bpmnMetaDataServiceTaskType = "service_task_type"
	bpmnMetaDataAction          = "action"
	bpmnMetaDataAllowedActions  = "allowed_actions"
)
```

在 `ServiceTaskAction()` 方法（第 196-198 行）之后追加：

```go
// AllowedActions 返回该服务任务声明的 allowed_actions metaData（逗号分隔的动作名列表），
// 未声明时返回空串。读法跟 ServiceTaskType/ServiceTaskAction 完全一致。
func (e *BPMNServiceTask) AllowedActions() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataAllowedActions)
}
```

- [ ] **Step 5: handleElement 分支接入异步判断 + 新增 createDelegatedTask**

在 `itsm-backend/service/bpmn_process_engine.go` 的 `handleElement` 里，把：

```go
		if serviceTaskType := serviceTask.ServiceTaskType(); serviceTaskType != "" {
			if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
				callbackVars := mergeServiceTaskVariables(instance.Variables, serviceTask)
```

改为：

```go
		if serviceTaskType := serviceTask.ServiceTaskType(); serviceTaskType != "" {
			if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
				if asyncHandler, ok := handler.(bpmn.AsyncServiceTaskHandler); ok && asyncHandler.IsAsync() {
					return e.createDelegatedTask(ctx, instance, serviceTask)
				}
				callbackVars := mergeServiceTaskVariables(instance.Variables, serviceTask)
```

（只在 `if handler := ...; handler != nil {` 内部开头插入这个新的 `if` 块，其余原有代码不动。）

在 `createUserTask` 方法结束之后（第 1035 行 `return nil` / `}` 之后）新增：

```go
// createDelegatedTask 为声明了异步 handler 的 serviceTask 节点创建 ProcessTask 并暂停流程：
// 不调用 handler.Execute、不推进 executeStep，流程实例的 CurrentActivityID（handleElement
// 顶部已设置）停在这个节点，直到外部通过 CompleteTask 显式完成该任务才会继续。
func (e *CustomProcessEngine) createDelegatedTask(ctx context.Context, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask) error {
	serviceTaskType := serviceTask.ServiceTaskType()

	taskVariables := map[string]interface{}{
		bpmnMetaDataServiceTaskType: serviceTaskType,
	}
	if action := serviceTask.ServiceTaskAction(); action != "" {
		taskVariables[bpmnMetaDataAction] = action
	}
	if allowedActions := serviceTask.AllowedActions(); allowedActions != "" {
		taskVariables[bpmnMetaDataAllowedActions] = allowedActions
	}

	_, err := e.client.ProcessTask.Create().
		SetTaskID(fmt.Sprintf("TASK-%s-%d", serviceTask.ID, time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey(serviceTask.ID).
		SetTaskName(serviceTask.Name).
		SetTaskType(serviceTaskType).
		SetStatus("delegated").
		SetTaskVariables(taskVariables).
		SetTenantID(instance.TenantID).
		SetCreatedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("创建委派任务失败: %w", err)
	}
	e.logger.Infow("异步 ServiceTask 已暂停，等待外部完成",
		"elementID", serviceTask.ID, "serviceTaskType", serviceTaskType, "instanceID", instance.ProcessInstanceID)
	return nil
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestHandleElement_AsyncServiceTask_PausesAndCreatesDelegatedTask -v`
Expected: PASS

- [ ] **Step 7: 回归确认**

Run: `cd itsm-backend && go test ./service/... -run TestHandleElement_ServiceTask -v`
Expected: 全部 PASS（`TestHandleElement_ServiceTask_DispatchesByMetaDataOverAttributeGuessing`、`TestHandleElement_ServiceTask_IncidentAutoAssign_NoAssignee_ContinuesFlow` 等既有同步用例不受影响）。

- [ ] **Step 8: 全量编译确认**

Run: `cd itsm-backend && go build ./...`
Expected: 无错误。

- [ ] **Step 9: Commit**

```bash
cd itsm-backend && git add service/bpmn/async_handler.go service/bpmn_types.go service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "feat(bpmn): add AsyncServiceTaskHandler and createDelegatedTask pause path"
```

---

### Task 3: authorizeTaskActor 新增 kaf_delegate 鉴权分支

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.KafDelegateTaskType`（Task 2 产出）。
- Produces: `(e *CustomProcessEngine) authorizeKafAutomationActor(ctx context.Context, task *ent.ProcessTask) error`（内部方法，不导出）。

- [ ] **Step 1: 写失败测试**

在 `itsm-backend/service/bpmn_process_engine_ext_test.go` 文件末尾追加：

```go
func TestAuthorizeTaskActor_KafDelegate_AllowsKafAutomationRoleWhenDelegated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	kafUser, err := engine.client.User.Create().
		SetUsername("kaf_automation_bot").
		SetEmail("kaf-automation@example.com").
		SetName("KAF Automation").
		SetPasswordHash("hash").
		SetRole("kaf_automation").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "kaf-authz-1")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus("delegated").
		Save(ctx)
	require.NoError(t, err)

	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, kafUser.ID)
	assert.NoError(t, engine.authorizeTaskActor(actorCtx, task))
}

func TestAuthorizeTaskActor_KafDelegate_RejectsNonKafAutomationRole(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine) // role 是 "agent"，不是 kaf_automation
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, taskID := createProcessFixture(t, engine, tenantID, "kaf-authz-2")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus("delegated").
		Save(ctx)
	require.NoError(t, err)

	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)
	assert.Error(t, engine.authorizeTaskActor(actorCtx, task))
}

func TestAuthorizeTaskActor_KafDelegate_RejectsNoActorContext(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, taskID := createProcessFixture(t, engine, tenantID, "kaf-authz-3")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus("delegated").
		Save(ctx)
	require.NoError(t, err)

	// 跟人工任务"无上下文即放行"的分支不同：kaf_delegate 任务没有认证主体必须拒绝。
	assert.Error(t, engine.authorizeTaskActor(ctx, task))
}

func TestAuthorizeTaskActor_KafDelegate_RejectsWhenNotDelegatedStatus(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	kafUser, err := engine.client.User.Create().
		SetUsername("kaf_automation_bot2").
		SetEmail("kaf-automation2@example.com").
		SetName("KAF Automation").
		SetPasswordHash("hash").
		SetRole("kaf_automation").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "kaf-authz-4")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, kafUser.ID)
	assert.Error(t, engine.authorizeTaskActor(actorCtx, task))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestAuthorizeTaskActor_KafDelegate -v`
Expected: 第一个用例（kaf_automation 角色 + delegated 状态应该放行）失败——现有 `authorizeTaskActor` 会把 `kafUser` 当成普通审批人去匹配 `task.Assignee`/`task.CandidateUsers`，两者都是空，判定为无权限，返回 error 而不是 nil。

- [ ] **Step 3: 实现鉴权分支**

在 `itsm-backend/service/bpmn_process_engine.go` 里，把现有的：

```go
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return nil
	}
```

改为：

```go
// kafAutomationRole 是 KAF 自动化账号在 ent.User.Role 上的取值。KAF 与 ITSM
// 是同一应用的不同模块，不引入独立的技术账号/scoped-token 体系——KAF 以真实
// ITSM 用户身份（绑定这个角色）调用 API，走跟其他调用方相同的认证中间件。
const kafAutomationRole = "kaf_automation"

func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	if task.TaskType == bpmn.KafDelegateTaskType {
		return e.authorizeKafAutomationActor(ctx, task)
	}

	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return nil
	}
```

（`authorizeTaskActor` 函数体剩余部分——assignee/candidateUsers 校验——原样不动。）

在 `authorizeTaskActor` 函数结束的 `}` 之后新增：

```go
// authorizeKafAutomationActor 校验 kaf_delegate 任务只能被 kaf_automation 角色的
// 账号完成，且任务必须处于 delegated 状态。assignee/candidateUsers 对机器完成的
// 任务没有意义——同一租户下所有 kaf_delegate 任务都由同一个账号处理，不存在
// "候选人"概念。无用户上下文时直接拒绝，不复用人工任务分支"无上下文即放行"的口子：
// kaf_delegate 必须始终有明确的认证主体。
func (e *CustomProcessEngine) authorizeKafAutomationActor(ctx context.Context, task *ent.ProcessTask) error {
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("委派任务必须由已认证的 KAF 自动化账号完成")
	}
	actor, err := e.client.User.Query().Where(user.ID(userID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("KAF 自动化账号不存在: %w", err)
	}
	if actor.Role != kafAutomationRole {
		return fmt.Errorf("当前账号不是 KAF 自动化账号，无权完成委派任务")
	}
	if task.Status != "delegated" {
		return fmt.Errorf("委派任务当前状态不允许完成: %s", task.Status)
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestAuthorizeTaskActor -v`
Expected: 全部 PASS，包括原有的 `TestAuthorizeTaskActor_AllowsAssigneeAndCandidate`、`TestAuthorizeTaskActor_NoActorContextIsPermissive`（证明人工任务分支未受影响）和四个新增的 `TestAuthorizeTaskActor_KafDelegate_*`。

- [ ] **Step 5: 全量编译确认**

Run: `cd itsm-backend && go build ./...`
Expected: 无错误。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend && git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "feat(bpmn): authorize kaf_delegate task completion by kaf_automation role"
```

---

### Task 4: 端到端验证——CompleteTask 恢复被暂停的委派任务

**Files:**
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`（本任务不新增生产代码，只验证 Task 2 + Task 3 通过真实 `CompleteTask` 入口正确组合）

**Interfaces:**
- Consumes: Task 2 的 `createDelegatedTask`/`fakeAsyncServiceTaskHandler`，Task 3 的 `authorizeKafAutomationActor`，现有 `CompleteTask`。

- [ ] **Step 1: 写测试**

在 `itsm-backend/service/bpmn_process_engine_ext_test.go` 文件末尾追加（这一步没有"先写会失败的测试"阶段——被测的三块生产代码在 Task 2/3 都已经实现，这个任务本身就是它们组合起来的集成验证）：

```go
const resumeTestFlowBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="resume_test_flow" name="Resume Test Flow" isExecutable="true">
    <bpmn:serviceTask id="Activity_KafDelegate" name="KAF 委派">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">fake_async_task2</bpmn:metaData>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End_1" name="结束" />
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Activity_KafDelegate" targetRef="End_1" />
  </bpmn:process>
</bpmn:definitions>`

func TestCompleteTask_ResumesDelegatedTask_AfterAsyncPause(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	authorCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	authorCtx = context.WithValue(authorCtx, bpmn.BPMNUserIDContextKey, actorID)

	fakeHandler := &fakeAsyncServiceTaskHandler{taskType: "fake_async_task2", handlerID: "fake_async_handler2"}
	engine.callbackRegistry.RegisterHandler(fakeHandler)

	instanceID, _ := createProcessFixture(t, engine, tenantID, "resume-1")
	instance, err := engine.client.ProcessInstance.Get(authorCtx, instanceID)
	require.NoError(t, err)

	// CompleteTask 内部会重新 ParseXML 解析流程定义，所以要把真实 XML（而不是纯 Go
	// 结构体）写回 ProcessDefinition，跟生产路径保持一致。
	_, err = engine.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).
		SetBpmnXML([]byte(resumeTestFlowBPMN)).
		Save(authorCtx)
	require.NoError(t, err)

	def, err := engine.client.ProcessDefinition.Get(authorCtx, instance.ProcessDefinitionID)
	require.NoError(t, err)
	parsed, err := engine.parser.ParseXML(def.BpmnXML)
	require.NoError(t, err)
	process := parsed.Processes[0]

	// 1. 到达委派节点：暂停，创建 ProcessTask。
	err = engine.handleElement(authorCtx, instance, process, "Activity_KafDelegate")
	require.NoError(t, err)
	assert.Equal(t, 0, fakeHandler.executed)

	delegatedTask, err := engine.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_KafDelegate")).
		Only(authorCtx)
	require.NoError(t, err)

	kafUser, err := engine.client.User.Create().
		SetUsername("kaf_automation_bot3").
		SetEmail("kaf-automation3@example.com").
		SetName("KAF Automation").
		SetPasswordHash("hash").
		SetRole("kaf_automation").
		SetActive(true).
		SetTenantID(tenantID).
		Save(authorCtx)
	require.NoError(t, err)

	// 2. 非 kaf_automation 账号调用应该被拒绝，且不改变任务状态。
	err = engine.CompleteTask(authorCtx, delegatedTask.TaskID, map[string]interface{}{})
	assert.Error(t, err)
	stillDelegated, err := engine.client.ProcessTask.Get(authorCtx, delegatedTask.ID)
	require.NoError(t, err)
	assert.Equal(t, "delegated", stillDelegated.Status)

	// 3. kaf_automation 账号调用：完成任务并推进流程。
	kafCtx := context.WithValue(authorCtx, bpmn.BPMNUserIDContextKey, kafUser.ID)
	err = engine.CompleteTask(kafCtx, delegatedTask.TaskID, map[string]interface{}{"resultSummary": "done"})
	require.NoError(t, err)

	completed, err := engine.client.ProcessTask.Get(kafCtx, delegatedTask.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	assert.Equal(t, 1, fakeHandler.executed, "完成时应该触发一次 handler.Execute 用于记录")

	updatedInstance, err := engine.client.ProcessInstance.Get(kafCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "End_1", updatedInstance.CurrentActivityID, "流程应该已经推进到结束节点")

	// 4. 重复完成：报错，不重复推进流程或重复触发 handler。
	err = engine.CompleteTask(kafCtx, delegatedTask.TaskID, map[string]interface{}{})
	assert.Error(t, err)
	assert.Equal(t, 1, fakeHandler.executed, "重复完成不应该再次触发 handler")
}
```

- [ ] **Step 2: 运行测试**

Run: `cd itsm-backend && go test ./service/... -run TestCompleteTask_ResumesDelegatedTask_AfterAsyncPause -v`
Expected: PASS。如果失败，先确认失败点——常见原因是 XML fixture 里 `<bpmn:metaData>` 用了属性形式而不是 chardata 形式（正确形态是 `<bpmn:metaData name="...">值</bpmn:metaData>`，不是 `<bpmn:metaData name="..." value="..." />`），或者 `bpmn:` 前缀遗漏。

- [ ] **Step 3: 回归确认**

Run: `cd itsm-backend && go test ./service/... -v 2>&1 | tail -60`
Expected: 全部 PASS，重点看 `TestBPMNProcessEngine_*`、`TestRecordApprovalDecision_*`、`TestAuthorizeTaskActor_*`、`TestHandleElement_ServiceTask_*` 这些既有用例没有被本计划的改动波及。

- [ ] **Step 4: Commit**

```bash
cd itsm-backend && git add service/bpmn_process_engine_ext_test.go
git commit -m "test(bpmn): verify CompleteTask resumes a delegated task end-to-end"
```

---

### Task 5: KafDelegateServiceTaskHandler 生产实现 + 注册

**Files:**
- Create: `itsm-backend/service/bpmn/kaf_delegate_handler.go`
- Modify: `itsm-backend/service/bpmn/bpmn_callback_registry.go`
- Test: `itsm-backend/service/bpmn/kaf_delegate_handler_test.go`

**Interfaces:**
- Consumes: `AsyncServiceTaskHandler`、`KafDelegateTaskType`（Task 2 产出，同包）。
- Produces: `bpmn.KafDelegateServiceTaskHandler`、`bpmn.NewKafDelegateServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *KafDelegateServiceTaskHandler`。`registerDefaultHandlers` 默认注册它，`GetHandler("kaf_delegate_handler")` 可查到。

- [ ] **Step 1: 写失败测试**

创建 `itsm-backend/service/bpmn/kaf_delegate_handler_test.go`：

```go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestKafDelegateServiceTaskHandler_TypeAndAsync(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	handler := NewKafDelegateServiceTaskHandler(client, logger)

	assert.Equal(t, "kaf_delegate", handler.GetTaskType())
	assert.Equal(t, "kaf_delegate_handler", handler.GetHandlerID())
	assert.True(t, handler.IsAsync())
}

func TestKafDelegateServiceTaskHandler_Execute_ReturnsSuccessWithoutSideEffects(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_handler_test2?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	handler := NewKafDelegateServiceTaskHandler(client, logger)

	result, err := handler.Execute(context.Background(), nil, map[string]interface{}{"resultSummary": "done"})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestCallbackRegistry_RegistersKafDelegateHandlerByDefault(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_registry_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	registry := NewCallbackRegistry(client, logger)
	handler := registry.GetHandler("kaf_delegate_handler")
	require.NotNil(t, handler)
	assert.Equal(t, "kaf_delegate", handler.GetTaskType())

	asyncHandler, ok := handler.(AsyncServiceTaskHandler)
	require.True(t, ok)
	assert.True(t, asyncHandler.IsAsync())
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestKafDelegateServiceTaskHandler -v`
Expected: 编译失败，`NewKafDelegateServiceTaskHandler undefined`。

- [ ] **Step 3: 实现 handler**

创建 `itsm-backend/service/bpmn/kaf_delegate_handler.go`：

```go
package bpmn

import (
	"context"

	"itsm-backend/dto"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// KafDelegateServiceTaskHandler 是 kaf_delegate 委派节点的服务任务处理器。
//
// 它是异步 handler（IsAsync()==true）：流程到达声明了 service_task_type="kaf_delegate"
// 的节点时，引擎的 handleElement 不会调用它的 Execute，而是创建 ProcessTask 并暂停
// （见 CustomProcessEngine.createDelegatedTask）。Execute 只在任务完成后触发一次，
// 用于记录/审计，不产生任何业务副作用——真正的
// WorkItem 动作（resolve/close 等）走上游委派设计 §4.3 的 typed action API，
// 不经过这个 Execute。
type KafDelegateServiceTaskHandler struct {
	logger *zap.SugaredLogger
}

// NewKafDelegateServiceTaskHandler 创建 KAF 委派任务处理器
func NewKafDelegateServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *KafDelegateServiceTaskHandler {
	return &KafDelegateServiceTaskHandler{logger: logger}
}

// GetTaskType 返回任务类型
func (h *KafDelegateServiceTaskHandler) GetTaskType() string {
	return KafDelegateTaskType
}

// GetHandlerID 返回处理器标识
func (h *KafDelegateServiceTaskHandler) GetHandlerID() string {
	return "kaf_delegate_handler"
}

// IsAsync 声明该 handler 对应的 serviceTask 节点是暂停型的，不同步执行。
func (h *KafDelegateServiceTaskHandler) IsAsync() bool {
	return true
}

// Validate 验证配置
func (h *KafDelegateServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// Execute 只在委派任务完成时被调用一次，用于记录完成事件，
// 不产生业务副作用。
func (h *KafDelegateServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	taskID := ""
	if task != nil {
		taskID = task.TaskID
	}
	h.logger.Infow("KAF 委派任务已完成", "taskID", taskID, "variables", variables)
	return &dto.ServiceTaskResult{Success: true, Message: "kaf_delegate 任务已完成"}, nil
}

// 确保 KafDelegateServiceTaskHandler 实现了 ServiceTaskHandlerInterface 和 AsyncServiceTaskHandler
var _ ServiceTaskHandlerInterface = (*KafDelegateServiceTaskHandler)(nil)
var _ AsyncServiceTaskHandler = (*KafDelegateServiceTaskHandler)(nil)
```

在 `itsm-backend/service/bpmn/bpmn_callback_registry.go` 的 `registerDefaultHandlers` 里，在"注册发布服务任务处理器"那一行之后加入：

```go
	// 注册发布服务任务处理器
	r.RegisterHandler(NewReleaseServiceTaskHandler(r.client, r.logger))
	// 注册 KAF 委派处理器（异步，见 KafDelegateServiceTaskHandler 注释）
	r.RegisterHandler(NewKafDelegateServiceTaskHandler(r.client, r.logger))
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/bpmn/... -run "TestKafDelegateServiceTaskHandler|TestCallbackRegistry_RegistersKafDelegateHandlerByDefault" -v`
Expected: 全部 PASS。

- [ ] **Step 5: 全量回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./service/bpmn/...`
Expected: 全部通过，无编译错误。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend && git add service/bpmn/kaf_delegate_handler.go service/bpmn/kaf_delegate_handler_test.go service/bpmn/bpmn_callback_registry.go
git commit -m "feat(bpmn): register KafDelegateServiceTaskHandler as default async handler"
```

---

## 完成后

五个任务全部完成后，回到上游委派设计 [2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md](../specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md) §11，把 P0-1 状态更新为"已实现"，P0-2 更新为"引擎侧已实现，HTTP 层路由权限声明仍待做"，P1-1 更新为"已实现（只加了 correlation_id）"。
