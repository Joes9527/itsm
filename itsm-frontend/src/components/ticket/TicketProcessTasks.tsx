'use client';

import React, { useRef, useState } from 'react';
import { Alert, Button, Modal } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
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

function taskSession(state = useAuthStore.getState()) {
  return JSON.stringify([state.user?.id, state.user?.tenantId, state.currentTenant?.id,
    state.isAuthenticated, state.currentTenant?.status, state.user?.permissions]);
}

export function TicketProcessTasks(props: { ticketId: number; recordClass: string; onTaskChange?: () => Promise<void> }) {
  const identity = useAuthStore(taskSession);
  return <ProcessTasksPanel key={`${identity}:${props.recordClass}:${props.ticketId}`} {...props} session={identity} />;
}

function ProcessTasksPanel({ ticketId, recordClass, onTaskChange, session }: {
  ticketId: number; recordClass: string; onTaskChange?: () => Promise<void>; session: string;
}) {
  const resource = useDetailResource(`${recordClass}:${ticketId}`, () => readTasks(ticketId, recordClass), tasks => tasks.length);
  const [busy, setBusy] = useState(false);
  const locked = useRef(false);
  const [selected, setSelected] = useState<UserTask | null>(null);
  const [mutationError, setMutationError] = useState<string>();
  const [submitted, setSubmitted] = useState(false);
  const perform = async (task: UserTask, action: 'claim' | 'complete') => {
    if (locked.current || resource.loading || resource.error || !task.uiActions?.[action]) return;
    const current = resource.capture();
    const assertContext = () => {
      if (!current() || taskSession() !== session) throw new Error('会话或工单已变化，请重新打开任务');
    };
    locked.current = true;
    setBusy(true);
    setMutationError(undefined);
    setSubmitted(false);
    try {
      assertContext();
      if (action === 'claim') await BPMNWorkflowApi.claimTask(task.id, assertContext);
      else await BPMNWorkflowApi.completeTask(task.id, {}, assertContext);
      assertContext();
      setSelected(null);
      setSubmitted(true);
      await resource.reload();
      assertContext();
      await onTaskChange?.();
    } catch (error) {
      if (!current() || taskSession() !== session) return;
      if (resource.deny(error)) setSelected(null);
      setMutationError(error instanceof Error ? error.message : '任务操作失败，请重试');
    } finally {
      locked.current = false; setBusy(false);
    }
  };
  const tasks = resource.data ?? [];
  return (
    <section aria-label="当前流程任务" className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-3">
      <h2 className="text-sm font-bold text-slate-800">当前流程任务</h2>
      <p className="text-xs text-slate-500">显示当前账号有权查看的任务，任务处理人由流程配置决定。</p>
      <DetailReadState error={resource.error} loading={resource.loading || busy} reload={async () => { if (!locked.current) await resource.reload(); }} />
      {mutationError && !selected && <Alert type="error" showIcon title={mutationError} />}
      {submitted && <p role="status">任务操作已提交，请以刷新后的状态为准。</p>}
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
            {task.uiActions?.reason && task.taskPurpose !== 'approval' && <p className="text-xs text-slate-500">{task.uiActions.reason}</p>}
            <div className="flex flex-wrap gap-2">
              {task.uiActions?.claim && <Button size="small" disabled={busy || resource.loading || !!resource.error} onClick={() => void perform(task, 'claim')}>领取任务</Button>}
              {task.uiActions?.complete && <Button size="small" disabled={busy || resource.loading || !!resource.error} onClick={() => { setMutationError(undefined); setSelected(task); }}>完成任务</Button>}
            </div>
            {task.taskPurpose === 'approval'  && <Link className="inline-block text-blue-600" href="/approvals">前往审批中心</Link>}
          </li>
        ))}
      </ul>
      <Modal title="完成流程任务" open={!!selected} okText="确认完成" cancelText="取消"
        confirmLoading={busy} closable={!busy} maskClosable={!busy} keyboard={!busy}
        cancelButtonProps={{ disabled: busy }} okButtonProps={{ disabled: resource.loading || !!resource.error }}
        onCancel={() => { if (!locked.current) setSelected(null); }} onOk={() => selected && void perform(selected, 'complete')}>
        <p>确认已完成「{selected?.taskName}」？提交后将按既有流程继续处理。</p>
        {mutationError && <Alert type="error" showIcon title={mutationError} />}
      </Modal>
    </section>
  );
}
