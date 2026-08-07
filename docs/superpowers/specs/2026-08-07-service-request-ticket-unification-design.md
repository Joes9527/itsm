# 服务请求（ServiceRequest）与工单（Ticket）统一 - 设计文档

## 背景与问题

当前 `handlers/service_request` 是一个完全独立于 `handlers/ticket`/`TicketService` 的业务域：自己的状态机、自己的硬编码三级审批（manager→IT→security，超时小时数写死）、自己的工作流触发逻辑——完全不经过 `ProcessTriggerService`/BPMN 引擎。这与工单域已经在用的 BPMN 触发路径（`TicketService.processTriggerSvc.TriggerProcess`）是两套并行的执行机制。

产品定位上，"服务请求"（Service Request）在 ITIL 语境里本质是"用户从服务目录发起的一种特定来源的工单"，不应该拥有一套独立的状态机/审批/工作流实现。这次重构的目标是让 ServiceRequest 委托给 Ticket 来承担状态、工作流、审批，消除"两套引擎"的问题。

**本设计的范围边界**：只做 ServiceRequest → Ticket 的委托关系重构。审批本身"收敛到 BPMN 统一执行"（覆盖 ticket/change 域、多级审批建模等）是另一个更大的子项目，留到下一轮单独 brainstorm，不在本设计范围内。本设计只要求：SR 不再有自己独立的状态机和审批实现，其审批读写委托给 ticket 当前已有的机制（单节点 BPMN 审批）——三级审批的精确建模是下一轮的工作。

## 现状核实（代码走查，2026-08-07）

- **两套执行机制并存**：`TicketService`/`ProblemService`/`IncidentService`/`ChangeService` 都持有 `ProcessTriggerServiceInterface` 并在创建时调用 `TriggerProcess`（`service/ticket_service.go:555-587` 等），走 BPMN 引擎（`service.NewCustomProcessEngine`，`internal/container/container.go` 里注入的自研引擎，注意**不是** `nitram509/lib-bpmn-engine`——后者在 `pkg/bpmn/engine_adapter.go` 里零调用方，是死代码）。`handlers/service_request/service.go` 完全没有调用 `ProcessTriggerService`，是硬编码的 `{1,Manager,24h},{2,IT,48h},{3,Security,72h}` 三步。
- **产品架构文档**（`prd/ITSM产品模块架构设计.md` 2.5 节）把"服务目录与服务请求"列为独立于"工单管理"（2.1 节）的模块，服务请求额外带有 `ProvisioningTask`（资源交付）这个工单没有的概念——这部分职责应该保留在 SR 侧，不下沉进 Ticket。
- **代码库现有先例**：Incident → Problem 的"转换"（`service/root_cause_analysis_service.go:CreateProblemFromIncident`）不是把 Incident 行改成 Problem 行，而是创建独立的 Problem 记录、用 `LinkProblemIncident` 显式关联——两个实体各自独立存在。本设计的"SR 委托给 Ticket"沿用同样的模式：SR 保留独立实体，通过 `ticket_id` 外键关联，而不是合并成一张表。
- **本地 `main` 与 `origin/main`（用户 fork）存在分叉**：`origin/main`（commit `77c3cc12`）已经合并了这次会话早前的 `feat/dynamic-custom-fields` 分支（含 Task 10 对 `controller/service_controller.go`、`service/service_request_service.go` 这套更早的遗留实现的删除）。本地 `main` 尚未同步，尝试合并时在 `ticket_controller.go`、`ticket_service.go`、`ticket_dto.go`、`mappers.go`、`TicketDetail.tsx`、`tickets/create/page.tsx`、`tickets/templates/page.tsx` 等文件上有真实冲突。**这是本设计能落地的前置条件**，需要在进入实施前解决，具体合并方式留到实施阶段处理（不在本设计范围内展开）。
- **当前自定义字段归属**：`field_definitions`（`entity_type="service_catalog"`，字段定义）与 `field_values`（`entity_type="service_request"`，字段值，`entity_id=ServiceRequest.ID`）是这次会话早前刚建好的机制。本设计会改变字段值的归属实体（见下）。

## 目标架构

