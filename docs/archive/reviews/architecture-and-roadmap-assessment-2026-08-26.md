# ITSM 架构现状诊断与功能 Roadmap（2026-08-26）

> **[已归档]** 本文档的结论已被 [architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md)
> 核实并订正，请以该文档为准。本文档保留作为历史记录。（本文档即将从 docs/ 移动到 docs/archive/reviews/）

> 评估日期：2026-08-26
> 基线 commit：`e9d5f52c`（HEAD，2026-08-26 18:05 +0800）
> 方法：对 [architecture-assessment-2026-08.md](./architecture-assessment-2026-08.md)、
> [business-completeness-assessment-2026-08.md](./business-completeness-assessment-2026-08.md)
> （均为 2026-08-13 评估，距今 169 次提交）的每一条结论**逐条重新核实**，不直接沿用旧结论。
> 核实方式：3 路独立只读复核（架构治理 / 业务可用性 / 平台扩展性）+ 本人实测（覆盖率、
> controller 文件体积、发布历史），每条结论要求文件:行号级代码证据。
> 视角：先判断架构地基是否稳，再评估业务功能（Ticket/Incident/Problem）能否真正给用户用，
> 最后评估系统能力（自定义表单/流程/审批/权限）能否快速响应新业务场景。

---

## 目录

1. 架构现状诊断
2. 业务功能可用性诊断（Ticket / Incident / Problem）
3. 系统能力响应速度诊断（自定义表单 / 流程 / 审批 / 权限）
4. 分阶段 Roadmap
5. 执行建议
6. 附录：核实方法与实测数据

---

## 一、架构现状诊断

**结论先行**：大方向是对的（BPMN 单引擎设计、RBAC 分层、Ent 租户隔离模型都合理），**但 v1.1
治理阶段没有走完**——旧机制清理了一半，新旧并存造成认知负担和不一致风险。这是业务功能和
扩展能力的地基，建议优先处理。

### 1. 审批机制仍是"多轨并存"，未收敛到单一路径 🔴 P0

| 机制 | 状态 | 证据 |
|---|---|---|
| legacy ApprovalWorkflow/ApprovalRecord | ✅ 已彻底下线 | migration `014_drop_legacy_approval_workflow`（`itsm-backend/migration/migrations.go:78`）；`approval_controller.go`/`approval_service.go` 已从 git 移除 |
| ApprovalChain | 🔴 仍活跃 | `itsm-backend/service/approval_chain_resolver.go:47-55` 仍按租户解析；`/approval-chains` CRUD 仍挂路由（`router/router.go:758-767`） |
| TicketApproval | 🔴 仍活跃 | `itsm-backend/service/ticket_workflow_service.go:373-375,447-516,757-760` 仍查/改 |
| BPMN ProcessApprovalDecision（规范路径） | 🟢 活跃 | `ent/schema/process_approval_decision.go:11-44`；`controller/bpmn_workflow_controller.go:125-135`。唯一符合 CLAUDE.md "不引入第二套审批引擎"原则的路径 |
| change 域自有审批记录/审批链 | ✅ 已收敛（原判断已过期，见下方更新说明） | PR#6 "Track4: 变更审批状态机迁移到 BPMN"，2026-08-25 已合并进 `origin/main`；`handlers/change/service.go` 现状：`SubmitChange` 同步触发 BPMN 且失败即报错，`TransitionStatus` 的 approve/reject 完全交给 BPMN，`GetApprovalHistory` 改读 `ProcessApprovalDecision`，`change_approvals`/`change_approval_chains` 写入路径已删除，`SubmitApproval` 端点已删除 |
| `srIncidentBridge`、`workflow_approval.go` 占位服务 | 🟡 未接线残留 | `internal/bootstrap/app.go:552-554,1105-1118`；`service/workflow_approval.go:60-76` 未接库/未接路由；`controller/workflow_controller.go:336-386` 有审批接口代码但未被 app.go/router.go 接线 |
| 其他残留 | 🟡 | `ent/schema/ticket_type.go:24-25` 仍有 `approval_workflow_id`/`approval_chain` 字段；`docs/docs.go:722,809,1755` 仍宣告旧 `/approval-workflows` |

**核心问题**：不是"还有旧代码"，而是**四条活跃路径同时存在**，新业务场景接入审批时开发者
不知道该走哪条——这是下面"系统能力响应速度"变慢的直接原因。

> **更新（2026-08-26 当晚）**：本节结论核实时，工作分支落后 `origin/main` 24 个未同步的提交而
> 不自知（详见附录新增的"分支同步事故"说明），其中包含已合并的 PR#6（Track4，2026-08-25），
> 完整收敛了 change 域审批。merge 回本分支重新核实后，上表 change 域那一行已更新为"已收敛"，
> 全仓库审批写路径现在只剩 `ApprovalChain`（配置解析，非竞争路径）+ BPMN
> `ProcessApprovalDecision`（唯一权威路径）+ `TicketApproval`（已确认是无法触发的孤儿代码，
> 见第二节 Ticket 审批行）——"四轨并存"已经降为"一条活路径 + 两处需要清理的死代码"，
> 详见同日写的 `docs/superpowers/specs/2026-08-26-approval-single-track-convergence-design.md`
> 顶部的状态更新。

