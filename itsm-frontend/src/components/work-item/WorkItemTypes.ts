// WorkItem 共享前端类型契约。Wave 2 的四个域迁移任务包直接消费这个文件里的类型，不允许
// 各自重新定义形状——见 docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md §4.4。

export interface WorkItemActionState {
  allowed: boolean;
  reason?: string;
}

export interface WorkItemCommon {
  id: number;
  number: string;
  recordClass: 'generic' | 'service_request_item' | 'incident' | 'problem' | 'change_request' | 'catalog_task';
  title: string;
  status: string;
  priority: string;
  requesterId: number;
  assigneeId?: number;
  categoryId?: number;
  createdAt: string;
  updatedAt: string;
}

export interface WorkItemSLAState {
  remainingSeconds: number | null;
  isBreached: boolean;
  responseDeadline: string | null;
  resolutionDeadline: string | null;
}

export type WorkItemActionType = 'approve' | 'reject' | 'resolve' | 'close' | 'assign' | string;

export interface WorkItemActionDispatch {
  (action: WorkItemActionType, payload?: Record<string, unknown>): Promise<void>;
}

export interface WorkItemShellProps {
  workItem: WorkItemCommon;
  actions: Record<string, WorkItemActionState>;
  sla?: WorkItemSLAState;
  onActionDispatch: WorkItemActionDispatch;
  /** 专业 Panel（Incident/Problem/Change/ServiceRequestItem 各自实现）挂载点 */
  professionalPanelSlot: React.ReactNode;
  loading?: boolean;
  error?: string | null;
}
