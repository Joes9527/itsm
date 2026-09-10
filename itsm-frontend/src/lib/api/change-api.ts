import type { RelationView, SourceRelation } from './workitem-relations';
import {
  createWorkItem,
  type CreationRequestOptions,
  type CreateWorkItemResult,
} from './work-item-creation';
/**
 * 变更管理 API 服务
 */

import { ApiError, httpClient } from './http-client';
import type { WorkItemActionState } from '@/components/work-item/WorkItemTypes';

// 变更状态类型
export type ChangeStatus =
  | 'submitted'
  | 'draft'
  | 'pending'
  | 'approved'
  | 'rejected'
  | 'scheduled'
  | 'in_progress'
  | 'completed'
  | 'failed'
  | 'rolled_back'
  | 'cancelled';

// 变更类型
export type ChangeType = 'normal' | 'standard' | 'emergency';

// 变更优先级
export type ChangePriority = 'low' | 'medium' | 'high' | 'critical';

// 变更影响范围
export type ChangeImpact = 'low' | 'medium' | 'high';

// 变更风险等级
export type ChangeRisk = 'low' | 'medium' | 'high';

// 变更请求接口
export interface ChangeRequest {
  requesterId?: number;
  title: string;
  description: string;
  justification: string;
  type: ChangeType;
  priority: ChangePriority;
  impactScope: ChangeImpact;
  riskLevel: ChangeRisk;
  plannedStartDate?: string;
  plannedEndDate?: string;
  implementationPlan: string;
  rollbackPlan: string;
  affectedCis: string[];
  sourceRelations?: SourceRelation[];
}

// 变更响应接口
export interface Change {
  number: string;
  version: number;
  outcome: '' | 'successful' | 'failed' | 'rolled_back';
  outcomeEvidence: string;
  reviewEvidence: string;
  reviewedBy: number;
  reviewedAt: string | null;
  standardTemplateId: number;
  currentTasks: Partial<Record<ChangeAction, string>> | null;
  id: number;
  title: string;
  description: string;
  justification: string;
  type: ChangeType;
  status: ChangeStatus;
  priority: ChangePriority;
  impactScope: ChangeImpact;
  riskLevel: ChangeRisk;
  assigneeId?: number;
  assigneeName?: string;
  createdBy: number;
  createdByName: string;
  tenantId: number;
  plannedStartDate?: string | null;
  plannedEndDate?: string | null;
  actualStartDate?: string | null;
  actualEndDate?: string | null;
  implementationPlan: string;
  rollbackPlan: string;
  affectedCis: string[];
  relations: RelationView[];
  createdAt: string;
  updatedAt: string;
  /**
   * 关联的 WorkItem（tickets.id）。后端创建事务保证该值存在；缺失表示开发数据违反
   * WorkItem 创建不变量。供 changes/[id]/page.tsx 接入 WorkItemShell 使用。
   */
  workItemId?: number;
  actions?: Record<string, WorkItemActionState>;
}

// 变更列表响应
export interface ChangeListResponse {
  total: number;
  changes: Change[];
}

// 变更统计响应
export interface ChangeStatsResponse {
  draft: number;
  scheduled: number;
  failed: number;
  successfulOutcomes: number;
  failedOutcomes: number;
  rolledBackOutcomes: number;
  total: number;
  pending: number;
  approved: number;
  inProgress: number;
  completed: number;
  rolledBack: number;
  rejected: number;
  cancelled: number;
}

// 变更审批记录
export interface ChangeApproval {
  id: number;
  changeId: number;
  approverId: number;
  approverName: string;
  status: ChangeStatus;
  comment?: string;
  approvedAt?: string;
  createdAt: string;
}

// 变更风险评估数据
export interface RiskAssessmentData {
  riskLevel: ChangeRisk;
  riskDescription: string;
  impactAnalysis: string;
  mitigationMeasures: string;
  contingencyPlan: string;
  riskOwner: string;
}

