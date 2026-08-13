# 审批收敛到 BPMN 收尾 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成 `docs/superpowers/specs/2026-08-13-approval-bpmn-convergence-completion-design.md` 定义的剩余工作——修复 `need_approval` 变量命名 bug、补齐 CAB 审批声明式属性、执行遗留审批引擎的批量迁移并下线、把 `handlers/change` 自己的独立审批状态机迁移到 BPMN、清理三处孤儿代码（Track 2b/5/6）、核实前端现状。

**Architecture:** 不引入新的执行模型。全部工作是把已经验证过、已经部分落地的"静态 BPMN 节点 + 声明式属性（`assigneeRole`/`assigneeDeptId`/`candidateGroups`）"模式应用到剩余节点和剩余业务域（变更管理），并把不再需要的旧代码删除。`ApprovalChain`/`ApprovalChainResolver` 保持"仅展示、不驱动执行"的既有定位，不改动。

**Tech Stack:** Go 1.x / Gin / Ent ORM / PostgreSQL / `expr-lang/expr`（BPMN 网关条件求值）/ `stretchr/testify`（测试）

## Global Constraints

- Controller 必须返回 DTO，禁止直接返回 Ent 模型。
- 新增/修改的审批相关操作必须写 `ProcessAuditLog`（通过 `BPMNAuditService`）。
- 涉及租户隔离的查询改动必须带上对应的跨租户回归测试。
- 不引入兼容层/桥接层/双写：本计划里的每次"切换到新路径"都配一次"删除旧路径"，不允许旧路径继续存活作为回退选项（历史审批记录本身已确认不需要保留）。
- Go 文件命名沿用 `snake_case`；不新建文件时优先在已有文件里追加。
- 每个任务完成后运行该任务改动范围内的 `go test`，全部任务完成后运行一次 `cd itsm-backend && go build ./...`。

---

## Task 1: 删除 Track 2b —— 已注册但空转的审批任务 stub

**背景**：`service/bpmn/approval_handler.go` 的 `ApprovalHandler` 已经注册进 callback registry（`service/bpmn/bpmn_callback_registry.go:134`），但 `submitApproval`/`approve`/`reject`/`delegate`/`escalateApproval` 全部只打日志、返回编造的成功结果。如果有已部署的流程定义声明了 `taskType="approval_task"` 的 ServiceTask，会拿到假成功。删除前必须先确认没有活跃引用。

**Files:**
- Modify: `itsm-backend/service/bpmn/bpmn_callback_registry.go:134`（删除注册行）
- Delete: `itsm-backend/service/bpmn/approval_handler.go`
- Delete: `itsm-backend/service/bpmn/approval_handler_test.go`（如存在）

**Interfaces:**
- 无对外接口变化——`ApprovalHandler` 未被任何路由/其他 service 直接引用（只通过 `GetTaskType()=="approval_task"` 间接匹配）。

- [ ] **Step 1: 确认没有已部署的流程定义引用 `approval_task`**

```bash
cd itsm-backend
grep -rln 'taskType="approval_task"\|serviceTaskType.*approval_task' service/bpmn/*.bpmn
```

Expected: 无输出（0 个文件命中）。如果有输出，停止本任务，把命中的文件记录下来跟人类确认——不能删除一个仍被引用的 handler。

- [ ] **Step 2: 删除文件**

```bash
rm itsm-backend/service/bpmn/approval_handler.go
[ -f itsm-backend/service/bpmn/approval_handler_test.go ] && rm itsm-backend/service/bpmn/approval_handler_test.go
```

- [ ] **Step 3: 删除注册行**

在 `itsm-backend/service/bpmn/bpmn_callback_registry.go` 的 `registerDefaultHandlers` 里删除：

```go
	// 注册审批处理器
	r.RegisterHandler(NewApprovalHandler(r.client, r.logger))
```

- [ ] **Step 4: 编译验证**

```bash
cd itsm-backend && go build ./...
```

Expected: 编译通过，无 `NewApprovalHandler`/`ApprovalHandler` 未定义错误（说明没有其他地方还在引用它）。

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn/bpmn_callback_registry.go
git add -u itsm-backend/service/bpmn/approval_handler.go itsm-backend/service/bpmn/approval_handler_test.go
git commit -m "$(cat <<'EOF'
refactor(bpmn): 删除空转的审批任务 stub handler

submitApproval/approve/reject/delegate/escalateApproval 全部只打日志、
返回编造的成功结果，已注册进 callback registry 但会对任何声明
taskType="approval_task" 的流程节点产生假成功。确认没有已部署的流程
定义引用后删除。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 删除 Track 5 / Track 6 —— 未接线的变更审批孤儿代码

**背景**：`controller/change_approval_controller.go` + `service/change_approval_service.go` + `service/cab_service.go`（Track 5，原始 SQL）和 `service/change_service.go` 里的 `ChangeService` 类型（Track 6）都已确认全仓库零构造调用。但 `change_service.go` 里的独立函数 `IsValidChangeStatusTransition`/`CloseChangeApprovalChains` 正被 `handlers/change/service.go` 实际调用，**不能整个文件删除**，只删 `ChangeService` 结构体和它的方法。

**Files:**
- Delete: `itsm-backend/controller/change_approval_controller.go`
- Delete: `itsm-backend/service/change_approval_service.go`
- Delete: `itsm-backend/service/cab_service.go`
- Delete: 上述三个文件对应的 `_test.go`（如存在）
- Modify: `itsm-backend/service/change_service.go`（只删 `ChangeService` 类型与其方法，保留独立函数）

**Interfaces:**
- Consumes: 无（这些代码本身零调用方）
- Produces: `service/change_service.go` 删减后仍导出 `IsValidChangeStatusTransition(currentStatus, newStatus, changeType string) bool` 和 `CloseChangeApprovalChains(ctx context.Context, changeID, tenantID int) error`，供 `handlers/change/service.go` 继续使用。

- [ ] **Step 1: 再次确认零构造调用（防止上次核实之后有新代码引用）**

```bash
cd itsm-backend
grep -rn "NewChangeApprovalController\|NewChangeApprovalService\|NewCABService\|NewChangeService\b" --include="*.go" . | grep -v "_test.go"
```

Expected: 只命中这几个函数自己的定义行（各 1 处），没有其他调用方。

- [ ] **Step 2: 删除 Track 5 三个文件**

```bash
rm itsm-backend/controller/change_approval_controller.go
rm itsm-backend/service/change_approval_service.go
rm itsm-backend/service/cab_service.go
rm -f itsm-backend/controller/change_approval_controller_test.go
rm -f itsm-backend/service/change_approval_service_test.go
rm -f itsm-backend/service/cab_service_test.go
```

- [ ] **Step 3: 从 `change_service.go` 删除 `ChangeService` 类型和它的方法**

删除以下声明（`itsm-backend/service/change_service.go`，按 `grep -n "^func \|^type "` 输出的范围）：`type ChangeService struct`、`NewChangeService`、`SetProcessTriggerService`、`SetApprovalService`、`CreateChange`、`validateCreateChange`、`validateChangeReferences`、`GetChange`、`ListChanges`、`UpdateChange`、`DeleteChange`、`GetChangeStats`、`UpdateChangeStatus`、`triggerWorkflowForChange`、`GetWorkflowStatus`、`mapProcessStatus`、`GetCalendarView`。

保留：`isValidChangeType`/`isValidChangePriority`/`isValidChangeImpact`/`isValidChangeRisk`/`uniqueNonEmptyStrings`/`optionalTime`/`optionalInt`/`persistedChangeStatus`/`apiChangeStatus`/`isTerminalChangeStatus`/`CloseChangeApprovalChains`/`IsValidChangeStatusTransition` ——这些是独立函数，先用 `grep -rn` 确认还有没有外部调用方，全部保留在文件里（不改名、不搬家，只是这个文件不再有 `ChangeService` 类型）。

- [ ] **Step 2: 编译验证**

```bash
cd itsm-backend && go build ./...
```

