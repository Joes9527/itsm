'use client';

/**
 * 工单审批链 Tab
 *
 * 只读展示该工单在 BPMN 引擎里留下的审批决策历史（ProcessApprovalDecision）。
 * 旧版 legacy ApprovalWorkflow 引擎（getWorkflows / getApprovalRecords）已随
 * 后端下线，这里不再拼装"工作流全景 Steps"这种已经不存在的概念——审批状态完全
 * 由 BPMN 驱动，能看到的只有已经发生过的决策记录，没有可预测的"审批链定义"。
 *
 * 这个 Tab 是只读历史展示：提交审批走的是工单详情页别处已有的操作入口，不是这里。
 */

import React, { useCallback, useEffect, useState } from 'react';
import { Spin, Empty, App } from 'antd';
import { BPMNWorkflowApi, type ProcessApprovalDecision } from '@/lib/api/bpmn-workflow-api';
import { ApprovalTimeline } from './ApprovalTimeline';
import { toApprovalSteps } from './approvalUtils';
import { getErrorMessage } from '@/lib/utils/error-message-handler';

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
  const { message } = App.useApp();
  const [decisions, setDecisions] = useState<ProcessApprovalDecision[]>([]);
  const [loading, setLoading] = useState(true);

  const loadAll = useCallback(async () => {
    setLoading(true);
    try {
      const data = await BPMNWorkflowApi.getTicketApprovalDecisions(ticketId);
      setDecisions(data);
    } catch (error) {
      message.error(getErrorMessage(error) || '加载审批记录失败');
      setDecisions([]);
    } finally {
      setLoading(false);
    }
  }, [ticketId, message]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  if (loading) {
    return (
      <div className="p-6">
        <Spin />
      </div>
    );
  }

  const steps = toApprovalSteps(decisions);

  if (steps.length === 0) {
    return (
      <div className="p-6">
        <Empty description="该工单未走审批流程" />
      </div>
    );
  }

  return (
    <ApprovalTimeline
      approvals={steps}
      formatDateTime={formatDateTime}
    />
  );
};

export default ApprovalWorkflowPanel;
