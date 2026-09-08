import { act, renderHook } from '@testing-library/react';
import { useWorkItemCreation } from '../useWorkItemCreation';
import { useAuthStore } from '@/lib/store/auth-store';
import { ApiError } from '@/lib/api/http-client';
const receipt = {
  workItemId: 41,
  number: 'WI-41',
  recordClass: 'generic' as const,
  professionalReference: { type: '', id: 0 },
  workflowStartStatus: 'pending' as const,
  replayed: false,
};
jest.mock('antd', () => ({
  message: { success: jest.fn(), error: jest.fn(), warning: jest.fn() },
}));
const session = () =>
  useAuthStore.setState({
    isAuthenticated: true,
    user: { id: 1, actorTenantId: 2, tenantId: 2 } as never,
    currentTenant: { id: 2 } as never,
  });
beforeEach(session);
let key = 0;
Object.defineProperty(crypto, 'randomUUID', {
  configurable: true,
  value: () => `attempt-${++key}`,
});
it('suppresses same-tick double submit and freezes body/key after response loss despite edits', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockRejectedValueOnce(new Error('lost')).mockResolvedValue(receipt);
  const body = { title: 'confirmed', fields: { office_location: 'A' } };
  await act(async () => {
    await Promise.all([result.current.submit(body, send), result.current.submit(body, send)]);
  });
  expect(send).toHaveBeenCalledTimes(1);
  body.fields.office_location = 'B';
  await act(async () => {
    await result.current.submit(body, send);
  });
  expect(send.mock.calls[1][0]).toEqual({ title: 'confirmed', fields: { office_location: 'A' } });
  expect(send.mock.calls[1][1].idempotencyKey).toBe(send.mock.calls[0][1].idempotencyKey);
});
it('retains recoverable old unknown attempt while explicitly confirming a new one', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockRejectedValue(new Error('lost'));
  await act(async () => {
    await result.current.submit({ title: 'first' }, send);
  });
  act(() => result.current.newConfirmation());
  await act(async () => {
    await result.current.submit({ title: 'second' }, send);
  });
  expect(result.current.attempts).toHaveLength(2);
  expect(send.mock.calls[0][1].idempotencyKey).not.toBe(send.mock.calls[1][1].idempotencyKey);
  await act(async () => {
    await result.current.retry(result.current.attempts[0].key);
  });
  expect(send.mock.calls[2][0]).toEqual({ title: 'first' });
});
it('prevents retry under a changed actor/tenant and requires fresh confirmation', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockRejectedValue(new Error('lost'));
  await act(async () => {
    await result.current.submit({ title: 'first' }, send);
  });
  useAuthStore.setState({ currentTenant: { id: 3 } as never });
  await act(async () => {
    await result.current.retry(result.current.attempts[0].key);
  });
  expect(send).toHaveBeenCalledTimes(1);
  act(() => result.current.newConfirmation());
  await act(async () => {
    await result.current.submit({ title: 'reviewed' }, send);
  });
  expect(send).toHaveBeenCalledTimes(2);
});

it('keeps a committed receipt successful when detail/navigation handling fails', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockResolvedValue(receipt);
  await act(async () => {
    await result.current.submit({ title: 'saved' }, send, () => {
      throw new Error('detail unavailable');
    });
  });
  expect(result.current.attempts[0]).toMatchObject({ state: 'committed', receipt });
  await act(async () => {
    await result.current.retry(result.current.attempts[0].key);
  });
  expect(send).toHaveBeenCalledTimes(1);
});

it('does not confirm or send without a verified session and can submit after login', async () => {
  useAuthStore.setState({ isAuthenticated: false, user: null, currentTenant: null });
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockResolvedValue(receipt);
  await act(async () => { await result.current.submit({ title: 'draft' }, send); });
  expect(send).not.toHaveBeenCalled();
  expect(result.current.attempts).toEqual([]);
  session();
  await act(async () => { await result.current.submit({ title: 'confirmed after login' }, send); });
  expect(send).toHaveBeenCalledTimes(1);
  expect(result.current.attempts[0]).toMatchObject({ state: 'committed', receipt });
});

