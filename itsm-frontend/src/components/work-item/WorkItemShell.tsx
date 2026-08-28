'use client';

import React from 'react';
import { Card, Space, Tag, Descriptions } from 'antd';
import { WorkItemProvider } from './WorkItemContext';
import type { WorkItemShellProps } from './WorkItemTypes';
import { WorkItemComments } from './WorkItemComments';
import { WorkItemAttachments } from './WorkItemAttachments';
import { WorkItemSLA } from './WorkItemSLA';
import { WorkItemActionBar } from './WorkItemActionBar';
import { TicketHistoryList } from '@/components/ticket/TicketHistoryList';
import { TicketRelationCards } from '@/components/ticket/TicketRelationCards';

// WorkItemShell 提供所有 recordClass 共用的公共区块骨架（编号/标题/状态/优先级/请求人/
// 分派/SLA/评论/附件/历史/关联/操作栏），专业字段由调用方通过 professionalPanelSlot 传入。
// 本组件本身不实现任何专业 Panel——那是各域专业组件（IncidentDetail/ProblemDetail/
// ChangeDetail）的范围。
//
// 不做的事：不在这里拼装任何具体域的 API 调用。通用动作通过 onActionDispatch 回调
// 交给调用方处理；Incident、Problem 和 Change Panel 保留各自的专用 API 调用，因为
// 它们的动作需要领域特有的 payload 和 modal。
export function WorkItemShell({
  workItem,
  actions,
  sla,
  onActionDispatch,
  showActionBar = false,
  professionalPanelSlot,
  loading,
  error,
}: WorkItemShellProps) {
  if (loading) {
    return <Card loading />;
  }
  if (error) {
    return <Card><Tag color="red">加载失败：{error}</Tag></Card>;
  }

  return (
    <WorkItemProvider value={{ workItem, actions, sla, onActionDispatch }}>
      <Space orientation="vertical" style={{ width: '100%' }} size="large">
        <Card>
          <Descriptions column={3} title={`${workItem.number} · ${workItem.title}`}>
            <Descriptions.Item label="状态">{workItem.status}</Descriptions.Item>
            <Descriptions.Item label="优先级">{workItem.priority}</Descriptions.Item>
            <Descriptions.Item label="处理人">{workItem.assigneeId ?? '未分配'}</Descriptions.Item>
          </Descriptions>
          {showActionBar && <WorkItemActionBar />}
        </Card>
        <WorkItemSLA sla={sla} />
        <Card>{professionalPanelSlot}</Card>
        <WorkItemComments workItemId={workItem.id} />
        <WorkItemAttachments workItemId={workItem.id} />
        <Card size="small" title="历史">
          <TicketHistoryList ticketId={workItem.id} />
        </Card>
        <Card size="small" title="关联">
          <TicketRelationCards ticketId={workItem.id} />
        </Card>
      </Space>
    </WorkItemProvider>
  );
}