```
ServiceRequest.Create()
        │
        ▼
（同一事务）先创建 Ticket
  title = 服务名, description = 申请理由,
  requester_id = 申请人, source = "service_catalog"
        │
        ├──▶ Ticket 创建照常触发 ProcessTriggerService
        │     → BPMN 引擎 → ticket_general_flow.bpmn
        │     （单节点"工单审批"，走已有机制，本设计不改）
        │
        ▼
再创建 ServiceRequest 行，携带 ticket_id 引用
  （catalog_id / cost_center / data_classification /
   source_ip_whitelist / expire_at / compliance_ack /
   needs_public_ip / ci_id 这些 SR 专属字段仍在 SR 表上）
        │
        ▼
状态/审批/工作流查询：SR 详情读取都通过 ticket_id 联查 Ticket，
不再有 SR 自己的 status / current_level / total_levels 字段
```

## 数据模型变更

### `ent.Ticket` 新增

```go
field.String("source").Default("manual").Optional()
  // "manual" | "service_catalog"，标记该工单是否来自服务目录申请
```

### `ent.ServiceRequest`（瘦身）

保留：
```go
field.Int("ticket_id")  // FK → Ticket，唯一，必填，1:1
field.Int("catalog_id")
field.String("cost_center").Optional()
field.String("data_classification")
field.JSON("source_ip_whitelist", []string{}).Optional()
field.Time("expire_at").Optional()
field.Bool("compliance_ack")
field.Bool("needs_public_ip")
field.Int("ci_id").Optional()
```

删除：`status`、`title`、`reason`、`current_level`、`total_levels`（全部改为读关联 Ticket 的对应字段）

### ProvisioningTask 集成点（评审补充）

**现状核实**：`POST /service-requests/:id/provision`（`ProvisioningController.StartProvisioning` → `ProvisioningService.CreateTaskFromServiceRequest`）是**人工触发**的操作（agent 点"开始交付"按钮），不是审批通过自动触发。当前实现直接查 `ent.ServiceRequest.Status`，硬编码检查 `!= "security_approved"` 则拒绝；创建 `ProvisioningTask` 成功后，同一事务里把 `ServiceRequest.Status` 回写为 `"provisioning"`。SR 瘦身删除 `status` 字段后，这两处都要改：

1. **前置条件检查**：把 `sr.Status == "security_approved"` 改为查询 `process_approval_decision` 表——`WHERE business_type='ticket' AND business_id=<关联的ticket.ID> AND decision='approved'` 存在即视为"已批准，允许启动交付"（`business_type`/`business_id` 是 BPMN 引擎在 `recordApprovalDecision` 时从 `ProcessTriggerRequest.BusinessType`/`BusinessID` 带过去的，ticket 触发时已经是 `"ticket"`/`ticket.ID`，可以直接查，不需要新增字段或旁路）。
2. **状态回写**：不再需要 `ServiceRequest.Status = "provisioning"` 这个动作——SR 已经没有自己的 status 字段。"是否已在交付/已交付"这个信息改为直接从 `ProvisioningTask` 本身派生（查该 SR 关联的 `ProvisioningTask` 是否存在、及其 `status` 字段），前端/API 需要展示"交付状态"时改查 `ProvisioningTask`，不再依赖 SR 的状态快照。
3. **`StartProvisioning` 保持人工触发不变**——这次重构不新增"审批通过自动开始交付"的自动化逻辑，只是把现有的人工触发链路的判断依据从 SR 状态改成 ticket 审批决策记录，行为对用户可见部分不变。

### `ent.ServiceRequestApproval` —— 整体删除

过渡期审批记录就是 Ticket 走 BPMN 产生的 `process_approval_decision`，不再有 SR 专属审批记录表。三级审批的精确建模（如果要保留独立于 ticket 通用审批的三级语义）留到下一轮"审批收敛"设计。

### 自定义字段值归属迁移

`field_values` 里 SR 提交的值，`entity_type` 从 `"service_request"` 改为 `"ticket"`，`entity_id` 从 `ServiceRequest.ID` 改为关联 `Ticket.ID`——这样工单详情页能像其他自定义字段一样直接展示这些值，不需要为 SR 单独查询。目录侧的字段**定义**（`entity_type="service_catalog"`，挂在 `ServiceCatalog.ID` 下）不变。

## API 与前端

**后端 API**：`/api/v1/service-requests` 系列端点保留（产品架构文档明确列出这套 API 面），内部实现改为 Ticket + ServiceRequest 联查组装，不再是权威数据源本身。创建接口返回体带上 `ticketId`。

