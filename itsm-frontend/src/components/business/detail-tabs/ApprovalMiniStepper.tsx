'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { Steps } from 'antd';
import { GitBranch } from 'lucide-react';
import { TicketApprovalApi } from '@/lib/api/ticket-approval-api';
import { toApprovalSteps } from './approvalUtils';
import type { ApprovalStep, ApprovalStepStatus } from './types';

const stepStatusMap: Record<ApprovalStepStatus, 'wait' | 'process' | 'finish' | 'error'> = {
  pending: 'process',
  approved: 'finish',
  rejected: 'error',
  delegated: 'finish',
  timeout: 'error',
  skipped: 'wait',
};

const decisionLabels: Record<ApprovalStepStatus, string> = {
  pending: '待审批',
  approved: '已通过',
  rejected: '已拒绝',
  delegated: '已委派',
  timeout: '已超时',
  skipped: '已跳过',
};

/**
 * 工单详情右侧工具箱：流转节点进度（Mini BPMN Stepper）。
 * 数据源与审批链 Tab 相同（TicketApprovalApi.getApprovalDecisions），
 * 这里只做紧凑纵向展示，不引入第二套审批状态映射。
 */
export const ApprovalMiniStepper: React.FC<{ ticketId: number }> = ({ ticketId }) => {
  const [steps, setSteps] = useState<ApprovalStep[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const decisions = await TicketApprovalApi.getApprovalDecisions(ticketId);
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
        流转节点进度
      </span>

      {steps.length === 0 ? (
        <span className="text-slate-400 text-xs">该工单未走审批流程</span>
      ) : (
        <Steps
          size="small"
          direction="vertical"
          current={steps.length}
          items={steps.map(step => ({
            title: <span className="text-xs">{step.step || `审批节点 ${step.level}`}</span>,
            description: (
              <span className="text-[11px] text-slate-500">
                {step.approverName || '-'} · {decisionLabels[step.status]}
              </span>
            ),
            status: stepStatusMap[step.status],
          }))}
        />
      )}
    </div>
  );
};

export default ApprovalMiniStepper;