export interface ChangeCMDBImpactSummary {
  changeId: number;
  totalAffectedCIs: number;
  criticalCICount: number;
  highRiskDependencyCount: number;
  openIncidentCount: number;
  recommendedRiskLevel: string;
  recommendedImpactScope: string;
  requiresCAB: boolean;
  requiresBackoutPlan: boolean;
  workflowHints: string[];
  itilPractices: string[];
  affectedCIs: number[];
}

// ==================== PIR (Post-Implementation Review) 类型定义 ====================
// PIR总体结果
export type PIROverallResult = 'successful' | 'partially_successful' | 'failed' | 'rolled_back';

// PIR请求
export interface CreatePIRRequest extends ChangeMutationIdentity {
  overallResult: PIROverallResult;
  objectivesAchieved: boolean;
  successSummary?: string;
  issuesEncountered?: string;
  lessonsLearned?: string;
  improvementRecommendations?: string;
  actualStartTime?: string;
  actualEndTime?: string;
  rollbackPerformed: boolean;
  rollbackReason?: string;
}

// PIR更新请求
export interface UpdatePIRRequest extends ChangeMutationIdentity {
  changeId: number;
  overallResult?: PIROverallResult;
  objectivesAchieved?: boolean;
  successSummary?: string;
  issuesEncountered?: string;
  lessonsLearned?: string;
  improvementRecommendations?: string;
}

// PIR响应
export interface PIRResponse {
  id: number;
  changeId: number;
  changeTitle: string;
  reviewerId: number;
  reviewerName: string;
  overallResult: PIROverallResult;
  objectivesAchieved: boolean;
  successSummary?: string;
  issuesEncountered?: string;
  lessonsLearned?: string;
  improvementRecommendations?: string;
  actualStartTime?: string;
  actualEndTime?: string;
  actualDurationMinutes: number;
  rollbackPerformed: boolean;
  rollbackReason?: string;
  tenantId: number;
  reviewDate: string;
  createdAt: string;
  updatedAt: string;
}

// PIR列表响应
export interface PIRListResponse {
  total: number;
  items: PIRResponse[];
}

// 日历视图项
export interface ChangeCalendarItem {
  id: number;
  title: string;
  changeNumber: string;
  status: string;
  riskLevel: string;
  category: string;
  plannedStart: string;
  plannedEnd: string;
  assigneeName: string;
}

// 变更API类
export class ChangeApi {
  // 获取变更列表
  static async getChanges(params?: {
    page?: number;
    pageSize?: number;
    status?: ChangeStatus;
    type?: ChangeType;
    priority?: ChangePriority;
    risk?: string;
    search?: string;
  }): Promise<ChangeListResponse> {
    return httpClient.get<ChangeListResponse>(
      '/api/v1/changes',
      params && {
        ...params,
        riskLevel: params.risk,
        risk: undefined,
      }
    );
  }

  // 获取单个变更
  static async getChange(id: number): Promise<Change> {
    return httpClient.request<Change>(`/api/v1/changes/${id}`, { preserveResponseKeys: true });
  }

  // 创建变更
  static async createChange(
    data: ChangeRequest,
    options: CreationRequestOptions
  ): Promise<CreateWorkItemResult> {
    return createWorkItem('/api/v1/changes', data, options);
  }

  // 更新变更
  static async updateChange(id: number, data: ChangeMetadataRequest): Promise<ChangeResult> {
    return changeMutation(`/api/v1/changes/${id}`, 'PUT', data);
  }

  // 删除变更
  static async deleteChange(id: number): Promise<void> {
    return httpClient.delete(`/api/v1/changes/${id}`);
  }

  // 获取变更统计
  static async getChangeStats(): Promise<ChangeStatsResponse> {
    return httpClient.get<ChangeStatsResponse>('/api/v1/changes/stats');
  }

  static async executeAction<A extends ChangeAction>(
    id: number,
    action: A,
    data: ChangeActionRequests[A]
  ): Promise<ChangeResult | ChangeTaskProgress> {
    const route = action === 'record_outcome' ? 'record-outcome' : action;
    return changeMutation(
      `/api/v1/changes/${id}/${route}`,
      'POST',
      data,
      action !== 'submit' && action !== 'cancel'
    );
  }