### 2. `handlers/<domain>/` 新分层违反自己的 Repository 边界 🔴 P0

| 文件 | 问题 | 证据 |
|---|---|---|
| `handlers/incident/handler.go` | 直接 `client.(*ent.Client)` 查库；跨域查 SLAViolation | `handler.go:288-293,676-679` |
| `handlers/standard_change/handler.go` | 无 `repository.go`；直接 `StandardChange.Create()`；跨域 `Change.Create()` | `handler.go:16-24,192-205,440-448` |
| `handlers/change/service.go` | 直接 `entClient.Tx()`；跨域查 ConfigurationItem/CIRelationship/Incident | `service.go:243-252,417-460` |
| `handlers/common/service.go` | 同时持有 `repo` 和 `client`；直接查 Tenant/User | `service.go:18-23,96-116,300-306` |
| `handlers/service_request/service.go` | 比旧报告收敛，但仍有直连 | `service.go:122-123,317-319` |

违反 AGENTS.md 分层规则：域包不应绕过自己的 `repository_impl` 直接查库。

### 3. 两套事件总线并行，核心业务生命周期"零发布" 🟠 P1（同时是扩展性最大瓶颈，见第三节）

- `pkg/eventbus.WatermillEventBus`（`pkg/eventbus/eventbus.go:36-44,97-144`）—— 已接入
  bootstrap（`internal/bootstrap/app.go:210-221`），实现 `handlers/shared.EventBus`。
- `service/common/event.EventBus`/`RedisStreamEventBus`（`event_bus.go:46-58`,
  `redis_event_bus.go:23-120`）—— **只有定义，运行时零调用**（`NewInMemoryEventBus`/
  `NewRedisStreamEventBus` 无构造点）。
- 真实发布点全库只有 2 处：`sla.breached`（`service/sla_monitor_service.go:258-267`）、
  `ai.triage.completed`（`handlers/ai/service.go:478-487`）。
- 对 `ticket_service.go`、`ticket_lifecycle_service.go`、`change_service.go`、
  `handlers/incident/` grep `Publish(` = **0**。
- `service/common/event/event.go:54-98,130-138` 定义了 `ticket.created`/`ticket.assigned`/
  `ticket.status.changed`/`approval.completed` 的构造函数，但**只有定义无调用方**。

### 4. 审计闭环"补了一半" 🟠 P1

**已补上**：
- BPMN 流程/任务审计：`service/bpmn_process_engine.go:278-279,400-401` 调
  `RecordProcessStarted/RecordTaskCompleted`；`service/bpmn_audit_service.go:84-125` 真写
  `ProcessAuditLog`。
- SLA breach 审计：`internal/bootstrap/app.go:218-224` 订阅；
  `service/sla_monitor_service.go:256-271` 发布；`service/event_audit_subscriber.go:15-18,55-65`
  写 `AuditLog`。
- AI 只读工具/triage：`handlers/ai/service.go:141-146,173-182,476-489`。

**仍缺口明显**：
- `ticket_workflow_service.go`、`handlers/change/service.go`、`incident_service.go`、
  `connector_controller.go`、`event_webhook_subscriber.go`、`ticket_notification_service.go`
  grep `audit|Audit` 均无匹配。
- ticket 批量删/导入导出无审计：`controller/ticket_controller.go:332-350,575-626`。
- CMDB 批量删/导入导出无审计：`controller/cmdb_controller.go:1339,1586,1680`。
- connector 发送/回调无审计：`controller/connector_controller.go:206-231`；
  `service/event_webhook_subscriber.go:51-84`；`service/bpmn/webhook_handler.go:122-159`。
- incident/change 生命周期无真正 AuditLog：`incident_service.go:1261-1290,1348-1414` 只写
  `IncidentEvent`；`handlers/change/service.go:560-620` 无审计调用。
- AI 高风险写操作未强制审计：`handlers/ai/service.go:340-355` 的 `CreateTicketByAI` 直接返回
  建议；手工 `/ai/audit` 仍在（`handlers/ai/handler.go:361-389`）默认不记录。

### 5. camelCase 契约仍在泄漏，且有扩散 🟠 P1

- `handlers/incident/handler.go:583-590,685-692`：`incident_id`/`occurred_at`/`created_at`
  仍直接进 `gin.H`。
