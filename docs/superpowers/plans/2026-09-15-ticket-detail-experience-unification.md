# Ticket Detail Experience Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 统一 Ticket 详情刷新，保护编辑上下文，并将活动任务与已有历史任务清晰分组。

**Architecture:** 保留面板和 adapter 的数据所有权，在页面范围协调其已有读取。useDetailResource 收敛读取结果与并发语义；审批决策在同一详情上下文单次读取。BPMN 命令和授权仍由后端负责。

**Tech Stack:** Next.js 15、React 19、TypeScript、Ant Design 6、现有 Zustand 身份状态、Jest/RTL、TypeScript Playwright。Node >=22、npm >=10，不新增依赖。

**Spec:** [2026-09-15-ticket-detail-experience-unification-design.md](../specs/2026-09-15-ticket-detail-experience-unification-design.md)

**Status:** accepted（Tasks 1–5 已实现并逐项审阅；Task 6 浏览器验收完成，最终独立审查与 PR 待完成）

## Global Constraints

- 不增加申请人全流程视图、节点预测、轮询/WebSocket、新审批引擎、任务命令、数据库迁移、共享环境写入或全仓主题重构。
- 不扩大任务可见范围，不按角色名或任务名推导动作，不增加领域状态机。无任务结果不等同于无流程或流程完成。
- 切换工单/账号/租户及权限失效不适用保留旧草稿承诺：继续清理旧身份内容和编辑状态，迟到请求不得恢复它们。
- 不创建全局 Manager/事件总线/第二套缓存；业务数据及计数继续使用唯一读取结果。
- 9 月 14 日的撤权、并发、版本、幂等及已确认写入/读取分离语义不得回退。
- 不把计划代码片段当作完整实现；以下接口为任务间约定，测试示例必须落入真实渲染/请求行为测试。

## 工作区与交付

基线 `dcc37fee`；设计提交 `ee6bf1d2`，文档分支 `codex/docs/ticket-detail-experience`。执行时从最新 main 创建独立 worktree 与 `codex/fix/ticket-detail-experience`；携入经审阅的设计/计划提交，先对比后续改动。不修改当前主工作区其他任务，不执行共享数据库写入。每个任务独立测试和提交，最终一个聚焦 PR；本计划不授权部署/合并。

命令默认在实施 worktree 的 `itsm-frontend/` 执行。基线运行现有 TicketDetail、TicketProcessTasks、TicketCommentStream、AttachmentPanel 和 WorkItemShell 测试，区分既有失败与新回归。Windows 环境通过 WSL 执行 Linux Node，不把 Windows node_modules 写入 Linux 工程。

## 文件与接口地图

| 文件（均相对 itsm-frontend/src） | 职责 |
| --- | --- |
| components/business/detail-tabs/useDetailResource.ts | 扩展现有读取回执、并发及写后读取语义 |
| components/business/detail-tabs/DetailRefreshContext.tsx（新增） | 仅页面范围的读取协调，跨共享面板复用 |
| components/business/detail-tabs/DetailReadState.tsx | 页面协调模式下仅显示失败重试；独立消费保留正常刷新 |
| components/ticket/useTicketDetailResource.ts（新增） | 从 TicketDetail 提取主体读取；分离首次加载和后台更新 |
| components/ticket/TicketDetail.tsx | 页面身份、协调 provider、统一按钮/快捷键及组合 |
| components/business/detail-tabs/ApprovalDecisionHistoryContext.tsx（新增） | 同一详情单一审批决策来源 |
| components/business/detail-tabs/useApprovalDecisionHistory.ts | 将已有读取置于对应页面 provider，保留单一解析逻辑 |
| components/ticket/TicketProcessTasks.tsx | 活动/历史呈现、动作后定向更新 |

具体共享消费者及测试列在各任务，不另增同业务 API。

### 协调接口（Task 1 产出，Task 2–5 消费）

