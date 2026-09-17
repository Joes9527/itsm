# WorkItem 后续 backlog 设计草案（DRAFT）

> 状态：**DRAFT / 未批准 / 未实现**。本文只为各项 backlog 给出问题、方向、备选、验收与待决问题；
> 不构成实现授权，不改变现有生产行为，不替代逐项独立设计与复审。
> 日期：2026-09-12。相关代码引用以当前分支为准。

## 0. 批准与推进规则

- 每项 backlog 独立走：草案 → 维护者/领域 owner 批准 → 正式 spec → 实现计划 → 隔离验证 → 独立复审。
- 不得合并为一次大波次；不得用本草案直接写生产代码或迁移。
- 涉及历史数据 backfill、凭据、恢复或清理的项，必须先定义回滚/补偿与真实证据。

---

## 1. BL-WI-PROCESS-AUDIT-CONTINUITY（I2）

### 1.1 问题

P 的 baseline 对 `preparationHistoricalTables` 中每张表的每行计算整行 digest：

- `itsm-backend/migration/work_item_preparation.go:24` 列出的表包括
  `process_instances`、`process_tasks`、`process_approval_decisions`、
  `process_callback_outboxes`、`audit_logs`、`change_status_events` 等。
- `preparationBaselineWithScope`（同文件 `:48`）对每行 `checksumSQL(data)`；
- R 的 `validateRetirementBaseline`（`work_item_controlled_retirement.go:201`）要求 P 时存在的行在 R 时仍完全一致。

这些表中的流程/任务/回调行是**活动执行记录**：正常任务完成、状态/变量/结束时间、
回调租约/attempt/完成都会改变。于是 P 之后正常的观察期推进会让 R 永久拒绝，
且刷新备份或重签名不能消除差异。一般在途流程退役能力因此未验收。

### 1.2 目标与非目标

- 目标：为“P 前已存在的活动流程”定义可验证的退役基线，使合法生命周期推进可通过 R，
  同时继续保持物理消失、跨租户/身份变化、不可变内容篡改、伪造证据 fail-closed。
- 非目标：不新增审批旁路、不做通用 `approved` 标记、不改写 P 回执、不引入第二套迁移/流程状态机、
  不把活动流程一律排除出退役范围。

### 1.3 建议方向：按表分类的“基线契约 v1”

在 P 证据中新增 `baseline_contract` 版本（仅描述策略，不新增业务表）：

| 类别 | 表 | 策略 |
|---|---|---|
| 不可变身份行 | `process_instances`、`process_tasks`、`process_approval_decisions`、`process_callback_outboxes` 的 identity 列 | identity/tenant/创建时间/业务键/定义 ID 必须精确相等；消失即拒绝 |
| 可变生命周期行 | 上述表的 status/end_time/variables/attempt/lease/last_error/completed_at 等 | 不比对整行；通过 owning 证据验证合法状态迁移 |
| 只追加证据 | `audit_logs`、`change_status_events`、`outbox_events` | 已有行不可变；允许新增；用 operationId/digest 验证 |
| 历史结构 | 022/027 退役对象 | 保持现有精确清单与拒绝逻辑 |

建议的逐表契约（草案）：

- `process_instances`
  - Immutable：`id`、`tenant_id`、`business_id`、`business_type`、`business_key`、
    `process_definition_id/key/version`、`created_at`（以实际列为准）。
  - Mutable：`status`、`end_time`、`current_task`、`variables` 等；仅允许通过
    `process_tasks` 完成事件、`change.*` 审计回执、outbox/callback 证据推进。
- `process_tasks`
  - Immutable：`id`、`tenant_id`、`process_instance_id`、`task_id`、`element_id`、
    `task_definition_key`、`created_at`。
  - Mutable：`status`、`assignee_id`、`completed_at`、任务变量；合法迁移必须对应
    真实的 task completion / callback / audit 回执。
