'use client';
import { useDetailRefresh, useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';

import React, { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Modal } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import Link from 'next/link';
import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';

// Wire identity mapping only; lifecycle and authorization remain in BPMN.
const businessTypes: Record<string, string> = {
  generic: 'generic', service_request_item: 'service_request_item', incident: 'incident',
  problem: 'problem', change_request: 'change_request', catalog_task: 'catalog_task',
};
const terminal = new Set(['completed', 'cancelled']);
const statuses: Record<string, string> = {
  created: '待处理', assigned: '已分配', started: '处理中', pending: '等待处理',
  delegated: '已委派', suspended: '已暂停', completed: '已完成', cancelled: '已取消',
};
const assignmentStates: Record<UserTask['assignmentState'], string> = {
  assigned: '已由工单分配',
  unassigned: '等待工单分配处理人',
  unavailable: '处理人当前不可用',
  terminal: '历史处理记录',
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
      tasks.push(task);
    }
    if (page * pageSize >= result.total) return tasks;
  }
}

function taskSession(state = useAuthStore.getState()) {
  return JSON.stringify([state.user?.id, state.user?.tenantId, state.currentTenant?.id,
    state.isAuthenticated, state.currentTenant?.status, state.user?.permissions]);
}

function TaskList({ tasks, busy, readLocked, onClaim, onComplete }: {
  tasks: UserTask[];
  busy: boolean;
  readLocked: boolean;
  onClaim: (task: UserTask) => void;
  onComplete: (task: UserTask) => void;
}) {
  return (
    <ul className="space-y-3">
      {tasks.map(task => (
        <li key={task.id} className="rounded-[8px] border border-border bg-raised p-3 text-sm text-foreground space-y-1 break-words">
          <div className="font-medium">{task.taskName || '未命名任务'}</div>
          <div>状态：{statuses[task.status] ?? `未知状态（${task.status || '-'}）`}</div>
          {task.assigneeSource === 'work_item_assignee' ? (
            <>
              <div>分配状态：<span>{terminal.has(task.status) && task.assignmentState === 'unavailable' ? '历史处理人记录不可用' : assignmentStates[task.assignmentState]}</span></div>
              {task.responsibleUserId > 0 && <div>处理人用户 ID：{task.responsibleUserId}</div>}
              {task.assignmentState === 'terminal' && task.actorId > 0 && <div>实际操作人用户 ID：{task.actorId}</div>}
            </>
          ) : (
            <div>任务处理人：<span>{task.assignee || '尚未指定个人'}</span></div>
          )}
          {task.callbackBlock && <p className="text-xs text-warning">{task.callbackBlock.reason}</p>}
          {task.uiActions?.reason && task.taskPurpose !== 'approval' && <p className="text-xs text-muted">{task.uiActions.reason}</p>}
          <div className="flex flex-wrap gap-2">
            {task.uiActions?.claim && <Button size="small" disabled={busy || readLocked} onClick={() => onClaim(task)}>领取任务</Button>}
            {task.uiActions?.complete && <Button size="small" disabled={busy || readLocked} onClick={() => onComplete(task)}>完成任务</Button>}
          </div>
          {task.taskPurpose === 'approval' && <Link className="inline-block text-primary" href="/approvals">前往审批中心</Link>}
        </li>
      ))}
    </ul>
  );
}

