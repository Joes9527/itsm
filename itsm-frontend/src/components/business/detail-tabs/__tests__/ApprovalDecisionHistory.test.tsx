import React from 'react';
import { render, screen, fireEvent, act } from '@/lib/test-utils';
import { ApprovalDecisionHistoryProvider } from '../ApprovalDecisionHistoryContext';
import { ApprovalMiniStepper } from '../ApprovalMiniStepper';
import { ApprovalWorkflowPanel } from '../ApprovalWorkflowPanel';
import { ProcessApprovalDecisionCards } from '@/components/ticket/ProcessApprovalDecisionCards';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: { getTicketApprovalDecisions: jest.fn() },
}));
const read = BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock;
const record = (nodeKey: string) => [{ id: 1, nodeKey, decision: 'approved', actorName: '主管' }];
const views = [
  ['mini', (id: number) => <ApprovalMiniStepper ticketId={id} />],
  ['panel', (id: number) => <ApprovalWorkflowPanel ticketId={id} isTicketFinal={false} />],
  ['cards', (id: number) => <ProcessApprovalDecisionCards ticketId={id} />],
] as const;

describe.each(views)('%s decision history', (_name, consumer) => {
  const view = (id: number) => <ApprovalDecisionHistoryProvider ticketId={id}>{consumer(id)}</ApprovalDecisionHistoryProvider>;
  beforeEach(() => read.mockReset());
  it('distinguishes read failure from empty history and retries', async () => {
    read.mockRejectedValueOnce(new Error('读取审批失败')).mockResolvedValueOnce([]);
    render(view(1));
    expect(await screen.findByRole('alert')).toHaveTextContent('读取审批失败');
    expect(screen.queryByText('暂无审批决策记录')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('暂无审批决策记录')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText('该工单未走审批流程')).not.toBeInTheDocument();
  });
  it('labels retained decisions when refreshing fails', async () => {
    read.mockResolvedValueOnce(record('已有决策')).mockRejectedValueOnce(new Error('网络异常'));
    render(view(1));
    await screen.findByText('已有决策', { exact: false });
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('刷新失败，当前显示上次读取的数据');
    expect(screen.getByText('已有决策', { exact: false })).toBeInTheDocument();
  });
  it('clears retained decisions when permission is denied', async () => {
    read.mockResolvedValueOnce(record('已有决策')).mockRejectedValueOnce(new ApiError('无权读取审批', 403));
    render(view(1));
    await screen.findByText('已有决策', { exact: false });
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('无权读取审批');
    expect(screen.queryByText('已有决策', { exact: false })).not.toBeInTheDocument();
    expect(screen.queryByText('暂无审批决策记录')).not.toBeInTheDocument();
  });
  it('treats malformed responses as failure instead of empty history', async () => {
    read.mockResolvedValueOnce(null);
    render(view(1));
    expect(await screen.findByRole('alert')).toHaveTextContent('审批决策记录格式异常');
    expect(screen.queryByText('暂无审批决策记录')).not.toBeInTheDocument();
  });
  it('isolates a tenant switch even when the ticket ID stays the same', async () => {
    const initialTenant = useAuthStore.getState().currentTenant;
    let resolve!: (value: unknown) => void;
    read.mockReturnValueOnce(new Promise(done => { resolve = done; })).mockResolvedValueOnce(record('新租户决策'));
    const { unmount } = render(view(1));
    try {
      act(() => useAuthStore.setState({ currentTenant: {
        id: 98765, name: '隔离测试', code: 'test', type: 'standard', status: 'active', createdAt: '', updatedAt: '',
      } }));
      await screen.findByText('新租户决策', { exact: false });
      await act(async () => resolve(record('旧租户决策')));
      expect(screen.queryByText('旧租户决策', { exact: false })).not.toBeInTheDocument();
    } finally {
      unmount();
      act(() => useAuthStore.setState({ currentTenant: initialTenant }));
    }
  });
  it('does not restore a previous ticket after navigation', async () => {
    let resolve!: (value: unknown) => void;
    read.mockReturnValueOnce(new Promise(done => { resolve = done; })).mockResolvedValueOnce(record('新工单决策'));
    const { rerender } = render(view(1));
    rerender(view(2));
    await screen.findByText('新工单决策', { exact: false });
    await act(async () => resolve(record('旧工单决策')));
    expect(screen.queryByText('旧工单决策', { exact: false })).not.toBeInTheDocument();
    expect(screen.getByText('新工单决策', { exact: false })).toBeInTheDocument();
  });
});
