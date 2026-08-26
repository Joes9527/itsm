# BPMN 权限模型端到端整改 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除 BPMN 任务/流程实例权限模型里"权限码身兼路由准入/提权信号/菜单可见性三职"的碎片化设计,建立显式系统调用声明机制、集中授权登记表 + 自动化守卫,并补齐流程实例读写接口(`GetProcessInstance`/`ListApprovalDecisions`/`SetProcessInstanceVariables`/`SuspendProcess`/`ResumeProcess`/`TerminateProcess`)此前完全缺失的参与者级别授权。

**Architecture:** 复用 `2026-08-25-bpmn-task-instance-authorization` 分支已经建立的 `bpmn.CallerIdentity`/`bpmn.ResolveCallerIdentity`/`bpmn.BPMNElevatedContextKey` 基础设施,新增一个 `bpmn.BPMNSystemCallerContextKey` 显式声明机制替代隐式 `userID<=0` 放行,新增两个流程实例级别的授权函数(`authorizeProcessInstanceViewer`/`authorizeProcessInstanceMutation`),并用一张集中登记表 + 守卫测试固化"每个接口对应哪种授权原语"这份此前只存在于人脑里的知识。权限种子数据(`task:read`/`process_instance:read`/`task:update`)从"路由准入 + 提权信号"两用,收窄为"仅提权信号",路由准入改为仅需登录。

**Tech Stack:** Go, Gin, Ent ORM, SQLite (`enttest`) for tests, `stretchr/testify`。

**Spec:** `docs/superpowers/specs/2026-08-26-bpmn-e2e-authorization-model-design.md`

## Global Constraints

- 每一个新增/修改的查询,只要涉及 `ProcessInstance`/`ProcessTask`,必须带显式 `TenantID` 谓词——不能只依赖上游 context 注入。
- "提权"判断必须只在服务端计算(`middleware.HasResourcePermission`),绝不能从客户端可控字段读取。
- 系统/内部调用必须通过 `bpmn.BPMNSystemCallerContextKey` 显式声明,不允许再用"没有 `userID`"这个副作用隐式推导。
- 每一条 `/bpmn/tasks/*`、`/bpmn/process-instances/*` 路由必须在 `service/bpmn/authorization_registry.go` 的 `BPMNTaskInstanceAuthRegistry` 里有对应条目,登记表和实际注册路由必须双向一致,由守卫测试强制。
- `authorizeProcessInstanceMutation` 的语义是"发起人或提权角色才能操作实例生命周期"——任务参与者(哪怕是当前审批环节的候选人)不能仅凭参与某个任务就暂停/恢复/终止/改写整个流程实例。
- 每个任务改完后运行该任务改动的窄范围测试;全部任务完成后必须运行一次 `cd itsm-backend && go test ./...` 并报告通过的包数量。
- Go 文件命名/组织遵循仓库既有惯例:snake_case 文件名,DTO/Controller/Service 分层不变,不引入新的水平分层。

---

## File Structure

- **Modify:** `itsm-backend/service/bpmn/handler_base.go` — 新增 `BPMNSystemCallerContextKey`。
- **Modify:** `itsm-backend/service/bpmn_process_engine.go` — `authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeTaskActor` 三个函数体;新增 `authorizeProcessInstanceViewer`/`authorizeProcessInstanceMutation` 两个函数;`GetProcessInstance`/`SetProcessInstanceVariables`/`SuspendProcess`/`ResumeProcess`/`TerminateProcess`/`ListApprovalDecisions` 六个方法接入授权检查。
- **Modify:** `itsm-backend/service/bpmn_process_engine_ext_test.go` — 更新/新增覆盖上述改动的测试。
- **Create:** `itsm-backend/service/bpmn/authorization_registry.go` — 集中授权登记表。
- **Create:** `itsm-backend/controller/bpmn_workflow_controller_authz_registry_test.go` — 登记表守卫测试。
- **Modify:** `itsm-backend/controller/bpmn_workflow_controller.go` — `GetProcessInstance`/`GetApprovalHistory`/`SetProcessInstanceVariables`/`SuspendProcess`/`ResumeProcess`/`TerminateProcess` 六个 handler 接入 `hasElevatedBPMNAccess`。
- **Modify:** `itsm-backend/router/router.go` — `/my-approvals`、`/workflow/tasks`、`/workflow/instances` 三条路由去掉 `RequirePermission`。
- **Modify:** `itsm-backend/pkg/seeder/seeder.go` — `dept_manager`/`end_user`/`service_catalog_admin` 三个角色权限清单;"我的待办"菜单种子的 `PermissionCode`。
- **Create:** `itsm-backend/migrations/20260826_bpmn_permission_model_e2e_fix.sql` — 权限种子迁移。

---

### Task 1: 新增显式系统调用声明 context key

**Files:**
- Modify: `itsm-backend/service/bpmn/handler_base.go:34`(紧跟 `BPMNElevatedContextKey` 之后)

**Interfaces:**
- Produces: `var BPMNSystemCallerContextKey = bpmnSystemCallerKey{}`——后续任务里 `authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeTaskActor`/`authorizeProcessInstanceViewer`/`authorizeProcessInstanceMutation` 都要读取这个 key。

- [ ] **Step 1: 直接添加(无需先写失败测试——这只是一个新增的 context key 声明,没有可测试的行为,行为验证在 Task 2-6 里进行)**

```go
// itsm-backend/service/bpmn/handler_base.go — 在 BPMNElevatedContextKey 声明之后插入

type bpmnSystemCallerKey struct{}

// BPMNSystemCallerContextKey carries an explicit declaration that this call
// originates from trusted internal/system code (e.g. a ticket creation flow
// auto-starting a BPMN process), not from an authenticated human caller.
// authorizeTaskViewer/authorizeTaskMutation/authorizeTaskActor/
// authorizeProcessInstanceViewer/authorizeProcessInstanceMutation check this
// key explicitly and fail closed when it's absent — replacing the previous
// implicit "no userID in context = permissive" convention, which had no real
// callers (verified 2026-08-26: zero non-HTTP, non-test call sites for any
// of the eight public TaskService/ProcessInstanceService methods these
// functions guard) and was a latent fail-open trap. Must only ever be set
// by code that is itself not reachable from an HTTP request — never derived
// from a client-suppliable field.
var BPMNSystemCallerContextKey = bpmnSystemCallerKey{}
```

- [ ] **Step 2: 编译确认**

Run: `cd itsm-backend && go build ./service/bpmn/...`
Expected: 编译通过(新增的包级变量在被使用前不会报 unused)。

- [ ] **Step 3: Commit**

```bash
cd itsm-backend
git add service/bpmn/handler_base.go
git commit -m "feat(bpmn): add explicit system-caller context key"
```

---

### Task 2: `authorizeTaskViewer` 改为显式系统调用声明,fail-closed 为默认

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2384-2418`(`authorizeTaskViewer`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.BPMNSystemCallerContextKey`(Task 1)。
- Produces: `authorizeTaskViewer` 签名不变(`func (s *bpmnTaskService) authorizeTaskViewer(ctx context.Context, task *ent.ProcessTask) error`)。`authorizeCounterSignViewer`(`service/bpmn_process_engine.go:2431-2441`)纯委托给 `authorizeTaskViewer`,没有自己的 `userID<=0` 分支,不需要改代码,但会自动继承这个 fail-closed 行为——本任务的测试要顺带验证这一点。

- [ ] **Step 1: 写失败测试**

