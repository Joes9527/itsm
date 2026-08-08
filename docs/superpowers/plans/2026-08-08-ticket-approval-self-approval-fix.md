# 修复工单默认审批流程"自己批自己" + 补齐缺失的 ticket_urgent_flow - 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 关闭 `ticket_general_flow.bpmn` 审批节点导致的"申请人可以批准自己工单"安全漏洞，同时补齐缺失的 `ticket_urgent_flow.bpmn` 让高/紧急优先级工单不再创建失败。

**Architecture:** `service/bpmn_process_engine.go` 的 `createUserTask` 新增一个 `task.TaskPurpose == "approval"` 专属分支：优先复用 `service/approver.DeptManagerResolver` 解析申请人部门负责人（含祖先部门递归）作为 assignee；解析失败或负责人就是申请人自己时，转候选组兜底（复用已有 `bpmn.GroupResolver`），并在两条路径的候选人结果里都排除申请人自己。`ticket_general_flow.bpmn` 和新建的 `ticket_urgent_flow.bpmn`（内容等价副本）的 `Activity_Approval` 节点打上 `taskPurpose="approval"` 标记以激活这条分支；`bpmn_template_service.go` 补一个部署清单 case。

**Tech Stack:** Go 1.x / Ent ORM / `stretchr/testify` / sqlite3（enttest）/ 自定义 BPMN 引擎（`service/bpmn_process_engine.go`，非 `nitram509/lib-bpmn-engine`）。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-08-08-ticket-approval-self-approval-fix-design.md`（已经用户复审通过）。
- 复用 `service/approver.DeptManagerResolver`（`itsm-backend/service/approver/dept_manager_resolver.go`），不重新实现部门→负责人解析；不引入第二套审批引擎。
- 兜底候选组名固定为 Go 常量 `"ticket-approvers"`（`approvalFallbackCandidateGroup`），只在 BPMN 没有显式声明 `candidateGroups` 时使用；BPMN 显式声明的 `candidateGroups`（比如 legacy 审批链迁移出来的按角色/组路由节点）始终优先，不被部门负责人解析覆盖。
- taskPurpose="approval" 的任务里，流程变量 `requester_id`/`triggered_by`/`assignee_id` 和 `getDefaultAssigntee` 数据库规则兜底完全不参与自动分配（这条链只保留给非 approval 任务）；BPMN 显式 `assignee` 属性（优先级最高）不受影响。
- 候选人排除的匹配语义：与 `authorizeTaskActor.allowed`（`bpmn_process_engine.go:417`）一致——候选人字符串等于申请人 ID 的十进制字符串，或等于申请人 `Username`；额外补一次 `Email` 匹配，覆盖 `GroupResolver.ExpandGroupsToUsers` 在用户名为空时退化用 Email 做候选人显示名的情况。
- 本次只改 `ticket_general_flow.bpmn` 一个文件的 `Activity_Approval` 节点（加上新建的 `ticket_urgent_flow.bpmn` 副本）；其余 12 个 BPMN 文件、20 个同类审批节点明确不在本次范围内。
- `ticket_urgent_flow.bpmn` 除 `process id`/`name`/`metaData description`/`BPMNPlane bpmnElement` 外，与 `ticket_general_flow.bpmn` 内容完全一致，不引入任何行为差异（无独立超时/升级规则）。
- 测试用 `enttest.Open(t, "sqlite3", "file:<unique-name>?mode=memory&cache=shared&_fk=1")`，DSN 里的 `<unique-name>` 每个测试文件用不同前缀，避免跨测试文件的内存库冲突（参考 `service/bpmn_process_engine_ext_test.go`、`service/approver/approver_test.go`、`service/bpmn/bpmn_group_resolver_db_test.go` 里已有的写法）。
- 所有新增/修改的 Go 代码文件必须保持 `package service`（`bpmn_process_engine.go`、`bpmn_template_service.go` 所在包），不要拆到子包——`createUserTask` 等改动的辅助函数是这个包内部实现细节。

## File Structure

- Modify: `itsm-backend/service/bpmn/ticket_general_flow.bpmn` — `Activity_Approval` 节点加 `taskPurpose="approval"`。
- Create: `itsm-backend/service/bpmn/ticket_urgent_flow.bpmn` — `ticket_general_flow.bpmn` 的内容等价副本。
- Modify: `itsm-backend/service/bpmn_template_service.go` — `listTemplates()` 的 `switch` 里加 `"ticket_urgent_flow"` case。
- Create: `itsm-backend/service/bpmn_template_service_test.go` — 覆盖模板发现、taskPurpose 标记、部署+启动回归。
- Modify: `itsm-backend/service/bpmn_process_engine.go` — `createUserTask` 改造 + 3 个新辅助函数 + 1 个新常量 + 1 个新 import。
- Create: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go` — 覆盖设计文档"测试计划"一节列出的全部场景。

---

### Task 1: `ticket_general_flow.bpmn` 打标记 + 补齐 `ticket_urgent_flow.bpmn`

**Files:**
- Modify: `itsm-backend/service/bpmn/ticket_general_flow.bpmn:91`
- Create: `itsm-backend/service/bpmn/ticket_urgent_flow.bpmn`
- Modify: `itsm-backend/service/bpmn_template_service.go:101-140`
- Test: `itsm-backend/service/bpmn_template_service_test.go`

