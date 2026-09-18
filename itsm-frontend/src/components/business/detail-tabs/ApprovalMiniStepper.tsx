'use client';

import React from 'react';
import { GitBranch } from 'lucide-react';
import { useApprovalDecisionHistory } from './useApprovalDecisionHistory';
import { useApprovalAbsenceReason } from './useApprovalAbsenceReason';
import { DetailReadState } from './DetailReadState';
import type { ApprovalStepStatus } from './types';

const statusNodeStyles: Record<
  ApprovalStepStatus,
  { circle: string; text: string; label: string }
> = {
  pending: {
    circle: 'bg-orange-100 text-orange-600 animate-pulse',
    text: 'font-bold text-orange-700',
    label: '进行中',
  },
  approved: {
    circle: 'bg-emerald-100 text-emerald-600',
    text: 'text-foreground',
    label: '已通过',
  },
  rejected: {
    circle: 'bg-red-100 text-red-600',
    text: 'text-red-700',
    label: '已拒绝',
  },
  delegated: {
    circle: 'bg-emerald-100 text-emerald-600',
    text: 'text-foreground',
    label: '已委派',
  },
  timeout: {
    circle: 'bg-red-100 text-red-600',
    text: 'text-red-700',
    label: '已超时',
  },
  skipped: {
    circle: 'bg-raised text-muted',
    text: 'text-muted',
    label: '已跳过',
  },
};

const statusGlyph: Record<ApprovalStepStatus, string> = {
  pending: '●',
  approved: '✓',
  rejected: '✗',
  delegated: '✓',
  timeout: '✗',
  skipped: '○',
};

function formatStepTime(iso?: string): string {
  if (!iso) return '';
  try {
    return new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

/**
 * The containing page must provide ApprovalDecisionHistoryProvider.
 * 工单详情右侧工具箱：审批决策历史。
 * 样式对齐 prototype 的 ✓/●/○ 时间轴；数据源与审批链 Tab 相同
 * （BPMNWorkflowApi.getTicketApprovalDecisions），不引入第二套状态映射。
 */
export const ApprovalMiniStepper: React.FC<{ ticketId: number }> = ({ ticketId }) => {
  const { steps, loading, error, reload, ready } = useApprovalDecisionHistory(ticketId);
  const reason = useApprovalAbsenceReason();

  return (
    <div className="bg-surface rounded-[8px] border border-border p-4 shadow-none space-y-3 text-xs">
      <span className="font-semibold text-foreground flex items-center gap-1.5 border-b border-border pb-2 text-xs">
        <GitBranch size={14} className="text-muted" />
        审批决策历史
      </span>

      <DetailReadState error={error} loading={loading} reload={reload} />
      {loading && !ready && <span>审批决策记录加载中...</span>}
      {ready && !error && !loading && steps.length === 0 ? (
        <span className="block text-muted text-xs">
          暂无审批决策记录
          {reason && <span className="mt-1 block">{reason}</span>}
        </span>
      ) : (
        <div className="space-y-2.5">
          {steps.map((step, idx) => {
            const style = statusNodeStyles[step.status];
            return (
              <div key={step.id ?? idx} className="flex items-center justify-between text-[12px]">
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    className={`w-4 h-4 rounded-full flex items-center justify-center text-[10px] font-bold shrink-0 ${style.circle}`}
                  >
                    {statusGlyph[step.status]}
                  </span>
                  <span className={`text-[12px] truncate ${style.text}`}>
                    {step.step || `审批节点 ${step.level}`}
                  </span>
                  {step.approverName && (
                    <span className="text-[10px] text-muted shrink-0">
                      ({step.approverName})
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-[10px] text-muted">
                    {style.label}
                  </span>
                  <span className="text-[11px] text-muted font-mono">
                    {formatStepTime(step.processedAt)}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default ApprovalMiniStepper;
