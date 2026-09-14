# Engineer Workspace 2A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 将工程师原型替换为真实个人队列与复用的详情处理能力。
**Architecture:** 队列仅查询当前用户分配记录；身份/权限隔离与请求归属复用 1A useDetailResource。右侧使用 TicketDetail id 属性，不复制业务写入。
**Tech Stack:** Next.js 15、React 19、Ant Design 6、Jest/RTL、Playwright、WSL Node 22。
**Spec:** [accepted 2A design](../specs/2026-09-14-engineer-workspace-real-data-design.md)

- 状态：implemented（仅 2A）
- 分支：codex/feat/engineer-workspace-real-data；从 87a21e5f 建立，依赖 1A，尚未合并主线。
- 隔离工作区：沿用 /home/administrator/project/itsm/.worktrees/ui-workbench-completion；原 1A 分支保留。

## Global Constraints

- 当前任务顺序执行，每项独立审查，不重新询问模式。
- 只接入分给我的；三种未具备明确契约的队列显示禁用和原因，不显示模拟数量。
- 数值 WorkItem ID 为查询/选择身份；总数取服务端 total，不根据当前页估算。
- 会话、用户租户、工作租户和权限变化清除列表与详情；无身份不发宽范围查询。
- 不修改后端、迁移、主线或其他 worktree；浏览器使用当前开发 API，仅写唯一 ENGINEER-WORKSPACE-* 测试记录。
- 私有依赖已安装；基线 TicketDetail/persona 2 套件 22 测试通过。命令在 itsm-frontend 下运行，使用 --reporters=default，产物不入库。

## Task 1: 队列查询与身份状态

**Files:** src/app/(main)/workspace/tickets/_hooks/useAssignedTickets.ts；其相邻 __tests__/useAssignedTickets.test.tsx；src/lib/api/api-config.ts。
**Interfaces:** useAssignedTickets 返回 identity、page、items、total、selectedId、loading、error、denied、reload、select、changePage；请求 TicketApi.getTickets({assigneeId, page, pageSize:20})。

- [x] 添加失败测试，使用真实 auth store、mock API 边界，验证精确 assigneeId 和 total、页切换不显示旧数据、选中记录移出队列、刷新500保留与403清除、身份/权限切换和迟到响应。
```tsx
const { result } = renderHook(() => useAssignedTickets());
await waitFor(() => expect(result.current.selectedId).toBe(101));
expect(TicketApi.getTickets).toHaveBeenCalledWith({assigneeId:7,page:1,pageSize:20});
```
- [x] 运行 `npx jest --runInBand --coverage=false --reporters=default --testPathPattern=useAssignedTickets` 确认缺少新能力导致失败。
- [x] 显式增加 GetTicketsParams.assigneeId；实现局部页/选择状态，复用 useDetailResource 处理请求归属；身份包含权限投影，工作租户须与会话租户一致。
```tsx
const resource = useDetailResource(scopeKey, () => TicketApi.getTickets({assigneeId:userId,page,pageSize:20}), data => data.total, undefined, enabled);
```
- [x] 测试与 type-check 通过，独立审查后提交 `feat(workspace): load assigned queue with session isolation`。

## Task 2: 页面组合与原型移除

**Files:** src/app/(main)/workspace/tickets/page.tsx；_components/AssignedTicketQueue.tsx；__tests__/page.test.tsx。
**Interfaces:** AssignedTicketQueue 消费 Task 1 hook 返回状态；页面渲染 `<TicketDetail key={identity + selectedId} id={String(selectedId)} />`，无所选项时卸载详情。

- [x] 对旧页面增加真实列表及选单后评论消费者测试，确认原型无法满足。
```tsx
render(<App><WorkspaceTicketsPage /></App>);
expect(await screen.findByRole('button', {name:/TKT-101/})).toBeVisible();
```
- [x] 左栏使用原生可聚焦按钮、Ant Design 分页/错误提示；三个未支持队列禁用并展示原因。页面窄屏上下布局，宽屏队列与详情并排；不固定全页高度截断内容。
- [x] 删除本页所有假成功、mock票据、固定画像/AI内容；复用 TicketDetail，保留现有详情路由默认行为。
- [x] 测试真实 TicketDetail 与真实评论消费者（只 mock API 与不相关外部能力）：选中 A→B 清除草稿，参数使用数值 ID；撤权卸载详情，初次失败可重试；验证禁用队列和无模拟成功入口。
- [x] 运行页面/队列/1A 回归及 type-check，独立审查后提交 `feat(workspace): replace prototype with real ticket handling`。

## Task 3: 真实路径与交付

**Files:** tests/e2e/flows/engineer-workspace.spec.ts；本计划、spec、docs/README.md。

