# Work Item 详情页能力对齐设计（Incident/Problem/Change 补齐 SLA/评论/附件/历史/关系）

- 日期：2026-08-28
- 状态：设计阶段，待审阅
- 依赖：`docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md`（下称"主 spec"）、
  `AGENTS.md` §Unified Work Item Domain Contract

## 1. 背景与目标

Wave1-3 已经把 Incident/Problem/Change 迁移到统一 WorkItem 模型（`record_class`/`work_item_id`
落地，`check_work_item_integrity` 验证通过），但迁移只完成了**数据层**收敛，**详情页 UI**
仍然停留在专业域各自为战的状态：

- `ProblemDetail.tsx`（172 行）、`ChangeDetail.tsx`（547 行）没有 SLA 卡片、没有专属评论/附件
  Tab、没有组件内的历史时间线，关系展示分散甚至重复（`ProblemInvestigationTab.tsx` 和
  `ProblemAssociationsTab.tsx` 都渲染关联关系）。
- `IncidentDetail.tsx`（1227 行）体量接近 `TicketDetail.tsx`，但评论走的是自己独立的
  `incident_events` 表（`incidentCommentAdapter`），不是统一的 `ticket_comments`。
- `WorkItemShell.tsx` 的 doc comment 已经写明要提供"编号/标题/状态/优先级/请求人/分派/SLA/
  评论/附件"，但实现只画了编号/标题/状态/优先级/处理人，`sla` prop 全程未渲染，
  `WorkItemComments`/`WorkItemAttachments` 是纯占位组件，三个页面（incidents/problems/
  changes `[id]/page.tsx`）传的 `actions={{}}` 也从未接上真实后端权限。

主 spec §17.3 对目标状态已经有明确定义："统一 WorkItem Shell 提供：编号、标题、状态、优先级、
请求人、分派；SLA、流程、评论、附件、时间线、关系；后端计算的操作权限。专业 Tab/Panel 提供
Incident、Problem、Change 或 Requested Item 字段。保留专业 URL，复用 Shell，**不复制公共详情
实现**。"

本设计的目标就是把现状收敛到这句话描述的状态，不引入新的平行实现。

## 2. 现状审计

### 2.1 能力矩阵（对照 `TicketDetail.tsx`）

| 能力 | Ticket | Incident | Problem | Change |
|---|---|---|---|---|
| SLA 卡片 | 有（`TicketApi.getTicketSLA`） | 无 | 无（`slaStatus` 字段存在于 `lib/api/problem-api.ts` 但后端从不写入，纯前端占位） | 无 |
| 评论 | 有，走 `ticket_comments`（`ticketCommentAdapter`） | 有，但走独立的 `incident_events` 表（`incidentCommentAdapter`），无 update，无 isInternal/mentions | 无 | 无 |
| 附件 | 有 | 无 | 无 | 无 |
| 历史/时间线 | 组件内置（`TicketHistoryList`） | 页面级 `HistoryTimeline`（走 `audit-log-history-adapter`） | 页面级 `HistoryTimeline` | 页面级 `HistoryTimeline` |
| 关系 | 组件内置（`TicketRelationCards`） | 页面级 | 页面级 `ProblemAssociationsTab` **加上** `ProblemInvestigationTab` 内重复的 relationships tab | 无 |
| 统一操作权限（`actions.*.allowed/reason`） | 有 | 无（页面传 `actions={{}}`） | 无（同上） | 无（同上） |
| 专业能力（Ticket 没有） | — | 大事件升级、监控告警 | 调查/根因/方案/知识库 | 风险评估/影响分析/回滚计划/PIR |

### 2.2 关键发现：后端接口和权限模型

- `GET /api/v1/tickets/:id/{comments,attachments,history,relations,sla}` 这五条路由**都已经是
  WorkItem 统一接口**，用的是 `tickets.id`（= `workItemId`），不是"专属 Ticket 记录"的概念——
  Problem/Change 的 `work_item_id` 就指向这些行。理论上不需要为 Problem/Change 新建任何后端
  表或接口，直接用 `workItemId` 调用这五条既有路由即可。
