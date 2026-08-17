/**
 * 工单审批相关接口。旧版 /api/v1/approval-workflows、/api/v1/tickets/approval/records
 * 在后端下线 legacy ApprovalWorkflow 引擎时已经被删除（router.go 已确认无此路由），
 * 这里只保留真实存在的两个接口：读 BPMN 审批决策历史、提交审批动作。
 */

import { httpClient } from './http-client';

export interface ProcessApprovalDecision {
  id: number;
  processInstanceId: number;
  processInstanceKey: string;
  processTaskId: number;
  taskId: string;
  processDefinitionKey: string;
  nodeKey: string;
  businessType?: string;
  businessId?: string;
  actorId: number;
  actorName?: string;
  action: string;
  decision: string;
  comment?: string;
  variablesSnapshot?: Record<string, unknown>;
  createdAt: string;
}

export interface SubmitApprovalRequest {
  approvalId: number;
  ticketId: number;
  action: 'approve' | 'reject' | 'delegate';
  comment?: string;
  delegateToUserId?: number;
}

export class TicketApprovalApi {
  static async getApprovalDecisions(ticketId: number): Promise<ProcessApprovalDecision[]> {
    const res = await httpClient.get<ProcessApprovalDecision[]>(
      `/api/v1/tickets/${ticketId}/approval-decisions`
    );
    return res || [];
  }

  static async submitApproval(data: SubmitApprovalRequest): Promise<void> {
    await httpClient.post('/api/v1/tickets/workflow/approve', data);
  }
}

export default TicketApprovalApi;
