'use client';

import React from 'react';
import { Card } from 'antd';

export function WorkItemAttachments({ workItemId }: { workItemId: number }) {
  return <Card size="small" title="附件" data-work-item-id={workItemId} />;
}
