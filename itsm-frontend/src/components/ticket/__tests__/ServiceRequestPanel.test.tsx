/**
 * ServiceRequestPanel Component Tests
 *
 * 覆盖：
 * - ticket 非 service_catalog 来源（by-ticket 查询 404/无数据）时不渲染
 * - service_catalog 来源且已有交付任务时渲染任务表格
 * - service_catalog 来源但尚无交付任务时渲染"开始交付"按钮，点击调用 startProvisioning
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ServiceRequestPanel from '../ServiceRequestPanel';

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

  it('renders the provisioning task table when tasks already exist', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 55,
      costCenter: 'CC-100',
      dataClassification: 'internal',
      needsPublicIp: true,
      expireAt: '2026-12-31T00:00:00Z',
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

    render(<ServiceRequestPanel ticketId={202} />);

    await waitFor(() => {
      expect(screen.getByText('服务申请信息')).toBeInTheDocument();
    });

    expect(mockListTasks).toHaveBeenCalledWith(55);
    expect(screen.getByText('CC-100')).toBeInTheDocument();
    expect(screen.getByText('aliyun')).toBeInTheDocument();
    expect(screen.getByText('ecs')).toBeInTheDocument();
    expect(screen.queryByText('开始交付')).not.toBeInTheDocument();
  });

  it('renders a start-provisioning button when no tasks exist yet, and calls startProvisioning on click', async () => {
    mockGetByTicket.mockResolvedValueOnce({
      id: 77,
      costCenter: 'CC-200',
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
});
