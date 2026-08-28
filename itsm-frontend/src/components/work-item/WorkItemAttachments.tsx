'use client';

import React from 'react';
import { Card } from 'antd';
import { AttachmentPanel, ticketAttachmentAdapter } from '@/components/business/detail-tabs';
import { useWorkItemContext } from './WorkItemContext';
import { toTargetType } from './toTargetType';

// WorkItemAttachments 是 WorkItemShell 的附件区块。附件在 Incident/Problem/Change 迁移前
// 就没有专属实现（不像评论有 incident_events 需要先搬），四个 recordClass 从一开始就统一走
// ticketAttachmentAdapter，不需要 Phase 2 那样的迁移步骤。
export function WorkItemAttachments({ workItemId }: { workItemId: number }) {
  const { workItem } = useWorkItemContext();
  return (
    <Card size="small" title="附件">
      <AttachmentPanel
        targetType={toTargetType(workItem.recordClass)}
        targetId={workItemId}
        adapter={ticketAttachmentAdapter}
      />
    </Card>
  );
}
