import React from 'react';
import { render, screen, fireEvent, act } from '@/lib/test-utils';
import { TicketProcessTasks } from '../TicketProcessTasks';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
jest.mock('@/lib/api/bpmn-workflow-api', () => ({ BPMNWorkflowApi: { listUserTasks: jest.fn(), claimTask: jest.fn(), completeTask: jest.fn() } }));
const read = BPMNWorkflowApi.listUserTasks as jest.Mock;
const task = { id: 8, businessType: 'service_request_item', businessId: 42, taskName: '主管审批', taskPurpose: 'approval', status: 'created', assignee: '主管甲' };
const page = (items: unknown[], total = items.length) => ({ items, total, page: 1, pageSize: 100 });
async function openCurrentTasks() {
  const toggle = await screen.findByRole('button', { name: /当前任务（\d+）/ });
  if (toggle.getAttribute('aria-expanded') === 'false') fireEvent.click(toggle);
}
async function openHistoryTasks() {
  const toggle = await screen.findByRole('button', { name: /历史任务（\d+）/ });
  if (toggle.getAttribute('aria-expanded') === 'false') fireEvent.click(toggle);
}
beforeEach(() => { jest.clearAllMocks(); read.mockReset(); });
it('reads scoped tasks and links approvals without using the ticket assignee', async () => {
  read.mockResolvedValue(page([task]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  expect(screen.getByText('主管审批')).toBeInTheDocument();
  expect(read).toHaveBeenCalledWith(expect.objectContaining({ businessType: 'service_request_item', businessId: 42 }));
  expect(screen.getByText('主管甲')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: '前往审批中心' })).toHaveAttribute('href', '/approvals');
});
it('does not equate no visible tasks with no workflow', async () => {
  read.mockResolvedValue(page([]));
  render(<TicketProcessTasks ticketId={42} recordClass="generic" />);
  expect(await screen.findByText('当前账号暂无可见的活动任务')).toBeInTheDocument();
  expect(screen.queryByText('未启动流程')).not.toBeInTheDocument();
});
it('shows read errors and retries', async () => {
  read.mockRejectedValueOnce(new Error('任务读取失败')).mockResolvedValue(page([]));
  render(<TicketProcessTasks ticketId={42} recordClass="generic" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('任务读取失败');
  expect(screen.queryByText('当前账号暂无可见的活动任务')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  await screen.findByText('当前账号暂无可见的活动任务');
});
it('rejects data from a different business identity', async () => {
  read.mockResolvedValue(page([{ ...task, businessId: 99 }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('任务关联不一致');
  expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
});
it('exhausts pages before grouping terminal tasks', async () => {
  const closed = Array.from({ length: 100 }, (_, i) => ({ ...task, id: i + 100, status: 'completed' }));
  read.mockResolvedValueOnce(page(closed, 101)).mockResolvedValueOnce({ ...page([task], 101), page: 2 });
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  expect(screen.getByText('主管审批')).toBeInTheDocument();
  expect(read).toHaveBeenCalledTimes(2);
});
it('fails closed for an unknown record class', async () => {
  render(<TicketProcessTasks ticketId={42} recordClass="unknown" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('暂不支持');
  expect(read).not.toHaveBeenCalled();
});

it('clears old tasks after a permission denial', async () => {
  read.mockResolvedValueOnce(page([task])).mockRejectedValueOnce(new ApiError('无权读取任务', 403));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  fireEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('无权读取任务');
  expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
});
it('does not restore a previous ticket after navigation', async () => {
  let resolve!: (value: unknown) => void;
  read.mockReturnValueOnce(new Promise(done => { resolve = done; })).mockResolvedValueOnce(page([]));
  const { rerender } = render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  rerender(<TicketProcessTasks ticketId={43} recordClass="service_request_item" />);
  await screen.findByText('当前账号暂无可见的活动任务');
  await act(async () => resolve(page([task])));
  expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
});
it('reports truncated pages instead of presenting a partial list', async () => {
  read.mockResolvedValue(page([task], 2));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('任务分页不完整');
  expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
});

it('preserves unknown task status as explicit text', async () => {
  read.mockResolvedValue(page([{ ...task, status: 'future_state', taskPurpose: '' }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  expect(screen.getByText('状态：未知状态（future_state）')).toBeInTheDocument();
  expect(screen.queryByRole('link', { name: '前往审批中心' })).not.toBeInTheDocument();
});

it('isolates a tenant change for the same ticket ID', async () => {
  const initial = useAuthStore.getState().currentTenant;
  let resolve!: (value: unknown) => void;
  read.mockReturnValueOnce(new Promise(done => { resolve = done; })).mockResolvedValueOnce(page([]));
  const { unmount } = render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  try {
    act(() => useAuthStore.setState({ currentTenant: { id: 98765, name: '测试', code: 'test', type: 'standard', status: 'active', createdAt: '', updatedAt: '' } }));
    await screen.findByText('当前账号暂无可见的活动任务');
    await act(async () => resolve(page([task])));
    expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
  } finally {
    unmount();
    act(() => useAuthStore.setState({ currentTenant: initial }));
  }
});

it('claims a task only when the server offers claim and refreshes its state', async () => {
  read.mockResolvedValueOnce(page([{ ...task, taskPurpose: '', uiActions: { claim: true, complete: false } }])).mockResolvedValue(page([]));
  (BPMNWorkflowApi.claimTask as jest.Mock).mockResolvedValue(undefined);
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  fireEvent.click(await screen.findByRole('button', { name: '领取任务' }));
  await screen.findByText('当前账号暂无可见的活动任务');
  expect(BPMNWorkflowApi.claimTask).toHaveBeenCalledWith(8, expect.any(Function));
});
it('confirms completion then calls the canonical task command', async () => {
  read.mockResolvedValueOnce(page([{ ...task, taskPurpose: '', uiActions: { claim: false, complete: true } }])).mockResolvedValue(page([]));
  (BPMNWorkflowApi.completeTask as jest.Mock).mockResolvedValue(undefined);
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  fireEvent.click(await screen.findByRole('button', { name: '完成任务' }));
  expect(BPMNWorkflowApi.completeTask).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole('button', { name: '确认完成' }));
  await screen.findByText('当前账号暂无可见的活动任务');
  expect(BPMNWorkflowApi.completeTask).toHaveBeenCalledWith(8, {}, expect.any(Function));
});
it('keeps old servers read only and explains unsupported forms', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', uiActions: { claim: false, complete: false, reason: '此任务需要填写专用表单' } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  expect(screen.getByText('此任务需要填写专用表单')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '完成任务' })).not.toBeInTheDocument();
});

it('does not duplicate a pending claim', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', uiActions: { claim: true, complete: false } }]));
  let finish!: () => void;
  (BPMNWorkflowApi.claimTask as jest.Mock).mockReturnValue(new Promise<void>(done => { finish = done; }));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  const button = await screen.findByRole('button', { name: '领取任务' });
  fireEvent.click(button); fireEvent.click(button);
  expect(BPMNWorkflowApi.claimTask).toHaveBeenCalledTimes(1);
  await act(async () => finish());
});
it('drops a pending completion dialog when the tenant changes', async () => {
  const initial = useAuthStore.getState().currentTenant;
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', uiActions: { claim: false, complete: true } }]));
  const { unmount } = render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  try {
    fireEvent.click(await screen.findByRole('button', { name: '完成任务' }));
    await screen.findByRole('dialog');
    act(() => useAuthStore.setState({ currentTenant: { id: 87654, name: '测试', code: 'test', type: 'standard', status: 'active', createdAt: '', updatedAt: '' } }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(BPMNWorkflowApi.completeTask).not.toHaveBeenCalled();
  } finally { unmount(); act(() => useAuthStore.setState({ currentTenant: initial })); }
});
it('clears actions when a mutation loses permission', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', uiActions: { claim: true, complete: false } }]));
  (BPMNWorkflowApi.claimTask as jest.Mock).mockRejectedValue(new ApiError('任务权限已变化', 403));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  fireEvent.click(await screen.findByRole('button', { name: '领取任务' }));
  await screen.findAllByText('任务权限已变化');
  expect(screen.queryByRole('button', { name: '领取任务' })).not.toBeInTheDocument();
});

it.each(['generic','service_request_item','incident','problem','change_request','catalog_task'])('queries canonical %s process identity', async recordClass => {
 read.mockResolvedValue(page([{ ...task, businessType: recordClass }]));
 render(<TicketProcessTasks ticketId={42} recordClass={recordClass} />);
 await openCurrentTasks();
 expect(screen.getByText('主管审批')).toBeInTheDocument();
 expect(read).toHaveBeenCalledWith(expect.objectContaining({ businessType: recordClass, businessId: 42 }));
});

it('shows a bound task waiting for WorkItem assignment without actions', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', assignee: '', assigneeSource: 'work_item_assignee',
    assignmentState: 'unassigned', responsibleUserId: 0, actorId: 0,
    uiActions: { claim: false, complete: false } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  await openCurrentTasks();
  expect(screen.getByText('等待工单分配处理人')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '领取任务' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '完成任务' })).not.toBeInTheDocument();
  expect(screen.queryByText(/用户 ID/)).not.toBeInTheDocument();
});

it('renders the backend owner projection and offered completion for a bound task', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', assignee: '17', assigneeSource: 'work_item_assignee',
    assignmentState: 'assigned', responsibleUserId: 17, actorId: 0,
    uiActions: { claim: false, complete: true } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  expect(await screen.findByText('处理人用户 ID：17')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '完成任务' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '领取任务' })).not.toBeInTheDocument();
});

it('shows unavailable bound assignment without guessing a user', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', assignee: '', assigneeSource: 'work_item_assignee',
    assignmentState: 'unavailable', responsibleUserId: 0, actorId: 0,
    uiActions: { claim: false, complete: false } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  await openCurrentTasks();
  expect(screen.getByText('处理人当前不可用')).toBeInTheDocument();
  expect(screen.queryByText(/用户 ID/)).not.toBeInTheDocument();
});

