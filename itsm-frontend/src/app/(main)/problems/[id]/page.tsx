'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { Alert, App, Button } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import ProblemDetail from '@/components/problem/ProblemDetail';
import { ProblemApi, type Problem } from '@/lib/api/problem-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { workItemIdentity } from '@/components/work-item/identity';
import { assignProblemWorkItem } from '@/lib/api/workitem-assignment';
import { useAssignmentCandidates } from '@/components/work-item/useAssignmentCandidates';
import type { AssignmentInput } from '@/components/work-item/WorkItemAssignment';
import { mapWorkItemSLA } from '@/components/work-item/mapWorkItemSLA';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';

function toWorkItemCommon(problem: Problem): WorkItemCommon {
  return {
    ...workItemIdentity(problem),
    recordClass: 'problem',
    title: problem.title,
    status: problem.status,
    priority: problem.priority,
    // 后端 dto.ProblemResponse 实际返回的创建人字段是 createdBy（reporterId 在后端不
    // 存在，是这个前端类型里的历史遗留字段名不匹配，见 problem-api.ts 里的注释）。
    requesterId: problem.createdBy ?? problem.reporterId ?? 0,
    assigneeId: problem.assigneeId,
    createdAt: problem.createdAt || '',
    updatedAt: problem.updatedAt || '',
  };
}

export default function ProblemDetailPage() {
  const router = useRouter();
  const params = useParams();
  const id = params?.id as string;
  const numericId = Number(id);

  const [identityError, setIdentityError] = useState<string | null>(null);
  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);
  const [panelRevision, setPanelRevision] = useState(0);
  const directory = useAssignmentCandidates(Boolean(problem?.actions?.assign?.allowed));

  const syncProblemSummary = useCallback((nextProblem: Problem) => {
    try {
      const common = toWorkItemCommon(nextProblem);
      setProblem(nextProblem);
      setWorkItem(common);
      setIdentityError(null);
    } catch (error) {
      setWorkItem(null);
      setIdentityError(error instanceof Error ? error.message : 'WorkItem 身份或版本无效');
    }
  }, []);

  const refreshAssignment = useCallback(async () => {
    const latest = await ProblemApi.getProblem(numericId);
    const identity = workItemIdentity(latest);
    syncProblemSummary(latest);
    setPanelRevision(value => value + 1);
    return {
      version: identity.version,
      currentAssigneeId: latest.assigneeId,
      allowed: latest.actions?.assign?.allowed === true,
      disabledReason: latest.actions?.assign?.reason,
    };
  }, [numericId, syncProblemSummary]);
  const submitAssignment = async (input: AssignmentInput) => {
    await assignProblemWorkItem(numericId, input);
    await refreshAssignment();
  };

  const loadSLA = useCallback(async (workItemId: number) => {
    try {
      const data = await TicketApi.getTicketSLA(workItemId);
      setSla(mapWorkItemSLA(data));
    } catch (err) {
      console.warn('[ProblemDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isInteger(numericId) || numericId <= 0) {
      setIdentityError('专业记录身份无效');
      return;
    }
    try {
      const problem = await ProblemApi.getProblem(numericId);
      syncProblemSummary(problem);
    } catch (err) {
      setWorkItem(null);
      setIdentityError('无法加载权威身份和版本，请刷新详情');
    }
  }, [numericId, syncProblemSummary]);

  useEffect(() => {
    loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, workItem?.version, loadSLA]);

  const detailAndTabs = (
    <>
      {/* 主详情组件保持不变——根因/临时解决方案/最终解决方案/影响范围等 Problem 专业
          字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共身份
          信息，不重新实现这些逻辑。 */}
      <ProblemDetail key={panelRevision} id={id} onProblemLoaded={syncProblemSummary} />
    </>
  );

  return (
    <App>
      <div style={{ padding: 24 }}>
        <div style={{ marginBottom: 16 }}>
          <Button
            type='link'
            icon={<ArrowLeft />}
            onClick={() => router.back()}
            style={{ paddingLeft: 0, color: '#666' }}
          >
            返回列表
          </Button>
        </div>

        {/* workItem 只有在问题摘要加载成功且满足 WorkItem 创建不变量时才非空。加载中、
            加载失败（见 loadWorkItemSummary 的 catch）或无效开发记录下 workItem 为 null，
            不用猜测的 ID 挂载 WorkItemShell。 */}
        {workItem && problem?.id === numericId ? (
          <WorkItemShell
            assignment={{
              currentAssigneeId: workItem.assigneeId,
              version: workItem.version,
              allowed: problem.actions?.assign?.allowed === true && !directory.error,
              disabledReason: directory.error || problem.actions?.assign?.reason,
              candidates: directory.candidates,
              submit: submitAssignment,
              refresh: refreshAssignment,
            }}
            workItem={workItem}
            sla={sla}
            actions={problem.actions ?? {}}
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
