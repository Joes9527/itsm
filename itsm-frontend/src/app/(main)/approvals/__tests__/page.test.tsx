import { render, screen, waitFor } from '@/lib/test-utils';
import ApprovalsCenterPage from '../page';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { httpClient } from '@/lib/api/http-client';

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: {
    listUserTasks: jest.fn(),
    claimTask: jest.fn(),
    submitApprovalDecision: jest.fn(),
  },
}));

jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn() },
}));

describe('ApprovalsCenterPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValue({
      items: [
        {
          id: 101,
          taskName: '经理审批',
          status: 'created',
          assignee: '',
          processInstanceId: 12,
          createdTime: '2026-09-01T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      pageSize: 100,
    });
  });

  it('uses BPMN ProcessTask as the only approval-center data source', async () => {
    render(<ApprovalsCenterPage />);

    expect(await screen.findByText('经理审批')).toBeInTheDocument();
    await waitFor(() => expect(BPMNWorkflowApi.listUserTasks).toHaveBeenCalledTimes(1));

    expect(httpClient.get).not.toHaveBeenCalled();
    expect(screen.queryByText('业务待审（参考）')).not.toBeInTheDocument();
  });
});
