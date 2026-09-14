'use client';

import React from 'react';
import Link from 'next/link';
import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';

// Wire identity mapping only; lifecycle and authorization remain in BPMN.
const businessTypes: Record<string, string> = {
  generic: 'ticket', service_request_item: 'service_request', incident: 'incident',
  problem: 'problem', change_request: 'change',
};
const terminal = new Set(['completed', 'cancelled']);
const statuses: Record<string, string> = {
  created: '待处理', assigned: '已分配', started: '处理中', pending: '等待处理',
  delegated: '已委派', suspended: '已暂停',
};

async function readTasks(ticketId: number, recordClass: string): Promise<UserTask[]> {
  const businessType = businessTypes[recordClass];
  if (!businessType) throw new Error('暂不支持此工单类型的流程任务查询');
  const tasks: UserTask[] = [];
  const seen = new Set<number>();
  const pageSize = 100;
  for (let page = 1; ; page++) {
    const result = await BPMNWorkflowApi.listUserTasks({ businessType, businessId: ticketId, page, pageSize });
    if (!Array.isArray(result.items) || !Number.isSafeInteger(result.total) || result.total < 0 ||
        result.page !== page || result.pageSize !== pageSize) throw new Error('任务分页响应异常，请重试');
    const expected = Math.max(0, Math.min(pageSize, result.total - (page - 1) * pageSize));
    if (result.items.length !== expected) throw new Error('任务分页不完整，请重试');
    for (const task of result.items) {
      if (task.businessType !== businessType || task.businessId !== ticketId) throw new Error('任务关联不一致，请重试');
      if (!Number.isSafeInteger(task.id) || task.id <= 0 || seen.has(task.id)) throw new Error('任务分页重复或无效，请重试');
      seen.add(task.id);
      if (!terminal.has(task.status)) tasks.push(task);
    }
    if (page * pageSize >= result.total) return tasks;
  }
}

/** Shows the caller's task scope, not a complete process diagram. */
export function TicketProcessTasks({ ticketId, recordClass }: { ticketId: number; recordClass: string }) {
  const resource = useDetailResource(`${recordClass}:${ticketId}`, () => readTasks(ticketId, recordClass), tasks => tasks.length);
  const tasks = resource.data ?? [];
  return (
    <section aria-label="当前流程任务" className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-3">
      <h2 className="text-sm font-bold text-slate-800">当前流程任务</h2>
      <p className="text-xs text-slate-500">显示当前账号有权查看的任务，任务处理人由流程配置决定。</p>
      <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />
      {resource.loading && !resource.ready && <p role="status">流程任务加载中...</p>}
      {resource.ready && !resource.loading && !resource.error && tasks.length === 0 && (
        <p className="text-sm text-slate-500">当前账号暂无可见的活动任务</p>
      )}
      <ul className="space-y-3">
        {tasks.map(task => (
          <li key={task.id} className="rounded-lg border border-slate-100 p-3 text-sm space-y-1 break-words">
            <div className="font-medium">{task.taskName || '未命名任务'}</div>
            <div>状态：{statuses[task.status] ?? `未知状态（${task.status || '-'}）`}</div>
            <div>任务处理人：<span>{task.assignee || '尚未指定个人'}</span></div>
            {task.taskPurpose === 'approval' && <Link className="inline-block text-blue-600" href="/approvals">前往审批中心</Link>}
          </li>
        ))}
      </ul>
    </section>
  );
}
