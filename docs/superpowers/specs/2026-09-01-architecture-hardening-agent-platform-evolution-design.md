# ITSM 架构可信化、Agent 平台化与规模化演进总设计

> 状态：已处理首轮书面 review，待复审确认
> 日期：2026-09-01
> 代码基线：`f11290317499b958ba93d85689286fdccccfe697`
> 基线身份：该 commit 是 2026-09-01 的远端 `origin/main`，不是待合并功能分支；本地 `main` 仍停在其祖先 `fda84251`，因为主 worktree 有其他 Agent 的未提交改动而未执行 fast-forward
> 依据：`AGENTS.md`、`f1129031` 当前代码复核、既有 WorkItem/BPMN/KAF 设计与验收材料，以及 2026-09-01 架构评估草稿中的待核查结论

## 一、目的与文档定位

本文定义一份覆盖 P0、P1、P2 的整体演进蓝图，把当前 ITSM 从“核心能力可用、局部可靠性已验证”推进到以下目标状态：

1. WorkItem、SLA、编号、流程授权和任务生命周期具备单一权威实现。
2. 租户隔离、审计、异步投递和失败恢复由统一平台能力强制执行。
3. KAF、Connector、AI Tool 和后续 Agent 场景通过同一 capability、权限、风险、审计和执行边界扩展。
4. SaaS、SaaS + MSP、多区域和数据驻留不分叉核心领域模型。

本文是治理总设计，不是一份覆盖全部代码的超大实施计划。每个子项目必须拥有独立 spec、implementation plan、worktree、review 和验收证据。不同子项目可以并行开发，但生产能力的启用必须通过依赖门禁。

本文在 `f1129031` 上重新核实并取代 [2026-08-30-architecture-assessment-remediation-execution-plan-design.md](./2026-08-30-architecture-assessment-remediation-execution-plan-design.md) 中已经过期的总体排期判断。仍然有效的 WorkItem、BPMN、KAF 领域设计继续作为子项目输入，不重复建立第二套领域合同。

## 二、已核实的当前基线

| 主题 | `f1129031` 代码事实 | 本设计处理方式 |
|---|---|---|
| WorkItem/SLA | Incident、Problem、Change 会创建 WorkItem，但没有统一 SLA 绑定，扩展表仍重复保存公共字段，后续状态可能与 WorkItem 不一致 | Phase 1 物理收口为 WorkItem 唯一权威 |
| `ticket_number` | `tickets.ticket_number` 全局唯一，但 Ticket/Incident、Problem、Change 存在不同计数维度和不同生成器 | Phase 1 改为租户内唯一并只保留一个分配器 |
| Ticket 审批 | `TicketWorkflowService.ApproveTicket` 先在 legacy transaction 外桥接并推进 BPMN，随后另开事务维护 `TicketApproval` 状态/委派记录，并按自己的 pending count 直接改写 `tickets.status`；前端提交仍携带 `approvalId` | Phase 1 删除 TicketApproval 运行态和 Ticket 专用 bridge，审批只操作 ProcessTask/ProcessApprovalDecision |
| BPMN 授权 | 主 ProcessInstance/Task API 已接入 typed access scope 和 participation policy；process-trigger status、dashboard/audit 等入口仍缺同租户对象授权 | Phase 1 完成所有入口收口 |
| BPMN ServiceTask dispatch | 未注册 handler 已返回显式错误，不再是 NoOp | 不重复修复；后续只治理“handler 返回成功但未产生声明效果” |
| BPMN callback | 已有 durable callback outbox，但 Generic/CC/通知 handler 仍可能在目标缺失、接收人为空或配置不支持时返回成功 | Phase 1 建立效果语义；Phase 2 迁入统一 Outbox/Worker |
| RLS | Web 启动已安装可配置 driver，当前 driver 只观测并透传；真正设置 tenant session 的连接路径未接入 | Phase 2 用唯一 RLS-aware 数据路径替换现有 skeleton |
| Audit | `AuditMiddleware` 已定义但未挂载；部分领域直接写 `AuditLog`，BPMN 又有自己的审计服务 | Phase 2 收敛为统一 AuditService，区分 request audit 与业务审计 |
| Outbox | 通用 `outbox_events` 的生产写入主要是 KAF；BPMN callback 使用另一套持久 outbox；其他领域仍有进程内发布 | Phase 2 一次性迁移到统一 envelope、claim、retry、DLQ 和 replay 平台 |
| KAF | lease、fencing、幂等、replay-only 和 monotonic receipt 已验证；仍是 controlled Dev、单 SSLVPN 场景，真实跨进程和真实 RLS 证据缺失 | Phase 2 完成生产准入，Phase 3 才扩场景 |
| 前端菜单 | 本地主 worktree 有后端菜单唯一权威的未提交改动，不属于 `f1129031` | 独立合入并补 fail-closed 回归测试，不混入本设计核心分支 |

