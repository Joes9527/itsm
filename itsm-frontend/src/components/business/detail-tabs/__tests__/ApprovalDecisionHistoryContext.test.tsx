import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@/lib/test-utils';
import { ApprovalDecisionHistoryProvider } from '../ApprovalDecisionHistoryContext';
import { useApprovalDecisionHistory } from '../useApprovalDecisionHistory';
import { ApiError } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApprovalMiniStepper } from '../ApprovalMiniStepper';
import { ProcessApprovalDecisionCards } from '@/components/ticket/ProcessApprovalDecisionCards';
import { DetailRefreshProvider, useDetailRefresh } from '../DetailRefreshContext';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: { getTicketApprovalDecisions: jest.fn() },
}));
const read = BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock;
const record = (nodeKey: string) => [{ id: 1, nodeKey, decision: 'approved', actorName: '主管' }];
function Refresh() {
  const refresh = useDetailRefresh()!;
  return (
    <>
      <button onClick={() => void refresh.refresh()}>刷新工单详情</button>
      <output data-testid='report'>{JSON.stringify(refresh.report)}</output>
    </>
  );
}
function Count({ ticketId }: { ticketId: number }) {
  const { decisionCount } = useApprovalDecisionHistory(ticketId);
  return <output data-testid='count'>{decisionCount ?? 'unknown'}</output>;
}
function Page({ ticketId = 1 }: { ticketId?: number }) {
  return (
    <DetailRefreshProvider identity={String(ticketId)}>
      <ApprovalDecisionHistoryProvider ticketId={ticketId}>
        <Refresh />
        <Count ticketId={ticketId} />
        <ApprovalMiniStepper ticketId={ticketId} />
        <ProcessApprovalDecisionCards ticketId={ticketId} />
      </ApprovalDecisionHistoryProvider>
    </DetailRefreshProvider>
  );
}
beforeEach(() => read.mockReset());
it('shares one initial decision read and one global refresh between both actual views', async () => {
  read.mockResolvedValue(record('原决策'));
  render(<Page />);
  await waitFor(() => expect(screen.getAllByText('原决策')).toHaveLength(2));
  expect(read).toHaveBeenCalledTimes(1);
  read.mockResolvedValue(record('新决策'));
  fireEvent.click(screen.getByRole('button', { name: '刷新工单详情' }));
  await waitFor(() => expect(screen.getAllByText('新决策')).toHaveLength(2));
  expect(read).toHaveBeenCalledTimes(2);
  expect(screen.queryByText('原决策')).not.toBeInTheDocument();
});

it('coalesces repeated refresh and local retry and keeps both views on temporary failure', async () => {
  read.mockResolvedValueOnce(record('保留决策')).mockRejectedValueOnce(new Error('网络离线'));
  render(<Page />);
  await waitFor(() => expect(screen.getAllByText('保留决策')).toHaveLength(2));
  fireEvent.click(screen.getByText('刷新工单详情'));
  await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2));
  expect(screen.getAllByText('保留决策')).toHaveLength(2);
  expect(screen.getByTestId('count')).toHaveTextContent('1');
  expect(screen.getByTestId('report')).toHaveTextContent('approval-decisions');
  let resolve!: (value: unknown) => void;
  read.mockReturnValueOnce(
    new Promise(done => {
      resolve = done;
    })
  );
  fireEvent.click(screen.getAllByRole('button', { name: '重试' })[0]);
  fireEvent.click(screen.getByText('刷新工单详情'));
  fireEvent.click(screen.getByText('刷新工单详情'));
  expect(read).toHaveBeenCalledTimes(3);
  await act(async () => resolve(record('恢复决策')));
  expect(screen.getAllByText('恢复决策')).toHaveLength(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
it('clears both views and decision count on denial and attributes the failed resource', async () => {
  read
    .mockResolvedValueOnce(record('敏感决策'))
    .mockRejectedValueOnce(new ApiError('无权读取', 403));
  render(<Page />);
  await waitFor(() => expect(screen.getAllByText('敏感决策')).toHaveLength(2));
  fireEvent.click(screen.getByText('刷新工单详情'));
  await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2));
  expect(screen.queryByText('敏感决策')).not.toBeInTheDocument();
  expect(screen.getByTestId('count')).toHaveTextContent('unknown');
  expect(screen.getByTestId('report')).toHaveTextContent('approval-decisions');
  expect(screen.getByTestId('report')).toHaveTextContent('无权读取');
  read.mockResolvedValueOnce([]);
  fireEvent.click(screen.getByText('刷新工单详情'));
  await waitFor(() => expect(screen.getAllByText('暂无审批决策记录')).toHaveLength(2));
  expect(screen.getByTestId('count')).toHaveTextContent('0');
});
it.each(['ticket', 'tenant', 'account'])(
  'isolates both views from late reads after %s changes',
  async kind => {
    const initial = useAuthStore.getState();
    let resolve!: (value: unknown) => void;
    read
      .mockReturnValueOnce(
        new Promise(done => {
          resolve = done;
        })
      )
      .mockResolvedValueOnce(record('新范围决策'));
    const { rerender, unmount } = render(<Page />);
    try {
      if (kind === 'ticket') rerender(<Page ticketId={2} />);
      else
        act(() =>
          useAuthStore.setState(
            kind === 'account'
              ? { user: { ...initial.user, id: 9999 } as typeof initial.user }
              : {
                  currentTenant: {
                    ...initial.currentTenant,
                    id: 9999,
                  } as typeof initial.currentTenant,
                }
          )
        );
      await waitFor(() => expect(screen.getAllByText('新范围决策')).toHaveLength(2));
      await act(async () => resolve(record('旧范围决策')));
      expect(screen.queryByText('旧范围决策')).not.toBeInTheDocument();
      expect(read).toHaveBeenCalledTimes(2);
    } finally {
      unmount();
      act(() =>
        useAuthStore.setState({ user: initial.user, currentTenant: initial.currentTenant })
      );
    }
  }
);
it('reports missing or mismatched owners without silently fetching', () => {
  const error = jest.spyOn(console, 'error').mockImplementation(() => {});
  try {
    expect(() => render(<ApprovalMiniStepper ticketId={1} />)).toThrow(
      'matching ApprovalDecisionHistoryProvider'
    );
    expect(() =>
      render(
        <ApprovalDecisionHistoryProvider ticketId={2}>
          <ApprovalMiniStepper ticketId={1} />
        </ApprovalDecisionHistoryProvider>
      )
    ).toThrow('matching ApprovalDecisionHistoryProvider');
    expect(read).not.toHaveBeenCalled();
  } finally {
    error.mockRestore();
  }
});