- `process_callback_outboxes`
  - Immutable：`execution_key`、`tenant_id`、`actor_id`、`actor_source`、`task_id`、
    `callback_kind`、`handler_id`、`action`。
  - Mutable：`status`、`attempt_count`、lease/`last_error`、`completed_at`；
    允许 `pending → processing → completed` 与受控 `blocked`，终态不可回退；
    删除仅在存在对应专业回执且结果为 applied/effect_applied 时允许。
- 通用：任何 identity/tenant/不可变列变化、物理消失、证据断链、未知格式 → 拒绝。

### 1.4 备选与权衡

1. **仅冻结 identity + 允许状态迁移证据**（推荐）：安全性与可运维性平衡；需要逐表定义可变列与证据来源。
2. 保留整行 digest，但在 P 时排除活动行：实现简单，但无法退役 P 前活动流程，等于永久延期。
3. 由 R 重新采样并对差异逐项人工批准：不可自动化、易变成通用旁路，不推荐。

### 1.5 验收标准（未来）

- 真实创建 P 前 process/task/已入队 callback；
- P 后经所属运行入口推进（完成/重试/租约/变量变化）；
- 取得最终恢复点并执行 R 成功；
- 缺失/错租户/错身份/伪造/断裂证据、冻结载荷变化、物理消失仍拒绝；
- 事务失败回滚与幂等重试无重复效果；
- 同一二进制重复三阶段恢复 V1。

### 1.6 待决问题

- 各表真正不可变的列集合，需要领域 owner 逐列确认。
- variables JSON 的语义等价与规范序列化规则。
- 会签/多实例/重复活动元素的任务身份规则。
- 旧附件字节与摘要兼容；无法推导旧基线时的处置。
- 是否需要 P 时对 P 前活动行做一次性“冻结载荷”快照；若需要，其不可变性与恢复策略。

---

## 2. BL-WI-PURGE-AUDIT-01（物理清理与事务审计）

### 2.1 问题

- 当前领域删除为软删除（例如 `tickets.deleted_at/version`）；P/R 基线依赖保留行。
- HTTP 审计在业务事务之后写入，不能作为原子物理清理或“扩展已消失”的身份凭据。
- 缺少清理入口、执行者/时间/范围/摘要、唯一操作身份与同事务不可变结果。

### 2.2 建议方向：同事务 purge receipt + 两阶段对象清理

- 引入 owned-domain 清理入口（API 或运维 CLI），仅允许领域 owner 角色。
- 单个数据库事务内：
  1. 读取并摘要待清聚合（WorkItem/扩展/关系/附件元数据/流程/回执引用）；
  2. 写入不可变 `purge_receipts`（tenant、actor、reason、operationId、target identity、
     snapshot digest、结果、时间、前序 receipt 引用）；
  3. 按无 CASCADE/无通配符的精确顺序删除数据库行；物理附件先逻辑标记。
- 事务提交后异步删除对象存储附件；失败进入补偿队列，receipt 记录补偿状态；
  重试幂等。
- P/R 将该 receipt 视为合法消失证据；缺少 receipt 的消失仍拒绝。

### 2.3 备选

1. 同步数据库 purge + 异步对象清理（推荐）。
2. 先 tombstone，延迟窗口后 purge worker。
3. 两阶段人工审批 purge（合规场景）。

### 2.4 验收标准

- 软删除→purge→R 成功；缺失/伪造/跨租户 receipt 拒绝；
- 对象删除失败/重试/补偿状态可观察且幂等；
- 保留期/legal hold 规则；审计与权限真实 PG 验证；
- HTTP 日志或自签声明不能替代事务审计。

### 2.5 待决问题

- legal hold、保留期与“被遗忘权”冲突处理。
- 对象存储是否支持事务/幂等删除；跨系统补偿边界。
- purge 是否允许批量；批量时 receipt 的粒度与顺序。

---

## 3. BL-CHG-WO-01（Change 多 WorkOrder）