## 三、不可变架构约束

后续子设计和实施计划不得重新选择或弱化以下约束：

1. `itsm-backend` 是领域规则、权限、租户、工作流、审计和 API 合同的唯一事实源。
2. `tickets` 是第一阶段 WorkItem 基表；公共字段只在 WorkItem 读取和写入。
3. Incident、Problem、Change Request、Requested Item 等专业 Service 继续拥有自己的状态机，不建立按 `recordClass` 分派所有专业规则的巨型 Service。
4. BPMN 是审批、履约、自动化和人工介入的唯一编排层，不创建第二套审批或任务推进引擎。
5. 菜单、角色名、tenant 过滤只能作为入口条件，不能替代对象级授权。
6. AI/Agent 只能给出结构化判断或请求 capability；代码负责权限、风险门、状态机、事务、外部调用和审计。
7. 未知 capability、handler、event type 或 dispatch target 必须 fail closed。
8. 一个业务概念只保留一个权威实现。新路径切换时删除旧 service、旧 generator、旧 DTO、旧表、旧 worker、旧 fallback 和旧调用点。
9. 不接受长期 dual read、dual write、deprecated alias、bridge service 或 patch-on-patch。短期数据处理步骤不能演变为运行时兼容路径。
10. 每个子项目都必须声明 `owns`、`depends_on`、`deletes` 和 `evidence`。

## 四、目标架构

```text
Next.js / API / CLI / External Agent
                 │
                 ▼
      Authentication + RBAC + Tenant/MSP Scope
                 │
       ┌─────────┴─────────┐
       ▼                   ▼
Professional Domain       BPMN Orchestration
Services                   Access/Lifecycle/Effect Policy
       │                   │
       └─────────┬─────────┘
                 ▼
       Shared WorkItem Application Capabilities
       Number + SLA + Common State + Audit + Outbox
                 │
                 ▼
       RLS-aware Unit of Work / PostgreSQL
                 │
                 ▼
       Unified Outbox / Worker / Handler Registry
                 │
       ┌─────────┼───────────┐
       ▼         ▼           ▼
   Domain Event  BPMN       Capability Provider
                Callback    KAF/Connector/Tool
```

总体演进分为三个 Phase：

| Phase | 核心目标 | 退出结果 |
|---|---|---|
| Phase 1：可信核心 | 消除数据、授权、审批双轨、生命周期和 callback 语义风险 | WorkItem、编号、SLA、Ticket 审批、BPMN 授权和效果语义只有一条权威路径 |
| Phase 2：可靠执行平台 | 建立强制租户隔离、业务审计和统一可靠执行底座 | RLS、Audit、Outbox/Worker 和 KAF 生产准入有真实运行证据 |
| Phase 3：生态与规模化 | 统一 Agent/Connector/Tool 扩展，并支持 MSP、多区域和灾备 | 新能力通过 registry 扩展，不修改核心 switch，不分叉领域模型 |

实现可以跨 Phase 并行；能力发布必须按依赖门禁阻断。

## 五、Phase 1：可信核心

### 5.1 WorkItem、编号与 SLA

专业域创建使用同一事务边界：

```text
专业域校验
  -> 开启事务
  -> NumberAllocator
  -> SLAPolicyBinder
  -> WorkItemCreator
  -> 专业扩展记录
  -> 提交
```

架构决策：

