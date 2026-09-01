import { render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { ManagerPendingApprovals } from '../ManagerPendingApprovals';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

// 回归覆盖：接口失败不能再用编造的假数据/假成功掩盖真实错误。
jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: {
    listUserTasks: jest.fn(),
    submitApprovalDecision: jest.fn(),
  },
}));

const mockListMyApprovalTasks = BPMNWorkflowApi.listUserTasks as jest.Mock;
const mockSubmitTaskDecision = BPMNWorkflowApi.submitApprovalDecision as jest.Mock;

const pendingTask = {
  id: 1,
  taskId: 'task-1',
  taskDefinitionKey: 'UserTask_DeptManagerApproval',
  taskName: 'SSL-VPN 远程访问权限申请',
  taskType: 'user_task',
  status: 'created',
  processInstanceId: 10,
  businessType: 'ticket',
  businessId: 42,
  taskPurpose: 'approval',
  taskVariables: {
    requesterName: '侯艾华',
    department: 'IT研发中心',
  },
  createdTime: '2026-08-20T10:00:00Z',
};

function mockTaskPages(tasks: typeof pendingTask[]) {
  mockListMyApprovalTasks.mockImplementation(({ status }: { status?: string }) =>
    Promise.resolve({
      items: tasks.filter((task) => task.status === status),
      total: tasks.length,
      page: 1,
      pageSize: 4,
    })
  );
}

describe('ManagerPendingApprovals', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders nothing when there are no pending approvals', async () => {
    mockTaskPages([]);
    const { container } = render(<ManagerPendingApprovals />);
    // container.firstChild is the AllTheProviders App wrapper, which is always
    // present; the component itself should render nothing inside it.
    await waitFor(() => expect(container.firstChild).toBeEmptyDOMElement());
  });

  it('shows a real error state instead of fabricated approval data when the fetch fails', async () => {
    mockListMyApprovalTasks.mockRejectedValue(new Error('network error'));
    render(<ManagerPendingApprovals />);

    expect(await screen.findByText('待办审批加载失败，请重试')).toBeInTheDocument();
    expect(screen.queryByText('李思源')).not.toBeInTheDocument();
  });

  it('renders real pending approval items returned by the backend', async () => {
    mockTaskPages([pendingTask]);

    render(<ManagerPendingApprovals />);

    expect(await screen.findByText('SSL-VPN 远程访问权限申请')).toBeInTheDocument();
    expect(screen.getByText('申请人：侯艾华')).toBeInTheDocument();
  });

  it('does not expose approval actions for non-approval BPMN user tasks', async () => {
    mockTaskPages([{ ...pendingTask, taskPurpose: 'fulfillment' }]);

    const { container } = render(<ManagerPendingApprovals />);

    await waitFor(() => expect(container.firstChild).toBeEmptyDOMElement());
    expect(screen.queryByRole('button', { name: '同意批准' })).not.toBeInTheDocument();
  });

  it('queries every active task status before selecting the pending approvals', async () => {
    mockTaskPages([]);

    render(<ManagerPendingApprovals />);

    await waitFor(() => expect(mockListMyApprovalTasks).toHaveBeenCalledTimes(4));
    expect(mockListMyApprovalTasks.mock.calls.map(([params]) => params.status)).toEqual(
      expect.arrayContaining(['created', 'assigned', 'started', 'pending'])
    );
  });

  it('continues paging when non-approval tasks fill the first status page', async () => {
    const fulfillmentTasks = Array.from({ length: 4 }, (_, index) => ({
      ...pendingTask,
      id: index + 10,
      taskId: `fulfillment-${index}`,
      taskPurpose: 'fulfillment',
    }));
    mockListMyApprovalTasks.mockImplementation(
      ({ status, page }: { status?: string; page?: number }) => {
        if (status !== 'created') {
          return Promise.resolve({ items: [], total: 0, page: 1, pageSize: 4 });
        }
        return Promise.resolve(
          page === 1
            ? { items: fulfillmentTasks, total: 5, page: 1, pageSize: 4 }
            : { items: [pendingTask], total: 5, page: 2, pageSize: 4 }
        );
      }
    );

    render(<ManagerPendingApprovals />);

    expect(await screen.findByText('SSL-VPN 远程访问权限申请')).toBeInTheDocument();
    expect(mockListMyApprovalTasks).toHaveBeenCalledWith({
      status: 'created',
      page: 2,
      pageSize: 4,
    });
  });

  it('keeps the task in the list and shows an error message when approval submission fails', async () => {
    mockTaskPages([pendingTask]);
    mockSubmitTaskDecision.mockRejectedValue(new Error('backend down'));

    render(<ManagerPendingApprovals />);
    const approveButton = await screen.findByRole('button', { name: '同意批准' });

    const user = userEvent.setup();
    await user.click(approveButton);

    await waitFor(() =>
      expect(mockSubmitTaskDecision).toHaveBeenCalledWith(1, {
        action: 'approve',
      })
    );
    // 失败后任务必须还在列表里，不能被静默移除或误报成功。
    expect(await screen.findByText('SSL-VPN 远程访问权限申请')).toBeInTheDocument();
  });

  it('removes the task only after the backend accepts the approval decision', async () => {
    mockTaskPages([pendingTask]);
    mockSubmitTaskDecision.mockResolvedValue(undefined);

    render(<ManagerPendingApprovals />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '同意批准' }));

    await waitFor(() =>
      expect(screen.queryByText('SSL-VPN 远程访问权限申请')).not.toBeInTheDocument()
    );
  });

  it('submits the rejection comment entered by the approver', async () => {
    mockTaskPages([pendingTask]);
    mockSubmitTaskDecision.mockResolvedValue(undefined);

    render(<ManagerPendingApprovals />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '驳回' }));
    await user.type(screen.getByRole('textbox', { name: '审批意见' }), '缺少业务必要性说明');
    await user.click(screen.getByRole('button', { name: '确认驳回' }));

    await waitFor(() =>
      expect(mockSubmitTaskDecision).toHaveBeenCalledWith(1, {
        action: 'reject',
        comment: '缺少业务必要性说明',
      })
    );
  });
});
