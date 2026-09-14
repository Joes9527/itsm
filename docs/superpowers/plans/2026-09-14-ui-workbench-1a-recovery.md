# UI Workbench 1A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 恢复工单详情附件管理，修复分配搜索及评论/附件/只读关系的错误与刷新行为，交付可独立验收的 1A。

**Architecture:** 附件继续使用 AttachmentPanel 与 ticketAttachmentAdapter，修复其实际传输契约；TicketDetail 只组合页面和同步数量。评论和关系保留现有数据源，身份切换和权限失效时清理状态。关系写入属于 1B，本计划不恢复旧 RelationPanel CRUD。

**Tech Stack:** Next.js 15、React 19、TypeScript、Ant Design 6、Jest/React Testing Library、TypeScript Playwright；WSL Node >=22。

**Spec:** [UI 工作台补齐设计](../specs/2026-09-14-ui-workbench-completion-design.md)，特别是 1A/1B 边界与第 4.5 节。

- 日期：2026-09-14
- 状态：draft（依据已接受设计编写，实施尚未开始）
- 分支：`codex/fix/ui-workbench-completion`
- 工作区：`/home/administrator/project/itsm/.worktrees/ui-workbench-completion`
- 初始源码基线：`a25e108d`；设计修订提交：`b154d847`

## Global Constraints

- 1A 可先交付，不得宣称 R3 或第一阶段整体完成。
- API 访问保留在 `src/lib/api/`，后端仍是权限、租户范围和关系合法性的权威来源。
- 1A 展示基线实际支持的父子关系。
- 1A 不计划 schema 迁移、共享数据库清理、初始化或批量修复。
- 采用 [工程治理](../../agent-engineering-governance.md) 的目录和交付规则；不修改其他 worktree，不提交 junit.xml、截图、缓存或构建产物。
- 下列命令均在本 worktree 的 `itsm-frontend` 下执行；Windows 调用格式：`wsl -d Ubuntu --cd /home/administrator/project/itsm/.worktrees/ui-workbench-completion/itsm-frontend <command>`。
- 定向 Jest 使用 `--coverage=false --reporters=default`，避免全局覆盖率误判和覆盖已跟踪 junit.xml；这不豁免最终 CI 门禁。

## 文件职责与依赖

| 文件（相对 itsm-frontend） | 职责 |
| --- | --- |
| src/lib/api/http-client.ts | 既有安全传输、响应解析；统一修复附件所需 multipart 分支 |
| src/lib/api/ticket-attachment-api.ts | 仅定义附件端点与 FormData，不另建认证或响应规则 |
| src/components/business/detail-tabs/AttachmentPanel.tsx | 附件上传/删除/预览/下载、状态反馈和授权门控 |
| src/components/business/detail-tabs/types.ts | 为附件消费者定义明确的显示权限及回调类型（如需新增） |
| src/components/ticket/TicketDetail.tsx | 人员搜索、面板组合、计数和身份边界 |
| src/components/ticket/TicketCommentStream.tsx | 评论读取/写入、错误与草稿状态 |
| src/components/ticket/TicketRelationCards.tsx | 1A 的真实只读父子关系列表及错误状态 |
| src/components/work-item/WorkItemAttachments.tsx、WorkItemShell.tsx | 共享附件/关系消费者；保持 workItem.id |
| src/components/ticket/TicketAttachmentGrid.tsx | 替换消费后检查引用，无消费者再删除 |

任务 1 先修复真实上传契约；任务 2 恢复附件交互；任务 3 修复评论/关系/身份与计数；任务 4 修复分配搜索；任务 5 做集成、共享消费者和真实路径验收。每项只提交自身文件，提交前 `git diff --check`。

## Task 1：附件传输契约与安全失败传播

**Files:**
- Modify: `src/lib/api/http-client.ts`, `src/lib/api/ticket-attachment-api.ts`
- Test: `src/lib/api/__tests__/ticket-attachment-api.test.ts`, `src/lib/api/__tests__/http-client.test.ts`（扩展已有文件，如不存在则创建）
- Read: `../itsm-backend/router/router.go`, `../itsm-backend/controller/ticket_attachment_controller.go`, `src/lib/api/http-client.ts`

**Interfaces:** 保持 `TicketAttachmentApi.uploadAttachment(ticketId: number, file: File, onProgress?: (percent: number) => void): Promise<TicketAttachment>`；失败向面板传播既有 `ApiError.status/code/errorCode`。

- [ ] 检查本 worktree 的 Node、依赖和最小基线；缺依赖用 npm ci 安装，不复用其他 worktree 的可写 node_modules。

