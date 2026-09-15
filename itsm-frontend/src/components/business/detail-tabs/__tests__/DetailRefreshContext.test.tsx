import React from 'react';
import { act, renderHook } from '@testing-library/react';
import {
  DetailRefreshProvider,
  useDetailRefresh,
  useDetailRefreshEntry,
  type DetailRefreshEntry,
} from '../DetailRefreshContext';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const entry = (
  key: string,
  reload: DetailRefreshEntry['reload'],
  writing = false
): DetailRefreshEntry => ({ key, label: key, reload, isWriting: () => writing });
function wrapper({ children }: { children: React.ReactNode }) {
  return <DetailRefreshProvider identity='one'>{children}</DetailRefreshProvider>;
}
it('deduplicates keys and reports actual outcomes, skipped writers and unknown resources', async () => {
  const first = jest.fn().mockResolvedValue({ status: 'success' });
  const second = jest
    .fn()
    .mockResolvedValue({ status: 'error', denied: false, message: 'offline' });
  const writer = jest.fn();
  const { result } = renderHook(
    () => {
      useDetailRefreshEntry(entry('first', first));
      useDetailRefreshEntry(entry('second', second));
      useDetailRefreshEntry(entry('writer', writer, true));
      return useDetailRefresh()!;
    },
    { wrapper }
  );
  await act(async () =>
    expect(
      await result.current.refresh(['first', 'first', 'second', 'writer', 'unmounted'])
    ).toEqual({
      succeeded: ['first'],
      failed: [{ key: 'second', label: 'second', message: 'offline' }],
      skipped: ['writer', 'unmounted'],
    })
  );
  expect(first).toHaveBeenCalledTimes(1);
  expect(writer).not.toHaveBeenCalled();
  expect(result.current.busy).toBe(false);
});
it('merges repeated clicks and does not count an unregistered pending resource as successful', async () => {
  const pending = deferred<{ status: 'success' }>();
  const reload = jest.fn().mockReturnValue(pending.promise);
  const { result, rerender } = renderHook(
    ({ mounted }) => {
      useDetailRefreshEntry(mounted ? entry('first', reload) : undefined);
      return useDetailRefresh()!;
    },
    { wrapper, initialProps: { mounted: true } }
  );
  let first!: ReturnType<typeof result.current.refresh>;
  let duplicate!: typeof first;
  act(() => {
    first = result.current.refresh();
    duplicate = result.current.refresh();
  });
  expect(result.current.busy).toBe(true);
  rerender({ mounted: false });
  await act(async () => {
    pending.resolve({ status: 'success' });
    expect(await first).toEqual({ succeeded: [], failed: [], skipped: ['first'] });
    expect(await duplicate).toEqual(await first);
  });
  expect(reload).toHaveBeenCalledTimes(1);
  expect(result.current.busy).toBe(false);
});
it('isolates reports and retained callbacks when page identity changes', async () => {
  const pending = deferred<{ status: 'success' }>();
  let identity = 'one';
  const scopedWrapper = ({ children }: { children: React.ReactNode }) => (
    <DetailRefreshProvider identity={identity}>{children}</DetailRefreshProvider>
  );
  const reload = jest
    .fn()
    .mockReturnValueOnce(pending.promise)
    .mockResolvedValue({ status: 'success' });
  const hook = renderHook(
    () => {
      useDetailRefreshEntry(entry('first', reload));
      return useDetailRefresh()!;
    },
    { wrapper: scopedWrapper }
  );
  const oldRefresh = hook.result.current.refresh;
  let old!: ReturnType<typeof oldRefresh>;
  act(() => {
    old = oldRefresh();
  });
  identity = 'two';
  hook.rerender();
  await act(async () => {
    await hook.result.current.refresh();
  });
  await act(async () => {
    pending.resolve({ status: 'success' });
    expect(await old).toEqual({ succeeded: [], failed: [], skipped: ['first'] });
  });
  expect(hook.result.current.report).toEqual({ succeeded: ['first'], failed: [], skipped: [] });
  await act(async () => {
    expect(await oldRefresh()).toEqual({ succeeded: [], failed: [], skipped: [] });
  });
  expect(reload).toHaveBeenCalledTimes(2);
});
it('starts after-write refresh while a manual batch is pending and keeps its report', async () => {
  const pending = deferred<{ status: 'discarded' }>();
  const reload = jest
    .fn()
    .mockReturnValueOnce(pending.promise)
    .mockResolvedValue({ status: 'success' });
  const { result } = renderHook(
    () => {
      useDetailRefreshEntry(entry('first', reload));
      return useDetailRefresh()!;
    },
    { wrapper }
  );
  let old!: ReturnType<typeof result.current.refresh>;
  act(() => {
    old = result.current.refresh();
  });
  await act(async () => {
    await result.current.refresh(['first', 'first'], { afterWrite: true });
  });
  expect(reload).toHaveBeenLastCalledWith({ afterWrite: true });
  await act(async () => {
    pending.resolve({ status: 'discarded' });
    await old;
  });
  expect(result.current.report).toEqual({ succeeded: ['first'], failed: [], skipped: [] });
  expect(result.current.busy).toBe(false);
});
it('turns unexpected thrown errors into failed reports and releases busy', async () => {
  const { result } = renderHook(
    () => {
      useDetailRefreshEntry(
        entry('first', () => {
          throw new Error('broken');
        })
      );
      return useDetailRefresh()!;
    },
    { wrapper }
  );
  await act(async () =>
    expect(await result.current.refresh()).toEqual({
      succeeded: [],
      failed: [{ key: 'first', label: 'first', message: 'broken' }],
      skipped: [],
    })
  );
  expect(result.current.busy).toBe(false);
});

it('skips a resource unmounted after its read completes while another read is pending', async () => {
  const pending = deferred<{ status: 'success' }>();
  const { result, rerender } = renderHook(
    ({ mounted }) => {
      useDetailRefreshEntry(
        mounted ? entry('first', async () => ({ status: 'success' })) : undefined
      );
      useDetailRefreshEntry(entry('second', () => pending.promise));
      return useDetailRefresh()!;
    },
    { wrapper, initialProps: { mounted: true } }
  );
  let batch!: ReturnType<typeof result.current.refresh>;
  await act(async () => {
    batch = result.current.refresh();
  });
  rerender({ mounted: false });
  await act(async () => {
    pending.resolve({ status: 'success' });
    expect(await batch).toEqual({ succeeded: ['second'], failed: [], skipped: ['first'] });
  });
});

it('keeps mounted resources registered through StrictMode effect replay', async () => {
  const strictWrapper = ({ children }: { children: React.ReactNode }) => (
    <React.StrictMode>
      <DetailRefreshProvider identity='one'>{children}</DetailRefreshProvider>
    </React.StrictMode>
  );
  const { result } = renderHook(
    () => {
      useDetailRefreshEntry(entry('first', async () => ({ status: 'success' })));
      return useDetailRefresh()!;
    },
    { wrapper: strictWrapper }
  );
  await act(async () =>
    expect(await result.current.refresh()).toEqual({
      succeeded: ['first'],
      failed: [],
      skipped: [],
    })
  );
});
