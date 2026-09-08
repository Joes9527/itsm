import { httpClient } from './http-client';

export type WorkItemRecordClass =
  | 'generic'
  | 'incident'
  | 'problem'
  | 'change_request'
  | 'service_request_item'
  | 'catalog_task';
export interface CreateWorkItemResult {
  workItemId: number;
  number: string;
  recordClass: WorkItemRecordClass;
  professionalReference: { type: string; id: number };
  workflowStartStatus: 'active' | 'not_required' | 'pending' | 'manual_intervention_required';
  replayed: boolean;
}
export interface CreationRequestOptions {
  idempotencyKey: string;
  assertSubmissionContext: () => void;
}
export function createWorkItem(
  endpoint: string,
  payload: unknown,
  options: CreationRequestOptions
): Promise<CreateWorkItemResult> {
  if (!options.idempotencyKey.trim()) throw new Error('缺少已确认申请标识');
  return httpClient.post<CreateWorkItemResult>(endpoint, payload, {
    headers: { 'Idempotency-Key': options.idempotencyKey },
    skipCamelCaseBody: true,
    assertSubmissionContext: options.assertSubmissionContext,
  });
}
export function creationReceiptMessage(receipt: CreateWorkItemResult): string {
  const statuses = {
    active: '流程已启动',
    not_required: '无需启动流程',
    pending: '流程启动排队中',
    manual_intervention_required: '流程启动需要人工处理',
  };
  return `${receipt.number} 已创建${receipt.replayed ? '（已确认原申请）' : ''}，${statuses[receipt.workflowStartStatus] || '流程状态待核查'}`;
}
export function professionalCreationPath(
  receipt: CreateWorkItemResult,
  type: 'incident' | 'problem' | 'change'
): string {
  const reference = receipt.professionalReference;
  if (reference.type !== type || !Number.isInteger(reference.id) || reference.id <= 0) {
    throw new Error(`${receipt.number} 已创建，但专业记录引用无效，请通过工单详情核查`);
  }
  const routes = { incident: 'incidents', problem: 'problems', change: 'changes' };
  return `/${routes[type]}/${reference.id}`;
}
