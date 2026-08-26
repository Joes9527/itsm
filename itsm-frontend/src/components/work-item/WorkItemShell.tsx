'use client';

import React from 'react';
import { Card, Space, Tag, Descriptions } from 'antd';
import { WorkItemProvider } from './WorkItemContext';
import type { WorkItemShellProps } from './WorkItemTypes';
import { WorkItemComments } from './WorkItemComments';
import { WorkItemAttachments } from './WorkItemAttachments';

// WorkItemShell 提供所有 recordClass 共用的公共区块骨架（编号/标题/状态/优先级/请求人/
// 分派/SLA/评论/附件），专业字段由调用方通过 professionalPanelSlot 传入。本组件本身
// 不实现任何专业 Panel——那是 Wave 2 各域任务包的范围。
//
// 不做的事：不在这里拼装任何具体域的 API 调用。所有动作都通过 onActionDispatch 回调
// 交给调用方处理，专业 Panel 也应该复用同一个回调，不要在 Panel 内部单独发 HTTP 请求。
export function WorkItemShell({
  workItem,
  actions,
  sla,
  onActionDispatch,
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
    <WorkItemProvider value={{ workItem, sla, onActionDispatch }}>
      <Space orientation="vertical" style={{ width: '100%' }} size="large">
        <Card>
          <Descriptions column={3} title={`${workItem.number} · ${workItem.title}`}>
            <Descriptions.Item label="状态">{workItem.status}</Descriptions.Item>
            <Descriptions.Item label="优先级">{workItem.priority}</Descriptions.Item>
            <Descriptions.Item label="处理人">{workItem.assigneeId ?? '未分配'}</Descriptions.Item>
          </Descriptions>
        </Card>
        <Card>{professionalPanelSlot}</Card>
        <WorkItemComments workItemId={workItem.id} />
        <WorkItemAttachments workItemId={workItem.id} />
      </Space>
    </WorkItemProvider>
  );
}
