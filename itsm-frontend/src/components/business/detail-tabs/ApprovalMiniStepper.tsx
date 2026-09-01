'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { GitBranch } from 'lucide-react';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { toApprovalSteps } from './approvalUtils';
import type { ApprovalStep, ApprovalStepStatus } from './types';

const statusNodeStyles: Record<ApprovalStepStatus, { circle: string; text: string; label: string }> = {
  pending: {
    circle: 'bg-orange-100 text-orange-600 animate-pulse',
    text: 'font-bold text-orange-700',
    label: '进行中',
  },
  approved: {
    circle: 'bg-emerald-100 text-emerald-600',
    text: 'text-slate-700',
    label: '已通过',
  },
  rejected: {
    circle: 'bg-red-100 text-red-600',
    text: 'text-red-700',
    label: '已拒绝',
  },
  delegated: {
    circle: 'bg-emerald-100 text-emerald-600',
    text: 'text-slate-700',
    label: '已委派',
  },
  timeout: {
    circle: 'bg-red-100 text-red-600',
    text: 'text-red-700',
    label: '已超时',
  },
  skipped: {
    circle: 'bg-slate-100 text-slate-400',
    text: 'text-slate-400',
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
 * 工单详情右侧工具箱：流转节点进度（BPMN）。
 * 样式对齐 prototype 的 ✓/●/○ 时间轴；数据源与审批链 Tab 相同
 * （BPMNWorkflowApi.getTicketApprovalDecisions），不引入第二套状态映射。
 */
export const ApprovalMiniStepper: React.FC<{ ticketId: number }> = ({ ticketId }) => {
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

  if (loading) return null;

  return (
    <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
      <span className="font-bold text-slate-800 flex items-center gap-1.5 border-b border-slate-100 pb-2 text-xs">
        <GitBranch size={14} className="text-slate-500" />
        流转节点进度 (BPMN)
      </span>

      {steps.length === 0 ? (
        <span className="text-slate-400 text-xs">该工单未走审批流程</span>
      ) : (
        <div className="space-y-2.5">
          {steps.map((step, idx) => {
            const style = statusNodeStyles[step.status];
            return (
              <div key={step.id ?? idx} className="flex items-center justify-between text-xs">
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    className={`w-4 h-4 rounded-full flex items-center justify-center text-[10px] font-bold shrink-0 ${style.circle}`}
                  >
                    {statusGlyph[step.status]}
                  </span>
                  <span className={`text-xs truncate ${style.text}`}>
                    {step.step || `审批节点 ${step.level}`}
                  </span>
                  {step.approverName && (
                    <span className="text-[10px] text-slate-400 shrink-0">({step.approverName})</span>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-[10px] text-slate-400">{style.label}</span>
                  <span className="text-[11px] text-slate-400 font-mono">{formatStepTime(step.processedAt)}</span>
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