```go
// 追加到 itsm-backend/service/bpmn_process_engine_ext_test.go

func TestGetTaskByID_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "viewer-nouser1")

	// 没有 BPMNUserIDContextKey，也没有 BPMNSystemCallerContextKey：必须拒绝，
	// 不再是旧约定里的"没有用户就放行"。
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.TaskService().GetTaskByID(ctx, taskID)
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied by default")
}

func TestGetTaskByID_ExplicitSystemCallerAllowed(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "viewer-syscaller1")

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err, "an explicitly declared system caller must be permitted")
	assert.Equal(t, taskID, task.ID)
}

func TestGetCounterSignStatus_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, parentTaskID := createProcessFixture(t, engine, tenantID, "countersign-nouser1")
	parentTask, err := engine.client.ProcessTask.Get(context.Background(), parentTaskID)
	require.NoError(t, err)

	elevatedCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	elevatedCtx = context.WithValue(elevatedCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	elevatedCtx = context.WithValue(elevatedCtx, bpmn.BPMNElevatedContextKey, true)
	_, err = engine.TaskService().CreateCounterSignTasks(elevatedCtx, parentTask.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", actorID)},
		Threshold:    1,
	})
	require.NoError(t, err)

	// 没有用户、没有系统调用声明、也没有提权：必须拒绝——这条断言同时证明
	// authorizeCounterSignViewer 继承了 authorizeTaskViewer 的新默认行为。
	noAuthCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	_, err = engine.TaskService().GetCounterSignStatus(noAuthCtx, parentTask.TaskID)
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied for counter-sign status too")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetTaskByID_NoUserNoSystemCallerDenied|TestGetTaskByID_ExplicitSystemCallerAllowed|TestGetCounterSignStatus_NoUserNoSystemCallerDenied' -v`
Expected: `TestGetTaskByID_NoUserNoSystemCallerDenied` 和 `TestGetCounterSignStatus_NoUserNoSystemCallerDenied` FAIL(旧代码里 `userID<=0` 直接放行,不会报错);`TestGetTaskByID_ExplicitSystemCallerAllowed` 应该已经 PASS(旧代码对没有用户的调用本来就放行,这条断言碰巧满足,但意义不同——修完之后它是"因为显式声明才放行"，不是"因为没有身份就放行"）。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 authorizeTaskViewer(第 2384-2418 行)

// authorizeTaskViewer allows a task to be read by: an elevated caller
// (task:read permission), an explicitly declared system caller
// (bpmn.BPMNSystemCallerContextKey), the task's participant
// (assignee/candidate_users/candidate_groups — see
// bpmn.CallerIdentity.IsTaskParticipant), or the initiator of the task's
// parent process instance (read-only progress visibility — matches the
// "can I see my own submitted request's approval chain" product
// expectation, distinct from being allowed to act on it). Everyone else,
// including a call with no authenticated user and no system-caller
// declaration, is denied — this replaces the previous implicit
// "no userID = permissive" convention.
func (s *bpmnTaskService) authorizeTaskViewer(ctx context.Context, task *ent.ProcessTask) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("查看用户不存在: %w", err)
	}
	if identity.IsTaskParticipant(task) {
		return nil
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(tenantID)).
		Only(ctx)
	if err == nil && instance.Initiator == identity.IDStr {
		return nil
	}
	return fmt.Errorf("当前用户无权查看该任务")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetTaskByID_|TestGetCounterSignStatus_' -v`
Expected: 全部 PASS,包括本任务新增的 3 个和此前已有的 `TestGetTaskByID_ParticipantCanView`/`TestGetTaskByID_InitiatorCanViewReadOnly`/`TestGetTaskByID_NonParticipantDenied`/`TestGetTaskByID_ElevatedCanViewAnything`/`TestGetTaskByID_CrossTenantNeverLeaks`/`TestGetCounterSignStatus_CrossTenantNeverLeaks`(这些测试都没有依赖旧的"没有用户就放行"行为,不应该受影响)。

- [ ] **Step 5: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。特别注意任何调用链间接触达 `GetTask`/`GetTaskByID`(比如 `ClaimTask`/`CompleteTaskByID`/`AssignTask`/`CancelTask`/`SetTaskVariables`/`CreateCounterSignTasks`/`Vote` 内部都会先 `GetTask`)且测试 context 里没有设置 `BPMNUserIDContextKey` 的既有测试——如果失败,按 Task 6 当年的约定,是给该测试 fixture 补上正确的 assignee/candidate/身份设置,而不是弱化这里的校验。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): authorizeTaskViewer requires explicit system-caller declaration, fail closed by default"
```

---

### Task 3: `authorizeTaskMutation` 改为显式系统调用声明

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2464-2467`(`authorizeTaskMutation` 开头的 `userID<=0` 分支)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.BPMNSystemCallerContextKey`(Task 1)。
- Produces: `authorizeTaskMutation` 签名不变(`func (s *bpmnTaskService) authorizeTaskMutation(ctx context.Context, task *ent.ProcessTask) (actorID int, actorName string, err error)`)。

- [ ] **Step 1: 写失败测试**

```go
// 追加到 itsm-backend/service/bpmn_process_engine_ext_test.go

func TestAssignTask_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target-3").SetEmail("assignee-target-3@example.com").SetName("Target3").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	_, taskID := createProcessFixture(t, engine, tenantID, "assign-nouser1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	err = engine.TaskService().AssignTask(ctx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied by default")
}

func TestAssignTask_ExplicitSystemCallerAllowedNoAudit(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target-4").SetEmail("assignee-target-4@example.com").SetName("Target4").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	_, taskID := createProcessFixture(t, engine, tenantID, "assign-syscaller1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)

	err = engine.TaskService().AssignTask(ctx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	require.NoError(t, err, "an explicitly declared system caller must be permitted")

	auditLogs, err := engine.client.ProcessAuditLog.Query().All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, auditLogs, "a system caller with no real actor must still produce no audit record")
}
```

`TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord`(`service/bpmn_process_engine_ext_test.go:1757`,已存在)测的正是旧约定("没设 `BPMNUserIDContextKey` 就当系统调用放行"),必须同步修改,否则会和新行为冲突而失败:

```go
// itsm-backend/service/bpmn_process_engine_ext_test.go — 替换 TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord
// 整个函数体，改用显式声明，其余断言不变

func TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target-2").SetEmail("assignee-target-2@example.com").SetName("Target2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "assign-noactor")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	// Explicitly declared system/internal call — no authenticated human actor.
	systemCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	systemCtx = context.WithValue(systemCtx, bpmn.BPMNSystemCallerContextKey, true)

	err = engine.TaskService().AssignTask(systemCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	require.NoError(t, err, "an explicitly declared system caller must still be permitted")

	auditLogs, err := engine.client.ProcessAuditLog.Query().All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, auditLogs, "a system/internal call with no actor must not produce an audit record")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestAssignTask_' -v`
Expected: `TestAssignTask_NoUserNoSystemCallerDenied` FAIL(旧代码放行);`TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord`(已改写)和 `TestAssignTask_ExplicitSystemCallerAllowedNoAudit` 应该已经 PASS(显式声明的路径行为和改写前一致)。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 authorizeTaskMutation 开头的 userID<=0 分支
// (第 2464-2467 行，函数其余部分不变)

func (s *bpmnTaskService) authorizeTaskMutation(ctx context.Context, task *ent.ProcessTask) (actorID int, actorName string, err error) {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return 0, "", nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return 0, "", fmt.Errorf("未认证的调用")
	}
	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	actor, actorErr := s.client.User.Query().Where(user.ID(userID), user.TenantID(tenantID)).Only(ctx)
	if actorErr != nil {
		return 0, "", fmt.Errorf("操作用户不存在: %w", actorErr)
	}
	if elevated {
		return userID, actor.Name, nil
	}
	identity, identErr := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if identErr != nil {
		return 0, "", fmt.Errorf("解析调用者身份失败: %w", identErr)
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return userID, actor.Name, nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, reqErr := isProcessInstanceRequester(ctx, s.client, task.ProcessInstanceID, tenantID, userID)
		if reqErr != nil {
			return 0, "", fmt.Errorf("校验申请人身份失败: %w", reqErr)
		}
		if !isRequester {
			return userID, actor.Name, nil
		}
	}
	return 0, "", fmt.Errorf("当前用户不是该任务的审批人或候选人")
}
```

注意:因为 `actorID>0` 才会写审计(Task 8 已经建好的规则,`if actorID > 0 { ... Record*(...) }`),`(0, "", nil)` 这个系统调用返回值和之前完全一样,不需要改动 `AssignTask`/`CancelTask`/`SetTaskVariables`/`CreateCounterSignTasks` 四个调用方的任何代码。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestAssignTask_|TestCancelTask_|TestSetTaskVariables_|TestCreateCounterSignTasks_' -v`
Expected: PASS。