function GroupToggle({ expanded, count, label, controls, onToggle }: {
  expanded: boolean;
  count: number;
  label: string;
  controls: string;
  onToggle: () => void;
}) {
  return (
    <button type="button" aria-expanded={expanded} aria-controls={controls} onClick={onToggle}
      className="flex w-full items-center justify-between gap-3 rounded-[8px] border border-border bg-raised px-3 py-2 text-left text-sm font-semibold text-foreground hover:bg-selected focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary">
      <span>{label}（{count}）</span>
      <span aria-hidden="true">{expanded ? '收起' : '展开'}</span>
    </button>
  );
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
  const commandPending = useRef(false);
  const refresh = useDetailRefresh();
  useDetailRefreshEntry({ key: 'process-tasks', label: '流程任务', reload: resource.reload, isWriting: () => commandPending.current });
  const [selected, setSelected] = useState<UserTask | null>(null);
  const [completionNote, setCompletionNote] = useState('');
  const [mutationError, setMutationError] = useState<string>();
  const [submitted, setSubmitted] = useState(false);
  const [updateFailed, setUpdateFailed] = useState(false);
  const [activeExpanded, setActiveExpanded] = useState(false);
  const [activeExpansionInitialized, setActiveExpansionInitialized] = useState(false);
  const [historyExpanded, setHistoryExpanded] = useState(false);
  const perform = async (task: UserTask, action: 'claim' | 'complete') => {
    if (locked.current || resource.loading || resource.error || !task.uiActions?.[action]) return;
    const note = completionNote.trim();
    if (action === 'complete' && task.uiActions.completionNoteRequired && (!note || Array.from(note).length > 4000)) {
      setMutationError('请填写 1–4000 字的处理说明');
      return;
    }
    const current = resource.capture();
    const assertContext = () => {
      if (!current() || taskSession() !== session) throw new Error('会话或工单已变化，请重新打开任务');
    };
    locked.current = true;
    commandPending.current = true;
    setBusy(true);
    setMutationError(undefined);
    setSubmitted(false);
    setUpdateFailed(false);
    try {
      try {
        assertContext();
        if (action === 'claim') await BPMNWorkflowApi.claimTask(task.id, assertContext);
        else await BPMNWorkflowApi.completeTask(task.id, task.uiActions.completionNoteRequired ? { variables: { workItemCompletionNote: note } } : {}, assertContext);
        assertContext();
      } catch (error) {
        if (!current() || taskSession() !== session) return;
        if (resource.deny(error)) setSelected(null);
        setMutationError(error instanceof Error ? error.message : '任务操作失败，请重试');
        return;
      }
      // The command is confirmed. Keep the UI locked until its reads settle,
      // while allowing the coordinator to read this resource after the write.
      commandPending.current = false;
      setSelected(null);
      setSubmitted(true);
      try {
        if (refresh) {
          const report = await refresh.refresh(['process-tasks', 'ticket', 'approval-decisions'], { afterWrite: true });
          if (current() && taskSession() === session) setUpdateFailed(report.failed.length > 0);
        } else {
          const result = await resource.reload({ afterWrite: true });
          assertContext();
          setUpdateFailed(result.status === 'error');
          await onTaskChange?.();
        }
      } catch (error) {
        if (current() && taskSession() === session) {
          if (resource.deny(error)) setSelected(null);
          setUpdateFailed(true);
        }
      }
    } finally {
      commandPending.current = false;
      locked.current = false; setBusy(false);
    }
  };
  const tasks = resource.data ?? [];
  const activeTasks = tasks.filter(task => !terminal.has(task.status));
  const historyTasks = tasks.filter(task => terminal.has(task.status));
  const initiallyExpanded = activeTasks.some(task => task.uiActions?.claim || task.uiActions?.complete);
  const shownActiveExpanded = activeExpansionInitialized ? activeExpanded : initiallyExpanded;

  useEffect(() => {
    if (resource.denied) setSelected(null);
  }, [resource.denied]);

  useEffect(() => {
    if (!activeExpansionInitialized && resource.ready && !resource.error) {
      setActiveExpanded(initiallyExpanded);
      setActiveExpansionInitialized(true);
    }
  }, [activeExpansionInitialized, initiallyExpanded, resource.error, resource.ready]);

  return (
    <section aria-label="当前流程任务" className="bg-surface rounded-[8px] border border-border p-5 shadow-none space-y-3 text-foreground">
      <h2 className="text-sm font-bold text-foreground">当前流程任务</h2>
      <p className="text-xs text-muted">显示当前账号有权查看的任务，任务处理人由流程配置决定。</p>
      <DetailReadState error={resource.error} loading={resource.loading || busy} reload={async () => { if (!locked.current) await resource.reload(); }} />
      {resource.ready && tasks.some(task => task.callbackBlock) && <Alert type="warning" showIcon title="流程执行已阻塞" description={<ul>{Array.from(new Set(tasks.flatMap(task => task.callbackBlock ? [task.callbackBlock.reason] : []))).map(reason => <li key={reason}>{reason}</li>)}</ul>} />}
      {mutationError && !selected && <Alert type="error" showIcon title={mutationError} />}
      {submitted && <p role="status">任务操作已提交，请以刷新后的状态为准。</p>}
      {updateFailed && <Alert type="warning" showIcon title="操作已完成，首次更新时部分数据读取失败" />}
      {resource.loading && !resource.ready && <p role="status">流程任务加载中...</p>}
      {resource.ready && !resource.loading && !resource.error && activeTasks.length === 0 && (
        <p className="text-sm text-muted">当前账号暂无可见的活动任务</p>
      )}
      {resource.ready && activeTasks.length > 0 && (
        <div className="space-y-3">
          <GroupToggle expanded={shownActiveExpanded} count={activeTasks.length} label="当前任务" controls="current-process-tasks"
            onToggle={() => { setActiveExpansionInitialized(true); setActiveExpanded(!shownActiveExpanded); }} />
          {shownActiveExpanded && <div id="current-process-tasks"><TaskList tasks={activeTasks} busy={busy}
            readLocked={resource.loading || !!resource.error} onClaim={task => void perform(task, 'claim')}
            onComplete={task => { setMutationError(undefined); setCompletionNote(''); setSelected(task); }} /></div>}
        </div>
      )}
      {resource.ready && historyTasks.length > 0 && (
        <div className="space-y-3 border-t border-border pt-3">
          <GroupToggle expanded={historyExpanded} count={historyTasks.length} label="历史任务" controls="historical-process-tasks"
            onToggle={() => setHistoryExpanded(value => !value)} />
          {historyExpanded && <div id="historical-process-tasks"><TaskList tasks={historyTasks} busy={busy}
            readLocked={resource.loading || !!resource.error} onClaim={task => void perform(task, 'claim')}
            onComplete={task => { setMutationError(undefined); setCompletionNote(''); setSelected(task); }} /></div>}
        </div>
      )}
      <Modal title="完成流程任务" open={!!selected} okText="确认完成" cancelText="取消"
        confirmLoading={busy} closable={!busy} maskClosable={!busy} keyboard={!busy}
        cancelButtonProps={{ disabled: busy }} okButtonProps={{ disabled: resource.loading || !!resource.error }}
        onCancel={() => { if (!locked.current) setSelected(null); }} onOk={() => selected && void perform(selected, 'complete')}>
        <p>确认已完成「{selected?.taskName}」？提交后将按既有流程继续处理。</p>
        {selected?.uiActions?.completionNoteRequired && <div className="my-3 space-y-2">
          <label htmlFor="task-completion-note">处理说明</label>
          <Input.TextArea id="task-completion-note" required rows={4} value={completionNote} disabled={busy}
            onChange={event => setCompletionNote(event.target.value)} aria-describedby="task-completion-note-help" />
          <p id="task-completion-note-help" className="text-xs text-muted">请填写 1–4000 字，记录本次处理内容和结果。</p>
        </div>}
        {mutationError && <Alert type="error" showIcon title={mutationError} />}
      </Modal>
    </section>
  );
}