- `handlers/ai/service.go:346-353,359-366`：`suggested_title`/`suggested_category`/`tenant_id`。
- `handlers/ai/handler.go:384-389`：`prompt_version`。
- **新增扩散**：`controller/incident_controller.go:1069-1072,1159-1162,1249-1252` 也直接返回
  `incident_id`——说明这不是自然消退的历史遗留，而是缺少 lint/review 卡口在持续复发。
- 其他 `gin.H` 捷径：`ticket_controller.go:626-628`、`connector_controller.go:231`、
  `workflow_controller.go:343-346`。

### 6. 租户隔离"部分靠父实体守卫，非结构化保证" 🟠 P1

| Schema | 状态 | 证据 |
|---|---|---|
| `prompt_template` | 无 `tenant_id`，孤儿 schema | `ent/schema/prompt_template.go:13-22`；仅 `router/ga_readiness.go:105-106` 全表 count |
| `marketplace_item`/`item_version` | 无 `tenant_id`，列表查询无租户过滤 | `ent/schema/marketplace_item.go:19-108`；`service/marketplace/service.go:41-95` |
| `message` | 无 `tenant_id` | `ent/schema/message.go:14-21`；`handlers/ai/repository_impl.go:103-107` 仅按 conversation_id 查 |
| `knowledge_article_version` | 无 `tenant_id`，靠父实体校验 | `ent/schema/knowledge_article_version.go:17-28`；`service/knowledge_service.go:409-439` 先验证父 KnowledgeArticle 再查子版本——可用但脆弱 |

### 7. 仓库卫生：临时产物、死代码仍未清理干净 🟡 P2

**仍被 git 跟踪的临时产物**：`insert_connector_menu.go`、`itsm-backend/Oops.rej`、
`itsm-backend/dto/workflow_dto.go.disabled`、`itsm-backend/fix_cmdb_import_export.patch`、
`itsm-backend/fixes.patch`、`itsm-backend/service/cmdb_import_export_service.go.orig`、
`itsm-backend/service/configuration_item_service.go.orig`。
（`Oops.orig` 已消失，不再被跟踪——说明清理在零星发生但不彻底）

**死代码面**：
- `handlers/incident/handler.go`+`repository_impl.go`：`internal/bootstrap/app.go:557-559`
  明确注释 "Incident handler has been removed from router config"，但因 `srIncidentBridge`
  （`app.go:552-554`）复用其 Service，整个包被间接保留，Handler/Repository 本身零调用方。
- Problem 域两套"调查"入口并存：`handlers/problem/handler.go`（`router.go:888-909`，含
  `/problems/:id/investigate`）+ `ProblemInvestigationController`（`router.go:1364-1370`）。
- 通知控制器：`controller/simple_notification_controller.go` 存在但 `app.go`/`router.go`
  搜 `SimpleNotificationController` 无匹配——纯死文件（已从旧报告"3个并存"降级为"2活跃+1死"）。
- SLA 计算路径仍多头：`TicketSLAService`（`app.go:277-287`）、`BPMNSLAService`
  （`app.go:488-492`）、`SLAMonitorService`/`EscalationService`（`app.go:671-679,1064-1096`）；
  新增同类死代码 `service/incident_escalation_service.go:17,300`（未见 bootstrap 接线）。

---

## 二、业务功能可用性诊断（Ticket / Incident / Problem）

**核心发现**：状态机后端本身是通的，**真正卡用户的是前后端接线错位或字段缺失**，不是缺功能。
这类问题修复成本低（几小时到 2 天），但用户可感知影响大——是 ROI 最高的一批修复项。

