'use client';

import { usePublishedWorkItemProcessTasks } from './WorkItemProcessTasksContext';
import { terminalTaskStatuses } from './workItemProcessTasks';
import type { UserTask } from '@/lib/api/bpmn-workflow-api';

/**
 * 审批决策为空时，说清楚"为什么没有"，而不是只显示一句「暂无审批决策记录」。
 *
 * 空态有五种互不相同的成因，用户看到的却完全一样：工单还没进流程、流程停在审批之前的节点
 * （例如等派单）、审批节点还没处理、审批节点处理过但决策没落库、流程不经过审批。前两种是
 * 正常状态，第四种是数据缺陷——把它们混成同一句话，用户无法判断该等待还是该报障。
 *
 * 判断依据只有流程任务这一份数据，不猜测流程定义里有没有审批节点：尚未创建的节点在任务表里
 * 不存在，"没有 approval 任务"既可能是流程不经过审批，也可能是还没走到，必须用实例的推进
 * 位置来区分。
 */
export function approvalAbsenceReason(tasks: UserTask[]): string {
  const active = tasks.filter(task => !terminalTaskStatuses.has(task.status));
  const approvalTasks = tasks.filter(task => task.taskPurpose === 'approval');

  if (tasks.length === 0) {
    return '该工单尚未启动流程实例，因此没有审批决策记录。';
  }

  const activeApproval = approvalTasks.find(task => !terminalTaskStatuses.has(task.status));
  if (activeApproval) {
    return `流程已到达审批节点「${activeApproval.taskName || '未命名任务'}」，尚未处理；处理后会在此显示审批决策记录。`;
  }

  if (active.length > 0) {
    // 还有活动任务且都不是审批节点——流程停在审批之前，正常状态，不是缺陷。
    const current = active[0];
    const waiting = current.assignmentState === 'unassigned' ? '（等待分配处理人）'
      : current.assignmentState === 'unavailable' ? '（处理人当前不可用）' : '';
    return `流程当前停在「${current.taskName || '未命名任务'}」${waiting}，尚未到达审批节点，因此还没有审批决策记录。`;
  }

  if (approvalTasks.length > 0) {
    // 审批节点已经全部结束却没有决策记录：这是数据缺陷，必须说成异常让用户可报告，
    // 不能和"还没审批"共用同一句话。
    return '流程中的审批节点已全部结束，但没有对应的审批决策记录，请联系管理员检查流程实例的决策写入。';
  }

  return '本工单的流程未经过审批节点，因此没有审批决策记录。';
}

/**
 * 空态说明，取自 TicketProcessTasks 已确认的那一次流程任务读取。
 * 任务尚未读出、读取失败或详情页未挂载读取方时返回 undefined——宁可不说原因，
 * 也不要基于读不到的数据编造原因。
 */
export function useApprovalAbsenceReason(): string | undefined {
  const tasks = usePublishedWorkItemProcessTasks();
  return tasks ? approvalAbsenceReason(tasks) : undefined;
}
