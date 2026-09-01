import { render, screen, waitFor } from '@/lib/test-utils';
import PendingApprovalsPage from '../page';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

// This page used to have a "服务请求审批" tab that called
// serviceRequestAPI.getPendingApprovals/applyApprovalAction directly against
// ServiceRequest — routes Task 1 deleted (SR-specific approval retired; approval now flows
// through the linked ticket's own BPMN process, i.e. the "我作为候选组员（BPMN）" tab below,
// which is unchanged and already correct). That tab was removed — this locks in that the page
// renders the BPMN task list directly with no dead tab left over.
jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: {
    listUserTasks: jest.fn(),
    claimTask: jest.fn(),
    submitApprovalDecision: jest.fn(),
  },
}));

const mockListMyTasks = BPMNWorkflowApi.listUserTasks as jest.Mock;

describe('PendingApprovalsPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders only the BPMN task list, with no retired "服务请求审批" tab', async () => {
    mockListMyTasks.mockResolvedValue({
      items: [
        {
          id: 1,
          taskName: '经理审批',
          taskPurpose: 'approval',
          processInstanceId: 1,
          status: 'pending',
          assignee: '',
          createdTime: '2026-08-01T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      pageSize: 10,
    });

    render(<PendingApprovalsPage />);

    await waitFor(() => expect(mockListMyTasks).toHaveBeenCalled());
    expect(await screen.findByText('经理审批')).toBeInTheDocument();

    expect(screen.queryByText('服务请求审批')).not.toBeInTheDocument();
    expect(
      screen.getByText('当前指派给我、或我可以领取的 BPMN 审批任务（包含服务目录申请单关联的工单）。')
    ).toBeInTheDocument();
  });
});
