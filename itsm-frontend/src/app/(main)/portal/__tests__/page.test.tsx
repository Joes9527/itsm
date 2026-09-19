import { render, screen } from '@/lib/test-utils';
import { useAuthStore } from '@/lib/store/auth-store';
import userEvent from '@testing-library/user-event';
import PortalPage from '../page';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { ticketService } from '@/lib/services/ticket-service';

const mockPush = jest.fn();

jest.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush }),
}));

// 回归覆盖：门户首页的"常用服务目录"与"我的近期工单"必须来自真实接口，不能是硬编码假数据。
jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: {
    getServices: jest.fn(),
  },
}));

// "我的近期工单"与 /my-requests 同源：GET /api/v1/tickets（行级范围由后端收窄）。
// 之前这里查的是 /service-requests/me（只有 service_request_item、且只看申请人），
// 于是"查看全部我的工单"跳过去看到的是另一批数据。
jest.mock('@/lib/services/ticket-service', () => ({
  ticketService: { listTickets: jest.fn() },
}));

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: {
    listUserTasks: jest.fn(),
    submitApprovalDecision: jest.fn(),
  },
}));

const mockGetServices = ServiceCatalogApi.getServices as jest.Mock;
const mockListTickets = ticketService.listTickets as jest.Mock;
const mockListMyApprovalTasks = BPMNWorkflowApi.listUserTasks as jest.Mock;

const workItem = (over: Record<string, unknown> = {}) => ({
  id: 42,
  ticketNumber: 'TKT-202609-000042',
  title: '申请研发出差 SSL-VPN 访问权限',
  status: 'in_progress',
  priority: 'medium',
  requesterId: 1,
  recordClass: 'service_request_item',
  createdAt: '2026-08-24T10:00:00Z',
  updatedAt: '2026-08-24T12:00:00Z',
  ...over,
});

const listResult = (tickets: unknown[]) => ({
  tickets,
  total: tickets.length,
  page: 1,
  pageSize: 3,
  size: 3,
});

describe('PortalPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    useAuthStore.setState({
      isAuthenticated: true,
      user: { id: 1, tenantId: 2, permissions: [], name: '侯艾华', username: 'end_user_test' } as never,
      currentTenant: { id: 2, status: 'active' } as never,
    });
    mockListMyApprovalTasks.mockResolvedValue({ items: [], total: 0, page: 1, pageSize: 4 });
    mockListTickets.mockResolvedValue(listResult([]));
  });

  it('renders real published catalogs and links each card to its own apply route', async () => {
    mockGetServices.mockResolvedValue({
      services: [
        {
          id: '25',
          name: 'SSL-VPN 远程办公访问权限申请',
          category: 'security',
          shortDescription: '申请安全接入公司内部网络',
          status: 'published',
          tags: [],
          fields: [],
          createdBy: 0,
          createdByName: '',
          createdAt: new Date(),
          updatedAt: new Date(),
        },
      ],
      total: 1,
    });

    render(<PortalPage />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /SSL-VPN 远程办公访问权限申请/ }));

    expect(mockPush).toHaveBeenCalledWith('/service-catalog/request/25');
    expect(screen.queryByText('申请 Microsoft 365 Copilot 许可证')).not.toBeInTheDocument();
  });

  it('shows a real error state (not fabricated recent work items) when the tickets API fails', async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockListTickets.mockRejectedValue(new Error('network error'));

    render(<PortalPage />);

    expect(await screen.findByText('近期工单加载失败')).toBeInTheDocument();
    expect(screen.queryByText('REQ-2026-0801')).not.toBeInTheDocument();
  });

  it("renders recent work items of every class for the signed-in requester, linked to the WorkItem detail page", async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockListTickets.mockResolvedValue(
      listResult([
        workItem(),
        workItem({ id: 77, title: '核心交换机固件升级', status: 'open', recordClass: 'change_request' }),
      ])
    );

    render(<PortalPage />);

    expect(await screen.findByText('申请研发出差 SSL-VPN 访问权限')).toBeInTheDocument();
    expect(screen.getByText('核心交换机固件升级')).toBeInTheDocument();
    expect(screen.getByText('处理中')).toBeInTheDocument();

    // 与 /my-requests 同一范围：只问当前用户提交的工单，不请求无范围的列表
    expect(mockListTickets).toHaveBeenCalledWith(
      expect.objectContaining({ requesterId: 1, page: 1, pageSize: 3 })
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /申请研发出差 SSL-VPN 访问权限/ }));
    expect(mockPush).toHaveBeenCalledWith('/tickets/42');
  });

  it('retries loading recent work items after an API failure', async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockListTickets.mockRejectedValue(new Error('network error'));

    render(<PortalPage />);

    expect(await screen.findByText('近期工单加载失败')).toBeInTheDocument();
    const callsBeforeRetry = mockListTickets.mock.calls.length;
    mockListTickets.mockResolvedValue(listResult([]));

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /重\s*试/ }));

    expect(mockListTickets).toHaveBeenCalledTimes(callsBeforeRetry + 1);
    expect(await screen.findByText('暂无近期工单')).toBeInTheDocument();
  });

  it('does not request an unscoped recent list when there is no signed-in identity', async () => {
    useAuthStore.setState({ isAuthenticated: false, user: null as never });
    mockGetServices.mockResolvedValue({ services: [], total: 0 });

    render(<PortalPage />);

    expect(await screen.findByText('暂无近期工单')).toBeInTheDocument();
    expect(mockListTickets).not.toHaveBeenCalled();
  });
});