### 3.1 问题

当前 Change 是单一专业扩展，缺少“一个 Change 下多个 WorkOrder”的模型、任务规划、
审批、执行与回滚语义。产品需要多 WorkOrder 时不能靠并行生命周期 hack。

### 3.2 建议方向

- 在 Change 下引入 `change_work_orders` 子实体（不新建生命周期引擎）：
  - identity/tenant/change_id、标题、范围、计划窗口、状态、版本、审计。
- BPMN 仍为编排层：Change 级审批 gate + WorkOrder 多实例 user tasks；
  任务完成回执与现有 `change.task_completion`/callback 模式一致。
- 状态汇总规则：Change 状态由 Change 级 gate 与全部 WorkOrder 状态推导；
  任一 WorkOrder blocked 需显式处置。
- 执行/取消/回滚按 WorkOrder 精确清单，沿用 RESTRICT 与回执。

### 3.3 备选

1. Change 子表 + BPMN 多实例（推荐）。
2. 用 WorkItem relations 连接多个 Change（不推荐：身份/生命周期歧义）。
3. 每个 WorkOrder 独立 Change（不推荐：SLA/审批/回滚碎片化）。

### 3.4 验收标准

- 创建/规划/执行/取消/回滚/幂等/权限/审计的真实 PG + 前端旅程；
- Change 级 approval 与 WorkOrder 级任务的边界测试；
- SLA/通知/附件/流程回执与现有合同一致。

### 3.5 待决问题

- WorkOrder 粒度与 Change 专业扩展字段的归属。
- 审批是 Change 级还是 WorkOrder 级，或二者组合。
- 部分成功/部分回滚的业务语义。

---

## 4. BL-RESP-01（首响测量）

### 4.1 问题

- 现有 `tickets.first_response_at` 与 `handlers/sla/` 服务参与 SLA 计算；
- Change 在 assess 时会设置 `first_response_at`（`handlers/change/commands.go:289`）；
- 决策要求：assignment/start 不应隐式计为 response；首响定义、去重与重开口径未定义。

### 4.2 建议方向

- 定义“首次人工响应事件”：按领域明确 eligible action（如面向请求人的实质回复/状态响应），
  与 assignment、internal note、系统通知区分。
- 新增 `ticket_response_events`（tenant、work_item_id、actor、channel、action、
  operationId、occurred_at、digest），同事务写入；`first_response_at` 作为投影缓存。
- SLA 计算改用事件；重开/重派/多响应按明确规则去重；提供历史数据 backfill/对账策略。

### 4.3 备选

1. 显式 response event 表 + 投影（推荐）。
2. 从 audit_logs 按 action 集合推导（实现快，但口径脆弱）。
3. 保留列，只修改写入点（最小改动，但缺少审计与去重依据）。

### 4.4 验收标准

- assignment/start 不设置首响；合格响应只记一次；重开周期按既有 SLA 规则；
- 报表/平均响应与事件一致；幂等与权限/租户验证。

### 4.5 待决问题

- 各领域哪些动作算首响；渠道差异；系统自动回复是否计。
- 历史 `first_response_at` 的 backfill 是否执行、如何对账。

---

## 5. 未注册独立 RCA SQL

### 5.1 问题

- `itsm-backend/migrations/20260909_problem_rca_authority.sql` 存在，但未注册到普通迁移流；
- 文件含 backfill/drop，直接执行有历史数据风险；033 实际为 Incident status events，
  不能伪造 033 回执替代。
- RCA 路由与测试存在（`service/problem_rca_authority_test.go`、
  `router/problem_rca_routes_test.go`），部署缺口可能影响可用性。

### 5.2 建议方向

- 先确认当前 RCA 路由/服务是否仍需要该 SQL 的结构或数据。
- 若只需结构：注册为新的、幂等的、forward-only 编号迁移（不 backfill、不 drop 历史语义）；
- 若需数据变换：拆成独立数据迁移，提供 dry-run 报告、映射规则、对账与回滚/恢复方案；
- 绝不保留“手工执行未注册 SQL”作为部署路径。

