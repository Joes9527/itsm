'use client';

import { useEffect, useRef } from 'react';
import { useAuthStore } from '@/lib/store/auth-store';
import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';

const ACTIVE_STATUSES = ['created', 'assigned', 'started', 'pending'];
function sessionKey(state: ReturnType<typeof useAuthStore.getState>) {
  return JSON.stringify([state.user?.id, state.user?.tenantId, state.currentTenant?.id,
    state.currentTenant?.status, state.isAuthenticated, [...(state.user?.permissions ?? [])].sort()]);
}

// The backend owns scope and task purpose. Query each supported active status and
// exhaust its pages before claiming a complete list; the portal may request a preview.
async function readApprovals(assertContext: () => void, limit: number | undefined, onError: (error: unknown) => void): Promise<UserTask[]> {
  const size = limit ?? 100;
  const outcomes = await Promise.allSettled(ACTIVE_STATUSES.map(async status => {
    const found: UserTask[] = [];
    const seen = new Set<number>();
    for (let page = 1; ; page++) {
      assertContext();
      const response = await BPMNWorkflowApi.listUserTasks({ status, page, pageSize: size }).catch(error => {
        assertContext();
        onError(error);
        throw error;
      });
      assertContext();
      if (!Array.isArray(response.items) || !Number.isFinite(response.total) || response.total < 0) {
        throw new Error('审批待办分页响应无效，请刷新重试');
      }
      for (const task of response.items) {
        if (!seen.has(task.id) && task.taskPurpose?.toLowerCase() === 'approval' &&
            ACTIVE_STATUSES.includes(task.status?.toLowerCase())) found.push(task);
      }
      const oldSize = seen.size;
      response.items.forEach(task => seen.add(task.id));
      const expected = Math.max(0, Math.min(size, response.total - (page - 1) * size));
      if (response.items.length !== expected || seen.size - oldSize !== response.items.length) {
        throw new Error('审批待办分页未完整返回，请刷新重试');
      }
      if ((limit && found.length >= limit) || page * size >= response.total) break;
    }
    return found;
  }));
  const failure = outcomes.find(outcome => outcome.status === 'rejected');
  if (failure?.status === 'rejected') throw failure.reason;
  const pages = outcomes.flatMap(outcome => outcome.status === 'fulfilled' ? outcome.value : []);
  const unique = new Map<number, UserTask>();
  pages.forEach(task => unique.set(task.id, task));
  const time = (task: UserTask) => Date.parse(task.createdTime) || 0;
  const result = [...unique.values()].sort((a, b) => time(b) - time(a) || b.id - a.id);
  return limit ? result.slice(0, limit) : result;
}

export function useApprovalTasks(limit?: number) {
  const state = useAuthStore();
  const identity = sessionKey(state);
  const request = useRef(0);
  const mounted = useRef(true);
  const denyRef = useRef<(error: unknown) => boolean>(() => false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; request.current++; };
  }, [identity]);
  const enabled = Boolean(state.isAuthenticated && state.user?.id && state.user.tenantId &&
    state.currentTenant?.id === state.user.tenantId && state.currentTenant.status === 'active');
  const assertContext = () => {
    if (!enabled || sessionKey(useAuthStore.getState()) !== identity) throw new Error('会话已变化，请重新打开审批');
  };
  const resource = useDetailResource(`${identity}:${limit ?? 'all'}`,
    () => {
      const run = ++request.current;
      const assertRead = () => {
        assertContext();
        if (!mounted.current || request.current !== run) throw new Error('审批读取上下文已变化');
      };
      return readApprovals(assertRead, limit, error => {
        // Every status failure reaches this callback, even after another status failed.
        // Reject stale runs before clearing the current session's data.
        assertRead();
        if (denyRef.current(error)) request.current++;
      });
    }, tasks => tasks.length, undefined, enabled);
  denyRef.current = resource.deny;
  return { ...resource, assertContext };
}
