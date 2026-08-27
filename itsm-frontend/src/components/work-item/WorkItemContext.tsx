'use client';

import type { ReactNode } from 'react';
import React, { createContext, useContext } from 'react';
import type {
  WorkItemCommon,
  WorkItemSLAState,
  WorkItemActionDispatch,
  WorkItemActionState,
} from './WorkItemTypes';

export interface WorkItemContextValue {
  workItem: WorkItemCommon;
  /**
   * 后端下发的动作可用性（按动作名索引）。专业 Panel 必须据此渲染按钮的禁用态和
   * 禁用原因，而不是自己复刻一套权限/状态机判断——WorkItemShellProps.actions 是
   * 锁定契约的一部分，它必须能被 Panel 通过 context 读到，否则 Shell 收下这个 prop
   * 却谁也拿不到，Wave 2 各域只能各写各的判断。
   */
  actions: Record<string, WorkItemActionState>;
  sla?: WorkItemSLAState;
  onActionDispatch: WorkItemActionDispatch;
}

const WorkItemContext = createContext<WorkItemContextValue | null>(null);

export function WorkItemProvider({
  value,
  children,
}: {
  value: WorkItemContextValue;
  children: ReactNode;
}) {
  return <WorkItemContext.Provider value={value}>{children}</WorkItemContext.Provider>;
}

export function useWorkItemContext(): WorkItemContextValue {
  const ctx = useContext(WorkItemContext);
  if (!ctx) {
    throw new Error('useWorkItemContext must be used within a WorkItemProvider (i.e. inside WorkItemShell)');
  }
  return ctx;
}