- 但这五条路由在 `router/router.go` 里全部挂的是 `middleware.RequirePermission("ticket", "read"/"create"/...)`——
  静态资源名，路由注册时写死。如果 Problem/Change 详情页直接复用，一个只有 `problem:read`/
  `change:read`、没有 `ticket:read` 权限的用户会被这几条路由 403 挡住。这是本设计要解决的
  第一个硬约束（见 §4.1）。
- Incident 的评论已经有独立实现（`incident_controller.go` 的 `GetIncidentComments`/
  `CreateIncidentComment`/`DeleteIncidentComment`，存到 `incident_events` 表，
  `event_type="comment"`）。附件、历史、关系 Incident 完全没有自己的实现。
- `AGENTS.md`："Prefer architectural refactoring over compatibility layers... When a new path
  replaces an old path, remove the old path in the same change unless backward compatibility is
  an explicit requirement." 主 spec §18.1："不新增平行业务路由...生产业务代码不长期双写。"
  → Incident 独立评论系统必须收口进统一 `ticket_comments`，不能长期两套并存（用户已确认，见
  §5 数据迁移方案）。

### 2.3 已经存在、可以直接复用的基础设施

- `components/business/detail-tabs/`：`CommentPanel`/`AttachmentPanel`/`HistoryTimeline` 是
  domain-agnostic 组件，靠 `adapter` prop 接数据源；`types.ts` 的 `TargetType` 已经列了
  `'ticket' | 'incident' | 'problem' | 'change' | 'release'` 五个值。`ticket-comment-adapter.ts`、
  `incident-comment-adapter.ts` 已经落地，模式已验证。
- `TicketAttachmentGrid`/`TicketHistoryList`/`TicketRelationCards` 三个组件的 props 只有
  `ticketId: number`（外加个别可选项），不依赖任何 Ticket 专属的类型或状态，可以直接传
  `workItemId` 复用，不需要新组件。
- `WorkItemTypes.ts` 已经定义了 `WorkItemSLAState`、`WorkItemActionState`、
  `WorkItemActionDispatch`，`WorkItemShellProps` 已经接了 `sla`/`actions`/`onActionDispatch`
  三个 prop——数据通路已经设计好，只是没人填数据、没人渲染。

### 2.4 已处理的相关项（本次会话前序）

- `components/problem/EnhancedProblemDetail.tsx`（755 行孤儿组件）已删除——它的能力和
  `ProblemInvestigationTab`/`ProblemAssociationsTab` 重复，不是本设计要补的缺口。
- `ProblemDetail.tsx` 的 `problem as unknown as Problem` 不安全类型强转已修复（改为直接使用
  `lib/api/problem-api` 的 `Problem` 类型）。

## 3. 设计原则

1. **不新建平行接口。** Problem/Change 的评论/附件/历史/关系/SLA 全部复用现有
   `/api/v1/tickets/:id/*` 路由，传 `workItemId`。
2. **不长期保留 Incident 的独立评论实现。** 本次一并回填历史数据、切前端 adapter、退役旧
   controller/路由，不留双写或双读的过渡状态在生产代码里长期存在。
3. **WorkItemShell 是唯一详情页公共壳，不复制。** SLA/History/Relations 三个新区块加进
   `WorkItemShell.tsx`（或其子组件），Incident/Problem/Change 三个页面不再各自实现等价逻辑。
4. **权限判断跟着数据的实际归属走，不跟着"这条路由历史上是给谁写的"走。** 新增
   `RequireWorkItemRecordClassPermission`，在请求时按 `tickets.record_class` 动态解析资源名。
5. **专业 Panel 保留专业能力。** Change 的风险评估/影响分析/回滚计划/PIR、Problem 的
   调查/根因/方案/知识库、Incident 的大事件升级/监控告警——这些是 Ticket 没有的真实专业能力，
   不动、不合并进 Shell。

## 4. 后端变更

### 4.1 `RequireWorkItemRecordClassPermission` 中间件

新文件或加进 `middleware/rbac.go`：

