import { act, fireEvent, render, screen, waitFor } from '@/lib/test-utils';
import ApprovalsCenterPage from '../page';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { ApiError } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

jest.mock('@/lib/api/bpmn-workflow-api', () => ({ BPMNWorkflowApi: {
  listUserTasks: jest.fn(), claimTask: jest.fn(), submitApprovalDecision: jest.fn(),
} }));
const list = jest.mocked(BPMNWorkflowApi.listUserTasks);
const task = { id: 101, taskName: '经理审批', taskPurpose: 'approval', status: 'created', assignee: '', processInstanceId: 12, createdTime: '2026-09-01T00:00:00Z' };
function rows(items = [task]) {
  list.mockImplementation(async params => ({ items: items.filter(item => item.status === params?.status), total: items.filter(item => item.status === params?.status).length, page: params?.page ?? 1, pageSize: params?.pageSize ?? 100 }) as never);
}
beforeEach(() => {
  jest.clearAllMocks();
  list.mockReset();
  jest.mocked(BPMNWorkflowApi.claimTask).mockReset().mockResolvedValue(undefined);
  jest.mocked(BPMNWorkflowApi.submitApprovalDecision).mockReset().mockResolvedValue(undefined);
  useAuthStore.setState({ isAuthenticated: true, user: { id: 1, tenantId: 2, permissions: [] } as never, currentTenant: { id: 2, status: 'active' } as never });
  rows();
});
it('uses all active BPMN statuses without inferring access from a manager role', async () => {
  render(<ApprovalsCenterPage />);
  expect(await screen.findByText('经理审批')).toBeInTheDocument();
  expect(list.mock.calls.map(([p]) => p?.status)).toEqual(expect.arrayContaining(['created','assigned','started','pending']));
});
it('finds approvals beyond 100 non-approval tasks without offering decisions for fulfillment', async () => {
  list.mockImplementation(async params => {
    if (params?.status !== 'created') return { items: [], total: 0 } as never;
    return { items: params.page === 1 ? Array.from({ length: 100 }, (_, id) => ({ ...task, id: id + 200, taskName: '履约任务', taskPurpose: 'fulfillment' })) : [task], total: 101 } as never;
  });
  render(<ApprovalsCenterPage />);
  expect(await screen.findByText('经理审批')).toBeInTheDocument();
  expect(list).toHaveBeenCalledWith({ status: 'created', page: 2, pageSize: 100 });
  expect(screen.queryByText('履约任务')).not.toBeInTheDocument();
});
it('identifies rows by WorkItem number', async () => {
  rows([{ ...task, businessType: 'service_request_item', businessId: 41, workItemNumber: 'TKT-21' } as typeof task]);
  render(<ApprovalsCenterPage />);
  expect(await screen.findByRole('link', { name: 'TKT-21' })).toHaveAttribute('href', '/service-requests/41');
});
it('shows a retryable initial error without a false empty state or count', async () => {
  list.mockRejectedValue(new Error('offline'));
  render(<ApprovalsCenterPage />);
  expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  expect(screen.queryByText('暂无审批待办')).not.toBeInTheDocument();
  rows(); fireEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByText('经理审批')).toBeInTheDocument();
});
it('keeps prior data on refresh failure but clears it on forbidden', async () => {
  render(<ApprovalsCenterPage />);
  await screen.findByText('经理审批');
  list.mockRejectedValue(new Error('offline'));
  fireEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('刷新失败');
  expect(screen.getByText('经理审批')).toBeInTheDocument();
  list.mockRejectedValue(new ApiError('denied', 403));
  fireEvent.click(screen.getByRole('button', { name: '刷新' }));
  await waitFor(() => expect(screen.queryByText('经理审批')).not.toBeInTheDocument());
});
it('claims a task and refreshes the authoritative list', async () => {
  render(<ApprovalsCenterPage />);
  fireEvent.click(await screen.findByRole('button', { name: '领取' }));
  await waitFor(() => expect(BPMNWorkflowApi.claimTask).toHaveBeenCalledWith(101, expect.any(Function)));
  await waitFor(() => expect(list.mock.calls.length).toBeGreaterThan(4));
});
it('requires a rejection reason and keeps the dialog on failure', async () => {
  jest.mocked(BPMNWorkflowApi.submitApprovalDecision).mockRejectedValue(new Error('decision denied'));
  render(<ApprovalsCenterPage />);
  fireEvent.click(await screen.findByRole('button', { name: '拒绝' }));
  fireEvent.click(screen.getByRole('button', { name: '确认拒绝' }));
  expect(BPMNWorkflowApi.submitApprovalDecision).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole('textbox', { name: '审批意见' }), { target: { value: '说明不足' } });
  fireEvent.click(screen.getByRole('button', { name: '确认拒绝' }));
  await waitFor(() => expect(BPMNWorkflowApi.submitApprovalDecision).toHaveBeenCalledWith(101, { action: 'reject', comment: '说明不足' }, expect.any(Function)));
  expect(await screen.findByText('decision denied')).toBeInTheDocument();
  expect(screen.getByRole('dialog')).toBeInTheDocument();
});
it('clears the decision and rejects late data after logout', async () => {
  render(<ApprovalsCenterPage />);
  fireEvent.click(await screen.findByRole('button', { name: '批准' }));
  let resolve!: (value: unknown) => void;
  list.mockImplementation(() => new Promise(r => { resolve = r; }) as never);
  fireEvent.click(screen.getByRole('button', { name: '刷新' }));
  act(() => useAuthStore.setState({ isAuthenticated: false }));
  await act(async () => { resolve({ items: [task], total: 1 }); });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.queryByText('经理审批')).not.toBeInTheDocument();
});

it.each([['service_request_item', '/service-requests/41'], ['catalog_task', '/service-requests/41'], ['generic', '/tickets/41'], ['change_request', '/changes/41'], ['incident', '/incidents/41'], ['problem', '/problems/41'], ['release', '/releases/41']])('links canonical %s identity', async (businessType, href) => {
 rows([{ ...task, businessType, businessId: 41, workItemNumber: 'TKT-canonical' } as typeof task]);
 render(<ApprovalsCenterPage />);
 expect(await screen.findByRole('link', { name: 'TKT-canonical' })).toHaveAttribute('href', href);
});
it.each(['service_request','ticket','change'])('rejects retired %s identity', async businessType => {
 rows([{ ...task, businessType, businessId: 41, workItemNumber: 'TKT-retired' } as typeof task]);
 render(<ApprovalsCenterPage />);
 expect(await screen.findByRole('link', { name: '流程实例 #12' })).toHaveAttribute('href', '/workflow/instances?instanceId=12');
 expect(screen.queryByRole('link', { name: 'TKT-retired' })).not.toBeInTheDocument();
});