- WorkItem 创建能力必须接受调用方事务，不能让专业域调用会自行提交的 `TicketService.CreateTicket`。
- `NumberAllocator` 是唯一编号分配器。编号按 tenant + period 由 PostgreSQL 原子序列表分配；Redis 不再作为编号事实源。
- `tickets` 唯一约束改为 `(tenant_id, ticket_number)`。人类编号只要求租户内唯一；内部关联、幂等和跨系统引用使用全局 ID/UUID。
- `SLAPolicyBinder` 按 tenant、recordClass、priority、category/catalog metadata 选择策略，不在专业域硬编码。
- SLA 必需但未匹配时创建失败；配置明确 optional 时记录 `not_applicable`、审计和指标，不允许静默缺失。
- title、description、status、priority、requester、assignee、tenant、公共时间戳和 SLA 只保存在 WorkItem。
- 专业状态机继续验证专业转换，但最终通过共享 WorkItem 状态写入口修改公共状态。
- Incident、Problem、Change、Requested Item 接入后删除各自编号生成器、公共字段写入和重复字段定义。
- P1-B 必须先生成并核对现有 `TicketService.CreateTicket` 调用方清单，至少覆盖普通/快速创建 Controller、MS Graph 邮件入口、Service Request、Tool Queue 和 Service 内部创建；调用方要么显式拥有 transaction，要么调用由 WorkItem application service 管理 transaction 的唯一顶层入口，不能保留两个 `CreateTicket` 语义。
- Phase 1 负责领域数据与流程语义的单轨化；统一业务 Audit 和事务 Outbox 由并行推进的 P2-B/P2-C 接入。接入前不添加临时审计 helper、临时 event publisher 或空实现 port。

### 5.2 BPMN 授权、生命周期与效果语义

所有 BPMN HTTP 入口统一构造 `BPMNAccessScope`：

```text
authenticated actor + tenant + endpoint permissions
  -> BPMNAccessScope
  -> tenant predicate
  -> participant/read-all/update-all policy
  -> lifecycle policy
  -> read or mutation
```

架构决策：

- process-trigger status/cancel/suspend/resume、dashboard、audit timeline 和所有 alias route 必须经过相同对象授权，不再用 legacy role gate 代替 participation policy。
- `TaskLifecyclePolicy` 集中定义 assign、claim、complete、cancel、delegate 和 variable mutation 的允许源状态。
- completed、cancelled 等终态禁止重新分派、取消或修改变量。
- callback 结果使用 `applied`、`idempotent`、`skipped_optional`、`blocked` 四种明确语义，替代模糊的 `Success bool`。
- `skipped_optional` 只允许流程定义预先声明 optional，并必须产生审计、warning 和 metric。
- 目标缺失、接收人为空、未知 CC 类型、不支持的变量占位符进入 `blocked`，不能返回普通成功。
- 通知 API 只保留 `eventType` 合同，删除前端 `type/channel` 请求模型及相关测试夹具。
- Phase 1 保持当前 durable callback 事实不退化；Phase 2 完成统一平台迁移后删除其专用调度实现。

### 5.3 Ticket 审批运行态单轨化

Ticket 批准、拒绝和委派必须直接操作当前用户有权处理的 BPMN `ProcessTask`。唯一运行态事实是 `ProcessTask` 和 `ProcessApprovalDecision`；`TicketApproval` 不再参与鉴权、待审批计数、状态推进或委派。

架构决策：

- 后端按 WorkItem 查询当前用户可操作的审批任务，并返回 task ID、instance ID、node、允许动作和版本；前端不再接收或提交 `approvalId`。
- approve/reject/delegate 直接调用 BPMN task command；对象授权、CAS、终态保护、决策记录和 token 推进全部位于 BPMN 事务路径。
- WorkItem 的 approved/rejected 等公共状态只能由 BPMN 节点完成后调用权威 WorkItem transition 写入，不能根据 `TicketApproval` pending count 推导。
- 删除 `TicketWorkflowService.ApproveTicket` 中的 `TicketApproval` 查询/更新/创建、直接 `tickets.status` 更新以及 Ticket 专用 `approvalBridge` 调用。
- 删除 `TicketApproval` schema/table、DTO、API 参数、生成代码、测试夹具和只服务该运行态的路由；开发数据库通过保留在仓库中的版本化 drop/reset/seed script 直接重建。
- `ApprovalChain` 只允许作为 ProcessBinding 在流程启动前解析的声明式审批人配置，不保存运行态结果或推进状态。一个 binding 只能选择 ApprovalChain 引用或 BPMN 内联审批人配置之一；配置 preflight 对同时声明两者的情况 fail closed。
- 审批历史 UI 只读取 `ProcessApprovalDecision`，批准、拒绝、委派、重复提交和并发提交都不再经过 legacy endpoint。

