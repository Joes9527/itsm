'use client';
import { TicketApi } from '@/lib/api/ticket-api';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';

export function useTicketDetailResource(ticketId: number, isWriting: () => boolean) {
  const resource = useDetailResource(
    ticketId,
    () => {
      if (!Number.isSafeInteger(ticketId) || ticketId <= 0) throw new Error('无效的工单ID');
      return TicketApi.getTicket(ticketId);
    },
    () => 0
  );
  // A read denial is a batch failure; only eligibility or unmount ends registration.
  useDetailRefreshEntry({ key: 'ticket', label: '工单详情', reload: resource.reload, isWriting });
  return { ...resource, initialLoading: resource.loading && !resource.data };
}
