import React from 'react';
import { App } from 'antd';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent, { PointerEventsCheckLevel } from '@testing-library/user-event';
import TicketDetail from '../TicketDetail';
import { TicketApi } from '@/lib/api/ticket-api';
import { TicketCommentApi } from '@/lib/api/ticket-comment-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { UserApi } from '@/lib/api/user-api';
import { KnowledgeBaseApi } from '@/lib/api/knowledge-base-api';
import { aiTriage } from '@/lib/api/ai-api';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
jest.mock('next/navigation', () => ({
  useParams: () => ({}),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/api/ticket-api');
jest.mock('@/lib/api/ticket-comment-api');
jest.mock('@/lib/api/bpmn-workflow-api');
jest.mock('@/lib/api/user-api');
jest.mock('@/lib/api/knowledge-base-api');
jest.mock('@/lib/api/ai-api');
const ticket = {
  id: 101,
  ticketNumber: 'T-101',
  title: '刷新测试工单',
  description: '正文',
  status: 'open',
  priority: 'high',
  recordClass: 'generic',
  version: 1,
  actions: {},
};
beforeEach(() => {
  jest.clearAllMocks();
  useAuthStore.setState({
    user: { id: 7, tenantId: 1, permissions: ['ai:read'] } as any,
    isAuthenticated: true,
  });
  (TicketApi.getTicket as jest.Mock).mockResolvedValue(ticket);
  (TicketApi.getTicketSLA as jest.Mock).mockResolvedValue(null);
  (TicketApi.getTicketHistory as jest.Mock).mockResolvedValue([]);
  (TicketCommentApi.getComments as jest.Mock).mockResolvedValue({ comments: [], total: 0 });
  (BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock).mockResolvedValue([]);
  (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValue({
    items: [],
    total: 0,
    page: 1,
    pageSize: 100,
  });
  (UserApi.getUsers as jest.Mock).mockResolvedValue({ users: [] });
  (KnowledgeBaseApi.search as jest.Mock).mockResolvedValue({ articles: [] });
  (aiTriage as jest.Mock).mockResolvedValue(null);
});

const mount = () =>
  render(
    <App>
      <TicketDetail id='101' />
    </App>
  );

it.each(['claim', 'complete'])('refreshes directed outcomes after %s and retries only the failed read', async action => {
  const task = { id: 8, businessType: 'generic', businessId: 101, taskName: '交付设备', status: 'created',
    taskPurpose: 'fulfillment', uiActions: { claim: action === 'claim', complete: action === 'complete' } };
  (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValueOnce({ items: [task], total: 1, page: 1, pageSize: 100 });
  const command = action === 'claim' ? BPMNWorkflowApi.claimTask : BPMNWorkflowApi.completeTask;
  (command as jest.Mock).mockResolvedValue(undefined);
  mount();
  fireEvent.click(await screen.findByText(action === 'claim' ? '领取任务' : '完成任务'));
  if (action === 'complete') {
    (BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock).mockRejectedValueOnce(new Error('审批离线'));
    fireEvent.click(await screen.findByText('确认完成'));
  } else {
    (TicketApi.getTicket as jest.Mock).mockRejectedValueOnce(new Error('主体离线'));
  }
  expect(await screen.findByText('操作已完成，部分数据更新失败')).toBeInTheDocument();
  expect(command).toHaveBeenCalledTimes(1);
  expect(TicketApi.getTicket).toHaveBeenCalledTimes(2);
  expect(BPMNWorkflowApi.getTicketApprovalDecisions).toHaveBeenCalledTimes(2);
  expect(BPMNWorkflowApi.listUserTasks).toHaveBeenCalledTimes(2);
  expect(TicketCommentApi.getComments).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByLabelText('重试'));
  await waitFor(() => expect(action === 'claim' ? TicketApi.getTicket : BPMNWorkflowApi.getTicketApprovalDecisions).toHaveBeenCalledTimes(3));
  expect(command).toHaveBeenCalledTimes(1);
});

it.each(['success', 'denial'])('skips tasks during a command and supersedes late pre-write %s while locking commands until all outcomes settle', async staleOutcome => {
  let finishCommand!: () => void;
  let finishOldTicket!: (value: unknown) => void;
  let denyOldTicket!: (reason: unknown) => void;
  let finishApproval!: (value: unknown) => void;
  const task = { id: 8, businessType: 'generic', businessId: 101, taskName: '交付设备', status: 'created',
    taskPurpose: 'fulfillment', uiActions: { claim: true } };
  (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValue({ items: [task], total: 1, page: 1, pageSize: 100 });
  (BPMNWorkflowApi.claimTask as jest.Mock).mockImplementationOnce(() => new Promise<void>(done => { finishCommand = done; }));
  mount();
  fireEvent.click(await screen.findByText('领取任务'));
  (TicketApi.getTicket as jest.Mock).mockImplementationOnce(() => new Promise((done, reject) => { finishOldTicket = done; denyOldTicket = reject; }));
  fireEvent.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(TicketApi.getTicket).toHaveBeenCalledTimes(2));
  expect(BPMNWorkflowApi.listUserTasks).toHaveBeenCalledTimes(1);
  (TicketApi.getTicket as jest.Mock).mockResolvedValue({ ...ticket, title: '完成后的工单', version: 2 });
  (BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock).mockImplementationOnce(() => new Promise(done => { finishApproval = done; }));
  await act(async () => finishCommand());
  await screen.findByText('#101 完成后的工单');
  expect(BPMNWorkflowApi.listUserTasks).toHaveBeenCalledTimes(2);
  expect(screen.getByText('领取任务').closest('button')).toBeDisabled();
  fireEvent.click(screen.getByText('领取任务'));
  expect(BPMNWorkflowApi.claimTask).toHaveBeenCalledTimes(1);
  await act(async () => staleOutcome === 'success'
    ? finishOldTicket({ ...ticket, title: '过时工单' })
    : denyOldTicket(new ApiError('过时拒绝', 403)));
  expect(screen.queryByText('#101 过时工单')).not.toBeInTheDocument();
  expect(screen.getByText('#101 完成后的工单')).toBeInTheDocument();
  await act(async () => finishApproval([]));
  await waitFor(() => expect(screen.getByText('领取任务').closest('button')).not.toBeDisabled());
  expect(TicketApi.getTicket).toHaveBeenCalledTimes(3);
  expect(BPMNWorkflowApi.claimTask).toHaveBeenCalledTimes(1);
});
it('preserves the real comment editor through header and keyboard refresh and retains background failures', async () => {
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await screen.findByText('#101 刷新测试工单');
  expect(screen.queryByLabelText('刷新工单详情')).toBeInTheDocument();
  await user.type(await screen.findByLabelText('评论'), '未提交草稿');
  const editor = screen.getByLabelText('评论');
  await user.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(TicketApi.getTicket).toHaveBeenCalledTimes(2));
  expect(screen.getByLabelText('评论')).toBe(editor);
  expect(editor).toHaveValue('未提交草稿');
  (TicketApi.getTicket as jest.Mock).mockRejectedValueOnce(new Error('主体离线'));
  fireEvent.keyDown(document.body, { altKey: true, key: 'r' });
  expect(await screen.findByText(/工单详情：主体离线/)).toBeInTheDocument();
  expect(editor).toHaveValue('未提交草稿');
  expect(screen.queryByText('加载失败')).not.toBeInTheDocument();
  expect(TicketCommentApi.getComments).toHaveBeenCalledTimes(3);
});
it('refreshes mounted hidden history without prefetching unopened history', async () => {
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await screen.findByText('#101 刷新测试工单');
  expect(TicketApi.getTicketHistory).not.toHaveBeenCalled();
  await user.click(screen.getByText(/^历史流转/));
  await screen.findByText('暂无流转历史');
  await user.click(screen.getByText(/^协作沟通与评论/));
  await user.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(TicketApi.getTicketHistory).toHaveBeenCalledTimes(2));
});
it('clears detail and draft on final denial', async () => {
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await screen.findByText('#101 刷新测试工单');
  expect(screen.queryByLabelText('刷新工单详情')).toBeInTheDocument();
  await user.type(await screen.findByLabelText('评论'), '秘密草稿');
  (TicketApi.getTicket as jest.Mock).mockRejectedValueOnce(new ApiError('无权读取此工单', 403));
  await user.click(screen.getByLabelText('刷新工单详情'));
  await screen.findByText('加载失败');
  expect(screen.queryByText('#101 刷新测试工单')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('评论')).not.toBeInTheDocument();
});

it('skips comments while a write is pending and updates other areas, then verifies the confirmed write', async () => {
  let finish!: () => void;
  (TicketCommentApi.createComment as jest.Mock).mockImplementationOnce(
    () =>
      new Promise(resolve => {
        finish = () => resolve({ id: 1 });
      })
  );
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await user.type(await screen.findByLabelText('评论'), '待发布评论');
  await user.click(screen.getByText('发送评论'));
  await waitFor(() => expect(TicketCommentApi.createComment).toHaveBeenCalledTimes(1));
  await user.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(TicketApi.getTicket).toHaveBeenCalledTimes(2));
  expect(TicketCommentApi.getComments).toHaveBeenCalledTimes(1);
  expect(await screen.findByText('部分区域正在操作或已离开，未刷新。')).toBeInTheDocument();
  (TicketCommentApi.getComments as jest.Mock).mockResolvedValueOnce({
    comments: [{ id: 1, userId: 7, content: '已发布评论' }],
    total: 1,
  });
  await act(async () => finish());
  await screen.findByText('已发布评论');
  expect(TicketCommentApi.getComments).toHaveBeenCalledTimes(2);
});
it('does not generate AI suggestions again on a plain refresh', async () => {
  mount();
  await screen.findByText('#101 刷新测试工单');
  await waitFor(() => expect(aiTriage).toHaveBeenCalledTimes(1), { timeout: 2000 });
  fireEvent.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(TicketApi.getTicket).toHaveBeenCalledTimes(2));
  await act(async () => {
    await new Promise(resolve => setTimeout(resolve, 900));
  });
  expect(aiTriage).toHaveBeenCalledTimes(1);
});
it('clears editor context when tenant identity changes and discards a late main read', async () => {
  let finish!: (value: unknown) => void;
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await user.type(await screen.findByLabelText('评论'), '旧租户草稿');
  (TicketApi.getTicket as jest.Mock).mockImplementationOnce(
    () =>
      new Promise(resolve => {
        finish = resolve;
      })
  );
  await user.click(screen.getByLabelText('刷新工单详情'));
  act(() => useAuthStore.setState({ user: { id: 7, tenantId: 2, permissions: [] } as any }));
  await waitFor(() => expect(screen.getByLabelText('评论')).toHaveValue(''));
  await act(async () => finish({ ...ticket, title: '旧租户迟到数据' }));
  expect(screen.queryByText(/旧租户迟到数据/)).not.toBeInTheDocument();
});