| 流程/联动点 | 状态 | 具体断点 | 证据 | 工作量 |
|---|---|---|---|---|
| Ticket 审批（TicketDetail） | 🟡 部分修复 | 按钮已按权限禁用/启用，但 `TicketApi.approveTicket` 调 `/tickets/workflow/approve` 时缺 `approvalId` 字段，后端要求必填 | `itsm-frontend/src/lib/api/ticket-api.ts:63-76`；`itsm-backend/dto/ticket_workflow_dto.go:143-148`；`components/ticket/TicketDetail.tsx:223-235,261-273,591-613` | 0.5-1天 |
| Ticket 评价 | 🔴 仍断裂 | `TicketRatingSection` 组件已实现但零页面引用；后端接口就绪 | `itsm-frontend/src/components/business/TicketRatingSection.tsx:43`；`itsm-backend/controller/ticket_rating_controller.go:26-77` | 几小时-0.5天 |
| Incident 关闭 | 🟡 部分修复 | 关闭按钮已补，但没传 `closeNotes`，后端强制非空校验必然失败 | `IncidentDetail.tsx:284-295,604-608`；`incident-api.ts:494-500`；`incident_controller.go:700-706` | 1-2小时 |
| Incident → Problem 转换 | ✅ 已修复 | 前后端全链路已打通 | `IncidentDetail.tsx:299-315,610-614`；`incident-api.ts:505-518`；`incident_controller.go:990-1024`；`root_cause_analysis_service.go:181-216` | 无 |
| Incident 升级规则引擎 | 🔴 仍断裂 | `incident_escalation_service.go` 实现存在，全仓库无实例化/调用方；实际跑的是另一套 ticket 升级服务 | `service/incident_escalation_service.go:23,145-168,298-314`；`internal/bootstrap/app.go:1064-1097` | 0.5-2天 |
| Problem → Known Error 发布 | 🟡 部分修复 | 后端路由已就绪（`CreateFromProblem`），但问题详情页无"发布已知错误"入口；KEDB 页是独立 CRUD 不带 problem 上下文 | `router/router.go:902-905`；`handlers/known_error/handler.go:562-589`；`ProblemDetail.tsx:85-159`；`problems/known-errors/page.tsx:182-190` | 0.5-1天 |
| Known Error → Knowledge | 🔴 仍断裂 | 后端也没做出这个 HTTP 能力，不只是前端未接线 | `handlers/known_error/handler.go:591-605`；`router/router.go:1018-1043` 无对应端点 | 1-2天 |
| Ticket → BPMN 审批中心 | 🟡 部分修复 | 审批能力已转移到 `/approvals*` 页面并可用，但 TicketDetail 自身审批入口仍错接；`/my-approvals` 旧路径仍有残余重定向 | `approvals/page.tsx:329-345`；`approvals/pending/page.tsx:196-211`；`workflow-api.ts:458-463`；`bpmn_workflow_controller.go:138-176`；`handlers/auth/azure.go:239-247` | 0.5天 |
| 权限种子（ticket:escalate 等） | ✅ 已修复 | seeder 已补全并被路由消费 | `pkg/seeder/seeder.go:1210-1252,1725-2005`；`router/router.go:554,930,963-964` | 无 |

**一句话结论**：旧报告"最痛的是断线"判断今天仍然成立，但修复进度不均——审批中心和 RBAC
收敛得最好，**TicketDetail 审批调用链、Problem→KEDB 前端入口、Incident 升级规则引擎**是当前
最明显的残余断点。

---

## 三、系统能力响应速度诊断（自定义表单 / 流程 / 审批 / 权限）

关键问题：不只是"功能对不对"，而是"下次业务方提一个新场景，工程师能不能不碰核心代码就接
进去"。

| 能力 | 响应速度 | 结论 | 证据 |
|---|---|---|---|
| 自定义字段 | 🟢 快 | 唯一真正坚实的扩展点：8 种类型、租户隔离、事务化替换、服务端必填校验都到位 | `field_definition_service.go:42-47,87-96`；`field_value_service.go:85-91,183-189`；`ticket_template_service.go:220-228` |
| RBAC 权限 | 🟢 较快 | 最近完成 dual-declaration 收敛、权限种子补全，是本轮复核里进展最扎实的部分 | 见第一节权限种子表 |
| BPMN 流程设计 | 🟡 中等，有硬上限 | 设计→部署→版本→绑定→触发→审批桥接全链路真实，但执行引擎只处理 4 种元素（userTask/endEvent/exclusiveGateway/serviceTask）；scriptTask/parallelGateway/subProcess/boundaryEvent 被解析但不执行 | `bpmn_process_engine.go:663-727`（执行分支）；`bpmn_types.go:29-43` + `bpmn_xml_parser.go:168-224`（解析支持更多类型） |
| 审批 | 🔴 慢，且容易做错 | 四条并行栈同时存在（第一节），新增审批场景时容易接错栈或被迫多处重复实现 | 同第一节 |
| 连接器（飞书/企微/钉钉/Webhook） | 🔴 慢 | 接口契约好（Manifest/Capability/Registry），但能力声明从未被实际派发；核心流硬编码 `"feishu"`；`NotifyTicketUpdate` 空 stub；入站统一 Router 未接线 | `ticket_service.go:282,849,1045,1215,1288,1358,1597,1779`；`ticket_workflow_service.go:915-927`；`connector/router.go:30-96`（仅定义）；`connector_controller.go:317-358`（FeishuCallback 只验签记日志不 Dispatch） |
| AI Skill / Tool | 🔴 慢 | SkillRegistry 零注册（唯一 SLAForecastSkill 绕过注册表硬编码注入）；ToolRegistry 仅 5 个工具且用 `switch` 分发；写工具在 ToolQueue 里单独硬编码 | `skill_registry.go:134-143`；`app.go:378,593`；`tool_registry.go:44-185`；`tool_queue.go:59-98`；marketplace 运行时安装仍是 TODO（`marketplace/service.go:216,247`） |
| 自动化规则 | 🔴 慢 | 只在"创建时"触发（唯一调用点）；条件不支持自定义字段；动作没有 webhook/tag | `ticket_service.go:248-255`（唯一 `ExecuteRulesForTicket` 调用点）；`ticket_automation_rule_service.go:336-350,406-497`；`ent/schema/ticket_automation_rule.go:17-56`（无 triggerType 字段） |
| 事件总线（发布订阅） | 🔴 最大瓶颈 | 基础设施就绪但核心业务生命周期"零发布者"，是上面连接器/自动化规则"响应新业务场景"能力的根因 | 见第一节第 3 条 |
| 多租户新租户配置 | 🟢 已修复（有残留） | CreateTenant 现在会自动种子，多数 seed helper 已参数化；但 default 租户仍是模板克隆源 | `tenant_service.go:79-85`；`pkg/seeder/seeder.go:781-852`；残留：`tenant_provisioner.go:32-35`、`app.go:527-531` 的 `defaultTenantID=1` |

