'use client';

import React, { useState, useEffect, useRef } from 'react';
import { App, Alert, Button, Input, Modal, Spin, Tag } from 'antd';
import { CheckCircle, XCircle, Clock, ShieldCheck } from 'lucide-react';
import Link from 'next/link';
import { useApprovalTasks } from '@/components/approvals/useApprovalTasks';
import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';

interface PendingApprovalItem {
  id: number;
  title: string;
  requesterName: string;
  department: string;
  serviceType: string;
  createdAt?: string;
  description?: string;
}


function formatCreatedAt(dateString?: string): string {
  if (!dateString) return '-';
  const time = new Date(dateString);
  if (Number.isNaN(time.getTime())) return '-';
  return time.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function readTaskVariable(task: UserTask, key: string): string | undefined {
  const value = task.taskVariables?.[key];
  return typeof value === 'string' && value.trim() ? value : undefined;
}

function toPendingApproval(task: UserTask): PendingApprovalItem {
  return {
    id: task.id,
    title: task.taskName || task.taskDefinitionKey || '-',
    requesterName:
      readTaskVariable(task, 'requesterName') ||
      readTaskVariable(task, 'requester_name') ||
      '-',
    department: readTaskVariable(task, 'department') || '-',
    serviceType: task.businessType || task.taskPurpose || '流程审批',
    createdAt: task.createdTime,
    description: readTaskVariable(task, 'description'),
  };
}

export const ManagerPendingApprovals: React.FC = () => {
  const { message } = App.useApp();
  const resource = useApprovalTasks(4);
  const approvals = (resource.data ?? []).map(toPendingApproval);
  const loading = resource.loading;
  const error = resource.error;
  const pending = useRef(new Set<number>());
  const [actionLoading, setActionLoading] = useState<Record<number, boolean>>({});
  const [rejectingTaskId, setRejectingTaskId] = useState<number | null>(null);
  const [rejectComment, setRejectComment] = useState('');

  useEffect(() => {
    pending.current = new Set();
    setActionLoading({}); setRejectingTaskId(null); setRejectComment('');
  }, [resource.identity, resource.denied]);

  if (loading && !resource.ready) {
    return (
      <div className="mb-8 flex justify-center py-6">
        <Spin size="small" />
      </div>
    );
  }

  if (error && !resource.ready) {
    return (
      <div className="mb-8">
        <Alert
          type="error"
          showIcon
          title={`待办审批加载失败，请重试：${error}`}
          action={
            <Button size="small" onClick={() => resource.reload()}>
              重试
            </Button>
          }
        />
        <Link href="/approvals">查看全部审批</Link>
      </div>
    );
  }

  if (approvals.length === 0) {
    return null;
  }

  const handleDecision = async (
    id: number,
    action: 'approve' | 'reject',
    comment?: string
  ) => {
    if (!resource.ready || loading || error || pending.current.size > 0) return;
    pending.current.add(id);
    const current = resource.capture();
    const assertContext = () => { resource.assertContext(); if (!current()) throw new Error('审批上下文已变化'); };
    setActionLoading((prev) => ({ ...prev, [id]: true }));
    try {
      assertContext();
      await BPMNWorkflowApi.submitApprovalDecision(id, {
        action,
        ...(comment ? { comment } : {}),
      }, assertContext);
      if (!current()) return;
      message.success(action === 'approve' ? '审批决定已提交' : '驳回决定已提交');
      setRejectingTaskId(null);
      setRejectComment('');
      await resource.reload({ afterWrite: true });
    } catch (err) {
      if (!current()) return;
      if (resource.deny(err)) { pending.current.clear(); setActionLoading({}); setRejectingTaskId(null); }
      message.error(err instanceof Error ? err.message : '操作失败，请重试');
    } finally {
      if (current()) { pending.current.delete(id); setActionLoading((prev) => ({ ...prev, [id]: false })); }
    }
  };

  return (
    <div className="mb-8">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
        <div className="flex items-center gap-2">
          <ShieldCheck size={18} className="text-amber-600" />
          <h3 className="text-base font-bold text-foreground m-0">
            审批待办（预览 {approvals.length} 项）
          </h3>

        </div>
        <Link href="/approvals" className="text-sm text-primary-600">查看全部审批</Link>
      </div>
      {error && <Alert className="mb-3" type="error" showIcon title={error} action={<Button size="small" onClick={() => resource.reload()}>重试</Button>} />}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {approvals.map((item) => (
          <div
            key={item.id}
            className="p-5 rounded-[8px] bg-surface border border-amber-200/80 dark:border-amber-900/50 shadow-none transition-all flex flex-col justify-between"
          >
            <div>
              <div className="flex items-start justify-between gap-2">
                <span className="text-[13px] font-semibold text-foreground">
                  {item.title}
                </span>
                <span className="text-[12px] text-muted whitespace-nowrap flex items-center gap-1">
                  <Clock size={12} /> {formatCreatedAt(item.createdAt)}
                </span>
              </div>
              <div className="flex flex-wrap items-center gap-2 mt-2 text-xs text-muted">
                <span className="font-medium text-foreground">申请人：{item.requesterName}</span>
                <span>•</span>
                <span>{item.department}</span>
                <span>•</span>
                <Tag color="orange" className="mr-0 text-[10px]">{item.serviceType}</Tag>
              </div>
              {item.description && (
                <div className="mt-3 p-2.5 rounded-[8px] bg-raised text-[12px] text-muted">
                  {item.description}
                </div>
              )}
            </div>

            <div className="flex items-center justify-end gap-2 mt-4 pt-3 border-t border-border">
              <Button
                size="small"
                danger
                loading={actionLoading[item.id]}
                disabled={loading || !!error || Object.values(actionLoading).some(Boolean)}
                onClick={() => {
                  setRejectingTaskId(item.id);
                  setRejectComment('');
                }}
                icon={<XCircle size={14} />}
              >
                驳回
              </Button>
              <Button
                size="small"
                type="primary"
                loading={actionLoading[item.id]}
                disabled={loading || !!error || Object.values(actionLoading).some(Boolean)}
                onClick={() => handleDecision(item.id, 'approve')}
                className=""
                icon={<CheckCircle size={14} />}
              >
                同意批准
              </Button>
            </div>
          </div>
        ))}
      </div>
      <Modal
        title="填写驳回意见"
        open={rejectingTaskId !== null && resource.ready && approvals.some(item => item.id === rejectingTaskId)}
        okText="确认驳回"
        cancelText="取消"
        closable={!Object.values(actionLoading).some(Boolean)}
        maskClosable={!Object.values(actionLoading).some(Boolean)}
        cancelButtonProps={{ disabled: Object.values(actionLoading).some(Boolean) }}
        confirmLoading={rejectingTaskId !== null && Boolean(actionLoading[rejectingTaskId])}
        okButtonProps={{ danger: true, disabled: !rejectComment.trim() || loading || !!error }}
        onCancel={() => {
          if (pending.current.size > 0) return;
          setRejectingTaskId(null);
          setRejectComment('');
        }}
        onOk={() => {
          if (rejectingTaskId !== null) {
            handleDecision(rejectingTaskId, 'reject', rejectComment.trim());
          }
        }}
      >
        <Input.TextArea
          aria-label="审批意见"
          value={rejectComment}
          onChange={(event) => setRejectComment(event.target.value)}
          placeholder="请输入驳回原因"
          rows={4}
        />
      </Modal>
    </div>
  );
};
