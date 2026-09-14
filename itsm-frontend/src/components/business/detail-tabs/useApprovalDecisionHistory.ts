import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { toApprovalSteps } from './approvalUtils';
import { useDetailResource } from './useDetailResource';

/** Decision records are history, not the process definition or its current task. */
export function useApprovalDecisionHistory(ticketId: number) {
  const resource = useDetailResource(ticketId, async () => {
    const decisions = await BPMNWorkflowApi.getTicketApprovalDecisions(ticketId);
    if (!Array.isArray(decisions)) throw new Error('审批决策记录格式异常');
    return decisions;
  }, decisions => decisions.length);
  return { ...resource, steps: toApprovalSteps(resource.data ?? []) };
}