```ts
export type DetailReadResult =
  | { status: 'success' }
  | { status: 'error'; message: string; denied: boolean }
  | { status: 'discarded' };
export type DetailReloadOptions = { afterWrite?: boolean };
export type DetailReload = (options?: DetailReloadOptions) => Promise<DetailReadResult>;
export type DetailRefreshEntry = {
  key: string; // 真实查询语义；当前页面 identity 由 provider 隔离
  label: string;
  reload: DetailReload;
  isWriting: () => boolean;
};
export type DetailRefreshReport = {
  succeeded: string[];
  failed: { key: string; label: string; message: string }[];
  skipped: string[]; // 写入中、卸载或旧上下文，不算成功
};
export type DetailRefreshController = {
  register: (entry: DetailRefreshEntry) => () => void;
  refresh: (keys?: readonly string[], options?: DetailReloadOptions) => Promise<DetailRefreshReport>;
  busy: boolean;
  report?: DetailRefreshReport;
};
```

`DetailRefreshProvider` 接受 `{ identity: string; children: React.ReactNode }`；`useDetailRefresh()` 返回 controller 或 undefined。`useDetailRefreshEntry(entry: DetailRefreshEntry | undefined): void` 负责注册/更新回调和卸载。未注册资源不抓取、不伪造成功；本计划选择重读已挂载的隐藏 Tab，未挂载 Tab 首次打开读取，不再新增隐藏 Tab 的脏缓存。

## Task 1：可观察读取结果与页面协调

**Files:** 修改 `components/business/detail-tabs/useDetailResource.ts`；新增 `DetailRefreshContext.tsx` 和相邻 `__tests__/useDetailResource.test.tsx`、`__tests__/DetailRefreshContext.test.tsx`。

**Consumes:** 现有 API load/count/onCountChange、身份归属、capture/deny。
**Produces:** 上述读取和协调接口；现有 reload 改为 DetailReload，未提供 options 的调用保持读取语义。

- [x] 写真实 hook 回归，覆盖同身份并发合并、错误回执、撤权清除和切换身份废弃结果。复用 auth-store fixture，不 mock 被测 hook。最小并发断言：

```ts
const load = jest.fn(async () => ['one']);
const { result } = renderHook(() => useDetailResource(42, load, rows => rows.length));
await waitFor(() => expect(result.current.ready).toBe(true));
load.mockClear();
await act(async () => {
  const outcomes = await Promise.all([result.current.reload(), result.current.reload()]);
  expect(outcomes).toEqual([{ status: 'success' }, { status: 'success' }]);
});
expect(load).toHaveBeenCalledTimes(1);
```

- [x] 运行 `npx jest --runInBand --coverage=false --runTestsByPath src/components/business/detail-tabs/__tests__/useDetailResource.test.tsx`，确认失败原因是未实现的并发/回执行为。
- [x] 在现有 hook 增加 identity 范围的 in-flight 引用。普通 reload 复用当前读取；afterWrite 发起新一代读取，旧响应废弃；同一批写后更新仍只调用每个 entry 一次。finally 仅清理属于该请求的 in-flight 引用，避免旧请求清除新请求。
- [x] 实现 provider，仅保存 entry 回调、批次身份和结果，不保存 data。批次内 key 去重；写入中 skip；所有分支最终解除 busy；换 identity 后不应用旧 report。用以下回执分支映射，不用 Promise fulfilled 等同成功：

```ts
if (outcome.status === 'success') report.succeeded.push(entry.key);
else if (outcome.status === 'error') report.failed.push({ key: entry.key, label: entry.label, message: outcome.message });
else report.skipped.push(entry.key);
```

- [x] 增加 provider 测试：两个 keys 一成一败、重复点击、卸载、identity 切换、writing skip，以及写前请求迟到不能覆盖写后结果。
- [x] 运行两个新增测试及现有使用 useDetailResource 的面板测试，确认类型兼容后提交 `fix(detail): coordinate scoped refresh and report read outcomes`。

## Task 2：主体后台更新、统一入口和读取消费者接入

