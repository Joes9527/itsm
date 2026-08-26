import type { ApprovalStep, ApprovalStepStatus } from './types';
import type { ProcessApprovalDecision } from '@/lib/api/ticket-approval-api';

/**
 * 审批决策 → 步骤状态映射，供 ApprovalWorkflowPanel 与 ApprovalMiniStepper 共用。
 * 单一事实源：审批链历史与右侧迷你 Stepper 不允许各自维护一套状态映射。
 */
export function decisionStatusToStepStatus(decision: string): ApprovalStepStatus {
  switch (decision) {
    case 'approved':
      return 'approved';
    case 'rejected':
      return 'rejected';
    case 'delegated':
      return 'delegated';
    case 'timeout':
      return 'timeout';
    default:
      // withdrawn / system_decision 等在 ApprovalStepStatus 里没有对应值，
      // 归到 skipped——保留记录可见，但不暗示这是一次正常的通过/拒绝决策。
      return 'skipped';
  }
}

export function toApprovalSteps(decisions: ProcessApprovalDecision[]): ApprovalStep[] {
  return decisions.map((d, index) => ({
    id: d.id,
    level: index + 1,
    step: d.nodeKey,
    status: decisionStatusToStepStatus(d.decision),
    approverId: d.actorId,
    approverName: d.actorName,
    comment: d.comment,
    processedAt: d.createdAt,
    createdAt: d.createdAt,
  }));
}
