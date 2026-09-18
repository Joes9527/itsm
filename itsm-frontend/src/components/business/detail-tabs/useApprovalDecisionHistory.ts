'use client';

import { useContext } from 'react';
import { ApprovalDecisionHistoryContext } from './ApprovalDecisionHistoryContext';

/** Decision records are history, not the process definition or its current task. */
export function useApprovalDecisionHistory(ticketId: number) {
  const owner = useContext(ApprovalDecisionHistoryContext);
  if (!owner || owner.ticketId !== ticketId) {
    throw new Error('Approval decision consumers require a matching ApprovalDecisionHistoryProvider');
  }
  return owner.resource;
}