```bash
node --version
npm ci
npm test -- --runInBand --coverage=false --reporters=default --runTestsByPath src/components/ticket/__tests__/TicketDetail.test.tsx src/config/persona/__tests__/persona-config.test.ts
```

记录实际结果；已有失败与新增回归分开，不静默降低门禁。

- [ ] 为上传增加失败测试。拦截传输返回后端真实成功 envelope（code=0），断言返回附件；code 非零、403 和网络错误不得作为成功。测试 FormData 字段 file、目标 ticketId、进度以及 cookie/CSRF 行为。

```ts
it('returns the server attachment through the shared multipart client', async () => {
  const file = new File(['log'], 'diagnostic.txt', { type: 'text/plain' });
  const attachment = { id: 17, ticketId: 101, fileName: file.name };
  const post = jest.spyOn(httpClient, 'post').mockResolvedValue(attachment);
  await expect(TicketAttachmentApi.uploadAttachment(101, file)).resolves.toEqual(attachment);
  expect(post.mock.calls[0][0]).toBe('/api/v1/tickets/101/attachments');
  expect((post.mock.calls[0][1] as FormData).get('file')).toBe(file);
});
```

测试文件导入 jest、httpClient、TicketAttachmentApi；传输级测试使用可控 XHR/fetch mock 触发真实客户端分支，而不是只重复上述 spy 断言。

- [ ] 运行上述两个测试文件，确认旧实现的独立 XHR/code===200 行为使新增用例失败。
- [ ] 移除 TicketAttachmentApi 的独立 XHR，改为现有 httpClient.post multipart 入口：

```ts
const data = new FormData();
data.append('file', file);
return httpClient.post<TicketAttachment>(
  `/api/v1/tickets/${ticketId}/attachments`, data,
  onProgress ? { onUploadProgress: onProgress } : undefined
);
```

不能只做这一替换：当前 httpClient.post 的 multipart 分支也绕过部分 requestInternal 安全流程。将该分支接入同一凭证、tenant header、CSRF 获取/恢复和错误解析策略；保留 XHR 上传进度，设置 withCredentials，不手写 multipart Content-Type。复用已有安全 helper，不另建认证重试规则。支持同一逻辑请求的有限认证/CSRF恢复；网络结果不确定时不自动重放上传。403 必须保留结构化状态，不能只抛普通字符串错误。增加 abort/timeout 结束处理，释放回调，避免永久 pending。

- [ ] 测试有/无进度的上传分支、成功 envelope、最终403、认证恢复失败、网络失败、超时和已有 http-client 回归；随后 type-check。
- [ ] 提交：`fix(attachments): use shared authenticated multipart transport`。

## Task 2：恢复附件功能且明确共享消费者授权

**Files:**
- Modify: `src/components/business/detail-tabs/AttachmentPanel.tsx`, `src/components/ticket/TicketDetail.tsx`, `src/components/work-item/WorkItemAttachments.tsx`
- Modify if needed: `src/components/business/detail-tabs/types.ts`
- Delete after reference check: `src/components/ticket/TicketAttachmentGrid.tsx`
- Test: `src/components/business/detail-tabs/__tests__/AttachmentPanel.test.tsx`, `src/components/work-item/__tests__/WorkItemAttachments.test.tsx`

**Interfaces:** 保留 AttachmentAdapter。AttachmentPanel 增加可选 `onCountChange?: (count: number | undefined) => void`；成功读取通知长度，身份/授权失效通知 undefined。操作显示策略通过明确 `permissions: { canRead: boolean; canUpload: boolean; canDelete: boolean }` 传入，所有生产消费者必须提供；上传者身份不能替代授权。source capability 缺失时不默认允许。

- [ ] 添加行为测试，adapter mock 实现 list/upload/remove/getDownloadUrl/getPreviewUrl。使用真实 Ant Design App 包装组件。

```tsx
const permissions = { canRead: true, canUpload: true, canDelete: true };
render(<App><AttachmentPanel targetType="ticket" targetId={101}
  adapter={adapter} permissions={permissions} /></App>);
expect(await screen.findByRole('button', { name: '上传附件' })).toBeEnabled();
// adapter.list 首次返回 []，上传完成返回真实附件，再次渲染后断言文件名可见。
```

补充删除取消/确认、上传失败重试、权限缺失、文件超限、预览/下载接口、相同按钮连续点击只触发一次操作。用户信息缺失不得暴露删除权。

