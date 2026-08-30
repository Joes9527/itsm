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

// 把 Change 响应映射成 WorkItemShell 的公共字段契约。同 Incident/Problem 迁移那次的模式
// （itsm-frontend/src/app/(main)/incidents/[id]/page.tsx、problems/[id]/page.tsx）：id 用
// workItemId（tickets.id，评论/附件/未来的 SLA 都挂在这个 ID 下），number 用变更自己的展示
// 编号（后端目前用 "C-{id}" 格式，见 dto.ChangeCalendarItem.ChangeNumber，这里保持一致）。
function toWorkItemCommon(change: Change): WorkItemCommon | null {
  if (!change.workItemId) {
    // 迁移前创建、还没跑 cmd/backfill_change_work_item 回填的存量变更没有 workItemId，
    // 此时不渲染 WorkItemShell（下面 ChangeDetail 本身仍然完整可用），避免用一个假的
    // ID 挂载评论/附件占位组件。
    return null;
  }
  return {
    id: change.workItemId,
    number: `C-${change.id}`,
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
  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [change, setChange] = useState<Change | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);

  const syncChangeSummary = useCallback((nextChange: Change) => {
    setChange(nextChange);
    setWorkItem(toWorkItemCommon(nextChange));
  }, []);

  const loadSLA = useCallback(async (workItemId: number) => {
    try {
      const data = await TicketApi.getTicketSLA(workItemId);
      setSla({
        slaName: data.slaName,
        responseTime: data.responseTime,
        resolutionTime: data.resolutionTime,
        responseDeadline: data.responseDeadline,
        resolutionDeadline: data.resolutionDeadline,
        responseTimeRemaining: data.responseTimeRemaining,
        resolutionTimeRemaining: data.resolutionTimeRemaining,
        isBreached: data.isBreached,
      });
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
    if (!Number.isFinite(numericId) || numericId <= 0) {
      return;
    }
    try {
      const change = await ChangeApi.getChange(numericId);
      syncChangeSummary(change);
    } catch (err) {
      // WorkItemShell 只是这里的外层展示壳（风险等级/CAB/发布窗口/实施结果/PIR 等专业字段
      // 仍然由下面完整功能的 ChangeDetail 负责渲染/编辑），summary 拉取失败时不阻塞整页——
      // workItem 保持 null，下面直接退化为原有的纯 ChangeDetail 展示，而不是让整页报错。
      // ChangeDetail 组件自己内部另有一次完整的变更详情拉取 + 错误处理，这里的失败不影响
      // 那条路径。
      console.warn('[ChangeDetailPage] Failed to load WorkItem summary', err);
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
  }, [workItem?.id, loadSLA]);

  const renderDetailAndTabs = (fallbackActions?: Change['actions']) => (
    <>
      {/* 主详情组件保持不变——风险等级/CAB/发布窗口（计划开始结束时间）/实施结果/PIR 等
          Change 专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层
          公共身份信息，不重新实现这些逻辑。 */}
      <ChangeDetail
        id={id}
        fallbackActions={fallbackActions}
        onChangeLoaded={syncChangeSummary}
      />

      {/* 追加：审批时间线。历史现在由 WorkItemShell 自己的区块渲染，不再在这里重复一份——
          见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md §5.2。 */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <div style={{ padding: '0 24px 24px' }}>
          <Card className="mt-4 rounded-lg shadow-sm border border-gray-200">
            <div className="flex items-center gap-1.5 mb-3 text-sm font-medium text-gray-700">
              <GitBranch size={14} />
              审批时间线
            </div>
            {approvalLoading ? (
              <div className="p-6 text-center">加载中...</div>
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
      {/* workItem 只有在变更摘要加载成功且带有 workItemId（Wave 2 迁移后创建/已跑过
          cmd/backfill_change_work_item 回填）时才非空。加载中、加载失败（见
          loadWorkItemSummary 的 catch）、或存量未回填变更这三种情况下 workItem 都是
          null，直接退化为原有的纯 ChangeDetail 展示——不用 WorkItemShell 自己的
          loading/error 态挡住已经完整可用的 ChangeDetail。 */}
      {workItem && change ? (
        <WorkItemShell
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
            type="info"
            showIcon
            message="该变更尚未关联 WorkItem，评论/附件/历史/关联等协作能力暂不可用"
            style={{ marginBottom: 16 }}
          />
          {renderDetailAndTabs(change?.actions)}
        </>
      )}
    </App>
  );
}