**改动量级排序（从小到大）**：

```
自定义字段配置 < 新租户基线克隆 < 新 BPMN ServiceTask handler / 新只读 AI 工具
< 新出站连接器 < 新写工具 / 新自动化动作
< ticket 生命周期事件化 / 连接器能力派发 / BPMN 新元素执行引擎
```

最后这一档——事件化、能力派发、BPMN 引擎扩展——目前都要改核心代码，这是"响应新业务场景
慢"的真实原因，也是接下来最值得投入的杠杆点。

---

## 四、分阶段 Roadmap

### Phase 1｜当前加固（架构地基 + 用户可感知断点，建议 2-3 周）

原则：先让现有功能可信、可用，再谈扩展。

| 项目 | 目标 | 依赖 | 验收标准 | 评分（业务影响/风险/ITIL关键度/战略对齐/工作量，1-5） |
|---|---|---|---|---|
| 审批机制单轨化 | 全部改为 BPMN `process_approval_decision` 驱动；ApprovalChain/TicketApproval/change 域自有审批降级为只读历史或删除；清理 `ticket_type.go` 残留字段与过期 API 文档 | 无 | 全仓库只有一条审批写路径；集成测试覆盖工单/变更/服务请求审批走 BPMN | 5/5/5/5/4 |
| 业务前端接线修复 | 见第二节表格逐项修复（Ticket审批/评价、Incident关闭payload、Incident升级引擎、Problem→KEDB入口、/my-approvals清理） | 无跨模块依赖，可并行拆给多人 | 每项走一遍真实浏览器路径验证，非 curl 走查 | 5/2/4/3/1（性价比最高） |
| handlers/ 层 repository 边界修复 | incident/standard_change/change/common 补齐 repository.go，DB 访问下沉 | 无 | 各域 handler/service 不再直接持有 `*ent.Client` 做业务查询 | 2/3/2/4/3 |
| 审计补齐 | ticket/incident/change 生命周期、批量操作、connector 动作写审计 | 可先手动埋点，事件总线打通后更彻底 | 关键操作可在审计日志里查到操作人/时间/结果 | 3/5/3/4/2 |
| snake_case 清理 + 仓库卫生 | gin.H 直出改 DTO；删除 .rej/.orig/.patch/.disabled 及死代码 | 无 | grep 关键字段名（如 `incident_id`）在响应体中零命中 | 2/2/1/3/1 |

### Phase 2｜下一能力（打通扩展杠杆，建议 1-2 个月）

目标：把"响应新业务场景需要改核心代码"的项逐个变成"配置/声明式接入"。

| 项目 | 目标 | 依赖 | 验收标准 |
|---|---|---|---|
| 事件总线统一 + 生命周期事件化 | 下线两套并行实现中的一套；CreateTicket/StatusChange/Resolve/Close/Incident 生命周期发布领域事件 | 本阶段其他项的前置依赖 | 连接器/自动化规则可通过订阅事件响应，不需改核心 service |
| 连接器能力派发去硬编码 | 核心流按 Manifest 声明的 Capability 动态选路，取代硬编码 `"feishu"`；`NotifyTicketUpdate` 真正实现；入站 Router 接线 | 事件总线 | 新增连接器渠道无需修改 ticket_service.go |
| BPMN 执行引擎元素扩展 | 支持 parallelGateway / scriptTask / subProcess / boundaryEvent 的真实执行 | 无 | 业务方可建模并行审批/子流程且能真正跑通 |
| Skill/Tool 注册去硬编码 | SkillRegistry 支持声明式 manifest + 热插拔；ToolRegistry 改为注册表驱动而非 switch | 无 | 新增 AI 能力不需要改 tool_registry.go 的 switch |
| 自动化规则增强 | 支持状态变化等多触发时机、自定义字段条件、webhook/tag 动作 | 事件总线 | 业务方可配置"状态变为xx时调用webhook"而不用改代码 |

### Phase 3｜长期方向（沿用现有 roadmap.md 既定方向）