**Files:** 新增 `components/ticket/useTicketDetailResource.ts`；修改 `TicketDetail.tsx`、`TicketCommentStream.tsx`、`TicketRelationCards.tsx`、`TicketHistoryList.tsx`、`ServiceRequestPanel.tsx`、`ServiceCatalogApprovalChain.tsx`；修改 `components/business/TicketNotificationSection.tsx`、`components/business/detail-tabs/AttachmentPanel.tsx`、`CommentPanel.tsx`、`DetailReadState.tsx`。修改相邻现有测试，新增 `components/ticket/__tests__/TicketDetailRefresh.test.tsx`。

**Consumes:** Task 1 的 provider/register/result/reload。
**Produces:** 页头唯一正常刷新、Alt+R、主体读写保护；所有已加载纯读取区域参与协调。

- [x] 在组合测试中保留真实评论编辑器和状态组件，仅 mock API；新增以下用户行为断言，并先运行观察失败：

```ts
await user.type(screen.getByRole('textbox', { name: /评论/ }), '未提交草稿');
await user.click(screen.getByRole('button', { name: '刷新工单详情' }));
expect(screen.getByRole('textbox', { name: /评论/ })).toHaveValue('未提交草稿');
expect(screen.queryByText('加载失败')).not.toBeInTheDocument();
```

如现有编辑器不是 textbox，使用其真实可访问角色补齐 label；不能为了断言替换成假编辑器。
- [x] 将主体读取移入 hook，原 fetchTicket 消费该唯一来源；页面仅在首次无数据时 Skeleton。后台错误独立提示，最终拒绝清除详情。provider 覆盖主体与子区域，identity 包含现有用户/租户/工单身份；权限变更继续走 auth 的失效规则。
- [x] 注册主体、SLA、评论、附件、关系、历史、通知、服务申请、目录审批链及任务（Task 5 完成动作接线）；键使用对应查询语义，如 `ticket`、`sla`、`comments`、`attachments`、`relations`、`history`、`notifications`、`service-request`、`catalog-approval-chain`、`process-tasks`。只在对应读取已初始化且获现有读取授权时注册。
- [x] 未使用 useDetailResource 的上述消费者收敛到已有 hook 或返回同一结果接口，删除被替代的旧 effect/load 状态；不保留两次首次读取。AI 建议生成不注册为普通 reload，不因手动刷新再次触发生成。保留其现有显式交互。
- [x] `DetailReadState` 以是否处在刷新上下文确定正常入口，实际判断为：

```tsx
const coordinated = useDetailRefresh() !== undefined;
const showReadAction = !!error || !coordinated;
// error 继续显示 Alert；showReadAction 才渲染已有小按钮。
```

保持 reload 兼容返回 Promise<void> 的独立消费者或统一调整类型为 Promise<unknown> 的展示回调，不能为 UI 类型问题再包装业务读取实现。
- [x] 页头按钮 aria-label 为“刷新工单详情”，点击与 Alt+R 同一路径；写入中资源 skip、其余资源更新，显示具体失败区域。初次 Skeleton 不移除必要的页面级协调 owner。
- [x] 评论/附件写后调用 `reload({ afterWrite: true })`；计数继续由结果派生。保护编辑原始 version、操作身份及不确定提交回执；后台更新不触发 setFieldsValue 覆盖已打开表单。
- [x] 运行 `npx jest --runInBand --coverage=false --testPathPattern='TicketDetail|TicketCommentStream|TicketRelationCards|AttachmentPanel|CommentPanel|TicketHistoryList|TicketNotificationSection|ServiceRequestPanel|ServiceCatalogApprovalChain'`。覆盖失败、草稿、并发、权限和其他独立消费者后提交 `fix(ticket): unify detail refresh without remounting editors`。

## Task 3：审批决策页面单一来源

