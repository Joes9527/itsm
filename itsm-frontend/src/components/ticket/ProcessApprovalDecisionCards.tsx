'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { GitBranch } from 'lucide-react';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { toApprovalSteps } from '@/components/business/detail-tabs/approvalUtils';
import type { ApprovalStep, ApprovalStepStatus } from '@/components/business/detail-tabs/types';

const statusBadge: Record<ApprovalStepStatus, { text: string; className: string }> = {
  pending: { text: '待审批', className: 'text-orange-600 bg-orange-50 border-orange-200' },
  approved: { text: '节点已通过', className: 'text-emerald-600 bg-emerald-50 border-emerald-200' },
  rejected: { text: '节点已拒绝', className: 'text-red-600 bg-red-50 border-red-200' },
  delegated: { text: '已委派', className: 'text-purple-600 bg-purple-50 border-purple-200' },
  timeout: { text: '已超时', className: 'text-red-600 bg-red-50 border-red-200' },
  skipped: { text: '已跳过', className: 'text-slate-500 bg-slate-100 border-slate-200' },
};

/** Read-only cards projected exclusively from BPMN ProcessApprovalDecision. */
export const ProcessApprovalDecisionCards: React.FC<{ ticketId: number }> = ({ ticketId }) => {
  const [steps, setSteps] = useState<ApprovalStep[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const decisions = await BPMNWorkflowApi.getTicketApprovalDecisions(ticketId);
      setSteps(toApprovalSteps(decisions ?? []));
    } catch {
      setSteps([]);
    } finally {
      setLoading(false);
    }
  }, [ticketId]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) {
    return <div className='p-6 text-center text-xs text-slate-400'>审批链加载中...</div>;
  }

  if (steps.length === 0) {
    return (
      <div className='text-center py-6 text-slate-400'>
        <GitBranch className='w-8 h-8 mx-auto mb-2 text-slate-300' />
        <span className='text-xs'>该工单未走审批流程</span>
      </div>
    );
  }

  return (
    <div className='space-y-3 pt-2 text-xs'>
      {steps.map(step => {
        const badge = statusBadge[step.status];
        return (
          <div
            key={step.id}
            className='p-3.5 bg-slate-50 rounded-xl border border-slate-100 space-y-2'
          >
            <div className='flex items-center justify-between'>
              <span className='font-bold text-slate-800'>
                {step.step || `审批节点 ${step.level}`}
              </span>
              <span
                className={`text-[11px] px-2 py-0.5 rounded font-medium border ${badge.className}`}
              >
                {badge.text}
              </span>
            </div>
            <div className='text-slate-600 space-y-1 text-xs'>
              <div className='flex justify-between'>
                <span>审批人: {step.approverName || '-'}</span>
                <span className='font-mono text-slate-400'>
                  {step.processedAt ? new Date(step.processedAt).toLocaleString('zh-CN') : ''}
                </span>
              </div>
              {step.comment && (
                <div className='bg-white p-2.5 rounded-lg border border-slate-100 text-slate-700'>
                  审批意见：{step.comment}
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
};

export default ProcessApprovalDecisionCards;
