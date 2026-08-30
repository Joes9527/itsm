import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemShell } from '../WorkItemShell';
import { useWorkItemContext } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

// Mock detail-tabs components to prevent real network calls from CommentPanel/AttachmentPanel
jest.mock('@/components/business/detail-tabs', () => ({
  CommentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="mocked-comment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  AttachmentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="mocked-attachment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  ticketCommentAdapter: {},
  ticketAttachmentAdapter: {},
}));

// Mock auth store to provide user context for WorkItemComments
jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: { id: 1 } }),
}));

// Mock the API modules backing the newly-wired TicketHistoryList/TicketRelationCards
// (Task 4) so their real network calls don't fire during render.
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

// Defensive mocks: CommentPanel/AttachmentPanel are already mocked above (whole
// @/components/business/detail-tabs module), so the real ticket-comment-adapter/
// ticket-attachment-adapter (and thus these API modules) never load in this test file.
// Mocked anyway per plan brief in case that indirection changes.
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

const workItem: WorkItemCommon = {
  id: 1,
  number: 'INC-202608-000001',
  recordClass: 'incident',
  title: '测试事件',
  status: 'in_progress',
  priority: 'high',
  requesterId: 10,
  createdAt: '2026-08-26T00:00:00Z',
  updatedAt: '2026-08-26T00:00:00Z',
};

const props = {
  workItem,
  actions: { approve: { allowed: true } },
  onActionDispatch: jest.fn(),
};

function ProbePanel() {
  const { workItem: fromContext, actions } = useWorkItemContext();
  return (
    <div data-testid="probe">
      {fromContext.title}
      <span data-testid="probe-resolve-allowed">{String(actions.resolve?.allowed)}</span>
      <span data-testid="probe-close-reason">{actions.close?.reason ?? ''}</span>
      <span data-testid="probe-action-count">{Object.keys(actions).length}</span>
    </div>
  );
}

describe('WorkItemShell', () => {
  it('does not render the generic action bar unless explicitly enabled', () => {
    render(<WorkItemShell {...props} professionalPanelSlot={<div>panel</div>} />);

    expect(screen.queryByRole('button', { name: '批准' })).not.toBeInTheDocument();
  });

  it('renders the generic action bar when showActionBar is true', () => {
    render(<WorkItemShell {...props} showActionBar professionalPanelSlot={<div>panel</div>} />);

    expect(screen.getByRole('button', { name: '批准' })).toBeInTheDocument();
  });

  it('renders the common fields and exposes them via useWorkItemContext to the professional panel slot', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{ resolve: { allowed: true } }}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<ProbePanel />}
      />
    );
    expect(screen.getByText(/INC-202608-000001/)).toBeInTheDocument();
    expect(screen.getByTestId('probe')).toHaveTextContent('测试事件');
  });

  // 锁定契约：Shell 收下的 actions 必须原样进入 context。Wave 2 的专业 Panel 靠它渲染
  // 按钮的禁用态和禁用原因；如果 Shell 只解构不透传，Panel 拿不到就只能各自复刻一套
  // 权限判断——正是 §4.4 要求"锁定契约"想避免的分叉。
  it('passes the actions map through to the professional panel via useWorkItemContext', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{
          resolve: { allowed: true },
          close: { allowed: false, reason: '必须先填写解决方案' },
        }}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<ProbePanel />}
      />
    );
    expect(screen.getByTestId('probe-resolve-allowed')).toHaveTextContent('true');
    expect(screen.getByTestId('probe-close-reason')).toHaveTextContent('必须先填写解决方案');
    expect(screen.getByTestId('probe-action-count')).toHaveTextContent('2');
  });

  it('shows an error state without rendering the panel slot', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{}}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<ProbePanel />}
        error="加载失败"
      />
    );
    expect(screen.queryByTestId('probe')).not.toBeInTheDocument();
  });

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
});