延续 [roadmap.md](./roadmap.md) v2.0/v3.0 主题（服务分解、MSP 计费、AI 全自动分诊、插件市场
运行时安装），**建议不要在 Phase 1/2 的地基问题解决前启动**——尤其是"服务分解"如果在审批
仍然四轨并存、事件总线仍未打通的情况下拆分服务边界，只会把当前的耦合问题分布式化，成本更高。

---

## 五、执行建议

1. **不要跳过 Phase 1 直接做 Phase 2**：事件总线打通、连接器能力派发这些"扩展性"工作，建立
   在一个还没有单一真相来源的审批体系上，会把技术债务焊死在新架构里。
2. **业务前端接线修复可以马上并行做**，跟架构治理没有依赖关系，是本次复核里发现的最高性价比
   任务，可直接拆给不同工程师并行处理。
3. **事件总线是整个可扩展性故事的"钥匙"**：一旦 ticket/incident/change 生命周期事件真正发布
   出去，连接器、webhook、自动化规则这三个当前都要改核心代码的扩展面会同时获得"订阅响应"
   能力，是投入产出比最高的单个架构决策。

---

## 附录：核实方法与实测数据

### 核实范围

- 对 [architecture-assessment-2026-08.md](./architecture-assessment-2026-08.md) 的 P0-1/P0-2/
  P1-1/P1-2/P1-3/P2（临时产物）/P2（死代码面）共 7 类发现逐条重查。
- 对 [business-completeness-assessment-2026-08.md](./business-completeness-assessment-2026-08.md)
  的 Ticket/Incident/Problem 生命周期断点、跨流程联动表、权限种子缺口、平台能力扩展性
  （事件总线/连接器/BPMN元素/AI Skill-Tool/自动化规则/多租户种子）逐条重查。
- 核实基线：`git log --since=2026-08-13` 共 169 次提交，含 RBAC dual-declaration convergence、
  ticket action authorization 统一、portal 假数据清理等直接相关改动。

### 本人实测数据

| 指标 | 数值 | 来源 |
|---|---|---|
| `service/...` 测试覆盖率 | 28.9% | `go test ./service/... -cover`（2026-08-26 实测） |
| `controller/...` 测试覆盖率 | 11.5% | 同上 |
| `handlers/incident` 覆盖率 | 1.3% | `go test ./handlers/... -cover` |
| `handlers/service_catalog` 覆盖率 | 72.0% | 同上 |
| `handlers/standard_change` 覆盖率 | 76.7% | 同上 |
| `cmdb_controller.go` | 1879 行 / 58KB | `wc -l` + `ls -la` |
| `incident_controller.go` | 1502 行 / 46KB | 同上 |
| `ticket_controller.go` | 1128 行 / 36KB | 同上 |
| `bpmn_workflow_controller.go` | 1102 行 / 33KB | 同上 |
| 当前产品版本 | v1.6.8（package.json），日常小版本连续发布至 [Unreleased] | `CHANGELOG.md` |
| 前端类型检查 | 通过（`tsc --noEmit` 零错误） | `npm run type-check` |

### 分支同步事故（2026-08-26 当晚发现，记录方法论教训）

本报告的核实工作全程基于工作分支 `fix/portal-approval-fake-success-and-real-data`（该分支于
2026-08-19 从 `origin/main` 的 `7bef9bd9` 分叉）。分叉之后，`origin/main` 独立合并了 24 个提交
（含 2026-08-25 合并的 PR#6 "Track4: 变更审批状态机迁移到 BPMN"），而本分支自己又累积了 83 个
未同步的提交——两边已经真分叉，不是简单的"落后可以直接 pull"。这导致本报告"审批机制多轨并存"
一节里"change 域自有审批记录/审批链仍活跃"的判断，在核实的那一刻（读分叉点之前的代码）是真实的，
但已经不反映 `origin/main` 的实际状态。

发现方式：当晚讨论"统一 Work Item 模型"重构的多 agent 并行执行方案时，检查本地 git worktree
列表，发现一个命名高度相关的 worktree（`change-approval-bpmn-migration`，分支
`track4-change-approval-bpmn-migration`），核实其 PR 记录后发现已合并但未反映在当前分支。
把 `origin/main` merge 回工作分支（commit `69ab3461`，解决了 2 处迁移编号冲突，`go test ./...`
全绿）后重新核实，确认 Track4 已完整收敛（细节见
`docs/superpowers/specs/2026-08-26-approval-single-track-convergence-design.md` 顶部的状态
更新）。

**教训**：对一个长期存活的工作分支做"现状核实"类的架构评估之前，必须先确认该分支相对
`origin/main`（以及相关的历史 worktree/分支）是否存在未同步的分叉，而不能默认工作分支就是
"当前状态"的权威来源。这条教训比报告本身任何一条具体发现都更值得记住。

### 与 roadmap.md 的对照