- [ ] 运行 AttachmentPanel 与 WorkItemAttachments 测试，确认缺权限契约/功能接线的断言先失败。
- [ ] 在共享组件保留工作台卡片呈现与完整操作；用 App.useApp() 的 modal/message，保留删除确认、上传进度、错误状态。由调用方消费现有会话权限和 recordClass→资源映射：附件路由 read/create/delete 分别对应读取/上传/删除；这仅控制入口，后端仍验证行权限及附件规则。未知 class 不授予权限，不能根据 persona 推断。
- [ ] TicketDetail 替换附件 Tab 为 AttachmentPanel；WorkItemAttachments 提供对应权限，targetId 始终是 WorkItem ID。确认所有消费者后删除 TicketAttachmentGrid，不保留第二套附件功能路径。
- [ ] 重跑测试与 type-check，提交：`fix(ticket): restore shared attachment management in workbench`。

## Task 3：读取失败、身份失效、并发与计数

**Files:**
- Modify: `src/components/ticket/TicketCommentStream.tsx`, `src/components/ticket/TicketRelationCards.tsx`, `src/components/ticket/TicketDetail.tsx`, `src/components/business/detail-tabs/AttachmentPanel.tsx`
- Read/verify: `src/components/work-item/WorkItemShell.tsx`, `src/lib/store/auth-store.ts`
- Test: `src/components/ticket/__tests__/TicketCommentStream.test.tsx`, `src/components/ticket/__tests__/TicketRelationCards.test.tsx`, `src/components/ticket/__tests__/TicketDetail.test.tsx`、任务 2 附件测试

**Interfaces:** 评论增加 `onCountChange?: (count: number | undefined) => void`，使用 list 返回 total；只读关系使用相同回调并从成功列表取长度。父层计数不计算独立业务规则。最终读取拒绝读取 `ApiError.status`，遵循设计中认证/CSRF恢复后的结果分类。

- [ ] 分别为三个面板写首次失败、重试、空结果、旧数据刷新失败和权限撤销测试。如下场景必须覆盖实际渲染与清理：

```ts
// list 先成功返回包含敏感内容的数据，再拒绝读取。
list.mockRejectedValueOnce(new ApiError('无权读取', 403));
// 通过面板的刷新/重试操作触发读取。
await user.click(screen.getByRole('button', { name: '刷新' }));
await waitFor(() => expect(screen.queryByText('敏感内容')).not.toBeInTheDocument());
expect(onCountChange).toHaveBeenLastCalledWith(undefined);
```

使用 deferred Promise 验证：工单 A 请求未结束→切换 B→B 成功→A 成功，不显示 A；同一工单授权失效后旧成功响应不得恢复内容。会话/租户变更同样清理；只用 ticketId 作为身份不足。

- [ ] 运行对应测试，确认旧 catch 清空/错误无提示/迟到响应行为失败。
- [ ] 每个面板明确 loading/error/denied 状态。网络与可恢复服务异常保留数据并显示刷新失败；最终 401/权限403或租户失效清内容、编辑、计数并禁用操作；授权恢复须重新读取成功后才能重开操作。

```ts
const requestId = ++requestSequence.current;
const data = await adapter.list(targetId);
if (requestId !== requestSequence.current) return;
setItems(data);
```

请求失效必须覆盖开始新请求、身份改变、卸载和权限拒绝；不能只在 effect cleanup 防护。写操作也捕获发起身份，完成后不修改另一工单的草稿、列表或成功提示。

- [ ] 提供明确刷新和重试入口。评论/附件写成功后分别处理“保存成功”和“刷新失败”；禁止重复提交，保留失败草稿。调用父层 onCountChange 更新对应角标，移除父层重复抓取评论/附件/关系数量的路径；未加载数量可不显示，不能编造 0。审批、历史数量保持原行为。
- [ ] TicketRelationCards 保持只读，正确显示实际 parent_child 类型/方向；不挂回旧 CRUD 和前端多类型枚举。共享 WorkItemShell 同样验证身份切换与拒绝读取。
- [ ] 全部相关测试通过后提交：`fix(workbench): distinguish denied reads and fence stale panel updates`。

## Task 4：分配搜索与人员读取状态

**Files:** Modify `src/components/ticket/TicketDetail.tsx`; Test `src/components/ticket/__tests__/TicketDetail.test.tsx`。

**Interfaces:** 不改变 UserApi.getUsers 和 TicketApi.assignTicket；为 Select option 增加纯文本 searchText，label 仍可为 JSX。

- [ ] 用两个不同姓名/用户名的用户 fixture 测试打开分配弹窗、输入关键词、过滤和清空；确保真实 Select 参与测试，不 mock 掉过滤函数。

