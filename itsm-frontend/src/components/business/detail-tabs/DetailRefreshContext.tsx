'use client';

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { DetailReload, DetailReloadOptions } from './useDetailResource';

export type DetailRefreshEntry = {
  key: string;
  label: string;
  reload: DetailReload;
  isWriting: () => boolean;
};
export type DetailRefreshReport = {
  succeeded: string[];
  failed: { key: string; label: string; message: string }[];
  skipped: string[];
};
export type DetailRefreshController = {
  register: (entry: DetailRefreshEntry) => () => void;
  refresh: (
    keys?: readonly string[],
    options?: DetailReloadOptions
  ) => Promise<DetailRefreshReport>;
  busy: boolean;
  report?: DetailRefreshReport;
};
const Context = createContext<DetailRefreshController | undefined>(undefined);
const emptyReport = (): DetailRefreshReport => ({ succeeded: [], failed: [], skipped: [] });

export function DetailRefreshProvider({
  identity,
  children,
}: {
  identity: string;
  children: React.ReactNode;
}) {
  const scope = useMemo(
    () => ({
      entries: new Map<string, DetailRefreshEntry>(),
      batches: new Map<string, Promise<DetailRefreshReport>>(),
      mounted: true,
      generation: 0,
      latest: 0,
      pending: 0,
    }),
    [identity]
  );
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const [state, setState] = useState<{
    scope: typeof scope;
    busy: boolean;
    report?: DetailRefreshReport;
  }>({ scope, busy: false });
  useEffect(() => {
    scope.mounted = true;
    return () => {
      scope.mounted = false;
      scope.generation++;
      scope.batches.clear();
    };
  }, [scope]);
  const register = useCallback(
    (entry: DetailRefreshEntry) => {
      if (currentScope.current !== scope || !scope.mounted) return () => {};
      scope.entries.set(entry.key, entry);
      return () => {
        if (scope.entries.get(entry.key) === entry) scope.entries.delete(entry.key);
      };
    },
    [scope]
  );
  const refresh = useCallback<DetailRefreshController['refresh']>(
    (keys, options) => {
      if (currentScope.current !== scope || !scope.mounted) return Promise.resolve(emptyReport());
      const selected = [...new Set(keys ?? scope.entries.keys())];
      const signature = JSON.stringify([...selected].sort());
      const existing = scope.batches.get(signature);
      if (existing && !options?.afterWrite) return existing;
      const generation = scope.generation;
      const batch = ++scope.latest;
      const current = () =>
        currentScope.current === scope && scope.mounted && scope.generation === generation;
      scope.pending++;
      setState(previous => ({
        scope,
        busy: true,
        report: previous.scope === scope ? previous.report : undefined,
      }));
      const participants = new Map(selected.map(key => [key, scope.entries.get(key)]));
      const reads = selected.map(async key => {
        const entry = scope.entries.get(key);
        if (!entry) return { key, status: 'discarded' as const };
        try {
          if (entry.isWriting()) return { key, status: 'discarded' as const };
          const outcome = await entry.reload(options);
          if (!current() || scope.entries.get(key) !== entry)
            return { key, status: 'discarded' as const };
          return { ...outcome, key, label: entry.label };
        } catch (error) {
          if (!current() || scope.entries.get(key) !== entry)
            return { key, status: 'discarded' as const };
          return {
            key,
            label: entry.label,
            status: 'error' as const,
            message: error instanceof Error ? error.message : '读取失败',
          };
        }
      });
      const pending = Promise.all(reads)
        .then(outcomes => {
          const report = emptyReport();
          for (const outcome of outcomes) {
            if (!current() || scope.entries.get(outcome.key) !== participants.get(outcome.key))
              report.skipped.push(outcome.key);
            else if (outcome.status === 'success') report.succeeded.push(outcome.key);
            else if (outcome.status === 'error')
              report.failed.push({
                key: outcome.key,
                label: outcome.label,
                message: outcome.message,
              });
            else report.skipped.push(outcome.key);
          }
          if (current() && scope.latest === batch)
            setState({ scope, busy: scope.pending > 1, report });
          return report;
        })
        .finally(() => {
          scope.pending--;
          if (scope.batches.get(signature) === pending) scope.batches.delete(signature);
          if (current())
            setState(previous =>
              previous.scope === scope ? { ...previous, busy: scope.pending > 0 } : previous
            );
        });
      scope.batches.set(signature, pending);
      return pending;
    },
    [scope]
  );
  const value = useMemo(
    () => ({
      register,
      refresh,
      busy: state.scope === scope && state.busy,
      report: state.scope === scope ? state.report : undefined,
    }),
    [register, refresh, scope, state]
  );
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function useDetailRefresh() {
  return useContext(Context);
}

export function useDetailRefreshEntry(entry: DetailRefreshEntry | undefined): void {
  const register = useDetailRefresh()?.register;
  const latest = useRef(entry);
  latest.current = entry;
  const key = entry?.key;
  useEffect(() => {
    if (!register || key === undefined) return;
    return register({
      key,
      get label() {
        return latest.current?.label ?? key;
      },
      reload: options =>
        latest.current?.reload(options) ?? Promise.resolve({ status: 'discarded' }),
      isWriting: () => latest.current?.isWriting() ?? true,
    });
  }, [register, key]);
}