### 5.4 Phase 1 门禁

- 四个专业域并发、跨租户创建和依赖降级测试不产生编号冲突。
- WorkItem 与专业扩展不存在重复公共字段或双写调用点。
- 新建专业 WorkItem 都得到“已绑定 SLA”或“明确不适用”的结果。
- participant、outsider、elevated、MSP delegate、revoked delegate 和 cross-tenant 权限矩阵通过。
- 所有任务终态操作被拒绝并留下审计。
- callback 不存在“成功但无效果”。
- 前后端通知合同测试和真实 API 测试通过。
- Ticket 审批只产生一条 BPMN task command 和一条 `ProcessApprovalDecision`；重复/并发提交只推进一次。
- `TicketApproval` 表、`approvalId` 请求字段、直接审批状态写入和 Ticket legacy approval 调用点全部为零。

## 六、Phase 2：可靠执行平台

### 6.1 唯一 RLS 数据路径

现有 observation-only driver、未接线 `AcquireConn` helper 和伪 middleware 被一个 RLS-aware Unit of Work/driver 替换：

- 所有租户 Ent/SQL 查询从 context 读取 tenant，在同一连接或事务上设置 `SET LOCAL app.current_tenant`。
- 非事务查询由 driver 建立短事务；显式事务在开始时设置 tenant。
- 缺 tenant 默认拒绝查询。
- 系统任务必须使用显式 system context 和独立受控数据库角色；普通应用连接不能运行时静默变成 BYPASSRLS。
- RawDB 租户访问迁移到同一边界，迁移完成后删除旁路入口。
- `off` 只允许本地测试；生产配置最终强制 `enforce`。`shadow` 只是部署验证阶段，不是长期并行数据路径。
- 所有 tenant table 的 policy、角色和 GUC 名称进入同一版本化清单，并由真实 PostgreSQL 测试验证。

### 6.2 唯一 AuditService

- HTTP middleware 只记录 request/access/security 事实。
- 领域 Service 在业务事务内记录 before、after、actor、tenant、source、correlation ID 和 result。
- BPMN、KAF、AI、Connector、批量任务通过同一 AuditService 写入，仅以 source 和 action 区分。
- `AuditLog`（或在子设计中明确改名后的单一 audit-event model）是权威审计存储；BPMN timeline 只是它的查询/投影，不再维护第二份 `ProcessAuditLog` 写事实。
- 高风险动作审计写入失败时业务事务回滚。
- 登录、读取和失败请求等不能加入业务事务的访问记录通过统一可靠投递持久化。
- Known Error 等直接使用 Ent 写 `AuditLog` 的手工实现、BPMN 重复审计写入口、旧 `ProcessAuditLog` 存储以及旧 AuditMiddleware 业务职责在迁移后删除。

### 6.3 唯一 Outbox/Worker 平台

统一 envelope 包含 tenant、event type、aggregate、payload version、idempotency key、actor/source、correlation ID、attempt、lease owner、fencing token、next attempt 和 destination/capability。

```text
业务事务 -> outbox_events -> Worker lease/fencing
         -> Handler Registry -> side effect
         -> receipt -> success/retry/blocked/dead-letter
```

架构决策：