it('renders frozen terminal responsibility and actual actor separately', async () => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: '', status: 'cancelled', assignee: '',
    assigneeSource: 'work_item_assignee', assignmentState: 'terminal', responsibleUserId: 0, actorId: 23,
    uiActions: { claim: false, complete: false } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  await openHistoryTasks();
  expect(screen.getByText('历史处理记录')).toBeInTheDocument();
  expect(screen.getByText('实际操作人用户 ID：23')).toBeInTheDocument();
  expect(screen.queryByText('处理人用户 ID：23')).not.toBeInTheDocument();
});

it.each([['completed', '已完成'], ['cancelled', '已取消']])('shows bound terminal status %s and unavailable history', async (status, label) => {
  read.mockResolvedValue(page([{ ...task, taskPurpose: 'fulfillment', assigneeSource: 'work_item_assignee', assignmentState: 'unavailable', status, responsibleUserId: 0, uiActions: { claim: false, complete: false } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openHistoryTasks();
  expect(screen.getByText(`状态：${label}`)).toBeInTheDocument();
  expect(screen.getByText('历史处理人记录不可用')).toBeInTheDocument();
  expect(screen.queryByText('处理人当前不可用')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '完成任务' })).not.toBeInTheDocument();
});
it('shows the backend execution denial for an assigned bound task', async () => {
  const reason = '当前账号无权执行此任务，请联系管理员核验任务及业务权限';
  read.mockResolvedValue(page([{ ...task, taskPurpose: 'fulfillment', assigneeSource: 'work_item_assignee', assignmentState: 'assigned', responsibleUserId: 7, uiActions: { claim: false, complete: false, reason } }]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await openCurrentTasks();
  expect(screen.getByText(reason)).toBeInTheDocument();
  expect(screen.getByText('已由工单分配')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '完成任务' })).not.toBeInTheDocument();
});

it('separates concurrent actionable tasks from terminal task history', async () => {
  read.mockResolvedValue(page([
    { ...task, id: 8, taskName: '处理任务甲', taskPurpose: 'fulfillment', uiActions: { claim: true, complete: false } },
    { ...task, id: 9, taskName: '处理任务乙', taskPurpose: 'fulfillment', uiActions: { claim: false, complete: true } },
    { ...task, id: 10, taskName: '已完成任务', taskPurpose: 'fulfillment', status: 'completed', uiActions: { claim: false, complete: false } },
  ]));

  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  expect(await screen.findByRole('button', { name: '当前任务（2）' })).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('处理任务甲')).toBeInTheDocument();
  expect(screen.getByText('处理任务乙')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '历史任务（1）' })).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('已完成任务')).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '历史任务（1）' }));
  expect(screen.getByText('已完成任务')).toBeInTheDocument();
});