```ts
const options = users.map(user => ({
  value: user.id,
  label: user.name || user.username,
  searchText: `${user.name || ''} ${user.username || ''}`.toLocaleLowerCase(),
}));
const filterOption = (input: string, option?: (typeof options)[number]) =>
  Boolean(option?.searchText.includes(input.toLocaleLowerCase()));
```

生产 label 保留原富文本；禁止 `(label as string).toLowerCase()`。额外覆盖无 user:read、请求失败、重试成功和分配提交 payload。

- [ ] 运行 TicketDetail 测试确认搜索用例失败。
- [ ] 使用 searchText；fetchUsers 区分无读取权限与请求失败，错误在弹窗可见并提供重试。分配入口仍由 ticket.actions.assign 门控，不扩大可分配人员范围。抄送复用同一人员读取状态，不将读取失败显示为正常空列表。
- [ ] 重跑测试与 type-check，提交：`fix(ticket): repair assignee search and user loading feedback`。

## Task 5：组合回归与真实路径验收

**Files:**
- Test: `src/components/work-item/__tests__/WorkItemShell.test.tsx`, `src/components/work-item/__tests__/WorkItemAttachments.test.tsx`, `src/components/ticket/__tests__/TicketDetail.test.tsx`
- Create: `tests/e2e/flows/ticket-workbench-recovery.spec.ts`
- Update after evidence: 本计划、对应设计和历史工作台计划中的已验证条目

- [ ] 先读 [E2E 指南](../../e2e-testing-guide.md) 与既有角色 fixtures。使用隔离测试账号/租户、带唯一标记的测试工单；不复用真实业务数据，不启动迁移或重置共享服务。
- [ ] 组合测试不得把本次修复面板全部 mock 成 null。覆盖 generic 及现有专业 WorkItemShell 附件、只读关系；选择 WorkItem ID 与专业扩展 ID 不同的 fixture，断言发出的 API URL 使用 WorkItem ID。
- [ ] 浏览器验证登录→详情→上传→刷新页面仍可见→预览/下载→删除取消与确认→重新读取；评论提交与计数、分配搜索、现有只读关系及失效提示。

```ts
// 在 E2E fixture 创建的测试工单详情页，测试上传后的持久化：
await page.locator('input[type="file"]').setInputFiles({
  name: 'workbench-proof.txt', mimeType: 'text/plain', buffer: Buffer.from('workbench test'),
});
await expect(page.getByText('workbench-proof.txt', { exact: true })).toBeVisible();
await page.reload();
await page.getByRole('tab', { name: /附件/ }).click();
await expect(page.getByText('workbench-proof.txt', { exact: true })).toBeVisible();
```

分别在桌面和 390px、light/dark 下检查弹窗、长文件名、错误提示和横向溢出。权限拒绝可用组件/契约测试精确注入；真实路径记录实际授权结果。截图、日志放已忽略目录，注明后端与前端实际源码 SHA。

- [ ] 运行相关检查：

```bash
npm run type-check -- --incremental false
npm run lint:check
npm test -- --runInBand --coverage=false --reporters=default --testPathPattern='TicketDetail|TicketCommentStream|TicketRelationCards|AttachmentPanel|WorkItemAttachments|WorkItemShell|ticket-attachment-api|http-client'
npm run build
PLAYWRIGHT_SKIP_CHANNELS=1 npx playwright test tests/e2e/flows/ticket-workbench-recovery.spec.ts --project=chromium --workers=1
```

- [ ] 按 CI 配置执行必需全局测试/覆盖率和 API 契约门禁；与基线对照列明既有失败。失败不能用缩小测试集宣称通过；环境未验证的浏览器项目明确保留未完成。
- [ ] 独立代码审查关注授权、身份、请求重放和共享消费者；修复重要发现后再交付。执行 git diff --check，确认无产物或无关文件。
- [ ] 提交测试与验收证据：`test(workbench): verify 1A recovery and shared consumers`。1A 完成后更新其条目；R3/1B 仍未完成，不将整个设计标为 implemented。

## Spec coverage / Self-review

| 设计要求 | 任务 |
| --- | --- |
| 附件上传删除预览下载与安全传输 | 1、2、5 |
| 可恢复错误、授权失效、身份切换和旧响应 | 2、3、5 |
| 评论/附件/只读关系计数与变更刷新 | 3 |
| 分配搜索、人员失败和抄送消费者 | 4 |
| WorkItem 身份及共享组件回归 | 2、3、5 |
| 布局、主题、390px、真实路径和交付证据 | 5 |
| 关系写入、迁移、统一关系切换 | 1B，明确不执行；第 4.5 节准入条件保留 |

本计划只授权 1A 的实现顺序，不授权集成关系候选分支或对共享环境执行迁移。
