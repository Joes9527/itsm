# 架构评估修复执行总设计

> 状态：Approved for planning
> 日期：2026-08-30
> 当前代码基线：本地及 `origin/main` 均为 `96916acf2133912fd37728332f5e6d7a06b6a611`
> 依据：当前仓库代码、`AGENTS.md`、[architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md)

## 一、目的

本文把架构评估中的 P0/P1 风险重新整理为可独立设计、实施、验证和回滚的修复工作流。所有现状判断只以当前仓库可读取的代码、迁移、测试和提交为依据，不把其他机器上的 worktree、未合并分支或会话记录视为已交付能力。

本文是治理总设计，不是把所有工作塞进一个实施计划。每个工作流必须生成独立的 `writing-plans` 实施计划；只有共享同一不变量、需要在同一事务或同一迁移中完成的改动才能进入同一计划。

## 二、不可变架构约束

以下约束来自 `AGENTS.md`，后续实施计划不得重新选择或弱化：

1. BPMN/ProcessTask 是审批、履约和自动化的唯一编排层，不保留第二套运行态审批引擎。
2. `tickets` 表是第一阶段 WorkItem 基表；共享身份、状态、优先级、请求人、处理人、租户、公共时间戳等字段只在 WorkItem 写入。
3. 专业扩展表只保存 Incident、Problem、Change Request、Requested Item 等领域专属字段。
4. 跨租户和实例级关联必须 fail closed；菜单、粗粒度角色和 tenant 过滤不能替代业务对象授权。
5. 审计、幂等、权限和业务副作用必须在所属 application service 的事务边界内设计，不能作为上线后的统一补丁。
6. 新路径替代旧路径时，同一工作流删除旧写路径；不得长期保留 bridge、双写、双读或静默 fallback。
7. 外部合并只能以当前仓库可见的 merge commit、文件、迁移和测试结果作为完成证据。

## 三、当前代码核实结论

| 领域 | 当前代码事实 | 结论 |
|---|---|---|
| Ticket 审批 | `TicketDetail.tsx` 仍用普通状态更新模拟批准/拒绝；`TicketApprovalApi.submitApproval` 后端仍查询、更新和创建 `TicketApproval`，再桥接 BPMN | 不是单纯前端接线问题，运行态审批仍是双轨 |
| ApprovalChain | 有完整 CRUD；Service Request 创建时解析规则并注入 `_approval_chain` BPMN 变量 | 当前是活跃配置来源，不能与 `TicketApproval` 一并直接下线 |
| BPMN 授权 | 流程实例列表、任务详情及 assign/cancel/variables/counter-sign 等路径缺少统一实例级授权 | 已确认安全缺陷，不再保留为调查项 |
| RLS | driver 的 `Exec/Query` 只观察并透传；`AcquireConn` 未接入生产调用；policy 只覆盖试点/历史迁移中的部分表 | `enforce` 尚未完成，不能直接灰度 |
| WorkItem | Ticket 与 Incident/Problem/Change 扩展表重复保存共享字段；`ticket_number` 仍有全局唯一约束 | 模型骨架已存在，物理收口尚未完成 |
| 完整性检查 | `check_work_item_integrity` 只检查 Incident/Problem/Change 的关联与 record class；Service Request/Catalog Task 被跳过 | 不能作为完整 WorkItem 验收工具 |
| Outbox | 当前 main 没有 `ent/schema/outbox_event.go` | 任何复用 schema 或 dispatcher 的设计都必须等待代码进入当前基线后重新核实 |
| Event Bus | Watermill/Redis Streams 已存在并有 Ticket、AI、SLA 发布调用 | Outbox 应补可靠事务边界，不应重新创建平行事件总线 |
| Connector | Manifest、Capability、Registry、Marketplace、HealthCheck 已存在 | 后续重点是持久生命周期、运行时启停、secret、retry/DLQ 和审计 |

## 四、关键架构决策

### ADR-1：审批运行态只保留 BPMN

Ticket 的批准、拒绝、委派必须直接操作具体 `ProcessTask`，并由 BPMN 在同一权威路径写入 `ProcessApprovalDecision` 和推进流程。前端不得传递旧 `TicketApproval.approvalId`，也不得直接写 `tickets.status=approved/rejected`。

`TicketApproval` 的运行态查询、写入、委派创建和审批人鉴权在同一工作流删除。历史数据如有保留要求，只允许通过一次性归档/迁移或只读历史适配实现，不能继续参与新审批。

`ApprovalChain` 暂时保留为 Service Request 的配置输入，但必须满足两个条件：

- 只在流程启动前解析为 BPMN 变量或流程配置，不保存运行态审批结果。
- 后续评估若 BPMN process binding/definition 已能表达同一规则，则迁移并删除 `ApprovalChain`，不保留两个配置权威来源。

### ADR-2：WorkItem 基表是共享字段唯一权威来源

不再讨论“扩展表权威或 WorkItem 权威二选一”。实施必须把共享字段的读写迁移到 `tickets`，随后删除扩展表中的重复列和双写代码。

