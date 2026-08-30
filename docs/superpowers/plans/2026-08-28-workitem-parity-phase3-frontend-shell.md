# WorkItem 详情页能力对齐 · Phase 3：前端 WorkItemShell 补齐 SLA/评论/附件/History/Relations/操作栏 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `WorkItemShell.tsx` into the actual "唯一详情页公共壳" the design doc's target state
describes: a real SLA card, real Comments/Attachments blocks (no longer placeholder `<Card/>`s), an
embedded History timeline, an embedded Relations list, and an action bar that renders backend-driven
`actions` — then wire real SLA data into the three domain pages and remove the page-level tabs that
Shell's new blocks make redundant.

**Architecture:** Five small, independently-testable pieces get built first in isolation
(`WorkItemSLA`, a `toTargetType` mapping helper, real `WorkItemComments`/`WorkItemAttachments`,
`WorkItemActionBar`), then assembled into `WorkItemShell.tsx` in one wiring task, then the three
`app/(main)/{incidents,problems,changes}/[id]/page.tsx` files are updated to fetch and pass `sla`
and to drop the tabs Shell now supersedes. `TicketHistoryList`/`TicketRelationCards` are reused
as-is (already domain-agnostic, keyed only by a numeric id) with no changes of their own.

**Tech Stack:** Next.js/React/TypeScript, Ant Design, Jest + React Testing Library.

**Spec:** `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md` §5.1, §5.2, §5.3

**Depends on:** Phase 2 (`2026-08-28-workitem-parity-phase2-incident-comment-migration.md`) must be
fully merged and its backfill run before this phase's `WorkItemComments` lands unconditionally on
`ticketCommentAdapter` — if Incident comments are still on the old adapter when this phase ships,
Incident's comments would silently break.

## Global Constraints

- `WorkItemShell` stays the single implementation — do not add a second, parallel comments/history/
  relations rendering path anywhere else (design principle §3.3).
- `TicketHistoryList`/`TicketRelationCards` are consumed unmodified, passed `workItem.id` as their
  `ticketId` prop — do not fork or wrap their internals.
- All four `recordClass` values use `ticketCommentAdapter`/`ticketAttachmentAdapter` unconditionally
  in the real `WorkItemComments`/`WorkItemAttachments` — no per-recordClass branching, because Phase
  2 already fully retired the Incident-specific path before this phase starts (§5.1's "统一用
  ticketCommentAdapter" end-state, reachable immediately given the phase ordering).
- The existing three assertions in `WorkItemShell.test.tsx` (number renders, actions pass through
  context, error state suppresses the panel slot) must keep passing unmodified — this phase adds
  mocks around them, not new behavior to them.
- `WorkItemActionBar` renders nothing (not an empty-state placeholder, an actually-empty node) when
  `actions` is `{}` — that's the real, correct state until Phase 4 ships backend-computed actions.

---

## Task 1: Extend `WorkItemSLAState` and build the `WorkItemSLA` component

**Files:**
- Modify: `itsm-frontend/src/components/work-item/WorkItemTypes.ts`
- Create: `itsm-frontend/src/components/work-item/WorkItemSLA.tsx`
- Create: `itsm-frontend/src/components/work-item/__tests__/WorkItemSLA.test.tsx`

**Interfaces:**
- Produces: extended `WorkItemSLAState` (replaces the current `remainingSeconds`-based shape, which
  has zero consumers today — safe to redefine) and `WorkItemSLA({ sla }: { sla?: WorkItemSLAState
  }): JSX.Element | null`, consumed by Task 4's `WorkItemShell.tsx` assembly.

- [ ] **Step 1: Write the failing test**

Create `itsm-frontend/src/components/work-item/__tests__/WorkItemSLA.test.tsx`:

```tsx
import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemSLA } from '../WorkItemSLA';
import type { WorkItemSLAState } from '../WorkItemTypes';

describe('WorkItemSLA', () => {
  it('renders nothing when sla is undefined', () => {
    const { container } = render(<WorkItemSLA />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the SLA name and deadlines when sla is present', () => {
    const sla: WorkItemSLAState = {
      slaName: '标准 SLA',
      responseTime: 60,
      resolutionTime: 480,
      responseDeadline: '2026-08-28T10:00:00Z',
      resolutionDeadline: '2026-08-28T18:00:00Z',
      responseTimeRemaining: 30,
      resolutionTimeRemaining: 200,
      isBreached: false,
    };
    render(<WorkItemSLA sla={sla} />);
    expect(screen.getByText('标准 SLA')).toBeInTheDocument();
    expect(screen.getByText('响应截止:')).toBeInTheDocument();
    expect(screen.getByText('解决截止:')).toBeInTheDocument();
  });

  it('highlights an overdue response and shows the breach tag', () => {
    const sla: WorkItemSLAState = {
      slaName: '标准 SLA',
      responseTime: 60,
      resolutionTime: 480,
      responseDeadline: '2026-08-28T10:00:00Z',
      resolutionDeadline: null,
      responseTimeRemaining: -15,
      resolutionTimeRemaining: null,
      isBreached: true,
    };
    render(<WorkItemSLA sla={sla} />);
    expect(screen.getByText(/已超时/)).toBeInTheDocument();
    expect(screen.getByText('SLA 已违规')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemSLA.test.tsx`
Expected: FAIL — `../WorkItemSLA` module not found.

- [ ] **Step 3: Write minimal implementation**

In `itsm-frontend/src/components/work-item/WorkItemTypes.ts`, replace the current
`WorkItemSLAState` (lines 23-28):

```ts
export interface WorkItemSLAState {
  remainingSeconds: number | null;
  isBreached: boolean;
  responseDeadline: string | null;
  resolutionDeadline: string | null;
}
```

with the fields `TicketDetail.tsx`'s SLA card and `TicketApi.getTicketSLA` already use (dropping
only the fields the UI never reads: `ticketId`, `slaDefinitionId`, `serviceType`, `priority`,
`firstResponseAt`, `resolvedAt`):

