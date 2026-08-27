'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { App, Button, Card, Tabs } from 'antd';
import { ArrowLeft, MessageSquare, Clock as HistoryIcon } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import IncidentDetail from '@/components/incident/IncidentDetail';
import {
  CommentPanel,
  HistoryTimeline,
  incidentCommentAdapter,
  fetchAuditLogHistory,
} from '@/components/business/detail-tabs';
import { useAuthStore } from '@/lib/store/auth-store';
import { IncidentAPI, type Incident } from '@/lib/api/incident-api';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon } from '@/components/work-item/WorkItemTypes';
import dayjs from 'dayjs';

const formatDateTime = (v?: string) => (v ? dayjs(v).format('YYYY-MM-DD HH:mm') : '-');

// 把 Incident 响应映射成 WorkItemShell 的公共字段契约。注意 id 用的是 workItemId
// （tickets.id，评论/附件/未来的 SLA 都挂在这个 ID 下），number 用的是 incidentNumber
// （事件自己的专业编号，用户在事件相关的上下文里认的是这个）——两者不是同一个数字，
// 混用会导致 WorkItemComments/WorkItemAttachments 挂到错误的 WorkItem 上。
function toWorkItemCommon(incident: Incident): WorkItemCommon | null {
  if (!incident.workItemId) {
    // 迁移前创建、还没跑 cmd/backfill_incident_work_item 回填的存量事件没有 workItemId，
    // 此时不渲染 WorkItemShell（下面 IncidentDetail 本身仍然完整可用），避免用一个假的
    // ID 挂载评论/附件占位组件。
    return null;
  }
  return {
    id: incident.workItemId,
    number: incident.incidentNumber || `#${incident.id}`,
    recordClass: 'incident',
    title: incident.title,
    status: incident.status,
    priority: incident.priority,
    requesterId: incident.reporterId ?? incident.reporter?.id ?? 0,
    assigneeId: incident.assigneeId ?? incident.assignee?.id,
    createdAt: incident.createdAt || '',
    updatedAt: incident.updatedAt || '',
  };
}

// 动态路由参数类型
export default function IncidentDetailPage() {
  const router = useRouter();
  const params = useParams();
  const id = params?.id as string;
  const numericId = Number(id);
  const { user } = useAuthStore();

  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isFinite(numericId) || numericId <= 0) {
      return;
    }
    try {
      const incident = await IncidentAPI.getIncident(numericId);
      setWorkItem(toWorkItemCommon(incident));
    } catch (err) {
      // WorkItemShell 只是这里的外层展示壳（专业字段仍然由下面完整功能的 IncidentDetail
      // 负责渲染/编辑），summary 拉取失败时不阻塞整页——workItem 保持 null，下面直接
      // 退化为原有的纯 IncidentDetail 展示，而不是让整页报错。IncidentDetail 组件自己
      // 内部另有一次完整的事件详情拉取 + 错误处理，这里的失败不影响那条路径。
      console.warn('[IncidentDetailPage] Failed to load WorkItem summary', err);
    }
  }, [numericId]);

  useEffect(() => {
    loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  const detailAndTabs = (
    <>
      {/* 主详情组件保持不变——严重程度/影响范围/紧急程度/关联CI/升级状态等 Incident
          专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共
          身份信息，不重新实现这些逻辑。 */}
      <IncidentDetail id={id} />

      {/* 追加：协作与历史 Tabs */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <Card className="mt-4 rounded-lg shadow-sm border border-gray-200">
          <Tabs
            defaultActiveKey="comments"
            items={[
              {
                key: 'comments',
                label: (
                  <span>
                    <MessageSquare size={14} className="inline mr-1" />
                    评论
                  </span>
                ),
                children: (
                  <CommentPanel
                    targetType="incident"
                    targetId={numericId}
                    adapter={incidentCommentAdapter}
                    currentUserId={user?.id}
                    formatDateTime={formatDateTime}
                    showInternalToggle={false}
                  />
                ),
              },
              {
                key: 'history',
                label: (
                  <span>
                    <HistoryIcon size={14} className="inline mr-1" />
                    历史（审计日志）
                  </span>
                ),
                children: (
                  <HistoryTimeline
                    targetType="incident"
                    targetId={numericId}
                    fetchAuditLog={fetchAuditLogHistory}
                    formatDateTime={formatDateTime}
                  />
                ),
              },
            ]}
          />
        </Card>
      )}
    </>
  );

  return (
    <App>
      <div style={{ padding: 24 }}>
        <div style={{ marginBottom: 16 }}>
          <Button
            type="link"
            icon={<ArrowLeft />}
            onClick={() => router.back()}
            style={{ paddingLeft: 0, color: '#666' }}
          >
            返回列表
          </Button>
        </div>

        {/* workItem 只有在事件摘要加载成功且带有 workItemId（Wave 2 迁移后创建/已跑过
            cmd/backfill_incident_work_item 回填）时才非空。加载中、加载失败（见
            loadWorkItemSummary 的 catch）、或存量未回填事件这三种情况下 workItem 都是
            null，直接退化为原有的纯 IncidentDetail 展示——不用 WorkItemShell 自己的
            loading/error 态挡住已经完整可用的 IncidentDetail。 */}
        {workItem ? (
          <WorkItemShell
            workItem={workItem}
            // Incident 各专业操作（确认/解决/关闭/升级/分配……）仍由下面 IncidentDetail
            // 内部既有的按钮 + IncidentAPI 调用处理，尚未统一收口到 onActionDispatch——
            // 那需要后端补一个"动作可用性"契约（actions 参数目前也是空的），是比这次
            // WorkItem 迁移更大的后续改造，不在本任务范围内。
            actions={{}}
            onActionDispatch={async () => {}}
            professionalPanelSlot={detailAndTabs}
          />
        ) : (
          detailAndTabs
        )}
      </div>
    </App>
  );
}
