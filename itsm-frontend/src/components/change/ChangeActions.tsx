'use client';

import React, { useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  DatePicker,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Typography,
} from 'antd';
import {
  ChangeApi,
  isChangeTaskProgress,
  type Change,
  type ChangeAction,
  type ChangeActionRequests,
  type ChangeTaskProgress,
  type ChangeResult,
} from '@/lib/api/change-api';
import { WorkItemActionButton } from '@/components/work-item/WorkItemActionButton';
import { useChangeOperation } from './useChangeOperation';

const labels: Record<ChangeAction, string> = {
  submit: '提交审批',
  cancel: '取消变更',
  assess: '完成评估',
  approve: '批准',
  reject: '拒绝',
  schedule: '安排实施窗口',
  implement: '开始实施',
  record_outcome: '记录实施结果',
  review: '完成审查',
  close: '关闭变更',
};
const progressLabels: Record<ChangeTaskProgress['progress'], string> = {
  pending: '流程推进等待中',
  processing: '流程推进处理中',
  blocked: '流程推进受阻，需要人工处理',
  completed: '当前任务回调已完成',
  effect_applied: '业务结果已提交，后续流程进度不可用',
};

export function ChangeActions({
  change,
  onRefresh,
}: {
  change: Change;
  onRefresh: () => Promise<void>;
}) {
  const [action, setAction] = useState<ChangeAction | null>(null);
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [progress, setProgress] = useState<ChangeTaskProgress | null>(null);
  const [receipt, setReceipt] = useState<ChangeResult | null>(null);
  const [inspection, setInspection] = useState<{
    operationId: string;
    action: ChangeAction;
  } | null>(null);
  const [polls, setPolls] = useState(0);
  const mounted = useRef(true);
  const operation = useChangeOperation();
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const inspect = async () => {
    if (!inspection) return;
    try {
      const next = await ChangeApi.getTaskProgress(
        change.id,
        inspection.operationId,
        inspection.action
      );
      if (!mounted.current) return;
      setProgress(next);
      if (next.result) setReceipt(next.result);
      setError('');
      await onRefresh();
    } catch (e) {
      if (mounted.current) {
        setError(e instanceof Error ? e.message : '进度查询失败');
        setPolls(5);
      }
    }
  };

  useEffect(() => {
    if (
      !inspection ||
      !progress ||
      !['pending', 'processing'].includes(progress.progress) ||
      polls >= 5
    )
      return;
    const timer = setTimeout(() => {
      setPolls(n => n + 1);
      void inspect();
    }, 2000);
    return () => clearTimeout(timer);
  }, [inspection, progress, polls]); // Each acceptance performs at most five read-only inspections.

  const submit = async () => {
    if (!action) return;
    try {
      const values = await form.validateFields();
      const taskId = change.currentTasks?.[action];
      const facts: Record<string, unknown> = {};
      if (action !== 'submit' && action !== 'cancel') {
        if (!taskId) throw new Error('缺少当前任务，请刷新变更详情');
        facts.taskId = taskId;
      }
      if (
        ['cancel', 'assess', 'approve', 'reject', 'record_outcome', 'review', 'close'].includes(
          action
        ) &&
        values.evidence
      )
        facts.evidence = values.evidence.trim();
      if (action === 'schedule') {
        facts.plannedStartDate = values.window[0].toISOString();
        facts.plannedEndDate = values.window[1].toISOString();
      }
      if (action === 'record_outcome') {
        facts.outcome = values.outcome;
        facts.actualEndDate = values.actualEndDate.toISOString();
      }
      if (action === 'review' || action === 'close') facts.pirId = values.pirId;
      const identity = operation.identity(`${change.id}/${action}`, facts, change.version);
      setBusy(true);
      setError('');
      const next = await ChangeApi.executeAction(change.id, action, {
        ...facts,
        ...identity,
      } as ChangeActionRequests[typeof action]);
      if (!mounted.current) return;
      operation.clear();
      if (isChangeTaskProgress(next)) {
        setProgress(next);
        setReceipt(next.result ?? null);
        setInspection({ operationId: identity.operationId, action });
        setPolls(0);
      } else {
        setReceipt(next);
        setProgress(null);
        setInspection(null);
      }
      setAction(null);
      await onRefresh();
    } catch (e) {
      if (e && typeof e === 'object' && 'errorFields' in e) return;
      if (mounted.current) setError(e instanceof Error ? e.message : '操作失败');
    } finally {
      if (mounted.current) setBusy(false);
    }
  };
  const awaiting = progress && ['pending', 'processing'].includes(progress.progress);
  return (
    <Space orientation='vertical' style={{ width: '100%' }}>
      <Space wrap>
        {(Object.keys(labels) as ChangeAction[]).map(key => (
          <WorkItemActionButton
            key={key}
            action={change.actions?.[key]}
            actionName={key}
            button={{
              disabled: busy || !!awaiting,
              onClick: () => {
                setAction(key);
                form.resetFields();
              },
            }}
          >
            {labels[key]}
          </WorkItemActionButton>
        ))}
        <Button onClick={() => void onRefresh().catch(e => setError(e.message))}>刷新详情</Button>
      </Space>
      {error && (
        <Alert
          type='error'
          showIcon
          title={error}
          description='请核查当前版本及任务；相同内容重试会保留原操作标识和版本。'
          action={
            <Button
              disabled={busy}
              onClick={async () => {
                try {
                  await onRefresh();
                  operation.clear();
                  form.resetFields();
                  setError('');
                } catch (e) {
                  setError(e instanceof Error ? e.message : '刷新失败');
                }
              }}
            >
              刷新版本并重新填写
            </Button>
          }
        />
      )}
      {receipt && (
        <Alert
          type='info'
          showIcon
          title={`业务结果已提交：版本 ${receipt.version}，状态 ${receipt.status}`}
        />
      )}
      {progress && (
        <Alert
          type={progress.progress === 'blocked' ? 'error' : 'info'}
          showIcon
          title={progressLabels[progress.progress]}
          description={
            <>
              {progress.reason && <div>{progress.reason}</div>}
              {awaiting && polls >= 5 && <div>自动查询已暂停，可手动查询进度。</div>}
              <Typography.Text>当前任务的回执不代表整个变更流程已关闭。</Typography.Text>
              <Button onClick={() => void inspect()}>查询进度</Button>
            </>
          }
        />
      )}
      <Modal
        title={action ? labels[action] : ''}
        open={!!action}
        onCancel={() => !busy && setAction(null)}
        onOk={() => void submit()}
        confirmLoading={busy}
        okText='提交'
        cancelText='返回'
      >
        <Form form={form} layout='vertical'>
          {action &&
            ['cancel', 'assess', 'approve', 'reject', 'record_outcome', 'review', 'close'].includes(
              action
            ) && (
              <Form.Item
                name='evidence'
                label='操作依据'
                rules={[
                  { required: action !== 'approve', whitespace: true, message: '请填写操作依据' },
                ]}
              >
                <Input.TextArea rows={3} />
              </Form.Item>
            )}
          {action === 'schedule' && (
            <Form.Item
              name='window'
              label='实施窗口'
              rules={[{ required: true, message: '请选择实施窗口' }]}
            >
              <DatePicker.RangePicker showTime />
            </Form.Item>
          )}
          {action === 'record_outcome' && (
            <>
              <Form.Item
                name='outcome'
                label='实施结果'
                rules={[{ required: true, message: '请选择实际实施结果' }]}
              >
                <Select
                  options={[
                    { value: 'successful', label: '成功' },
                    { value: 'failed', label: '失败' },
                    { value: 'rolled_back', label: '已回滚' },
                  ]}
                />
              </Form.Item>
              <Form.Item
                name='actualEndDate'
                label='实际结束时间'
                rules={[{ required: true, message: '请选择实际结束时间' }]}
              >
                <DatePicker showTime />
              </Form.Item>
            </>
          )}
          {(action === 'review' || action === 'close') && (
            <Form.Item
              name='pirId'
              label='PIR 记录 ID'
              rules={[{ required: true, message: '请填写本次审查的 PIR ID' }]}
            >
              <InputNumber min={1} precision={0} />
            </Form.Item>
          )}
        </Form>
      </Modal>
    </Space>
  );
}