it('keeps history collapsed when there are no active tasks and preserves actor ownership details', async () => {
  read.mockResolvedValue(page([{ ...task, status: 'cancelled', taskName: '已取消任务', taskPurpose: 'fulfillment',
    assigneeSource: 'work_item_assignee', assignmentState: 'terminal', responsibleUserId: 17, actorId: 23,
    uiActions: { claim: false, complete: false } }]));

  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  expect(await screen.findByText('当前账号暂无可见的活动任务')).toBeInTheDocument();
  const history = screen.getByRole('button', { name: '历史任务（1）' });
  expect(history).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('实际操作人用户 ID：23')).not.toBeInTheDocument();
  fireEvent.click(history);
  expect(screen.getByText('处理人用户 ID：17')).toBeInTheDocument();
  expect(screen.getByText('实际操作人用户 ID：23')).toBeInTheDocument();
});

it('preserves a user-collapsed current task group across an ordinary refresh', async () => {
  const actionable = { ...task, taskPurpose: 'fulfillment', uiActions: { claim: true, complete: false } };
  read.mockResolvedValue(page([actionable]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  const current = await screen.findByRole('button', { name: '当前任务（1）' });
  expect(current).toHaveAttribute('aria-expanded', 'true');
  fireEvent.click(current);
  expect(current).toHaveAttribute('aria-expanded', 'false');
  fireEvent.click(screen.getByRole('button', { name: '刷新' }));

  await act(async () => { expect(read).toHaveBeenCalledTimes(2); });
  expect(screen.getByRole('button', { name: '当前任务（1）' })).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('主管审批')).not.toBeInTheDocument();
});

it('initializes current-task expansion again after the ticket identity changes', async () => {
  const actionable = { ...task, taskPurpose: 'fulfillment', uiActions: { claim: true, complete: false } };
  read.mockResolvedValueOnce(page([actionable])).mockResolvedValueOnce(page([{ ...actionable, businessId: 43 }]));
  const { rerender } = render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);

  const current = await screen.findByRole('button', { name: '当前任务（1）' });
  fireEvent.click(current);
  expect(current).toHaveAttribute('aria-expanded', 'false');

  rerender(<TicketProcessTasks ticketId={43} recordClass="service_request_item" />);
  expect(await screen.findByRole('button', { name: '当前任务（1）' })).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('主管审批')).toBeInTheDocument();
});
