import { act, render, screen } from '@/lib/test-utils';
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
// 只替换 listTickets：页面还要用 ticketService 的状态词表（getStatusLabel/getStatusColor），
// 整个模块被 mock 掉会让它在导入期拿到 undefined。
// 注意 ticketService 是 `new TicketService()` 的实例——方法在原型上，直接展开
// {...instance} 会把它们全部丢掉（表现为词表函数 undefined），所以要走原型。
jest.mock('@/lib/services/ticket-service', () => {
  const actual = jest.requireActual('@/lib/services/ticket-service');
  const instance = Object.create(Object.getPrototypeOf(actual.ticketService));
  Object.assign(instance, actual.ticketService, { listTickets: jest.fn() });
  return { ...actual, ticketService: instance };
});

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

    // 没有身份时不能断言"没有工单"——那是另一个状态（已登录但列表为空）的结论
    expect(await screen.findByText('请先登录后查看您的近期工单')).toBeInTheDocument();
    expect(screen.queryByText('暂无近期工单')).not.toBeInTheDocument();
    expect(mockListTickets).not.toHaveBeenCalled();
  });

  it('loads the recent list once a trusted identity arrives after mount', async () => {
    useAuthStore.setState({ isAuthenticated: false, user: null as never });
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockListTickets.mockResolvedValue(listResult([workItem({ title: '登录后才出现的工单' })]));

    render(<PortalPage />);
    await screen.findByText('请先登录后查看您的近期工单');

    // 会话投影后到：拿到可信身份后必须补发请求，而不是停在"没有工单"
    act(() => {
      useAuthStore.setState({
        isAuthenticated: true,
        user: {
          id: 1,
          tenantId: 2,
          permissions: [],
          name: '侯艾华',
          username: 'end_user_test',
        } as never,
      });
    });

    expect(await screen.findByText('登录后才出现的工单')).toBeInTheDocument();
    expect(mockListTickets).toHaveBeenCalledWith(
      expect.objectContaining({ requesterId: 1, page: 1, pageSize: 3 })
    );
  });
});
