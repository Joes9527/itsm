import { act, renderHook, waitFor } from '@testing-library/react';
import { useDetailResource } from '../useDetailResource';
import { ApiError } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const count = (rows: string[]) => rows.length;

it('merges concurrent same-identity reads and returns success receipts', async () => {
  const load = jest.fn().mockResolvedValue(['one']);
  const { result } = renderHook(() => useDetailResource(42, load, count));
  await waitFor(() => expect(result.current.ready).toBe(true));
  load.mockClear();
  await act(async () => {
    expect(await Promise.all([result.current.reload(), result.current.reload()])).toEqual([
      { status: 'success' },
      { status: 'success' },
    ]);
  });
  expect(load).toHaveBeenCalledTimes(1);
});

it('reports transient errors while retaining data, and denial clears data and count', async () => {
  const load = jest.fn().mockResolvedValue(['one']);
  const onCount = jest.fn();
  const { result } = renderHook(() => useDetailResource(42, load, count, onCount));
  await waitFor(() => expect(result.current.ready).toBe(true));
  load.mockRejectedValueOnce(new Error('offline'));
  await act(async () =>
    expect(await result.current.reload()).toMatchObject({
      status: 'error',
      denied: false,
      message: expect.stringContaining('offline'),
    })
  );
  expect(result.current.data).toEqual(['one']);
  const captured = result.current.capture();
  load.mockRejectedValueOnce(new ApiError('forbidden', 403));
  await act(async () =>
    expect(await result.current.reload()).toEqual({
      status: 'error',
      denied: true,
      message: 'forbidden',
    })
  );
  expect(result.current.data).toBeUndefined();
  expect(result.current.denied).toBe(true);
  expect(onCount).toHaveBeenLastCalledWith(undefined);
  expect(captured()).toBe(false);
});

it('discards a late old-tenant result without restoring protected data', async () => {
  const original = useAuthStore.getState().currentTenant;
  const pending = deferred<string[]>();
  const load = jest
    .fn()
    .mockResolvedValueOnce(['old'])
    .mockReturnValueOnce(pending.promise)
    .mockResolvedValue(['new']);
  const { result, unmount } = renderHook(() => useDetailResource(42, load, count));
  try {
    await waitFor(() => expect(result.current.ready).toBe(true));
    let old!: ReturnType<typeof result.current.reload>;
    act(() => {
      old = result.current.reload();
    });
    act(() =>
      useAuthStore.setState({
        currentTenant: {
          id: 98765,
          name: '测试',
          code: 'test',
          type: 'standard',
          status: 'active',
          createdAt: '',
          updatedAt: '',
        },
      })
    );
    await waitFor(() => expect(result.current.data).toEqual(['new']));
    await act(async () => {
      pending.resolve(['late']);
      expect(await old).toEqual({ status: 'discarded' });
    });
    expect(result.current.data).toEqual(['new']);
  } finally {
    unmount();
    act(() => useAuthStore.setState({ currentTenant: original }));
  }
});

it('starts a fresh after-write read and discards the pre-write response', async () => {
  const pending = deferred<string[]>();
  const load = jest
    .fn()
    .mockResolvedValueOnce(['initial'])
    .mockReturnValueOnce(pending.promise)
    .mockResolvedValue(['written']);
  const { result } = renderHook(() => useDetailResource(42, load, count));
  await waitFor(() => expect(result.current.ready).toBe(true));
  let old!: ReturnType<typeof result.current.reload>;
  act(() => {
    old = result.current.reload();
  });
  await act(async () =>
    expect(await result.current.reload({ afterWrite: true })).toEqual({ status: 'success' })
  );
  await act(async () => {
    pending.resolve(['stale']);
    expect(await old).toEqual({ status: 'discarded' });
  });
  expect(result.current.data).toEqual(['written']);
});

it('does not let a discarded pre-write request clear the newer in-flight read', async () => {
  const before = deferred<string[]>();
  const after = deferred<string[]>();
  const load = jest
    .fn()
    .mockResolvedValueOnce(['initial'])
    .mockReturnValueOnce(before.promise)
    .mockReturnValueOnce(after.promise);
  const { result } = renderHook(() => useDetailResource(42, load, count));
  await waitFor(() => expect(result.current.ready).toBe(true));
  let old!: ReturnType<typeof result.current.reload>;
  let fresh!: typeof old;
  act(() => {
    old = result.current.reload();
    fresh = result.current.reload({ afterWrite: true });
  });
  await act(async () => {
    before.resolve(['stale']);
    expect(await old).toEqual({ status: 'discarded' });
  });
  let joined!: typeof old;
  act(() => {
    joined = result.current.reload();
  });
  expect(load).toHaveBeenCalledTimes(3);
  await act(async () => {
    after.resolve(['written']);
    expect(await fresh).toEqual({ status: 'success' });
    expect(await joined).toEqual({ status: 'success' });
  });
  expect(result.current.data).toEqual(['written']);
});

it('invalidates pending reads when a command denies access', async () => {
  const pending = deferred<string[]>();
  const load = jest.fn().mockResolvedValueOnce(['protected']).mockReturnValueOnce(pending.promise);
  const { result } = renderHook(() => useDetailResource(42, load, count));
  await waitFor(() => expect(result.current.ready).toBe(true));
  let read!: ReturnType<typeof result.current.reload>;
  act(() => {
    read = result.current.reload();
    result.current.deny(new ApiError('revoked', 403));
  });
  await act(async () => {
    pending.resolve(['late']);
    expect(await read).toEqual({ status: 'discarded' });
  });
  expect(result.current.data).toBeUndefined();
  expect(result.current.denied).toBe(true);
});