### 5.3 验收标准

- 新/空与既存目标都能按目录顺序应用；checksum/回执真实；
- RCA 路由端到端 + 权限/租户；回滚/恢复演练；
- 历史数据 backfill（若有）有逐项对账证据与维护者批准。

### 5.4 待决问题

- 该 SQL 的 drop/backfill 是否仍必要；历史数据量与保留要求。
- 迁移编号分配与目录 revision。

---

## 6. HMAC provider secret/nonce 恢复

### 6.1 问题

- `handlers/intake/identity_exchange_service.go` 使用 HMAC provider 签名 +
  `NonceStore`（Redis `SetNX`，键 `intake:identity-exchange:nonce:*`）防重放。
- 现有恢复只验证“显式空 provider 且真实拒绝交换”，未覆盖启用 provider 的
  secret/nonce 恢复；恢复后重放风险未验证。

### 6.2 建议方向

- 引入 provider secret 版本/key id：请求携带 key id，服务端按版本校验；
  轮换时保留可配置 grace 窗口。
- 恢复语义：nonce store 与业务库同一恢复点；若无法精确恢复 nonce，
  必须 fail-closed 或要求使用新 key id 重新签名，不能静默接受旧 nonce。
- 恢复演练验证：旧 secret、恢复点后 secret、重放、跨租户/跨 provider 均拒绝；
  轮换后新请求成功。

### 6.3 备选

1. nonce store 纳入备份/恢复（推荐）。
2. 恢复后强制轮换 provider secret + 拒绝旧签名（更安全但需外部协调）。
3. 混合：新 key id 立即生效，旧 key id 仅 grace 窗口。

### 6.4 验收标准

- 启用 HMAC provider 的恢复演练；重放/篡改/错 provider/错租户拒绝；
- secret 轮换审计；恢复后旧/新会话行为明确；
- 目标环境 secret 管理流程文档化。

### 6.5 待决问题

- 外部 provider 是否支持 key id/grace 窗口。
- nonce 记录 TTL 与恢复点时间差的最大容忍窗口。

---

## 7. Redis 冷启动撤销降级加固

### 7.1 问题

- 启动日志显示 Redis 连接失败时 rate limiter 使用内存 fallback
  （`internal/bootstrap/app.go:871`、`:880`）；
- 认证/access 撤销在 Redis 冷启动失败时可能退化到内存存储，导致撤销短暂失效，
  且缺少显式 fail-closed 或 durable fallback。

### 7.2 建议方向

- 抽象 revocation store，并区分 strict/degraded 模式：
  - strict（生产默认）：Redis/revocation 依赖未就绪则启动失败或拒绝鉴权；
  - degraded：仅在显式配置下启用，使用数据库持久化 revocation 表，并对所有拒绝
    决定写审计/指标；恢复后对账。
- 禁止静默使用纯内存撤销作为生产降级；至少要求告警+审计+定期对账。
- 故障注入验证：Redis down/restart/冷启动时 revoked token 仍被拒绝。

### 7.3 备选

1. strict fail-closed（推荐）。
2. 数据库持久化 revocation fallback（可用性更好，成本更高）。
3. 内存降级 + 强告警（不推荐作为最终状态）。

### 7.4 验收标准

- Redis 不可用时 revoked access 拒绝或服务不可用，绝不静默放行；
- 恢复后状态一致；审计/指标可观测；无重复撤销副作用。

### 7.5 待决问题

- 生产可接受的可用性/安全权衡。
- DB fallback 的读放大与 TTL 清理策略。

---

## 8. 匿名卷归属与清理策略

### 8.1 问题

早期探索存在无法归属的匿名卷；治理禁止猜删/prune，因此长期披露为残留不确定性。

