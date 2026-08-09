# 审批收敛·组件② — 退休双重触发 + 补两个绑定/文件缺失 bug + 4 个默认模板取舍 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删掉 `CreateTicket` 里的旧审批系统同步触发调用；补齐 `change_emergency_flow.bpmn` 缺文件；修正 `config/seed/default.json` 里跟 resolver 实际查询方式对不上的 `ProcessBinding` 种子数据；把"服务请求审批"/"权限申请审批"两个默认模板迁到 BPMN 原生机制，"普通变更审批"/"紧急变更审批"两个直接退休（Change 从未真正用过它们）。

**Architecture:** 四个任务，`ticket_service.go` 的调用点删除（2a）和 `change_emergency_flow.bpmn` 补文件（2b）互相独立；`config/seed/default.json` 的 `ProcessBinding` 修复（2c）和 `ApprovalWorkflow` 清理+新模板接入（2d）都改同一个 JSON 文件的不同数组，2c 必须先于 2d（2d 依赖 2c 已经把 `service_request_flow` 的绑定修好，2d 打的 `taskPurpose` 标记才有实际效果）。

**Tech Stack:** Go 1.x / Ent ORM / `stretchr/testify` / sqlite3（enttest）——跟这次会话之前的自审批修复、组件①用同一套。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md`（组件②部分，已经用户复审通过，复审后又修正过两处：优先级路由机制改成 `ResolveWithPriority` 显式特判而非 `ProcessBinding.Conditions`；`business_type="change"` 那行种子数据的修复对 Change 当前行为无影响）。
- **写这份计划时新发现、订正过设计文档的一点**：`ApprovalWorkflow`/`ProcessBinding` 真正生效的种子源是 `itsm-backend/config/seed/default.json`（通过 `pkg/seeder/seeder.go` 的 `loadSeedConfig` 在进程启动时读取，写入 `tenant.code=="default"` 模板租户），不是 `pkg/seeder/tenant_provisioner.go` 或 `pkg/seeder/seeder.go` 里那份内嵌的 Go 结构体兜底配置（`getEmbeddedConfig()`，只有 JSON 文件缺失/损坏时才会启用，且内容已经跟 JSON 文件不同步——这个不同步本身不在这次修复范围内，只是记录一下，不要把它跟真正生效的 JSON 文件搞混）。所有这次要改的种子数据改动都只改 `config/seed/default.json`，不改 `pkg/seeder/tenant_provisioner.go`（它只是把"default"模板租户已经落库的记录克隆给运维手工跑 `cmd/provision_tenant` 工具时指定的目标租户，不需要跟着改）。
- `service_request_flow.bpmn`/`change_normal_flow.bpmn` 的审批节点所在网关都已经有正确的 `conditionExpression`（`need_approval == true`/`!= true`），不像 `ticket_general_flow.bpmn` 那样有"审批节点走不到"的拓扑 bug——这次改动不需要修网关，只需要打标记/建新文件/改种子数据。
- 只有 `service_request` 这一条 `ProcessBinding` 种子行的修复会改变真实运行时行为（`service_request_flow.bpmn` 从"够不着"变成"够得着"）。`incident`/`problem`/`change`/`release` 四条种子行的 `business_type` 同样写错了（用的是顶层业务域名而不是 `"ticket"` + `business_sub_type`），但这次一并顺手修正——`incident`/`problem`/`change` 三个业务域各自的 service（`incident_service.go`/`problem_service.go`/`change_service.go`）都硬编码自己的 `ProcessDefinitionKey`，压根不查这张表，修不修都不影响它们当前行为；`release_service.go` 现在完全不调用 `TriggerProcess`，这条绑定目前对任何行为都没有影响。改的理由是保持同一个文件里同一类字段全部写法一致，不是因为这四条本身在生效。
- 新增/修改的所有测试保持在 `package service`（`ticket_service_test.go`/`process_resolver_test.go`/`bpmn_template_service_test.go` 所在包），不新建子包。
- 测试用 `enttest.Open(t, "sqlite3", "file:<unique-name>?mode=memory&cache=shared&_fk=1")`，DSN 每个测试文件用不同前缀（`testDSN()` 辅助函数已存在于 `service/enttest_dsn_test.go`，可以直接复用它生成的通用 DSN，不需要每个测试都手写唯一名）。

## File Structure

- Modify: `itsm-backend/service/ticket_service.go` — 删除 `CreateTicket` 里同步触发旧审批系统的代码块。
- Create: `itsm-backend/service/bpmn/change_emergency_flow.bpmn` — `change_normal_flow.bpmn` 的结构等价副本。
- Modify: `itsm-backend/config/seed/default.json` — 修正 5 条 `process_bindings` 的 `business_type`/`business_sub_type`；删除全部 4 条 `approval_workflows`。
- Modify: `itsm-backend/service/bpmn/service_request_flow.bpmn` — `Activity_Approval` 节点加 `taskPurpose="approval"`。
- Create: `itsm-backend/service/bpmn/service_request_urgent_flow.bpmn` — `service_request_flow.bpmn` 的结构等价副本。
- Modify: `itsm-backend/service/bpmn_template_service.go` — `listTemplates()` 的 `switch` 加 `"service_request_urgent_flow"` case。
- Modify: `itsm-backend/service/process_resolver.go` — `ResolveWithPriority` 加 `service_request_flow`→`service_request_urgent_flow` 的优先级特判。
- Create: `itsm-backend/service/process_resolver_test.go` — 目前这个文件不存在，新建。

---

### Task 1: 退休 `CreateTicket` 里同步触发的旧审批系统调用

**Files:**
- Modify: `itsm-backend/service/ticket_service.go:210-224`
- Test: `itsm-backend/service/ticket_service_test.go`（追加）

**Interfaces:**
- Consumes: `ApprovalService.TriggerApproval`（已存在，本任务不修改这个方法本身，只删调用点）、`NewApprovalService(client, logger) *ApprovalService`（已存在，`service/approval_service.go:26`）、`TicketService.SetApprovalService(a *ApprovalService)`（已存在，`ticket_service.go:110`，本任务保留不动——组件④删旧代码时才处理这个 setter）。
- Produces：无新增符号，纯删除。

- [ ] **Step 1: 写回归测试（先写，此时会失败——现在的代码确实会创建 ApprovalRecord）**

在 `itsm-backend/service/ticket_service_test.go` 文件末尾追加：

```go

