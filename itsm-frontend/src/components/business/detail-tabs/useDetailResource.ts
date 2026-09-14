'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

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
  const scope = useRef({ key, epoch: 0, request: 0, mounted: true });
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
    setState({ key: scope.current.key, loading: false, denied: true, error: error.message });
    callbacks.current.onCountChange?.(undefined);
    return true;
  }, []);
  const reload = useCallback(async () => {
    const owner = scope.current;
    const request = ++owner.request;
    const current = () => owner === scope.current && owner.mounted && request === owner.request;
    if (!enabled) {
      setState({ key, loading: false, denied: true, error: '无权读取' });
      callbacks.current.onCountChange?.(undefined);
      return;
    }
    setState(previous => ({
      ...previous,
      key,
      data: previous.key === key ? previous.data : undefined,
      loading: true,
      error: undefined,
    }));
    try {
      const data = await callbacks.current.load();
      if (!current()) return;
      setState({ key, data, loading: false, denied: false });
      callbacks.current.onCountChange?.(callbacks.current.count(data));
    } catch (error) {
      if (!current() || deny(error)) return;
      setState(previous => ({
        ...previous,
        loading: false,
        error:
          (previous.data !== undefined ? '刷新失败，当前显示上次读取的数据：' : '') +
          (error instanceof Error ? error.message : '读取失败'),
      }));
    }
  }, [key, enabled, deny]);
  useEffect(() => {
    const owner = scope.current;
    owner.mounted = true;
    callbacks.current.onCountChange?.(undefined);
    void reload();
    return () => {
      owner.mounted = false;
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
