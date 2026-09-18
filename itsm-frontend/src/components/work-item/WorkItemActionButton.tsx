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
      {/* icon-gate: 整个 button props 包由调用方传入。本组件无法约束图标来源，
          调用方（IncidentDetail / ProblemDetail / ChangeActions）每次往 button.icon
          里放图标时都要自己确认用的是 @ant-design/icons 而不是 lucide。 */}
      <Button
        {...button}
        disabled={!action.allowed || button.disabled === true}
        title={action.reason}
        aria-describedby={!action.allowed && action.reason ? reasonId : undefined}
      >
        {children}
      </Button>
      {!action.allowed && action.reason && (
        <span id={reasonId} role='note' style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
          {action.reason}
        </span>
      )}
    </Space>
  );
}

export default WorkItemActionButton;