// TestTicketService_CreateTicket_DoesNotTriggerLegacyApproval 是审批收敛组件②的回归测试：
// CreateTicket 只应该触发 BPMN（异步），不应该再同步调用旧的 ApprovalService.TriggerApproval。
// 用真实 ApprovalService + 一条会精确匹配的 ApprovalWorkflow 种子数据来验证——如果调用点还在，
// 这条工作流会命中并创建一条 ApprovalRecord；调用点删掉之后不会有任何记录产生。
func TestTicketService_CreateTicket_DoesNotTriggerLegacyApproval(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ticketService := NewTicketServiceForTest(client, logger)
	ticketService.SetApprovalService(NewApprovalService(client, logger))

	ctx := context.Background()

	testTenant, err := client.Tenant.Create().
		SetName("Dual Trigger Test Tenant").
		SetCode("dual-trigger-test").
		SetDomain("dual-trigger.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("dualtriggeruser").
		SetEmail("dualtrigger@example.com").
		SetName("Dual Trigger User").
		SetPasswordHash("hashedpassword").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 精确匹配的旧审批工作流：ticket_type + priority 都对得上下面创建的工单，
	// 节点用 "user" 类型直接指向 testUser，resolveApprover 不需要额外查询就能成功，
	// 保证"如果调用点还在，一定会真的创建出 ApprovalRecord"，不是被节点解析失败悄悄跳过。
	_, err = client.ApprovalWorkflow.Create().
		SetName("Dual Trigger Regression Workflow").
		SetTicketType("incident").
		SetPriority("medium").
		SetIsActive(true).
		SetTenantID(testTenant.ID).
		SetNodes([]map[string]interface{}{
			{"assigneeType": "user", "assigneeValue": fmt.Sprintf("%d", testUser.ID), "name": "回归测试审批"},
		}).
		Save(ctx)
	require.NoError(t, err)

	// 注意：ApprovalWorkflow.TicketType 匹配的是 ticket.Type，不是 ticket 的分类展示字段
	// Category（那个字段只用来查一个可选的 CategoryID，跟 TriggerApproval 的匹配逻辑无关）。
	// 必须显式传 Type，不能只传 Category，否则这条测试即使旧调用点还在也会因为
	// findMatchingWorkflow 匹配不上而通不过——变成一条自己骗自己的假阳性测试。
	created, err := ticketService.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:       "双重触发回归测试工单",
		Description: "验证 CreateTicket 不再同步调用旧审批系统",
		Priority:    "medium",
		Type:        "incident",
		RequesterID: testUser.ID,
	}, testTenant.ID)
	require.NoError(t, err)
	require.NotNil(t, created)

	count, err := client.ApprovalRecord.Query().Where(approvalrecord.TenantIDEQ(testTenant.ID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "CreateTicket 不应该再触发旧审批系统创建 ApprovalRecord")
}
```

在文件顶部 `import` 块里补上 `"itsm-backend/ent/approvalrecord"`（如果还没有的话，检查一下现有 import 列表）。

- [ ] **Step 2: 运行测试，确认按预期失败**

Run: `cd itsm-backend && go test ./service/... -run TestTicketService_CreateTicket_DoesNotTriggerLegacyApproval -v`
Expected：FAIL——`assert.Equal(t, 0, count, ...)` 断言失败，实际 count 是 1（现有代码确实同步创建了一条 `ApprovalRecord`）。

- [ ] **Step 3: 删除 `CreateTicket` 里的旧审批触发代码块**

在 `itsm-backend/service/ticket_service.go` 里，把这段（现在大约在 210-224 行）完整删除：

```go
	// 触发审批（同步，走 ApprovalService，查找匹配工作流并创建 ApprovalRecord）
	// 这是 V1 缺失的 Phase 1 #1 缺陷修复：V2 必须让工单进入审批链路
	if s.approvalSvc != nil {
		if _, err := s.approvalSvc.TriggerApproval(ctx, &ApprovalTriggerRequest{
			TicketID:     tkt.ID,
			TicketNumber: tkt.TicketNumber,
			TicketTitle:  tkt.Title,
			TicketType:   string(tkt.Type),
			Priority:     string(tkt.Priority),
			RequesterID:  tkt.RequesterID,
			TenantID:     tenantID,
		}); err != nil {
			s.logger.Warnw("Approval trigger failed", "error", err, "ticket_id", tkt.ID)
		}
	}