- 一条 outbox row 只对应一个 destination 和一套 claim 生命周期；需要 fan-out 时，业务事务为每个 destination 写一条 delivery，禁止两个 dispatcher 竞争同一 status。
- Ticket、Incident、Problem、Change、SLA、BPMN callback、KAF 和 Connector 复用同一 claim、retry、DLQ、replay 和可观测实现。
- `OutboxEvent` 演进为统一投递事实；`ProcessCallbackOutbox` 属于重复调度状态，迁移后删除。
- `KafTaskActionLedger` 作为已接受动作/副作用账本、`KafTaskCompletionReceipt` 作为 monotonic completion receipt 可以保留，因为它们记录领域执行事实而不是投递 claim；两者不得自行实现 poll、lease、retry 或 DLQ。
- P2-C 的详细设计必须逐表列出 `keep`、`migrate`、`delete` 及理由；未列入保留清单的专用 delivery state 默认删除。
- 未知 handler/event type 进入 blocked/DLQ 并告警，不得 no-op。
- 迁移完成后删除 KAF 专用 dispatcher、BPMN callback 专用 worker/table、关键事件 fire-and-forget 发布以及重复 retry/lease 代码。
- Web 进程只提交业务事务和 outbox；SLA、Embedding、Webhook、Connector 同步、导出和长耗时 AI 工作逐步迁入独立 Worker。

### 6.4 KAF 生产准入

在扩展第二个 Agent 场景前，完成现有 SSLVPN 权威场景：

- active 的专用 `kaf_automation` 主体和专用 credential；
- 独立 ITSM/KAF 进程和 Microsoft Graph 真实副作用；
- commit-after-error、replay-only、lease expiry、fencing 和 monotonic receipt 验证；
- 真实 PostgreSQL RLS 探针零 skip；
- Audit、Outbox、receipt 和 Graph 最终状态一致；
- 清理和恢复完成；
- 提交 Live Dev Closeout Addendum 和独立生产准入结论。

### 6.5 Phase 2 门禁

- 目标 tenant table 的 RLS enforce 集成测试全部实际运行并通过。
- 高风险动作审计覆盖率可计算，抽样记录包含完整 actor/source/before/after/correlation。
- Worker 重启、重复投递、乱序回执、lease 过期和 DLQ replay 均可恢复。
- 旧 RLS、Audit、Outbox、callback worker 和 KAF dispatcher 调用点为零。
- SSLVPN 跨进程 closeout 与持久化证据一致。

## 七、Phase 3：生态与规模化

### 7.1 单一 Capability Registry

扩展现有 Connector/Capability/Manifest 方向，建立唯一 Registry；不新增 KAF registry、AI tool registry 或另一套 dispatch switch。

每个 manifest 声明稳定 ID、版本、provider、输入/输出 schema、权限、tenant/region、风险等级、审批要求、幂等能力、超时、重试、补偿、secret/config schema 和健康检查。

```text
BPMN / Catalog / AI suggestion / operator
  -> Capability Registry
  -> permission + tenant + risk policy
  -> approval gate
  -> Unified Outbox/Worker
  -> Provider Adapter
  -> Receipt + Audit
```

KAF 是 `ExternalAgentProvider`；Microsoft Graph、Feishu、DingTalk、WeCom 等是其他 provider adapter。旧类型 switch、专用 dispatcher 和场景硬编码在迁移后删除。

### 7.2 Agent 场景扩展

场景按风险逐级启用：

1. 只读或建议型：Problem RCA 信息收集、Change impact 建议。
2. 可逆低风险动作：通知、群组同步、标准账户配置。
3. 需要审批的高风险动作：Change 实施、权限授予、批量变更。
4. 多 Agent 协作：Agent 之间只能通过 WorkItem/BPMN 交接，不能直接修改彼此或权威领域状态。

AI 输出必须保留 confidence、model/provider、prompt version、actor/source、decision 和 feedback。代码继续强制权限、风险门、审批、事务、副作用和审计。

### 7.3 人工介入

`blocked` 是正式运行态：

- UI 显示阻塞节点、原因、最后执行者、重试次数和责任人。
- 授权人员可以重试、修正配置、重新分派或取消。
- 人工动作使用同一 fencing、幂等、生命周期和审计边界。
- 不允许通过直接数据库修改跳过流程节点。

### 7.4 SaaS、MSP 与多区域