- [ ] **Step 5: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): authorizeTaskMutation requires explicit system-caller declaration, fail closed by default"
```

---

### Task 4: `authorizeTaskActor` 改为显式系统调用声明

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:534-568`(`authorizeTaskActor`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.BPMNSystemCallerContextKey`(Task 1)。
- Produces: `authorizeTaskActor` 签名不变(`func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error`)。把守 `CompleteTask`/`CompleteTaskByID`/`SubmitTaskDecision`/`Vote`。

- [ ] **Step 1: 写失败测试(含改写既有测试)**

`TestAuthorizeTaskActor_NoActorContextIsPermissive`(`service/bpmn_process_engine_ext_test.go:873`,已存在)测的是旧约定,必须改写:

```go
// itsm-backend/service/bpmn_process_engine_ext_test.go — 替换 TestAuthorizeTaskActor_NoActorContextIsPermissive
// 整个函数体

func TestAuthorizeTaskActor_NoActorNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, taskID := createProcessFixture(t, engine, tenantID, "noctx1")
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	// No actor in context AND no explicit system-caller declaration must now
	// be denied — replaces the old implicit "no actor = permissive" default.
	assert.Error(t, engine.authorizeTaskActor(ctx, task))
}

func TestAuthorizeTaskActor_ExplicitSystemCallerAllowed(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)

	_, taskID := createProcessFixture(t, engine, tenantID, "syscaller2")
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	assert.NoError(t, engine.authorizeTaskActor(ctx, task),
		"an explicitly declared system caller must be permitted")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestAuthorizeTaskActor_' -v`
Expected: `TestAuthorizeTaskActor_NoActorNoSystemCallerDenied` FAIL(旧代码放行);`TestAuthorizeTaskActor_ExplicitSystemCallerAllowed` 和 `TestAuthorizeTaskActor_AllowsCandidateGroupMatch` 应该已经 PASS。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 authorizeTaskActor(第 534-568 行)

// authorizeTaskActor ensures that task actions are performed by the assigned
// user, an explicitly resolved candidate (by ID, username, email, or
// candidate_groups membership), or an explicitly declared system caller
// (bpmn.BPMNSystemCallerContextKey). candidate_groups matches must
// additionally pass isProcessInstanceRequester exclusion, since (unlike
// candidate_users) candidate_groups isn't pre-filtered for self-approval at
// task-creation time — see bpmn.CallerIdentity.MatchesCandidateGroup's doc
// comment. A call with no authenticated actor and no system-caller
// declaration is denied — replaces the previous implicit permissive default.
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, e.client, e.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("审批用户不存在: %w", err)
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, rErr := isProcessInstanceRequester(ctx, e.client, task.ProcessInstanceID, tenantID, userID)
		if rErr != nil {
			return fmt.Errorf("校验申请人身份失败: %w", rErr)
		}
		if !isRequester {
			return nil
		}
	}
	return fmt.Errorf("当前用户不是该任务的审批人或候选人")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestAuthorizeTaskActor_|TestClaimTaskByID_|TestVote_' -v`
Expected: PASS。

- [ ] **Step 5: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。注意 `CompleteTask`/`CompleteTaskByID`/`SubmitTaskDecision`/`Vote` 相关的既有测试,如果有测试 context 没设置 `BPMNUserIDContextKey` 且依赖旧的放行行为,按同样原则补身份设置,不弱化校验。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): authorizeTaskActor requires explicit system-caller declaration, fail closed by default"
```

---

### Task 5: 流程实例读接口授权——`authorizeProcessInstanceViewer` + `GetProcessInstance`/`ListApprovalDecisions`

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2099-2115`(`GetProcessInstance`)、`:2697-2709`(`ListApprovalDecisions`)、新增 `authorizeProcessInstanceViewer`(放在 `authorizeTaskMutation` 之后)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:550-564`(`GetProcessInstance` handler)、`:167-178`(`GetApprovalHistory` handler)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.ResolveCallerIdentity`、`bpmn.BPMNElevatedContextKey`、`bpmn.BPMNSystemCallerContextKey`(均已存在/Task 1)。
- Produces: `func authorizeProcessInstanceViewer(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) error`——独立函数(不是某个 service struct 的方法),因为它要被 `bpmnProcessInstanceService`(`GetProcessInstance`)和 `bpmnTaskService`(`ListApprovalDecisions`)两个不同的 struct 调用,和 `isProcessInstanceRequester`/`isTaskCandidate` 一样的既有模式。`GetProcessInstance(ctx, processInstanceID string) (*ent.ProcessInstance, error)` 和 `ListApprovalDecisions(ctx, processInstanceKey string) ([]*ent.ProcessApprovalDecision, error)` 签名不变。

- [ ] **Step 1: 写失败测试**

```go
// 追加到 itsm-backend/service/bpmn_process_engine_ext_test.go

func TestGetProcessInstance_ParticipantCanView(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "instview1")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", actorID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	instance, err := engine.ProcessInstanceService().GetProcessInstance(ctx, fmt.Sprintf("%d", instanceID))
	require.NoError(t, err)
	assert.Equal(t, instanceID, instance.ID)
}

func TestGetProcessInstance_InitiatorCanView(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "instview2")
	_, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee("someone-else").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	instance, err := engine.ProcessInstanceService().GetProcessInstance(ctx, fmt.Sprintf("%d", instanceID))
	require.NoError(t, err, "the process initiator must be able to view their own instance")
	assert.Equal(t, instanceID, instance.ID)
}

func TestGetProcessInstance_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("instview-bystander").SetEmail("instview-bystander@example.com").SetName("Bystander").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "instview3")
	_, err = engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err = engine.ProcessInstanceService().GetProcessInstance(notParticipantCtx, fmt.Sprintf("%d", instanceID))
	assert.Error(t, err, "a non-participant, non-initiator, non-elevated caller must be denied")

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	instance, err := engine.ProcessInstanceService().GetProcessInstance(elevatedCtx, fmt.Sprintf("%d", instanceID))
	require.NoError(t, err, "an elevated caller must be able to view any instance")
	assert.Equal(t, instanceID, instance.ID)
}

func TestGetProcessInstance_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("instview-other").SetDomain("instview-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	otherInstanceID, _ := createProcessFixture(t, engine, otherTenant.ID, "instview4-other")
	_, err = engine.client.ProcessInstance.UpdateOneID(otherInstanceID).
		SetInitiator(fmt.Sprintf("%d", actorID)). // same numeric ID, different tenant
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	_, err = engine.ProcessInstanceService().GetProcessInstance(ctx, fmt.Sprintf("%d", otherInstanceID))
	assert.Error(t, err, "must never return another tenant's instance regardless of elevation")
}

func TestGetProcessInstance_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	instanceID, _ := createProcessFixture(t, engine, tenantID, "instview5")

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	_, err := engine.ProcessInstanceService().GetProcessInstance(ctx, fmt.Sprintf("%d", instanceID))
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied by default")
}

func TestListApprovalDecisions_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("history-bystander").SetEmail("history-bystander@example.com").SetName("Bystander2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "history1")
	instance, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err = engine.TaskService().ListApprovalDecisions(notParticipantCtx, instance.ProcessInstanceID)
	assert.Error(t, err, "a non-participant, non-initiator, non-elevated caller must be denied approval history")

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	_, err = engine.TaskService().ListApprovalDecisions(elevatedCtx, instance.ProcessInstanceID)
	require.NoError(t, err, "an elevated caller must be able to view any instance's approval history")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetProcessInstance_|TestListApprovalDecisions_' -v`
Expected: 除 `TestGetProcessInstance_CrossTenantNeverLeaks`(现有代码已经做了租户过滤,这条本来就应该 PASS)外全部 FAIL——当前代码完全没有参与者校验。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn_process_engine.go — 在 authorizeTaskMutation 函数之后新增

// authorizeProcessInstanceViewer allows a process instance to be read by:
// an elevated caller, an explicitly declared system caller, the instance's
// initiator, or a participant of any task belonging to this instance
// (mirrors authorizeTaskViewer's philosophy for the read side, extended
// from task-scope to instance-scope). Not a method on any one service
// struct — GetProcessInstance (bpmnProcessInstanceService) and
// ListApprovalDecisions (bpmnTaskService) both call it, matching the
// existing isProcessInstanceRequester/isTaskCandidate standalone-function
// convention in this file.
func authorizeProcessInstanceViewer(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = instance.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, client, bpmn.NewGroupResolver(client), tenantID, userID)
	if err != nil {
		return fmt.Errorf("查看用户不存在: %w", err)
	}
	if instance.Initiator == identity.IDStr {
		return nil
	}
	tasks, err := client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("查询流程任务失败: %w", err)
	}
	for _, task := range tasks {
		if identity.IsTaskParticipant(task) {
			return nil
		}
	}
	return fmt.Errorf("当前用户无权查看该流程实例")
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 GetProcessInstance(第 2099-2115 行)

