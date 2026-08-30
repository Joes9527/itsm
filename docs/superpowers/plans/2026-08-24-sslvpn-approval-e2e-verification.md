# SSL-VPN 服务申请与多级审批端到端场景验证实施计划 (SSL-VPN Approval E2E Verification Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于旧版 ITSM 真实规范（SSL-VPN 远程访问权限申请），构建并运行覆盖「服务目录提单 -> 8项动态自定义字段 -> BPMN 双级串行审批流 (申请人 → 上级领导 → 李昕/L2网络运维) -> 审批移交」的全栈双层端到端验证体系（Layer 1 后端状态机深度断言 + Layer 2 前端 Playwright 真实多角色浏览器走查）。

**Architecture:** 
1. **BPMN 流程层**：定义 `sslvpn_approval_flow.bpmn` 双级串行审批模型，并完成引擎加载与服务目录绑定。
2. **Layer 1 (Backend API/DB Scenario Runner)**：在 `itsm-backend/tests/e2e/` 编写基于真实 REST API 的场景化测试，按照时间步推进工单与流程流转，直查底层数据库表（`tickets`、`custom_field_values`、`bpmn_process_instances`、`bpmn_tasks`、`sla_instances`、`audit_logs`）进行高保真断言。
3. **Layer 2 (Playwright Multi-Persona E2E Suite)**：在 `itsm-frontend/tests/e2e/` 构建真实多角色浏览器走查脚本，模拟申请人填写动态表单、主管审批、李昕技术复审的真实 UI 协同。

**Tech Stack:** Go (Gin, Ent, stretchr/testify), BPMN 2.0 (lib-bpmn-engine), TypeScript (Playwright, Next.js, Ant Design), PostgreSQL.

