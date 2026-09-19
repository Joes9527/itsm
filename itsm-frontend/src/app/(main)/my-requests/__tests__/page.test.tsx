import { render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import MyRequestsPage from '../page';
import { ticketService } from '@/lib/services/ticket-service';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';

// /my-requests 是"我的工单"：数据来自 GET /api/v1/tickets（行级范围由后端
// authorization.WorkItemReadScope 收窄），覆盖 incident/problem/change_request/
// service_request_item/generic 全部 recordClass——不再只查 service_request_item。
// 本文件回归覆盖：数据来源与跳转、范围过滤、状态/关键字由服务端过滤、
// 未登录时不发起无范围请求、错误态与权限拒绝态。
// 只替换单例：TicketStatus 是模块的值导出（状态下拉的值域），mock 掉整个模块
// 会让页面在导入期拿到 undefined。
jest.mock('@/lib/services/ticket-service', () => ({
  ...jest.requireActual('@/lib/services/ticket-service'),
  ticketService: { listTickets: jest.fn() },
}));

const mockListTickets = ticketService.listTickets as jest.Mock;

const workItem = (over: Record<string, unknown> = {}) => ({
  id: 101,
  ticketNumber: 'TKT-202609-000101',
  title: '样例工单',
  description: '',
  priority: 'medium',
  status: 'new',
  requesterId: 7,
  createdAt: '2026-09-01T02:00:00Z',
  updatedAt: '2026-09-02T02:00:00Z',
  ...over,
});

const listResult = (tickets: unknown[]) => ({
  tickets,
  total: tickets.length,
  page: 1,
  pageSize: 10,
  size: 10,
});

describe('MyRequestsPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    useAuthStore.setState({
      isAuthenticated: true,
      user: {
        id: 7,
        tenantId: 2,
        permissions: ['ticket:read'],
        name: '测试用户',
        username: 'tester',
      } as never,
      currentTenant: { id: 2, status: 'active' } as never,
    });
    mockListTickets.mockResolvedValue(listResult([workItem()]));
  });

  it('renders every record class with its own label and links to the unified WorkItem detail page', async () => {
    mockListTickets.mockResolvedValue(
      listResult([
        workItem({ id: 101, title: '生产数据库连接超时', recordClass: 'incident' }),
        workItem({ id: 102, title: '核心交换机固件升级', recordClass: 'change_request' }),
        workItem({ id: 103, title: '知识库检索命中率下降', recordClass: 'problem' }),
        workItem({ id: 104, title: '申请 SSLVPN 访问权限', recordClass: 'service_request_item' }),
        workItem({ id: 105, title: '申请 Copilot 许可证', recordClass: 'generic' }),
      ])
    );

    render(<MyRequestsPage />);

    const cases: Array<[number, string, string]> = [
      [101, '生产数据库连接超时', '事件'],
      [102, '核心交换机固件升级', '变更'],
      [103, '知识库检索命中率下降', '问题'],
      [104, '申请 SSLVPN 访问权限', '服务请求'],
      [105, '申请 Copilot 许可证', '工单'],
    ];

    for (const [id, title, classLabel] of cases) {
      const heading = await screen.findByText(title);
      const card = heading.closest('.ant-card') as HTMLElement;
      expect(within(card).getByText(classLabel)).toBeInTheDocument();
      expect(within(card).getByRole('link', { name: /查看详情/ })).toHaveAttribute(
        'href',
        `/tickets/${id}`
      );
    }
  });

  it("asks the backend for the signed-in user's own submissions by default", async () => {
    render(<MyRequestsPage />);

    await waitFor(() => expect(mockListTickets).toHaveBeenCalled());

    expect(mockListTickets).toHaveBeenCalledWith(
      expect.objectContaining({ requesterId: 7, page: 1, pageSize: 10 })
    );
    expect(mockListTickets.mock.calls[0][0]).not.toHaveProperty('assigneeId');
  });

  it('refetches with the assignee filter when switching to 我处理的', async () => {
    const user = userEvent.setup();
    render(<MyRequestsPage />);
    await waitFor(() => expect(mockListTickets).toHaveBeenCalled());

    await user.click(screen.getByRole('button', { name: '我处理的' }));

    await waitFor(() => {
      const lastCall = mockListTickets.mock.calls.at(-1)?.[0];
      expect(lastCall).toEqual(expect.objectContaining({ assigneeId: 7 }));
      expect(lastCall).not.toHaveProperty('requesterId');
    });
  });

  it('drops the actor filter when switching to 全部', async () => {
    const user = userEvent.setup();
    render(<MyRequestsPage />);
    await waitFor(() => expect(mockListTickets).toHaveBeenCalled());

    // antd 会在两个字的中文按钮文案中间插入空格（渲染为"全 部"）
    await user.click(screen.getByRole('button', { name: /全\s*部/ }));

    await waitFor(() => {
      const lastCall = mockListTickets.mock.calls.at(-1)?.[0];
      expect(lastCall).not.toHaveProperty('requesterId');
      expect(lastCall).not.toHaveProperty('assigneeId');
    });
  });

  it('sends the chosen status to the backend instead of filtering only the loaded page', async () => {
    const user = userEvent.setup();
    render(<MyRequestsPage />);
    await waitFor(() => expect(mockListTickets).toHaveBeenCalled());

    await user.click(screen.getByRole('combobox'));
    await user.click(await screen.findByTitle('已解决'));

    await waitFor(() =>
      expect(mockListTickets).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: 'resolved', page: 1 })
      )
    );
  });

  it('searches on the backend so results are not limited to the loaded page', async () => {
    const user = userEvent.setup();
    render(<MyRequestsPage />);
    await waitFor(() => expect(mockListTickets).toHaveBeenCalled());

    await user.type(screen.getByPlaceholderText(/搜索/), 'SSLVPN');

    await waitFor(
      () =>
        expect(mockListTickets).toHaveBeenLastCalledWith(
          expect.objectContaining({ keyword: 'SSLVPN', page: 1 })
        ),
      { timeout: 3000 }
    );
  });

  it('does not request an unscoped list when there is no signed-in identity', async () => {
    useAuthStore.setState({ isAuthenticated: false, user: null as never });

    render(<MyRequestsPage />);

    expect(await screen.findByText(/请先登录/)).toBeInTheDocument();
    expect(mockListTickets).not.toHaveBeenCalled();
  });

  it('shows the error state and refetches on retry', async () => {
    mockListTickets.mockRejectedValueOnce(new Error('network down'));
    const user = userEvent.setup();
    render(<MyRequestsPage />);

    expect(await screen.findByText('加载工单失败，请重试')).toBeInTheDocument();
    // 请求失败时必须只呈现错误态：此时"暂无工单"是另一个（成功但为空）状态的不实断言
    expect(screen.queryByText('暂无工单')).not.toBeInTheDocument();

    const callsBeforeRetry = mockListTickets.mock.calls.length;
    mockListTickets.mockResolvedValue(listResult([workItem({ title: '重试后出现的工单' })]));
    await user.click(screen.getByRole('button', { name: /重\s*试/ }));

    expect(await screen.findByText('重试后出现的工单')).toBeInTheDocument();
    expect(mockListTickets).toHaveBeenCalledTimes(callsBeforeRetry + 1);
  });

  it('shows a permission-denied state when the backend rejects with 403', async () => {
    mockListTickets.mockRejectedValue(new ApiError('权限不足', 403));

    render(<MyRequestsPage />);

    expect(await screen.findByText('没有查看工单的权限')).toBeInTheDocument();
    expect(screen.queryByText('加载工单失败，请重试')).not.toBeInTheDocument();
  });

  it('shows the empty state when the actor has no work items', async () => {
    mockListTickets.mockResolvedValue(listResult([]));

    render(<MyRequestsPage />);

    expect(await screen.findByText('暂无工单')).toBeInTheDocument();
  });
});
