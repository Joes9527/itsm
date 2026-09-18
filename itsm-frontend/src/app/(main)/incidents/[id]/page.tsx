'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { Alert, App, Button } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import IncidentDetail from '@/components/incident/IncidentDetail';
import { IncidentAPI, type Incident } from '@/lib/api/incident-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { workItemIdentity } from '@/components/work-item/identity';
import { assignIncidentWorkItem } from '@/lib/api/workitem-assignment';
import { useAssignmentCandidates } from '@/components/work-item/useAssignmentCandidates';
import type { AssignmentInput } from '@/components/work-item/WorkItemAssignment';
import { mapWorkItemSLA } from '@/components/work-item/mapWorkItemSLA';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';

function toWorkItemCommon(incident: Incident): WorkItemCommon {
  return {
    ...workItemIdentity(incident),
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

  const [identityError, setIdentityError] = useState<string | null>(null);
  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [incident, setIncident] = useState<Incident | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);
  const [panelRevision, setPanelRevision] = useState(0);
  const directory = useAssignmentCandidates(Boolean(incident?.actions?.assign?.allowed));

  const syncIncidentSummary = useCallback((nextIncident: Incident) => {
    try {
      const common = toWorkItemCommon(nextIncident);
      setIncident(nextIncident);
      setWorkItem(common);
      setIdentityError(null);
    } catch (error) {
      setWorkItem(null);
      setIdentityError(error instanceof Error ? error.message : 'WorkItem 身份或版本无效');
    }
  }, []);

  const handleIncidentLoaded = useCallback(
    (loadedIncident: unknown) => {
      syncIncidentSummary(loadedIncident as Incident);
    },
    [syncIncidentSummary]
  );

  const refreshAssignment = useCallback(async () => {
    const latest = await IncidentAPI.getIncident(numericId);
    const identity = workItemIdentity(latest);
    syncIncidentSummary(latest);
    setPanelRevision(value => value + 1);
    return {
      version: identity.version,
      currentAssigneeId: latest.assigneeId,
      allowed: latest.actions?.assign?.allowed === true,
      disabledReason: latest.actions?.assign?.reason,
    };
  }, [numericId, syncIncidentSummary]);
  const submitAssignment = async (input: AssignmentInput) => {
    await assignIncidentWorkItem(numericId, input);
    await refreshAssignment();
  };

  const loadSLA = useCallback(async (workItemId: number) => {
    try {
      const data = await TicketApi.getTicketSLA(workItemId);
      setSla(mapWorkItemSLA(data));
    } catch (err) {
      console.warn('[IncidentDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isInteger(numericId) || numericId <= 0) {
      setIdentityError('专业记录身份无效');
      return;
    }
    try {
      const incident = await IncidentAPI.getIncident(numericId);
      syncIncidentSummary(incident);
    } catch (err) {
      setWorkItem(null);
      setIdentityError('无法加载权威身份和版本，请刷新详情');
    }
  }, [numericId, syncIncidentSummary]);

  useEffect(() => {
    loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, workItem?.version, loadSLA]);

  const detailAndTabs = (
    // 主详情组件保持不变——严重程度/影响范围/紧急程度/关联CI/升级状态等 Incident
    // 专业字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共
    // 身份信息，不重新实现这些逻辑。评论/历史现在由 WorkItemShell 自己的区块渲染
    // （见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md
    // §5.2），不再在这里重复一份。
    <IncidentDetail key={panelRevision} id={id} onIncidentLoaded={handleIncidentLoaded} />
  );

  return (
    <App>
      <div style={{ padding: 24 }}>
        <div style={{ marginBottom: 16 }}>
          <Button
            type='link'
            icon={<ArrowLeft />}
            onClick={() => router.back()}
            style={{ paddingLeft: 0, color: 'var(--color-text-secondary)' }}
          >
            返回列表
          </Button>
        </div>

        {/* workItem 只有在事件摘要加载成功且满足 WorkItem 创建不变量时才非空。加载中、
            加载失败（见 loadWorkItemSummary 的 catch）或无效开发记录下 workItem 为 null，
            不用猜测的 ID 挂载 WorkItemShell。 */}
        {workItem && incident?.id === numericId ? (
          <WorkItemShell
            assignment={{
              currentAssigneeId: workItem.assigneeId,
              version: workItem.version,
              allowed: incident.actions?.assign?.allowed === true && !directory.error,
              disabledReason: directory.error || incident.actions?.assign?.reason,
              candidates: directory.candidates,
              submit: submitAssignment,
              refresh: refreshAssignment,
            }}
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
              type={identityError ? 'error' : 'info'}
              showIcon
              message={identityError ?? '正在加载权威身份和版本…'}
              style={{ marginBottom: 16 }}
            />
          </>
        )}
      </div>
    </App>
  );
}
