'use client';

import React, { useEffect, useState } from 'react';
import Link from 'next/link';
import { Card, Space, Button, Table, Tag, App, Empty, Tooltip, Modal, Input, Typography } from 'antd';
import { X, Check, UserPlus, RotateCcw, CheckCircle } from 'lucide-react';

import { BPMNWorkflowApi, type UserTask } from '@/lib/api/bpmn-workflow-api';

// 这个页面原来有两个 Tab："服务请求审批"（直接对 ServiceRequest 调用
// serviceRequestAPI.getPendingApprovals/applyApprovalAction）和"我作为候选组员（BPMN）"。
// Task 1 把 SR 自己的审批阶段概念整体退休——审批人不再直接对 ServiceRequest 做
// approve/reject，任何审批（包括服务目录发起的申请）现在都流经关联 Ticket 自己的 BPMN
// 流程，也就是下面 MyBpmnTaskTab 已经在做的事。"服务请求审批"Tab 打的
// /api/v1/service-requests/approvals/pending 和 .../approval 路由已经被删除，这个 Tab
// 一直在报错/静默返回空。与其保留一个假装还能用的入口，不如去掉；一个专门给"审批人代表
// 组织去审批 SR 自己的申请单"用的独立视图（而不是通用的"我的 BPMN 待办任务"）如果以后
// 真的需要，属于"审批收敛到 BPMN"这条后续工作，不在本次改造范围内。
export default function PendingApprovalsPage() {
  return (
    <div className="max-w-6xl mx-auto p-6">
      <Space orientation="vertical" size={16} className="w-full">
        <Card className="rounded-lg shadow-sm border border-gray-200">
          <div>
            <h2 className="text-2xl font-bold text-gray-900 mb-0">我的待办审批</h2>
            <p className="text-gray-500 mt-1 mb-0">
              当前指派给我、或我可以领取的 BPMN 审批任务（包含服务目录申请单关联的工单）。
            </p>
          </div>
        </Card>

        <Card className="rounded-lg shadow-sm border border-gray-200">
          <MyBpmnTaskTab />
        </Card>
      </Space>
    </div>
  );
}

