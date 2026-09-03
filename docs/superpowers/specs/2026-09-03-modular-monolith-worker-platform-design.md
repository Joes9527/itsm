# 模块化单体边界与 Worker 平台设计

> 状态：Approved for planning
>
> 日期：2026-09-03
>
> 依据：[AGENTS.md](../../../AGENTS.md)、当前 `itsm-backend` 实现、现有 KAF/BPMN Outbox 实现与用户确认的架构决策。

## 1. 目标与范围

本设计同时解决两项关联的架构治理工作：

1. 在不拆分微服务的前提下，收敛模块化单体的领域边界与依赖方向。
2. 将 API 进程内的后台副作用迁移至独立 Worker 运行角色，并采用可靠、可审计的异步执行模型。

这不是一次全面微服务化，也不重新定义 Ticket、Incident、Problem、Change 或 Service Request 的专业状态机。`tickets` 仍是第一阶段 WorkItem 基表，专业域服务仍拥有各自的状态迁移规则。

## 2. 已确认的架构决策

### ADR-1：采用同仓库、同模块、多命令运行角色

运行时部署为独立命令，而不是通过 API 进程运行 goroutine/ticker：

```text
itsm-api       HTTP API、同步命令和查询
itsm-worker    持久化异步任务与外部副作用执行
itsm-migrate   Ent schema 与 post-schema migration
```

三种命令共享 `internal/bootstrap`、领域服务、Repository、配置、日志与数据库连接配置。它们不通过 RPC 调用，不维护独立数据库，也不引入分布式事务。

**理由：**当前 Ticket、Incident、Change、Service Request、BPMN、SLA 和 CMDB 之间存在事务性关联及统一授权要求。先隔离运行时职责，能够获得独立扩缩容和故障隔离，而不会过早引入跨服务一致性成本。

### ADR-2：领域依赖只能单向向下

```text
HTTP Handler / DTO -> Domain Application Service -> Domain Repository -> Infrastructure
```

| 层级 | 允许职责 | 禁止职责 |
|---|---|---|
| Handler | 参数绑定、DTO 映射、认证上下文读取、调用服务、返回 DTO | Ent 查询、事务、状态转换、跨域 repository 调用 |
| Domain Service | 专业状态机、对象授权、租户检查、事务、领域协作、审计、事件/任务落库 | Gin/HTTP 依赖、后台 goroutine、跨域 repository 实现调用 |
| Repository | 本领域 tenant-scoped 持久化、条件更新、锁与实体映射 | 业务状态决定、跨域服务编排 |
| Infrastructure | Ent、PostgreSQL、Redis、MinIO、LLM、Connector 等适配 | 向上调用 Handler/Service、未知目标静默成功 |

跨域协作使用对方领域服务暴露的最小接口或显式 port。现有 Service Request 以最小 Ticket/Incident 接口实现协作是允许模式；一个领域不得 import 并直接调用另一领域的 Repository 实现。

### ADR-3：任务迁移采用单路径原子替换

`AGENTS.md` 禁止新旧路径长期并存、双写、双读、fallback 和未知行为静默跳过。因此每一种后台任务在上线时必须完成原子替换：

```text
旧路径：itsm-api ticker / in-memory queue -> 外部副作用
新路径：Domain Service -> 本地事务写 Event/Job -> itsm-worker -> 外部副作用
```

生产环境不得让 API 和 Worker 对同一任务类型同时执行，也不得在 Worker 不可用时退回 API 同步执行。Worker 未就绪、任务能力未注册或外部执行不可确认时，任务必须保留为 `pending`、进入 `blocked`/`dead_letter`，或进入人工接管，且必须产生审计记录。

非生产环境可使用隔离数据库或 mock destination 验证 Worker 行为；这只是测试手段，不是生产双执行链路。

### ADR-4：业务事件与投递状态分离

当前 `OutboxEvent` 的状态/lease 模型已经适用于一个事件交给一个 Dispatcher。后续一个领域事实可能需要同时驱动通知、Connector、Embedding、搜索索引或 AI，因此采用以下模型：

