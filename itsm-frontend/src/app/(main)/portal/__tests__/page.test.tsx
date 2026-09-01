import { render, screen } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import PortalPage from '../page';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

const mockPush = jest.fn();

jest.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush }),
}));

// 回归覆盖：门户首页的"常用服务目录"与"我的近期请求"必须来自真实接口，不能是硬编码假数据。
jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: {
    getServices: jest.fn(),
    getServiceRequests: jest.fn(),
  },
}));

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: {
    listUserTasks: jest.fn(),
    submitApprovalDecision: jest.fn(),
  },
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: { name: '侯艾华', username: 'end_user_test' } }),
}));

const mockGetServices = ServiceCatalogApi.getServices as jest.Mock;
const mockGetServiceRequests = ServiceCatalogApi.getServiceRequests as jest.Mock;
const mockListMyApprovalTasks = BPMNWorkflowApi.listUserTasks as jest.Mock;

describe('PortalPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListMyApprovalTasks.mockResolvedValue({ items: [], total: 0, page: 1, pageSize: 4 });
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
    mockGetServiceRequests.mockResolvedValue({ requests: [], total: 0 });

    render(<PortalPage />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /SSL-VPN 远程办公访问权限申请/ }));

    expect(mockPush).toHaveBeenCalledWith('/service-catalog/request/25');
    expect(screen.queryByText('申请 Microsoft 365 Copilot 许可证')).not.toBeInTheDocument();
  });

  it('shows a real error state (not fabricated recent requests) when the requests API fails', async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockGetServiceRequests.mockRejectedValue(new Error('network error'));

    render(<PortalPage />);

    expect(await screen.findByText('近期请求加载失败')).toBeInTheDocument();
    expect(screen.queryByText('REQ-2026-0801')).not.toBeInTheDocument();
  });

  it('renders real recent service requests linked to their ticket detail page', async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockGetServiceRequests.mockResolvedValue({
      requests: [
        {
          id: 1,
          ticketId: 42,
          ticketTitle: '申请研发出差 SSL-VPN 访问权限',
          ticketStatus: 'in_progress',
          createdAt: '2026-08-24T10:00:00Z',
          updatedAt: '2026-08-24T12:00:00Z',
        },
      ],
      total: 1,
    });

    render(<PortalPage />);

    expect(await screen.findByText('申请研发出差 SSL-VPN 访问权限')).toBeInTheDocument();
    expect(screen.getByText('处理中')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /申请研发出差 SSL-VPN 访问权限/ }));
    expect(mockPush).toHaveBeenCalledWith('/tickets/42');
  });

  it('retries loading recent requests after an API failure', async () => {
    mockGetServices.mockResolvedValue({ services: [], total: 0 });
    mockGetServiceRequests.mockRejectedValue(new Error('network error'));

    render(<PortalPage />);

    expect(await screen.findByText('近期请求加载失败')).toBeInTheDocument();
    const callsBeforeRetry = mockGetServiceRequests.mock.calls.length;
    mockGetServiceRequests.mockResolvedValue({ requests: [], total: 0 });

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /重\s*试/ }));

    expect(mockGetServiceRequests).toHaveBeenCalledTimes(callsBeforeRetry + 1);
    expect(await screen.findByText('暂无近期请求')).toBeInTheDocument();
  });
});
