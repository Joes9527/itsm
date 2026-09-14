'use client';

import { useEffect, useState } from 'react';
import { useAuthStore } from '@/lib/store/auth-store';
import { TicketApi } from '@/lib/api/ticket-api';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';

export function useAssignedTickets() {
  const { user, currentTenant, isAuthenticated, hasPermission } = useAuthStore();
  const userId = user?.id;
  const enabled = Boolean(isAuthenticated && userId && Number.isSafeInteger(userId) && userId > 0 &&
    user?.tenantId && currentTenant?.id === user.tenantId && currentTenant.status === 'active' &&
    hasPermission('ticket:read'));
  const identity = JSON.stringify([userId, user?.tenantId, currentTenant?.id, currentTenant?.status,
    isAuthenticated, [...(user?.permissions ?? [])].sort()]);
  const [navigation, setNavigation] = useState<{identity: string; page: number; selectedId?: number}>({identity, page: 1});
  const page = navigation.identity === identity ? navigation.page : 1;
  const resource = useDetailResource(
    `${identity}:${page}`,
    () => TicketApi.getTickets({assigneeId: userId, page, pageSize: 20}),
    data => data.total,
    undefined,
    enabled
  );
  const items = resource.data?.tickets;
  const selectedId = items?.some(item => item.id === navigation.selectedId) && navigation.identity === identity
    ? navigation.selectedId : items?.[0]?.id;
  // Persist the effective selection so a moved record does not steal focus when it later returns.
  useEffect(() => {
    setNavigation(previous => previous.identity === identity && previous.page === page && previous.selectedId === selectedId
      ? previous : {identity, page, selectedId});
  }, [identity, page, selectedId]);

  return {
    identity, page, items: items ?? [], total: resource.data?.total, selectedId,
    loading: resource.loading, error: resource.error, denied: resource.denied, reload: resource.reload,
    select: (id: number) => {
      if (items?.some(item => item.id === id)) setNavigation({identity, page, selectedId: id});
    },
    changePage: (next: number) => {
      if (Number.isSafeInteger(next) && next > 0) setNavigation({identity, page: next});
    },
  };
}