| 数据对象 | 权威职责 | 示例 |
|---|---|---|
| `outbox_events` | 已提交的、不可变的领域事实 | `knowledge.published` |
| `outbox_deliveries` | 将一个 Event 投递给一个指定 destination 的可靠执行状态 | 向 Embedding Worker 投递该事件 |
| `async_jobs` | 用户显式发起且可长时间运行的命令 | 导出 SLA 报表、批准后的 AI Tool 调用 |

这不是双路径：Event、Delivery、Job 是不同事实。每一个副作用都只能由一个 Delivery 或 Job 记录承担，Worker 是唯一执行者。

## 3. 领域边界迁移

### 3.1 迁移批次

| 批次 | 领域 | 迁移闭环 | 删除旧入口的门槛 |
|---|---|---|---|
| 1 | Ticket / WorkItem | 创建、分派、评论、附件、关闭、评价、BPMN 启动 | 所有 API 与内部调用走统一 Application Service；旧 Controller 不再直接查询 Ent 或写状态 |
| 2 | Incident | 创建、确认、升级、解决、关闭、转 Problem、CI 影响 | WorkItem 是共享字段唯一写入处；旧 Incident 写路径删除 |
| 3 | Change | 风险、CAB/BPMN 审批、排程、实施、回滚、PIR | 不存在绕过 BPMN 的批准/拒绝状态写入 |
| 4 | Service Request | Catalog preflight、动态字段、Requested Item、履约、目标 Incident | 所有 Catalog 目标类、流程、SLA 与能力组合在服务层校验；遗留 fallback 删除 |

### 3.2 每个批次的完成定义

1. 建立 API 黑盒合同测试：成功、同租户未授权、跨租户、重复提交、并发提交。
2. 在 Domain Service 内收敛该命令的权限、事务、审计与 Event/Job 落库。
3. 将所有调用方迁移至新服务。
4. 在同一个发布批次中删除旧 Controller/Service 写路径及其路由、DTO、测试。
5. 增加架构测试，阻止 Handler 引入 Ent、阻止跨域 Repository 依赖。

专业服务负责验证专业状态机，但 WorkItem 共享字段仅由 `tickets` 存储。不得用 `switch recordClass` 构建通用专业状态机。

## 4. Worker 平台执行契约

每个 `outbox_delivery` 或 `async_job` 必须遵循以下统一行为：

| 能力 | 必须行为 |
|---|---|
| Claim | 仅 Worker 能通过条件更新取得执行权；API 永不直接执行异步外部副作用 |
| Lease | 每次 claim 持有短租约；崩溃后可回收；只有当前 lease token 可完成、重试或终止 |
| Fencing | 对可变业务状态的回写必须携带版本/CAS 条件，失去 lease 的 Worker 不得覆盖新执行者结果 |
| 幂等 | 每项外部操作使用稳定 `idempotencyKey`；接收方或本地执行记录保证重投不重复副作用 |
| Retry | 网络、限流、临时 5xx 可退避重试；参数、权限、能力缺失等确定错误进入 blocked/DLQ |
| Ambiguous | 外部调用已开始但结果不可确认时，不盲目重试；转人工 reconciliation，保留证据 |
| Replay | 管理员重放创建新的投递尝试，不改写原始 Event；须校验 RBAC、tenant 与重放原因 |
| Governance | 持久化 tenant、actor/source、correlation ID、任务版本、开始/结束时间、脱敏错误分类与结果引用 |

Worker 对领域事实的回写必须调用拥有该事实的 Domain Service，不能直接跨域写 Ent 表。

## 5. 任务迁移顺序

