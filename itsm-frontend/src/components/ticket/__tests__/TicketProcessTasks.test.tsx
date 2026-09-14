import React from 'react';
import { render, screen, fireEvent, act } from '@/lib/test-utils';
import { TicketProcessTasks } from '../TicketProcessTasks';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
jest.mock('@/lib/api/bpmn-workflow-api', () => ({ BPMNWorkflowApi: { listUserTasks: jest.fn() } }));
const read = BPMNWorkflowApi.listUserTasks as jest.Mock;
const task = { id: 8, businessType: 'service_request', businessId: 42, taskName: '主管审批', taskPurpose: 'approval', status: 'created', assignee: '主管甲' };
const page = (items: unknown[], total = items.length) => ({ items, total, page: 1, pageSize: 100 });
beforeEach(() => read.mockReset());
it('reads scoped tasks and links approvals without using the ticket assignee', async () => {
  read.mockResolvedValue(page([task]));
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  expect(await screen.findByText('主管审批')).toBeInTheDocument();
  expect(read).toHaveBeenCalledWith(expect.objectContaining({ businessType: 'service_request', businessId: 42 }));
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
it('exhausts pages before filtering terminal tasks', async () => {
  const closed = Array.from({ length: 100 }, (_, i) => ({ ...task, id: i + 100, status: 'completed' }));
  read.mockResolvedValueOnce(page(closed, 101)).mockResolvedValueOnce({ ...page([task], 101), page: 2 });
  render(<TicketProcessTasks ticketId={42} recordClass="service_request_item" />);
  await screen.findByText('主管审批');
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
  await screen.findByText('主管审批');
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
  expect(await screen.findByText('状态：未知状态（future_state）')).toBeInTheDocument();
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