### 8.2 建议方向（运维策略，非功能 backlog）

- 新规则：所有 disposable 资源必须带 `com.itsm.test.owner` 与 run-id 标签；
  清理只按精确 ID/名称，禁止通配符与 prune。
- 归属流程：对照创建日志、容器 inspect、W/运行台账、镜像/端口/命令指纹；
  能形成证据链才清理，否则继续披露。
- 每次运行在 `resource-ownership.json`/cleanup receipt 中记录容器与卷 ID；
  清理后逐项核验不存在。

### 8.3 验收标准

- 不再产生新的无标签 disposable 卷；
- 可归属资源按精确 ID 清理并记录；不可归属资源保持披露；
- 不触碰 shared/default 与其他任务资源。

### 8.4 待决问题

- 历史卷是否有足够日志可归属；若无，保留披露是否可接受。

---

## 9. BL-CTI-01（AI 分类建议与三级分类节点 ID 契约）

### 9.1 问题

`TicketDetail` 的"采纳 AI 建议"把 AI 返回的**分类显示名**直接写进工单编辑载荷（`{ category: suggestion.category, priority }`），
而 AI triage 返回的分类是硬编码英文关键词（`handlers/ai/service.go`：network/software/hardware/access/printer/email/general），
前端类型也只有 `category: string`（`src/lib/api/ai-api.ts`），**没有任何 CTI 节点 ID**。

在 CTI 治理契约下（分类只接受最深节点 ID、编辑边界拒绝未知字段），该路径必然失败：

- 改动前：按名称解析 → 目标不存在（共享 dev 库 183 个分类中，与这些英文名完全同名者 **0** 个）→ 整单更新失败；
- 改动后：编辑边界拒绝未知字段 → 同样整单失败（仅错误信息不同）。

因此这是**既有不可用功能**而非回归；但 AI 建议与分类契约之间的缺口需要显式规划，避免长期停留在"点了必然失败"的状态。

### 9.2 目标与非目标

- 目标：AI 建议给出可执行的分类目标（节点 ID），采纳时走与其它入口相同的 ID 契约并记录原因与来源（模型/提示版本）。
- 非目标：不让 AI 绕过权限或完成门禁；不新增第二套关键词分类器；不改写历史工单分类。

### 9.3 建议方向

1. AI triage 的结构化输出增加 `categoryId`（在建议阶段用既有分类服务解析并校验），同时保留展示名与置信度；
2. 前端采纳时提交 `categoryId` + `classificationReason`（例如"采纳 AI 建议：<模型/提示版本>"），复用 `TicketEditPayload`；
3. 比较与展示改用节点 ID（展示可用名称），去掉按名称比较；
4. AI 无法给出合法节点时，明确降级为"需要人工选择分类"，不产生任何写入。

### 9.4 备选

- UI 侧按名称反查分类树：会重新引入同名歧义，仅可作为"人工确认"的临时手段；
- 短期失败关闭降级：采纳动作只处理优先级，分类部分给出明确提示（不静默忽略）。

### 9.5 验收标准（未来）

- 采纳后工单分类等于 AI 建议的节点 ID（三级路径合法、同租户）；回执含原因、前后路径与模型/提示版本；
- 无匹配节点时给出明确的人工选择提示，且**没有任何写入**；
- 同名分类场景下建议结果按 ID 唯一确定。

### 9.6 待决问题

- 节点 ID 解析由 AI 服务负责，还是由 triage 调用方（ticket/intake）负责？
- 低置信度建议是否允许一键采纳，或必须人工确认？
- 建议卡片是否需要展示置信度与匹配依据以便审计？

---

## 10. 下一步

1. 由维护者/领域 owner 逐项审阅本草案，选择方向并关闭待决问题。
2. 每项通过后单独产出 `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md` 正式 spec。
3. 再通过 `writing-plans` 生成实现计划与 TDD 步骤；未经批准不写生产代码/迁移。