- [x] 使用已有角色 fixtures，核验 engineer1/user1 登录与真实权限，不因缺权限自动提权。固定测试目标与数据前缀。
- [x] 实际创建测试记录并分配给目标账号，验证队列选单、评论持久化、分配移出后刷新；如后端禁止删除流程中测试工单，注明保留，不绕过流程。
```ts
await page.goto('/workspace/tickets');
await page.getByRole('button', {name: new RegExp(marker)}).click();
await expect(page.getByRole('heading', {name:marker})).toBeVisible();
```
- [x] 在 1440/390、light/dark 下记录截图并检查横向溢出；权限不足使用真实账号结果加组件拒绝契约验证，分别记述。
- [x] 运行 `npm run type-check -- --incremental false`、`npm run lint:check`、`npm test -- --runInBand --forceExit --reporters=default`、`npm run build` 及工程/API路径守卫；随后对最终构建运行 Chromium E2E。
- [x] 独立最终审查，修复重要发现，更新证据/设计状态与测试清理边界，`git diff --check`，提交 `test(workspace): verify engineer workflow and record delivery`。

## Evidence

- 基线：2 套件 22 测试通过；主线仍未包含统一关系能力，不执行 1B。

- Task 1：b9a31d5c，8 个 hook 行为测试通过，独立审查无关键或重要问题。
- Task 2：4223e9fc，真实 TicketDetail/评论组合测试 4 个通过，包含迟到 A 评论不能覆盖 B；与 1A 组合共 9 套件 67 测试通过。独立审查无关键或重要问题。
- 本批测试使用 frontend-testing-guide 与 frontend-e2e-check；当前 API 二进制 SHA256 仍为 3fa2ab3ef156ddd435b8cf8a378dd68d529a754ab727cc7e144ec2287d811fe2，未替换共享服务。


## Task 3 最终验收（2026-09-14）

- 生产源码：`4223e9fc`（包括 Task 1 `b9a31d5c`）；E2E 与清理修复：`877f35c6`。生产构建、两轮真实浏览器均使用该生产源码，清理修复后最后一轮使用最终测试文件。
- 全量 Jest：223 套件通过，3193 通过、13 skipped；statements 80.67%、branches 66.76%、functions 82.50%、lines 82.13%。命令 `npm test -- --runInBand --forceExit --reporters=default`，退出码 0；日志 `/tmp/engineer-workspace-full.log`。
- 最终 TypeScript 检查通过；全量 lint 0 errors，仅保留 BPMNDesigner:347 既有 unused eslint-disable warning。最终 E2E 文件修改后另行通过 eslint 与全量 type-check。生产 build 退出码 0；工程契约守卫、自测均通过，API 路径 795 匹配、0 缺失。
- Chromium 真实 E2E 最后一轮：1 passed，7.9s。使用 `PLAYWRIGHT_BASE_URL=http://127.0.0.1:3015 PLAYWRIGHT_EXTERNAL_SERVER=1 PLAYWRIGHT_SKIP_CHANNELS=1 npx playwright test tests/e2e/flows/engineer-workspace.spec.ts --project=chromium --workers=1 --reporter=list`（验收额外输出 JSON 到 `/tmp` 保存账号/工单标记）。验证工程师真实权限、个人队列、选择 WorkItem、评论发送与刷新持久化、管理员通过既有分配 API 转派测试记录后从原队列移除、guest 真实 API 403。未宣称该工程师拥有分配权限或已通过工程师 UI 提交转派。
- 1440px 与 390px、light/dark 的横向溢出断言通过；人工检查桌面和窄屏截图确认队列、所选记录、评论和禁用原因可见。TicketDetail 既有浅色卡片在暗色主题下仍存在，不属于本批全仓主题迁移。未运行 Firefox/WebKit。
- 开发 API：`127.0.0.1:8080`；二进制 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/bin/itsm-api-support-handoff-20260911`，SHA256 `3fa2ab3ef156ddd435b8cf8a378dd68d529a754ab727cc7e144ec2287d811fe2`。二进制源码 SHA 未验证，不以本 worktree 后端源码冒充实际部署版本。临时生产前端 3015 已停止，既有 API/前端未停止或替换。
- 原 fixtures 的 engineer1/user1 登录 401，因此创建本任务唯一标记账号，使用既有 ops_engineer/guest 角色定义；没有调整既有用户或角色权限。两轮账号 #7893/#7894、#7895/#7896 已停用，并再次 GET 确認 active=false。凭据随机生成且不入库、不写交付文档。
- 测试工单 `ENGINEER-WORKSPACE-1789358912848`（#9）与 `ENGINEER-WORKSPACE-1789358995811`（#10）及其测试评论保留：删除返回 403“工单流程流转中，不可删除”。未绕过流程或操作数据库；均已通过正常分配接口转给管理员，后续按正常流程清理。
- 独立最终审查发现 E2E 清理失败会跳过账号停用的 P2，已改为逐项串行 try/catch、收集错误后统一断言并核验停用持久化；复审确认关闭。没有剩余关键或重要发现。
- 日志 `/tmp/engineer-workspace-*.log`、JSON `/tmp/engineer-workspace-e2e*.json`、截图 `/tmp/itsm-playwright-results/` 均不提交。`git diff --check` 通过；没有后端变更或迁移，因此未重复运行 Go 全量测试。

2A 已完成。该分支依赖尚未合并的 1A，合并时先集成 1A；本任务未推送或合并主线。1B 关系管理仍等待领域集成及部署，未分配/SLA/待用户回复队列、主管与管理层真实能力及全仓视觉收敛仍是后续交付。