**Files:** 新增 `components/business/detail-tabs/ApprovalDecisionHistoryContext.tsx`；修改该目录 `useApprovalDecisionHistory.ts`、`ApprovalMiniStepper.tsx`、`ApprovalWorkflowPanel.tsx`；修改 `components/ticket/ProcessApprovalDecisionCards.tsx`、`TicketDetail.tsx`；核对并修改 `components/work-item/WorkItemShell.tsx` 的页面 owner。新增相邻 `__tests__/ApprovalDecisionHistoryContext.test.tsx`，更新审批与 WorkItemShell 既有测试。

**Consumes:** Task 1 读取协调；既有 getTicketApprovalDecisions/toApprovalSteps。
**Produces:** `ApprovalDecisionHistoryProvider({ticketId, children})`，消费者 hook 只读取该页面 context；独立页面在自身根部提供一个 provider，移除重复读取路径。

- [x] 在同一 provider 渲染实际 MiniStepper 与 DecisionCards，mock API 一次返回记录；断言初次一次请求，全局刷新再增加一次，两个显示同步。加入下列请求断言：

```ts
expect(BPMNWorkflowApi.getTicketApprovalDecisions).toHaveBeenCalledTimes(1);
await user.click(screen.getByRole('button', { name: '刷新工单详情' }));
await waitFor(() => expect(BPMNWorkflowApi.getTicketApprovalDecisions).toHaveBeenCalledTimes(2));
```

- [x] 先运行新增测试确认现有多来源行为失败。
- [x] provider 内保留唯一 useDetailResource，注册 `approval-decisions`。消费者缺 provider 明确报开发接线错误，不能静默再发起另一个查询。全部调用点迁移，包括不位于 TicketDetail 的 ApprovalWorkflowPanel 消费者。
- [x] 同一结果经现有 toApprovalSteps 派生展示与匹配语义的决策计数；目录审批链继续独立读取。将权限拒绝、临时失败、换工单及重复刷新覆盖到共享视图。
- [x] 运行 `npx jest --runInBand --coverage=false --testPathPattern='Approval|WorkItemShell|TicketDetailRefresh'`；提交 `refactor(detail): share approval decisions within each work item page`。

## Task 4：活动与历史任务呈现

**Files:** 修改 `components/ticket/TicketProcessTasks.tsx` 及 `__tests__/TicketProcessTasks.test.tsx`；必要时新增同目录 `TicketProcessTaskList.tsx` 负责列表项，读取和命令保持原组件拥有。

**Consumes:** 既有 UserTask、uiActions、Task 1 读取；Task 2 统一刷新环境。
**Produces:** 当前任务摘要/展开区、默认折叠历史区，不改变请求和授权范围。

- [x] 基于现有 task/page fixtures 加入活动任务与绑定终态任务组合测试。先运行验证历史记录仍出现在当前列表的失败。
- [x] 从已返回集合派生活动/历史，不扩大 readTasks 的后端查询范围：

```ts
const activeTasks = tasks.filter(task => !terminal.has(task.status));
const historyTasks = tasks.filter(task => terminal.has(task.status));
const initiallyExpanded = activeTasks.some(task => task.uiActions?.claim || task.uiActions?.complete);
```

未知状态仍显示未知提示并遵循后端动作；不能作为已完成归历史。展开状态首次成功读取初始化一次，后续刷新尊重用户选择，identity 切换重置。
- [x] 当前可执行默认展开、只读紧凑、空态一行；历史用可访问的折叠控件，标明“历史任务”，保留负责人/实际操作人区别。两个分组共用相同读取状态，不各增刷新。
- [x] 删除本面板硬编码 bg-white 等亮色限定，使用既有 bg-surface/text-foreground/border-border token；不改全局主题。领取/完成及审批链接保持可访问。
- [x] 测试同时存在多个可执行任务、只有历史、无任务、未知状态、权限拒绝、用户折叠后刷新及 identity 切换。运行 `npx jest --runInBand --coverage=false --runTestsByPath src/components/ticket/__tests__/TicketProcessTasks.test.tsx` 后提交 `fix(ticket): separate current workflow tasks from task history`。

