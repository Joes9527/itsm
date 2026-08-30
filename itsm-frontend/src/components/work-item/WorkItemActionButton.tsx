'use client';

import React from 'react';
import { Button, Space } from 'antd';
import type { WorkItemActionState } from './WorkItemTypes';

interface WorkItemActionButtonProps {
  action: WorkItemActionState | undefined;
  actionName: string;
  children: React.ReactNode;
  button: React.ComponentProps<typeof Button>;
}

export function WorkItemActionButton({
  action,
  actionName,
  children,
  button,
}: WorkItemActionButtonProps) {
  if (!action) {
    return null;
  }

  const reasonId = `work-item-action-${actionName}-reason`;
  return (
    <Space size={4}>
      <Button
        {...button}
        disabled={!action.allowed || button.disabled === true}
        title={action.reason}
        aria-describedby={!action.allowed && action.reason ? reasonId : undefined}
      >
        {children}
      </Button>
      {!action.allowed && action.reason && (
        <span id={reasonId} role='note' style={{ color: '#8c8c8c', fontSize: 12 }}>
          {action.reason}
        </span>
      )}
    </Space>
  );
}

export default WorkItemActionButton;
