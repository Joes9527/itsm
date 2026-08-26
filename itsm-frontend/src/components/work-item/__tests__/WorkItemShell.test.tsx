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
  const { workItem: fromContext } = useWorkItemContext();
  return <div data-testid="probe">{fromContext.title}</div>;
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