func (s *bpmnProcessInstanceService) GetProcessInstance(ctx context.Context, processInstanceID string) (*ent.ProcessInstance, error) {
	id, err := strconv.Atoi(processInstanceID)
	if err != nil {
		return nil, fmt.Errorf("无效的流程实例ID: %w", err)
	}
	query := s.client.ProcessInstance.Query().
		Where(processinstance.ID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instance, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}
	if err := authorizeProcessInstanceViewer(ctx, s.client, instance); err != nil {
		return nil, err
	}

	return instance, nil
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 ListApprovalDecisions(第 2697-2709 行)

func (s *bpmnTaskService) ListApprovalDecisions(ctx context.Context, processInstanceKey string) ([]*ent.ProcessApprovalDecision, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return nil, fmt.Errorf("缺少租户上下文")
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.ProcessInstanceID(processInstanceKey), processinstance.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}
	if err := authorizeProcessInstanceViewer(ctx, s.client, instance); err != nil {
		return nil, err
	}
	return s.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.ProcessInstanceKey(processInstanceKey),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
}
```

- [ ] **Step 4: 控制器接入 elevated 上下文**

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 GetProcessInstance(第 550-564 行)

func (c *BPMNWorkflowController) GetProcessInstance(ctx *gin.Context) {
	processInstanceID := ctx.Param("id")
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "read")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	instance, err := c.processEngine.ProcessInstanceService().GetProcessInstance(workflowCtx, processInstanceID)
	if err != nil {
		common.NotFound(ctx, "流程实例不存在: "+err.Error())
		return
	}

	common.Success(ctx, dto.ToBPMNProcessInstanceResponse(instance))
}
```

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 GetApprovalHistory(第 167-178 行)

func (c *BPMNWorkflowController) GetApprovalHistory(ctx *gin.Context) {
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "read")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	decisions, err := c.processEngine.TaskService().ListApprovalDecisions(workflowCtx, ctx.Param("id"))
	if err != nil {
		common.InternalError(ctx, "查询审批历史失败: "+err.Error())
		return
	}
	common.Success(ctx, dto.ToProcessApprovalDecisionResponseList(decisions))
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetProcessInstance_|TestListApprovalDecisions_' -v`
Expected: 全部 PASS。

- [ ] **Step 6: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。注意 `SetProcessInstanceVariables` 内部会调用 `GetProcessInstance`,如果它的既有测试(如果存在)context 里没设身份,这一步就会先暴露出来——按同样原则修 fixture,Task 6 会再单独给 `SetProcessInstanceVariables` 加写操作级别的校验。

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): GetProcessInstance/ListApprovalDecisions require participant/initiator/elevated access"
```

---

### Task 6: 流程实例写接口授权——`authorizeProcessInstanceMutation` + `SetProcessInstanceVariables`/`SuspendProcess`/`ResumeProcess`/`TerminateProcess`

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:1554-1596`(`SuspendProcess`)、`:1603` 起(`ResumeProcess`)、`:1649` 起(`TerminateProcess`)、`:2243-2260`(`SetProcessInstanceVariables`)、新增 `authorizeProcessInstanceMutation`(放在 `authorizeProcessInstanceViewer` 之后)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:567-589`(`SetProcessInstanceVariables`)、`:592-614`(`SuspendProcess`)、`:617-631`(`ResumeProcess`)、`:634-656`(`TerminateProcess`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `authorizeProcessInstanceViewer` 同一批基础设施(Task 5)。
- Produces: `func authorizeProcessInstanceMutation(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) error`——独立函数,原因同 Task 5:`SuspendProcess`/`ResumeProcess`/`TerminateProcess` 是 `CustomProcessEngine` 的方法,`SetProcessInstanceVariables` 是 `bpmnProcessInstanceService` 的方法,两个不同 struct 都要调。四个方法签名不变。

- [ ] **Step 1: 写失败测试**

```go
// 追加到 itsm-backend/service/bpmn_process_engine_ext_test.go

func TestSuspendProcess_InitiatorAllowedParticipantDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	participant, err := engine.client.User.Create().
		SetUsername("suspend-participant").SetEmail("suspend-participant@example.com").SetName("Participant").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "suspend1")
	instance, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", participant.ID)).Save(context.Background())
	require.NoError(t, err)

	// A task participant (not the initiator) must NOT be able to suspend the
	// whole instance — participation in one approval step is not the same
	// authority as controlling the instance's lifecycle.
	participantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, participant.ID)
	participantCtx = context.WithValue(participantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.SuspendProcess(participantCtx, instance.ProcessInstanceID, "test")
	assert.Error(t, err, "a task participant who is not the initiator must not be able to suspend the instance")

	initiatorCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	initiatorCtx = context.WithValue(initiatorCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.SuspendProcess(initiatorCtx, instance.ProcessInstanceID, "test")
	require.NoError(t, err, "the instance's own initiator must be able to suspend it")
}

func TestSuspendProcess_ElevatedAllowed(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("suspend-other").SetEmail("suspend-other@example.com").SetName("Other").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, _ := createProcessFixture(t, engine, tenantID, "suspend2")
	instance, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	err = engine.SuspendProcess(ctx, instance.ProcessInstanceID, "test")
	require.NoError(t, err, "an elevated caller must be able to suspend any instance")
}

func TestSuspendProcess_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	instanceID, _ := createProcessFixture(t, engine, tenantID, "suspend3")
	instance, err := engine.client.ProcessInstance.Get(context.Background(), instanceID)
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.SuspendProcess(ctx, instance.ProcessInstanceID, "test")
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied by default")
}

func TestResumeProcess_InitiatorAllowed(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	instanceID, _ := createProcessFixture(t, engine, tenantID, "resume1")
	instance, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).SetStatus("suspended").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.ResumeProcess(ctx, instance.ProcessInstanceID)
	require.NoError(t, err)
}