Expected: 编译通过。

- [ ] **Step 3: Commit**

```bash
git add -A itsm-backend/controller/change_approval_controller.go itsm-backend/service/change_approval_service.go itsm-backend/service/cab_service.go itsm-backend/service/change_service.go
git commit -m "$(cat <<'EOF'
refactor(change): 删除未接线的变更审批孤儿实现（Track5/6）

change_approval_controller.go/change_approval_service.go/cab_service.go
（原始 SQL 操作 change_approvals 表）和 change_service.go 里的
ChangeService 类型，全仓库确认零构造调用，是历史遗留的独立实现。
change_service.go 里仍被 handlers/change 使用的
IsValidChangeStatusTransition/CloseChangeApprovalChains 保留。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 修复 `need_approval`/`approval_required` 变量命名不一致

**背景**：`ticket_service.go`/`handlers/service_request/service.go` 设置的流程变量是 `approval_required`，但 `service_request_flow.bpmn`、`service_request_urgent_flow.bpmn`、`change_normal_flow.bpmn`、`change_emergency_flow.bpmn` 四个已部署种子流程的网关条件读的是 `need_approval`。任何应该走审批的流程，网关条件永远为 false，直接跳过审批。

**Files:**
- Modify: `itsm-backend/service/bpmn/service_request_flow.bpmn`
- Modify: `itsm-backend/service/bpmn/service_request_urgent_flow.bpmn`
- Modify: `itsm-backend/service/bpmn/change_normal_flow.bpmn`
- Modify: `itsm-backend/service/bpmn/change_emergency_flow.bpmn`
- Test: `itsm-backend/service/bpmn_approval_gateway_variable_test.go`（新建）

**Interfaces:**
- Consumes: `CustomProcessEngine.StartProcess(ctx, processDefinitionKey, businessKey, variables map[string]interface{}) (*ent.ProcessInstance, error)`（已有方法，签名见 `service/bpmn_process_engine.go`）
- Produces: 无新接口，只是修正已部署 XML 里的变量名。

- [ ] **Step 1: 写失败测试——先证明 bug 存在**

创建 `itsm-backend/service/bpmn_approval_gateway_variable_test.go`：

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestApprovalGatewayReadsApplicationVariableName 是一个回归测试：
// 应用代码统一使用 approval_required 作为流程变量名，四个已部署种子流程
// 的审批网关必须读同一个变量名，不能各写各的（这正是 need_approval /
// approval_required 命名不一致导致审批被跳过的那个 bug 的真实场景）。
func TestApprovalGatewayReadsApplicationVariableName(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger)

	deploymentSvc := NewBPMNTemplateService(client, logger)
	ctx := context.Background()
	tenantID := 1

	_, err := deploymentSvc.LoadAndDeployTemplates(ctx, tenantID)
	require.NoError(t, err)

	cases := []struct {
		processKey    string
		approvalNode  string
		skipNode      string
	}{
		{"service_request_flow", "Activity_Approval", "Activity_Execute"},
		{"change_normal_flow", "Activity_CABApproval", "Activity_Schedule"},
	}

	for _, tc := range cases {
		t.Run(tc.processKey, func(t *testing.T) {
			instance, err := engine.StartProcess(ctx, tc.processKey, "test-business-key-"+tc.processKey, map[string]interface{}{
				"approval_required": true,
			})
			require.NoError(t, err)
			require.Equal(t, tc.approvalNode, instance.CurrentActivityID,
				"approval_required=true 时流程必须停在审批节点，不能跳过审批直接往后走")
		})
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd itsm-backend && go test ./service/... -run TestApprovalGatewayReadsApplicationVariableName -v
```

Expected: FAIL——`instance.CurrentActivityID` 会是 `Activity_Execute`/`Activity_Schedule`（跳过了审批），不是预期的审批节点。

- [ ] **Step 3: 修复四个 BPMN 文件**

在下列文件里，把所有 `variables['need_approval']` 替换成 `variables['approval_required']`：

- `itsm-backend/service/bpmn/service_request_flow.bpmn`（2 处：`Flow_3`/`Flow_4` 的 conditionExpression）
- `itsm-backend/service/bpmn/service_request_urgent_flow.bpmn`（同样 2 处）
- `itsm-backend/service/bpmn/change_normal_flow.bpmn`（`Flow_3`/`Flow_Schedule` 的 conditionExpression）
- `itsm-backend/service/bpmn/change_emergency_flow.bpmn`（同上）

```bash
cd itsm-backend
sed -i "s/variables\['need_approval'\]/variables['approval_required']/g" \
  service/bpmn/service_request_flow.bpmn \
  service/bpmn/service_request_urgent_flow.bpmn \
  service/bpmn/change_normal_flow.bpmn \
  service/bpmn/change_emergency_flow.bpmn

grep -rn "need_approval" service/bpmn/*.bpmn
```

Expected: `grep` 无输出（4 个文件里都不再有 `need_approval`）。

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd itsm-backend && go test ./service/... -run TestApprovalGatewayReadsApplicationVariableName -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn/service_request_flow.bpmn itsm-backend/service/bpmn/service_request_urgent_flow.bpmn itsm-backend/service/bpmn/change_normal_flow.bpmn itsm-backend/service/bpmn/change_emergency_flow.bpmn itsm-backend/service/bpmn_approval_gateway_variable_test.go
git commit -m "$(cat <<'EOF'
fix(bpmn): 修复审批网关变量名不一致导致审批被跳过

应用代码统一设置 approval_required 流程变量，但 4 个已部署种子流程
（service_request_flow/service_request_urgent_flow/change_normal_flow/
change_emergency_flow）的审批网关条件读的是从未被任何代码设置过的
need_approval，导致所有应走审批的流程实例都直接跳过审批节点。补充
端到端回归测试：真实部署模板、启动实例、断言流程真的停在审批节点。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 补齐 CAB 审批节点的声明式属性

**背景**：`change_normal_flow.bpmn`/`change_emergency_flow.bpmn` 的 `Activity_CABApproval` 节点没有任何声明式属性，落到"申请人自己部门负责人"的默认兜底，不是真正的 CAB 成员。种子 `roles` 数据里已有 `change_manager` 角色（历史上曾在已删除的 `ApprovalWorkflow` 种子里被用作"变更经理审批"角色），沿用同一个角色，不发明新概念。

**Files:**
- Modify: `itsm-backend/service/bpmn/change_normal_flow.bpmn`
- Modify: `itsm-backend/service/bpmn/change_emergency_flow.bpmn`
- Test: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`（已存在，追加用例）

**Interfaces:**
- Consumes: `createUserTask` 里已有的 `assigneeRole` 解析路径（`service/bpmn_process_engine.go`，按角色查候选人、排除申请人自己、无候选人时转 `ticket-approvers` 候选组兜底）——本任务不改这段逻辑，只是让 CAB 节点真正声明这个属性。
- Produces: 无新接口。

- [ ] **Step 1: 在两个 BPMN 文件里给 `Activity_CABApproval` 加 `assigneeRole` 属性**

`itsm-backend/service/bpmn/change_normal_flow.bpmn`，把：

```xml
    <bpmn:userTask id="Activity_CABApproval" name="CAB审批">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">approve_change</bpmn:metaData>
      </bpmn:extensionElements>
```

改成：

```xml
    <bpmn:userTask id="Activity_CABApproval" name="CAB审批" taskPurpose="approval" assigneeRole="change_manager">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">approve_change</bpmn:metaData>
      </bpmn:extensionElements>
