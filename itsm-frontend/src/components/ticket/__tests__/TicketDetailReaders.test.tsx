import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  DetailRefreshProvider,
  useDetailRefresh,
} from '@/components/business/detail-tabs/DetailRefreshContext';
import { TicketHistoryList } from '../TicketHistoryList';
import ServiceCatalogApprovalChain from '../ServiceCatalogApprovalChain';
import ServiceRequestPanel from '../ServiceRequestPanel';
import { TicketApi } from '@/lib/api/ticket-api';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';
import { ApiError } from '@/lib/api/http-client';
jest.mock('next/navigation', () => ({ useRouter: () => ({ push: jest.fn() }) }));
jest.mock('@/lib/api/ticket-api');
jest.mock('@/lib/api/service-catalog-api');
jest.mock('@/lib/api/service-request-api');
function Refresh() {
  const controller = useDetailRefresh()!;
  return <button onClick={() => void controller.refresh()}>页面刷新</button>;
}
beforeEach(() => jest.resetAllMocks());
it('keeps history after temporary refresh error, clears after denial, and derives counts from its list', async () => {
  const count = jest.fn();
  (TicketApi.getTicketHistory as jest.Mock).mockResolvedValue([{ id: 1, action: '已经派单' }]);
  render(
    <DetailRefreshProvider identity='101'>
      <Refresh />
      <TicketHistoryList ticketId={101} onCountChange={count} />
    </DetailRefreshProvider>
  );
  await screen.findByText(/已经派单/);
  expect(count).toHaveBeenLastCalledWith(1);
  (TicketApi.getTicketHistory as jest.Mock).mockRejectedValueOnce(new Error('历史离线'));
  await userEvent.click(screen.getByText('页面刷新'));
  expect(await screen.findByRole('alert')).toHaveTextContent('历史离线');
  expect(screen.getByText(/已经派单/)).toBeInTheDocument();
  (TicketApi.getTicketHistory as jest.Mock).mockRejectedValueOnce(new ApiError('历史禁止', 403));
  await userEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(screen.queryByText(/已经派单/)).not.toBeInTheDocument());
  expect(count).toHaveBeenLastCalledWith(undefined);
});
it('catalog chain accepts a refreshed empty result and reports failures', async () => {
  (ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock).mockResolvedValue({
    formData: { ApprovalChain: [{ level: 1, name: '旧链' }] },
  });
  render(
    <DetailRefreshProvider identity='101'>
      <Refresh />
      <ServiceCatalogApprovalChain ticketId={101} />
    </DetailRefreshProvider>
  );
  await screen.findByText(/旧链/);
  (ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock).mockRejectedValueOnce(
    new Error('链离线')
  );
  await userEvent.click(screen.getByText('页面刷新'));
  expect(await screen.findByRole('alert')).toHaveTextContent('链离线');
  expect(screen.getByText(/旧链/)).toBeInTheDocument();
  (ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock).mockResolvedValue({ formData: {} });
  await userEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(screen.queryByText(/旧链/)).not.toBeInTheDocument());
});
it('service request treats failed lookup as recoverable error and authoritative absence as empty', async () => {
  (ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock).mockRejectedValueOnce(
    new Error('申请离线')
  );
  render(<ServiceRequestPanel ticketId={101} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('申请离线');
  (ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock).mockResolvedValue(null);
  await userEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(serviceRequestAPI.listProvisioningTasks).not.toHaveBeenCalled();
});