```go
// RequireWorkItemRecordClassPermission 按路径参数 :id 对应 tickets 行的实际 record_class
// 动态解析资源名，再复用现有的 hasResourcePermission。用于 WorkItem 级共享接口
// （comments/attachments/history/relations/sla），因为同一条路由现在可能服务 Ticket、
// Incident、Problem 或 Change 四种专业域，静态 RequirePermission("ticket", action) 会
// 错误地要求非 Ticket 域的查看者也必须有 ticket 权限。
func RequireWorkItemRecordClassPermission(action string) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 取 role/tenant_id/client（同 RequirePermission 现有逻辑）
        // 2. 解析 :id，查 tickets.record_class（tenant-scoped）
        // 3. record_class -> resource 映射：
        //      "incident"       -> "incident"
        //      "problem"        -> "problem"
        //      "change_request" -> "change"
        //      其余（generic/service_request_item/catalog_task） -> "ticket"
        // 4. hasResourcePermission(client, role, resolvedResource, action, tenantID)
        // 5. 查不到该 ticket（不存在或跨租户）-> 404，不是 403，避免探测其它租户 ID 是否存在
    }
}
```

替换 `router/router.go` 里以下五条路由的权限中间件（保持路径/方法/controller 不变，只换
`middleware.RequirePermission("ticket", X)` → `middleware.RequireWorkItemRecordClassPermission(X)`）：

```
tickets.GET    /:id/comments
tickets.POST   /:id/comments
tickets.PUT    /:id/comments/:comment_id
tickets.DELETE /:id/comments/:comment_id
tickets.GET    /:id/attachments
tickets.POST   /:id/attachments
tickets.GET    /:id/attachments/:attachment_id
tickets.GET    /:id/attachments/:attachment_id/preview
tickets.DELETE /:id/attachments/:attachment_id
tickets.GET    /:id/history
tickets.GET    /:id/relations
tickets.GET    /:id/relations/stats
tickets.GET    /:id/sla
```

其余 `tickets.*` 路由（创建、专属 Ticket 字段更新等）保持 `RequirePermission("ticket", ...)`
不变——只有"WorkItem 共享能力"这一类接口才需要按 recordClass 动态判权限，Ticket 自己的
CRUD 不受影响。

### 4.2 Incident 评论迁移

1. **回填工具** `cmd/backfill_incident_comments`（镜像 `cmd/backfill_incident_work_item` 的形状）：
   遍历 `IncidentEvent` 中 `event_type="comment"` 的行，按 `incident.work_item_id` 写入
   `ticket_comments`（保留原 `user_id`/`content`/`created_at`；`is_internal`/`mentions` 用
   默认值，因为旧数据本来就没有这两个字段）。`-dry-run` 默认开启，tenant-scoped，可重复执行——
   幂等性通过写入前查重实现：`ticket_comments` 里已存在同一
   `(ticket_id, user_id, content, created_at)` 组合的行就跳过，不新增列、不改
   `IncidentEvent`/`ticket_comments` 的 schema。
2. **前端切换**：`WorkItemComments` 对 Incident 也走 `ticketCommentAdapter` + `workItemId`，
   和 Problem/Change 统一。
3. **retire 旧接口**（必须最后做，前端不再依赖旧接口之后才能删，否则中间状态会导致评论区读不到
   数据）：删除 `incident_controller.go` 的 `GetIncidentComments`/`CreateIncidentComment`/
   （如有 `DeleteIncidentComment`），删除对应路由注册，删除 `incident-comment-adapter.ts`。

## 5. 前端变更

### 5.1 `WorkItemComments` / `WorkItemAttachments` 改造

两个组件从占位实现改成"按 `recordClass` 选 adapter，渲染通用 `CommentPanel`/
`AttachmentPanel`"。回填完成、Incident 旧接口退役后，四个 recordClass 统一用
`ticketCommentAdapter`/`ticketAttachmentAdapter`，不再需要按 recordClass 分支——这是本设计
唯一在完成 §4.2 之后可以被简化掉的分支逻辑。