- `tenant_id` 继续表示数据归属；唯一 tenant placement/policy 事实源记录 region、residency、encryption 和 MSP delegation。
- MSP 操作者通过显式 delegated scope 访问客户租户，不能通过普通请求参数切换 tenant。
- 业务数据、Worker 和 Outbox 在所属区域内闭环，不建立跨区域同步领域事务。
- 控制面只管理 placement、manifest、版本和健康状态，不持有 WorkItem 等专业事实。
- 跨区域分析使用经过授权和脱敏的事件投影。
- 灾备以 region-local 数据库、对象存储和 Outbox checkpoint 为恢复单元。
- 客户自管密钥、数据驻留和恢复目标进入 tenant policy，不形成部署模式专用业务分支。

### 7.5 Phase 3 门禁

- 新 capability 无需修改核心业务 switch。
- 每个 capability 都通过 tenant、permission、risk、audit、idempotency 和 failure 测试。
- 高风险 Agent 动作未经审批不能进入 Outbox。
- MSP delegated scope 通过授权、越权、撤权和跨租户测试。
- 区域故障演练证明业务与 Outbox 可恢复且不会重复执行。
- 旧 KAF/Connector/AI 专用派发路径调用点为零并删除。

## 八、多 Agent 执行拓扑

每个 wave 使用一个主 Agent 和三个实施 Agent：

- 主 Agent：冻结接口、分配文件 owner、维护依赖和集成队列、处理共享接线、交叉 review、运行总门禁。
- 实施 Agent：在独立 worktree 中完成边界明确的子项目，不直接合入或强推 main。

### 8.1 Wave 分配

| Wave | Agent A | Agent B | Agent C |
|---|---|---|---|
| 1A | NumberAllocator、复合唯一约束、schema script | BPMN 对象授权和终态策略 | callback 效果语义、通知合同和 handler |
| 1B-Core | WorkItemCreator、SLAPolicyBinder、事务接口 | 跨域合同测试和开发数据 reset/seed script | 专业域回归矩阵和验证 script |
| 1B-Domain | Ticket/Incident 接入 | Problem 接入并删除重复字段 | Change/Requested Item 接入并删除重复字段 |
| 1C | TicketApproval 后端删除和 BPMN task command | 前端 task-based 审批接线和旧 API 删除 | 审批并发/权限/E2E 与 drop/reset/verification script |
| 2A | RLS-aware driver、角色和 policy | AuditService 与审计合同 | 统一 Outbox schema、Worker 和 Registry |
| 2B | 专业域审计与 Outbox producer | BPMN callback 迁移和旧实现删除 | KAF dispatcher 迁移和 closeout 工具 |
| 2C | RLS 全表覆盖和 RawDB 清理 | 高风险审计验证 | Worker 故障注入、DLQ/replay 和恢复 |
| 3A | Capability Registry、manifest、policy | KAF/Connector provider adapter | 管理 UI 和人工介入状态 |
| 3B | Problem/Change Agent 场景 | 企业连接器 | Agent/Connector 可观测和生产验收 |
| 3C | Tenant placement、MSP scope | region-local Worker/Outbox 和灾备 | 多区域、撤权、恢复和驻留 E2E |

`1B-Domain` 必须基于已经合入的 `1B-Core` 开始。`1C` 必须基于 P1-C 的统一 BPMN 对象授权和终态策略开始。Phase 3 场景实现必须基于已经完成生产准入的 Phase 2 平台。

### 8.2 文件所有权与集成

Ent schema/迁移序号、`internal/bootstrap/app.go`、`router.go`、`bpmn_process_engine.go`、共享 DTO、Registry interface、`AGENTS.md` 和 `CLAUDE.md` 在每个 wave 只能有一个 owner。

执行顺序：

1. 主 Agent 记录基线、接口、文件 owner、删除清单和验收命令。
2. 实施 Agent 先写失败测试，再实现并提交小而完整的 commit。
3. Agent 交付 commit、测试输出、删除项和剩余风险。
4. 另一实施 Agent 做交叉 review，重点检查 tenant、permission、transaction 和重复实现。
5. 主 Agent 按依赖顺序合入 integration branch，并独占 bootstrap/router 等共享接线。
6. 运行局部、合同、全量、迁移和运行时门禁。
7. 旧调用点、旧表、旧 worker、旧 DTO 或旧脚本入口未清零时不得关闭 wave。

## 九、错误、事务与人工恢复

统一错误分类：