| 顺序 | 任务 | 当前形式 | 原子替换后的唯一执行形式 | 验收重点 |
|---|---|---|---|---|
| 1 | SLA 监控与升级 | API ticker 扫描 | schedule Job + SLA evaluation/escalation Delivery | 重复执行无重复升级，多副本不重复，违规状态可追溯 |
| 2 | Knowledge Embedding | API 启动扫描与周期轮询 | `knowledge.published/unpublished` Delivery + 显式 reindex Job | 发布/下架与向量状态一致，补偿重建可控 |
| 3 | Connector/Webhook | KAF 单目的地 Dispatcher 范式 | destination-specific Delivery Worker | 签名、密钥脱敏、租约、retry/DLQ、回调幂等 |
| 4 | 导出 | HTTP 长任务或同步查询 | `export.requested` Async Job，MinIO 产物引用 | tenant/请求人下载鉴权、过期清理、失败可重试 |
| 5 | AI Tool Queue | 审批后进程内 channel | `ai.tool_execution` Async Job | 审批只改 Job 状态，副作用可审计、超时可人工接管 |

每项迁移的生产发布条件是 Worker 的该任务 handler 已注册、健康检查通过、端到端 PostgreSQL 故障测试通过，并在同一变更中删除 API ticker/goroutine/channel consumer。

## 6. 运行、可观测性与安全

- `itsm-api` 的 readiness 只反映 HTTP、依赖和同步领域操作可用性；`itsm-worker` 独立报告 handler 注册、数据库 claim 能力与 backlog 状态。
- 最低指标包括 pending backlog、任务 age、claim 冲突、lease recovery、retry 数、DLQ 数、执行延迟、成功率和人工 reconciliation 数。
- 每个 Event/Delivery/Job 以 tenant scope 查询；生产诊断禁止无 tenant 条件的 payload dump。
- Worker 使用明确的 system actor 或受控的原始请求人身份。高风险写操作保持原 actor/source 与 correlation ID。
- 任务能力注册与 Connector manifest 必须 fail closed。缺少 required handler 不能被当作可选步骤。

## 7. 实施顺序与依赖

```text
G0 架构护栏和领域调用盘点
  -> D1 Ticket / WorkItem 收口
  -> D2 Incident 收口
  -> D3 Change 收口
  -> D4 Service Request 收口

W0 itsm-worker 命令与共享 bootstrap
  -> W1 Event/Delivery/Job schema ADR 与迁移
  -> W2 Worker claim/lease/fencing/DLQ/replay 基础
  -> W3 SLA
  -> W4 Embedding
  -> W5 Connector/Webhook
  -> W6 Export
  -> W7 AI Tool Queue
```

G0/D 系列可与 W0/W1 的设计和基础实现并行，但以下高冲突面只能由明确 owner 串行合并：Ent schema/migration、`internal/bootstrap`、`router/router.go` 和 BPMN 核心引擎。

## 8. 验收矩阵

| 范围 | 必过验证 |
|---|---|
| 领域边界 | Handler 无直接 Ent 查询；无跨域 Repository 调用；旧路由/旧写路径已删除；合同测试覆盖权限、租户、并发、重试 |
| WorkItem | 共享字段只有 WorkItem 写入；专业扩展无重复共享字段；专业状态转换遵循所属 Service |
| Worker 正确性 | 多 Worker 并发仅一个 claim 成功；lease 过期可恢复；旧 worker fencing 回写失败；重复 Job/Event 不重复副作用 |
| 故障治理 | 5xx/超时退避；4xx/配置错误进入 blocked 或 DLQ；ambiguous delivery 转人工 reconciliation；replay 有审计 |
| 安全与隔离 | 跨租户 Job/Delivery 不可读取或操作；密钥和 payload 不进入日志/错误；高风险重放需权限与理由 |
| 生产验证 | PostgreSQL 下验证锁、约束、迁移与 RLS；API/Worker 多副本故障演练；关联的前端任务状态与错误状态 E2E 验证 |

## 9. 明确不做

- 不在本阶段拆出独立 Workflow、CMDB、AI 或 Connector 微服务。
- 不为兼容而长期保留 API ticker 与 Worker 双执行。
- 不让 Worker 绕过领域服务直接修改另一领域的数据。
- 不把所有异步任务统一塞入一种无语义的 queue；领域事件与用户命令保持不同事实模型。
- 不在缺少可量化读性能瓶颈时提前引入全面 CQRS 或读写数据库拆分。