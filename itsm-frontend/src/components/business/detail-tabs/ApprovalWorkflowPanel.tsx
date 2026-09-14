'use client';

/**
 * 工单审批链 Tab
 *
 * 只读展示该工单在 BPMN 引擎里留下的审批决策历史（ProcessApprovalDecision）。
 * 旧版 legacy ApprovalWorkflow 引擎（getWorkflows / getApprovalRecords）已随
 * 后端下线，这里不再拼装"工作流全景 Steps"这种已经不存在的概念——审批状态完全
 * 由 BPMN 驱动，能看到的只有已经发生过的决策记录，没有可预测的"审批链定义"。
 *
 * 这个 Tab 只读展示历史，审批操作由既有审批中心处理。
 */

import React from 'react';
import { Empty } from 'antd';
import { ApprovalTimeline } from './ApprovalTimeline';
import { useApprovalDecisionHistory } from './useApprovalDecisionHistory';
import { DetailReadState } from './DetailReadState';

export interface ApprovalWorkflowPanelProps {
  ticketId: number;
  ticketType?: string;
  priority?: string;
  currentUserId?: number;
  isTicketFinal: boolean;
  onRefresh?: () => void;
  formatDateTime?: (s: string) => string;
}

export const ApprovalWorkflowPanel: React.FC<ApprovalWorkflowPanelProps> = ({
  ticketId,
  formatDateTime,
}) => {
  const { steps, loading, error, reload, ready } = useApprovalDecisionHistory(ticketId);
  return (
    <div className="p-6">
      <DetailReadState error={error} loading={loading} reload={reload} />
      {loading && !ready && <div>审批决策记录加载中...</div>}
      {ready && !error && !loading && steps.length === 0 && (
        <Empty description="暂无审批决策记录" />
      )}
      {steps.length > 0 && <ApprovalTimeline approvals={steps} formatDateTime={formatDateTime} />}
    </div>
  );
};

export default ApprovalWorkflowPanel;