**Spec:** [`docs/superpowers/specs/2026-08-24-sslvpn-approval-e2e-verification-design.md`](file:///home/administrator/project/itsm/docs/superpowers/specs/2026-08-24-sslvpn-approval-e2e-verification-design.md)

## Global Constraints

- 字段命名契约：HTTP/JSON 交互统一使用 `camelCase`，Ent Schema 与 DB 使用 `snake_case`；严格防范 `custom_field_values` 丢值。
- 审批链条：申请人 (`end_user`) -> 上级领导 (`dept_manager`) -> 李昕/L2网络运维 (`network_eng`) -> 状态置为 `pending_assignment`。
- 范围边界：开通执行、回填交付与结单评价明确排除在本次任务之外。
- 测试独立性与重入性：测试使用唯一单号前缀（如 `VPN-E2E-*`），支持自动清理与独立重入。

---

### Task 1: BPMN 双级审批模型与元数据准备

**Files:**
- Create: `itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn`
- Create: `itsm-backend/tests/fixtures/sslvpn_fixtures.go`
- Test: `itsm-backend/service/bpmn_engine_service_test.go`

**Interfaces:**
- Consumes: `bpmn_engine_service.go`, `ProcessDefinition`
- Produces: `sslvpn_approval_flow` 流程定义，包含 `UserTask_DeptManagerApproval` 与 `UserTask_L2NetworkOpsApproval` 节点；`sslvpn_access_request` 服务目录与 8 项自定义字段定义。

- [ ] **Step 1: 编写 BPMN 流程模型 XML 文件**

在 `itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn` 中定义标准的双级串行审批流程：
```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                  targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="sslvpn_approval_flow" name="SSL-VPN 申请与双级审批流" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="提交申请">
      <bpmn:outgoing>Flow_To_DeptApproval</bpmn:outgoing>
    </bpmn:startEvent>

    <bpmn:userTask id="UserTask_DeptManagerApproval" name="上级领导初审" candidateGroups="dept_manager">
      <bpmn:incoming>Flow_To_DeptApproval</bpmn:incoming>
      <bpmn:outgoing>Flow_To_L2OpsApproval</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:userTask id="UserTask_L2NetworkOpsApproval" name="李昕/L2网络运维技术复审" candidateGroups="network_eng">
      <bpmn:incoming>Flow_To_L2OpsApproval</bpmn:incoming>
      <bpmn:outgoing>Flow_To_End</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:endEvent id="EndEvent_1" name="审批完成移交">
      <bpmn:incoming>Flow_To_End</bpmn:incoming>
    </bpmn:endEvent>

    <bpmn:sequenceFlow id="Flow_To_DeptApproval" sourceRef="StartEvent_1" targetRef="UserTask_DeptManagerApproval"/>
    <bpmn:sequenceFlow id="Flow_To_L2OpsApproval" sourceRef="UserTask_DeptManagerApproval" targetRef="UserTask_L2NetworkOpsApproval"/>
    <bpmn:sequenceFlow id="Flow_To_End" sourceRef="UserTask_L2NetworkOpsApproval" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>
```

- [ ] **Step 2: 编写测试 fixture 助手 (初始化 SSL-VPN 目录项与 8 个自定义字段)**

在 `itsm-backend/tests/fixtures/sslvpn_fixtures.go` 中提供 `EnsureSSLVPNMetadata(client *ent.Client, tenantID int) error`：
- 创建分类 `网络与远程访问服务` (`network_and_remote_access`)；
- 创建服务项 `SSL-VPN 远程办公访问权限申请` (`sslvpn_access_request`) 并绑定 `sslvpn_approval_flow`；
- 创建 8 个自定义字段定义：`applicant_name`, `applicant_upn`, `employee_id`, `department`, `vpn_level`, `target_systems`, `access_duration`, `access_reason`；
- 预置测试账号：`end_user_test` (`end_user`), `supervisor_test` (`dept_manager`), `lixin_test` (`network_eng`)。

- [ ] **Step 3: 运行 BPMN 引擎单测验证模型加载与解析**

Run: `cd itsm-backend && go test -v ./service -run TestBPMNEngine`
Expected: PASS，验证引擎能够成功加载并解析 `sslvpn_approval_flow.bpmn`。

- [ ] **Step 4: Commit Task 1**

```bash
git add itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn itsm-backend/tests/fixtures/
git commit -m "feat(e2e): add sslvpn approval bpmn model and test fixtures"
```

---

### Task 2: Layer 1 - 后端场景自动化集成测试套件

**Files:**
- Create: `itsm-backend/tests/e2e/sslvpn_scenario_test.go`
- Modify: `itsm-backend/tests/e2e/suite_test.go` (if applicable for test harness)

**Interfaces:**
- Consumes: REST APIs (`/api/v1/tickets`, `/api/v1/bpmn/tasks`, `/api/v1/auth/login`), `sslvpn_fixtures.go`
- Produces: 自动化后端场景测试，输出每一步的 DB 表和状态机断言结果。

- [ ] **Step 1: 编写 Step 1 提单与 DB 持久化深度断言**

在 `sslvpn_scenario_test.go` 中实现：
- 使用 `end_user_test` Token 发送 `POST /api/v1/tickets`；
- 断言 HTTP 返回 200/201，工单状态为 `pending_approval`；
- 直接查询 DB `custom_field_values` 表，断言 8 个自定义字段的 `field_key` 和值全部准确保存且无大小写丢值；
- 直接查询 DB `bpmn_process_instances` 表，断言流程实例为 `ACTIVE`；
- 直接查询 DB `bpmn_tasks` 表，断言生成 1 条 `UserTask_DeptManagerApproval` 待办任务。

- [ ] **Step 2: 编写 Step 2 上级领导初审与流程推进断言**

- 使用 `supervisor_test` Token 调用审批接口 `POST /api/v1/bpmn/tasks/:id/complete`；
- 断言原上级任务状态置为 `COMPLETED`；
- 直接查询 DB `bpmn_tasks` 表，断言自动生成第 2 道任务 `UserTask_L2NetworkOpsApproval`，候选组为 `network_eng`；
- 直接查询 DB `audit_logs` 表，断言留存主管审批记录。

- [ ] **Step 3: 编写 Step 3 李昕/L2网络运维技术复审与移交断言**

- 使用 `lixin_test` Token 调用审批接口 `POST /api/v1/bpmn/tasks/:id/complete`；
- 断言第 2 道任务置为 `COMPLETED`；
- 直接查询 DB `bpmn_process_instances` 表，断言流程实例状态流转为 `COMPLETED`；
- 直接查询 DB `tickets` 表，断言工单状态自动更新为 `pending_assignment`（待派单/待帮助台处理），`approval_status` 为 `approved`。

- [ ] **Step 4: 运行后端场景测试并验证全绿**

Run: `cd itsm-backend && go test -v ./tests/e2e -run TestSSLVPNScenarioE2E`
Expected: PASS，打印完整生命周期流转与 8 个字段断言成功日志。

- [ ] **Step 5: Commit Task 2**

```bash
git add itsm-backend/tests/e2e/sslvpn_scenario_test.go
git commit -m "test(e2e): add layer 1 backend scenario deep assertion suite for sslvpn"
```

---

### Task 3: Layer 2 - 前端 Playwright 真实多角色 UI 场景走查套件

**Files:**
- Create: `itsm-frontend/tests/e2e/sslvpn-approval-flow.spec.ts`
- Create: `itsm-frontend/tests/e2e/pages/ServiceCatalogPage.ts` (可选 POM)
- Create: `itsm-frontend/tests/e2e/pages/TaskApprovalPage.ts` (可选 POM)

**Interfaces:**
- Consumes: Playwright Test Runner, 前端 App Router UI 页面 (`/services`, `/tickets/[id]`, `/tasks/todo`)
- Produces: 3 角色浏览器 E2E 走查测试。

- [ ] **Step 1: 编写申请人端（Applicant）提单用例**

在 `itsm-frontend/tests/e2e/sslvpn-approval-flow.spec.ts` 中实现：
- 登录 `end_user_test`；
- 访问服务目录并定位到 `SSL-VPN 远程办公访问权限申请`；
- 校验页面是否正确渲染 8 个自定义输入控件；
- 填充表单（姓名 `侯艾华`、工号 `EMP001`、选择 `Level 2`、目标网段 `10.128.35.0/24` 等）并点击提交；
- 校验跳转后工单详情页右上角状态 Badge 显示为 `待审批`，审批步骤显示 `等待直属上级审批`。

- [ ] **Step 2: 编写上级领导端（Supervisor）待办审批用例**

- 切换 Browser Context 或登出并以 `supervisor_test` 登录；
- 进入待办中心 `/tasks/todo`，定位到该 VPN 申请待办项；
- 打开审批弹窗，断言展示申请人填写的 8 项自定义字段；
- 输入批注“同意申请，出差值班需要”，点击【同意】；
- 断言待办项已完成，工单当前节点更新为 `等待李昕/L2网络运维审批`。

- [ ] **Step 3: 编写李昕/L2网络运维端（L2 Ops）技术复审用例**

- 切换登录为 `lixin_test`；
- 进入待办中心，打开该条 VPN 申请；
- 检查 VPN 权限级别与目标系统，输入批注“网络权限核准通过”，点击【同意】；
- 断言审批完成，工单详情页状态 Badge 实时刷新为 `待分配 / 待处理` (`pending_assignment`)。

- [ ] **Step 4: 运行 Playwright UI 走查测试**

Run: `cd itsm-frontend && npx playwright test tests/e2e/sslvpn-approval-flow.spec.ts --project=chromium`
Expected: PASS，3 角色浏览器交互流全部成功。

- [ ] **Step 5: Commit Task 3**

```bash
git add itsm-frontend/tests/e2e/sslvpn-approval-flow.spec.ts
git commit -m "test(e2e): add layer 2 playwright multi-persona ui walkthrough for sslvpn"
```

---

### Task 4: 双层全链路回归与交付验收报告

**Files:**
- Create: `docs/reports/2026-08-24-sslvpn-approval-e2e-verification-report.md`

**Interfaces:**
- Consumes: Task 1, Task 2, Task 3
- Produces: 详尽的双层端到端执行证据与回归报告。

- [ ] **Step 1: 全量执行 Layer 1 后端场景测试并捕获日志**

Run: `cd itsm-backend && go test -v ./tests/e2e -run TestSSLVPNScenarioE2E`
Verify: 全部断言通过，无字段丢失与状态阻塞。

- [ ] **Step 2: 全量执行 Layer 2 前端 Playwright 浏览器测试并捕获报告**

Run: `cd itsm-frontend && npx playwright test tests/e2e/sslvpn-approval-flow.spec.ts`
Verify: 3 角色 UI 流程全绿。

- [ ] **Step 3: 编写端到端验收总结报告**

在 `docs/reports/2026-08-24-sslvpn-approval-e2e-verification-report.md` 中记录测试用例、执行时间、双层覆盖证据以及对 `custom_field_values` 与 BPMN 引擎的验证结论。

- [ ] **Step 4: Commit Task 4**

```bash
git add docs/reports/2026-08-24-sslvpn-approval-e2e-verification-report.md
git commit -m "docs(report): generate sslvpn e2e verification report"
```
