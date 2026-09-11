'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { Alert, App, Card } from 'antd';
import { GitBranch } from 'lucide-react';
import { useParams } from 'next/navigation';
import ChangeDetail from '@/components/change/ChangeDetail';
import { ChangeApi, type Change, type ChangeApproval } from '@/lib/api/change-api';
import { TicketApi } from '@/lib/api/ticket-api';
import {
  ApprovalTimeline,
  type ApprovalStep,
  type ApprovalStepStatus,
} from '@/components/business/detail-tabs';
import { workItemIdentity } from '@/components/work-item/identity';
import { assignChangeWorkItem } from '@/lib/api/workitem-assignment';
import { useAssignmentCandidates } from '@/components/work-item/useAssignmentCandidates';
import type { AssignmentInput } from '@/components/work-item/WorkItemAssignment';
import { mapWorkItemSLA } from '@/components/work-item/mapWorkItemSLA';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';
import dayjs from 'dayjs';

const formatDateTime = (v?: string) => (v ? dayjs(v).format('YYYY-MM-DD HH:mm') : '-');

/**
 * 将 change 的 status 映射到 ApprovalStepStatus
 * change 的审批 record.status 会用 change 的 workflow 状态，例如 approved/rejected/pending
 */
function mapApprovalStatus(status: string): ApprovalStepStatus {
  switch (status) {
    case 'approved':
      return 'approved';
    case 'rejected':
      return 'rejected';
    case 'pending':
      return 'pending';
    default:
      return 'pending';
  }
}

function toWorkItemCommon(change: Change): WorkItemCommon {
  return {
    ...workItemIdentity(change),
    recordClass: 'change_request',
    title: change.title,
    status: change.status,
    priority: change.priority,
    requesterId: change.createdBy,
    assigneeId: change.assigneeId,
    createdAt: change.createdAt || '',
    updatedAt: change.updatedAt || '',
  };
}

export default function ChangeDetailPage() {
  const params = useParams();
  const id = params?.id as string;
  const numericId = Number(id);

  const [approvals, setApprovals] = useState<ApprovalStep[]>([]);
  const [approvalLoading, setApprovalLoading] = useState(false);
  const [identityError, setIdentityError] = useState<string | null>(null);
  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [change, setChange] = useState<Change | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);
  const [panelRevision, setPanelRevision] = useState(0);
  const directory = useAssignmentCandidates(Boolean(change?.actions?.assign?.allowed));

  const syncChangeSummary = useCallback((nextChange: Change) => {
    try {
      const common = toWorkItemCommon(nextChange);
      setChange(nextChange);
      setWorkItem(common);
      setIdentityError(null);
    } catch (error) {
      setWorkItem(null);
      setIdentityError(error instanceof Error ? error.message : 'WorkItem 身份或版本无效');
    }
  }, []);

  const refreshAssignment = useCallback(async () => {
    const latest = await ChangeApi.getChange(numericId);
    const identity = workItemIdentity(latest);
    syncChangeSummary(latest);
    setPanelRevision(value => value + 1);
    return {
      version: identity.version,
      currentAssigneeId: latest.assigneeId,
      allowed: latest.actions?.assign?.allowed === true,
      disabledReason: latest.actions?.assign?.reason,
    };
  }, [numericId, syncChangeSummary]);
  const submitAssignment = async (input: AssignmentInput) => {
    await assignChangeWorkItem(numericId, input);
    await refreshAssignment();
  };

  const loadSLA = useCallback(async (workItemId: number) => {
    try {
      const data = await TicketApi.getTicketSLA(workItemId);
      setSla(mapWorkItemSLA(data));
    } catch (err) {
      console.warn('[ChangeDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  const loadApprovals = useCallback(async () => {
    if (!Number.isFinite(numericId) || numericId <= 0) return;
    setApprovalLoading(true);
    try {
      const records = await ChangeApi.getChangeApprovals(numericId);
      const steps: ApprovalStep[] = (records || []).map((r: ChangeApproval, idx: number) => ({
        id: r.id,
        level: idx + 1,
        status: mapApprovalStatus(r.status),
        approverId: r.approverId,
        approverName: r.approverName,
        comment: r.comment,
        processedAt: r.approvedAt,
        createdAt: r.createdAt,
      }));
      setApprovals(steps);
    } catch (e) {
      // 静默：Empty 态
      console.warn('load change approvals failed', e);
      setApprovals([]);
    } finally {
      setApprovalLoading(false);
    }
  }, [numericId]);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isInteger(numericId) || numericId <= 0) {
      setIdentityError('专业记录身份无效');
      return;
    }
    try {
      const change = await ChangeApi.getChange(numericId);
      syncChangeSummary(change);
    } catch (err) {
      setWorkItem(null);
      setIdentityError('无法加载权威身份和版本，请刷新详情');
    }
  }, [numericId, syncChangeSummary]);

  useEffect(() => {
    void loadApprovals();
  }, [loadApprovals]);

  useEffect(() => {
    void loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, change?.version, loadSLA]);

  const renderDetailAndTabs = () => (
    <>
      {/* 主详情组件保持不变——风险等级/CAB/发布窗口（计划开始结束时间）/实施结果/PIR 等
          Change 专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层
          公共身份信息，不重新实现这些逻辑。 */}
      <ChangeDetail key={panelRevision} id={id} onChangeLoaded={syncChangeSummary} />

      {/* 追加：审批时间线。历史现在由 WorkItemShell 自己的区块渲染，不再在这里重复一份——
          见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md §5.2。 */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <div style={{ padding: '0 24px 24px' }}>
          <Card className='mt-4 rounded-lg shadow-sm border border-gray-200'>
            <div className='flex items-center gap-1.5 mb-3 text-sm font-medium text-gray-700'>
              <GitBranch size={14} />
              审批时间线
            </div>
            {approvalLoading ? (
              <div className='p-6 text-center'>加载中...</div>
            ) : (
              <ApprovalTimeline
                approvals={approvals}
                canApprove={false}
                showApprovalActions={false}
                formatDateTime={formatDateTime}
              />
            )}
          </Card>
        </div>
      )}
    </>
  );

  return (
    <App>
      {/* workItem 只有在变更摘要加载成功且满足 WorkItem 创建不变量时才非空。加载中、
          加载失败（见 loadWorkItemSummary 的 catch）或无效开发记录下 workItem 为 null，
          不用猜测的 ID 挂载 WorkItemShell。 */}
      {workItem && change?.id === numericId ? (
        <WorkItemShell
          assignment={{
            currentAssigneeId: workItem.assigneeId,
            version: workItem.version,
            allowed: change.actions?.assign?.allowed === true && !directory.error,
            disabledReason: directory.error || change.actions?.assign?.reason,
            candidates: directory.candidates,
            submit: submitAssignment,
            refresh: refreshAssignment,
          }}
          workItem={workItem}
          sla={sla}
          actions={change.actions ?? {}}
          showActionBar={false}
          onActionDispatch={async () => {}}
          professionalPanelSlot={renderDetailAndTabs()}
        />
      ) : (
        <>
          <Alert
            type={identityError ? 'error' : 'info'}
            showIcon
            message={identityError ?? '正在加载权威身份和版本…'}
            style={{ marginBottom: 16 }}
          />
        </>
      )}
    </App>
  );
}