```

删除后，紧接着的"异步发送通知"那段代码块保持不变、直接衔接上一段（原本在这段代码之前的 SLA 计算逻辑）——不要额外调整周围代码的空行/注释。`TicketService` struct 里的 `approvalSvc` 字段（第 38 行）、`SetApprovalService` 方法（第 109-111 行）、`TicketServiceConfig.ApprovalService` 字段（第 56 行）都不要动，留着给组件④处理。

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestTicketService_CreateTicket' -v`
Expected：全部 PASS，包括新增的和已经存在的 `TestTicketService_CreateTicket` 系列测试（回归确认删除这段代码没有破坏正常的工单创建流程）。

- [ ] **Step 5: 跑整个 service 包测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -100`
Expected：编译通过；重点确认 `ticket_service_test.go`、`ticket_service_ext_test.go`、`ticket_service_table_test.go`、`ticket_core_service_test.go`、`approval_service_test.go`、`approval_service_table_test.go` 全部继续 PASS（`approval_service.go` 本身没改，`TriggerApproval` 方法还在，只是不再被 `CreateTicket` 调用——直接测 `ApprovalService` 的测试不应该受影响）。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/ticket_service.go service/ticket_service_test.go
git commit -m "fix(ticket): retire duplicate legacy-approval trigger in CreateTicket

CreateTicket synchronously called ApprovalService.TriggerApproval AND
asynchronously triggered BPMN for every ticket, unconditionally --
two independent 'V1 missing this, V2 must fix it' passes that didn't
know about each other. Removes the synchronous legacy call; BPMN
triggering (already the correct mechanism) is untouched."
```

---

### Task 2: 补齐 `change_emergency_flow.bpmn` 缺文件

**Files:**
- Create: `itsm-backend/service/bpmn/change_emergency_flow.bpmn`
- Test: `itsm-backend/service/bpmn_template_service_test.go`（追加）

**Interfaces:**
- Consumes: `BPMNTemplateService.listTemplates()`/`DeployTemplateByName`（已存在）、`CustomProcessEngine.StartProcess`（已存在）——跟这次会话 Task 1（`ticket_urgent_flow.bpmn`）用的是完全一样的模式。
- Produces：无新增符号，只是新增一个 `.bpmn` 文件。`bpmn_template_service.go` 的 `listTemplates()` switch 已经有 `case "change_emergency_flow":`（第 116-119 行），这次不需要再改这个文件——之前这个 case 是死代码（对应文件不存在），补上文件之后自动激活，不用碰 Go 代码。

- [ ] **Step 1: 新建 `change_emergency_flow.bpmn`**