## Task 5：任务命令后刷新与组合验证

**Files:** 修改 `TicketProcessTasks.tsx`、`TicketDetail.tsx`，扩展 `__tests__/TicketProcessTasks.test.tsx`、`__tests__/TicketDetailRefresh.test.tsx`；确认评论/附件写后读取回归仍通过。

**Consumes:** Task 1 refresh(keys, {afterWrite:true})；Task 2/3 注册资源；Task 4 列表。
**Produces:** 任务命令后的有序定向读取及正确成功/失败提示。

- [x] 用 mock claimTask/completeTask 成功、后续 getTicket 或审批 GET 失败的测试，断言只调用一次命令，显示“操作已完成，部分数据更新失败”；点击失败区域重试不再执行命令。
- [x] 对操作结果显式分段：写入失败走原命令错误路径；写入成功后执行读取刷新，不将读取失败当作提交失败。保留锁、capture、session 和 command 前后授权检查。
- [x] 协调环境只调用一次以下批次，移除旧 resource.reload 与 onTaskChange 重复链；独立消费者继续以单次局部 reload 接线，不假造成功：

```ts
await refresh.refresh(['process-tasks', 'ticket', 'approval-decisions'], { afterWrite: true });
```

变量 refresh 来自 useDetailRefresh；独立页面必须验证传入 onTaskChange 的现有职责并维持其契约，不能静默删回调。
- [x] 覆盖写入中手动刷新、写前读取晚到、失败后重试、临时错误锁动作、最终撤权清弹窗、任务完成更新工单/审批、表单原版本未被新 ticket 覆盖。
- [x] 运行 `npx jest --runInBand --coverage=false --testPathPattern='TicketDetail|TicketProcessTasks|TicketCommentStream|AttachmentPanel|WorkItemShell'`；提交 `fix(ticket): refresh task outcomes without repeating commands`。

## Task 6：浏览器验收与交付

**Files:** 新增 `itsm-frontend/tests/e2e/flows/ticket-detail-refresh.spec.ts`；更新 `ticket-process-tasks-ui.spec.ts` 的活动/历史语义；更新本计划、设计状态及 `docs/README.md`。截图日志只放忽略产物或 /tmp。

**Consumes:** Tasks 1–5 最终页面；现有 auth-utils 的 loginAndReturn/DEFAULT_LOGIN。
**Produces:** 可复现的当前源码验证证据，未覆盖项明确记录。

- [x] 在专用端口启动实施 worktree 构建，核验 backend/frontend 来源。通过 PLAYWRIGHT_BASE_URL 与既有只读测试工单 ID 配置，不复用未知旧镜像作证。
- [x] 使用真实登录/详情读取，错误与任务写入采用已有 route fixture 隔离，禁止请求真实 KAF/外部授权。完整真实命令验证仅在独立测试数据环境执行，并分别报告模拟与真实覆盖。
- [x] 写浏览器断言，使用实际可访问编辑器与按钮；不能仅断言 CSS class：

```ts
for (const width of [1440, 390]) {
  await page.setViewportSize({ width, height: 900 });
  await expect(page.getByRole('button', { name: '刷新工单详情', exact: true })).toHaveCount(1);
  expect(await page.evaluate(() => document.body.scrollWidth <= innerWidth)).toBe(true);
}
```