**Interfaces:**
- Consumes: `BPMNParser.ParseXML([]byte) (*BPMNDefinitions, error)`（已存在，`service/bpmn_xml_parser.go:20`）；`BPMNDefinitions.Processes[0].UserTasks[].TaskPurpose`（已存在字段，`service/bpmn_types.go:86`）；`BPMNTemplateService.listTemplates()`/`DeployTemplateByName(ctx, name, tenantID) error`（已存在，`service/bpmn_template_service.go`）；`CustomProcessEngine.StartProcess(ctx, processDefinitionKey, businessKey, variables) (*ent.ProcessInstance, error)`（已存在，`service/bpmn_process_engine.go:202`）；包级未导出变量 `bpmnTemplates embed.FS`（`service/bpmn_template_service.go:20`，测试文件与它同包，可以直接 `bpmnTemplates.ReadFile("bpmn/xxx.bpmn")`）。
- Produces：无新增导出符号——Task 2 不依赖 Task 1 的任何 Go 符号，两个任务的执行顺序可以互换，这里按"先接口简单的"排列。

- [ ] **Step 1: 给 `ticket_general_flow.bpmn` 的 `Activity_Approval` 节点加 `taskPurpose="approval"`**

把 `itsm-backend/service/bpmn/ticket_general_flow.bpmn` 第 91 行：

```xml
    <bpmn:userTask id="Activity_Approval" name="工单审批">
```

改成：

```xml
    <bpmn:userTask id="Activity_Approval" name="工单审批" taskPurpose="approval">
```

（只加这一个属性，节点其余内容——`incoming`/`outgoing`——不变。）

- [ ] **Step 2: 新建 `ticket_urgent_flow.bpmn`**

