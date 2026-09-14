import { act, renderHook, waitFor } from '@testing-library/react';
import { useApprovalTasks } from '../useApprovalTasks';
import { useAuthStore } from '@/lib/store/auth-store';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { ApiError } from '@/lib/api/http-client';
jest.mock('@/lib/api/bpmn-workflow-api', () => ({ BPMNWorkflowApi: { listUserTasks: jest.fn() } }));
const list = jest.mocked(BPMNWorkflowApi.listUserTasks);
const task = { id: 1, taskName: '审批', status: 'created', taskPurpose: 'approval', createdTime: '' };
beforeEach(() => {
  list.mockReset();
  useAuthStore.setState({ isAuthenticated: true, user: { id: 1, tenantId: 2, permissions: [] } as never, currentTenant: { id: 2, status: 'active' } as never });
});
it.each(['empty','repeated'])('rejects an incomplete %s final page', async mode => {
  const first = Array.from({ length: 100 }, (_, id) => ({ ...task, id: id + 1 }));
  list.mockImplementation(async p => p?.status !== 'created' ? { items: [], total: 0 } as never : { items: p?.page === 1 ? first : mode === 'empty' ? [] : first, total: 101 } as never);
  const { result } = renderHook(() => useApprovalTasks());
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.ready).toBe(false);
  expect(result.current.error).toBeTruthy();
});
it('clears prior rows when a delayed 403 follows a transient error from another status', async () => {
  list.mockImplementation(async p => ({ items: p?.status === 'created' ? [task] : [], total: p?.status === 'created' ? 1 : 0 }) as never);
  const { result } = renderHook(() => useApprovalTasks());
  await waitFor(() => expect(result.current.data).toHaveLength(1));
  let rejectDenied!: (error: unknown) => void;
  list.mockImplementation(p => {
    if (p?.status === 'created') return Promise.reject(new Error('offline'));
    if (p?.status === 'assigned') return new Promise((_, reject) => { rejectDenied = reject; });
    return Promise.resolve({ items: [], total: 0 }) as never;
  });
  act(() => { void result.current.reload(); });
  await act(async () => { rejectDenied(new ApiError('denied', 403)); });
  await waitFor(() => expect(result.current.denied).toBe(true));
  expect(result.current.data).toBeUndefined();
});

it('does not let an old refresh denial clear a newer successful result', async () => {
  let rejectOld!: (error: unknown) => void;
  list.mockImplementation(p => p?.status === 'created' ? new Promise((_, reject) => { rejectOld = reject; }) : Promise.resolve({ items: [], total: 0 }) as never);
  const { result } = renderHook(() => useApprovalTasks());
  list.mockImplementation(async p => ({ items: p?.status === 'created' ? [task] : [], total: p?.status === 'created' ? 1 : 0 }) as never);
  await act(async () => { await result.current.reload(); });
  await act(async () => { rejectOld(new ApiError('old denial', 403)); });
  expect(result.current.ready).toBe(true);
  expect(result.current.denied).toBe(false);
  expect(result.current.data).toHaveLength(1);
});
it('does not display unknown-purpose or completed tasks as approvals', async () => {
  list.mockImplementation(async p => ({ items: p?.status === 'created' ? [{ ...task, taskPurpose: '' }, { ...task, id: 2, status: 'completed' }] : [], total: p?.status === 'created' ? 2 : 0 }) as never);
  const { result } = renderHook(() => useApprovalTasks());
  await waitFor(() => expect(result.current.ready).toBe(true));
  expect(result.current.data).toEqual([]);
});
