/**
 * ServiceRequestPanel Component Tests
 *
 * 覆盖：
 * - ticket 非 service_catalog 来源（by-ticket 查询 404/无数据）时不渲染
 * - service_catalog 来源且已有交付任务时渲染任务表格，含关联CI的点击跳转
 * - service_catalog 来源但尚无交付任务时渲染"开始交付"按钮，点击调用 startProvisioning
 * - 没有关联CI时不渲染CI跳转按钮
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
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

  it('renders nothing when the ticket has no linked service request (404 / lookup failure)', async () => {
    mockGetByTicket.mockRejectedValueOnce(new Error('No service request linked to this ticket'));

    const { container } = render(<ServiceRequestPanel ticketId={101} />);

    await waitFor(() => {
      expect(mockGetByTicket).toHaveBeenCalledWith(101);
    });

    // 加载结束后应该静默不渲染，不出现任何卡片/错误提示
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText('服务申请信息')).not.toBeInTheDocument();
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
      expect(screen.getByText('服务申请信息')).toBeInTheDocument();
    });

    expect(mockListTasks).toHaveBeenCalledWith(55);
    expect(screen.getByText('CC-100')).toBeInTheDocument();
    expect(screen.getByText('aliyun')).toBeInTheDocument();
    expect(screen.getByText('ecs')).toBeInTheDocument();
    expect(screen.queryByText('开始交付')).not.toBeInTheDocument();

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
      expect(screen.getByText('服务申请信息')).toBeInTheDocument();
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
});
