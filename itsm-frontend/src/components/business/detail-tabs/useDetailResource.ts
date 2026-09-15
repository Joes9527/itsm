'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

export type DetailReadResult =
  | { status: 'success' }
  | { status: 'error'; message: string; denied: boolean }
  | { status: 'discarded' };
export type DetailReloadOptions = { afterWrite?: boolean };
export type DetailReload = (options?: DetailReloadOptions) => Promise<DetailReadResult>;

type ReadScope = {
  key: string;
  epoch: number;
  request: number;
  mounted: boolean;
  inFlight?: Promise<DetailReadResult>;
};

export function useDetailIdentity(targetId: number | string) {
  const session = useAuthStore(
    state =>
      `${state.user?.id ?? ''}:${state.user?.tenantId ?? ''}:${state.currentTenant?.id ?? ''}`
  );
  return `${session}:${targetId}`;
}

// Local detail panels share request ownership, not business rules or data caches.
export function useDetailResource<T>(
  targetId: number | string,
  load: () => Promise<T>,
  count: (data: T) => number,
  onCountChange?: (count: number | undefined) => void,
  enabled = true
) {
  const identity = useDetailIdentity(targetId);
  const key = `${identity}:${enabled}`;
  const scope = useRef<ReadScope>({ key, epoch: 0, request: 0, mounted: true });
  if (scope.current.key !== key)
    scope.current = { key, epoch: scope.current.epoch + 1, request: 0, mounted: true };
  const callbacks = useRef({ load, count, onCountChange });
  callbacks.current = { load, count, onCountChange };
  const [state, setState] = useState<{
    key: string;
    data?: T;
    loading: boolean;
    error?: string;
    denied: boolean;
  }>({ key, loading: true, denied: false });
  const capture = useCallback(() => {
    const owner = scope.current;
    const epoch = owner.epoch;
    return () => scope.current === owner && owner.mounted && owner.epoch === epoch;
  }, []);
  const deny = useCallback((error: unknown) => {
    if (!(error instanceof ApiError) || ![401, 403].includes(error.status)) return false;
    scope.current.epoch++;
    scope.current.request++;
    scope.current.inFlight = undefined;
    setState({ key: scope.current.key, loading: false, denied: true, error: error.message });
    callbacks.current.onCountChange?.(undefined);
    return true;
  }, []);
  const reload = useCallback<DetailReload>(
    options => {
      const owner = scope.current;
      // A callback retained by an old page must not load the new page's resource.
      if (owner.key !== key || !owner.mounted) return Promise.resolve({ status: 'discarded' });
      if (!enabled) {
        setState({ key, loading: false, denied: true, error: '无权读取' });
        callbacks.current.onCountChange?.(undefined);
        return Promise.resolve({ status: 'error', message: '无权读取', denied: true });
      }
      if (owner.inFlight && !options?.afterWrite) return owner.inFlight;
      const request = ++owner.request;
      const current = () => owner === scope.current && owner.mounted && request === owner.request;
      setState(previous => ({
        ...previous,
        key,
        data: previous.key === key ? previous.data : undefined,
        loading: true,
        error: undefined,
      }));
      // The settlement handler runs after assignment, including synchronous load failures.
      const pending = (async (): Promise<DetailReadResult> => {
        if (!current()) return { status: 'discarded' };
        try {
          const data = await callbacks.current.load();
          if (!current()) return { status: 'discarded' };
          setState({ key, data, loading: false, denied: false });
          callbacks.current.onCountChange?.(callbacks.current.count(data));
          return { status: 'success' };
        } catch (error) {
          if (!current()) return { status: 'discarded' };
          const message = error instanceof Error ? error.message : '读取失败';
          if (deny(error)) return { status: 'error', message, denied: true };
          setState(previous => ({
            ...previous,
            loading: false,
            error:
              (previous.data !== undefined ? '刷新失败，当前显示上次读取的数据：' : '') + message,
          }));
          return { status: 'error', message, denied: false };
        }
      })().finally(() => {
        if (owner.inFlight === pending) owner.inFlight = undefined;
      });
      owner.inFlight = pending;
      return pending;
    },
    [key, enabled, deny]
  );
  useEffect(() => {
    const owner = scope.current;
    owner.mounted = true;
    callbacks.current.onCountChange?.(undefined);
    void reload();
    return () => {
      owner.mounted = false;
      owner.inFlight = undefined;
      owner.request++;
      owner.epoch++;
    };
  }, [reload]);
  const visible = state.key === key && enabled;
  return {
    identity,
    reload,
    capture,
    deny,
    data: visible ? state.data : undefined,
    loading: visible ? state.loading : enabled,
    error: visible ? state.error : !enabled ? '无权读取' : undefined,
    denied: !enabled || (visible && state.denied),
    ready: visible && state.data !== undefined && !state.denied,
  };
}