it('does not reopen a sensitive edit form after a denied read is retried', async () => {
  (TicketApi.getTicket as jest.Mock).mockResolvedValue({
    ...ticket,
    actions: { edit: { allowed: true } },
  });
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
  await user.type(await screen.findByLabelText('工单标题'), '敏感编辑草稿');
  (TicketApi.getTicket as jest.Mock).mockRejectedValueOnce(new ApiError('详情权限撤销', 403));
  fireEvent.keyDown(document.body, { key: 'r', altKey: true });
  await screen.findByText('加载失败');
  await user.click(screen.getByText(/重\s*试/).closest('button')!);
  await screen.findByText('#101 刷新测试工单');
  expect(screen.queryByLabelText('工单标题')).not.toBeInTheDocument();
});

it('shares sidebar decisions with the approval tab and derives its count from the same response', async () => {
  const read = BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock;
  read.mockResolvedValue([{ id: 1, nodeKey: '共享决策', decision: 'approved', actorName: '主管' }]);
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  mount();
  await screen.findByText('共享决策');
  expect(screen.getByText(/审批链.*1/)).toBeInTheDocument();
  expect(read).toHaveBeenCalledTimes(1);
  await user.click(screen.getByText(/^审批链/));
  await waitFor(() => expect(screen.getAllByText('共享决策')).toHaveLength(2));
  expect(read).toHaveBeenCalledTimes(1);
  read.mockResolvedValue([]);
  await user.click(screen.getByLabelText('刷新工单详情'));
  await waitFor(() => expect(screen.getAllByText('暂无审批决策记录')).toHaveLength(2));
  expect(screen.getByText(/审批链.*0/)).toBeInTheDocument();
  expect(read).toHaveBeenCalledTimes(2);
});
