import { act, renderHook, waitFor } from '@testing-library/react';
import { useAssignedTickets } from '../useAssignedTickets';
import { useAuthStore } from '@/lib/store/auth-store';
import { TicketApi } from '@/lib/api/ticket-api';
import { ApiError } from '@/lib/api/http-client';
import type { TicketListResponse, SessionUser, Tenant } from '@/lib/api/api-config';

jest.mock('@/lib/api/ticket-api', () => ({ TicketApi: { getTickets: jest.fn() } }));
const getTickets = jest.mocked(TicketApi.getTickets);
const response = (ids: number[], total = ids.length): TicketListResponse => ({
  tickets: ids.map(id => ({
    id,
    title: `Ticket ${id}`,
    ticketNumber: `TKT-${id}`,
  })) as TicketListResponse['tickets'],
  total,
  page: 1,
  size: 20,
  pageSize: 20,
});
function session(id = 7, tenant = 1, permissions = ['ticket:read']) {
  useAuthStore.setState({
    user: { id, tenantId: tenant, actorTenantId: tenant, permissions } as SessionUser,
    currentTenant: { id: tenant, status: 'active' } as Tenant,
    isAuthenticated: true,
  });
}
beforeEach(() => {
  jest.clearAllMocks();
  session();
  getTickets.mockResolvedValue(response([101, 102], 51));
});
afterEach(() => useAuthStore.setState({ user: null, currentTenant: null, isAuthenticated: false }));

it('queries the current assignee and keeps server total independent of page length', async () => {
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.selectedId).toBe(101));
  expect(getTickets).toHaveBeenCalledWith({ assigneeId: 7, page: 1, pageSize: 20 });
  expect(result.current.total).toBe(51);
  act(() => result.current.select(102));
  expect(result.current.selectedId).toBe(102);
  act(() => result.current.select(999));
  expect(result.current.selectedId).toBe(102);
});
it('never falls back to an unfiltered query without a valid session or read permission', async () => {
  useAuthStore.setState({ user: null });
  const { result, rerender } = renderHook(useAssignedTickets);
  expect(result.current.denied).toBe(true);
  act(() => session(7, 1, []));
  rerender();
  await act(async () => {});
  expect(getTickets).not.toHaveBeenCalled();
  expect(result.current.items).toEqual([]);
});
it('clears old page and fences a late page response', async () => {
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.selectedId).toBe(101));
  let finish!: (value: TicketListResponse) => void;
  getTickets.mockImplementationOnce(
    () =>
      new Promise(resolve => {
        finish = resolve;
      })
  );
  act(() => result.current.changePage(2));
  expect(result.current.items).toEqual([]);
  getTickets.mockResolvedValueOnce(response([301]));
  act(() => result.current.changePage(3));
  await waitFor(() => expect(result.current.selectedId).toBe(301));
  await act(async () => finish(response([201])));
  expect(result.current.selectedId).toBe(301);
  expect(getTickets).toHaveBeenLastCalledWith({ assigneeId: 7, page: 3, pageSize: 20 });
});
it('drops a moved record on refresh and keeps the next selection stable', async () => {
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.selectedId).toBe(101));
  getTickets.mockResolvedValueOnce(response([102]));
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.selectedId).toBe(102);
  getTickets.mockResolvedValueOnce(response([101, 102]));
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.selectedId).toBe(102);
});
it('preserves data on refresh failure but clears content and total on final denial', async () => {
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.selectedId).toBe(101));
  getTickets.mockRejectedValueOnce(new ApiError('offline', 500));
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.selectedId).toBe(101);
  expect(result.current.error).toContain('刷新失败');
  getTickets.mockRejectedValueOnce(new ApiError('forbidden', 403));
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.denied).toBe(true);
  expect(result.current.selectedId).toBeUndefined();
  expect(result.current.total).toBeUndefined();
});
it('resets page and content on tenant change and rejects mismatched tenant projection', async () => {
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.selectedId).toBe(101));
  act(() => result.current.changePage(2));
  await waitFor(() => expect(result.current.page).toBe(2));
  act(() => useAuthStore.setState({ currentTenant: { id: 2, status: 'active' } as Tenant }));
  expect(result.current.denied).toBe(true);
  expect(result.current.selectedId).toBeUndefined();
  getTickets.mockResolvedValueOnce(response([202]));
  act(() => session(8, 2));
  await waitFor(() => expect(result.current.selectedId).toBe(202));
  expect(getTickets).toHaveBeenLastCalledWith({ assigneeId: 8, page: 1, pageSize: 20 });
});
it('fences an in-flight response after permission revocation', async () => {
  let finish!: (value: TicketListResponse) => void;
  getTickets.mockImplementationOnce(
    () =>
      new Promise(resolve => {
        finish = resolve;
      })
  );
  const { result } = renderHook(useAssignedTickets);
  act(() => session(7, 1, []));
  await act(async () => finish(response([101])));
  expect(result.current.items).toEqual([]);
  expect(result.current.denied).toBe(true);
});
it('can retry an initial failure and distinguishes a successful empty queue', async () => {
  getTickets.mockRejectedValueOnce(new Error('unavailable'));
  const { result } = renderHook(useAssignedTickets);
  await waitFor(() => expect(result.current.error).toBe('unavailable'));
  expect(result.current.total).toBeUndefined();
  getTickets.mockResolvedValueOnce(response([]));
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.total).toBe(0);
  expect(result.current.error).toBeUndefined();
});