创建 `itsm-backend/service/bpmn/ticket_urgent_flow.bpmn`，完整内容如下（是 Step 1 改完后的 `ticket_general_flow.bpmn` 的副本，只改了 `process id`/`name`、`metaData description`、`BPMNPlane bpmnElement` 这三处，其余包括 `Activity_Approval` 的 `taskPurpose="approval"` 标记都保持一致）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                 xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
                 xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
                 targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="ticket_urgent_flow" name="紧急工单流程" isExecutable="true">
    <bpmn:extensionElements>
      <bpmn:metaData name="category">ticket</bpmn:metaData>
      <bpmn:metaData name="version">1.0.0</bpmn:metaData>
      <bpmn:metaData name="description">高/紧急优先级工单处理流程（结构与通用工单流程等价，暂无独立的超时/升级差异）</bpmn:metaData>
    </bpmn:extensionElements>

    <!-- 开始事件：工单创建 -->
    <bpmn:startEvent id="StartEvent_1" name="工单创建">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>

    <!-- 任务分配 -->
    <bpmn:userTask id="Activity_Assign" name="任务分配" instantiate="false">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">assign</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
      <bpmn:outgoing>Flow_Approval</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 处理任务 -->
    <bpmn:userTask id="Activity_Handle" name="工单处理">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">update_status</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 升级判断网关 -->
    <bpmn:exclusiveGateway id="Gateway_Escalate" name="是否需要升级?">
      <bpmn:incoming>Flow_3</bpmn:incoming>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
      <bpmn:outgoing>Flow_Resolve</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 升级处理 -->
    <bpmn:userTask id="Activity_Escalate" name="升级处理">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">escalate</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 解决工单 -->
    <bpmn:userTask id="Activity_Resolve" name="解决工单">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">update_status</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_Resolve</bpmn:incoming>
      <bpmn:outgoing>Flow_5</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 通知请求人 -->
    <bpmn:serviceTask id="Activity_NotifyRequester" name="通知请求人" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">notify_requester</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_5</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:serviceTask>

    <!-- 结束事件：工单关闭 -->
    <bpmn:endEvent id="EndEvent_1" name="工单关闭">
      <bpmn:incoming>Flow_6</bpmn:incoming>
    </bpmn:endEvent>

    <!-- 审批判断 -->
    <bpmn:exclusiveGateway id="Gateway_Approval" name="是否需要审批?">
      <bpmn:incoming>Flow_Approval</bpmn:incoming>
      <bpmn:outgoing>Flow_ApprovalTask</bpmn:outgoing>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 审批任务 -->
    <bpmn:userTask id="Activity_Approval" name="工单审批" taskPurpose="approval">
      <bpmn:incoming>Flow_ApprovalTask</bpmn:incoming>
      <bpmn:outgoing>Flow_ApprovalResult</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 审批结果网关 -->
    <bpmn:exclusiveGateway id="Gateway_ApprovalResult" name="审批结果?">
      <bpmn:incoming>Flow_ApprovalResult</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
      <bpmn:outgoing>Flow_Reject</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 驳回到处理 -->
    <bpmn:sequenceFlow id="Flow_Reject" sourceRef="Gateway_ApprovalResult" targetRef="Activity_Handle" />

    <!-- 连线定义 -->
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Assign" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_Assign" targetRef="Activity_Handle" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Activity_Handle" targetRef="Gateway_Escalate" />
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Gateway_Escalate" targetRef="Activity_Escalate">
      <bpmn:conditionExpression xsi:type="bpmn:tExpression" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
        <bpmn:body>${variables['need_escalate'] == true}</bpmn:body>
      </bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_Resolve" sourceRef="Gateway_Escalate" targetRef="Activity_Resolve">
      <bpmn:conditionExpression xsi:type="bpmn:tExpression" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
        <bpmn:body>${variables['need_escalate'] != true}</bpmn:body>
      </bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Activity_Resolve" targetRef="Activity_NotifyRequester" />
    <bpmn:sequenceFlow id="Flow_6" sourceRef="Activity_NotifyRequester" targetRef="EndEvent_1" />
    <bpmn:sequenceFlow id="Flow_Approval" sourceRef="Activity_Assign" targetRef="Gateway_Approval" />
    <bpmn:sequenceFlow id="Flow_ApprovalTask" sourceRef="Gateway_Approval" targetRef="Activity_Approval" />
    <bpmn:sequenceFlow id="Flow_ApprovalResult" sourceRef="Activity_Approval" targetRef="Gateway_ApprovalResult" />
  </bpmn:process>

  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="ticket_urgent_flow">
      <!-- 省略DI定义 -->
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>
```

- [ ] **Step 3: 给 `bpmn_template_service.go` 补部署清单 case**

在 `itsm-backend/service/bpmn_template_service.go` 的 `listTemplates()` 方法里，`switch key {` 块中 `case "ticket_general_flow":` 那一段之后（第 106 行 `case "ticket_assignment_flow":` 之前）插入：

```go
		case "ticket_urgent_flow":
			info.Name = "紧急工单流程"
			info.Category = "ticket"
			info.Description = "高/紧急优先级工单处理流程（结构与通用工单流程等价，暂无独立超时/升级差异）"
```

- [ ] **Step 4: 写测试文件 `bpmn_template_service_test.go`**

创建 `itsm-backend/service/bpmn_template_service_test.go`：

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBPMNTemplateService_ListTemplates_IncludesTicketUrgentFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "ticket_urgent_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "ticket_urgent_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急工单流程", found.Name)
	assert.Equal(t, "ticket", found.Category)
	assert.Equal(t, "ticket_urgent_flow.bpmn", found.Filename)
}

func TestBPMNTemplateService_TicketGeneralAndUrgentFlow_ApprovalNodeMarked(t *testing.T) {
	parser := NewBPMNParser()

	for _, file := range []string{"ticket_general_flow.bpmn", "ticket_urgent_flow.bpmn"} {
		data, err := bpmnTemplates.ReadFile("bpmn/" + file)
		require.NoError(t, err, file)

		defs, err := parser.ParseXML(data)
		require.NoError(t, err, file)
		require.Len(t, defs.Processes, 1, file)

		var approval *BPMNUserTask
		for _, ut := range defs.Processes[0].UserTasks {
			if ut.ID == "Activity_Approval" {
				approval = ut
				break
			}
		}
		require.NotNil(t, approval, "%s 应该有 Activity_Approval 节点", file)
		assert.Equal(t, "approval", approval.TaskPurpose, "%s 的 Activity_Approval 应该打上 taskPurpose=approval", file)
	}
}

func TestBPMNTemplateService_DeployAndStartTicketUrgentFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_urgent_flow_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Urgent Flow Tenant").
		SetCode("urgent-flow").
		SetDomain("urgent.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	templateSvc := NewBPMNTemplateService(client)
	err = templateSvc.DeployTemplateByName(ctx, "ticket_urgent_flow", tenant.ID)
	require.NoError(t, err, "ticket_urgent_flow 模板应该能正常部署")

	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok)

	runCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	instance, err := engine.StartProcess(runCtx, "ticket_urgent_flow", "TICKET-URGENT-1", map[string]interface{}{
		"requester_id": float64(1),
	})
	require.NoError(t, err, "ticket_urgent_flow 应该能成功启动流程实例，不再报'获取流程定义失败'")
	assert.NotNil(t, instance)
	assert.Equal(t, "ticket_urgent_flow", instance.ProcessDefinitionKey)
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestBPMNTemplateService -v`
Expected: 三个测试全部 PASS。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn/ticket_general_flow.bpmn service/bpmn/ticket_urgent_flow.bpmn service/bpmn_template_service.go service/bpmn_template_service_test.go
git commit -m "feat(bpmn): mark ticket approval node + add missing ticket_urgent_flow"
```

---

### Task 2: `createUserTask` 审批任务专属分配逻辑（核心安全修复）

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:1-23`（import）、`:616-736`（`createUserTask` 及新增辅助函数）
- Test: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`

**Interfaces:**
- Consumes: `approver.NewDeptManagerResolver().Resolve(ctx, client *ent.Client, appCtx *approver.ApproverContext) ([]*approver.ApproverInfo, error)`（已存在，`service/approver/dept_manager_resolver.go`；`ApproverContext{TenantID, DepartmentID}`，`ApproverInfo{UserID, UserName, UserEmail, Role, Source}`，均已存在于 `service/approver/resolver.go`）；`e.groupResolver.ExpandGroupsToUsers(ctx, tenantID, candidateGroupsCSV) ([]int, []string, error)` / `e.groupResolver.MergeCandidateUsers(bpmnCandidateUsers, groupUsernames) string`（已存在，`service/bpmn/bpmn_group_resolver.go`）；`e.client *ent.Client`、`e.logger *zap.SugaredLogger`（`CustomProcessEngine` 已有字段，`bpmn_process_engine.go:96-97`）。
- Produces：本任务新增 3 个未导出方法/函数，供本文件内部使用，不对外暴露：
  - `func (e *CustomProcessEngine) loadApprovalRequester(ctx context.Context, instance *ent.ProcessInstance, getUserID func(string) string) *ent.User`
  - `func (e *CustomProcessEngine) resolveApprovalAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string`
  - `func excludeUserFromCandidates(usernames []string, u *ent.User) []string`
  - 新增常量 `const approvalFallbackCandidateGroup = "ticket-approvers"`

- [ ] **Step 1: 写测试文件（先写，此时还会失败——现有代码没有区分 approval 任务，assignee 会落到申请人自己）**

创建 `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`：

```go
package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ==================== 测试夹具 ====================

type approvalAssignmentFixture struct {
	ctx    context.Context
	engine *CustomProcessEngine
	client *ent.Client
	tenant *ent.Tenant
	def    *ent.ProcessDefinition
}

func newApprovalAssignmentFixture(t *testing.T) *approvalAssignmentFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:approval_assignment_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok, "expected ProcessEngine to be *CustomProcessEngine")

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Approval Assignment Tenant").
		SetCode("aa-tenant").
		SetDomain("aa.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-aa").
		SetDeploymentName("Approval Assignment Deployment").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	def, err := client.ProcessDefinition.Create().
		SetKey("approval_assignment_test").
		SetName("Approval Assignment Test").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return &approvalAssignmentFixture{ctx: ctx, engine: engine, client: client, tenant: tenant, def: def}
}