it.each([401, 403, 409, 422])('keeps a lost original outcome recoverable when a later retry returns %s', async status => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockRejectedValueOnce(new Error('response lost'))
    .mockRejectedValueOnce(new ApiError('retry rejected', status, 4001, 'retry_rejected', false))
    .mockResolvedValue({ ...receipt, replayed: true });
  await act(async () => { await result.current.submit({ title: 'confirmed' }, send); });
  const originalKey = result.current.attempts[0].key;
  await act(async () => { await result.current.retry(originalKey); });
  expect(result.current.attempts[0]).toMatchObject({ key: originalKey, state: 'unknown' });
  await act(async () => { await result.current.retry(originalKey); });
  expect(result.current.attempts[0]).toMatchObject({ state: 'committed', receipt: { replayed: true } });
  expect(send.mock.calls.map(call => call[1].idempotencyKey)).toEqual([originalKey, originalKey, originalKey]);
});

it('marks a first definitive validation rejection without claiming a committed receipt', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const send = jest.fn().mockRejectedValue(new ApiError('invalid requester', 422, 4001, 'invalid_requester', false));
  await act(async () => { await result.current.submit({ title: 'invalid' }, send); });
  expect(result.current.attempts[0]).toMatchObject({ state: 'rejected', error: 'invalid requester' });
  expect(result.current.attempts[0].receipt).toBeUndefined();
});

it('keeps an in-flight committed receipt but prevents navigation after the actor changes', async () => {
  const { result } = renderHook(() => useWorkItemCreation());
  const onCommitted = jest.fn();
  const send = jest.fn().mockImplementation(async () => {
    useAuthStore.setState({ user: { id: 9, actorTenantId: 2, tenantId: 2 } as never });
    return receipt;
  });
  await act(async () => { await result.current.submit({ title: 'saved for original actor' }, send, onCommitted); });
  expect(result.current.attempts[0]).toMatchObject({ state: 'committed', receipt });
  expect(onCommitted).not.toHaveBeenCalled();
});

it.each(['committed', 'unknown'])('durably requests a fresh confirmation while sending and preserves the settled %s attempt', async state => {
  const { result } = renderHook(() => useWorkItemCreation());
  let reject!: (error: Error) => void;
  let resolve!: (value: typeof receipt) => void;
  const pending = new Promise<typeof receipt>((accept, fail) => { resolve = accept; reject = fail; });
  const send = jest.fn().mockRejectedValueOnce(new Error('lost')).mockReturnValueOnce(pending).mockResolvedValue(receipt);
  await act(async () => { await result.current.submit({ title: 'original' }, send); });
  const originalKey = result.current.attempts[0].key;
  let retry!: Promise<unknown>;
  act(() => {
    retry = result.current.retry(originalKey);
    result.current.newConfirmation();
    result.current.newConfirmation();
    void result.current.submit({ title: 'cannot send concurrently' }, send);
  });
  expect(send).toHaveBeenCalledTimes(2);
  await act(async () => {
    if (state === 'committed') resolve(receipt);
    else reject(new Error('retry lost'));
    await retry;
  });
  await act(async () => { await result.current.submit({ title: 'new confirmation' }, send); });
  expect(send.mock.calls[2][0]).toEqual({ title: 'new confirmation' });
  expect(send.mock.calls[2][1].idempotencyKey).not.toBe(originalKey);
  expect(result.current.attempts).toHaveLength(2);
  expect(result.current.attempts[0]).toMatchObject({ key: originalKey, state });
  expect(send.mock.calls[1][0]).toEqual({ title: 'original' });
  expect(send.mock.calls[1][1].idempotencyKey).toBe(originalKey);
  await act(async () => { await result.current.retry(originalKey); });
  if (state === 'unknown') {
    expect(send.mock.calls[3][0]).toEqual({ title: 'original' });
    expect(send.mock.calls[3][1].idempotencyKey).toBe(originalKey);
  } else expect(send).toHaveBeenCalledTimes(3);
});
