'use client';

import React from 'react';
import { Card } from 'antd';
import { CommentPanel, ticketCommentAdapter } from '@/components/business/detail-tabs';
import { useAuthStore } from '@/lib/store/auth-store';
import { useWorkItemContext } from './WorkItemContext';
import { toTargetType } from './toTargetType';

// WorkItemComments 是 WorkItemShell 的评论区块。四个 recordClass（含已收口的 Incident——见
// docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md §4.2）统一走
// ticketCommentAdapter + workItemId，不需要再按 recordClass 切换 adapter。
export function WorkItemComments({ workItemId }: { workItemId: number }) {
  const { workItem } = useWorkItemContext();
  const { user } = useAuthStore();
  return (
    <Card size="small" title="评论">
      <CommentPanel
        targetType={toTargetType(workItem.recordClass)}
        targetId={workItemId}
        adapter={ticketCommentAdapter}
        currentUserId={user?.id}
        showInternalToggle
      />
    </Card>
  );
}
