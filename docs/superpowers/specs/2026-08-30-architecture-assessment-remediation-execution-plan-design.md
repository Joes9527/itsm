# 架构评估落地执行计划 — 设计文档

> 状态：Proposed
> 日期：2026-08-30
> 依据：[architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md)（已核实修订版）
> 基线：本地 `main`(`1952559a6`) 与远端 `192.168.31.66:/home/administrator/project/itsm`(`main` @ `5bfe8c3e`，两者仅差一次未推送的文档提交)
> 方法：对评估报告 P0/P1 共 17 条结论去重、逐条与远端 worktree 现状核对后重新排期。核对方式见"六、执行纪律"。

## 一、背景与目的

`architecture-product-assessment-2026-08-30.md` 给出了 P0(6 项)/P1(6 项)/P2(5 项)技术债清单，但本身不是执行计划——按原文顺序直接派发会有两个问题：

1. **重复计数**：P0"统一工单审批"与 P1"审批机制收敛①"是同一个改动；P0"编号一致性"与本轮评审新增的"WorkItem 共享字段权威来源收口"都已经是 `2026-08-26-unified-work-item-multi-agent-execution-plan.md` Wave 3 里明确记录、明确留作独立后续工作的收尾项，不是两个独立任务。
2. **与在制品冲突**：本文档写作过程中直接核实发现，评估报告完全没有覆盖的"KAF 委派链路"不仅是一个真实缺口，而且**已经有另一批 agent 在 `192.168.31.66` 上按 `docs/superpowers/plans/2026-08-29-kaf-delegation-transactional-delivery.md` 执行到 Task 6**（详见五、执行纪律与六、证据索引）。如果不做这一步核对，本计划会把已完成 80% 的工作重新排入 Phase 1，造成真实的 git 冲突。

本文档的产出物是：一份去重后的、按依赖关系分阶段的任务清单，供后续用 `executing-plans`/`subagent-driven-development` 转成可执行任务包。**不重新设计任何子系统**——已有设计文档的任务直接引用，只在"通用 Outbox 消费扩展"和"配置 Preflight 检查项"两处补最小限度的新设计。

## 二、范围与不做什么

**范围**：`architecture-product-assessment-2026-08-30.md` 的 P0 全部 6 项 + P1 全部 6 项，去重合并为 12 个工作项（含 1 个降级为调查项、1 个整项删除）。P2 的 5 项不在本轮排期内，留待 P0/P1 收尾后按原文档条件触发。

**不做什么**：

- 不重新规划 KAF 委派链路。`192.168.31.66:/home/administrator/project/itsm` 的 `feat/kaf-delegation-transactional-delivery` worktree 已完成 Task 1-4（提交）并在推进 Task 6（未提交，2026-08-30 16:32 仍在改动），本计划只在"六、执行纪律"里记一条监控/合并后收尾项，不派发新任务包。
- 不重新设计 `ent/schema/outbox_event.go` 的表结构。该 schema 已在上述 worktree 的 Task 2 提交（`2db8f3ec feat(outbox): persist tenant-scoped delegation events`）并经多轮 review 加固（乐观租约 `claim_token`/`claim_expires_at`、字段级 `.Sensitive()`），合并进 main 后直接复用。
- 不覆盖 P2（读模型、服务拆分、自主 Agent、多模态、多区域 MSP）——这些的前置条件（RLS/Worker/事件可靠性稳定）要等本轮 P0/P1 完成才成立。

## 三、任务清单总览

