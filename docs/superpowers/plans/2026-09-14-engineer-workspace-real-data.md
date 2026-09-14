# Engineer Workspace 2A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将工程师原型替换为真实个人队列与复用的详情处理能力。
**Architecture:** 队列仅查询当前用户分配记录；身份/权限隔离与请求归属复用 1A useDetailResource。右侧使用 TicketDetail id 属性，不复制业务写入。
**Tech Stack:** Next.js 15、React 19、Ant Design 6、Jest/RTL、Playwright、WSL Node 22。
**Spec:** [accepted 2A design](../specs/2026-09-14-engineer-workspace-real-data-design.md)

- 状态：accepted / executing
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

- [ ] 添加失败测试，使用真实 auth store、mock API 边界，验证精确 assigneeId 和 total、页切换不显示旧数据、选中记录移出队列、刷新500保留与403清除、身份/权限切换和迟到响应。
```tsx
const { result } = renderHook(() => useAssignedTickets());
await waitFor(() => expect(result.current.selectedId).toBe(101));
expect(TicketApi.getTickets).toHaveBeenCalledWith({assigneeId:7,page:1,pageSize:20});
```
- [ ] 运行 `npx jest --runInBand --coverage=false --reporters=default useAssignedTickets` 确认缺少新能力导致失败。
- [ ] 显式增加 GetTicketsParams.assigneeId；实现局部页/选择状态，复用 useDetailResource 处理请求归属；身份包含权限投影，工作租户须与会话租户一致。
```tsx
const resource = useDetailResource(scopeKey, () => TicketApi.getTickets({assigneeId:userId,page,pageSize:20}), data => data.total, undefined, enabled);
```
- [ ] 测试与 type-check 通过，独立审查后提交 `feat(workspace): load assigned queue with session isolation`。

## Task 2: 页面组合与原型移除

**Files:** src/app/(main)/workspace/tickets/page.tsx；_components/AssignedTicketQueue.tsx；__tests__/page.test.tsx。
**Interfaces:** AssignedTicketQueue 消费 Task 1 hook 返回状态；页面渲染 `<TicketDetail key={identity + selectedId} id={String(selectedId)} />`，无所选项时卸载详情。

- [ ] 对旧页面增加真实列表及选单后评论消费者测试，确认原型无法满足。
```tsx
render(<App><WorkspaceTicketsPage /></App>);
expect(await screen.findByRole('button', {name:/TKT-101/})).toBeVisible();
```
- [ ] 左栏使用原生可聚焦按钮、Ant Design 分页/错误提示；三个未支持队列禁用并展示原因。页面窄屏上下布局，宽屏队列与详情并排；不固定全页高度截断内容。
- [ ] 删除本页所有假成功、mock票据、固定画像/AI内容；复用 TicketDetail，保留现有详情路由默认行为。
- [ ] 测试真实 TicketDetail 与真实评论消费者（只 mock API 与不相关外部能力）：选中 A→B 清除草稿，参数使用数值 ID；撤权卸载详情，初次失败可重试；验证禁用队列和无模拟成功入口。
- [ ] 运行页面/队列/1A 回归及 type-check，独立审查后提交 `feat(workspace): replace prototype with real ticket handling`。

## Task 3: 真实路径与交付

**Files:** tests/e2e/flows/engineer-workspace.spec.ts；本计划、spec、docs/README.md。

- [ ] 使用已有角色 fixtures，核验 engineer1/user1 登录与真实权限，不因缺权限自动提权。固定测试目标与数据前缀。
- [ ] 实际创建测试记录并分配给目标账号，验证队列选单、评论持久化、分配移出后刷新；如后端禁止删除流程中测试工单，注明保留，不绕过流程。
```ts
await page.goto('/workspace/tickets');
await page.getByRole('button', {name: new RegExp(marker)}).click();
await expect(page.getByRole('heading', {name:marker})).toBeVisible();
```
- [ ] 在 1440/390、light/dark 下记录截图并检查横向溢出；权限不足使用真实账号结果加组件拒绝契约验证，分别记述。
- [ ] 运行 `npm run type-check -- --incremental false`、`npm run lint:check`、`npm test -- --runInBand --forceExit --reporters=default`、`npm run build` 及工程/API路径守卫；随后对最终构建运行 Chromium E2E。
- [ ] 独立最终审查，修复重要发现，更新证据/设计状态与测试清理边界，`git diff --check`，提交 `test(workspace): verify engineer workflow and record delivery`。

## Evidence

- 基线：2 套件 22 测试通过；主线仍未包含统一关系能力，不执行 1B。