  static async getTaskProgress(
    id: number,
    operationId: string,
    action: ChangeAction
  ): Promise<ChangeTaskProgress> {
    return changeMutation(
      `/api/v1/changes/${id}/task-progress?${new URLSearchParams({ operationId, action })}`,
      'GET',
      undefined,
      true
    ) as Promise<ChangeTaskProgress>;
  }

  static async assignChange(
    id: number,
    data: ChangeMutationIdentity & { assigneeId: number }
  ): Promise<ChangeResult> {
    return changeMutation(`/api/v1/changes/${id}/assign`, 'POST', data);
  }

  // 获取变更审批历史
  static async getChangeApprovals(id: number): Promise<ChangeApproval[]> {
    return httpClient.get<ChangeApproval[]>(`/api/v1/changes/${id}/approvals`);
  }

  // 获取变更模板
  static async getChangeTemplates(): Promise<any[]> {
    return httpClient.get('/api/v1/changes/templates');
  }

  // 导出变更数据
  static async exportChanges(params?: {
    format?: 'excel' | 'pdf' | 'csv';
    status?: ChangeStatus;
    startDate?: string;
    endDate?: string;
  }): Promise<Blob> {
    return httpClient.request<Blob>({
      url: '/api/v1/changes/export',
      method: 'GET',
      params,
      responseType: 'blob',
    });
  }

  // 获取变更关联工单
  static async getRelatedTickets(id: number): Promise<any[]> {
    return httpClient.get(`/api/v1/changes/${id}/tickets`);
  }

  // 获取日历视图数据
  static async getCalendar(params?: {
    startDate?: string;
    endDate?: string;
    status?: string;
  }): Promise<{ items: ChangeCalendarItem[]; total: number }> {
    return httpClient.get('/api/v1/changes/calendar', params);
  }

  // 获取变更风险评估
  static async getRiskAssessment(id: number): Promise<RiskAssessmentData | null> {
    return httpClient.get(`/api/v1/changes/${id}/risk`);
  }

  // 获取基于 CMDB 的变更影响摘要
  static async getCMDBImpactSummary(id: number): Promise<ChangeCMDBImpactSummary> {
    return httpClient.get(`/api/v1/changes/${id}/cmdb-impact`);
  }

  static async updateRisk(
    id: number,
    data: ChangeMutationIdentity & Partial<RiskAssessmentData>
  ): Promise<ChangeResult> {
    return changeMutation(`/api/v1/changes/${id}/risk`, 'PUT', data);
  }

  // 获取变更实施日志
  static async getImplementationLogs(id: number): Promise<any[]> {
    return httpClient.get(`/api/v1/changes/${id}/logs`);
  }

  // ==================== PIR (Post-Implementation Review) ====================

  // PIR总体结果类型
  static async getPIRs(params?: {
    page?: number;
    pageSize?: number;
    result?: 'successful' | 'partially_successful' | 'failed' | '全部';
  }): Promise<PIRListResponse> {
    return httpClient.get<PIRListResponse>(
      '/api/v1/changes/pirs',
      params as Record<string, unknown>
    );
  }

  // 获取变更关联的PIR
  static async getPIR(changeId: number): Promise<PIRResponse | null> {
    try {
      return await httpClient.get<PIRResponse>(`/api/v1/changes/${changeId}/pir`);
    } catch (error: any) {
      if (error instanceof ApiError && error.status === 404) {
        return null;
      }
      throw error;
    }
  }

  static async createPIR(changeId: number, data: CreatePIRRequest): Promise<PIRMutationResult> {
    return changeMutation(`/api/v1/changes/${changeId}/pir`, 'POST', data, false, true);
  }

  static async updatePIR(pirId: number, data: UpdatePIRRequest): Promise<PIRMutationResult> {
    return changeMutation(`/api/v1/changes/pir/${pirId}`, 'PUT', data, false, true);
  }