补齐输入草稿→刷新→保留、打开编辑→刷新→版本冲突、失败区域重试、全局请求计数、暗色、键盘、任务完成→活动消失/历史折叠、只读与拒绝权限。使用精确等待条件，避免任意 sleep。
- [x] 运行 `npm run type-check`、`npm run lint:check`、`npm run build`。运行 `PLAYWRIGHT_SKIP_CHANNELS=1 npx playwright test tests/e2e/flows/ticket-detail-refresh.spec.ts tests/e2e/flows/ticket-process-tasks-ui.spec.ts --project=chromium --workers=1`；未配置 fixture 导致 skip 不算通过。
- [x] 定向测试外，仅在共享影响或新失败确有需要时扩大测试。检查 diff 无重复实现、临时文件及无关格式化，运行 `git diff --check`。
- [ ] 独立审查重点：授权未被 UI 推断、资源单一来源、写后读取顺序、旧身份隔离、草稿/版本保护、任务命令唯一入口。修复阻断项并重跑相关验证。
- [ ] 将设计标记 implemented 仅限上述功能与证据齐备后；记录浏览器范围、已有警告和环境限制。提交 `test(ticket): verify unified detail refresh and task experience`，创建聚焦 PR，未经用户后续授权不部署/合并。

## 依赖与执行顺序

Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6。Task 4 的展示可独立审查，但共享同一组件，不与 Task 5 并发编辑。若使用子代理，每个任务传入 spec、本计划和前序接口/提交；不得只给简短目标。所有未勾选步骤均未执行，不将计划中的预期 PASS 写成实际结果。
## 2026-09-15 实施与验收证据

Tasks 1–5 的勾选依据为逐项实现、失败后修复及独立复审记录：`f1ac1188`（读取回执与写后读取）、`d1d76070`（统一刷新与拒绝归属）、`bd446b43`（审批单一来源及共享调用兼容）、`704c0f1d`（任务分组）、`6c3dfc68`（任务命令后刷新及历史失败提示）。Jest 29 使用单数 `--testPathPattern`，不使用计划早期的复数参数。

Task 6 使用当前 worktree 的 standalone 生产构建，专用端口 3012，同源代理到既有后端 8080。隔离安装按 CI 执行 `npm ci --legacy-peer-deps --ignore-scripts`；Next 15.5.25、React 19.2.8、Ant Design 6.3.1、Playwright 1.58.2 与锁文件一致。未修改依赖清单或锁文件。

- `npm run type-check`、`npm run lint:check`、`ITSM_BACKEND_URL=http://127.0.0.1:8080 npm run build` 通过。Lint 保留 BPMNDesigner:348 的既有 unused-disable 警告；构建保留既有 ESLint Next 插件提示。
- 两个目标 Playwright 文件：Chromium、单 worker、真实 cookie 登录及既有 generic 工单 10 读取，**6/6 通过，无跳过**。环境：`PLAYWRIGHT_EXTERNAL_SERVER=1 PLAYWRIGHT_BASE_URL=http://127.0.0.1:3012 PLAYWRIGHT_TICKET_DETAIL_ID=10 PLAYWRIGHT_PROCESS_TASK_TICKET_ID=10 PLAYWRIGHT_SKIP_CHANNELS=1 PLAYWRIGHT_BROWSERS_PATH=/home/administrator/.cache/ms-playwright`。
- 覆盖评论草稿、单次主体/评论/任务读取、其他 API 每批不重复、AI 不随刷新重发、Alt+R、原编辑版本冲突、局部重试、最终拒绝清理、服务端只读动作、任务领取/完成及历史折叠。1440/390 亮暗主题无 body 横向溢出，并保存菜单与任务状态截图于 `/tmp`。
- 共享组件回归初次最终依赖运行 179/180 通过；唯一失败为旧 ServiceRequestPanel 把读取失败当空状态的断言。改为成功 null 与失败后局部重试的独立断言，聚焦重跑 **12/12 通过**。WorkItemShell、审批页及 useApprovalTasks 兼容集 **39/39 通过**。未反复运行未改动的全套测试。
- 浏览器错误、AI 建议、只读权限、版本冲突与任务命令均为 route 隔离数据；兜底 abort 所有非认证业务写请求。真实共享环境仅登录/读取；未运行真实工单、评论、任务命令或 KAF/外部授权。未以模拟响应证明真实后端命令成功。
- 截图和详细命令日志仅留 `/tmp` 与本地忽略产物。最终控制器独立审查、设计 implemented 状态和 PR 创建仍待完成；未推送、部署或合并。