仓库现有 [roadmap.md](./roadmap.md)（last synced 2026-06-28）用 v1.0/v1.1/v1.5/v2.0/v3.0 的
季度版本框架规划，但实际产品已经是连续小版本发布模式（v1.6.8+），覆盖率目标（v1.1: 40%）
与实测（~15-29% 视包而定）仍有明显差距，controller 拆分目标（v1.1: 4个大文件）仍是 0/4。
建议下次更新 roadmap.md 时，把"覆盖率里程碑"和"controller 拆分"两项的进度线拉直到与实测
一致，并纳入本报告 Phase 1/2 的条目。

---

## 七、二次核实结果（本轮复核新增，2026-08-26）

> 复核方式：4 路独立只读子代理并行核对本文档一~三节共约 60 条断言的文件:行号证据，逐条判定
> CONFIRMED / PARTIALLY CONFIRMED / NOT CONFIRMED。结论：**约 90% 断言逐字确认**，其余为行号
> 小幅漂移或机制描述细节出入，**零断言被证伪**；有一处（Ticket 审批按钮）实测比原描述更严重。

### 需要修正的证据引用

| 章节 | 原证据 | 修正 |
|---|---|---|
| 一/1 审批 | `internal/bootstrap/app.go:552-554,1105-1118` 作为 `workflow_approval` 占位服务的接线证据 | 这两处实际是 `srIncidentBridge` 的接线代码，与审批无关；真实证据是"全仓库 grep `WorkflowApprovalService`/`WorkflowController` 在 `app.go`/`router.go` 中零实例化"，无具体可指行号 |
| 一/7 死代码 | `srIncidentBridge` 复用的是 `handlers/incident.Service` | 实际复用的是旧包 `service.IncidentService`（legacy 层），与 `handlers/incident` 无关——意味着 `handlers/incident/handler.go`+`repository_impl.go` **可以整体删除**，比"间接保留"更彻底 |
| 一/3 事件总线 | 全仓库仅 2 处真实 `Publish(` | 另有 1 处死代码 `handlers/ticket/aggregate.go:599`（`TicketDomainService`，从未被实例化），不影响"零发布"结论 |
| 二 权限种子 | 路由证据 `router/router.go:930,963-964` | 应为 `router/router.go:554`（`ticket:escalate` 实际校验处），930/963-964 是无关的 change/release 权限 |
| 二 Ticket审批 | 前端调用 `approveTicket` 缺 `approvalId` | 实测更严重：`TicketDetail.tsx` 审批按钮根本未调用 `approveTicket`，而是直接调 `TicketApi.updateTicketStatus(id,'approved')`，**完全绕过 BPMN 审批工作流** |
| 三 自定义字段 | 必填校验证据在 `field_definition_service.go`/`field_value_service.go` | 必填校验实际在 `service/ticket_service.go:447-475`（`validateRequiredFields`） |
| 三 连接器硬编码 | `ticket_workflow_service.go:915-927` | 该区间是 `NotifyTicketUpdate`，无关；真实硬编码在 `:1042`（`normalizeNotifyChannels`）与 `:1140`（`sendConnectorCCNotification` switch） |
| 三 多租户残留 | `tenant_provisioner.go:32-35` 硬编码 `defaultTenantID=1` | 该处硬编码的是字符串 `tenant.CodeEQ("default")`，非整数；整数 `defaultTenantID=1` 只在 `app.go:527` |
| 附录 仓库卫生 | 列出 7 个残留临时文件 | 实测还有 1 个遗漏：`itsm-backend/Oops.rej.orig` |

其余约 50 条断言（审批四轨并存、`handlers/<domain>/` 违反 repository 边界、审计缺口、camelCase
泄漏、租户隔离缺失、BPMN 执行引擎硬上限、Skill/Tool 硬编码、自动化规则限制等）逐字确认，证据
链可信。

---

## 八、P0/P1/P2 具体技术方案（本轮复核新增）

### P0-1 审批机制单轨化

现状：4 条活跃写路径同时存在——`ApprovalChain`、`TicketApproval`、BPMN
`ProcessApprovalDecision`、change 域自有 `ApprovalRecord`（change 已桥接 BPMN 但仍各写一份）。

收敛步骤：
1. change 域：`handlers/change/service.go:584-596` 已把 BPMN 当权威源，下一步把自己的
   `ApprovalRecord` 写入改为"由 BPMN 审批完成事件回填"的只读镜像，停止双写。
2. `TicketApproval`/`ApprovalChain`：新审批一律路由到 BPMN 流程任务；两表降级为存量数据只读
   查询。
3. 删除未接线死代码：`service/workflow_approval.go`、`controller/workflow_controller.go:336-386`。
4. migration 删除 `ent/schema/ticket_type.go:24-25` 的 `approval_workflow_id`/`approval_chain`
   残留字段；清理 `docs/docs.go` 中 3 处过期 `/approval-workflows` swagger 声明。
5. 验收：集成测试覆盖 ticket/change/service_request 审批全部落在 `process_approval_decision`
   表。