```ts
export interface WorkItemSLAState {
  slaName: string;
  responseTime: number; // 目标响应时长，分钟
  resolutionTime: number; // 目标解决时长，分钟
  responseDeadline: string | null;
  resolutionDeadline: string | null;
  responseTimeRemaining: number | null; // 剩余响应时长，分钟；负数表示已超时
  resolutionTimeRemaining: number | null; // 剩余解决时长，分钟；负数表示已超时
  isBreached: boolean;
}
```

Create `itsm-frontend/src/components/work-item/WorkItemSLA.tsx`:

```tsx
'use client';

import React from 'react';
import { Card, Progress, Tag } from 'antd';
import { Clock } from 'lucide-react';
import type { WorkItemSLAState } from './WorkItemTypes';

// 视觉与字段完全对齐 TicketDetail.tsx 现有的 SLA 卡片（响应/解决倒计时 + 超时高亮），
// 不新建视觉规范——见设计文档 §5.2。
const getSLAPercent = (total: number, remaining: number | null): number => {
  if (!total || total <= 0 || remaining === null) return 0;
  return Math.min(100, Math.max(0, Math.round(((total - remaining) / total) * 100)));
};

const formatHours = (minutes: number): string => (minutes / 60).toFixed(1);

export function WorkItemSLA({ sla }: { sla?: WorkItemSLAState }) {
  if (!sla) {
    return null;
  }

  return (
    <Card
      size="small"
      title={
        <span className="flex items-center gap-1.5">
          <Clock size={14} className="text-slate-500" />
          SLA 时效与承诺
        </span>
      }
      extra={<Tag color={sla.isBreached ? 'red' : 'blue'}>{sla.slaName}</Tag>}
    >
      <div className="bg-slate-50 p-3 rounded-xl border border-slate-100 space-y-2 text-xs">
        {sla.responseDeadline && (
          <div className="flex items-center justify-between">
            <span className="text-slate-500 text-[11px]">响应截止:</span>
            <span
              className={`font-mono text-xs ${
                sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0
                  ? 'text-red-600 font-bold'
                  : 'text-slate-800'
              }`}
            >
              {new Date(sla.responseDeadline).toLocaleString()}
              {sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0 && ' (已超时)'}
            </span>
          </div>
        )}

        {sla.resolutionDeadline && (
          <div className="flex items-center justify-between">
            <span className="text-slate-500 text-[11px]">解决截止:</span>
            <span
              className={`font-mono text-xs ${
                sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0
                  ? 'text-red-600 font-bold'
                  : 'text-slate-800'
              }`}
            >
              {new Date(sla.resolutionDeadline).toLocaleString()}
              {sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0 && ' (已超时)'}
            </span>
          </div>
        )}

        {sla.isBreached && (
          <div className="pt-1">
            <Tag color="red" className="w-full text-center">
              SLA 已违规
            </Tag>
          </div>
        )}

        {sla.responseTime > 0 && (
          <div className="space-y-1">
            <div className="flex justify-between text-[11px] text-slate-500">
              <span>响应进度</span>
              <span>
                {sla.responseTimeRemaining !== null ? `剩余 ${sla.responseTimeRemaining} 分钟` : '--'}
              </span>
            </div>
            <Progress
              percent={getSLAPercent(sla.responseTime, sla.responseTimeRemaining)}
              size="small"
              strokeColor={
                sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0
                  ? '#ff4d4f'
                  : getSLAPercent(sla.responseTime, sla.responseTimeRemaining) >= 70
                    ? '#fa8c16'
                    : '#52c41a'
              }
            />
            <div className="flex justify-between text-[11px] text-slate-400 font-mono">
              <span>
                {sla.responseTimeRemaining !== null
                  ? `剩余 ${formatHours(sla.responseTimeRemaining)} 小时`
                  : '--'}
              </span>
              <span>目标 {formatHours(sla.responseTime)} 小时</span>
            </div>
          </div>
        )}

        {sla.resolutionTime > 0 && (
          <div className="space-y-1">
            <div className="flex justify-between text-[11px] text-slate-500">
              <span>解决进度</span>
              <span>
                {sla.resolutionTimeRemaining !== null
                  ? `剩余 ${sla.resolutionTimeRemaining} 分钟`
                  : '--'}
              </span>
            </div>
            <Progress
              percent={getSLAPercent(sla.resolutionTime, sla.resolutionTimeRemaining)}
              size="small"
              strokeColor={
                sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0
                  ? '#ff4d4f'
                  : getSLAPercent(sla.resolutionTime, sla.resolutionTimeRemaining) >= 70
                    ? '#fa8c16'
                    : '#52c41a'
              }
            />
            <div className="flex justify-between text-[11px] text-slate-400 font-mono">
              <span>
                {sla.resolutionTimeRemaining !== null
                  ? `剩余 ${formatHours(sla.resolutionTimeRemaining)} 小时`
                  : '--'}
              </span>
              <span>目标 {formatHours(sla.resolutionTime)} 小时</span>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

export default WorkItemSLA;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemSLA.test.tsx`
Expected: PASS (3 tests)

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors (confirms nothing else referenced the old `WorkItemSLAState` shape).

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add src/components/work-item/WorkItemTypes.ts src/components/work-item/WorkItemSLA.tsx src/components/work-item/__tests__/WorkItemSLA.test.tsx
git commit -m "feat(work-item): add WorkItemSLA card, extend WorkItemSLAState to match TicketApi.getTicketSLA"
```

---

## Task 2: `toTargetType` mapping + real `WorkItemComments`/`WorkItemAttachments`

**Files:**
- Create: `itsm-frontend/src/components/work-item/toTargetType.ts`
- Create: `itsm-frontend/src/components/work-item/__tests__/toTargetType.test.ts`
- Modify: `itsm-frontend/src/components/work-item/WorkItemComments.tsx`
- Modify: `itsm-frontend/src/components/work-item/WorkItemAttachments.tsx`
- Create: `itsm-frontend/src/components/work-item/__tests__/WorkItemComments.test.tsx`
- Create: `itsm-frontend/src/components/work-item/__tests__/WorkItemAttachments.test.tsx`

**Interfaces:**
- Consumes: `useWorkItemContext()` (existing, `./WorkItemContext`); `CommentPanel`,
  `ticketCommentAdapter`, `AttachmentPanel`, `ticketAttachmentAdapter` (existing,
  `@/components/business/detail-tabs`, `TargetType` also re-exported from that same barrel via its
  `export * from './types'`); `useAuthStore()` (existing, `@/lib/store/auth-store`).
- Produces: `toTargetType(recordClass: WorkItemCommon['recordClass']): TargetType` — mirrors the
  backend's `resourceForRecordClass` (`itsm-backend/middleware/workitem_rbac.go`, Phase 1 Task 1)
  branch-for-branch, so keep the four cases lined up the same way.

- [ ] **Step 1: Write the failing tests**

Create `itsm-frontend/src/components/work-item/__tests__/toTargetType.test.ts`:

```ts
import { toTargetType } from '../toTargetType';