| Phase | 任务 | 来源 | 依赖 |
|---|---|---|---|
| 0 | A. 修复 `TicketDetail.tsx:283` 审批接线 | P0"统一工单审批" + P1"审批机制收敛①" | 无 |
| 0 | F. 配置 Preflight | P0 | 无 |
| 1 | E. RLS shadow 上线 + 覆盖率收集 + enforce 灰度 | P0"RLS 上线准备" | 无 |
| 1 | D. WorkItem Phase 6 收尾 | P0"编号一致性" + 新增 P1"共享字段收口" | 无（与其他 Phase 1 任务共享 ent schema 面，不能与 Phase 2 的 outbox 消费任务并行改同批文件） |
| 1 | 确认 08-24/08-25 权限现状 | 本轮 KAF 调查新增发现 | 无（不再是任何任务前置条件，纯收尾调查） |
| 2 | H/I. Outbox 消费扩展到内部事件总线 + Worker 化 | P1"Outbox 与事件治理" + "Worker 化" | KAF 分支合并（复用其 `outbox_event` 表） |
| 3 | J. 模块化重构 | P1 | 无，持续性 |
| 3 | K. 连接器平台 | P1 | 无 |
| 3 | L. AI 评估与控制台 | P1 | 无 |
| 贯穿 | C. 关键流程 E2E | P0 | 依赖 A（审批 E2E 需要正确路径） |
| 收尾 | B. `TicketApproval`/`ApprovalChain` 下线核实 | P1"审批机制收敛②" | 依赖 A 落地 |
| 收尾 | G. 审计补齐 | P0 | 等其他域改动稳定后再补，避免来回改 |
| 收尾 | 更新 AGENTS.md 补 KAF 委派架构约束 | 本轮 KAF 调查新增发现 | 依赖 KAF 分支合并进 main |

**已从原评估报告中移除、不再规划的项**（均已核实为已完成或已被后续工作覆盖，详见 `architecture-product-assessment-2026-08-30.md` 的对应订正）：

- Change 域"多套历史审批模型并存"——PR#6 已 100% 收口到 BPMN。
- "先建 RLS shadow 模式"——`database/rls/driver.go` 的 shadow/enforce driver 已实现（R2A 骨架完成），Phase 1 的 E 项只做启用与灰度，不重新设计。
- KAF-ITSM Task 6+ 落地——见二、范围与不做什么。

## 四、依赖关系图

```
Phase 0（立即，互不依赖）
  A ─┬─→ C（关键流程 E2E，贯穿多个 Phase）
     └─→ B（收尾）
  F

Phase 1（互相独立，可并行，但都不能与 Phase 2 的 outbox 消费任务同时改 ent schema）
  E（RLS）
  D（WorkItem Phase 6）
  08-24/08-25 权限现状确认

Phase 2（等待外部事件：KAF 分支合并）
  KAF 分支合并 → H/I（Outbox 消费扩展 + Worker 化）

Phase 3（独立、持续性，随时可以启动/暂停）
  J（模块化重构）
  K（连接器平台）
  L（AI 评估控制台）

收尾（依赖其他 Phase 稳定）
  G（审计补齐，等域改动稳定）
  KAF 分支合并 → 更新 AGENTS.md
```

## 五、执行纪律

**在把任何任务包正式派发执行之前，必须先核对 `192.168.31.66:/home/administrator/project/itsm` 下 `.claude/worktrees/` 和 `.worktrees/` 是否已有同名/同范围的 worktree 在进行。** 连接方式：`ssh -p 22223 administrator@192.168.31.66`（直连；`~/.ssh/config` 里的 `kaf-ci`/jump-host 路径截至本文档写作时握手失败，直连可用，具体故障现象见 `connecting-to-kaf-ci` 技能之外的本次会话记录）。核对方法：

```bash
ssh -p 22223 administrator@192.168.31.66 "cd /home/administrator/project/itsm && git worktree list"
```

对每个待派发任务，检查是否已有 worktree 分支名或最近提交内容与其重叠；如有重叠，优先监控/合并该分支，不重复排期。这条纪律的直接教训：本文档写作过程中曾把"KAF-ITSM Task 6+ 落地"排入 Phase 1，后核实发现该工作已完成 80%，若非补做这一步核对会直接造成重复规划和潜在合并冲突。

## 六、分 Phase 任务详情

### Phase 0

**A. 修复 `TicketDetail.tsx:283` 审批接线**

- 范围：`itsm-frontend/src/components/ticket/TicketDetail.tsx` 的 `handleApprove`（约第 279-291 行）及同文件驳回逻辑（约第 322 行），改为调用 `TicketApprovalApi.submitApproval`/BPMN 审批接口，删除 `TicketApi.updateTicketStatus(ticketId, 'approved'/'rejected')` 调用。
- 证据：`git blame` 确认该函数自 2026-07-26 起未变更，`main` 分支截至 2026-08-30 仍是错误接线；`architecture-and-roadmap-assessment-2026-08-26.md` 已将其列为 P0-3 跟踪，`docs/superpowers/specs/2026-08-26-approval-single-track-convergence-design.md` 确认这是审批单轨化链路上唯一剩余的活跃问题。
- 验收标准：批准/驳回均写入 `ProcessApprovalDecision`，流程只推进一次；补 E2E 覆盖授权、拒绝、重复提交。