// createUserInTenant 在给定租户下建一个用户；deptID <= 0 表示不设置部门。
func (f *approvalAssignmentFixture) createUserInTenant(t *testing.T, tenantID int, username string, deptID int) *ent.User {
	t.Helper()
	q := f.client.User.Create().
		SetUsername(username).
		SetEmail(username + "@aa.example.com").
		SetName(username).
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenantID)
	if deptID > 0 {
		q = q.SetDepartmentID(deptID)
	}
	u, err := q.Save(f.ctx)
	require.NoError(t, err)
	return u
}

func (f *approvalAssignmentFixture) createUser(t *testing.T, username string, deptID int) *ent.User {
	return f.createUserInTenant(t, f.tenant.ID, username, deptID)
}

// createDepartmentInTenant 在给定租户下建一个部门；managerID/parentID <= 0 表示不设置。
func (f *approvalAssignmentFixture) createDepartmentInTenant(t *testing.T, tenantID int, name string, managerID, parentID int) *ent.Department {
	t.Helper()
	q := f.client.Department.Create().
		SetName(name).
		SetCode(name).
		SetTenantID(tenantID)
	if managerID > 0 {
		q = q.SetManagerID(managerID)
	}
	if parentID > 0 {
		q = q.SetParentID(parentID)
	}
	d, err := q.Save(f.ctx)
	require.NoError(t, err)
	return d
}

func (f *approvalAssignmentFixture) createDepartment(t *testing.T, name string, managerID, parentID int) *ent.Department {
	return f.createDepartmentInTenant(t, f.tenant.ID, name, managerID, parentID)
}

func (f *approvalAssignmentFixture) createGroup(t *testing.T, name string, memberIDs ...int) *ent.Group {
	t.Helper()
	g, err := f.client.Group.Create().
		SetName(name).
		SetTenantID(f.tenant.ID).
		AddMemberIDs(memberIDs...).
		Save(f.ctx)
	require.NoError(t, err)
	return g
}

func (f *approvalAssignmentFixture) createInstance(t *testing.T, keySuffix string, variables map[string]interface{}) *ent.ProcessInstance {
	t.Helper()
	inst, err := f.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-aa-" + keySuffix).
		SetProcessDefinitionKey(f.def.Key).
		SetProcessDefinitionID(f.def.ID).
		SetStatus("running").
		SetVariables(variables).
		SetTenantID(f.tenant.ID).
		Save(f.ctx)
	require.NoError(t, err)
	return inst
}

func approvalTask(id, name string) *BPMNUserTask {
	return &BPMNUserTask{ID: id, Name: name, TaskPurpose: "approval"}
}

// getCreatedTask 按 taskDefinitionKey 取回刚创建的 ProcessTask，方便断言 Assignee/CandidateUsers。
func (f *approvalAssignmentFixture) getCreatedTask(t *testing.T, instanceID int, taskDefinitionKey string) *ent.ProcessTask {
	t.Helper()
	task, err := f.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceIDEQ(instanceID), processtask.TaskDefinitionKeyEQ(taskDefinitionKey)).
		Only(f.ctx)
	require.NoError(t, err)
	return task
}

// ==================== 主路径：部门负责人 ====================

func TestCreateUserTask_Approval_ManagerPath_AssignsDeptManager(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager1", 0)
	dept := fx.createDepartment(t, "Engineering", manager.ID, 0)
	requester := fx.createUser(t, "requester1", dept.ID)

	instance := fx.createInstance(t, "manager-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), task.Assignee)
}

func TestCreateUserTask_Approval_ManagerPath_ParentDepartmentFallback(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager2", 0)
	parentDept := fx.createDepartment(t, "Parent", manager.ID, 0)
	childDept := fx.createDepartment(t, "Child", 0, parentDept.ID)
	requester := fx.createUser(t, "requester2", childDept.ID)

	instance := fx.createInstance(t, "parent-fallback", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), task.Assignee, "子部门没有 manager 时应该递归查父部门")
}

// ==================== 兜底路径：部门未配置 manager ====================

