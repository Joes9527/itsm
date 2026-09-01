'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { Alert, App, Button } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import IncidentDetail from '@/components/incident/IncidentDetail';
import { IncidentAPI, type Incident } from '@/lib/api/incident-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';

// 把 Incident 响应映射成 WorkItemShell 的公共字段契约。注意 id 用的是 workItemId
// （tickets.id，评论/附件/未来的 SLA 都挂在这个 ID 下），number 用的是 incidentNumber
// （事件自己的专业编号，用户在事件相关的上下文里认的是这个）——两者不是同一个数字，
// 混用会导致 WorkItemComments/WorkItemAttachments 挂到错误的 WorkItem 上。
function toWorkItemCommon(incident: Incident): WorkItemCommon | null {
  if (!incident.workItemId) {
    // 缺少 workItemId 表示开发数据违反 WorkItem 创建不变量。拒绝用专业记录 ID 猜测
    // WorkItem 身份，避免把评论或附件挂到错误记录。
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

  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [incident, setIncident] = useState<Incident | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);

  const syncIncidentSummary = useCallback((nextIncident: Incident) => {
    setIncident(nextIncident);
    setWorkItem(toWorkItemCommon(nextIncident));
  }, []);

  const handleIncidentLoaded = useCallback((loadedIncident: unknown) => {
    syncIncidentSummary(loadedIncident as Incident);
  }, [syncIncidentSummary]);

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
      console.warn('[IncidentDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isFinite(numericId) || numericId <= 0) {
      return;
    }
    try {
      const incident = await IncidentAPI.getIncident(numericId);
      syncIncidentSummary(incident);
    } catch (err) {
      // WorkItemShell 只是这里的外层展示壳（专业字段仍然由下面完整功能的 IncidentDetail
      // 负责渲染/编辑），summary 拉取失败时不阻塞整页——workItem 保持 null，下面直接
      // 退化为原有的纯 IncidentDetail 展示，而不是让整页报错。IncidentDetail 组件自己
      // 内部另有一次完整的事件详情拉取 + 错误处理，这里的失败不影响那条路径。
      console.warn('[IncidentDetailPage] Failed to load WorkItem summary', err);
    }
  }, [numericId, syncIncidentSummary]);

  useEffect(() => {
    loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, loadSLA]);

  const detailAndTabs = (
    // 主详情组件保持不变——严重程度/影响范围/紧急程度/关联CI/升级状态等 Incident
    // 专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共
    // 身份信息，不重新实现这些逻辑。评论/历史现在由 WorkItemShell 自己的区块渲染
    // （见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md
    // §5.2），不再在这里重复一份。
    <IncidentDetail id={id} onIncidentLoaded={handleIncidentLoaded} />
  );
  const fallbackDetail = (
    <IncidentDetail
      id={id}
      fallbackActions={incident?.actions}
      onIncidentLoaded={handleIncidentLoaded}
    />
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

        {/* workItem 只有在事件摘要加载成功且满足 WorkItem 创建不变量时才非空。加载中、
            加载失败（见 loadWorkItemSummary 的 catch）或无效开发记录下 workItem 为 null，
            不用猜测的 ID 挂载 WorkItemShell。 */}
        {workItem && incident ? (
          <WorkItemShell
            workItem={workItem}
            sla={sla}
            actions={incident.actions ?? {}}
            showActionBar={false}
            onActionDispatch={async () => {}}
            professionalPanelSlot={detailAndTabs}
          />
        ) : (
          <>
            <Alert
              type="info"
              showIcon
              message="该事件尚未关联 WorkItem，评论/附件/历史/关联等协作能力暂不可用"
              style={{ marginBottom: 16 }}
            />
            {fallbackDetail}
          </>
        )}
      </div>
    </App>
  );
}
