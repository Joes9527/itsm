/**
 * ServiceRequestPanel Component Tests
 *
 * 覆盖：
 * - ticket 非 service_catalog 来源（by-ticket 成功查询无数据）时不渲染
 * - service_catalog 来源且已有交付任务时渲染任务表格，含关联CI的点击跳转
 * - service_catalog 来源但尚无交付任务时渲染"开始交付"按钮，点击调用 startProvisioning
 * - 没有关联CI时不渲染CI跳转按钮
 */

import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ServiceRequestPanel from '../ServiceRequestPanel';

// 覆盖 jest.setup.js 里全局的 next/navigation mock：全局 mock 每次调用 useRouter() 都会
// 返回一个全新的 jest.fn()，测试里拿不到引用去断言。这里用一个模块级的 mockPush 固定下来。
const mockPush = jest.fn();
jest.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush }),
}));

jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: {
    getServiceRequestByTicketId: jest.fn(),
  },
}));

jest.mock('@/lib/api/service-request-api', () => ({
  serviceRequestAPI: {
    listProvisioningTasks: jest.fn(),
    startProvisioning: jest.fn(),
  },
}));

import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';

const mockGetByTicket = ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock;
const mockListTasks = serviceRequestAPI.listProvisioningTasks as jest.Mock;
const mockStartProvisioning = serviceRequestAPI.startProvisioning as jest.Mock;

describe('ServiceRequestPanel', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders nothing after a successful lookup confirms no linked service request', async () => {
    mockGetByTicket.mockResolvedValueOnce(null);
    let view: ReturnType<typeof render>;
    await act(async () => { view = render(<ServiceRequestPanel ticketId={101} />); });
    expect(mockGetByTicket).toHaveBeenCalledWith(101);
    expect(view!.container).toBeEmptyDOMElement();
    expect(mockListTasks).not.toHaveBeenCalled();
  });

  it('shows a lookup failure and retries instead of claiming no linked request', async () => {
    mockGetByTicket.mockRejectedValueOnce(new Error('Service request lookup failed')).mockResolvedValueOnce(null);
    const { container } = render(<ServiceRequestPanel ticketId={101} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Service request lookup failed');
    await userEvent.setup().click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(container).toBeEmptyDOMElement());
    expect(mockGetByTicket).toHaveBeenCalledTimes(2);
    expect(mockListTasks).not.toHaveBeenCalled();
  });
  it('renders the provisioning task table when tasks already exist, plus a clickable linked-CI reference', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 55,
      costCenter: 'CC-100',
      dataClassification: 'internal',
      needsPublicIp: true,
      expireAt: '2026-12-31T00:00:00Z',
      ciId: 88,
    });
    mockListTasks.mockResolvedValueOnce([
      {
        id: 1,
        provider: 'aliyun',
        resourceType: 'ecs',
        status: 'succeeded',
        updatedAt: '2026-08-01T00:00:00Z',
      },
    ]);

    const user = userEvent.setup();
    render(<ServiceRequestPanel ticketId={202} />);

    await waitFor(() => {
      expect(screen.getByText('服务申请与规格参数')).toBeInTheDocument();
    });

    expect(mockListTasks).toHaveBeenCalledWith(55);
    expect(screen.getByText('CC-100')).toBeInTheDocument();
    expect(screen.getByText('(aliyun)')).toBeInTheDocument();
    expect(screen.getByText('ecs')).toBeInTheDocument();
    // 原型样式：开始交付按钮常驻面板头部
    expect(screen.getByText('开始交付')).toBeInTheDocument();

    // 关联CI：可点击跳转到 /cmdb/cis/:id（老页面的行为，折进 ServiceRequestPanel 后不能丢）
    const ciLink = screen.getByText('CI #88');
    expect(ciLink).toBeInTheDocument();
    await user.click(ciLink);
    expect(mockPush).toHaveBeenCalledWith('/cmdb/cis/88');
  });

  it('shows a dash instead of a CI link when the service request has no linked CI', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 66,
      costCenter: 'CC-300',
    });
    mockListTasks.mockResolvedValueOnce([]);

    render(<ServiceRequestPanel ticketId={404} />);

    await waitFor(() => {
      expect(screen.getByText('服务申请与规格参数')).toBeInTheDocument();
    });

    expect(screen.queryByText(/^CI #/)).not.toBeInTheDocument();
  });

  it('renders a start-provisioning button when no tasks exist yet, and calls startProvisioning on click', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 77,
      costCenter: 'CC-200',
      actions: { provision: { allowed: true } },
    });
    mockListTasks.mockResolvedValueOnce([]);
    mockStartProvisioning.mockResolvedValueOnce({ task: { id: 9 } });

    const user = userEvent.setup();
    render(<ServiceRequestPanel ticketId={303} />);

    const startButton = await screen.findByText('开始交付');
    expect(startButton).toBeInTheDocument();

    // 点击后重新加载（第二次 getByTicket/listProvisioningTasks 调用）
    mockGetByTicket.mockResolvedValueOnce({ id: 77, costCenter: 'CC-200' });
    mockListTasks.mockResolvedValueOnce([]);

    await user.click(startButton);

    await waitFor(() => {
      expect(mockStartProvisioning).toHaveBeenCalledWith(77);
    });
  });

  it('disables the start-provisioning button when actions.provision.allowed is false', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 78,
      costCenter: 'CC-201',
      actions: { provision: { allowed: false, reason: '申请人不能交付自己提交的服务请求' } },
    });
    mockListTasks.mockResolvedValueOnce([]);

    render(<ServiceRequestPanel ticketId={305} />);

    const startButton = await screen.findByText('开始交付');
    const buttonElement = startButton.closest('button');
    expect(buttonElement).toBeDisabled();
    expect(buttonElement).toHaveAttribute('title', '申请人不能交付自己提交的服务请求');
    expect(mockStartProvisioning).not.toHaveBeenCalled();
  });
  it.each([
    ['awaiting_approval', '待审批'], ['fulfilling', '履约中'], ['unknown', '结果未知'],
    ['completed', '已完成'], ['rejected', '已拒绝'], ['cancelled', '已取消'],
  ])('renders authoritative access state %s without manual delivery', async (state, label) => {
    mockGetByTicket.mockResolvedValueOnce({ id: 80, fulfillmentState: state,
      actions: { provision: { allowed: false, reason: 'managed_access_requires_verified_delegation' } } });
    mockListTasks.mockResolvedValueOnce([]);
    render(<ServiceRequestPanel ticketId={80} />);
    expect(await screen.findByText(label)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '开始交付' })).not.toBeInTheDocument();
    expect(screen.queryByText('尚未开始交付')).not.toBeInTheDocument();
  });
});