describe('toTargetType', () => {
  it.each([
    ['incident', 'incident'],
    ['problem', 'problem'],
    ['change_request', 'change'],
    ['generic', 'ticket'],
    ['service_request_item', 'ticket'],
    ['catalog_task', 'ticket'],
  ] as const)('maps recordClass %s to TargetType %s', (recordClass, expected) => {
    expect(toTargetType(recordClass)).toBe(expected);
  });
});
```

Create `itsm-frontend/src/components/work-item/__tests__/WorkItemComments.test.tsx`:

```tsx
import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemComments } from '../WorkItemComments';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

jest.mock('@/components/business/detail-tabs', () => ({
  CommentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="comment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  ticketCommentAdapter: {},
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: { id: 42 } }),
}));

const workItem: WorkItemCommon = {
  id: 7,
  number: 'PRB-1',
  recordClass: 'problem',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemComments', () => {
  it('renders CommentPanel with the mapped targetType and the given workItemId', () => {
    render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemComments workItemId={7} />
      </WorkItemProvider>
    );
    const panel = screen.getByTestId('comment-panel');
    expect(panel).toHaveAttribute('data-target-type', 'problem');
    expect(panel).toHaveAttribute('data-target-id', '7');
  });
});
```

Create `itsm-frontend/src/components/work-item/__tests__/WorkItemAttachments.test.tsx`:

```tsx
import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemAttachments } from '../WorkItemAttachments';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

jest.mock('@/components/business/detail-tabs', () => ({
  AttachmentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="attachment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  ticketAttachmentAdapter: {},
}));