| 类型 | 行为 |
|---|---|
| `validation/configuration` | 不重试，返回可操作的配置或输入错误 |
| `forbidden/not_found` | fail closed，外部响应不泄露对象存在性 |
| `conflict/idempotent` | 状态/版本冲突；相同已完成请求返回既有结果 |
| `blocked` | 建立人工介入状态、审计、指标和告警 |
| `transient` | 按 manifest/policy 退避重试 |
| `terminal` | 达到上限后进入 DLQ，不静默丢弃 |

WorkItem、专业扩展、SLA、业务审计和 Outbox 在同一数据库事务提交。Graph、Connector 或 Agent 等外部副作用只能由提交后的 Worker 执行；receipt、idempotency key 和 fencing 负责恢复。

## 十、开发环境迁移与脚本保存策略

当前只有开发环境、没有需要保留的生产历史数据，因此本计划不包含历史数据回填、线上备份、滚动兼容或运行时 dual-read/dual-write。

采用直接替换策略：

1. 通过版本化脚本删除或重建受影响的开发表、约束、索引、policy 和 seed 数据。
2. 应用代码只面向新 schema；旧字段、旧表和旧调用点在同一 wave 删除。
3. 开发数据使用可重复的 reset/seed script 重建，不编写仅用于保留旧数据的 bridge。
4. 每次数据库、配置、权限、菜单、registry、seed、验证和清理变更都必须以 script 或注册迁移保存在仓库中。
5. 禁止只在终端、数据库控制台或验收环境手工修改而不提交对应 script。

脚本治理：

- 数据库变更统一进入项目主 migration catalog；RLS 独立 SQL 内容迁入同一版本顺序，删除第二套执行入口但保留全部版本化脚本内容。
- 每个 schema wave 保留 apply、development reset/seed、verification 和必要的 empty-schema rollback/cleanup script。
- 脚本名称包含日期/顺序和业务目的，纳入 checksum、顺序和重复执行测试。
- capability manifest、权限和菜单初始化也使用版本化脚本，不在 bootstrap 中硬编码租户数据。
- 验收报告记录实际执行的 script、revision、checksum、退出码和验证查询。
- 后续如出现需要保留的生产数据，必须另写迁移设计，不能在实施时临时恢复兼容层。

## 十一、测试、可观测与完成证据

### 11.1 测试层次

- 单元：NumberAllocator、SLA policy、BPMN access/lifecycle/effect、risk policy、错误分类。
- 合同：前后端 DTO、Capability manifest、Outbox envelope 和 payload version。
- PostgreSQL repository：编号并发、事务回滚、约束、lease 和 fencing；SQLite 不能替代。
- RLS integration：真实 `RLS_TEST_DSN`，任何 skip 都算门禁失败。
- 权限矩阵：participant、outsider、elevated、MSP delegate、revoked delegate 和 cross-tenant。
- 故障注入：commit-after-error、worker crash、lease expiry、重复投递、乱序 receipt、外部限流。
- E2E：WorkItem 生命周期、BPMN 审批、通知、SSLVPN、blocked/manual intervention 和恢复。
- 脚本：空数据库 apply、reset/seed、verification、重复执行和 revision/checksum 检查。
- 性能：RLS 查询、Outbox backlog、SLA scan、审计写入和 Worker throughput 预算。

### 11.2 指标

| 指标 | 统一口径 | 阶段门禁 |
|---|---|---|
| 租户隔离验证覆盖率 | 实际在 PostgreSQL enforce 下执行且通过的关键 tenant table 数 / 关键 tenant table 总数；skip 不计入分子 | Phase 2 关键表 100%，skip=0 |
| WorkItem 数据一致性事故数 | 编号冲突、SLA 静默缺失、公共状态/字段不一致的运行时记录数 | Phase 1 验收与后续季度均为 0 |
| 高风险操作审计覆盖率 | 产生完整 actor/tenant/source/before/after/correlation 审计的成功高风险动作数 / 成功高风险动作总数 | 发布门禁 100%，持续运营不得低于 99% |
| Agent 委派完成率与人工介入率 | 按场景统计免人工成功数、blocked/人工处理数、平均时长；不同风险场景分别设目标 | SSLVPN 先建立 Dev/生产基线，新场景没有基线前不得宣称统一目标 |
| 事件可靠性/Outbox 覆盖度 | 事务性写入统一 Outbox 的范围内关键事件类型数 / 已定义关键事件类型总数，同时统计 lag、retry、DLQ、replay 和 duplicate suppression | Phase 2 范围内关键事件 100%，DLQ 可见且有 owner |
| BPMN 执行完整性 | blocked、合法 optional skip、终态修改拒绝、重复 command suppression 的数量和比率 | 非声明 optional 的无效果成功为 0，终态非法修改成功为 0 |
| Capability 运行质量 | 按 provider、risk、tenant、region 统计成功率、延迟、重试和人工介入 | 每个 capability 在 manifest 中定义独立 SLO 后才能启用 |