  static async deletePIR(
    pirId: number,
    data: ChangeMutationIdentity & { changeId: number }
  ): Promise<PIRMutationResult> {
    return changeMutation(`/api/v1/changes/pir/${pirId}`, 'DELETE', data, false, true);
  }
}

export interface ChangeMutationIdentity {
  expectedVersion: number;
  operationId: string;
}
export interface ChangeResult {
  workItemId: number;
  version: number;
  status: string;
  replayed: boolean;
}
export interface PIRMutationResult extends ChangeResult {
  pirId: number;
}
export interface ChangeTaskProgress {
  progress: 'pending' | 'processing' | 'blocked' | 'completed' | 'effect_applied';
  taskId: string;
  executionKey: string;
  reason?: string;
  result?: ChangeResult;
}
export interface ChangeActionRequests {
  submit: ChangeMutationIdentity;
  cancel: ChangeMutationIdentity & { evidence: string };
  assess: ChangeMutationIdentity & { taskId: string; evidence: string };
  approve: ChangeMutationIdentity & { taskId: string; evidence?: string };
  reject: ChangeMutationIdentity & { taskId: string; evidence: string };
  schedule: ChangeMutationIdentity & {
    taskId: string;
    plannedStartDate: string;
    plannedEndDate: string;
  };
  implement: ChangeMutationIdentity & { taskId: string };
  record_outcome: ChangeMutationIdentity & {
    taskId: string;
    outcome: 'successful' | 'failed' | 'rolled_back';
    evidence: string;
    actualEndDate: string;
  };
  review: ChangeMutationIdentity & { taskId: string; evidence: string; pirId: number };
  close: ChangeMutationIdentity & { taskId: string; evidence: string; pirId: number };
}
export type ChangeAction = keyof ChangeActionRequests;
export type ChangeMetadataRequest = ChangeMutationIdentity &
  Partial<Omit<ChangeRequest, 'requesterId' | 'sourceRelations'>>;

function isResult(value: unknown): value is ChangeResult {
  const v = value as ChangeResult | undefined;
  return (
    !!v &&
    Number.isInteger(v.workItemId) &&
    v.workItemId > 0 &&
    Number.isInteger(v.version) &&
    v.version > 0 &&
    typeof v.status === 'string' &&
    typeof v.replayed === 'boolean'
  );
}
export function isChangeTaskProgress(value: unknown): value is ChangeTaskProgress {
  const v = value as ChangeTaskProgress | undefined;
  return (
    !!v &&
    ['pending', 'processing', 'blocked', 'completed', 'effect_applied'].includes(v.progress) &&
    typeof v.taskId === 'string' &&
    !!v.taskId &&
    typeof v.executionKey === 'string' &&
    !!v.executionKey &&
    (v.result === undefined || isResult(v.result))
  );
}
async function changeMutation<T extends ChangeResult | ChangeTaskProgress>(
  path: string,
  method: 'GET' | 'POST' | 'PUT' | 'DELETE',
  data?: unknown,
  task = false,
  pir = false
): Promise<T> {
  try {
    return await httpClient.request<T>(path, {
      method,
      body: data === undefined ? undefined : JSON.stringify(data),
      skipCamelCaseBody: true,
      preserveResponseKeys: true,
      validateResponse: (value, status) => {
        if (task) {
          if (
            !isChangeTaskProgress(value) ||
            (status === 200 && !value.result) ||
            (status === 202 &&
              (!['pending', 'processing'].includes(value.progress) || value.result))
          )
            throw new Error('操作回执无效，结果未知，请查询进度');
        } else if (
          !isResult(value) ||
          (pir &&
            (!Number.isInteger((value as PIRMutationResult).pirId) ||
              (value as PIRMutationResult).pirId <= 0))
        )
          throw new Error('操作回执缺失，结果未知，请刷新核查');
      },
    });
  } catch (error) {
    if (
      task &&
      error instanceof ApiError &&
      error.status === 409 &&
      isChangeTaskProgress(error.data) &&
      error.data.progress === 'blocked'
    )
      return error.data as T;
    throw error;
  }
}