const workItem: WorkItemCommon = {
  id: 9,
  number: 'C-1',
  recordClass: 'change_request',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemAttachments', () => {
  it('renders AttachmentPanel with the mapped targetType and the given workItemId', () => {
    render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemAttachments workItemId={9} />
      </WorkItemProvider>
    );
    const panel = screen.getByTestId('attachment-panel');
    expect(panel).toHaveAttribute('data-target-type', 'change');
    expect(panel).toHaveAttribute('data-target-id', '9');
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/toTargetType.test.ts src/components/work-item/__tests__/WorkItemComments.test.tsx src/components/work-item/__tests__/WorkItemAttachments.test.tsx`
Expected: FAIL — `toTargetType` module not found; `WorkItemComments`/`WorkItemAttachments` still
render the old placeholder `<Card data-work-item-id=.../>` with no `comment-panel`/`attachment-panel`
test id.

- [ ] **Step 3: Write minimal implementation**

Create `itsm-frontend/src/components/work-item/toTargetType.ts`:

```ts
import type { TargetType } from '@/components/business/detail-tabs';
import type { WorkItemCommon } from './WorkItemTypes';

// toTargetType 把 WorkItem 的 recordClass 映射到 detail-tabs 通用组件（CommentPanel/
// AttachmentPanel）用的 TargetType。与后端 middleware.resourceForRecordClass
// （itsm-backend/middleware/workitem_rbac.go）刻意保持同一组映射规则：incident/problem/
// change_request 三个专业域各自对应，其余 recordClass 统一落到 "ticket"。
export function toTargetType(recordClass: WorkItemCommon['recordClass']): TargetType {
  switch (recordClass) {
    case 'incident':
      return 'incident';
    case 'problem':
      return 'problem';
    case 'change_request':
      return 'change';
    default:
      return 'ticket';
  }
}
```

Replace `itsm-frontend/src/components/work-item/WorkItemComments.tsx` in full:

```tsx
'use client';

import React from 'react';
import { Card } from 'antd';
import { CommentPanel, ticketCommentAdapter } from '@/components/business/detail-tabs';
import { useAuthStore } from '@/lib/store/auth-store';
import { useWorkItemContext } from './WorkItemContext';
import { toTargetType } from './toTargetType';

// WorkItemComments 是 WorkItemShell 的评论区块。四个 recordClass（含已收口的 Incident——见
// docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md §4.2）统一走
// ticketCommentAdapter + workItemId，不需要再按 recordClass 切换 adapter。
export function WorkItemComments({ workItemId }: { workItemId: number }) {
  const { workItem } = useWorkItemContext();
  const { user } = useAuthStore();
  return (
    <Card size="small" title="评论">
      <CommentPanel
        targetType={toTargetType(workItem.recordClass)}
        targetId={workItemId}
        adapter={ticketCommentAdapter}
        currentUserId={user?.id}
        showInternalToggle
      />
    </Card>
  );
}
```

Replace `itsm-frontend/src/components/work-item/WorkItemAttachments.tsx` in full:

```tsx
'use client';

import React from 'react';
import { Card } from 'antd';
import { AttachmentPanel, ticketAttachmentAdapter } from '@/components/business/detail-tabs';
import { useWorkItemContext } from './WorkItemContext';
import { toTargetType } from './toTargetType';

// WorkItemAttachments 是 WorkItemShell 的附件区块。附件在 Incident/Problem/Change 迁移前
// 就没有专属实现（不像评论有 incident_events 需要先搬），四个 recordClass 从一开始就统一走
// ticketAttachmentAdapter，不需要 Phase 2 那样的迁移步骤。
export function WorkItemAttachments({ workItemId }: { workItemId: number }) {
  const { workItem } = useWorkItemContext();
  return (
    <Card size="small" title="附件">
      <AttachmentPanel
        targetType={toTargetType(workItem.recordClass)}
        targetId={workItemId}
        adapter={ticketAttachmentAdapter}
      />
    </Card>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/toTargetType.test.ts src/components/work-item/__tests__/WorkItemComments.test.tsx src/components/work-item/__tests__/WorkItemAttachments.test.tsx`
Expected: PASS (6 tests total across the three files)

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add src/components/work-item/toTargetType.ts src/components/work-item/WorkItemComments.tsx src/components/work-item/WorkItemAttachments.tsx src/components/work-item/__tests__/toTargetType.test.ts src/components/work-item/__tests__/WorkItemComments.test.tsx src/components/work-item/__tests__/WorkItemAttachments.test.tsx
git commit -m "feat(work-item): replace WorkItemComments/WorkItemAttachments placeholders with real CommentPanel/AttachmentPanel wiring"
```

---

## Task 3: `WorkItemActionBar`

**Files:**
- Create: `itsm-frontend/src/components/work-item/WorkItemActionBar.tsx`
- Create: `itsm-frontend/src/components/work-item/__tests__/WorkItemActionBar.test.tsx`

**Interfaces:**
- Consumes: `useWorkItemContext()` → `{ actions: Record<string, WorkItemActionState>,
  onActionDispatch: WorkItemActionDispatch }` (existing).
- Produces: `WorkItemActionBar(): JSX.Element | null`, consumed by Task 4's `WorkItemShell.tsx`.

- [ ] **Step 1: Write the failing test**

Create `itsm-frontend/src/components/work-item/__tests__/WorkItemActionBar.test.tsx`:

```tsx
import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { WorkItemActionBar } from '../WorkItemActionBar';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

const workItem: WorkItemCommon = {
  id: 1,
  number: 'X-1',
  recordClass: 'incident',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemActionBar', () => {
  it('renders nothing when actions is empty', () => {
    const { container } = render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders an enabled button for an allowed action and calls onActionDispatch with the action name', () => {
    const dispatch = jest.fn().mockResolvedValue(undefined);
    render(
      <WorkItemProvider
        value={{ workItem, actions: { resolve: { allowed: true } }, onActionDispatch: dispatch }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    const button = screen.getByRole('button', { name: '解决' });
    expect(button).toBeEnabled();
    fireEvent.click(button);
    expect(dispatch).toHaveBeenCalledWith('resolve');
  });

  it('disables the button and surfaces the reason as its title when not allowed', () => {
    render(
      <WorkItemProvider
        value={{
          workItem,
          actions: { close: { allowed: false, reason: '必须先填写解决方案' } },
          onActionDispatch: jest.fn(),
        }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    const button = screen.getByRole('button', { name: '关闭' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', '必须先填写解决方案');
  });

  it('falls back to the raw action key when there is no Chinese label for it', () => {
    render(
      <WorkItemProvider
        value={{
          workItem,
          actions: { convert_to_problem: { allowed: true } },
          onActionDispatch: jest.fn(),
        }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    expect(screen.getByRole('button', { name: 'convert_to_problem' })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemActionBar.test.tsx`
Expected: FAIL — `../WorkItemActionBar` module not found.

- [ ] **Step 3: Write minimal implementation**

Create `itsm-frontend/src/components/work-item/WorkItemActionBar.tsx`:

```tsx
'use client';

import React from 'react';
import { Button, Space } from 'antd';
import { useWorkItemContext } from './WorkItemContext';

// WorkItemActionBar 遍历 actions（通过 context 拿到），把每个动作渲染成一个按钮：
// disabled/title 取 allowed/reason，点击调用 onActionDispatch——见设计文档 §5.2。本组件不关心
// 具体业务含义，只负责渲染；Phase 4（后端 actions 计算）落地前三个页面传的都是空
// actions={{}}，此时不渲染任何按钮——这是"当前没有可执行的动作"这个真实状态，不是待完善的
// 占位态。
const ACTION_LABELS: Record<string, string> = {
  approve: '批准',
  reject: '拒绝',
  resolve: '解决',
  close: '关闭',
  assign: '指派',
  edit: '编辑',
  delete: '删除',
  cc: '抄送',
};

function labelFor(action: string): string {
  return ACTION_LABELS[action] ?? action;
}

export function WorkItemActionBar() {
  const { actions, onActionDispatch } = useWorkItemContext();
  const entries = Object.entries(actions);
  if (entries.length === 0) {
    return null;
  }
  return (
    <Space wrap>
      {entries.map(([action, state]) => (
        <Button
          key={action}
          disabled={!state.allowed}
          title={state.reason}
          onClick={() => void onActionDispatch(action)}
        >
          {labelFor(action)}
        </Button>
      ))}
    </Space>
  );
}

export default WorkItemActionBar;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemActionBar.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add src/components/work-item/WorkItemActionBar.tsx src/components/work-item/__tests__/WorkItemActionBar.test.tsx
git commit -m "feat(work-item): add WorkItemActionBar rendering actions from WorkItemContext"
```

---

## Task 4: Assemble everything into `WorkItemShell.tsx`

**Files:**
- Modify: `itsm-frontend/src/components/work-item/WorkItemShell.tsx`
- Modify: `itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx`

**Interfaces:**
- Consumes: `WorkItemSLA` (Task 1), `WorkItemActionBar` (Task 3), `WorkItemComments`/
  `WorkItemAttachments` (Task 2, unchanged prop signature from before — `{ workItemId: number }`),
  `TicketHistoryList`/`TicketRelationCards` (existing, `@/components/ticket/TicketHistoryList`,
  `@/components/ticket/TicketRelationCards`, prop `ticketId: number`).

- [ ] **Step 1: Update the failing/at-risk test**

The three existing tests in `WorkItemShell.test.tsx` don't change their assertions, but now render
several new children that make real API calls unless mocked. Replace the top of
`itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx` (before the `workItem`
const) with:

```tsx
import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemShell } from '../WorkItemShell';
import { useWorkItemContext } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

jest.mock('@/lib/api/ticket-api', () => ({
  TicketApi: {
    getTicketHistory: jest.fn().mockResolvedValue([]),
  },
}));

jest.mock('@/lib/api/ticket-relations-api', () => ({
  TicketRelationsApi: {
    getTicketRelations: jest.fn().mockResolvedValue([]),
  },
}));

jest.mock('@/lib/api/ticket-comment-api', () => ({
  TicketCommentApi: {
    getComments: jest.fn().mockResolvedValue({ comments: [], total: 0 }),
  },
}));

jest.mock('@/lib/api/ticket-attachment-api', () => ({
  TicketAttachmentApi: {
    listAttachments: jest.fn().mockResolvedValue({ attachments: [] }),
  },
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: { id: 1 } }),
}));
```

Leave the rest of the file (the `workItem` const, `ProbePanel`, and all three `it(...)` blocks)
unchanged — only these mocks are new, added above the existing content.

Then append two new tests at the end of the `describe('WorkItemShell', ...)` block, before its
closing `});`:

```tsx
  it('renders the SLA card when sla is provided', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{}}
        sla={{
          slaName: '标准 SLA',
          responseTime: 60,
          resolutionTime: 480,
          responseDeadline: '2026-08-28T10:00:00Z',
          resolutionDeadline: '2026-08-28T18:00:00Z',
          responseTimeRemaining: 30,
          resolutionTimeRemaining: 200,
          isBreached: false,
        }}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<div />}
      />
    );
    expect(screen.getByText('标准 SLA')).toBeInTheDocument();
  });

  it('does not render an SLA card when sla is not provided', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{}}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<div />}
      />
    );
    expect(screen.queryByText(/SLA 时效与承诺/)).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemShell.test.tsx`
Expected: FAIL — the two new tests fail (no SLA card rendered yet, since `WorkItemShell.tsx` doesn't
render `WorkItemSLA` yet). The three pre-existing tests should still PASS at this point (the mocks
added are additive and don't change current behavior) — if any of them unexpectedly fail here, stop
and investigate before proceeding to Step 3, since that would mean the mocks themselves are wrong,
not that Step 3's implementation is missing yet.

- [ ] **Step 3: Write minimal implementation**

Replace `itsm-frontend/src/components/work-item/WorkItemShell.tsx` in full:

```tsx
'use client';

import React from 'react';
import { Card, Space, Tag, Descriptions } from 'antd';
import { WorkItemProvider } from './WorkItemContext';
import type { WorkItemShellProps } from './WorkItemTypes';
import { WorkItemComments } from './WorkItemComments';
import { WorkItemAttachments } from './WorkItemAttachments';
import { WorkItemSLA } from './WorkItemSLA';
import { WorkItemActionBar } from './WorkItemActionBar';
import { TicketHistoryList } from '@/components/ticket/TicketHistoryList';
import { TicketRelationCards } from '@/components/ticket/TicketRelationCards';

// WorkItemShell 提供所有 recordClass 共用的公共区块骨架（编号/标题/状态/优先级/请求人/
// 分派/SLA/评论/附件/历史/关联/操作栏），专业字段由调用方通过 professionalPanelSlot 传入。
// 本组件本身不实现任何专业 Panel——那是各域专业组件（IncidentDetail/ProblemDetail/
// ChangeDetail）的范围。
//
// 不做的事：不在这里拼装任何具体域的 API 调用。所有动作都通过 onActionDispatch 回调
// 交给调用方处理，专业 Panel 也应该复用同一个回调，不要在 Panel 内部单独发 HTTP 请求。
export function WorkItemShell({
  workItem,
  actions,
  sla,
  onActionDispatch,
  professionalPanelSlot,
  loading,
  error,
}: WorkItemShellProps) {
  if (loading) {
    return <Card loading />;
  }
  if (error) {
    return <Card><Tag color="red">加载失败：{error}</Tag></Card>;
  }

  return (
    <WorkItemProvider value={{ workItem, actions, sla, onActionDispatch }}>
      <Space orientation="vertical" style={{ width: '100%' }} size="large">
        <Card>
          <Descriptions column={3} title={`${workItem.number} · ${workItem.title}`}>
            <Descriptions.Item label="状态">{workItem.status}</Descriptions.Item>
            <Descriptions.Item label="优先级">{workItem.priority}</Descriptions.Item>
            <Descriptions.Item label="处理人">{workItem.assigneeId ?? '未分配'}</Descriptions.Item>
          </Descriptions>
          <WorkItemActionBar />
        </Card>
        <WorkItemSLA sla={sla} />
        <Card>{professionalPanelSlot}</Card>
        <WorkItemComments workItemId={workItem.id} />
        <WorkItemAttachments workItemId={workItem.id} />
        <Card size="small" title="历史">
          <TicketHistoryList ticketId={workItem.id} />
        </Card>
        <Card size="small" title="关联">
          <TicketRelationCards ticketId={workItem.id} />
        </Card>
      </Space>
    </WorkItemProvider>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-frontend && npx jest src/components/work-item/__tests__/WorkItemShell.test.tsx`
Expected: PASS (5 tests: the original 3 plus the 2 new ones)

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

Run: `cd itsm-frontend && npx jest src/components/work-item/`
Expected: PASS — the entire `work-item/__tests__/` directory (all files from Tasks 1-4) is green.

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add src/components/work-item/WorkItemShell.tsx src/components/work-item/__tests__/WorkItemShell.test.tsx
git commit -m "feat(work-item): assemble SLA/Comments/Attachments/History/Relations/ActionBar into WorkItemShell"
```

---

## Task 5: Delete the orphaned `ProblemSLACard`

**Files:**
- Delete: `itsm-frontend/src/components/problem/ProblemSLACard.tsx`

**Interfaces:** none — verified zero import call sites anywhere in `src/` (only self-references
within the file itself and one unrelated comment in `problem-api.ts` mention its name). SLA for
Problem is now served by `WorkItemSLA` (Task 1) via the shared `/tickets/:id/sla` endpoint, per
design doc §6: "`ProblemSLACard.tsx` 是否保留/删除，视 §7 实施时是否还有调用方而定" — it has none.

- [ ] **Step 1: Delete the file**

```bash
cd itsm-frontend
git rm src/components/problem/ProblemSLACard.tsx
```

- [ ] **Step 2: Verify nothing referenced it**

Run: `cd itsm-frontend && grep -rn "ProblemSLACard" src` (excluding the comment in `problem-api.ts`
if it's still useful context — it may be worth trimming that comment too since the component it
refers to no longer exists, but that's optional cleanup, not required)
Expected: no import/JSX usage left.

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd itsm-frontend
git add -A src/components/problem/
git commit -m "chore(problem): remove orphaned ProblemSLACard, superseded by WorkItemSLA"
```

---

## Task 6: Wire real `sla` into the three pages, remove now-redundant page-level tabs

**Files:**
- Modify: `itsm-frontend/src/app/(main)/incidents/[id]/page.tsx`
- Modify: `itsm-frontend/src/app/(main)/problems/[id]/page.tsx`
- Modify: `itsm-frontend/src/app/(main)/changes/[id]/page.tsx`

**Interfaces:**
- Consumes: `TicketApi.getTicketSLA(id: number)` (existing, `@/lib/api/ticket-api`);
  `WorkItemSLAState` (Task 1's extended shape).

Why the page-level "历史" tabs (all three pages) and Incident's page-level "评论" tab are removed
here, not kept: `fetchAuditLogHistory`'s own doc comment
(`itsm-frontend/src/components/business/detail-tabs/adapters/audit-log-history-adapter.ts:5-6`)
says it's an explicit fallback "适用于尚未提供 /:id/history 端点的模块" — Phase 1 gave all four
recordClasses that endpoint, and Task 4 just wired `WorkItemShell`'s real `TicketHistoryList` block
to it, so the fallback's precondition no longer holds. Likewise Incident's page-level `CommentPanel`
(switched to `ticketCommentAdapter` + `workItem.id` in Phase 2 Task 3, as an interim measure to keep
comments working before this Shell block existed) now shows the exact same data Task 4's
`WorkItemComments` block already renders — keeping both would be the literal "复制" the design
doc's principle §3.3 rules out. `ProblemAssociationsTab` and `ApprovalTimeline` are **not** touched:
they're backed by genuinely different data (`ProblemApi.association*`, Change's CAB approval
records) that `TicketRelationCards` does not represent — removing them would be a capability
regression, not a dedup.

- [ ] **Step 1: `incidents/[id]/page.tsx` — add SLA fetch, remove the whole "协作与历史" block**

Replace the import block (lines 1-18):

```tsx
'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { App, Button, Card } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import IncidentDetail from '@/components/incident/IncidentDetail';
import { useAuthStore } from '@/lib/store/auth-store';
import { IncidentAPI, type Incident } from '@/lib/api/incident-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';
import dayjs from 'dayjs';

const formatDateTime = (v?: string) => (v ? dayjs(v).format('YYYY-MM-DD HH:mm') : '-');
```

(`useAuthStore` was already imported before Phase 2 Task 3 for `currentUserId`; it's no longer
needed directly in this file once the page-level `CommentPanel` is removed below — drop this import
too if nothing else in the file uses `user`. Check with `grep -n "\buser\b" "src/app/(main)/incidents/[id]/page.tsx"`
after Step 1's edits; if the only remaining reference was the removed `CommentPanel`'s
`currentUserId={user?.id}`, remove the `useAuthStore` import and its `const { user } = useAuthStore();`
line too.)

Add SLA state and a loader, right after the existing `const [workItem, setWorkItem] =
useState<WorkItemCommon | null>(null);` line:

```tsx
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);

  const loadSLA = useCallback(async (workItemId: number) => {
    try {
      const data = await TicketApi.getTicketSLA(workItemId);
      setSla({
        slaName: data.slaName,
        responseTime: data.responseTime,
        resolutionTime: data.resolutionTime,
        responseDeadline: data.responseDeadline,
        resolutionDeadline: data.resolutionDeadline,
        responseTimeRemaining: data.responseTimeRemaining,
        resolutionTimeRemaining: data.resolutionTimeRemaining,
        isBreached: data.isBreached,
      });
    } catch (err) {
      console.warn('[IncidentDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, loadSLA]);
```

Replace `detailAndTabs` (currently the `<IncidentDetail id={id} />` plus the "追加：协作与历史
Tabs" `Card` block) with just:

```tsx
  const detailAndTabs = (
    // 主详情组件保持不变——严重程度/影响范围/紧急程度/关联CI/升级状态等 Incident
    // 专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共
    // 身份信息，不重新实现这些逻辑。评论/历史现在由 WorkItemShell 自己的区块渲染
    // （见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md
    // §5.2），不再在这里重复一份。
    <IncidentDetail id={id} />
  );
```

Pass `sla` to `WorkItemShell`:

```tsx
        {workItem ? (
          <WorkItemShell
            workItem={workItem}
            sla={sla}
            actions={{}}
            onActionDispatch={async () => {}}
            professionalPanelSlot={detailAndTabs}
          />
        ) : (
          detailAndTabs
        )}
```

- [ ] **Step 2: `problems/[id]/page.tsx` — add SLA fetch, keep 关联 tab, drop 历史 tab**

Add the same `sla` state + `loadSLA` + effect pattern as Step 1 (import `TicketApi` from
`@/lib/api/ticket-api` and `WorkItemSLAState` alongside the existing `WorkItemCommon` import), and
pass `sla={sla}` to `<WorkItemShell>`.

Replace the import line `import { HistoryTimeline, fetchAuditLogHistory } from
'@/components/business/detail-tabs';` — delete it entirely (nothing else in this file uses either).
Also drop `Clock as HistoryIcon` from the lucide-react import (`import { ArrowLeft, Link2, Clock as
HistoryIcon } from 'lucide-react';` → `import { ArrowLeft, Link2 } from 'lucide-react';`), and drop
`Tabs` from the antd import (`import { App, Button, Card, Tabs } from 'antd';` → `import { App,
Button, Card } from 'antd';`).

Replace the "追加：关联 + 历史 Tabs" block (the `<Tabs defaultActiveKey="associations"
items={[...]}>` with its two items) with a direct render of just the associations content:

```tsx
      {/* 追加：关联（工单/事件/变更）。历史现在由 WorkItemShell 自己的区块渲染，不再
          在这里重复一份——见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md
          §5.2。这里仍然保留 ProblemAssociationsTab：它的数据走 ProblemApi 专属的关联接口，
          跟 WorkItemShell 的 TicketRelationCards（走 /tickets/:id/relations）不是同一份数据，
          删掉会丢功能，不是去重。 */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <Card className="mt-4 rounded-lg shadow-sm border border-gray-200">
          <div className="flex items-center gap-1.5 mb-3 text-sm font-medium text-gray-700">
            <Link2 size={14} />
            关联（工单/事件/变更）
          </div>
          <ProblemAssociationsTab problemId={numericId} />
        </Card>
      )}
```

- [ ] **Step 3: `changes/[id]/page.tsx` — add SLA fetch, keep 审批时间线, drop 历史 tab**

Add the same `sla` state + `loadSLA` + effect pattern as Step 1, and pass `sla={sla}` to
`<WorkItemShell>`.

Drop `HistoryTimeline, fetchAuditLogHistory` from the `@/components/business/detail-tabs` import
(keep `ApprovalTimeline`, `type ApprovalStep`, `type ApprovalStepStatus`). Drop `Clock as
HistoryIcon` from the lucide-react import (`import { GitBranch, Clock as HistoryIcon } from
'lucide-react';` → `import { GitBranch } from 'lucide-react';`). Drop `Tabs` from the antd import
(`import { App, Card, Tabs } from 'antd';` → `import { App, Card } from 'antd';`).

Replace the "追加：审批链 + 历史 Tabs" block (the `<Tabs defaultActiveKey="approvals"
items={[...]}>` with its two items) with a direct render of just the approvals content:

```tsx
      {/* 追加：审批时间线。历史现在由 WorkItemShell 自己的区块渲染，不再在这里重复一份——
          见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md §5.2。 */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <div style={{ padding: '0 24px 24px' }}>
          <Card className="mt-4 rounded-lg shadow-sm border border-gray-200">
            <div className="flex items-center gap-1.5 mb-3 text-sm font-medium text-gray-700">
              <GitBranch size={14} />
              审批时间线
            </div>
            {approvalLoading ? (
              <div className="p-6 text-center">加载中...</div>
            ) : (
              <ApprovalTimeline
                approvals={approvals}
                canApprove={false}
                showApprovalActions={false}
                formatDateTime={formatDateTime}
              />
            )}
          </Card>
        </div>
      )}
```

- [ ] **Step 4: Type-check all three**

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

- [ ] **Step 5: Run the frontend test suite**

Run: `cd itsm-frontend && npm test`
Expected: PASS — no test in the repo asserted on the removed page-level History/Comments tabs
(these pages had no existing dedicated test files per the codebase search during planning; if this
step turns up one, update its assertions to match the new layout rather than reverting the removal).

- [ ] **Step 6: Manual verification**

Per this repo's UI-change convention (CLAUDE.md "For UI or frontend changes, start the dev server
and use the feature in a browser"): start both backend and frontend, open one Incident, one Problem,
and one Change detail page each with a `workItemId` and an SLA policy bound. Confirm for each:
- The SLA card renders with real countdown data.
- History and Relations blocks render (empty state is fine if there's no data).
- Incident: no duplicate comment UI (only Shell's, not a second page-level one).
- Problem: 关联（工单/事件/变更） tab still works and shows Problem-specific associations, not an
  empty `TicketRelationCards` list standing in for it.
- Change: 审批时间线 still renders CAB approval history.

- [ ] **Step 7: Commit**

```bash
cd itsm-frontend
git add "src/app/(main)/incidents/[id]/page.tsx" "src/app/(main)/problems/[id]/page.tsx" "src/app/(main)/changes/[id]/page.tsx"
git commit -m "feat(work-item): wire real SLA data into detail pages, drop tabs superseded by WorkItemShell"
```

---

## Task 7: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Frontend**

Run: `cd itsm-frontend && npm run type-check && npm run lint:check && npm test`
Expected: all pass.

- [ ] **Step 2: Backend regression check**

Run: `cd itsm-backend && go build ./... && go test ./...`
Expected: pass — this phase makes no backend changes, but Phase 1/2 must still be green on the same
branch before this phase is considered mergeable.

- [ ] **Step 3: Commit (only if verification surfaced fixes)**

If any step above required a fix, commit it separately with a message describing what it addressed.
