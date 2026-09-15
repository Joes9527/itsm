'use client';

import React, { createContext } from 'react';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { toApprovalSteps } from './approvalUtils';
import { useDetailResource } from './useDetailResource';
import { useDetailRefreshEntry } from './DetailRefreshContext';

function useDecisionResource(ticketId: number) {
  const resource = useDetailResource(
    ticketId,
    async () => {
      const decisions = await BPMNWorkflowApi.getTicketApprovalDecisions(ticketId);
      if (!Array.isArray(decisions)) throw new Error('审批决策记录格式异常');
      return decisions;
    },
    decisions => decisions.length
  );
  useDetailRefreshEntry({
    key: 'approval-decisions',
    label: '审批决策历史',
    reload: resource.reload,
    isWriting: () => false,
  });
  return {
    ...resource,
    steps: toApprovalSteps(resource.data ?? []),
    decisionCount: resource.ready ? resource.data?.length : undefined,
  };
}

export const ApprovalDecisionHistoryContext = createContext<
  { ticketId: number; resource: ReturnType<typeof useDecisionResource> } | undefined
>(undefined);

/** Each detail page owns one authorized decision history; consumers never fetch. */
export function ApprovalDecisionHistoryProvider({
  ticketId,
  children,
}: {
  ticketId: number;
  children: React.ReactNode;
}) {
  const resource = useDecisionResource(ticketId);
  return (
    <ApprovalDecisionHistoryContext.Provider value={{ ticketId, resource }}>
      {children}
    </ApprovalDecisionHistoryContext.Provider>
  );
}
