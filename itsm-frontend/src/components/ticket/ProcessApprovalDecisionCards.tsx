'use client';

import React from 'react';
import { GitBranch } from 'lucide-react';
import { useApprovalDecisionHistory } from '@/components/business/detail-tabs/useApprovalDecisionHistory';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';
import type { ApprovalStepStatus } from '@/components/business/detail-tabs/types';

const statusBadge: Record<ApprovalStepStatus, { text: string; className: string }> = {
  pending: { text: '待审批', className: 'text-orange-600 bg-orange-50 border-orange-200' },
  approved: { text: '节点已通过', className: 'text-emerald-600 bg-emerald-50 border-emerald-200' },
  rejected: { text: '节点已拒绝', className: 'text-red-600 bg-red-50 border-red-200' },
  delegated: { text: '已委派', className: 'text-purple-600 bg-purple-50 border-purple-200' },
  timeout: { text: '已超时', className: 'text-red-600 bg-red-50 border-red-200' },
  skipped: {
    text: '已跳过',
    className:
      'text-muted bg-raised border-border',
  },
};

/** Read-only cards projected exclusively from BPMN ProcessApprovalDecision. */
export const ProcessApprovalDecisionCards: React.FC<{ ticketId: number }> = ({ ticketId }) => {
  const { steps, loading, error, reload, ready } = useApprovalDecisionHistory(ticketId);

  return (
    <div className='space-y-3 pt-2 text-[12px]'>
      <DetailReadState error={error} loading={loading} reload={reload} />
      {loading && !ready && <div>审批决策记录加载中...</div>}
      {ready && !error && !loading && steps.length === 0 && (
        <div className='text-center py-6 text-muted'>
          <GitBranch className='w-8 h-8 mx-auto mb-2 text-muted' />
          <span>暂无审批决策记录</span>
        </div>
      )}
      {steps.map(step => {
        const badge = statusBadge[step.status];
        return (
          <div
            key={step.id}
            className="p-3.5 bg-raised rounded-[8px] border border-border space-y-2"
          >
            <div className="flex items-center justify-between">
              <span className="font-bold text-foreground">
                {step.step || `审批节点 ${step.level}`}
              </span>
              <span
                className={`text-[11px] px-2 py-0.5 rounded font-medium border ${badge.className}`}
              >
                {badge.text}
              </span>
            </div>
            <div className="text-muted space-y-1 text-[12px]">
              <div className="flex justify-between">
                <span>审批人: {step.approverName || '-'}</span>
                <span className="font-mono text-muted">
                  {step.processedAt ? new Date(step.processedAt).toLocaleString('zh-CN') : ''}
                </span>
              </div>
              {step.comment && (
                <div className="bg-surface p-2.5 rounded-[8px] border border-border text-foreground">
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