func TestCreateUserTask_Approval_FallbackPath_NoManager_UsesCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept", 0, 0)
	requester := fx.createUser(t, "requester3", dept.ID)
	approverA := fx.createUser(t, "approverA", 0)
	approverB := fx.createUser(t, "approverB", 0)
	fx.createGroup(t, "ticket-approvers", approverA.ID, approverB.ID)

	instance := fx.createInstance(t, "no-manager", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee, "部门没配 manager 时不应该直接指派给任何人")
	assert.Contains(t, task.CandidateUsers, "approverA")
	assert.Contains(t, task.CandidateUsers, "approverB")
}

// ==================== 核心安全断言：两条路径都要排除申请人自己 ====================

func TestCreateUserTask_Approval_ManagerPath_SkipsWhenManagerIsRequester(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// 申请人自己就是这个部门的 manager
	dept := fx.createDepartment(t, "Self Managed", 0, 0)
	requester := fx.createUser(t, "requester4", dept.ID)
	_, err := fx.client.Department.UpdateOne(dept).SetManagerID(requester.ID).Save(fx.ctx)
	require.NoError(t, err)

	backupApprover := fx.createUser(t, "backupApprover", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "manager-is-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(requester.ID), task.Assignee, "部门负责人是申请人自己时不能把 assignee 定成申请人")
	assert.Equal(t, "", task.Assignee, "应该转入候选组兜底，而不是留一个错误的 assignee")
}

func TestCreateUserTask_Approval_FallbackPath_ExcludesRequesterFromCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 2", 0, 0)
	requester := fx.createUser(t, "requester5", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover2", 0)
	// requester 自己也在 ticket-approvers 组里——现实中常见场景（普通审批人自己也会提工单）
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "exclude-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotContains(t, task.CandidateUsers, "requester5", "candidate_users 不能包含申请人的用户名")
	assert.NotContains(t, task.CandidateUsers, strconv.Itoa(requester.ID), "candidate_users 不能包含申请人的 ID")
	assert.Contains(t, task.CandidateUsers, "backupApprover2")
}

func TestCreateUserTask_Approval_FallbackPath_EmptyAfterExclusion_NoOrphanRequester(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 3", 0, 0)
	requester := fx.createUser(t, "requester6", dept.ID)
	// ticket-approvers 组里只有申请人自己——排除之后应该是空，而不是静默把申请人塞回去
	fx.createGroup(t, "ticket-approvers", requester.ID)

	instance := fx.createInstance(t, "empty-after-exclusion", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee)
	assert.Equal(t, "", task.CandidateUsers, "候选组排除申请人后为空时，不能兜底又把申请人塞回去")
}

// ==================== 跳过范围：assignee_id 变量对 approval 任务完全不生效 ====================

func TestCreateUserTask_Approval_IgnoresAssigneeIDVariable(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 4", 0, 0)
	requester := fx.createUser(t, "requester7", dept.ID)
	scriptedAssignee := fx.createUser(t, "scriptedAssignee", 0)

	instance := fx.createInstance(t, "ignore-assignee-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"assignee_id":  float64(scriptedAssignee.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(scriptedAssignee.ID), task.Assignee, "approval 任务不应该读 assignee_id 流程变量")
}

// ==================== BPMN 显式声明 candidateGroups 时，不触发部门负责人解析 ====================
// 覆盖 legacy 审批链迁移出来的按角色/组路由节点（service/legacy_approval_migration_service.go
// 生成的 BPMN 也带 taskPurpose="approval"，但候选人来自迁移前配置的具体角色/组，不该被这次
// 修复新增的"部门负责人"语义覆盖）。

func TestCreateUserTask_Approval_ExplicitCandidateGroupsSkipsManagerPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager3", 0)
	dept := fx.createDepartment(t, "Has Manager", manager.ID, 0)
	requester := fx.createUser(t, "requester8", dept.ID)
	legacyApprover := fx.createUser(t, "legacyApprover", 0)
	fx.createGroup(t, "legacy-role-approvers", legacyApprover.ID)

	instance := fx.createInstance(t, "explicit-candidate-groups", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.CandidateGroups = "legacy-role-approvers"

	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", created.Assignee, "BPMN 显式声明 candidateGroups 时不应该触发部门负责人解析")
	assert.Contains(t, created.CandidateUsers, "legacyApprover")
	assert.NotContains(t, created.CandidateUsers, strconv.Itoa(manager.ID), "不应该混入跟这个节点配置无关的部门负责人")
}

// ==================== 跨租户隔离回归 ====================

func TestCreateUserTask_Approval_TenantIsolation_DepartmentNotVisibleAcrossTenants(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Other Tenant").
		SetCode("other-tenant").
		SetDomain("other.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	// 在另一个租户建一个有 manager 的部门
	otherManager := fx.createUserInTenant(t, otherTenant.ID, "otherManager", 0)
	otherDept := fx.createDepartmentInTenant(t, otherTenant.ID, "Other Dept", otherManager.ID, 0)

	// 本租户的申请人 department_id 误配置成了另一个租户的部门 ID（模拟数据错误场景）
	requester := fx.createUser(t, "requester9", otherDept.ID)
	backupApprover := fx.createUser(t, "backupApprover3", 0)
	fx.createGroup(t, "ticket-approvers", backupApprover.ID)

	instance := fx.createInstance(t, "tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(otherManager.ID), task.Assignee, "不能跨租户解析到另一个租户的部门负责人")
	assert.Equal(t, "", task.Assignee, "部门在本租户查不到时应该转候选组兜底")
	assert.Contains(t, task.CandidateUsers, "backupApprover3")
}

// ==================== 回归：非 approval 任务的自动分配链不受影响 ====================

func TestCreateUserTask_NonApproval_StillUsesRequesterIDFallback(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	requester := fx.createUser(t, "requester10", 0)
	instance := fx.createInstance(t, "non-approval", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	// TaskPurpose 留空，模拟普通工作任务（比如"任务分配"节点）
	err := fx.engine.createUserTask(fx.ctx, instance, &BPMNUserTask{ID: "Activity_Handle", Name: "工单处理"})
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Handle")
	assert.Equal(t, strconv.Itoa(requester.ID), task.Assignee, "非 approval 任务应该继续走原来的 requester_id 兜底链")
}

// ==================== authorizeTaskActor 集成：申请人不能操作自己的审批任务 ====================

func TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_ManagerPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Self Managed 2", 0, 0)
	requester := fx.createUser(t, "requester11", dept.ID)
	_, err := fx.client.Department.UpdateOne(dept).SetManagerID(requester.ID).Save(fx.ctx)
	require.NoError(t, err)
	backupApprover := fx.createUser(t, "backupApprover4", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "authz-manager-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")

	actorCtx := context.WithValue(fx.ctx, bpmn.BPMNUserIDContextKey, requester.ID)
	err = fx.engine.authorizeTaskActor(actorCtx, task)
	assert.Error(t, err, "申请人不应该能操作自己提交工单的审批任务")
}

func TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_CandidateGroupPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 5", 0, 0)
	requester := fx.createUser(t, "requester12", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover5", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "authz-candidate-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")

	actorCtx := context.WithValue(fx.ctx, bpmn.BPMNUserIDContextKey, requester.ID)
	err := fx.engine.authorizeTaskActor(actorCtx, task)
	assert.Error(t, err, "申请人不应该能通过候选组身份操作自己提交工单的审批任务")
}

// ==================== 纯函数单元测试 ====================

func TestExcludeUserFromCandidates(t *testing.T) {
	u := &ent.User{ID: 42, Username: "alice", Email: "alice@example.com"}

	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"匹配用户名", []string{"alice", "bob"}, []string{"bob"}},
		{"匹配 ID 字符串", []string{"42", "bob"}, []string{"bob"}},
		{"匹配 Email", []string{"alice@example.com", "bob"}, []string{"bob"}},
		{"不匹配则原样保留", []string{"bob", "carol"}, []string{"bob", "carol"}},
		{"空输入", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := excludeUserFromCandidates(tt.input, u)
			assert.Equal(t, tt.want, got)
		})
	}

	assert.Nil(t, excludeUserFromCandidates([]string{"a"}, nil), "nil user 时原样返回")
}
```

测试代码里的 `getCreatedTask` 用到了 `processtask.ProcessInstanceIDEQ`/`processtask.TaskDefinitionKeyEQ`，各处断言用到了 `strconv.Itoa`——都是标准库/已有 ent 生成代码，不需要额外写包装函数。在文件顶部 `import` 块（`"itsm-backend/ent"`、`"itsm-backend/ent/enttest"` 那两行附近）补上：

```go
	"strconv"

	"itsm-backend/ent/processtask"
```

- [ ] **Step 2: 运行测试，确认按预期失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestCreateUserTask_Approval|TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask|TestExcludeUserFromCandidates' -v`

Expected：`TestExcludeUserFromCandidates` 因为 `excludeUserFromCandidates` 函数还不存在，编译失败；其余用到 `createUserTask` 的测试能编译（`createUserTask` 已存在），但断言失败——现有代码里 approval 任务的 assignee 会解析成 `requester_id`（对应测试里预期 `task.Assignee != strconv.Itoa(requester.ID)` 的用例会失败），候选组展开也不会排除申请人（预期 `NotContains` 的用例会失败）。这一步是确认"改之前测试真的会因为这个 bug 失败"，不是编译错误就直接跳过。

- [ ] **Step 3: 加 import 和常量**

在 `itsm-backend/service/bpmn_process_engine.go` 的 import 块（第 3-25 行）里，`"itsm-backend/ent/user"` 那一行下面加一行：

```go
	"itsm-backend/service/approver"
```

在 `createUserTask` 函数定义（第 617 行）前面加常量：

```go
// approvalFallbackCandidateGroup 是 taskPurpose="approval" 任务在没有解析出部门负责人、
// 且 BPMN 也没有显式声明 candidateGroups 时使用的默认候选组名。租户需要在 /admin/groups
// 里创建这个组并配置至少 2 名成员，否则单人部门 + 单人组的组合会出现审批任务无人可领。
const approvalFallbackCandidateGroup = "ticket-approvers"

```

- [ ] **Step 4: 重写 `createUserTask` 函数体**

把 `itsm-backend/service/bpmn_process_engine.go` 里现有的 `createUserTask` 函数（第 617-733 行，从 `func (e *CustomProcessEngine) createUserTask` 到它的 `}`，即 `splitNonEmptyCSV` 定义之前）整体替换成：

```go
func (e *CustomProcessEngine) createUserTask(ctx context.Context, instance *ent.ProcessInstance, task *BPMNUserTask) error {
	// 自动分配逻辑：优先级 BPMN定义 > 流程变量(request/assignee) > 默认分配
	assignee := task.Assignee

	// 辅助函数：从变量中提取用户ID
	getUserID := func(key string) string {
		if v, ok := instance.Variables[key]; ok {
			switch val := v.(type) {
			case float64:
				// JSON numbers are float64
				if val > 0 {
					return strconv.FormatFloat(val, 'f', 0, 64)
				}
			case int:
				if val > 0 {
					return strconv.Itoa(val)
				}
			case string:
				if val != "" && val != "0" {
					return val
				}
			}
		}
		return ""
	}

	// taskPurpose="approval" 的任务需要申请人身份，用来：
	// 1) 解析申请人所在部门的负责人作为 assignee；
	// 2) 把申请人自己从 candidateGroups 展开出的候选人里剔除。
	// 非 approval 任务不需要，不做这次额外查询。
	var approvalRequester *ent.User
	if task.TaskPurpose == "approval" {
		approvalRequester = e.loadApprovalRequester(ctx, instance, getUserID)
	}

	// 如果BPMN没有定义分配人，从流程变量中获取
	if assignee == "" {
		if task.TaskPurpose == "approval" {
			// 审批任务：不走 requester_id/triggered_by/assignee_id/getDefaultAssigntee 这条链
			// （否则几乎总会落回申请人自己），改成部门负责人解析。
			// 如果 BPMN 已经显式声明了 candidateGroups（比如 legacy 审批链迁移出来的按角色/组
			// 路由节点，见 legacy_approval_migration_service.go），说明这个节点的路由方式是
			// 配置驱动的，不触发部门负责人解析，直接进入下面的候选组展开——避免用一个跟节点
			// 配置无关的"部门负责人"语义覆盖它。
			if strings.TrimSpace(task.CandidateGroups) == "" {
				assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
			}
		} else {
			// 优先使用 requester_id（工单申请人）
			assignee = getUserID("requester_id")
			// 其次使用 triggered_by（触发者）
			if assignee == "" {
				assignee = getUserID("triggered_by")
			}
			// 再其次使用 assignee_id
			if assignee == "" {
				assignee = getUserID("assignee_id")
			}
			// 如果还是没有，根据任务名称自动分配
			if assignee == "" {
				assignee = e.getDefaultAssigntee(ctx, instance, task)
			}
		}
	}

	// 审批任务如果部门负责人解析失败（没配置/manager就是申请人自己），且 BPMN 也没有
	// 声明 candidateGroups，兜底用固定候选组，保证任务始终有机会被领取。
	candidateGroupsToExpand := task.CandidateGroups
	if task.TaskPurpose == "approval" && assignee == "" && strings.TrimSpace(candidateGroupsToExpand) == "" {
		candidateGroupsToExpand = approvalFallbackCandidateGroup
	}

	// 展开 candidateGroups 为具体用户，合并到 candidate_users。
	// 这样「我的待办」接口才有可能查到分配给我的任务。
	expandedCandidateUsers := task.CandidateUsers
	if e.groupResolver != nil && strings.TrimSpace(candidateGroupsToExpand) != "" {
		_, groupUsernames, err := e.groupResolver.ExpandGroupsToUsers(ctx, instance.TenantID, candidateGroupsToExpand)
		if err != nil {
			// 解析失败：记录警告但不阻塞流程，以免审批组配置漂移导致整个流程中断
			e.logger.Warnw(
				"审批组展开失败，继续仅使用 BPMN candidateUsers",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"error", err,
			)
		} else {
			if task.TaskPurpose == "approval" && approvalRequester != nil {
				groupUsernames = excludeUserFromCandidates(groupUsernames, approvalRequester)
			}
			expandedCandidateUsers = e.groupResolver.MergeCandidateUsers(task.CandidateUsers, groupUsernames)
			e.logger.Infow(
				"审批组已展开",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"expandedUsers", groupUsernames,
			)
		}
	}
	if task.TaskPurpose == "approval" && assignee == "" && strings.TrimSpace(expandedCandidateUsers) == "" {
		e.logger.Warnw(
			"审批任务没有解析到任何审批人（部门负责人未配置，候选组展开后也为空），任务将无人可领",
			"taskID", task.ID,
			"taskName", task.Name,
			"candidateGroups", candidateGroupsToExpand,
		)
	}

	// Use instance.ID (auto-generated integer) for the relationship
	taskConfig := map[string]interface{}{
		"taskPurpose": task.TaskPurpose, "approvalMode": task.ApprovalMode,
		"approvalThreshold": task.ApprovalThreshold, "rejectStrategy": task.RejectStrategy,
		"timeoutAction": task.TimeoutAction, "allowDelegate": task.AllowDelegate,
		"allowAddApprover":        task.AllowAddApprover,
		"commentRequiredOnReject": task.CommentRequiredOnReject,
	}
	createdTask, err := e.client.ProcessTask.Create().
		SetTaskID(fmt.Sprintf("TASK-%s-%d", task.ID, time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey(task.ID).
		SetTaskName(task.Name).
		SetTaskType("user_task").
		SetStatus("created").
		SetAssignee(assignee).
		SetCandidateUsers(expandedCandidateUsers).
		SetCandidateGroups(candidateGroupsToExpand).
		SetFormKey(task.FormKey).
		SetTaskVariables(taskConfig).
		SetTenantID(instance.TenantID).
		SetCreatedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("创建用户任务失败: %w", err)
	}
	if task.TaskPurpose == "approval" && task.ApprovalMode != "" && task.ApprovalMode != "single" {
		approvers := splitNonEmptyCSV(expandedCandidateUsers)
		if len(approvers) > 1 {
			threshold := task.ApprovalThreshold
			switch task.ApprovalMode {
			case "any":
				threshold = 1
			case "all", "sequential":
				threshold = len(approvers)
			}
			approvalType := "parallel"
			if task.ApprovalMode == "sequential" {
				approvalType = "serial"
			}
			if _, err := e.taskService.CreateCounterSignTasks(ctx, createdTask.TaskID, &CounterSignRequest{ApprovalType: approvalType, Approvers: approvers, Threshold: threshold}); err != nil {
				return fmt.Errorf("创建会签任务失败: %w", err)
			}
		}
	}
	e.logger.Infow("User task created with auto-assignment", "taskID", task.ID, "taskName", task.Name, "assignee", assignee)
	return nil
}

// loadApprovalRequester 加载 taskPurpose="approval" 任务对应流程实例的申请人（requester_id
// 流程变量指向的 User），找不到时返回 nil（调用方会退化到候选组兜底路径，不会报错阻塞流程）。
func (e *CustomProcessEngine) loadApprovalRequester(ctx context.Context, instance *ent.ProcessInstance, getUserID func(string) string) *ent.User {
	idStr := getUserID("requester_id")
	if idStr == "" {
		return nil
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return nil
	}
	requester, err := e.client.User.Query().
		Where(user.IDEQ(id), user.TenantIDEQ(instance.TenantID)).
		Only(ctx)
	if err != nil {
		e.logger.Warnw("解析审批任务申请人失败", "requesterID", id, "tenantID", instance.TenantID, "error", err)
		return nil
	}
	return requester
}

// resolveApprovalAssignee 把申请人所在部门（含祖先部门递归）的负责人解析为审批任务的
// assignee。复用 service/approver.DeptManagerResolver（已有、已测试、已被 legacy 审批链
// approval_service.go:940 使用的部门->负责人查询），不重新实现部门递归逻辑。
// 解析失败，或者解析出的负责人正好是申请人自己（避免部门负责人审批自己提交的工单），
// 都返回空字符串——调用方会转入 candidateGroups 兜底路径。
func (e *CustomProcessEngine) resolveApprovalAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string {
	if requester == nil || requester.DepartmentID == 0 {
		return ""
	}
	approvers, err := approver.NewDeptManagerResolver().Resolve(ctx, e.client, &approver.ApproverContext{
		TenantID:     instance.TenantID,
		DepartmentID: requester.DepartmentID,
	})
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"审批任务未解析到部门负责人，转候选组兜底",
			"requesterID", requester.ID, "departmentID", requester.DepartmentID, "error", err,
		)
		return ""
	}
	manager := approvers[0]
	if manager.UserID == requester.ID {
		e.logger.Infow(
			"部门负责人是申请人本人，转候选组兜底，避免自己审批自己",
			"requesterID", requester.ID, "departmentID", requester.DepartmentID,
		)
		return ""
	}
	return strconv.Itoa(manager.UserID)
}

// excludeUserFromCandidates 从 candidateGroups 展开出来的候选人显示名列表里剔除某个用户。
// 匹配语义跟 authorizeTaskActor.allowed（bpmn_process_engine.go:417）保持一致：用户 ID
// 字符串或用户名；额外多判断一次 Email，覆盖 GroupResolver.ExpandGroupsToUsers 在用户名
// 为空时退化用 Email 做候选人显示名的情况。
func excludeUserFromCandidates(usernames []string, u *ent.User) []string {
	if u == nil || len(usernames) == 0 {
		return usernames
	}
	idStr := strconv.Itoa(u.ID)
	filtered := make([]string, 0, len(usernames))
	for _, name := range usernames {
		if name == idStr {
			continue
		}
		if u.Username != "" && name == u.Username {
			continue
		}
		if u.Email != "" && name == u.Email {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestCreateUserTask_Approval|TestCreateUserTask_NonApproval|TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask|TestExcludeUserFromCandidates' -v`
Expected：全部 PASS。

- [ ] **Step 6: 跑整个 service 包和全量测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -100`
Expected：编译通过；重点看 `bpmn_process_engine_test.go`、`bpmn_process_engine_ext_test.go`、`bpmn_approval_bridge_service_test.go`、`ticket_workflow_bpmn_bridge_test.go`、`legacy_approval_migration_service_unit_test.go`、`provisioning_service_test.go` 这几个跟 `createUserTask`/approval 相关的既有测试文件全部继续 PASS。

再跑一次全仓库测试确认没有跨包影响：

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok" | head -50`
Expected：没有 FAIL 输出（`grep -v "^ok"` 之后应该只剩下没有测试文件的包的 `?` 行，没有 `FAIL`）。

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_approval_assignment_test.go
git commit -m "fix(bpmn): approval tasks resolve dept-manager assignee, exclude requester from candidates

Closes the self-approval gap left open by the ServiceRequest-to-Ticket
delegation work: taskPurpose=\"approval\" tasks no longer fall back to
requester_id/triggered_by/assignee_id, and both the dept-manager and
candidateGroups fallback paths exclude the requester from the result."
```