### 5.2 `WorkItemShell.tsx` 新增区块

- **SLA 卡片**：新增 `WorkItemSLA` 组件，消费 `WorkItemShellProps.sla`（`WorkItemSLAState`）。
  样式对齐 `TicketDetail.tsx` 现有的 SLA 卡片（响应/解决倒计时 + 超时高亮），不新建视觉规范。
- **History 面板**：直接复用 `TicketHistoryList`，传 `ticketId={workItem.id}`。
- **Relations 面板**：直接复用 `TicketRelationCards`，传 `ticketId={workItem.id}`。
- **操作栏**：`WorkItemShell` 已经通过 `WorkItemContext` 把 `actions`/`onActionDispatch` 传给
  子树，但目前没有任何组件真正读取渲染。新增一个操作栏区块，遍历 `actions` 渲染按钮，
  `disabled`/`title` 取 `allowed`/`reason`，点击调用 `onActionDispatch`。

### 5.3 三个页面补数据组装

`app/(main)/{incidents,problems,changes}/[id]/page.tsx` 目前 `sla` 完全不传、
`actions={{}}` 是硬编码空对象。改为：

- 拉取 `workItem.id` 对应的 `/tickets/:id/sla`，映射成 `WorkItemSLAState` 传给 `WorkItemShell`。
- 后端需要每个专业 controller（Incident/Problem/Change）在返回详情时计算并暴露真实的
  `actions` 映射（allowed/reason），而不是前端猜权限。这部分需要专业 service 层配合——不在
  本设计的前端改动范围内，作为本设计的一个后端子任务列在 §7 阶段划分里。

## 6. 明确不做的事

- 不新建 Problem/Change 专属的评论/附件/历史/关系数据库表或 controller。
- 不触碰 Change 的风险评估/影响分析/回滚计划/PIR，不触碰 Problem 的调查/方案/知识库，不触碰
  Incident 的大事件升级/监控告警——这些是真实专业能力，不是本次要收敛的重复实现。
- 不处理 `ticket_number` 跨域冲突（已有独立 backlog）。
- 不改变 `tickets` 物理表名（已有独立倾向性结论：不改名）。
- 不新建 `ProblemSLACard.tsx` 之外的 SLA 计算逻辑——`slaStatus` 等字段本来就是死字段，SLA
  统一从 `/tickets/:id/sla` 拿。`ProblemSLACard.tsx` 是否保留/删除，视 §7 实施时是否还有
  调用方而定，不在本设计里预先下结论。

## 7. 阶段划分

1. **后端**：`RequireWorkItemRecordClassPermission` 中间件 + 单测（四种 recordClass 权限矩阵）+
   替换五条路由 + 集成测试（非 ticket 权限用户可读自己域的评论/附件/历史/关系/SLA，读不了别的域）。
2. **Incident 评论迁移**：回填工具（dry-run 验证 + 实际回填）→ 前端切 adapter → 退役旧接口
   （顺序不能反，避免回填前旧数据不可读）。
3. **前端 WorkItemShell**：SLA/History/Relations 区块 + 操作栏，三个页面补 `sla` 组装。
4. **后端 actions 计算**：Incident/Problem/Change 专业 service 计算真实 `actions` 映射
   （需要先盘点每个域有哪些状态相关操作，工作量待评估，可能需要拆成独立 spec）。
5. 每阶段独立验证（`go build`/`go vet`/`go test ./...`、`npm run type-check`、相关集成测试），
   不合并到一个大 PR。

## 8. 测试计划

- 后端：`RequireWorkItemRecordClassPermission` 单测覆盖四种 recordClass × 有权限/无权限矩阵；
  回填工具的 dry-run/幂等性测试；至少一条集成测试验证 Problem/Change 通过 workItemId 能读到
  评论/附件/历史/关系/SLA。
- 前端：`npm run type-check` + `WorkItemShell` 新区块的渲染测试（有 sla/无 sla、有
  actions/无 actions 的空态）；回归 `TicketDetail.tsx`/`IncidentDetail.tsx` 现有测试不受影响。