**F. 配置 Preflight**

- 范围：新增启动期检查，覆盖 DB、Redis、RLS 模式、可选 MinIO、必需 ENV、LLM 配置的一致性。
- 需要新设计（原评估报告和现有 specs 均未细化）：检查项清单、失败时的启动阻断 vs 降级策略、日志格式。留待写 `writing-plans` 时展开。
- 验收标准：环境错误在启动前明确失败；可选能力降级有显式状态输出。

### Phase 1

**E. RLS shadow 上线 + enforce 灰度**

- 范围：不重新设计 driver（已实现），在预发/生产环境启用 `RLS_MODE=shadow`，接入可观测性收集缺失 tenant 条件的覆盖率数据，按表/模块修复后推进 `RLS_MODE=enforce`。
- 证据：`database/rls/driver.go` 头部注释"R2A skeleton. off + shadow verified; enforce implemented but disabled until R2B灰度收尾"。
- 验收标准：关键租户表在 enforce 下通过集成测试；跨租户读写均被 DB 拒绝。

**D. WorkItem Phase 6 收尾**

- 范围：
  1. `ticket_number` 唯一约束从全局唯一改为 `(tenant_id, ticket_number)` 复合唯一；Ticket/Incident/Problem/Change 四处生成逻辑收敛为一个。
  2. 审计 Incident/Problem/Change/Service Request 扩展表对 `title`/`description`/`status`/`priority` 等共享字段的读写路径，明确"扩展表权威、WorkItem 基表只读镜像"或反之，禁止两边都能写。
  3. 补跑 `cmd/check_work_item_integrity` 对一个 schema 已同步（含 `record_class` 列）的真实 Postgres，此前从未端到端跑通过。
- 证据：`docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md` §18.8（Phase 6 物理清理，原始设计）；`docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md` Wave 3 状态记录（"已知问题：ticket_number 跨域撞号，backlog，用户已确认延后处理"）；`service/incident_service.go:160-198` 的双写代码注释自述"持续双向同步要么是禁止的双写反模式……留作独立后续项"。
- 依赖：与 Phase 2 的 outbox 消费任务共享 ent schema 变更面，不能并行执行——遵循既有 Wave 纪律，schema 变更必须先单点串行完成。
- 验收标准：并发、多租户、Ticket/Incident/Problem/Change 创建不发生编号冲突；`check_work_item_integrity` 对真实库跑通过；共享字段权威来源在代码里唯一。

**确认 08-24/08-25 权限现状**

- 范围：核实 `handlers/bpmn` 相关路由的实例级授权（发起人/候选人过滤）是否已实现（`2026-08-25-bpmn-task-instance-authorization-design.md` 自述"待实现"，本轮未重新验证代码）；核实 RBAC 双轨制收敛（`2026-08-24-rbac-dual-declaration-convergence-design.md`）里记录的约 34 条无覆盖路由现状。
- 验收标准：产出一份现状核实结论（file:line 证据），不要求本阶段修复，仅为后续排期提供依据。

### Phase 2（等待 KAF 分支合并触发）

**H/I. Outbox 消费扩展 + Worker 化**

- 范围：KAF 分支合并进 main 后，复用其 `ent/schema/outbox_event.go`，新增内部消费分支——Ticket/Incident/Change/SLA 等领域事件写入同一张 `outbox_events` 表，由独立 Worker 消费并发布到 Watermill/Redis Streams（区别于 KAF 分支的 HMAC 签名 HTTP dispatcher，是同一张表的第二个 dispatcher）。SLA/Embedding/Webhook/Connector 同步/导出/长 AI 任务逐步迁移为独立 Worker，任务状态、重试次数、执行租约、幂等键持久化。
- 需要新设计：内部 dispatcher 与 KAF dispatcher 的字段/语义复用边界（`aggregate_type`/`aggregate_id`/`event_type` 的取值规范扩展到非 KAF 事件）。留待写 `writing-plans` 时展开，需先读 KAF 分支合并后的实际 schema。
- 验收标准：业务事务与 Outbox 原子；发布可重试；消费者幂等；具备 DLQ/replay；多 API 副本不重复执行后台任务。

