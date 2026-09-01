'use client';

import React, { useState, useEffect, useCallback } from 'react';
import { App, Alert, Button, Input, Modal, Spin, Tag } from 'antd';
import { CheckCircle, XCircle, Clock, ShieldCheck } from 'lucide-react';
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

const PENDING_TASK_STATUSES = ['created', 'assigned', 'started', 'pending'] as const;
const APPROVAL_PAGE_SIZE = 4;

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

async function listApprovalTasksByStatus(status: string): Promise<UserTask[]> {
  const approvals: UserTask[] = [];
  let page = 1;

  while (approvals.length < APPROVAL_PAGE_SIZE) {
    const result = await BPMNWorkflowApi.listUserTasks({
      status,
      page,
      pageSize: APPROVAL_PAGE_SIZE,
    });
    approvals.push(
      ...result.items.filter((task) => task.taskPurpose?.toLowerCase() === 'approval')
    );

    if (
      result.items.length < APPROVAL_PAGE_SIZE ||
      page * APPROVAL_PAGE_SIZE >= result.total
    ) {
      break;
    }
    page += 1;
  }

  return approvals.slice(0, APPROVAL_PAGE_SIZE);
}

export const ManagerPendingApprovals: React.FC = () => {
  const { message } = App.useApp();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [approvals, setApprovals] = useState<PendingApprovalItem[]>([]);
  const [actionLoading, setActionLoading] = useState<Record<number, boolean>>({});
  const [rejectingTaskId, setRejectingTaskId] = useState<number | null>(null);
  const [rejectComment, setRejectComment] = useState('');

  // 是否有待办完全由后端 BPMN 任务候选人查询结果决定——不再用本地角色
  // 白名单前置判断，避免和 persona-config.ts 的角色配置各自维护、逐渐漂移不一致。
  const fetchApprovals = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const pages = await Promise.all(
        PENDING_TASK_STATUSES.map(listApprovalTasksByStatus)
      );
      const uniqueTasks = new Map<number, UserTask>();
      pages.forEach((tasks) => {
        tasks.forEach((task) => {
          uniqueTasks.set(task.id, task);
        });
      });
      const items = Array.from(uniqueTasks.values())
        .sort((left, right) => {
          const leftTime = left.createdTime ? Date.parse(left.createdTime) : 0;
          const rightTime = right.createdTime ? Date.parse(right.createdTime) : 0;
          return rightTime - leftTime;
        })
        .slice(0, 4)
        .map(toPendingApproval);
      setApprovals(items);
    } catch (e) {
      setError('待办审批加载失败，请重试');
      setApprovals([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchApprovals();
  }, [fetchApprovals]);

  if (loading) {
    return (
      <div className="mb-8 flex justify-center py-6">
        <Spin size="small" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="mb-8">
        <Alert
          type="error"
          showIcon
          title={error}
          action={
            <Button size="small" onClick={() => fetchApprovals()}>
              重试
            </Button>
          }
        />
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
    setActionLoading((prev) => ({ ...prev, [id]: true }));
    try {
      await BPMNWorkflowApi.submitApprovalDecision(id, {
        action,
        ...(comment ? { comment } : {}),
      });
      message.success(action === 'approve' ? '审批已通过，已自动流转至下一环节' : '已成功驳回该申请');
      setApprovals((prev) => prev.filter((item) => item.id !== id));
      setRejectingTaskId(null);
      setRejectComment('');
    } catch (err) {
      message.error('操作失败，请重试');
    } finally {
      setActionLoading((prev) => ({ ...prev, [id]: false }));
    }
  };

  return (
    <div className="mb-8">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <ShieldCheck size={18} className="text-amber-600" />
          <h3 className="text-base font-bold text-slate-800 dark:text-slate-100 m-0">
            待我审批 ({approvals.length})
          </h3>
          <span className="text-xs bg-amber-100 dark:bg-amber-950 text-amber-800 dark:text-amber-300 font-semibold px-2 py-0.5 rounded-full">
            部门负责人审批链
          </span>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {approvals.map((item) => (
          <div
            key={item.id}
            className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-amber-200/80 dark:border-amber-900/50 shadow-sm hover:shadow-md transition-all flex flex-col justify-between"
          >
            <div>
              <div className="flex items-start justify-between gap-2">
                <span className="text-sm font-bold text-slate-900 dark:text-slate-100">
                  {item.title}
                </span>
                <span className="text-xs text-slate-400 whitespace-nowrap flex items-center gap-1">
                  <Clock size={12} /> {formatCreatedAt(item.createdAt)}
                </span>
              </div>
              <div className="flex items-center gap-2 mt-2 text-xs text-slate-500">
                <span className="font-medium text-slate-700 dark:text-slate-300">申请人：{item.requesterName}</span>
                <span>•</span>
                <span>{item.department}</span>
                <span>•</span>
                <Tag color="orange" className="mr-0 text-[10px]">{item.serviceType}</Tag>
              </div>
              {item.description && (
                <div className="mt-3 p-2.5 rounded-xl bg-slate-50 dark:bg-slate-800/50 text-xs text-slate-600 dark:text-slate-300">
                  {item.description}
                </div>
              )}
            </div>

            <div className="flex items-center justify-end gap-2 mt-4 pt-3 border-t border-slate-100 dark:border-slate-800">
              <Button
                size="small"
                danger
                loading={actionLoading[item.id]}
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
                onClick={() => handleDecision(item.id, 'approve')}
                className="bg-emerald-600 hover:bg-emerald-500 border-none"
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
        open={rejectingTaskId !== null}
        okText="确认驳回"
        cancelText="取消"
        confirmLoading={rejectingTaskId !== null && Boolean(actionLoading[rejectingTaskId])}
        okButtonProps={{ danger: true, disabled: !rejectComment.trim() }}
        onCancel={() => {
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