`ServiceRequestApproval` 表删除后，原本专门查/提审批记录的端点（`GET/POST /service-requests/:id/approvals`、`GET /service-requests/approvals/pending`）不再有自己的数据源——改为内部转发到 ticket 域已有的审批查询/提交接口（按 `ticket_id` 查），响应体保持字段名不变以维持前端契约，具体是"路由层转发"还是"直接改前端调用 ticket 的接口"留给实施阶段按改动量选择，但端点本身的语义（"查/提这个 SR 的审批"）不变。

**前端页面**：
- 退休两个独立的 SR 详情页（`/service-requests/[id]` 用的 `ServiceRequestDetail.tsx`、`/my-requests/[requestId]` 自己的 `Descriptions` 实现）——这两处此前分别维护自定义字段展示代码，是重复实现。统一跳转/复用 `/tickets/:ticketId`，该页面在 `ticket.source === "service_catalog"` 时额外渲染一个"服务申请信息"面板（cost center / compliance / 交付状态等 SR 专属字段）。
- 提交页（`service-catalog/request/[id]/page.tsx`）UX 不变，提交成功后跳转目标改为 `/tickets/:ticketId`。
- `/my-requests` 列表页保留，作为"按 `source=service_catalog` 过滤 + 我的申请"这个视角的入口，点进详情跳 `/tickets/:ticketId`。

## 实施前提条件

1. **本地 `main` 与 `origin/main` 同步**：本设计直接在 ticket 域上改动，必须基于收敛后的代码（含 `feat/dynamic-custom-fields` 分支已经merge的内容），不能在分叉的本地 main 上另起一套。这是硬性前提，具体合并方式（重新走一遍冲突解决 / 直接以 origin/main 为准覆盖本地 / 其他方式）留到进入实施计划阶段确定。
2. **数据**：当前 ServiceRequest/ServiceRequestApproval 相关数据是本次会话开发过程中产生的测试数据，无需保留迁移，可直接结构变更。

## 实施顺序

**步骤 1-3（schema 改动 + handler/service 重写）必须在同一次提交里落地，不能分批合入**——第 1-2 步做完、第 3 步没做完之前，代码编译不过（旧 handler/service 还在引用被删的 `ServiceRequestApproval`/`current_level`/`total_levels`/`sr.Status`）。写实施计划时这几步要合成一个任务，不能拆成独立可合并的小任务。

1. ent schema 改动：`Ticket` 加 `source`，`ServiceRequest` 瘦身 + 加 `ticket_id`，`ServiceRequestApproval` 删除
2. `go generate ./ent`
3. 后端重写（同一提交内完成）：
   - `handlers/service_request/service.go`：`Create` 改为先建 Ticket（含触发 `ProcessTriggerService`）再建 SR；`Get`/`List` 改为联查组装；删除三级审批硬编码逻辑
   - `service/provisioning_service.go`：`CreateTaskFromServiceRequest` 的前置检查从 `sr.Status=="security_approved"` 改为查 `process_approval_decision`；删除对 `ServiceRequest.Status` 的回写
4. 迁移 SQL：新增/删除相应列和表
5. 自定义字段值归属迁移：`extractServiceRequestFieldValues` 写入路径的 `entity_type`/`entity_id` 改为指向 ticket
6. 前端：退休两个独立详情页，`/tickets/[id]` 页面加 SR 面板；提交页跳转目标调整；交付状态展示改为查 `ProvisioningTask`
7. 全量测试 + 手动验证主链路

## 测试计划

- SR 创建后能查到关联 Ticket，且 Ticket 确实触发了 BPMN 流程（`process_instance` 有记录）
- 自定义字段值查询命中 `entity_type=ticket, entity_id=ticket.ID`，旧的 `entity_type=service_request` 路径不再产生新数据
- `/my-requests` 过滤视图返回正确的 `source=service_catalog` 工单列表
- ticket 详情页在 `source=service_catalog` 时正确渲染 SR 专属面板，非该来源时不渲染
- 明确写一条测试断言"三级审批已退化为单节点 BPMN 审批"——证明这是设计内的已知过渡行为，不是遗漏
- `StartProvisioning` 在关联 ticket 没有 `process_approval_decision(decision='approved')` 记录时拒绝启动交付；有记录时正常创建 `ProvisioningTask`（覆盖评审补充的集成点）

## 非目标（本设计不做）

- 审批统一收敛到 BPMN（三级顺序审批的精确建模、change CAB 会签、legacy `approval_controller`/`approval_chain_controller` 清理）——下一轮单独 brainstorm
- `ProvisioningTask`（资源交付）相关逻辑变更——本设计不涉及，保留在 SR 侧不变
- incident/problem 审批能力——本设计不涉及