### Phase 3（独立、持续性）

**J. 模块化重构**：拆 Router/BPMN/Ticket/CMDB，handler 数据访问下沉 repository，迁移完成即删除旧入口。

**K. 连接器平台**：Manifest/Capability 派发、secret/health/retry/DLQ，新连接器不修改核心业务 switch。

**L. AI 评估与控制台**：建议/引用/置信度/反馈/工具审批的统一界面和数据模型。

以上三项原评估报告已有较完整的目标描述，本文档不重复展开，按原文验收标准执行；因体量大、边界独立，建议在 Phase 0/1 稳定后再决定是否用多 agent worktree 并行分发。

### 贯穿与收尾

**C. 关键流程 E2E**：覆盖 Ticket、Incident→Problem、Problem→Known Error、Change/CAB、Service Request，每条链路含 happy path、权限拒绝、跨租户拒绝、重试/重复提交。跟着 A 及各 Phase 1/2 的域改动同步补，不是一次性冲刺。

**B. `TicketApproval`/`ApprovalChain` 下线核实**：`architecture-and-roadmap-assessment-2026-08-26.md` 已核实 `TicketApproval.Create()` 全仓库仅在委派分支出现一次、`ApprovalChain` 仅是流程变量路由展示元数据。A 落地后，确认两表无新增写入，清理为只读或下线。

**G. 审计补齐**：覆盖领域状态迁移、审批、连接器、批量操作和 AI 写动作，审计写入放在领域 application service 的事务边界内。等 Phase 0/1/2 的域改动稳定后再统一补，避免与并行改动的文件冲突。

**更新 AGENTS.md 补 KAF 委派架构约束**：KAF 分支合并进 main 后，在 AGENTS.md 补一节反映委派节点（`kaf_delegate`）暂停/恢复语义、`kaf_automation` 技术账号模型、`AsyncServiceTaskHandler` 扩展点，防止后续开发者/agent 不了解这套机制而重新发明或误用现有 NoOp 兜底。

## 七、关键架构决策记录

**决策**：内部通用 Outbox 与 KAF 委派 Outbox 使用同一张 `outbox_events` 表，不新建第二张表。

**理由**：KAF 侧契约（`event_id`/`correlationId`/签名/去重键）已经在 `docs/superpowers/plans/2026-08-29-kaf-delegation-transactional-delivery.md` 和已合并的 Task 1-4 代码里钉死；ITSM 侧再自建一张语义相近的表会产生两套 outbox 概念并存，违反 AGENTS.md"一个业务概念一个权威来源"的原则（Architecture Principles 一节）。内部消费（Watermill/Streams）与 KAF 消费（HMAC 签名 HTTP）是同一张表的两个独立 dispatcher 实现，通过 `event_type`/`aggregate_type` 区分路由，不是两套存储。

## 八、证据与关联文档索引

- [architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md) — 本计划的直接依据
- [architecture-and-roadmap-assessment-2026-08-26.md](../../architecture-and-roadmap-assessment-2026-08-26.md) — 审批收敛 P0-3 的原始跟踪记录
- [2026-08-26-unified-work-item-model-design.md](./2026-08-26-unified-work-item-model-design.md) §18.8 — WorkItem Phase 6 清理范围
- [2026-08-26-unified-work-item-multi-agent-execution-plan.md](./2026-08-26-unified-work-item-multi-agent-execution-plan.md) Wave 3 — ticket_number 撞号缺陷的原始记录
- [2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md](./2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md) — KAF 委派整体设计
- [2026-08-29-bpmn-async-service-task-design.md](./2026-08-29-bpmn-async-service-task-design.md) — 已实现的暂停型 service task 语义
- [../plans/2026-08-29-kaf-delegation-transactional-delivery.md](../plans/2026-08-29-kaf-delegation-transactional-delivery.md) — KAF 委派实施计划，Task 1-6 定义
- 远端进度证据（2026-08-30 现场核实，非文档，记录于本次会话）：
  - `192.168.31.66:/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`（分支 `feat/kaf-delegation-transactional-delivery`，Task 1-4 已提交，Task 6 进行中）
  - `192.168.31.66:/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`（KAF 侧 Task 5 已完成，33 测试通过）