专业状态机仍由 `IncidentService`、`ProblemService`、`ChangeService`、`ServiceRequestService` 负责校验，但合法迁移最终写入 WorkItem 的共享 `status` 字段；领域专属状态数据只有确有独立语义时才能留在扩展表，并必须使用不同字段名。

### ADR-3：RLS 当前属于实现阶段，不属于发布阶段

`RLS_MODE=shadow/enforce` 只有在以下能力全部完成后才可用于环境灰度：

- HTTP 请求的 tenant context 与数据库连接/事务绑定。
- application role 与 BYPASSRLS admin role 使用独立连接池，禁止运行时静默回退到同一高权账号。
- GUC 名称在配置、代码、policy 和测试中一致。
- 所有目标 tenant 表有版本化 policy 清单、迁移和回滚脚本。
- 缺 tenant context 在 enforce 下于查询前失败。
- 真实 PostgreSQL 验证同租户可读写、跨租户不可见且不可写、后台系统任务使用显式 bypass。

在这些条件完成前，只允许使用 `off`；现有 `shadow` 统计可用于开发诊断，但不能作为 RLS 已生效的证明。

### ADR-4：审计和 E2E 是工作流验收门禁

不再设置“最后统一补审计”的独立修复阶段。每个涉及状态迁移、审批、连接器、自动化、批量操作或 AI 写动作的工作流必须同时定义：

- actor、tenant、source、correlation ID、前后状态和结果。
- 审计写入与业务副作用的事务关系。
- happy path、同租户未授权、跨租户、重复提交、并发或重试测试。
- 失败后不得出现业务已提交但审计、事件或流程状态缺失的半完成状态。

最终可以做审计覆盖率盘点，但盘点只发现遗漏，不负责重新设计各领域事务。

### ADR-5：Outbox 存储模型等待实际 schema 后决策

撤销“内部事件与 KAF 委派必须共用一张 `outbox_events` 表”的预先决定。`AGENTS.md` 的单一权威来源原则要求一个业务事实只有一个写入来源，不等于所有传输目的地必须共享一个状态列。

外部 Outbox 合并进入当前 main 后，必须先写独立 ADR，并在以下模型中明确选择一个：

1. 每条 outbox event 只有一个 `transport/destination`，由唯一 dispatcher claim。
2. `outbox_events` 保存业务事件，`outbox_deliveries` 为每个目的地保存独立 claim、重试和终态。
3. 若 KAF HTTP 与内部事件生命周期、保留策略和安全边界不同，使用独立投递表，但共享统一事件 envelope/ID 规范。

无论选择哪种模型，都必须避免两个 dispatcher 竞争同一 `status`，并定义 lease、幂等、DLQ、replay、租户隔离和敏感 payload 规则。

## 五、工作流与执行顺序

### Wave 0：安全与审批正确性

#### S0：BPMN 实例级授权

目标：所有流程实例和任务读写都通过统一 participation policy；粗粒度 RBAC 只决定是否可调用端点，实例策略决定可查看或操作的对象。

范围：

- `StartProcess` 持久化 initiator。
- 统一解析 assignee、candidate users、candidate groups 和 initiator。
- `ListProcessInstances`、`GetTask`、`ListUserTasks` 收敛读范围。
- assign、cancel、set variables、counter-sign 及其状态读取接入相同策略。
- elevated 权限继续使用现有 `process_instance:read`、`task:read`、`task:update`，不新增角色体系。
- 所有写动作补 `ProcessAuditLog`，并对 tenant 与 task/instance 关联 fail closed。

验收：参与者只能访问自己参与的对象；同租户非参与者被拒；跨租户即使猜中 ID 也被拒；elevated 权限有明确测试；所有高风险写动作有审计。

#### A0：Ticket 审批单轨化

依赖：S0 的统一任务授权与 task action 接口。

目标：Ticket UI 直接完成 BPMN `ProcessTask`，只产生 `ProcessApprovalDecision`，流程只推进一次。

范围：

- 后端提供按 Ticket/WorkItem 查询当前用户可操作审批任务的 DTO/API。
- 前端审批按钮使用 task ID 提交 approve/reject/delegate，并要求必要意见。
- 删除 `TicketWorkflowService.ApproveTicket` 中 `TicketApproval` 查询、写入、委派创建和 BPMN bridge。
- 删除新审批对 `TicketApproval` 的所有依赖；完成历史数据处置后删除 schema、路由、DTO 和生成代码。
- 单独记录 `ApprovalChain` 的配置用途，不把它当作运行态审批记录。

验收：授权、拒绝、委派、重复提交、并发提交、跨租户均通过真实 API/E2E；只有一条 ProcessTask 完成和一条 ProcessApprovalDecision；没有普通状态更新模拟审批。

#### R0：RLS 可实施基础

可与 S0/A0 分支独立实施，但未通过真实 PostgreSQL 门禁前不得设置 enforce。

目标：让 tenant context 真正控制低权数据库连接与 policy，建立可灰度的技术基础。