```

`itsm-backend/service/bpmn/change_emergency_flow.bpmn` 做同样的改动（两个文件的 `Activity_CABApproval` 节点结构完全一致）。

- [ ] **Step 2: 追加测试用例**

在 `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go` 里追加（参照该文件里已有测试的 setup 方式）：

```go
func TestCABApprovalAssignsChangeManagerRole(t *testing.T) {
	client, engine, tenantID, cleanup := setupApprovalAssignmentTest(t) // 复用文件里已有的 setup helper
	defer cleanup()
	ctx := context.Background()

	// 建一个 role=change_manager 的用户
	cmUser := client.User.Create().
		SetUsername("cm_user").
		SetEmail("cm@example.com").
		SetRole("change_manager").
		SetTenantID(tenantID).
		SetIsActive(true).
		SaveX(ctx)

	// 申请人自己不是 change_manager，避免被排除逻辑误判
	requester := client.User.Create().
		SetUsername("requester").
		SetEmail("requester@example.com").
		SetRole("end_user").
		SetTenantID(tenantID).
		SetIsActive(true).
		SaveX(ctx)

	instance, err := engine.StartProcess(ctx, "change_normal_flow", "test-cab-approval", map[string]interface{}{
		"approval_required": true,
		"requester_id":      requester.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "Activity_CABApproval", instance.CurrentActivityID)

	task := client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_CABApproval")).
		OnlyX(ctx)

	require.Contains(t, task.CandidateUsers, cmUser.Username,
		"CAB 审批候选人必须包含 role=change_manager 的用户")
}
```

（如果文件里没有现成的 `setupApprovalAssignmentTest` helper，改成直接照抄文件里其它测试函数开头的 client/engine/tenant 初始化代码，不要新发明一套 setup 方式。）

- [ ] **Step 3: 运行测试**

```bash
cd itsm-backend && go test ./service/... -run "TestCABApprovalAssignsChangeManagerRole" -v
```

Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add itsm-backend/service/bpmn/change_normal_flow.bpmn itsm-backend/service/bpmn/change_emergency_flow.bpmn itsm-backend/service/bpmn_process_engine_approval_assignment_test.go
git commit -m "$(cat <<'EOF'
fix(bpmn): CAB 审批节点补齐声明式属性，不再落到部门负责人兜底

change_normal_flow.bpmn/change_emergency_flow.bpmn 的 Activity_CABApproval
此前没有任何 assigneeRole/candidateGroups 属性，实际会解析成"申请人自己
部门负责人"，不是真正的 CAB 成员，违反变更管理必须保留 CAB 概念的要求。
补上 assigneeRole="change_manager"，复用已有角色，不新建组织概念。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 执行批量迁移 CLI（存量 ApprovalWorkflow → BPMN）

**背景**：`cmd/migrate_legacy_approvals/main.go` 是完整的、默认 dry-run 的迁移工具，代码已经完成但从未真正执行过（git log 只有一次"新增"提交）。本任务是运维操作 + 验证，不是写新代码。

**Files:**
- 无代码改动（本任务是执行既有工具 + 验证）
- Test: `itsm-backend/cmd/migrate_legacy_approvals/main_test.go`（如已有单测覆盖 dry-run 逻辑，跳过；如没有，本任务不新增，迁移逻辑本身的单测属于 `legacy_approval_migration_service_test.go` 的既有职责范围，不在这次任务里重复造）

**Interfaces:**
- Consumes: `LegacyApprovalMigrationService.MigrateAllTenants(ctx, dryRun bool) (map[int][]*LegacyApprovalMigrationResult, error)`（已有方法）

- [ ] **Step 1: 在本地/dev 环境跑 dry-run，人工核对输出**

```bash
cd itsm-backend
go run ./cmd/migrate_legacy_approvals -dry-run=true
```

核对输出里每个租户的每条 `ApprovalWorkflow` 记录：
- 生成的 BPMN XML 是否包含正确数量的 UserTask 节点（对应原 workflow 的 node 数）。
- 每个节点的声明式属性（`assignee`/`candidateGroups`/`assigneeRole`/`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`）是否对应原节点的 `assigneeType`。
- 任何 `assigneeType == "amount_based"` 的节点是否被正确跳过并输出明确警告（不能静默丢弃整个工作流的其余节点）。

- [ ] **Step 2: 确认 dry-run 输出无误后，真正执行**

```bash
go run ./cmd/migrate_legacy_approvals -dry-run=false
```

- [ ] **Step 3: 验证——创建一个会触发审批的工单，确认走新流程**

用一个已知配置过 `ApprovalWorkflow` 的租户，通过真实 HTTP 客户端（不是内部函数调用）创建一个会命中该 workflow 条件的工单：

```bash
curl -s -X POST http://localhost:8090/api/v1/tickets \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"迁移验证工单","type":"service_request","priority":"high"}' | jq .
```

然后查这张工单关联的流程实例，确认 `process_definition_key` 是迁移出来的 BPMN 流程（不是 `ticket_general_flow` 兜底），且 `current_activity_id` 停在了正确的审批节点：

```bash
curl -s http://localhost:8090/api/v1/tickets/<ticket_id>/workflow-status \
  -H "Authorization: Bearer $TOKEN" | jq .
```

- [ ] **Step 4: 记录迁移结果**

把 dry-run 和真实执行的完整输出保存一份（迁移了哪些租户的哪些 workflow，跳过了哪些 amount_based 节点），作为 Task 6 下线旧引擎前的验证依据——不写进代码仓库，作为本次实施的操作记录留给人类确认。

---

## Task 6: 下线 legacy `ApprovalWorkflow`/`ApprovalRecord` 引擎

**背景**：Task 5 迁移完成并验证后，`controller/approval_controller.go`、`service/approval_service.go`、`service/legacy_approval_migration_service.go`、`cmd/migrate_legacy_approvals`、对应路由和 Ent schema 可以整体下线。`ApprovalChain`（`/admin/approval-chains`，`ent/schema/approvalchain.go`）是独立机制，不在这次删除范围。

**Files:**
- Delete: `itsm-backend/controller/approval_controller.go`
- Delete: `itsm-backend/service/approval_service.go`
- Delete: `itsm-backend/service/legacy_approval_migration_service.go`
- Delete: `itsm-backend/cmd/migrate_legacy_approvals/`（整个目录）
- Delete: 上述文件对应的 `_test.go`
- Modify: `itsm-backend/router/router.go`（删除 `/approval-workflows`、`/approvals` 路由组）
- Modify: `itsm-backend/internal/bootstrap/app.go`（删除 `ApprovalController`/`ApprovalService` 的构造与注入）
- Delete: `itsm-backend/ent/schema/approval_workflow.go`
- Delete: `itsm-backend/ent/schema/approval_record.go`
- Modify: 运行 `go generate ./ent` 重新生成 Ent 代码

**Interfaces:**
- 无对外接口保留——这是纯下线，不是废弃标记。`TicketService` 对 `ApprovalService` 的字段/setter（`ticket_service.go` 的 `approvalSvc`/`SetApprovalService`）也要清理，因为它已经是 Task 5 之前就确认的零调用死引用。

- [ ] **Step 1: 确认迁移已完成、旧引擎无残留依赖方**

```bash
cd itsm-backend
grep -rn "approvalSvc\|ApprovalService\b" service/ticket_service.go
grep -rn "config.ApprovalController\|config.ApprovalService" internal/bootstrap/app.go router/router.go
```

核对 Task 5 的迁移记录，确认所有租户的所有 `ApprovalWorkflow` 记录都已迁移（`MigrateAllTenants` 返回的结果里没有失败项，或失败项已经人工确认可以放弃）。

- [ ] **Step 2: 删除代码文件**

```bash
rm itsm-backend/controller/approval_controller.go
rm -f itsm-backend/controller/approval_controller_test.go
rm itsm-backend/service/approval_service.go
rm -f itsm-backend/service/approval_service_test.go
rm itsm-backend/service/legacy_approval_migration_service.go
rm -f itsm-backend/service/legacy_approval_migration_service_test.go
rm -rf itsm-backend/cmd/migrate_legacy_approvals
```

- [ ] **Step 3: 删除路由（`router/router.go`）**

删除 `/tickets/approval/submit`、`/tickets/approval/records`、`/approval-workflows` 路由组、兼容路径 `/approvals` 路由组（对照本次会话核实时定位的行号 592-618 附近，实际以当前文件内容为准，找 `config.ApprovalController.` 的所有匹配行整段删除）。

- [ ] **Step 4: 删除 bootstrap 里的构造与注入（`internal/bootstrap/app.go`）**

删除 `ApprovalController`/`ApprovalService` 的 `New...` 构造调用，以及 `ticketService.SetApprovalService(approvalService)` 这一行注入。

- [ ] **Step 5: 从 `ticket_service.go` 删除已经零调用的 `approvalSvc` 字段和 setter**

```go
// 删除字段声明
approvalSvc            *ApprovalService

// 删除
func (s *TicketService) SetApprovalService(a *ApprovalService) {
	s.approvalSvc = a
}
```

（如果 `TicketServiceConfig` 结构体里也有对应的 `ApprovalService` 字段，一并删除。）

- [ ] **Step 6: 删除 Ent schema，重新生成**

```bash
rm itsm-backend/ent/schema/approval_workflow.go
rm itsm-backend/ent/schema/approval_record.go
cd itsm-backend && go generate ./ent/...
```

- [ ] **Step 7: 新增迁移文件，DROP 对应的表**

在 `itsm-backend/migration/migrations.go` 里追加一条新迁移（照抄文件里已有的 DROP TABLE 迁移写法，例如 `012_drop_service_catalog_item`），新增 `013_drop_legacy_approval_workflow`，内容为 `DROP TABLE IF EXISTS approval_records; DROP TABLE IF EXISTS approval_workflows;`（先删有外键指向 `approval_workflows` 的 `approval_records`，再删 `approval_workflows`）。

- [ ] **Step 8: 编译 + 全量测试**

```bash
cd itsm-backend
go build ./...
go test ./... 2>&1 | tail -50
```

Expected: 编译通过；测试全绿（或只有跟本次改动无关、已知的预置失败——如果发现跟审批相关的测试挂了，必须先修，不能跳过）。

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(approval): 下线 legacy ApprovalWorkflow/ApprovalRecord 引擎

存量自定义工作流已通过 cmd/migrate_legacy_approvals 全部迁移到 BPMN 并
验证过（迁移记录见本次实施过程）。删除 controller/approval_controller.go、
service/approval_service.go、legacy_approval_migration_service.go、
迁移 CLI 本身、对应路由与 Ent schema。ApprovalChain（/admin/approval-chains）
是独立机制不受影响。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 让 UserTask 完成时也能触发 `service_task_type`/`action` 回调

**背景（本次实施计划新发现，08-08/08-10 均未提及）**：`change_normal_flow.bpmn`/`change_emergency_flow.bpmn` 的每个节点都带了 `service_task_type="change_task"`/`action="approve_change"` 等 metadata，`service/bpmn/change_handler.go` 的 `ChangeServiceTaskHandler` 也已经完整实现并注册——但 `callbackRegistry` 只在 **ServiceTask** 分支被调用（`service/bpmn_process_engine.go:558-579`），这些节点全部是 **UserTask**，完成时从不触发这个 handler。也就是说即使 Task 8/9 把 `handlers/change` 接上 BPMN，`Change.Status` 也不会随流程推进自动更新，除非先修这个分发缺口。这是复用现有机制（不新发明回调机制），只是把已有的 ServiceTask 分发逻辑也应用到带这个 metadata 的 UserTask 完成路径上。

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`（`CompleteTask` 方法，具体位置在 `recordApprovalDecision` 调用之后、任务状态更新为 `completed` 之后）
- Test: `itsm-backend/service/bpmn_usertask_callback_test.go`（新建）

**Interfaces:**
- Consumes: `CallbackRegistry.GetHandler(taskType string) ServiceTaskHandlerInterface`（已有方法，`service/bpmn/bpmn_callback_registry.go`）；`ServiceTaskHandlerInterface.Execute(ctx, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error)`（已有接口）
- Produces: 无新接口，`CompleteTask` 完成后行为变化——带 `service_task_type` metadata 的 UserTask 现在会真的触发对应 handler。

- [ ] **Step 1: 写失败测试**

创建 `itsm-backend/service/bpmn_usertask_callback_test.go`：

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestUserTaskWithServiceTaskTypeMetadataTriggersCallback 是回归测试：
// change_normal_flow.bpmn 的 Activity_CABApproval 是 UserTask，但带了
// service_task_type=change_task/action=approve_change 的 metadata，
// 完成这个任务时必须真的调用 ChangeServiceTaskHandler.approveChange，
// 把 Change.Status 更新成 pending_approval——这之前从未发生过，因为
// CompleteTask 只在 ServiceTask 分支查 callback registry。
func TestUserTaskWithServiceTaskTypeMetadataTriggersCallback(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger)
	ctx := context.Background()
	tenantID := 1

	deploymentSvc := NewBPMNTemplateService(client, logger)
	_, err := deploymentSvc.LoadAndDeployTemplates(ctx, tenantID)
	require.NoError(t, err)

	ch := client.Change.Create().
		SetTitle("测试变更").
		SetStatus("draft").
		SetTenantID(tenantID).
		SaveX(ctx)

	instance, err := engine.StartProcess(ctx, "change_normal_flow", "test-callback", map[string]interface{}{
		"approval_required": true,
		"change_id":         ch.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "Activity_Assessment", instance.CurrentActivityID)

	task := client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_Assessment")).
		OnlyX(ctx)

	err = engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{"change_id": ch.ID}, tenantID)
	require.NoError(t, err)

	// action=update_change 的 handler 不改状态字段（除非变量里传了 status），
	// 但至少要证明 handler 被真的调用到——用一个必然改变状态的节点更直接验证：
	// 继续走到 Activity_CABApproval 并完成它，断言 Change.Status 变成 pending_approval。
	cabTask := client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_CABApproval")).
		OnlyX(ctx)
	err = engine.CompleteTask(ctx, cabTask.TaskID, map[string]interface{}{
		"change_id":      ch.ID,
		"approvalAction": "approve",
	}, tenantID)
	require.NoError(t, err)

	updated := client.Change.Query().Where(change.ID(ch.ID)).OnlyX(ctx)
	require.Equal(t, "pending_approval", updated.Status,
		"完成 Activity_CABApproval 必须触发 ChangeServiceTaskHandler.approveChange，把 Change.Status 更新成 pending_approval")
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd itsm-backend && go test ./service/... -run TestUserTaskWithServiceTaskTypeMetadataTriggersCallback -v
```

Expected: FAIL——`updated.Status` 仍是 `draft`（因为 callback 从未被调用）。

- [ ] **Step 3: 实现——在 `CompleteTask` 里补上 UserTask 的 callback 分发**

在 `itsm-backend/service/bpmn_process_engine.go` 的 `CompleteTask` 方法里，找到任务状态更新为 `completed` 之后、返回之前的位置，追加：

```go
	// UserTask 如果声明了 service_task_type/action metadata（比如变更流程的
	// Activity_CABApproval、Activity_Schedule 等节点），完成时也要走跟 ServiceTask
	// 一样的 callback registry 分发——这些节点在 BPMN 图上是 UserTask（需要人工
	// 操作触发完成），但完成后的业务副作用（更新 Change.Status 等）复用同一套
	// ChangeServiceTaskHandler/TicketServiceTaskHandler 实现，不新写一套。
	if e.callbackRegistry != nil {
		if serviceTaskType, ok := task.TaskVariables["service_task_type"].(string); ok && serviceTaskType != "" {
			handler := e.callbackRegistry.GetHandler(serviceTaskType)
			if handler != nil {
				callbackVars := make(map[string]interface{}, len(variables)+1)
				for k, v := range variables {
					callbackVars[k] = v
				}
				if action, ok := task.TaskVariables["action"].(string); ok {
					callbackVars["action"] = action
				}
				if _, err := handler.Execute(ctx, task, callbackVars); err != nil {
					e.logger.Warnw("UserTask 完成后回调执行失败", "taskID", task.TaskID, "serviceTaskType", serviceTaskType, "error", err)
				}
			}
		}
	}
```

`task.TaskVariables["service_task_type"]`/`["action"]` 的来源：`createUserTask` 建任务时已经把 `taskConfig`（含 `taskPurpose`/`approvalMode` 等）写进了 `TaskVariables`（`bpmn_process_engine.go:806-812` 附近），需要确认 `service_task_type`/`action` 这两个 BPMN metaData 也被解析进了 `BPMNUserTask` 结构体、并同样写进 `taskConfig`——如果 `bpmn_xml_parser.go` 目前只解析进 `BPMNUserTask.Extra`/等价的 metaData map 而没有透传到 `taskConfig`，在这一步一并把这两个 key 加进 `createUserTask` 里构造 `taskConfig` 的那段代码（跟 `taskPurpose`/`approvalMode` 相邻的位置）。

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd itsm-backend && go test ./service/... -run TestUserTaskWithServiceTaskTypeMetadataTriggersCallback -v
```

Expected: PASS。

- [ ] **Step 5: 跑一遍现有 BPMN 引擎全量测试，确认没有破坏别的场景**

```bash
cd itsm-backend && go test ./service/... -run "TestBPMN|TestProcess|TestApproval" -v 2>&1 | tail -80
```

Expected: 全部 PASS（这一步改动是纯新增分支，不应该影响已有的 ServiceTask 分发路径或不带 `service_task_type` 的普通 UserTask）。

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_usertask_callback_test.go
git commit -m "$(cat <<'EOF'
fix(bpmn): UserTask 完成时也触发 service_task_type/action 回调

change_normal_flow.bpmn 等流程的节点带了 service_task_type=change_task/
action=approve_change 等 metadata，对应的 ChangeServiceTaskHandler 已经
完整实现并注册，但 CompleteTask 之前只在 ServiceTask 分支查 callback
registry——这些节点全是 UserTask，回调从未被触发，Change.Status 不会
随流程推进更新。复用同一套 callback registry，不新写分发机制。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: `SubmitChange` 改为触发 BPMN 流程

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`（`SubmitChange` 方法）
- Modify: `itsm-backend/handlers/change/service.go`（`Service`/`NewService` 结构体，注入 `ProcessTriggerServiceInterface`）
- Modify: `itsm-backend/internal/bootstrap/app.go`（`change.NewService` 调用点，传入 processTriggerSvc）
- Test: `itsm-backend/handlers/change/service_test.go`（追加用例）

**Interfaces:**
- Consumes: `service.ProcessTriggerServiceInterface.TriggerProcess(ctx, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error)`（已有接口，`dto.ProcessTriggerRequest` 字段：`BusinessType dto.BusinessType`、`BusinessID int`、`ProcessDefinitionKey string`、`Variables map[string]interface{}`、`TriggeredBy string`、`TriggeredAt time.Time`、`TenantID int`）
- Produces: `SubmitChange` 成功后 `Change.Status` 变为 `"pending"`（不变，跟改动前行为一致，只是不再经过 `SubmitForApproval`/`ApprovalRecord`）。

- [ ] **Step 1: 写失败测试**

在 `itsm-backend/handlers/change/service_test.go` 追加：

```go
func TestSubmitChangeTriggersBPMNInsteadOfApprovalRecord(t *testing.T) {
	repo, entClient, logger := setupChangeServiceTest(t) // 复用文件里已有的 setup helper
	mockTrigger := &mockProcessTriggerService{}
	svc := NewService(repo, entClient, logger)
	svc.SetProcessTriggerService(mockTrigger) // Step 2 里会加这个 setter

	ch, err := svc.CreateChange(context.Background(), &Change{Title: "测试变更", Type: "normal", TenantID: 1, CreatedBy: 1})
	require.NoError(t, err)

	_, err = svc.SubmitChange(context.Background(), ch.ID, 1, 1, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	require.True(t, mockTrigger.called, "SubmitChange 必须调用 ProcessTriggerService.TriggerProcess")
	require.Equal(t, "change_normal_flow", mockTrigger.lastReq.ProcessDefinitionKey)
	require.Equal(t, ch.ID, mockTrigger.lastReq.BusinessID)

	updated, err := svc.GetChange(context.Background(), ch.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "pending", updated.Status)

	// 不应该再走 repo.SubmitForApproval 创建 ApprovalRecord
	history, _ := repo.GetApprovalHistory(context.Background(), ch.ID, 1)
	require.Empty(t, history, "迁移到 BPMN 后不应该再创建 change 自己的 ApprovalRecord")
}

// mockProcessTriggerService 是一个测试替身，记录最后一次调用的请求。
type mockProcessTriggerService struct {
	called  bool
	lastReq *dto.ProcessTriggerRequest
}

func (m *mockProcessTriggerService) TriggerProcess(ctx context.Context, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error) {
	m.called = true
	m.lastReq = req
	return &dto.ProcessTriggerResponse{ProcessInstanceID: "test-instance-1", BusinessKey: fmt.Sprintf("change:%d", req.BusinessID)}, nil
}
```

- [ ] **Step 2: 运行测试，确认编译失败（`SetProcessTriggerService` 还不存在）**

```bash
cd itsm-backend && go test ./handlers/change/... -run TestSubmitChangeTriggersBPMNInsteadOfApprovalRecord -v
```

Expected: 编译错误，`svc.SetProcessTriggerService` undefined。

- [ ] **Step 3: 实现——给 `Service` 加 `ProcessTriggerService` 依赖**

在 `itsm-backend/handlers/change/service.go`：

```go
type Service struct {
	repo              Repository
	logger            *zap.SugaredLogger
	entClient         *ent.Client
	pirService        *service.ChangePIRService
	processTriggerSvc service.ProcessTriggerServiceInterface
}

// SetProcessTriggerService 注入流程触发服务（运行时依赖注入，跟 TicketService 的
// SetApprovalService 是同一种接线方式）。
func (s *Service) SetProcessTriggerService(t service.ProcessTriggerServiceInterface) {
	s.processTriggerSvc = t
}
```

删除构造函数里的 `svc.approvalBridge = service.NewBPMNApprovalBridge(entClient, logger)`（Task 10 会正式删掉 `BPMNApprovalBridge` 本体，这里先停止使用它）。

- [ ] **Step 4: 实现——`SubmitChange` 触发 BPMN 而不是创建 `ApprovalRecord`**

把 `SubmitChange` 里的这段：

```go
	if err := s.repo.SubmitForApproval(ctx, changeID, tenantID, req.ApproverIDs, req.Comment); err != nil {
		s.logger.Warnw("Failed to atomically submit change", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("提交变更审批失败: %w", err)
	}
```

改成：

```go
	// H-2/C-2 之后的收敛：变更提交审批不再自己维护 ApprovalRecord/ApprovalChain，
	// 触发 BPMN 流程（change_normal_flow / change_emergency_flow），审批推进和
	// Change.Status 更新都由 BPMN 侧的 ChangeServiceTaskHandler 负责（见 Task 7）。
	processKey := "change_normal_flow"
	if c.Type == "emergency" {
		processKey = "change_emergency_flow"
	}
	if s.processTriggerSvc != nil {
		triggerReq := &dto.ProcessTriggerRequest{
			BusinessType:         dto.BusinessTypeChange,
			BusinessID:           changeID,
			ProcessDefinitionKey: processKey,
			Variables: map[string]interface{}{
				"change_id":         changeID,
				"approval_required": true,
				"requester_id":      c.CreatedBy,
			},
			TriggeredBy: fmt.Sprintf("%d", submitterID),
			TriggeredAt: time.Now(),
			TenantID:    tenantID,
		}
		if _, err := s.processTriggerSvc.TriggerProcess(ctx, triggerReq); err != nil {
			return nil, fmt.Errorf("触发变更审批流程失败: %w", err)
		}
	}

	c.Status = "pending"
	if _, err := s.repo.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("更新变更状态失败: %w", err)
	}
```

（`req.ApproverIDs`/`req.Comment` 字段这次不再使用——BPMN 侧的 `assigneeRole="change_manager"` 已经决定了审批人，前端"指定审批人"这个入参在 Task 13 核实前端时一并核实是否还需要保留在请求体里。）

- [ ] **Step 5: 在 bootstrap 里接线**

`itsm-backend/internal/bootstrap/app.go` 里找到 `change.NewService(...)` 的调用点，紧跟着加一行：

```go
changeService.SetProcessTriggerService(processTriggerSvc)
```

（`processTriggerSvc` 变量名以该文件里 `ticketService.SetProcessTriggerService`/等价注入调用旁边实际使用的变量名为准。）

- [ ] **Step 6: 运行测试，确认通过**

```bash
cd itsm-backend && go test ./handlers/change/... -run TestSubmitChangeTriggersBPMNInsteadOfApprovalRecord -v
```

Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/service_test.go itsm-backend/internal/bootstrap/app.go
git commit -m "$(cat <<'EOF'
feat(change): SubmitChange 触发 BPMN 流程，不再自建 ApprovalRecord

复用 service_request 已验证的迁移模式：SubmitChange 改成调用
ProcessTriggerService.TriggerProcess（change_normal_flow /
change_emergency_flow），不再调用 repo.SubmitForApproval 创建 change
自己的 ApprovalRecord。审批推进和状态更新交给 BPMN + Task7 补的
UserTask 回调机制。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: `TransitionStatus`(approve/reject) 改为操作 BPMN 任务，删除 `checkAndTransitionChange`

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`（`ProcessApproval`/`checkAndTransitionChange` 删除，approve/reject 路径改走 BPMN）
- Modify: `itsm-backend/handlers/change/handler.go`（`TransitionStatus` 里 approve/reject 分支调用改掉）
- Test: `itsm-backend/handlers/change/service_test.go`（追加用例）

**Interfaces:**
- Consumes: `CustomProcessEngine.CompleteTask(ctx, taskID string, variables map[string]interface{}, tenantID int) error`（已有方法）；`ProcessTask` 没有直接的 `business_id` 字段，需要先按 `ProcessInstance.BusinessKey`（`"change:<id>"` 格式，Task8 触发时约定）查到流程实例，再按 `process_instance_id` 查当前待办的 `ProcessTask`。
- Produces: `func (s *Service) ProcessApproval(ctx context.Context, changeID int, status string, comment *string, tenantID int) error`（**签名变化**：不再是旧版的 `(recordID int, status string, comment *string, tenantID int) (*ApprovalRecord, error)`，改成 `changeID` 而非 `recordID`，只返回 `error`，不再返回 `*ApprovalRecord`——因为不再有 change 自己的 ApprovalRecord 可返回）。`TransitionStatus` 对外 HTTP 行为不变（approve/reject 请求语义不变），只是内部调用点要跟着改成新签名（Step 4）。

- [ ] **Step 1: 写失败测试**

```go
func TestApproveChangeCompletesViaBPMNTask(t *testing.T) {
	repo, entClient, logger := setupChangeServiceTest(t)
	mockTrigger := &mockProcessTriggerService{}
	svc := NewService(repo, entClient, logger)
	svc.SetProcessTriggerService(mockTrigger)

	ch, _ := svc.CreateChange(context.Background(), &Change{Title: "测试变更", Type: "normal", TenantID: 1, CreatedBy: 1})
	_, err := svc.SubmitChange(context.Background(), ch.ID, 1, 1, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	// mockTrigger 不会真的建 BPMN 任务，这条用例只验证 ProcessApproval 不再
	// 调用 repo.UpdateApprovalRecord/CreateApprovalRecord —— 真正的"完成 BPMN
	// 任务"路径由 Task 3/4/7 已经覆盖的引擎级测试保证。
	err = svc.ProcessApproval(context.Background(), ch.ID, "approved", nil, 1)
	require.NoError(t, err)

	history, _ := repo.GetApprovalHistory(context.Background(), ch.ID, 1)
	require.Empty(t, history, "迁移后 ProcessApproval 不应该再写 change 自己的 ApprovalRecord")
}
```

- [ ] **Step 2: 运行测试确认失败**（当前实现仍会调用 `repo.UpdateApprovalRecord`）

```bash
cd itsm-backend && go test ./handlers/change/... -run TestApproveChangeCompletesViaBPMNTask -v
```

- [ ] **Step 3: 实现——`ProcessApproval` 改为完成 BPMN 任务**

把 `handlers/change/service.go` 的 `ProcessApproval` 方法体替换成：

```go
// ProcessApproval 处理审批决策——完成 change 关联的 BPMN 审批任务，
// Change.Status 的更新交给 Task7 补的 UserTask 回调机制（ChangeServiceTaskHandler），
// 这里不再直接改 Change.Status，也不再维护 change 自己的 ApprovalRecord。
func (s *Service) ProcessApproval(ctx context.Context, changeID int, status string, comment *string, tenantID int) error {
	if s.processEngine == nil {
		return fmt.Errorf("process engine not configured")
	}

	// ProcessTask 没有直接的 business_id 字段，跟 triggerWorkflowForTicket/
	// GetWorkflowStatus 已有的关联方式一致：先按 ProcessInstance.BusinessKey
	// （触发时约定的 "change:<id>" 格式，见 Task8 TriggerProcess 那次调用）
	// 找到流程实例，再按 process_instance_id 查它当前的 CAB 审批任务。
	businessKey := fmt.Sprintf("change:%d", changeID)
	instance, err := s.entClient.ProcessInstance.Query().
		Where(
			processinstance.TenantID(tenantID),
			processinstance.BusinessKey(businessKey),
			processinstance.StatusEQ("running"),
		).
		First(ctx)
	if err != nil {
		return fmt.Errorf("未找到该变更关联的运行中流程实例: %w", err)
	}

	task, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.TenantID(tenantID),
			processtask.ProcessInstanceID(instance.ID),
			processtask.TaskDefinitionKey("Activity_CABApproval"),
			processtask.StatusIn("created", "assigned"),
		).
		First(ctx)
	if err != nil {
		return fmt.Errorf("未找到该变更待处理的 CAB 审批任务: %w", err)
	}

	commentText := ""
	if comment != nil {
		commentText = *comment
	}
	action := "reject"
	if status == "approved" {
		action = "approve"
	}

	return s.processEngine.CompleteTask(ctx, task.TaskID, map[string]interface{}{
		"change_id":      changeID,
		"approvalAction": action,
		"approvalComment": commentText,
	}, tenantID)
}
```

（按 `change_id` 精确过滤的具体写法要看 `ProcessTask`/`ProcessInstance` 的关联方式——如果 `ProcessTask` 没有直接挂 `change_id` 字段，改成先按 `ProcessInstance.BusinessKey == fmt.Sprintf("change:%d", changeID)` 查到 `process_instance_id`，再按这个 ID 查 `ProcessTask`，这是 `bpmn_process_engine.go` 里其它地方已经在用的关联方式，不新发明。）

给 `Service` 加 `processEngine service.ProcessEngineInterface` 字段和对应的 `SetProcessEngine` setter，在 bootstrap 里注入（跟 Step 5 of Task 8 同样的接线方式）。

删除整个 `checkAndTransitionChange` 方法——它的职责（"所有必需审批人都通过才转 approved"）现在由 BPMN 流程本身的顺序结构保证（`Activity_CABApproval` 完成 = 审批通过，直接往下走）。

- [ ] **Step 4: 在 `handlers/change/handler.go` 里更新 `TransitionStatus` 对 approve/reject 的分发**

找到 `TransitionStatus` 里调用 `s.svc.ProcessApproval(...)` 的地方，改成传新签名（`changeID, status, comment, tenantID`，不再传 `recordID`）。

- [ ] **Step 5: 运行测试确认通过**

```bash
cd itsm-backend && go test ./handlers/change/... -v 2>&1 | tail -60
```

Expected: 全部 PASS。

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/handler.go itsm-backend/handlers/change/service_test.go
git commit -m "$(cat <<'EOF'
refactor(change): 审批通过/驳回改为完成 BPMN 任务，删除 checkAndTransitionChange

ProcessApproval 不再维护 change 自己的 ApprovalRecord，改成查到关联的
CAB 审批 BPMN 任务并调用 CompleteTask。checkAndTransitionChange 的"所有
必需审批人通过才转 approved"职责现在由 BPMN 流程结构本身保证，整个方法
删除。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: 删除 `BPMNApprovalBridge` / P0-1 补丁

**背景**：Task 8/9 完成后，change 的创建（`SubmitChange`）和决策（`ProcessApproval`）都已经原生在 BPMN 上，不再需要"事后追认"的桥接补丁。

**Files:**
- Delete: `itsm-backend/service/bpmn_approval_bridge_service.go`
- Delete: `itsm-backend/service/bpmn_approval_bridge_service_test.go`
- Modify: `itsm-backend/handlers/change/service.go`（确认 `approvalBridge` 字段和相关引用已在 Task 8 Step 3 清理，这里做最终确认，删掉遗留的 import）

**Interfaces:**
- 无——纯删除，`BPMNApprovalBridge` 在 Task 8 之后已经没有调用方。

- [ ] **Step 1: 确认零调用**

```bash
cd itsm-backend
grep -rn "BPMNApprovalBridge\|NewBPMNApprovalBridge\|approvalBridge" --include="*.go" . | grep -v "_test.go"
```

Expected: 无输出（Task 8 已经删掉了 `handlers/change/service.go` 里的引用）。

- [ ] **Step 2: 删除文件**

```bash
rm itsm-backend/service/bpmn_approval_bridge_service.go
rm -f itsm-backend/service/bpmn_approval_bridge_service_test.go
```

- [ ] **Step 3: 编译验证**

```bash
cd itsm-backend && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add -A itsm-backend/service/bpmn_approval_bridge_service.go itsm-backend/service/bpmn_approval_bridge_service_test.go
git commit -m "$(cat <<'EOF'
refactor(change): 删除 BPMNApprovalBridge（P0-1 补丁）

变更创建（SubmitChange）和审批决策（ProcessApproval）现在原生走 BPMN，
不再需要事后追认 BPMN 任务的桥接补丁。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: `GetApprovals`/`GetApprovalSummary` 改读 BPMN

**Files:**
- Modify: `itsm-backend/handlers/change/service.go`（`GetApprovalSummary`；`GetApprovalHistory` 改成从 `ProcessApprovalDecision` 表查）
- Modify: `itsm-backend/handlers/change/handler.go`（`GetApprovals`/`GetApprovalSummary` 响应结构如有变化同步更新 DTO）
- Test: `itsm-backend/handlers/change/service_test.go`（追加用例）

**Interfaces:**
- Consumes: `ent.Client.ProcessApprovalDecision.Query()`（已有 Ent schema，字段：`business_type`/`business_id`/`actor_id`/`action`/`decision`/`comment`/`created_at`，见 `ent/schema/process_approval_decision.go`）
- Produces: `GetApprovalHistory(ctx, changeID, tenantID) ([]*ApprovalRecord, error)` 签名不变（`handlers/change` 包内的 `ApprovalRecord` 类型保留到 Task 12，只改数据来源）。

- [ ] **Step 1: 写失败测试**

```go
func TestGetApprovalHistoryReadsFromProcessApprovalDecision(t *testing.T) {
	repo, entClient, logger := setupChangeServiceTest(t)
	svc := NewService(repo, entClient, logger)

	entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).
		SetProcessTaskID(1).
		SetProcessInstanceKey("test-instance").
		SetTaskID("task-1").
		SetProcessDefinitionKey("change_normal_flow").
		SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").
		SetBusinessID("42").
		SetActorID(7).
		SetAction("approve").
		SetDecision("approved").
		SetTenantID(1).
		SaveX(context.Background())

	history, err := svc.GetApprovalHistory(context.Background(), 42, 1)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, 7, history[0].ApproverID)
	require.Equal(t, "approved", history[0].Status)
}
```

- [ ] **Step 2: 运行测试确认失败**（当前实现读的是 `repo.GetApprovalHistory`，即 change 自己的表，查不到刚插入的 `ProcessApprovalDecision` 记录）

- [ ] **Step 3: 实现**

```go
func (s *Service) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	decisions, err := s.entClient.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.TenantID(tenantID),
			processapprovaldecision.BusinessType("change"),
			processapprovaldecision.BusinessID(fmt.Sprintf("%d", changeID)),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询审批历史失败: %w", err)
	}

	records := make([]*ApprovalRecord, 0, len(decisions))
	for _, d := range decisions {
		status := d.Decision // "approved" / "rejected"
		var comment *string
		if d.Comment != "" {
			c := d.Comment
			comment = &c
		}
		records = append(records, &ApprovalRecord{
			ChangeID:   changeID,
			ApproverID: d.ActorID,
			Status:     status,
			Comment:    comment,
			TenantID:   tenantID,
		})
	}
	return records, nil
}
```

`GetApprovalSummary` 同样改成基于这份 `decisions` 列表统计（通过数/驳回数/待处理数），不再读 change 自己的 `ApprovalChain`/`ApprovalRecord` 表。

- [ ] **Step 4: 运行测试确认通过**

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/handlers/change/service.go itsm-backend/handlers/change/handler.go itsm-backend/handlers/change/service_test.go
git commit -m "$(cat <<'EOF'
refactor(change): GetApprovalHistory/GetApprovalSummary 改读 ProcessApprovalDecision

审批历史现在从 BPMN 原生的 ProcessApprovalDecision 表查询，不再依赖
change 自己维护的 ApprovalRecord 表（Task12 会删除这张表本身）。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: 删除 `handlers/change` 包内自己的 `ApprovalRecord`/`ApprovalChain` 类型

**背景**：Task 8/9/11 完成后，`handlers/change` 包内的 `ApprovalRecord`/`ApprovalChain` 类型（以及对应的 repo 方法：`CreateApprovalRecord`/`UpdateApprovalRecord`/`SubmitForApproval`/`GetApprovalChain`/`ConfigureWorkflow`）不再被业务逻辑使用，只剩类型定义和 repository 实现本身。删除后一并检查 `service/change_service.go` 里保留的 `CloseChangeApprovalChains` 是否还有调用方（`ProcessApproval` 旧实现里的调用点已经在 Task 9 被替换掉）。

**Files:**
- Modify: `itsm-backend/handlers/change/entity.go`（删除 `ApprovalRecord`/`ApprovalChain` 类型定义）
- Modify: `itsm-backend/handlers/change/repository.go`（删除接口方法声明）
- Modify: `itsm-backend/handlers/change/repository_impl.go`（删除实现）
- Modify: `itsm-backend/service/change_service.go`（如 `CloseChangeApprovalChains` 已无调用方，一并删除）
- Modify: `itsm-backend/migration/migrations.go`（新增 DROP TABLE 迁移，删除 `change_approvals`/`change_approval_chains` 两张裸 SQL 表）

**Interfaces:**
- 无对外接口——`ConfigureWorkflow` 如果还被路由暴露（`router.go` 里如果有对应端点），需要在 Task 13 核实前端是否还用得到，用不到就一并删路由。

- [ ] **Step 1: 确认这些类型/方法真的没有调用方了**

```bash
cd itsm-backend
grep -rn "\.CreateApprovalRecord\|\.UpdateApprovalRecord\|\.SubmitForApproval\|\.GetApprovalChain\b" handlers/change/*.go | grep -v "_test.go\|repository.go:\|repository_impl.go:"
grep -rn "service\.CloseChangeApprovalChains" --include="*.go" .
```

Expected: 第一条 grep 无业务代码命中（只剩接口/实现声明本身，以及 `_test.go` 里的测试，测试在下一步一并删）；第二条 grep 无输出（说明 Task 9 之后 `CloseChangeApprovalChains` 也没人调用了）。

- [ ] **Step 2: 删除类型定义、接口方法、实现**

从 `handlers/change/entity.go` 删除 `ApprovalRecord`/`ApprovalChain` struct；从 `handlers/change/repository.go` 删除对应接口方法签名；从 `handlers/change/repository_impl.go` 删除对应实现函数。`ConfigureWorkflow` 方法（`handlers/change/service.go`）如果只是用来配置这两个类型，一并删除。

如果 `CloseChangeApprovalChains` 确认无调用方，从 `service/change_service.go` 删除这个函数。

- [ ] **Step 3: 删除底层裸 SQL 表**

`handlers/change` 的 `ApprovalRecord`/`ApprovalChain` 类型不是 Ent 支撑的——`repository_impl.go` 的 `CreateApprovalRecord`/`GetApprovalChain` 直接手写 SQL 操作 `change_approvals`/`change_approval_chains` 两张表（建表迁移 `006_add_change_approvals`，`migration/migrations.go:33-180` 附近）。这两张表跟 Track5（`change_approval_service.go`，已在 Task2 删除代码）用的是同一批物理表，现在没有任何代码再读写它们了。

在 `itsm-backend/migration/migrations.go` 追加一条新迁移（照抄文件里已有的 DROP TABLE 迁移写法，例如 `012_drop_service_catalog_item`），新增 `014_drop_change_approvals`：

```sql
DROP TABLE IF EXISTS change_approval_chains;
DROP TABLE IF EXISTS change_approvals;
```

（先删 `change_approval_chains` 再删 `change_approvals`，避免外键顺序问题——具体外键方向以 `006_add_change_approvals` 迁移里的实际 `REFERENCES` 声明为准。）

- [ ] **Step 4: 编译 + 测试**

```bash
cd itsm-backend
go build ./...
go test ./handlers/change/... ./service/... -v 2>&1 | tail -80
```

- [ ] **Step 5: Commit**

```bash
git add -A itsm-backend/handlers/change/ itsm-backend/service/change_service.go
git commit -m "$(cat <<'EOF'
refactor(change): 删除包内自建的 ApprovalRecord/ApprovalChain 类型

Task8/9/11 完成后，这两个类型和对应的 repo 方法
（CreateApprovalRecord/UpdateApprovalRecord/SubmitForApproval/
GetApprovalChain/ConfigureWorkflow）不再被业务逻辑使用。审批状态和
历史现在完全交给 BPMN 原生的 ProcessInstance/ProcessTask/
ProcessApprovalDecision。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: 核实前端审批菜单现状，收敛或清理

**背景**：08-10 文档声称已经"隐藏旧「审批管理」菜单"、"新增「待审批」菜单聚合 BPMN 待审批任务"，但直接查文件发现 `admin/approvals/page.tsx` 和 `my-approvals/page.tsx` 两个页面文件都还在。本任务先核实真实现状（菜单是否可达），再决定删除还是保留。

**Files:**
- Research: `itsm-frontend/src/components/layout/`（导航菜单配置）
- Research: `itsm-frontend/src/app/(main)/admin/approvals/page.tsx`
- 视核实结果 Modify/Delete 相应文件

**Interfaces:**
- 无预先约定——本任务第一步是纯调研，产出决定后续要不要动代码。

- [ ] **Step 1: 核实 `admin/approvals` 是否在导航菜单里可达**

```bash
cd itsm-frontend
grep -rn "admin/approvals\|审批管理" src/components/layout/ src/lib/
```

- [ ] **Step 2: 根据核实结果分两种情况处理**

**情况 A——菜单已经不可达，只是页面文件没删**：

```bash
rm -rf src/app/\(main\)/admin/approvals
```

检查 `admin/approvals` 页面用到的 API 方法（`ticket-approval-api.ts` 里对应 `/approval-workflows` 的部分）是否也已经在 Task 6 的后端改动后失效，一并删除前端这部分调用代码。

**情况 B——菜单仍然可达**：不属于这次计划的既定范围（这是"是否需要新做一次菜单收敛"的产品决定，不是"补完已经决定的动作"），记录下来向人类确认是否要扩展本次范围，不要在没有确认的情况下改动导航结构。

- [ ] **Step 3: 如果走了情况 A，跑一遍前端类型检查**

```bash
cd itsm-frontend && npm run type-check
```

Expected: 通过，无 `admin/approvals` 相关的悬空引用。

- [ ] **Step 4: Commit（仅情况 A 需要）**

```bash
git add -A itsm-frontend/src/app/\(main\)/admin/approvals
git commit -m "$(cat <<'EOF'
chore(frontend): 删除已不在导航菜单里的旧审批管理页面

08-10 的菜单重构已经隐藏了「审批管理」入口，但页面文件本身没有跟着
删除。对应的后端 ApprovalWorkflow 引擎也已在 Task6 下线。

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: 更新 `change-api.ts` 匹配新的审批响应形状

**背景**：Task 11 之后，`/changes/:id/approvals`/`/changes/:id/approval-summary` 的数据来源从 change 自己的表变成 `ProcessApprovalDecision`，如果响应 DTO 字段发生变化，前端调用点需要同步。

**Files:**
- Modify: `itsm-frontend/src/lib/api/change-api.ts`
- Modify: `itsm-frontend/src/types/change.ts`（如有对应类型定义）
- Test: 相关 Jest 测试（如已有覆盖这几个 API 方法）

**Interfaces:**
- Consumes: 后端 Task 11 之后的 `GetApprovalHistory`/`GetApprovalSummary` 响应（camelCase DTO，字段跟改动前保持一致——`approverId`/`status`/`comment`，这次改动只换了数据来源，不改字段名，前端理论上不需要改动）

- [ ] **Step 1: 对比改动前后的 DTO 字段**

```bash
cd itsm-backend
grep -n "ApproverID\|Status\|Comment" handlers/change/entity.go | head -10
```

确认 Task 11 里 `ApprovalRecord` struct 本身字段没变（`ChangeID`/`ApproverID`/`Status`/`Comment`/`TenantID`），只是数据来源变了。DTO mapper（`dto.ToApprovalRecordResponse` 或等价函数，如果存在）字段名也应该没变。

- [ ] **Step 2: 如果字段确实没变，本任务只需要跑一遍前端集成测试确认响应兼容**

```bash
cd itsm-frontend
npm run test:integration -- change-api
```

Expected: 通过。如果发现字段确实有变化（比如驳回意见的字段名从 `comment` 变成了别的），在 `change-api.ts` 和对应 TypeScript 类型里同步改掉，遵循项目已有的"后端 DTO 是契约，不在前端打补丁绕过"的约定。

- [ ] **Step 3: 如无需改动，记录确认结果；如有改动，Commit**

```bash
git add itsm-frontend/src/lib/api/change-api.ts itsm-frontend/src/types/change.ts
git commit -m "$(cat <<'EOF'
fix(frontend): 同步变更审批响应类型（如 Task11 后端字段有变化）

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## 最终验证

全部任务完成后：

```bash
cd itsm-backend && go build ./... && go test ./... 2>&1 | tail -80
cd itsm-frontend && npm run type-check && npm run lint:check
```

用真实 HTTP 客户端走一遍完整链路（不是内部函数调用——这个项目自己的教训是低保真度验证会漏掉正是藏在"应用层设的变量名"和"XML/前端层读的变量名"这类接缝里的 bug）：

1. 创建一个 `priority=high` 的服务请求，确认路由到 `service_request_urgent_flow` 且真的停在审批节点（验证 Task 3）。
2. 直接调用 `handlers/change` 的 `/changes` + `/changes/:id/submit` 创建并提交一个普通变更，确认触发 `change_normal_flow`、CAB 审批候选人是 `change_manager` 角色用户（验证 Task 4/8）、完成 CAB 审批后 `Change.Status` 正确流转（验证 Task 7/9）、`/changes/:id/approvals` 能查到审批历史（验证 Task 11）。
3. 确认 `/approval-workflows`、`/tickets/approval/submit` 等已删除端点返回 404（不是被中间件拦截的权限错误），确认没有遗留路由（验证 Task 6）。
