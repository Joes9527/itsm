import { render, screen, waitFor } from '@/lib/test-utils';
import ServiceRequestsPage from '../page';
import { serviceRequestAPI } from '@/lib/api/service-request-api';

// SR 自己不再维护审批阶段，审批只走关联 ticket 的 BPMN 流程。这个页面之前：
//   1) 用已经不存在的 status 字符串（submitted/manager_approved/.../delivered）分桶统计，
//      永远算出 0；
//   2) 渲染一个"待审批" Tab，数据源 getPendingApprovals 打在已经删除的路由上，
//      靠 .catch(() => 空) 掩盖，永远空着。
// 这里回归覆盖：统计改用批量回填的 ticketStatus 分桶，且"待审批" Tab 已经被去掉。
jest.mock('@/components/service-request/ServiceRequestList', () => ({
  __esModule: true,
  default: () => <div>service-request-list-stub</div>,
}));

jest.mock('@/lib/api/service-request-api', () => ({
  serviceRequestAPI: {
    getUserServiceRequests: jest.fn(),
  },
}));

const mockGetUserServiceRequests = serviceRequestAPI.getUserServiceRequests as jest.Mock;

function statisticValueByTitle(title: string): string {
  const titleEl = screen.getByText(title);
  const root = titleEl.closest('.ant-statistic') as HTMLElement;
  const valueEl = root.querySelector('.ant-statistic-content-value');
  return (valueEl?.textContent ?? '').trim();
}

describe('ServiceRequestsPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('buckets stats by the linked ticket status instead of the retired SR approval stages', async () => {
    mockGetUserServiceRequests.mockResolvedValue({
      requests: [
        { id: 1, ticketStatus: 'open' },
        { id: 2, ticketStatus: 'in_progress' },
        { id: 3, ticketStatus: 'in_progress' },
        { id: 4, ticketStatus: 'resolved' },
        { id: 5, ticketStatus: 'closed' },
        { id: 6, ticketStatus: 'cancelled' },
      ],
      total: 6,
    });

    render(<ServiceRequestsPage />);

    await waitFor(() => expect(mockGetUserServiceRequests).toHaveBeenCalled());
    await waitFor(() => expect(statisticValueByTitle('请求总数')).toBe('6'));

    expect(statisticValueByTitle('待处理')).toBe('1'); // open
    expect(statisticValueByTitle('处理中')).toBe('2'); // in_progress x2
    expect(statisticValueByTitle('已完成')).toBe('2'); // resolved + closed
    // cancelled 不计入任何桶 — 1(待处理) + 2(处理中) + 2(已完成) = 5，比总数 6 少 1。
  });

  it('no longer renders the retired "待审批" approvals tab', async () => {
    mockGetUserServiceRequests.mockResolvedValue({ requests: [], total: 0 });

    render(<ServiceRequestsPage />);

    await waitFor(() => expect(mockGetUserServiceRequests).toHaveBeenCalled());

    expect(screen.queryByText('待审批')).not.toBeInTheDocument();
    expect(screen.getByText('service-request-list-stub')).toBeInTheDocument();
  });
});
