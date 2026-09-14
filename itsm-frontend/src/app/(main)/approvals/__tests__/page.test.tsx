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
  it('identifies approval rows by the owning WorkItem number', async () => {
    (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValueOnce({
      items: [{ id: 102, taskName: '审批', status: 'created', assignee: '',
        processInstanceId: 13, businessType: 'service_request_item', businessId: 41,
        workItemNumber: 'TKT-202609-000021', createdTime: '2026-09-01T00:00:00Z' }],
    });
    render(<ApprovalsCenterPage />);
    expect(await screen.findByRole('link', { name: 'TKT-202609-000021' }))
      .toHaveAttribute('href', '/service-requests/41');
  });

  // C1: 流程身份是 recordClass（设计 §15.2.2），审批链接必须按规范词表解析，
  // 每个专业类都要落到自己的详情路由。
  it.each([
    ['service_request_item', 41, '/service-requests/41'],
    ['catalog_task', 11, '/service-requests/11'],
    ['generic', 7, '/tickets/7'],
    ['change_request', 9, '/changes/9'],
    ['incident', 3, '/incidents/3'],
    ['problem', 5, '/problems/5'],
    ['release', 2, '/releases/2'],
  ])('links a %s approval row to its owning record route', async (businessType, businessId, href) => {
    (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValueOnce({
      items: [{ id: 103, taskName: '审批', status: 'created', assignee: '',
        processInstanceId: 14, businessType, businessId,
        workItemNumber: 'TKT-202609-000022', createdTime: '2026-09-01T00:00:00Z' }],
    });
    render(<ApprovalsCenterPage />);
    expect(await screen.findByRole('link', { name: 'TKT-202609-000022' }))
      .toHaveAttribute('href', href);
  });

  // 退役的 Wave-1 词表不得被解释成业务链接：宁可退化到流程实例链接，
  // 也不能把旧身份当成新身份指向一个专业详情页。
  it.each([['service_request'], ['ticket'], ['change']])(
    'refuses to build a business link for the retired identity %s',
    async (retiredType) => {
      (BPMNWorkflowApi.listUserTasks as jest.Mock).mockResolvedValueOnce({
        items: [{ id: 104, taskName: '审批', status: 'created', assignee: '',
          processInstanceId: 15, businessType: retiredType, businessId: 41,
          workItemNumber: 'TKT-202609-000023', createdTime: '2026-09-01T00:00:00Z' }],
      });
      render(<ApprovalsCenterPage />);
      expect(await screen.findByRole('link', { name: '流程实例 #15' }))
        .toHaveAttribute('href', '/workflow/instances?instanceId=15');
      expect(screen.queryByRole('link', { name: 'TKT-202609-000023' })).not.toBeInTheDocument();
    },
  );

});
