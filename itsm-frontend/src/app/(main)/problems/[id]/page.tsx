'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { Alert, App, Button } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import ProblemDetail from '@/components/problem/ProblemDetail';
import { ProblemApi, type Problem } from '@/lib/api/problem-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';

// Shared identity is the authorized WorkItem ID and canonical number.
function toWorkItemCommon(problem: Problem): WorkItemCommon | null {
  if (!problem.workItemId || !problem.number) {
    // 缺少 workItemId 表示开发数据违反 WorkItem 创建不变量。拒绝用专业记录 ID 猜测
    // WorkItem 身份，避免把评论或附件挂到错误记录。
    return null;
  }
  return {
    id: problem.workItemId,
    number: problem.number,
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

  const [workItem, setWorkItem] = useState<WorkItemCommon | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);

  const syncProblemSummary = useCallback((nextProblem: Problem) => {
    setProblem(nextProblem);
    setWorkItem(toWorkItemCommon(nextProblem));
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
      console.warn('[ProblemDetailPage] Failed to load SLA', err);
      setSla(undefined);
    }
  }, []);

  const loadWorkItemSummary = useCallback(async () => {
    if (!Number.isFinite(numericId) || numericId <= 0) {
      return;
    }
    try {
      const problem = await ProblemApi.getProblem(numericId);
      syncProblemSummary(problem);
    } catch (err) {
      // WorkItemShell 只是这里的外层展示壳（专业字段仍然由下面完整功能的 ProblemDetail
      // 负责渲染/编辑），summary 拉取失败时不阻塞整页——workItem 保持 null，下面直接
      // 退化为原有的纯 ProblemDetail 展示，而不是让整页报错。ProblemDetail 组件自己
      // 内部另有一次完整的问题详情拉取 + 错误处理，这里的失败不影响那条路径。
      console.warn('[ProblemDetailPage] Failed to load WorkItem summary', err);
    }
  }, [numericId, syncProblemSummary]);

  useEffect(() => {
    loadWorkItemSummary();
  }, [loadWorkItemSummary]);

  useEffect(() => {
    if (workItem?.id) {
      void loadSLA(workItem.id);
    }
  }, [workItem?.id, loadSLA]);

  const detailAndTabs = (
    <>
      {/* 主详情组件保持不变——根因/临时解决方案/最终解决方案/影响范围等 Problem 专业
          字段、以及所有编辑动作都在这个组件内部完成，WorkItemShell 只包一层公共身份
          信息，不重新实现这些逻辑。 */}
      <ProblemDetail id={id} onProblemLoaded={syncProblemSummary} />


    </>
  );
  const fallbackDetail = (
    <ProblemDetail
      id={id}
      fallbackActions={problem?.actions}
      onProblemLoaded={syncProblemSummary}
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

        {/* workItem 只有在问题摘要加载成功且满足 WorkItem 创建不变量时才非空。加载中、
            加载失败（见 loadWorkItemSummary 的 catch）或无效开发记录下 workItem 为 null，
            不用猜测的 ID 挂载 WorkItemShell。 */}
        {workItem && problem ? (
          <WorkItemShell
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
              type="info"
              showIcon
              message="该问题尚未关联 WorkItem，评论/附件/历史/关联等协作能力暂不可用"
              style={{ marginBottom: 16 }}
            />
            {fallbackDetail}
          </>
        )}
      </div>
    </App>
  );
}
