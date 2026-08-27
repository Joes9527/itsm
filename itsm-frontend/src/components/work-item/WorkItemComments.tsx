'use client';

import React from 'react';
import { Card } from 'antd';

// 挂载点占位实现——Wave 2 迁移各域评论 UI 时改造这个组件去调用真实的评论 API，
// 调用方（WorkItemShell 及所有消费它的专业 Panel）只依赖 workItemId 这一个 prop，
// 不会因为内部实现从占位换成真实请求而改动。
export function WorkItemComments({ workItemId }: { workItemId: number }) {
  return <Card size="small" title="评论" data-work-item-id={workItemId} />;
}