验收：试点表先通过 app/admin 双角色集成测试；缺 tenant context fail closed；shadow 指标可采集；迁移和回滚脚本可重复执行。全表推广另起 R1 计划。

### Wave 1：WorkItem 与配置契约

#### W0：WorkItem 共享字段物理收口

目标：`tickets` 成为共享字段唯一写入位置，并统一编号服务。

范围：

- 盘点 Incident/Problem/Change/Service Request 所有共享字段读写。
- 先回填和一致性报告，再切读，再切写，最后删除扩展表重复列；每一步有可回滚迁移。
- `ticket_number` 从全局唯一改为 `(tenant_id, ticket_number)` 复合唯一。
- Ticket/Incident/Problem/Change/Requested Item 使用同一编号分配接口。
- 完整性检查覆盖全部已支持 record class，并校验 extension tenant、外键、共享字段迁移结果。

验收：并发、多租户、多 record class 创建无冲突；共享字段只有一个写路径；真实 PostgreSQL 迁移、检查和回滚验证通过。

#### F0：配置 Schema 与 Preflight

目标：把配置解析、静态校验、外部依赖 readiness 和可选能力降级分开。

必须定义四维矩阵：development/production、required/optional、startup/readiness、secret/non-secret。生产环境缺失固定 JWT secret、RLS enforce 使用高权回退账号、配置了 KAF URL 但无 secret 等情况必须阻断启动；Redis、MinIO、LLM 等按实际功能依赖输出结构化能力状态，不允许静默伪装为可用。

验收：表驱动测试覆盖所有配置组合；日志和健康接口不泄露 secret；Compose 示例与配置 schema 一致。

### Wave 2：可靠事件与 Worker

触发条件：Outbox schema 和 KAF 相关代码通过可识别 merge commit 进入当前 main，并通过本仓库测试。不得以其他机器的工作目录状态作为触发条件。

执行顺序：

1. O0：基于实际 schema 编写多目的地投递 ADR。
2. O1：为 Ticket/Incident/Change/SLA 等业务事务增加 outbox 原子写入。
3. O2：实现独立 dispatcher/worker、lease、幂等、DLQ 和 replay。
4. O3：逐个迁移 SLA、Embedding、Webhook、Connector 同步、导出和长 AI 任务；每类任务单独计划和验收。

### Wave 3：独立平台工作流

以下工作不进入本总设计的首个实施计划，必须分别重新核实现状并编写独立设计/计划：

- M0 模块化重构：以可测试边界和删除旧入口为目标，不以文件行数为唯一拆分依据。
- C0 连接器治理：复用现有 Manifest/Capability/Registry，补持久生命周期、运行时启停、secret、入站安全、retry/DLQ 和审计。
- AI0 AI 评估控制台：统一 provider/model/prompt version/confidence/source/feedback/tool approval/execution evidence，明确降级输出。

## 六、计划拆分与依赖

```text
S0 BPMN 实例级授权
  └── A0 Ticket 审批单轨化

R0 RLS 可实施基础
  └── R1 全表 policy 与环境灰度

W0 WorkItem 物理收口
F0 配置 Schema/Preflight

外部 Outbox merge commit 可见
  └── O0 投递 ADR
       └── O1 事务 Outbox
            └── O2 Worker 基础
                 └── O3 各后台任务迁移
```

首个 `writing-plans` 只覆盖 S0。A0、R0、W0、F0、O0-O3 分别产生独立计划，避免共享状态不足的多 agent 并发修改同一批核心文件。

## 七、执行与验证纪律

1. 开始任务前记录 `git rev-parse HEAD`、`git status --short --branch` 和目标文件现状。
2. 只根据当前 worktree 和当前可见提交排期；外部分支必须先合并或以本地可读 ref 提供。
3. Ent schema/migration、`router.go`、`bpmn_process_engine.go` 等高冲突面串行修改；其他工作流使用独立 worktree。
4. 每个任务遵循 TDD：失败测试、最小实现、局部测试、相关包测试、构建、提交、review。
5. SQLite 单测不能替代 PostgreSQL 的 RLS、约束、锁和迁移验证。
6. 验收证据必须包含命令、退出码、测试名称和关键数据库断言；“代码存在”或“另一台机器测试通过”不算本仓库验收。

## 八、关联文档

- [AGENTS.md](../../../AGENTS.md) — 架构、租户、安全和 WorkItem 实施契约
- [architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md) — 原始风险评估
- [2026-08-25-bpmn-task-instance-authorization-design.md](./2026-08-25-bpmn-task-instance-authorization-design.md) — S0 的已有详细设计输入
- [2026-08-26-approval-single-track-convergence-design.md](./2026-08-26-approval-single-track-convergence-design.md) — A0 的历史收敛背景
- [2026-08-26-unified-work-item-model-design.md](./2026-08-26-unified-work-item-model-design.md) — W0 的详细领域模型
- [2026-08-29-kaf-delegation-transactional-delivery.md](../plans/2026-08-29-kaf-delegation-transactional-delivery.md) — 外部 Outbox 合并后 O0 的核对输入，不作为当前代码事实
