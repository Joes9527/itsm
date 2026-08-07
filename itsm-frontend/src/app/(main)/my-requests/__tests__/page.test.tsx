import { render, screen, waitFor, within } from '@/lib/test-utils';
import MyRequestsPage from '../page';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';

// 状态/标题已经委托给关联的 Ticket（Task 1 从 ServiceRequest 表移除了 status/title/reason），
// /my-requests 列表页改成展示后端批量回填的 ticketTitle/ticketStatus，并跳转到
// /tickets/:ticketId（不再有独立的 SR 详情页）——这里回归覆盖这两点。
jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: {
    getServiceRequests: jest.fn(),
  },
}));

const mockGetServiceRequests = ServiceCatalogApi.getServiceRequests as jest.Mock;

describe('MyRequestsPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders the linked ticket title/status and links to the ticket detail page', async () => {
    mockGetServiceRequests.mockResolvedValue({
      requests: [
        {
          id: 1,
          ticketId: 42,
          catalogId: 7,
          requesterId: 3,
          ticketTitle: '申请一台云主机',
          ticketStatus: 'in_progress',
          createdAt: '2026-08-01T00:00:00Z',
          catalog: { id: 7, name: '云主机', category: '云服务', description: 'desc' },
        },
      ],
      total: 1,
    });

    render(<MyRequestsPage />);

    await waitFor(() => expect(mockGetServiceRequests).toHaveBeenCalled());

    const title = await screen.findByText('申请一台云主机');
    // 状态标签文案（"处理中"）在筛选栏和请求卡片上都会出现，用卡片容器把断言限定在
    // 卡片内部，避免和筛选栏的同名按钮撞车。
    const card = title.closest('.ant-card') as HTMLElement;
    expect(within(card).getByText('处理中')).toBeInTheDocument();

    const link = within(card).getByRole('link', { name: /查看详情/ });
    expect(link).toHaveAttribute('href', '/tickets/42');
  });

  it('falls back to the catalog name and a placeholder status when the linked ticket has none yet', async () => {
    mockGetServiceRequests.mockResolvedValue({
      requests: [
        {
          id: 2,
          ticketId: 43,
          catalogId: 8,
          requesterId: 3,
          createdAt: '2026-08-02T00:00:00Z',
          catalog: { id: 8, name: 'VPN 权限', category: '网络', description: 'desc' },
        },
      ],
      total: 1,
    });

    render(<MyRequestsPage />);

    const title = await screen.findByText('VPN 权限');
    const card = title.closest('.ant-card') as HTMLElement;
    expect(within(card).getByText('-')).toBeInTheDocument();

    const link = within(card).getByRole('link', { name: /查看详情/ });
    expect(link).toHaveAttribute('href', '/tickets/43');
  });
});