### P0-2 handlers/<domain>/ Repository 边界修复

顺序：`standard_change`（零基线，无 repository.go）→ `incident`/`common` → `change`（涉及事务，
最后做）。
- 把直查 `*ent.Client` 的逻辑下沉到各自 `repository.go`/`repository_impl.go`。
- 跨域查询（incident 查 SLAViolation、change 查 CI/CIRelationship/Incident）改为调用对方域已
  暴露的 service 接口，而不是直接查表。
- 验收：`handlers/<domain>/` 下 handler/service 不再直接持有 `*ent.Client` 做业务查询（这也是
  AGENTS.md/CLAUDE.md 已明确的分层规则）。

### P0-3 业务前端接线修复（性价比最高，可并行）

- **Ticket 审批（优先级最高，因为审批被绕过）**：需先补一个"查询当前工单待审批任务
  approvalId"的接口（或在 TicketDetail 加载数据时一并下发），再把按钮改为调用
  `TicketApi.approveTicket`，停止直接调用 `updateTicketStatus`。
- Ticket 评价：把 `TicketRatingSection` 挂载到 TicketDetail（工单关闭后展示）。
- Incident 关闭：前端加必填 `closeNotes` 输入框；同时给后端 DTO 加 `binding:"required"` 做
  前置拦截（当前校验只在 service 层）。
- Problem→KEDB：`ProblemDetail.tsx` 加"发布已知错误"入口，调用已就绪的 `CreateFromProblem`。
- Known Error→Knowledge：需先补后端接口（`handlers/known_error/handler.go` 新增
  convert-to-knowledge handler+route），非纯前端任务。
- 清理 `handlers/auth/azure.go:246` 指向不存在页面的 `/my-approvals` 死重定向，改指向
  `/approvals`。

### P1-1 事件总线统一 + 生命周期事件化

下线 `service/common/event`（零调用），统一用已接入 bootstrap 的 `pkg/eventbus.WatermillEventBus`。
在 `ticket_service`/`change_service`/`incident_service` 的创建/状态变更/关闭/分派方法中插入
`Publish`（goroutine + 失败日志，不阻塞主流程）。这是审计补齐、连接器解耦、自动化规则多触发点
的共同前置依赖。顺带清理 `handlers/ticket/aggregate.go:599` 从未实例化的 `TicketDomainService`
死代码。

### P1-2 审计闭环补齐

短期手动埋点：`ticket_workflow_service`（审批通过/拒绝）、`handlers/change/service.go`（状态
流转）、`incident_service.go`（ack/close）补 `AuditLog.Create`；ticket/CMDB 批量删除导入导出、
connector 收发/webhook 补显式审计调用；`CreateTicketByAI` 等高风险写操作默认记录审计，不依赖
手工 `/ai/audit`。中期：事件总线打通后用 `EventAuditSubscriber` 模式统一订阅生命周期事件自动
写审计。

### P1-3 camelCase 契约泄漏治理

把列出的 `gin.H` 字面量（`handlers/incident/handler.go`、`handlers/ai/service.go`、
`handlers/ai/handler.go`、`controller/incident_controller.go` 新增 3 处等）替换为 DTO 结构体；
加一个 CI 脚本 grep 响应体里的 snake_case 字段名模式作为 pre-commit/CI gate，防止"持续复发"
（文档已指出这不是自然消退的历史遗留）。

### P1-4 租户隔离补齐

`prompt_template`、`marketplace_item`/`item_version`、`message` 补 `tenant_id` 字段 + migration
+ 服务层过滤。`knowledge_article_version` 现状（靠父实体校验）可用但脆弱，列为 P2 优化，视情况
加冗余 `tenant_id`。

### P2 仓库卫生 + Phase 2 扩展性

- 删除已确认死透的临时文件（含遗漏的 `Oops.rej.orig`）；整体删除
  `handlers/incident/handler.go`+`repository_impl.go`（`srIncidentBridge` 实际依赖旧
  `service.IncidentService`，与该包无关）；删除 `simple_notification_controller.go`；Problem
  Investigation 两入口二选一；`incident_escalation_service.go` 明确产品诉求后决定接入
  bootstrap 或删除。
- Phase 2（依赖事件总线先打通）：BPMN 引擎补齐 `scriptTask`/`parallelGateway`/`subProcess`/
  `boundaryEvent` 真实执行；连接器核心流按 capability 动态选路取代硬编码 `"feishu"`，
  `FeishuCallback` 真正调用 `connector/router.go` 的 `Dispatch`；`SkillRegistry`/`ToolRegistry`
  改声明式注册取代 switch；自动化规则 schema 加 `trigger_type` 字段支持多触发点、条件支持自定义
  字段、动作支持 webhook/tag；`app.go:527` 的 `defaultTenantID=1` 改为按需对每个新租户触发模板
  部署。
