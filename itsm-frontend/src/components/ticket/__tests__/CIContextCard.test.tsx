/**
 * CIContextCard Component Tests
 *
 * 覆盖：
 * - 非 service_catalog 来源不渲染
 * - service_catalog 且有关联 CI 时展示 CI 名称/类型/拓扑信息
 * - service_catalog 且无关联 CI 时展示空态
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { CIContextCard } from '../CIContextCard';

jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: { getServiceRequestByTicketId: jest.fn() },
}));

jest.mock('@/lib/api/cmdb-api', () => ({
  CMDBApi: { getCI: jest.fn(), getCITopology: jest.fn() },
}));

import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { CMDBApi } from '@/lib/api/cmdb-api';

const mockGetByTicket = ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock;
const mockGetCI = CMDBApi.getCI as jest.Mock;
const mockGetTopology = CMDBApi.getCITopology as jest.Mock;

describe('CIContextCard', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders nothing for non-service_catalog tickets', async () => {
    const { container } = render(<CIContextCard ticketId={101} source="web" />);

    await waitFor(() => {
      expect(mockGetByTicket).not.toHaveBeenCalled();
    });
    expect(container).toBeEmptyDOMElement();
  });

  it('shows CI name, type chip and topology counts when the service request has a linked CI', async () => {
    mockGetByTicket.mockResolvedValueOnce({ id: 55, ciId: 88 });
    mockGetCI.mockResolvedValueOnce({
      id: '88',
      name: 'app-promotion-calc-cluster',
      type: 'application',
      description: '生产核心促销微服务集群',
    });
    mockGetTopology.mockResolvedValueOnce({ totalNodes: 14, totalEdges: 5 });

    render(<CIContextCard ticketId={202} source="service_catalog" />);

    await waitFor(() => {
      expect(screen.getByText('app-promotion-calc-cluster')).toBeInTheDocument();
    });
    expect(mockGetCI).toHaveBeenCalledWith(88);
    expect(mockGetTopology).toHaveBeenCalledWith(88, 3);
    expect(screen.getByText('应用集群')).toBeInTheDocument();
    expect(screen.getByText(/14 个节点/)).toBeInTheDocument();
  });

  it('shows empty state when the service request has no linked CI', async () => {
    mockGetByTicket.mockResolvedValueOnce({ id: 66 });

    render(<CIContextCard ticketId={303} source="service_catalog" />);

    await waitFor(() => {
      expect(screen.getByText('无关联 CI')).toBeInTheDocument();
    });
    expect(mockGetCI).not.toHaveBeenCalled();
  });
});
