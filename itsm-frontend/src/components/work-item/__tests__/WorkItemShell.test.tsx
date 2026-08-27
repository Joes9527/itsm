import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemShell } from '../WorkItemShell';
import { useWorkItemContext } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

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
});