// === BPMN 任务 Tab（作为候选组员/被指派人） ===
function MyBpmnTaskTab() {
  const { message } = App.useApp();
  const [loading, setLoading] = useState(false);
  const [tasks, setTasks] = useState<UserTask[]>([]);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [claimingIds, setClaimingIds] = useState<Set<string | number>>(new Set());
  const [decision, setDecision] = useState<{ task: UserTask; action: 'approve' | 'reject' } | null>(null);
  const [decisionComment, setDecisionComment] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const load = async (p = page, s = size) => {
    try {
      setLoading(true);
      const data = await BPMNWorkflowApi.listUserTasks({ page: p, pageSize: s });
      setTasks(data.items.filter(task => task.taskPurpose.toLowerCase() === 'approval'));
      setTotal(data.total);
      setPage(data.page);
      setSize(data.pageSize);
    } catch (e) {
      console.error(e);
      message.error('加载 BPMN 待办任务失败');
      setTasks([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  const handleClaim = async (taskId: string | number) => {
    try {
      setClaimingIds(prev => new Set(prev).add(taskId));
      await BPMNWorkflowApi.claimTask(taskId);
      message.success('已领取');
      load();
    } catch (e) {
      console.error(e);
      message.error('领取失败：' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setClaimingIds(prev => {
        const next = new Set(prev);
        next.delete(taskId);
        return next;
      });
    }
  };

  // 打开审批决策弹窗
  const openDecision = (task: UserTask, action: 'approve' | 'reject') => {
    setDecisionComment('');
    setDecision({ task, action });
  };

  // 提交审批决策：批准可不填意见，拒绝必须填写（与后端校验一致）
  const submitDecision = async () => {
    if (!decision) return;
    const comment = decisionComment.trim();
    if (decision.action === 'reject' && !comment) {
      message.warning('拒绝时必须填写审批意见');
      return;
    }
    setSubmitting(true);
    try {
      await BPMNWorkflowApi.submitApprovalDecision(decision.task.id, { action: decision.action, comment });
      message.success(decision.action === 'approve' ? '已批准' : '已拒绝');
      setDecision(null);
      load();
    } catch (e) {
      console.error(e);
      message.error('提交审批决策失败：' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setSubmitting(false);
    }
  };

  useEffect(() => {
    load(1, size);
     
  }, []);

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="text-sm text-gray-500">
          包含我作为 <b>被指派人</b>、<b>候选人</b> 或 <b>候选组员</b> 的待办 BPMN UserTask。
        </div>
        <Button size="small" icon={<RotateCcw />} onClick={() => load(page, size)} loading={loading}>
          刷新
        </Button>
      </div>
      {tasks.length === 0 && !loading ? (
        <Empty description="暂无 BPMN 待办任务" image={Empty.PRESENTED_IMAGE_SIMPLE}>
          <div className="text-center">
            <CheckCircle className="text-4xl text-green-500 mb-4" />
            <p className="text-gray-500">没有需要我处理的 BPMN 审批节点</p>
          </div>
        </Empty>
      ) : (
        <Table<UserTask>
          rowKey={(t) => String(t.id)}
          loading={loading}
          dataSource={tasks}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            onChange: (p, s) => load(p, s),
          }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 90 },
            { title: '节点', dataIndex: 'taskName', width: 180 },
            {
              title: '实例',
              dataIndex: 'processInstanceId',
              width: 140,
              render: (v: string) => (v ? String(v) : '-'),
            },
            {
              title: '状态',
              dataIndex: 'status',
              width: 120,
              render: (v: string) => <Tag>{String(v)}</Tag>,
            },
            {
              title: '当前指派',
              dataIndex: 'assignee',
              width: 140,
              render: (v: string) => (v ? String(v) : <Tag color="default">未领取</Tag>),
            },
            {
              title: '创建时间',
              dataIndex: 'createdTime',
              width: 200,
              render: (v: string) => (v ? new Date(v).toLocaleString() : '-'),
            },
            {
              title: '操作',
              key: 'actions',
              width: 300,
              render: (_, r) => (
                <Space size="small">
                  {!r.assignee && (
                    <Tooltip title="领取任务并成为指派人">
                      <Button
                        size="small"
                        icon={<UserPlus />}
                        loading={claimingIds.has(r.id)}
                        onClick={() => handleClaim(r.id)}
                      >
                        领取
                      </Button>
                    </Tooltip>
                  )}
                  <Button
                    type="primary"
                    size="small"
                    icon={<Check />}
                    onClick={() => openDecision(r, 'approve')}
                  >
                    批准
                  </Button>
                  <Button
                    danger
                    size="small"
                    icon={<X />}
                    onClick={() => openDecision(r, 'reject')}
                  >
                    拒绝
                  </Button>
                  <Link href={`/workflow/instances?instanceId=${r.processInstanceId}`}>
                    <Button size="small">详情</Button>
                  </Link>
                </Space>
              ),
            },
          ]}
        />
      )}

      {/* 审批决策弹窗 */}
      <Modal
        title={decision?.action === 'approve' ? '批准任务' : '拒绝任务'}
        open={!!decision}
        onOk={submitDecision}
        onCancel={() => setDecision(null)}
        okText={decision?.action === 'approve' ? '确认批准' : '确认拒绝'}
        okButtonProps={{ danger: decision?.action === 'reject', loading: submitting }}
        cancelText="取消"
        destroyOnHidden
      >
        {decision && (
          <div className="space-y-3">
            <div>
              <Typography.Text type="secondary">任务：</Typography.Text>
              <Typography.Text strong>{decision.task.taskName || String(decision.task.id)}</Typography.Text>
            </div>
            <div>
              <Typography.Text type="secondary">
                审批意见{decision.action === 'reject' ? '（必填）' : '（选填）'}：
              </Typography.Text>
              <Input.TextArea
                rows={3}
                className="mt-1"
                value={decisionComment}
                onChange={(e) => setDecisionComment(e.target.value)}
                placeholder={decision.action === 'reject' ? '请填写拒绝原因' : '可填写审批意见'}
                maxLength={500}
                showCount
              />
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