创建 `itsm-backend/service/bpmn/change_emergency_flow.bpmn`，是 `itsm-backend/service/bpmn/change_normal_flow.bpmn` 的结构等价副本，只改 `process id`/`name`、`metaData` 的 `sub_category`/`description`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                 xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
                 xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
                 targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="change_emergency_flow" name="紧急变更流程" isExecutable="true">
    <bpmn:extensionElements>
      <bpmn:metaData name="category">change</bpmn:metaData>
      <bpmn:metaData name="sub_category">emergency</bpmn:metaData>
      <bpmn:metaData name="version">1.0.0</bpmn:metaData>
      <bpmn:metaData name="description">紧急变更快速处理流程（结构与普通变更流程等价，暂无独立的审批链/时限差异）</bpmn:metaData>
    </bpmn:extensionElements>

    <!-- 开始事件：变更申请 -->
    <bpmn:startEvent id="StartEvent_1" name="变更申请">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>

    <!-- 变更评估 -->
    <bpmn:userTask id="Activity_Assessment" name="变更评估">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">update_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 审批判断网关 -->
    <bpmn:exclusiveGateway id="Gateway_Approval" name="是否需要审批?">
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
      <bpmn:outgoing>Flow_Schedule</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- CAB审批 -->
    <bpmn:userTask id="Activity_CABApproval" name="CAB审批">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">approve_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_3</bpmn:incoming>
      <bpmn:outgoing>Flow_ApprovalResult</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 审批结果网关 -->
    <bpmn:exclusiveGateway id="Gateway_ApprovalResult" name="审批结果?">
      <bpmn:incoming>Flow_ApprovalResult</bpmn:incoming>
      <bpmn:outgoing>Flow_Schedule</bpmn:outgoing>
      <bpmn:outgoing>Flow_Reject</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 驳回处理 -->
    <bpmn:userTask id="Activity_Reject" name="变更驳回">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">reject_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_Reject</bpmn:incoming>
      <bpmn:outgoing>Flow_End</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 排期 -->
    <bpmn:userTask id="Activity_Schedule" name="变更排期">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">schedule_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_Schedule</bpmn:incoming>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 实施 -->
    <bpmn:userTask id="Activity_Implement" name="变更实施">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">implement_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:outgoing>Flow_5</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 验证 -->
    <bpmn:userTask id="Activity_Verify" name="变更验证">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">verify_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_5</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 验证结果网关 -->
    <bpmn:exclusiveGateway id="Gateway_VerifyResult" name="验证结果?">
      <bpmn:incoming>Flow_6</bpmn:incoming>
      <bpmn:outgoing>Flow_Close</bpmn:outgoing>
      <bpmn:outgoing>Flow_BackToImplement</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 回退实施 -->
    <bpmn:sequenceFlow id="Flow_BackToImplement" sourceRef="Gateway_VerifyResult" targetRef="Activity_Implement" />

    <!-- 关闭 -->
    <bpmn:userTask id="Activity_Close" name="关闭变更">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">close_change</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_Close</bpmn:incoming>
      <bpmn:outgoing>Flow_End</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 结束事件 -->
    <bpmn:endEvent id="EndEvent_1" name="变更完成">
      <bpmn:incoming>Flow_End</bpmn:incoming>
    </bpmn:endEvent>

    <!-- 连线 -->
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Assessment" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_Assessment" targetRef="Gateway_Approval" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_Approval" targetRef="Activity_CABApproval">
      <bpmn:conditionExpression><![CDATA[${variables['need_approval'] == true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_Schedule" sourceRef="Gateway_Approval" targetRef="Activity_Schedule">
      <bpmn:conditionExpression><![CDATA[${variables['need_approval'] != true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_ApprovalResult" sourceRef="Activity_CABApproval" targetRef="Gateway_ApprovalResult" />
    <bpmn:sequenceFlow id="Flow_Reject" sourceRef="Gateway_ApprovalResult" targetRef="Activity_Reject" />
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Activity_Schedule" targetRef="Activity_Implement" />
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Activity_Implement" targetRef="Activity_Verify" />
    <bpmn:sequenceFlow id="Flow_6" sourceRef="Activity_Verify" targetRef="Gateway_VerifyResult" />
    <bpmn:sequenceFlow id="Flow_Close" sourceRef="Gateway_VerifyResult" targetRef="Activity_Close">
      <bpmn:conditionExpression><![CDATA[${variables['verify_passed'] == true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_End" sourceRef="Activity_Close" targetRef="EndEvent_1" />
    <bpmn:sequenceFlow id="Flow_EndReject" sourceRef="Activity_Reject" targetRef="EndEvent_1" />
  </bpmn:process>
</bpmn:definitions>
```

- [ ] **Step 2: 追加测试**

在 `itsm-backend/service/bpmn_template_service_test.go` 文件末尾追加：

```go

func TestBPMNTemplateService_ListTemplates_IncludesChangeEmergencyFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "change_emergency_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "change_emergency_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急变更流程", found.Name)
	assert.Equal(t, "change", found.Category)
	assert.Equal(t, "emergency", found.SubCategory)
}

func TestBPMNTemplateService_DeployAndStartChangeEmergencyFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_emergency_flow_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Change Emergency Flow Tenant").
		SetCode("change-emergency-flow").
		SetDomain("change-emergency.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	templateSvc := NewBPMNTemplateService(client)
	err = templateSvc.DeployTemplateByName(ctx, "change_emergency_flow", tenant.ID)
	require.NoError(t, err, "change_emergency_flow 模板应该能正常部署")

	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok)

	runCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	instance, err := engine.StartProcess(runCtx, "change_emergency_flow", "CHANGE-EMERGENCY-1", map[string]interface{}{
		"requester_id": float64(1),
	})
	require.NoError(t, err, "change_emergency_flow 应该能成功启动流程实例，不再报'流程定义不存在'")
	assert.NotNil(t, instance)
	assert.Equal(t, "change_emergency_flow", instance.ProcessDefinitionKey)
}
```

- [ ] **Step 3: 运行测试，确认通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestBPMNTemplateService_ListTemplates_IncludesChangeEmergencyFlow|TestBPMNTemplateService_DeployAndStartChangeEmergencyFlow' -v`
Expected：两个测试都 PASS。

- [ ] **Step 4: Commit**

```bash
cd itsm-backend
git add service/bpmn/change_emergency_flow.bpmn service/bpmn_template_service_test.go
git commit -m "feat(bpmn): add missing change_emergency_flow.bpmn

change_service.go has hardcoded this key for emergency changes since
before this change, but no matching .bpmn file existed -- creation
silently failed to enter any workflow (error swallowed in an async
goroutine). Content-equivalent copy of change_normal_flow.bpmn,
matching this session's ticket_urgent_flow precedent: no behavioral
differentiation yet, just closing the missing-file gap."
```

---

### Task 3: 修正 `config/seed/default.json` 的 `ProcessBinding` 种子数据

**Files:**
- Modify: `itsm-backend/config/seed/default.json:111-121`
- Test: `itsm-backend/service/process_resolver_test.go`（新建，这个文件目前不存在）

**Interfaces:**
- Consumes: `ProcessResolver.Resolve`/`ResolveWithPriority`（已存在，`service/process_resolver.go`，本任务不改这个文件本身）、`ProcessBindingService.FindBestBinding`（已存在，`service/bpmn_process_binding_service.go:227`）、`NewProcessBindingService(client) *ProcessBindingService`（已存在）、`NewProcessResolver(client, bindingService) *ProcessResolver`（已存在）。
- Produces：无新增 Go 符号，只改种子数据 JSON + 新建一个测试文件验证修复后的行为。

- [ ] **Step 1: 写测试（先写，此时会失败——种子数据还没修）**

创建 `itsm-backend/service/process_resolver_test.go`：

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func newProcessResolverFixture(t *testing.T) (*ent.Client, *ProcessResolver, *ent.Tenant) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:process_resolver_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Process Resolver Tenant").
		SetCode("process-resolver-tenant").
		SetDomain("process-resolver.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	bindingSvc := NewProcessBindingService(client)
	resolver := NewProcessResolver(client, bindingSvc)
	return client, resolver, tenant
}

// TestProcessResolver_ServiceRequestBinding_MatchesTicketBusinessTypeWithSubType 复现并锁定
// config/seed/default.json 里 ProcessBinding 种子数据的 business_type 修复——种子行必须写成
// business_type="ticket" + business_sub_type="service_request"，跟 FindBestBinding 实际查询
// 方式一致，不能直接写 business_type="service_request"（那样的行永远匹配不上）。
func TestProcessResolver_ServiceRequestBinding_MatchesTicketBusinessTypeWithSubType(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	// 模拟修正后的种子数据形状
	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetBusinessSubType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ticket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err := resolver.Resolve(ctx, ticket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_flow", key, "修正后的种子数据形状应该能被 resolver 正确匹配到，不再落到 ticket_general_flow 兜底")
}

// TestProcessResolver_ServiceRequestBinding_OldSeedShapeNeverMatches 是一条"锁定当前 bug 形状"
// 的对照测试——用旧的（错误的）business_type="service_request" 写法建绑定，证明它确实匹配不上、
// 会落到兜底默认流程。这条测试不是要修的目标，是用来证明"为什么种子数据必须改成上面那条测试
// 的形状"，两条测试合起来才是完整的回归覆盖。
func TestProcessResolver_ServiceRequestBinding_OldSeedShapeNeverMatches(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	// 模拟修正前（当前 config/seed/default.json 里）的错误种子数据形状
	_, err := client.ProcessBinding.Create().
		SetBusinessType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	// 兜底默认绑定：business_type="ticket"，没有 business_sub_type
	_, err = client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetProcessDefinitionKey("ticket_general_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ticket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err := resolver.Resolve(ctx, ticket, "")
	require.NoError(t, err)
	assert.Equal(t, "ticket_general_flow", key, "旧的错误种子数据形状会匹配不上 service_request_flow，落到通用兜底流程")
}
```

- [ ] **Step 2: 运行测试，确认 `TestProcessResolver_ServiceRequestBinding_MatchesTicketBusinessTypeWithSubType` 通过、另一条也通过**

Run: `cd itsm-backend && go test ./service/... -run TestProcessResolver_ServiceRequestBinding -v`
Expected：两条测试目前都应该 PASS——这两条测试测的是 `ProcessResolver`/`FindBestBinding` 的既有查询逻辑本身（这次任务不改这部分 Go 代码），不是测种子数据文件内容。它们的作用是：先在这一步确认 resolver 的匹配语义如预期（正确形状匹配、错误形状不匹配），再在 Step 4 之后人工核对 `config/seed/default.json` 确实改成了"正确形状"那一种——种子数据本身没有专门的加载态测试（`loadSeedConfig` 依赖真实文件路径和进程启动流程，不适合在这条测试里覆盖），所以这一步之后还需要 Step 5 的人工核对。

- [ ] **Step 3: 修正 `config/seed/default.json` 的 `process_bindings` 数组**

把 `itsm-backend/config/seed/default.json` 里现在这一段（大约在 111-121 行）：

```json
  "process_bindings": [
    {"business_type": "ticket", "process_definition_key": "ticket_general_flow", "is_default": true},
    {"business_type": "incident", "process_definition_key": "incident_emergency_flow", "is_default": true},
    {"business_type": "problem", "process_definition_key": "problem_management_flow", "is_default": true},
    {"business_type": "change", "process_definition_key": "change_normal_flow", "is_default": true},
    {"business_type": "service_request", "process_definition_key": "service_request_flow", "is_default": true},
    {"business_type": "release", "process_definition_key": "release_approval_flow", "is_default": true},
    {"business_type": "cloud_public_ops", "process_definition_key": "cloud_public_ops_flow", "is_default": false},
    {"business_type": "cloud_private_ops", "process_definition_key": "cloud_private_ops_flow", "is_default": false},
    {"business_type": "cloud_security_scan", "process_definition_key": "cloud_security_scan_flow", "is_default": false}
  ],
```

改成：

```json
  "process_bindings": [
    {"business_type": "ticket", "process_definition_key": "ticket_general_flow", "is_default": true},
    {"business_type": "ticket", "business_sub_type": "incident", "process_definition_key": "incident_emergency_flow", "is_default": true},
    {"business_type": "ticket", "business_sub_type": "problem", "process_definition_key": "problem_management_flow", "is_default": true},
    {"business_type": "ticket", "business_sub_type": "change", "process_definition_key": "change_normal_flow", "is_default": true},
    {"business_type": "ticket", "business_sub_type": "service_request", "process_definition_key": "service_request_flow", "is_default": true},
    {"business_type": "ticket", "business_sub_type": "release", "process_definition_key": "release_approval_flow", "is_default": true},
    {"business_type": "cloud_public_ops", "process_definition_key": "cloud_public_ops_flow", "is_default": false},
    {"business_type": "cloud_private_ops", "process_definition_key": "cloud_private_ops_flow", "is_default": false},
    {"business_type": "cloud_security_scan", "process_definition_key": "cloud_security_scan_flow", "is_default": false}
  ],
```

（第一行 `ticket_general_flow` 那条本来就是正确的顶层兜底绑定，没有 `business_sub_type`，保持不变；`cloud_*` 三条是完全不同的业务域，不属于这次修复的范围，保持不变。只改 `incident`/`problem`/`change`/`service_request`/`release` 这五条，统一成 `business_type: "ticket"` + `business_sub_type: "<原来的值>"`。）

- [ ] **Step 4: 用一次性脚本验证 JSON 改动本身是合法的、能被 `loadSeedConfig` 正常解析**

Run: `cd itsm-backend && go run -tags migrate main.go 2>&1 | tail -30`（如果本地有可写的测试数据库连接；如果没有现成的测试库，改为运行 `cd itsm-backend && go build ./... && python3 -c "import json; json.load(open('config/seed/default.json'))" && echo "JSON 合法"`，只验证 JSON 语法正确，不需要真的跑一次迁移/种子）

Expected：JSON 解析不报错。（这一步不是这个任务的强制性自动化测试，是给实现者的人工核对提示——`loadSeedConfig` 的完整端到端行为不在这次任务的自动化测试范围内，Step 1-2 的两条测试已经覆盖了 resolver 匹配逻辑本身。）

- [ ] **Step 5: 跑 service 包测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run 'TestProcessResolver' -v`
Expected：全部 PASS。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add config/seed/default.json service/process_resolver_test.go
git commit -m "fix(seed): correct ProcessBinding business_type/business_sub_type shape

ProcessBindingService.FindBestBinding queries business_type='ticket'
+ business_sub_type=<domain>, but the seed data wrote business_type=
<domain> directly with no sub_type -- these rows could never match.
Only the service_request row has an observable behavior change
(service_request_flow.bpmn becomes reachable); incident/problem/change
already bypass this table via hardcoded process keys in their own
services, and release doesn't trigger BPMN at all yet, so those three
rows are a consistency-only fix."
```

---

### Task 4: 服务请求审批迁到 BPMN 原生机制，退休 4 个默认 `ApprovalWorkflow` 种子模板

**Files:**
- Modify: `itsm-backend/service/bpmn/service_request_flow.bpmn:31`
- Create: `itsm-backend/service/bpmn/service_request_urgent_flow.bpmn`
- Modify: `itsm-backend/service/bpmn_template_service.go:127-131`（`listTemplates()` 的 switch）
- Modify: `itsm-backend/service/process_resolver.go`（`ResolveWithPriority`）
- Modify: `itsm-backend/config/seed/default.json:105-109`（删除 `approval_workflows` 数组的全部 4 条）
- Test: `itsm-backend/service/bpmn_template_service_test.go`、`itsm-backend/service/process_resolver_test.go`（都追加）

**Interfaces:**
- Consumes: 全部是已存在的机制（`BPMNTemplateService`/`CustomProcessEngine.StartProcess`/`ProcessResolver`），跟 Task 2、这次会话之前 `ticket_general_flow`→`ticket_urgent_flow` 的模式完全一致。
- Produces：无新增 Go 符号，`ResolveWithPriority` 内部逻辑扩展（不改函数签名）。

**依赖**：这个任务必须在 Task 3 完成之后做——`service_request_flow.bpmn` 打的 `taskPurpose="approval"` 标记要在 `ProcessBinding` 已经能正确解析到这个流程（Task 3 的修复）之后才有实际效果，顺序反过来的话没有编译/测试层面的问题，但打了标记的流程在 Task 3 完成前仍然"够不着"，验证不出真实效果。

- [ ] **Step 1: 给 `service_request_flow.bpmn` 的 `Activity_Approval` 节点加 `taskPurpose="approval"`**

把 `itsm-backend/service/bpmn/service_request_flow.bpmn` 第 31 行：

```xml
    <bpmn:userTask id="Activity_Approval" name="请求审批">
```

改成：

```xml
    <bpmn:userTask id="Activity_Approval" name="请求审批" taskPurpose="approval">
```

- [ ] **Step 2: 新建 `service_request_urgent_flow.bpmn`**

创建 `itsm-backend/service/bpmn/service_request_urgent_flow.bpmn`，是 Step 1 改完后的 `service_request_flow.bpmn` 的结构等价副本（只改 `process id`/`name`、`metaData description`）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 targetNamespace="http://bpmn.io/schema/bpmn">

  <bpmn:process id="service_request_urgent_flow" name="紧急服务请求流程" isExecutable="true">
    <bpmn:extensionElements>
      <bpmn:metaData name="category">service_request</bpmn:metaData>
      <bpmn:metaData name="version">1.0.0</bpmn:metaData>
      <bpmn:metaData name="description">高优先级服务请求处理流程（结构与标准服务请求流程等价，暂无独立超时/升级差异）</bpmn:metaData>
    </bpmn:extensionElements>

    <!-- 开始事件：请求提交 -->
    <bpmn:startEvent id="StartEvent_1" name="请求提交">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>

    <!-- 请求受理 -->
    <bpmn:userTask id="Activity_Accept" name="请求受理">
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 判断是否需要审批 -->
    <bpmn:exclusiveGateway id="Gateway_Approval" name="需要审批?">
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 审批流程 -->
    <bpmn:userTask id="Activity_Approval" name="请求审批" taskPurpose="approval">
      <bpmn:incoming>Flow_3</bpmn:incoming>
      <bpmn:outgoing>Flow_5</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 审批结果 -->
    <bpmn:exclusiveGateway id="Gateway_ApprovalResult" name="审批结果?">
      <bpmn:incoming>Flow_5</bpmn:incoming>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
      <bpmn:outgoing>Flow_Reject</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 驳回通知 -->
    <bpmn:serviceTask id="Activity_RejectNotify" name="驳回通知" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">generic_task</bpmn:metaData>
        <bpmn:metaData name="action">notify_rejection</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_Reject</bpmn:incoming>
      <bpmn:outgoing>Flow_EndReject</bpmn:outgoing>
    </bpmn:serviceTask>

    <!-- 执行服务 -->
    <bpmn:userTask id="Activity_Execute" name="执行服务">
      <bpmn:incoming>Flow_4</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 确认完成 -->
    <bpmn:userTask id="Activity_Confirm" name="用户确认">
      <bpmn:incoming>Flow_6</bpmn:incoming>
      <bpmn:outgoing>Flow_7</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 满意度网关 -->
    <bpmn:exclusiveGateway id="Gateway_Satisfaction" name="满意?">
      <bpmn:incoming>Flow_7</bpmn:incoming>
      <bpmn:outgoing>Flow_8</bpmn:outgoing>
      <bpmn:outgoing>Flow_Feedback</bpmn:outgoing>
    </bpmn:exclusiveGateway>

    <!-- 改进服务 -->
    <bpmn:userTask id="Activity_Feedback" name="改进服务">
      <bpmn:incoming>Flow_Feedback</bpmn:incoming>
      <bpmn:outgoing>Flow_6</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 服务完成 -->
    <bpmn:serviceTask id="Activity_Complete" name="服务完成" implementation="##WebService">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">generic_task</bpmn:metaData>
        <bpmn:metaData name="action">complete_service</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_8</bpmn:incoming>
      <bpmn:outgoing>Flow_End</bpmn:outgoing>
    </bpmn:serviceTask>

    <!-- 结束事件 -->
    <bpmn:endEvent id="EndEvent_1" name="流程结束">
      <bpmn:incoming>Flow_End</bpmn:incoming>
    </bpmn:endEvent>

    <bpmn:endEvent id="EndEvent_Reject" name="请求被拒">
      <bpmn:incoming>Flow_EndReject</bpmn:incoming>
    </bpmn:endEvent>

    <!-- 连线 -->
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Accept" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_Accept" targetRef="Gateway_Approval" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_Approval" targetRef="Activity_Approval">
      <bpmn:conditionExpression><![CDATA[${variables['need_approval'] == true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Gateway_Approval" targetRef="Activity_Execute">
      <bpmn:conditionExpression><![CDATA[${variables['need_approval'] != true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Activity_Approval" targetRef="Gateway_ApprovalResult" />
    <bpmn:sequenceFlow id="Flow_Reject" sourceRef="Gateway_ApprovalResult" targetRef="Activity_RejectNotify">
      <bpmn:conditionExpression><![CDATA[${variables['approval_result'] == 'rejected'}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_EndReject" sourceRef="Activity_RejectNotify" targetRef="EndEvent_Reject" />
    <bpmn:sequenceFlow id="Flow_6" sourceRef="Activity_Execute" targetRef="Activity_Confirm" />
    <bpmn:sequenceFlow id="Flow_7" sourceRef="Activity_Confirm" targetRef="Gateway_Satisfaction" />
    <bpmn:sequenceFlow id="Flow_8" sourceRef="Gateway_Satisfaction" targetRef="Activity_Complete">
      <bpmn:conditionExpression><![CDATA[${variables['satisfied'] == true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_Feedback" sourceRef="Gateway_Satisfaction" targetRef="Activity_Feedback">
      <bpmn:conditionExpression><![CDATA[${variables['satisfied'] != true}]]></bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_End" sourceRef="Activity_Complete" targetRef="EndEvent_1" />
  </bpmn:process>
</bpmn:definitions>
```

- [ ] **Step 3: 给 `bpmn_template_service.go` 补部署清单 case**

在 `itsm-backend/service/bpmn_template_service.go` 的 `listTemplates()` 方法里，`switch key {` 块中 `case "service_request_flow":` 那一段之后（`case "problem_management_flow":` 之前）插入：

```go
		case "service_request_urgent_flow":
			info.Name = "紧急服务请求流程"
			info.Category = "service_request"
			info.Description = "高优先级服务请求处理流程（结构与标准服务请求流程等价，暂无独立超时/升级差异）"
```

- [ ] **Step 4: `ResolveWithPriority` 加优先级特判**

在 `itsm-backend/service/process_resolver.go` 的 `ResolveWithPriority` 方法里，找到这段（现有代码）：

```go
	// 如果是通用工单（没有匹配到特定类型），根据优先级调整
	if processKey == "ticket_general_flow" {
		if ticket.Priority == "high" || ticket.Priority == "urgent" {
			return "ticket_urgent_flow", nil
		}
	}

	return processKey, nil
```

改成：

```go
	// 如果是通用工单（没有匹配到特定类型），根据优先级调整
	if processKey == "ticket_general_flow" {
		if ticket.Priority == "high" || ticket.Priority == "urgent" {
			return "ticket_urgent_flow", nil
		}
	}

	// 服务请求场景同理：高/紧急优先级路由到独立的紧急服务请求流程。这条特判是
	// ProcessBinding.Conditions 机制在工单这条路径上走不到（TriggerProcess 只在
	// ProcessDefinitionKey 为空时才会去查会求值 Conditions 的 ProcessRoutingService，
	// 而这里的 processKey 永远非空）之后选定的替代方案，跟上面 ticket_general_flow
	// 那条保持同样的实现方式，不是发明新机制。
	if processKey == "service_request_flow" {
		if ticket.Priority == "high" || ticket.Priority == "urgent" {
			return "service_request_urgent_flow", nil
		}
	}

	return processKey, nil
```

- [ ] **Step 5: 删除 `config/seed/default.json` 里全部 4 条 `approval_workflows`**

把 `itsm-backend/config/seed/default.json` 里现在的：

```json
  "approval_workflows": [
    {"name": "服务请求审批", "description": "涉及资源、权限或费用的服务请求需要部门负责人审批", "ticket_type": "service_request", "priority": "", "nodes": [{"type": "approval", "name": "部门负责人审批", "approver_type": "role", "role": "dept_manager", "timeout": 480}]},
    {"name": "普通变更审批", "description": "普通变更需要技术负责人和变更经理审批", "ticket_type": "change", "priority": "medium,high", "nodes": [{"type": "approval", "name": "技术负责人审批", "approver_type": "role", "role": "ops_manager", "timeout": 480}, {"type": "approval", "name": "变更经理审批", "approver_type": "role", "role": "change_manager", "timeout": 480}]},
    {"name": "紧急变更审批", "description": "紧急变更采用快速审批并要求事后复盘", "ticket_type": "change", "priority": "urgent", "nodes": [{"type": "approval", "name": "值班经理审批", "approver_type": "role", "role": "ops_manager", "timeout": 60}, {"type": "approval", "name": "安全确认", "approver_type": "role", "role": "security_admin", "timeout": 120}]},
    {"name": "权限申请审批", "description": "高权限申请需要直属负责人和安全管理员审批", "ticket_type": "service_request", "priority": "high", "nodes": [{"type": "approval", "name": "直属负责人审批", "approver_type": "role", "role": "dept_manager", "timeout": 480}, {"type": "approval", "name": "安全管理员审批", "approver_type": "role", "role": "security_admin", "timeout": 480}]}
  ],
```

改成：

```json
  "approval_workflows": [],
```

（不要整个删掉 `"approval_workflows"` 这个 key——`ApprovalWorkflowSeed` 结构体解析这个字段用的是 `[]ApprovalWorkflowSeed`，缺失这个 key 时 JSON 反序列化会让它是 nil，而 `mergeSeedConfig` 对 nil 数组的处理是"保留内置兜底配置的值，不覆盖"（`if override.ApprovalWorkflows != nil { ... }`）——这跟这次的目标相反，这次是要让"default"模板租户上不再有这 4 条记录，所以必须显式写一个空数组 `[]`，不能让这个 key 整个消失。）

- [ ] **Step 6: 写测试**

在 `itsm-backend/service/bpmn_template_service_test.go` 文件末尾追加：

```go

func TestBPMNTemplateService_ListTemplates_IncludesServiceRequestUrgentFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "service_request_urgent_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "service_request_urgent_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急服务请求流程", found.Name)
	assert.Equal(t, "service_request", found.Category)
}

func TestBPMNTemplateService_ServiceRequestFlows_ApprovalNodeMarked(t *testing.T) {
	parser := NewBPMNParser()

	for _, file := range []string{"service_request_flow.bpmn", "service_request_urgent_flow.bpmn"} {
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
```

在 `itsm-backend/service/process_resolver_test.go` 文件末尾追加：

```go

func TestProcessResolver_ResolveWithPriority_ServiceRequestHighPriority_RoutesToUrgentFlow(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetBusinessSubType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	highPriorityTicket := &ent.Ticket{Type: "service_request", Priority: "high", TenantID: tenant.ID}
	key, err := resolver.ResolveWithPriority(ctx, highPriorityTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_urgent_flow", key, "高优先级服务请求应该路由到紧急变体")

	normalPriorityTicket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err = resolver.ResolveWithPriority(ctx, normalPriorityTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_flow", key, "普通优先级服务请求应该保持标准流程，不受这次改动影响")
}

func TestProcessResolver_ResolveWithPriority_TicketGeneralFlow_StillRoutesToUrgentFlow(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetProcessDefinitionKey("ticket_general_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	urgentTicket := &ent.Ticket{Type: "generic", Priority: "urgent", TenantID: tenant.ID}
	key, err := resolver.ResolveWithPriority(ctx, urgentTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "ticket_urgent_flow", key, "既有的 ticket_general_flow 优先级路由回归——本任务只新增一条特判，不能影响这一条")
}
```

- [ ] **Step 7: 运行测试，确认全部通过**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run 'TestBPMNTemplateService|TestProcessResolver' -v`
Expected：全部 PASS，包括新增的和 Task 2、Task 3 已经加过的、以及既有的 `ticket_general_flow`/`ticket_urgent_flow` 相关测试。

- [ ] **Step 8: 跑整个 service 包和全量测试，确认没有回归**

Run: `cd itsm-backend && go test ./service/... -v 2>&1 | tail -150`
Expected：编译通过，重点确认没有既有测试因为 `config/seed/default.json` 的改动或 `ResolveWithPriority` 的扩展而失败。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 9: Commit**

```bash
cd itsm-backend
git add service/bpmn/service_request_flow.bpmn service/bpmn/service_request_urgent_flow.bpmn service/bpmn_template_service.go service/bpmn_template_service_test.go service/process_resolver.go service/process_resolver_test.go config/seed/default.json
git commit -m "feat(bpmn): migrate service-request approval to BPMN, retire dead default ApprovalWorkflow templates

Marks service_request_flow.bpmn's Activity_Approval with
taskPurpose=\"approval\" (now reachable after Task 3's binding fix),
adds a content-equivalent service_request_urgent_flow.bpmn for
priority=high/urgent routing (ResolveWithPriority explicit case,
mirroring the existing ticket_general_flow/ticket_urgent_flow
pattern -- ProcessBinding.Conditions is unreachable on this path, see
the design doc's correction). Clears the 4-entry approval_workflows
seed array: the change/emergency-change templates were pure dead
weight (change never queries ProcessBinding), and service-request/
permission-request are now covered natively by BPMN instead."
```
