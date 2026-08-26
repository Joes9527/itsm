'use client';

import type { ReactNode } from 'react';
import React, { createContext, useContext } from 'react';
import type { WorkItemCommon, WorkItemSLAState, WorkItemActionDispatch } from './WorkItemTypes';

interface WorkItemContextValue {
  workItem: WorkItemCommon;
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
