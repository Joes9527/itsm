'use client';

import React from 'react';
import { Button, Space } from 'antd';
import { useWorkItemContext } from './WorkItemContext';

// WorkItemActionBar 遍历 actions（通过 context 拿到），把每个动作渲染成一个按钮：
// disabled/title 取 allowed/reason，点击调用 onActionDispatch——见设计文档 §5.2。本组件不关心
// 具体业务含义，只负责渲染；Phase 4（后端 actions 计算）落地前三个页面传的都是空
// actions={{}}，此时不渲染任何按钮——这是"当前没有可执行的动作"这个真实状态，不是待完善的
// 占位态。
const ACTION_LABELS: Record<string, string> = {
  approve: '批准',
  reject: '拒绝',
  resolve: '解决',
  close: '关闭',
  assign: '指派',
  edit: '编辑',
  delete: '删除',
  cc: '抄送',
};

function labelFor(action: string): string {
  return ACTION_LABELS[action] ?? action;
}

export function WorkItemActionBar() {
  const { actions, onActionDispatch } = useWorkItemContext();
  const entries = Object.entries(actions);
  if (entries.length === 0) {
    return null;
  }
  return (
    <Space wrap>
      {entries.map(([action, state]) => {
        const label = labelFor(action);
        return (
          <Button
            key={action}
            disabled={!state.allowed}
            title={state.reason}
            aria-label={label}
            onClick={() => void onActionDispatch(action)}
          >
            {label}
          </Button>
        );
      })}
    </Space>
  );
}

export default WorkItemActionBar;
