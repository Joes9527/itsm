import { renderHook, waitFor } from '@testing-library/react';
import { useAssignmentCandidates } from '../useAssignmentCandidates';
import { UserApi } from '@/lib/api/user-api';
jest.mock('@/lib/api/user-api', () => ({ UserApi: { getUsers: jest.fn() } }));
beforeEach(() => jest.clearAllMocks());
test('continues beyond an inactive first page and includes an eligible user on page two', async () => {
  jest
    .mocked(UserApi.getUsers)
    .mockResolvedValueOnce({
      users: Array.from({ length: 100 }, (_, i) => ({
        id: i + 1,
        name: 'inactive',
        active: false,
      })),
      pagination: { page: 1, pageSize: 100, total: 101, totalPages: 2 },
    } as never)
    .mockResolvedValueOnce({
      users: [{ id: 101, name: 'Eligible later page', active: true }],
      pagination: { page: 2, pageSize: 100, total: 101, totalPages: 2 },
    } as never);
  const { result } = renderHook(() => useAssignmentCandidates(true));
  await waitFor(() =>
    expect(result.current.candidates).toEqual([{ id: 101, label: 'Eligible later page' }])
  );
  expect(UserApi.getUsers).toHaveBeenNthCalledWith(2, { page: 2, pageSize: 100 });
});
test('a later-page failure is visible and does not silently expose a partial directory', async () => {
  jest
    .mocked(UserApi.getUsers)
    .mockResolvedValueOnce({
      users: [{ id: 1, name: 'First', active: true }],
      pagination: { page: 1, pageSize: 100, total: 101, totalPages: 2 },
    } as never)
    .mockRejectedValueOnce(new Error('denied'));
  const { result } = renderHook(() => useAssignmentCandidates(true));
  await waitFor(() => expect(result.current.error).toMatch(/目录/));
  expect(result.current.candidates).toEqual([]);
});
