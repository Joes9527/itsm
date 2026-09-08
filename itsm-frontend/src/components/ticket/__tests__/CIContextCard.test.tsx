/**
 * CIContextCard Component Tests
 *
 * 覆盖：
 * - 非 Requested Item 不渲染
 * - Requested Item 且有关联 CI 时展示 CI 名称/类型/拓扑信息
 * - Requested Item 且无关联 CI 时展示空态
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
    mockGetByTicket.mockReset();
    mockGetCI.mockReset();
    mockGetTopology.mockReset();
  });

  it.each(['generic', 'incident', 'problem', 'change_request', 'catalog_task'])('renders nothing for %s even if source claims service_catalog', async recordClass => {
    const { container } = render(<CIContextCard ticketId={101} {...{recordClass, source: "service_catalog"}} />);

    await waitFor(() => {
      expect(mockGetByTicket).not.toHaveBeenCalled();
    });
    expect(container).toBeEmptyDOMElement();
  });

  it.each(['service_catalog', 'kaf_web'])('shows linked CI for a Requested Item from %s', async source => {
    mockGetByTicket.mockResolvedValueOnce({ id: 55, ciId: 88 });
    mockGetCI.mockResolvedValueOnce({
      id: '88',
      name: 'app-promotion-calc-cluster',
      type: 'application',
      description: '生产核心促销微服务集群',
    });
    mockGetTopology.mockResolvedValueOnce({ totalNodes: 14, totalEdges: 5 });

    render(<CIContextCard ticketId={202} {...{recordClass: "service_request_item", source}} />);

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

    render(<CIContextCard ticketId={303} {...{recordClass: "service_request_item", source: "service_catalog"}} />);

    await waitFor(() => {
      expect(screen.getByText('无关联 CI')).toBeInTheDocument();
    });
    expect(mockGetCI).not.toHaveBeenCalled();
  });
});
