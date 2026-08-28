'use client';

import React, { useEffect, useState, useCallback } from 'react';
import { App, Button, Card } from 'antd';
import { ArrowLeft, Link2 } from 'lucide-react';
import { useRouter, useParams } from 'next/navigation';
import ProblemDetail from '@/components/problem/ProblemDetail';
import ProblemAssociationsTab from '@/components/problem/ProblemAssociationsTab';
import { ProblemApi, type Problem } from '@/lib/api/problem-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { WorkItemShell } from '@/components/work-item/WorkItemShell';
import type { WorkItemCommon, WorkItemSLAState } from '@/components/work-item/WorkItemTypes';

// 把 Problem 响应映射成 WorkItemShell 的公共字段契约。同 Incident 迁移那次的模式
// （itsm-frontend/src/app/(main)/incidents/[id]/page.tsx）：id 用 workItemId
// （tickets.id，评论/附件/未来的 SLA 都挂在这个 ID 下），number 用 Problem 自己的展示
// 编号（后端 dto.ProblemResponse 目前没有专属的 problemNumber 字段，用 #id 兜底）。
function toWorkItemCommon(problem: Problem): WorkItemCommon | null {
  if (!problem.workItemId) {
    // 迁移前创建、还没跑 cmd/backfill_problem_work_item 回填的存量问题没有 workItemId，
    // 此时不渲染 WorkItemShell（下面 ProblemDetail 本身仍然完整可用），避免用一个假的
    // ID 挂载评论/附件占位组件。
    return null;
  }
  return {
    id: problem.workItemId,
    number: `#${problem.id}`,
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
  const [sla, setSla] = useState<WorkItemSLAState | undefined>(undefined);

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
      setWorkItem(toWorkItemCommon(problem));
    } catch (err) {
      // WorkItemShell 只是这里的外层展示壳（专业字段仍然由下面完整功能的 ProblemDetail
      // 负责渲染/编辑），summary 拉取失败时不阻塞整页——workItem 保持 null，下面直接
      // 退化为原有的纯 ProblemDetail 展示，而不是让整页报错。ProblemDetail 组件自己
      // 内部另有一次完整的问题详情拉取 + 错误处理，这里的失败不影响那条路径。
      console.warn('[ProblemDetailPage] Failed to load WorkItem summary', err);
    }
  }, [numericId]);

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
      <ProblemDetail id={id} />

      {/* 追加：关联（工单/事件/变更）。历史现在由 WorkItemShell 自己的区块渲染，不再
          在这里重复一份——见 docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md
          §5.2。这里仍然保留 ProblemAssociationsTab：它的数据走 ProblemApi 专属的关联接口，
          跟 WorkItemShell 的 TicketRelationCards（走 /tickets/:id/relations）不是同一份数据，
          删掉会丢功能，不是去重。 */}
      {Number.isFinite(numericId) && numericId > 0 && (
        <Card className="mt-4 rounded-lg shadow-sm border border-gray-200">
          <div className="flex items-center gap-1.5 mb-3 text-sm font-medium text-gray-700">
            <Link2 size={14} />
            关联（工单/事件/变更）
          </div>
          <ProblemAssociationsTab problemId={numericId} />
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

        {/* workItem 只有在问题摘要加载成功且带有 workItemId（Wave 2 迁移后创建/已跑过
            cmd/backfill_problem_work_item 回填）时才非空。加载中、加载失败（见
            loadWorkItemSummary 的 catch）、或存量未回填问题这三种情况下 workItem 都是
            null，直接退化为原有的纯 ProblemDetail 展示——不用 WorkItemShell 自己的
            loading/error 态挡住已经完整可用的 ProblemDetail。 */}
        {workItem ? (
          <WorkItemShell
            workItem={workItem}
            sla={sla}
            // Problem 各专业操作（调查/记录根因/提供解决方案/关闭……）仍由下面
            // ProblemDetail 内部既有的按钮 + ProblemApi 调用处理，尚未统一收口到
            // onActionDispatch——那需要后端补一个"动作可用性"契约（actions 参数目前也
            // 是空的），是比这次 WorkItem 迁移更大的后续改造，不在本任务范围内。与
            // Incident 迁移那次（incidents/[id]/page.tsx）的处理方式一致。
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