func TestTerminateProcess_NonInitiatorDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("terminate-other").SetEmail("terminate-other@example.com").SetName("Other2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, _ := createProcessFixture(t, engine, tenantID, "terminate1")
	instance, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	notInitiatorCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notInitiatorCtx = context.WithValue(notInitiatorCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.TerminateProcess(notInitiatorCtx, instance.ProcessInstanceID, "test")
	assert.Error(t, err)

	elevatedCtx := context.WithValue(notInitiatorCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TerminateProcess(elevatedCtx, instance.ProcessInstanceID, "test")
	require.NoError(t, err)
}

func TestSetProcessInstanceVariables_InitiatorAllowedParticipantDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	participant, err := engine.client.User.Create().
		SetUsername("vars-participant").SetEmail("vars-participant@example.com").SetName("Participant2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "instvars1")
	_, err = engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", participant.ID)).Save(context.Background())
	require.NoError(t, err)

	participantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, participant.ID)
	participantCtx = context.WithValue(participantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.ProcessInstanceService().SetProcessInstanceVariables(participantCtx, fmt.Sprintf("%d", instanceID), map[string]interface{}{"foo": "bar"})
	assert.Error(t, err, "a task participant who is not the initiator must not be able to set instance variables")

	initiatorCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	initiatorCtx = context.WithValue(initiatorCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	err = engine.ProcessInstanceService().SetProcessInstanceVariables(initiatorCtx, fmt.Sprintf("%d", instanceID), map[string]interface{}{"foo": "bar"})
	require.NoError(t, err)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestSuspendProcess_|TestResumeProcess_|TestTerminateProcess_|TestSetProcessInstanceVariables_' -v`
Expected: 除已有的 `TestSetProcessInstanceVariables_ParticipantAllowedAndAudited`(测的是任务级别的 `SetTaskVariables`,不是这里的 `SetProcessInstanceVariables`,函数不同,不受影响)外,新增测试全部 FAIL——当前代码没有任何参与者/发起人校验。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn_process_engine.go — 在 authorizeProcessInstanceViewer 函数之后新增

// authorizeProcessInstanceMutation gates suspend/resume/terminate/
// SetProcessInstanceVariables — instance-lifecycle administrative actions,
// deliberately narrower than authorizeProcessInstanceViewer: any task
// participant can VIEW instance progress, but only the instance's own
// initiator (cancelling/pausing their own request) or an elevated caller
// may control its lifecycle. A participant on one approval step within the
// instance has no business terminating the whole process.
func authorizeProcessInstanceMutation(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = instance.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, client, bpmn.NewGroupResolver(client), tenantID, userID)
	if err != nil {
		return fmt.Errorf("操作用户不存在: %w", err)
	}
	if instance.Initiator == identity.IDStr {
		return nil
	}
	return fmt.Errorf("当前用户无权操作该流程实例")
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — 在 SuspendProcess(第 1554 行起)
// "1. 获取流程实例" 之后、"2. 更新实例状态" 之前插入

	if err := authorizeProcessInstanceMutation(ctx, e.client, instance); err != nil {
		return err
	}
```

```go
// itsm-backend/service/bpmn_process_engine.go — 在 ResumeProcess(第 1603 行起)
// "1. 获取流程实例" 之后、"2. 更新实例状态" 之前插入，与 SuspendProcess 完全同样的一行检查

	if err := authorizeProcessInstanceMutation(ctx, e.client, instance); err != nil {
		return err
	}
```

```go
// itsm-backend/service/bpmn_process_engine.go — 在 TerminateProcess(第 1649 行起)
// "1. 获取流程实例" 之后、"2. 更新实例状态" 之前插入，与 SuspendProcess 完全同样的一行检查

	if err := authorizeProcessInstanceMutation(ctx, e.client, instance); err != nil {
		return err
	}
```

三处插入点在各自函数里的形状完全一致(`query := e.client.ProcessInstance.Query().Where(processinstance.ProcessInstanceID(processInstanceID))`……`instance, err := query.First(ctx)`……`if err != nil { return fmt.Errorf("获取流程实例失败: %w", err) }`,插入点紧跟在这个 `if err != nil` 块结束之后),`SuspendProcess`/`ResumeProcess`/`TerminateProcess` 三个函数体在这一段之外的内容不受影响。

```go
// itsm-backend/service/bpmn_process_engine.go — 替换 SetProcessInstanceVariables(第 2243-2260 行)

func (s *bpmnProcessInstanceService) SetProcessInstanceVariables(ctx context.Context, processInstanceID string, variables map[string]interface{}) error {
	instance, err := s.GetProcessInstance(ctx, processInstanceID)
	if err != nil {
		return err
	}
	if err := authorizeProcessInstanceMutation(ctx, s.client, instance); err != nil {
		return err
	}

	for _, reserved := range reservedInstanceVariableKeys {
		if _, exists := variables[reserved]; exists {
			return fmt.Errorf("变量 %q 由流程触发方管理，不允许经此端点覆盖", reserved)
		}
	}

	_, err = s.client.ProcessInstance.UpdateOne(instance).
		SetVariables(variables).
		Save(ctx)

	return err
}
```

- [ ] **Step 4: 控制器接入 elevated 上下文**

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 SetProcessInstanceVariables(第 567-589 行)

func (c *BPMNWorkflowController) SetProcessInstanceVariables(ctx *gin.Context) {
	processInstanceID := ctx.Param("id")

	var req struct {
		Variables map[string]interface{} `json:"variables" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "update")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	err := c.processEngine.ProcessInstanceService().SetProcessInstanceVariables(workflowCtx, processInstanceID, req.Variables)
	if err != nil {
		common.InternalError(ctx, "设置流程实例变量失败: "+err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "流程实例变量设置成功", nil)
}
```

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 SuspendProcess(第 592-614 行)

func (c *BPMNWorkflowController) SuspendProcess(ctx *gin.Context) {
	processInstanceID := ctx.Param("id")

	var req struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "update")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	err := c.processEngine.SuspendProcess(workflowCtx, processInstanceID, req.Reason)
	if err != nil {
		common.InternalError(ctx, "暂停流程实例失败: "+err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "流程实例暂停成功", nil)
}
```

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 ResumeProcess(第 617-631 行)

func (c *BPMNWorkflowController) ResumeProcess(ctx *gin.Context) {
	processInstanceID := ctx.Param("id")
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "update")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	err := c.processEngine.ResumeProcess(workflowCtx, processInstanceID)
	if err != nil {
		common.InternalError(ctx, "恢复流程实例失败: "+err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "流程实例恢复成功", nil)
}
```

```go
// itsm-backend/controller/bpmn_workflow_controller.go — 替换 TerminateProcess(第 634-656 行)

func (c *BPMNWorkflowController) TerminateProcess(ctx *gin.Context) {
	processInstanceID := ctx.Param("id")

	var req struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "update")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	err := c.processEngine.TerminateProcess(workflowCtx, processInstanceID, req.Reason)
	if err != nil {
		common.InternalError(ctx, "终止流程实例失败: "+err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "流程实例终止成功", nil)
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestSuspendProcess_|TestResumeProcess_|TestTerminateProcess_|TestSetProcessInstanceVariables_' -v`
Expected: 全部 PASS。

- [ ] **Step 6: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): SetProcessInstanceVariables/Suspend/Resume/TerminateProcess require initiator/elevated access"
```

---

### Task 7: 集中授权登记表 + 自动化守卫测试

**Files:**
- Create: `itsm-backend/service/bpmn/authorization_registry.go`
- Create: `itsm-backend/controller/bpmn_workflow_controller_authz_registry_test.go`

**Interfaces:**
- Produces: `bpmn.AuthPrimitive`、`bpmn.RouteAuthEntry`、`bpmn.BPMNTaskInstanceAuthRegistry`——供守卫测试引用;未来任何人工审查也可以直接读这张表。

- [ ] **Step 1: 写失败测试**

```go
// itsm-backend/controller/bpmn_workflow_controller_authz_registry_test.go

package controller

import (
	"strings"
	"testing"

	"itsm-backend/service/bpmn"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBPMNAuthorizationRegistryMatchesRegisteredRoutes guards against the
// exact class of gap this plan exists to close: GetCounterSignStatus shipped
// with zero authorization because no structured list of "every route needs
// an entry" existed. This test walks the actual gin routes RegisterRoutes
// produces and cross-checks them against bpmn.BPMNTaskInstanceAuthRegistry
// in both directions — a route with no registry entry, or a registry entry
// for a route that no longer exists, both fail the test.
func TestBPMNAuthorizationRegistryMatchesRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	controller := &BPMNWorkflowController{}
	controller.RegisterRoutes(group)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		path := strings.TrimPrefix(route.Path, "/api/v1")
		if !strings.HasPrefix(path, "/bpmn/tasks") && !strings.HasPrefix(path, "/bpmn/process-instances") {
			continue
		}
		registered[route.Method+" "+path] = true
	}

	registryKeys := make(map[string]bool, len(bpmn.BPMNTaskInstanceAuthRegistry))
	for _, entry := range bpmn.BPMNTaskInstanceAuthRegistry {
		key := entry.Method + " " + entry.Path
		registryKeys[key] = true
		assert.True(t, registered[key], "registry entry %s has no matching registered route —登记表和实际路由已经不一致", key)
	}
	for key := range registered {
		assert.True(t, registryKeys[key], "registered route %s has no authorization_registry.go entry — 新接口忘了登记", key)
	}
	require.NotEmpty(t, registered, "sanity check: RegisterRoutes must have produced at least one /bpmn/tasks or /bpmn/process-instances route")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./controller/... -run TestBPMNAuthorizationRegistryMatchesRegisteredRoutes -v`
Expected: FAIL(`bpmn.BPMNTaskInstanceAuthRegistry`/`bpmn.AuthPrimitive`/`bpmn.RouteAuthEntry` 还不存在,编译失败)。

- [ ] **Step 3: 写最小实现**

```go
// itsm-backend/service/bpmn/authorization_registry.go

package bpmn

// AuthPrimitive names which authorization function gates a route.
type AuthPrimitive string

const (
	// AuthPrimitiveTaskViewer: authorizeTaskViewer (participant/initiator/elevated read access to one task).
	AuthPrimitiveTaskViewer AuthPrimitive = "task_viewer"
	// AuthPrimitiveTaskMutation: authorizeTaskMutation (participant/elevated write access to one task).
	AuthPrimitiveTaskMutation AuthPrimitive = "task_mutation"
	// AuthPrimitiveCounterSignViewer: authorizeCounterSignViewer (parent-task-or-any-sub-task participant/elevated read access to counter-sign status).
	AuthPrimitiveCounterSignViewer AuthPrimitive = "counter_sign_viewer"
	// AuthPrimitiveTaskActor: authorizeTaskActor / isTaskCandidate (candidate-level check for claim/complete/decisions/vote).
	AuthPrimitiveTaskActor AuthPrimitive = "task_actor"
	// AuthPrimitiveParticipantScoped: ListUserTasks / ListProcessInstances forcing non-elevated callers to their own scope.
	AuthPrimitiveParticipantScoped AuthPrimitive = "participant_scoped"
	// AuthPrimitiveInstanceViewer: authorizeProcessInstanceViewer (participant/initiator/elevated read access to one process instance).
	AuthPrimitiveInstanceViewer AuthPrimitive = "instance_viewer"
	// AuthPrimitiveInstanceMutation: authorizeProcessInstanceMutation (initiator/elevated write access to one process instance's lifecycle).
	AuthPrimitiveInstanceMutation AuthPrimitive = "instance_mutation"
)

// RouteAuthEntry documents which authorization primitive, elevated
// resource:action pair, and system-caller policy applies to one route.
// This is the single source of truth this plan's guard test
// (controller/bpmn_workflow_controller_authz_registry_test.go) checks
// against the actual gin route registration — a route with no entry here
// fails that test, preventing a repeat of the GetCounterSignStatus gap
// (shipped with zero authorization because no such list existed).
type RouteAuthEntry struct {
	Method            string // "GET" / "POST" / "PUT" / "DELETE"
	Path              string // gin route pattern relative to /api/v1, e.g. "/bpmn/tasks/:id"
	Primitive         AuthPrimitive
	ElevatedResource  string // empty string means this route has no elevation concept
	ElevatedAction    string
	AllowSystemCaller bool // whether BPMNSystemCallerContextKey is expected to be honored here
}

// BPMNTaskInstanceAuthRegistry lists every /bpmn/tasks/* and
// /bpmn/process-instances/* route and the authorization primitive that
// gates it. AllowSystemCaller is false throughout: as of 2026-08-26, a
// full-codebase audit found zero real non-HTTP, non-test callers of any of
// the service methods these routes reach — see the design spec's Component
// 2 for the audit method and conclusion. If a real system-caller use case
// is added later, flip the relevant row to true and record why.
var BPMNTaskInstanceAuthRegistry = []RouteAuthEntry{
	{Method: "GET", Path: "/bpmn/tasks", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/tasks/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/assign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/claim", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/complete", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "POST", Path: "/bpmn/tasks/:id/decisions", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/cancel", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/variables", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "POST", Path: "/bpmn/tasks/:id/counter-sign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/tasks/:id/counter-sign-status", Primitive: AuthPrimitiveCounterSignViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/vote", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances/:id", Primitive: AuthPrimitiveInstanceViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances/:id/approval-history", Primitive: AuthPrimitiveInstanceViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/variables", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/suspend", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/resume", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/terminate", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./controller/... -run TestBPMNAuthorizationRegistryMatchesRegisteredRoutes -v`
Expected: PASS。如果失败并提示"registered route X has no entry"或"registry entry Y has no matching route",说明第 3 步抄路径/方法时打错了字——逐条核对 `controller/bpmn_workflow_controller.go` 的 `RegisterRoutes` 里 `/bpmn/tasks`/`/bpmn/process-instances` 两个分组的实际注册,不要靠猜。

- [ ] **Step 5: 全包测试**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn/authorization_registry.go controller/bpmn_workflow_controller_authz_registry_test.go
git commit -m "feat(bpmn): add centralized authorization registry + route-coverage guard test"
```

---

### Task 8: 路由准入改为仅需登录 + 菜单权限码改为 `bpmn:read`

**Files:**
- Modify: `itsm-backend/router/router.go:601`(`/my-approvals`)、`:1398`(`/workflow/instances`)、`:1399`(`/workflow/instances/:id`)、`:1405`(`/workflow/tasks`)
- Modify: `itsm-backend/pkg/seeder/seeder.go:1493-1499`(菜单种子注释 + `PermissionCode`)

**Interfaces:**
- 无新增函数签名——纯路由中间件/种子数据改动。

- [ ] **Step 1: 写失败测试(路由层面的行为测试)**

```go
// 追加到 itsm-backend/controller/bpmn_workflow_controller_authz_registry_test.go

// TestMyApprovalsRouteRequiresOnlyAuthentication 确认 /my-approvals、
// /workflow/tasks、/workflow/instances 三条"看自己数据"的路由，路由层面不再
// 要求 task:read/process_instance:read 权限码——可见范围完全由 service 层的
// 参与者收敛逻辑（ListUserTasks/ListProcessInstances，本分支之前的工作已经
// 建好）负责，不再靠权限码在路由层做二次限定。这个测试只检查路由注册时绑的
// gin.HandlerFunc 数量/种类，不需要起真实 HTTP server。
func TestMyApprovalsRouteRequiresOnlyAuthentication(t *testing.T) {
	// 本测试留空实现占位在 Step 3 之后由实现者根据 router.go 实际的路由组
	// 构造方式（gin.RouterGroup.Handlers()）编写——见 Step 1 后面的说明。
}
```

> 这条路由级别的行为，用 `gin.RouterGroup`/`gin.Engine` 直接做单元测试成本较高（`router.go` 的 `SetupRoutes` 需要完整的 `RouterConfig`，构造成本大），本任务改为用**代码走查 + 服务层测试**的方式验证，不新增一个真实的路由单元测试文件——步骤如下：

- [ ] **Step 1(替代方案): 确认服务层已经独立提供了保护**

`ListUserTasks`(Task 7,已在本分支之前的工作里完成)和 `ListProcessInstances`(Task 5,同上)在 `userID<=0` 且非提权时已经返回空列表(fail-closed),不依赖路由层权限码。运行:

Run: `cd itsm-backend && go test ./service/... -run 'TestListUserTasks_NoUserIDNonElevatedReturnsEmpty|TestListProcessInstances_NonElevatedSeesOnlyInitiatedOrParticipated' -v`
Expected: PASS(这两个测试已经存在,本任务不新增测试,只是确认它们仍然通过,作为"路由层放开权限码是安全的"这个判断的证据)。

- [ ] **Step 2: 修改路由**

```go
// itsm-backend/router/router.go — 第 601 行，删除 RequirePermission 中间件参数

// 修改前:
// tenant.GET("/my-approvals", middleware.RequirePermission("task", "read"), config.BPMNWorkflowController.ListUserTasks)
// 修改后:
tenant.GET("/my-approvals", config.BPMNWorkflowController.ListUserTasks)
```

```go
// itsm-backend/router/router.go — 第 1398-1399 行

// 修改前:
// workflow.GET("/instances", middleware.RequirePermission("process_instance", "read"), config.BPMNWorkflowController.ListProcessInstances)
// workflow.GET("/instances/:id", middleware.RequirePermission("process_instance", "read"), config.BPMNWorkflowController.GetProcessInstance)
// 修改后:
workflow.GET("/instances", config.BPMNWorkflowController.ListProcessInstances)
workflow.GET("/instances/:id", config.BPMNWorkflowController.GetProcessInstance)
```

```go
// itsm-backend/router/router.go — 第 1405 行

// 修改前:
// workflow.GET("/tasks", middleware.RequirePermission("task", "read"), config.BPMNWorkflowController.ListUserTasks)
// 修改后:
workflow.GET("/tasks", config.BPMNWorkflowController.ListUserTasks)
```

`/workflow/instances/:id/{terminate,suspend,resume}`(第 1401-1403 行)保持 `RequirePermission("process_instance","update")` 不变——这个权限码的分发范围本来就窄(只有 `it_director`/`ops_director`/`sysadmin`/`super_admin`),不是这次要收紧的三个权限码之一,而且 Task 6 已经给底层的 `SuspendProcess`/`ResumeProcess`/`TerminateProcess` 加上了 `authorizeProcessInstanceMutation`(发起人或提权才能操作)——真正的"发起人能操作自己的流程"这条路径走的是不带权限码门槛的 `/bpmn/process-instances/:id/{suspend,resume,terminate}` 规范路由,不依赖这条 `/workflow/*` 别名路由放宽。

- [ ] **Step 3: 修改菜单种子**

```go
// itsm-backend/pkg/seeder/seeder.go — 替换第 1493-1499 行

			// "我的待办"(/approvals/pending) 页面本身一直存在且能正常工作(BPMN UserTask 审批收件箱)，
			// 但从未被加入过菜单种子——旧的 "审批管理"(/admin/approvals) 菜单在 34e4b951 因为指向已删除
			// 的管理页面而被移除(见上面 SortOrder 260 处的注释)，但同一次改动没有补上这个真正给普通
			// 审批人用的收件箱页面的菜单项，导致任何角色都无法从侧边栏发现它，只能靠直接输入 URL。
			// PermissionCode 用 bpmn:read 而不是 task:read——2026-08-26 权限模型整改后，task:read
			// 收窄为纯"提权"信号，只有 sysadmin/it_director/ops_director/change_manager 持有；
			// bpmn:read 才是部门经理/普通用户等常规审批角色广泛持有、且已经是其它 BPMN 菜单项统一
			// 使用的可见性门槛，改用它保持一致，同时不会让这些角色的"我的待办"入口消失。
			{Name: "我的待办", Path: "/approvals/pending", Icon: "CheckSquare", PermissionCode: "bpmn:read", SortOrder: 75},
```

- [ ] **Step 4: 编译确认**

Run: `cd itsm-backend && go build ./...`
Expected: 编译通过。

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add router/router.go pkg/seeder/seeder.go
git commit -m "fix(bpmn): my-approvals/workflow-tasks/workflow-instances require only authentication, not task:read/process_instance:read"
```

---

### Task 9: 权限种子迁移——收紧 `task:read`/`process_instance:read`/`task:update` 分发范围

**Files:**
- Modify: `itsm-backend/pkg/seeder/seeder.go:1855-1872`(`dept_manager`)、`:1792-1802`(`service_catalog_admin`)、`:1892-1912`(`end_user`)
- Modify: `itsm-backend/pkg/seeder/seeder_test.go`(追加测试,复用既有的 `newTestSeeder` helper 和包导入)
- Create: `itsm-backend/migrations/20260826_bpmn_permission_model_e2e_fix.sql`

**Interfaces:**
- 无新增函数签名——纯权限种子数据改动 + 对应迁移脚本。

**已核实**:`s.expectedRolePermissions` 只在 `func (s *Seeder) seedRolePermissions(ctx context.Context)`(`pkg/seeder/seeder.go:1693`)内部赋值(第 1918 行 `s.expectedRolePermissions = rolePermissionMap`),这个方法同时会执行真实的 DB 写入,不是一个可以脱离数据库单独调用的纯函数——不能像最初设想的那样脱离 DB 做静态断言。改为跟随 `pkg/seeder/seeder_test.go` 已经建立的真实模式:用 `newTestSeeder(t, mode)` 起一个 `enttest` sqlite 内存库,跑真正的 `seeder.SeedAll(ctx)`,再用 ent client 查询种子结果——`TestSeedAllProductDefaultsDoNotCreateBusinessSamples` 等既有测试就是这个模式。

- [ ] **Step 1: 写失败测试(追加到 `itsm-backend/pkg/seeder/seeder_test.go`,复用文件已有的 `newTestSeeder` helper 和 `role`/`permission`/`rolepermission`/`menu` 包导入,不需要新增导入)**

```go
// 追加到 itsm-backend/pkg/seeder/seeder_test.go

func TestSeedAll_TightenedRolesLackBPMNElevatedPermissions(t *testing.T) {
	seeder, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	seeder.SeedAll(ctx)

	tightenedRoles := []string{"dept_manager", "end_user", "service_catalog_admin"}
	revokedCodes := []string{"task:read", "process_instance:read", "task:update"}

	for _, roleCode := range tightenedRoles {
		r, err := seeder.client.Role.Query().Where(role.CodeEQ(roleCode)).Only(ctx)
		require.NoError(t, err, "role %q must exist after seeding", roleCode)

		for _, permCode := range revokedCodes {
			perm, err := seeder.client.Permission.Query().Where(permission.CodeEQ(permCode)).Only(ctx)
			require.NoError(t, err, "permission %q must be a defined permission", permCode)

			has, err := seeder.client.RolePermission.Query().
				Where(rolepermission.RoleID(r.ID), rolepermission.PermissionID(perm.ID)).
				Exist(ctx)
			require.NoError(t, err)
			assert.False(t, has, "role %q must not hold %q after the 2026-08-26 permission model tightening", roleCode, permCode)
		}
	}

	myApprovalsMenu, err := seeder.client.Menu.Query().Where(menu.PathEQ("/approvals/pending")).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "bpmn:read", myApprovalsMenu.PermissionCode)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./pkg/seeder/... -run TestSeedAll_TightenedRolesLackBPMNElevatedPermissions -v`
Expected: FAIL(`dept_manager`/`end_user`/`service_catalog_admin` 目前仍持有这三个权限码,"我的待办"菜单的 `permission_code` 目前仍是 `task:read`)。

- [ ] **Step 3: 修改 seeder.go 三个角色的权限清单**

```go
// itsm-backend/pkg/seeder/seeder.go — service_catalog_admin，替换第 1792-1802 行

		"service_catalog_admin": {
			"service:read", "service:write",
			"service_catalog:read", "service_catalog:write", "service_catalog:delete",
			"service_request:read", "service_request:write", "service_request:delete", "service_request:provision",
			"ticket_template:read", "ticket_template:create", "ticket_template:update", "ticket_template:delete",
			"ticket_category:read", "ticket_category:create", "ticket_category:update",
			"workflow:read",
			"approval:read",
			"approval_workflow:read",
			"sla:read",
			"knowledge:read",
			"tag:read",
			// task:read/process_instance:read 已在 2026-08-26 权限模型整改中移除：这两个权限码
			// 现在是纯"提权看租户内所有任务/实例"信号，service_catalog_admin 是目录/模板配置角色，
			// 不需要跨用户查看审批数据；本角色查看自己参与的审批走 bpmn:read 门槛的 /my-approvals，
			// 不受影响。
		},
```

```go
// itsm-backend/pkg/seeder/seeder.go — dept_manager，替换第 1855-1872 行

		"dept_manager": {
			"ticket:read", "ticket:create", "ticket:update", "ticket:escalate", "incident:read",
			"problem:read", "change:read", "change:rollback", "report:read",
			"user:read", "department:read", "team:read",
			"knowledge:read", "release:read", "release:approve", "release:rollback",
			// release:approve/rollback 只让业务域 API（/releases/:id/approve 等）能调，
			// release:read 同理必须补：光有 approve 权限但没有 read，审批人连
			// GET /releases/:id（发布详情页）都会被 RBAC 拒 403，真实浏览器验证时
			// 点开发布详情直接 404，approve 按钮压根摸不到。
			// bpmn:read/bpmn:write：审批人查看"我的待办"（/api/v1/bpmn/tasks）、提交同意/拒绝
			// （POST /api/v1/bpmn/tasks/:id/decisions）走的是 /api/v1/bpmn/* 这组接口，实际
			// API 访问由 middleware.RequireLegacyBPMNRoles() 固定角色 allowlist 把关，
			// dept_manager 本身就在该 allowlist 里，所以这两个 bpmn:* 授权现在只控制 BPMN
			// 菜单可见性（menu_service.go），不再是 API 能不能调的门槛——保留它们是为了让
			// 部门经理在菜单里也能看到"我的待办"入口，不是因为撤掉会导致 403。
			// task:read 已在 2026-08-26 权限模型整改中移除：这个权限码现在是纯"提权看全部"
			// 信号，dept_manager 只能看到自己参与/发起的任务，不再默认能看到别人的——这正是
			// 本次整改要修的问题（之前 task:read 被误当成"我的待办"的访问门槛，导致几乎所有
			// 角色都被误判为提权）。
			"bpmn:read", "bpmn:write",
		},
```

```go
// itsm-backend/pkg/seeder/seeder.go — end_user，替换第 1892-1912 行

		"end_user": {
			"ticket:read", "ticket:create", "ticket:update", "knowledge:read", "service_catalog:read",
			"ticket_category:read", "ticket_template:read", "notification:read",
			"tag:read",
			// service_request:write/read：没有这条，终端用户从服务目录发起自助申请
			// （POST /api/v1/service-requests，服务目录的核心使用场景）会被全局 RBAC
			// 直接拒在 handler 之前，报"权限不足"——end_user 是唯一预期会调这个接口的
			// 角色，之前只有 it_director/ops_director/sysadmin/service_catalog_admin
			// 这些管理级角色有，等于普通用户永远走不通自助服务目录。
			"service_request:write", "service_request:read",
			// bpmn:read/bpmn:write：服务请求 BPMN 流程里第一个节点（Activity_Accept"请求受理"）
			// 没有声明 candidateGroups/candidateUsers 时，bpmn_process_engine.go 会把它默认
			// 分配给 requester_id 本人——也就是说申请人自己就是审批链路上第一个"任务受理人"。
			// /api/v1/bpmn/* 的实际 API 访问现在由 middleware.RequireLegacyBPMNRoles() 固定角色
			// allowlist 把关（end_user 本身就在该 allowlist 里），所以这两条 bpmn:* 授权现在只
			// 控制 BPMN 菜单可见性，不是接口能不能调的门槛，保留它们是为了让申请人在菜单里也能
			// 看到相应入口。"我的待办"菜单项（PermissionCode: "bpmn:read"）也靠这条授权可见。
			// task:read 已在 2026-08-26 权限模型整改中移除：/api/v1/workflow/tasks、
			// /api/v1/tenant/my-approvals 这两条路由不再要求这个权限码（改为仅需登录），
			// 可见范围完全由 ListUserTasks 的参与者收敛逻辑负责——end_user 不再需要持有这个
			// 权限码就能看到自己的"我的待办"，而这个权限码本身收窄为纯提权信号，end_user 不应
			// 该拥有它（否则又会被误判为能看到全租户任务，重蹈这次要修的问题）。
			"bpmn:read", "bpmn:write",
		},
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd itsm-backend && go test ./pkg/seeder/... -run TestSeedAll_TightenedRolesLackBPMNElevatedPermissions -v`
Expected: PASS。

- [ ] **Step 5: 写迁移脚本**

```sql
-- itsm-backend/migrations/20260826_bpmn_permission_model_e2e_fix.sql
--
-- Migration: 20260826_bpmn_permission_model_e2e_fix
-- Description: 收紧 task:read/process_instance:read/task:update 的分发范围，
--              把"我的待办"菜单的可见性门槛从 task:read 改为 bpmn:read。
--
-- 背景：task:read/process_instance:read 此前同时承担三个职责——路由准入
-- （/my-approvals、/workflow/tasks、/workflow/instances 用 RequirePermission
-- 把关）、提权信号（hasElevatedBPMNAccess 用它判断"是否能看到全租户任务/实例"）、
-- 菜单可见性（"我的待办"菜单项）。默认种子数据里这两个权限码几乎发给了所有能
-- 碰到 BPMN 接口的角色，导致提权判断对绝大多数真实用户永远为真，参与者范围限定
-- （2026-08-25-bpmn-task-instance-authorization 分支的工作）在默认配置下基本
-- 不生效。本迁移配合同一 PR 的代码改动（路由准入改为仅需登录，菜单可见性改用
-- bpmn:read），把这两个权限码收窄为纯"提权"信号。
--
-- 改动前状态（供未来撤销参考，撤销方式是照此写一条新的正向迁移，参见
-- migrations/20260814_revert_end_user_overgrant.sql 的先例——这个仓库的
-- migrations/*.sql 是单向 forward-only 迁移，不接入 migration/migrator.go
-- 的 RollbackSQL/RollbackMigration 机制，那套机制服务于另一批 Go 结构体
-- 定义的迁移）：
--   - dept_manager 的 role_permissions 里有 task:read（附带 bpmn:read/bpmn:write，本迁移不动后两个）
--   - end_user 的 role_permissions 里有 task:read（附带 bpmn:read/bpmn:write，本迁移不动后两个）
--   - service_catalog_admin 的 role_permissions 里有 task:read、process_instance:read
--   - menus 表"我的待办"行（path = '/approvals/pending'）的 permission_code 是 'task:read'

-- Step 1: 收紧 dept_manager
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'dept_manager')
  AND permission_id IN (SELECT id FROM permissions WHERE code = 'task:read');

-- Step 2: 收紧 end_user
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'end_user')
  AND permission_id IN (SELECT id FROM permissions WHERE code = 'task:read');

-- Step 3: 收紧 service_catalog_admin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'service_catalog_admin')
  AND permission_id IN (
    SELECT id FROM permissions WHERE code IN ('task:read', 'process_instance:read')
  );

-- Step 4: "我的待办"菜单可见性门槛从 task:read 改为 bpmn:read
UPDATE menus SET permission_code = 'bpmn:read'
WHERE path = '/approvals/pending' AND permission_code = 'task:read';
```

> 如果实际的 `roles`/`permissions`/`role_permissions`/`menus` 表结构和这里假设的列名（`role_id`/`permission_id`/`code`/`permission_code`/`path`）不完全一致,以 `migrations/20260812_fill_missing_permissions.sql` 和 `migrations/20260628_add_connector_menu.sql` 这两个已经在用的迁移文件里的真实写法为准,不要凭空假设列名。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add pkg/seeder/seeder.go pkg/seeder/seeder_test.go migrations/20260826_bpmn_permission_model_e2e_fix.sql
git commit -m "fix(rbac): tighten task:read/process_instance:read/task:update seed distribution, my-approvals menu uses bpmn:read"
```

---

### Task 10: 全量回归 + SSL-VPN E2E 重跑

**Files:** 无(纯验证)

- [ ] **Step 1: 全量构建**

Run: `cd itsm-backend && go build ./...`
Expected: 干净通过,无错误。

- [ ] **Step 2: 全量测试**

Run: `cd itsm-backend && go test ./...`
Expected: 全部包通过,报告通过的包数量(no `FAIL` 行)。

- [ ] **Step 3: 重点回归——SSL-VPN E2E**

Run: `cd itsm-backend && go test ./tests/e2e/... -run TestSSLVPN -v`(如果测试函数名不叫这个,先用 `grep -n "^func Test" tests/e2e/sslvpn_scenario_test.go` 找到真实的测试函数名)
Expected: PASS。这条测试专门守护"粗粒度角色门槛/权限收紧不能误拒合法候选人"这类回归——RBAC 收敛分支就在这里真实抓到过一次误拒(network_eng 候选人被粗粒度门槛挡在外面)，这次收紧 `task:read`/`process_instance:read`/`task:update` 种子分发范围是同一类风险,必须让这条测试跑一遍且保持绿色,不能只看 `go test ./...` 整体通过就下结论。

- [ ] **Step 4: RBAC 回归目录重跑**

Run: `cd itsm-backend && go test ./tests/rbac/... -v`
Expected: PASS。确认收紧 `dept_manager`/`end_user`/`service_catalog_admin` 的权限没有意外波及这三个角色的其它、本次没有打算动的权限。

- [ ] **Step 5: 记录并报告**

把 Step 2 的通过包数量、Step 3/Step 4 的具体测试名和结果,整理进最终报告——不要只说"全部通过",要给出可核对的具体数字和测试名(遵循本仓库 AGENTS.md"运行完整测试套件并报告通过数量"的要求)。

---

## 完成后

全部 10 个任务完成、全量测试通过后,使用 superpowers:finishing-a-development-branch 决定这个分支(`worktree-bpmn-task-instance-authorization`)接下来怎么处理——注意此时这个分支上还叠着更早的 `2026-08-25-bpmn-task-instance-authorization` 9 任务的全部提交,一并考虑要不要在同一次合并里处理。