### 11.3 每个 wave 的完成证据

- 基线 commit、worktree、分支和合并顺序。
- 测试命令、退出码、通过/失败/skip 数。
- 变更 script、revision、checksum 和验证查询。
- `rg` 证明旧实现调用点为零。
- 运行时持久记录、日志、指标和外部最终状态。
- `git diff --check`、相关构建、race/并发测试和干净 worktree。
- 架构合同变更时，`AGENTS.md` 与 `CLAUDE.md` 同一提交同步。

测试绿色只是必要条件；数据、权限、脚本、运行时和删除证据同时成立后才能关闭 wave。

## 十二、子项目拆分与依赖

总体设计后续拆为以下独立 spec 和 plan：

1. P1-A：WorkItem NumberAllocator 与 schema 直接替换。
2. P1-B：WorkItemCreator、SLA policy 与专业域物理收口。
3. P1-C：BPMN 残留对象授权与终态策略。
4. P1-D：BPMN callback 效果语义与通知合同。
5. P1-E：Ticket 审批运行态 BPMN 单轨化与 `TicketApproval` 删除。
6. P2-A：RLS-aware data path 与全表 policy。
7. P2-B：统一 AuditService 与高风险动作接入。
8. P2-C：统一 Outbox/Worker、旧 callback/KAF dispatcher 迁移。
9. P2-D：KAF SSLVPN Live Dev Closeout 与生产准入。
10. P3-A：Capability Registry、manifest、risk/approval policy。
11. P3-B：Agent/Connector 场景扩展与人工介入。
12. P3-C：Tenant placement、MSP delegated scope、多区域与灾备。

依赖关系：

```text
P1-A -> P1-B
P1-C -> P1-E

P2-A --------------------------┐
P2-B --------------------------┼-> P2-D -> P3-A -> P3-B
P2-C <- P1-B + P1-D + P1-E ----┘

P2-A + P2-B + P2-C -> P3-C
```

P1-A/P1-C/P1-D 可以在接口冻结后并行；P1-E 等待 P1-C。P2-A/P2-B/P2-C 的接口设计可以并行，但共享 schema/bootstrap 接线按 owner 串行集成。任何 P3 场景都不能绕过对应 P2 门禁。

## 十三、非目标

- 不在本轮全面微服务化专业领域。
- 不物理重命名 `tickets` 为 `work_items`。
- 不在 Phase 1 扩展新的 KAF/Agent 场景。
- 不为不存在的生产历史数据建立兼容层或迁移桥。
- 不以 UI 隐藏代替后端授权。
- 不创建第二套审批、审计、事件总线、Outbox、Worker、Capability Registry 或 tenant scope。
- 不以 deprecated、TODO、fallback 或“后续删除”作为 wave 完成状态。

## 十四、书面设计批准后的下一步

本总体设计批准后，不直接生成覆盖全部 Phase 的单一 implementation plan。首先为 P1-A、P1-C、P1-D 编写独立详细设计或核实现有设计是否足够；接口冻结后分别使用 `writing-plans` 生成依赖有序的实施计划。P1-B 在 P1-A 的编号和 transaction contract 确定后进入设计与执行；P1-E 在 P1-C 的 task authorization/command contract 合入后立即进入设计与执行。

每个子项目重复执行：设计批准、计划批准、独立 worktree、TDD 实施、交叉 review、集成门禁、旧路径删除和证据归档。
