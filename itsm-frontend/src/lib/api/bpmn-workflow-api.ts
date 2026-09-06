/** Canonical frontend client for controller/bpmn_workflow_controller.go. */
import { httpClient } from './http-client';

export interface Pagination {
  total: number;
  page: number;
  pageSize: number;
}
interface BackendListResponse<T> {
  data: T[];
  pagination: Pagination;
}

export interface ProcessDefinition {
  id: number;
  key: string;
  name: string;
  description?: string;
  version: string;
  category: string;
  bpmnXml?: string;
  processVariables?: Record<string, unknown>;
  isActive: boolean;
  isLatest: boolean;
  deploymentId: number;
  deploymentName?: string;
  deployedAt?: string;
  tenantId: number;
  createdAt: string;
  updatedAt: string;
}

export interface ProcessDefinitionListResponse extends Pagination {
  items: ProcessDefinition[];
}
export interface CreateProcessDefinitionRequest {
  key: string;
  name: string;
  description?: string;
  category?: string;
  bpmnXml: string;
  processVariables?: Record<string, unknown>;
}
export interface UpdateProcessDefinitionRequest {
  name?: string;
  description?: string;
  category?: string;
  bpmnXml?: string;
  processVariables?: Record<string, unknown>;
  isActive?: boolean;
}
export interface CloneProcessDefinitionRequest {
  newKey: string;
  newName: string;
}

export interface ProcessInstance {
  id: number;
  processInstanceId: string;
  processDefinitionKey: string;
  processDefinitionId: number;
  status: 'created' | 'running' | 'completed' | 'terminated' | 'suspended';
  currentActivityId?: string;
  currentActivityName?: string;
  variables?: Record<string, unknown>;
  businessKey?: string;
  initiator?: string;
  startTime?: string;
  endTime?: string;
  tenantId: number;
  createdAt: string;
  updatedAt: string;
}
export interface ProcessInstanceListResponse extends Pagination {
  items: ProcessInstance[];
}
export interface StartProcessRequest {
  processDefinitionKey: string;
  businessKey: string;
  variables?: Record<string, unknown>;
}

export interface UserTask {
  id: number;
  taskId: string;
  taskDefinitionKey: string;
  taskName: string;
  taskType: string;
  status: string;
  priority: string;
  assignee: string;
  candidateUsers: string;
  candidateGroups: string;
  processInstanceId: number;
  processInstanceKey: string;
  processDefinitionKey: string;
  businessKey: string;
  businessType: string;
  businessId: number;
  taskPurpose: string;
  formKey?: string;
  taskVariables?: Record<string, unknown>;
  dueDate?: string;
  createdTime: string;
}
export interface UserTaskListResponse extends Pagination {
  items: UserTask[];
}
export interface CompleteTaskRequest {
  variables?: Record<string, unknown>;
  comment?: string;
}
export interface SubmitApprovalDecisionRequest {
  action: 'approve' | 'reject';
  comment?: string;
  variables?: Record<string, unknown>;
}

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
  action:
    | 'approve'
    | 'reject'
    | 'delegate'
    | 'transfer'
    | 'add_approver'
    | 'withdraw'
    | 'timeout'
    | 'system_decision';
  decision: string;
  comment?: string;
  variablesSnapshot?: Record<string, unknown>;
  createdAt: string;
}

export interface ProcessVersion {
  id: string;
  processDefinitionKey: string;
  version: string;
  name: string;
  description: string;
  bpmnXml: string;
  deploymentId: string;
  isActive: boolean;
  createdAt: string;
  updatedAt: string;
  createdBy: string;
  tenantId: number;
  changeLog: string;
  compatibilityNotes: string;
}
export interface CreateVersionRequest {
  baseVersion?: string;
  category?: string;
  processVariables?: Record<string, unknown>;
  processDefinitionKey: string;
  name: string;
  description?: string;
  bpmnXml: string;
  changeLog?: string;
  compatibilityNotes?: string;
}
export interface VersionComparison {
  baseVersion: ProcessVersion;
  targetVersion: ProcessVersion;
  changes: Array<Record<string, string>>;
  breakingChanges: string[];
  compatibility: string;
}
export interface InstanceStats {
  total: number;
  running: number;
  completed: number;
  suspended: number;
  terminated: number;
}
export interface TaskStats {
  totalTasks: number;
  completedTasks: number;
  pendingTasks: number;
  overdueTasks: number;
  averageCompletion: number;
  statusBreakdown: Record<string, number>;
  assigneeBreakdown: Record<string, number>;
  timeDistribution: Record<string, unknown>;
}
export interface CounterSignRequest {
  approvalType: 'serial' | 'parallel';
  approvers: string[];
  threshold: number;
}
export interface CounterSignStatus {
  parentTaskId: string;
  total: number;
  completed: number;
  approved: number;
  rejected: number;
  pending: number;
  status: string;
}
export interface VersionChangeLog {
  id: number;
  processDefinitionKey: string;
  version: string;
  changeType: string;
  changedFields: string[];
  changeDetails?: string;
  createdBy?: number;
  createdAt?: string;
}

export class BPMNWorkflowApi {
  private static readonly baseUrl = '/api/v1/bpmn';

  static async createProcessDefinition(
    data: CreateProcessDefinitionRequest
  ): Promise<ProcessDefinition> {
    return httpClient.post(`${this.baseUrl}/process-definitions`, data);
  }

  static async listProcessDefinitions(params?: {
    page?: number;
    pageSize?: number;
    key?: string;
    category?: string;
    isActive?: boolean;
  }): Promise<ProcessDefinitionListResponse> {
    const query: Record<string, string> = {};
    if (params?.page) query.page = String(params.page);
    if (params?.pageSize) query.pageSize = String(params.pageSize);
    if (params?.key) query.key = params.key;
    if (params?.category) query.category = params.category;
    if (params?.isActive !== undefined) query.isActive = String(params.isActive);
    const response = await httpClient.get<BackendListResponse<ProcessDefinition>>(
      `${this.baseUrl}/process-definitions`,
      query
    );
    return { items: response.data, ...response.pagination };
  }

  static async getProcessDefinition(key: string, version?: string): Promise<ProcessDefinition> {
    return httpClient.get(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}`,
      version ? { version } : undefined
    );
  }

  static async updateProcessDefinition(
    key: string,
    version: string,
    data: UpdateProcessDefinitionRequest
  ): Promise<ProcessDefinition> {
    return httpClient.put(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}?version=${encodeURIComponent(version)}`,
      data
    );
  }

  static async deleteProcessDefinition(key: string, version: string): Promise<void> {
    await httpClient.delete(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}?version=${encodeURIComponent(version)}`
    );
  }

  static async exportProcessDefinition(
    key: string,
    version?: string
  ): Promise<{ workflow: Record<string, unknown>; exportTime: string; exportVersion: string }> {
    return httpClient.get(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}/export`,
      version ? { version } : undefined
    );
  }

  static async cloneProcessDefinition(
    key: string,
    version: string,
    data: CloneProcessDefinitionRequest
  ): Promise<ProcessDefinition> {
    return httpClient.post(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}/clone?version=${encodeURIComponent(version)}`,
      data
    );
  }

  static async setProcessDefinitionActive(
    key: string,
    version: string,
    active: boolean
  ): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}/active?version=${encodeURIComponent(version)}`,
      { active }
    );
  }

  static async startProcess(data: StartProcessRequest): Promise<ProcessInstance> {
    return httpClient.post(`${this.baseUrl}/process-instances`, data);
  }

  static async listProcessInstances(params?: {
    page?: number;
    pageSize?: number;
    processDefinitionKey?: string;
    status?: string;
    businessKey?: string;
  }): Promise<ProcessInstanceListResponse> {
    const query: Record<string, string> = {};
    if (params?.page) query.page = String(params.page);
    if (params?.pageSize) query.pageSize = String(params.pageSize);
    if (params?.processDefinitionKey) query.processDefinitionKey = params.processDefinitionKey;
    if (params?.status) query.status = params.status;
    if (params?.businessKey) query.businessKey = params.businessKey;
    const response = await httpClient.get<BackendListResponse<ProcessInstance>>(
      `${this.baseUrl}/process-instances`,
      query
    );
    return { items: response.data, ...response.pagination };
  }

  static async getProcessInstance(id: string | number): Promise<ProcessInstance> {
    return httpClient.get(`${this.baseUrl}/process-instances/${encodeURIComponent(String(id))}`);
  }

  static async setProcessInstanceVariables(
    id: string | number,
    variables: Record<string, unknown>
  ): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/process-instances/${encodeURIComponent(String(id))}/variables`,
      { variables },
      { skipCamelCaseBody: true }
    );
  }

  static async suspendProcess(id: string | number, reason: string): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/process-instances/${encodeURIComponent(String(id))}/suspend`,
      { reason }
    );
  }

  static async resumeProcess(id: string | number): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/process-instances/${encodeURIComponent(String(id))}/resume`,
      {}
    );
  }

  static async terminateProcess(id: string | number, reason: string): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/process-instances/${encodeURIComponent(String(id))}/terminate`,
      { reason }
    );
  }

  static async listUserTasks(params?: {
    page?: number;
    pageSize?: number;
    processInstanceId?: number;
    processDefinitionKey?: string;
    businessType?: string;
    businessId?: number;
    status?: string;
  }): Promise<UserTaskListResponse> {
    const query: Record<string, string> = {};
    if (params?.page) query.page = String(params.page);
    if (params?.pageSize) query.pageSize = String(params.pageSize);
    if (params?.processInstanceId) query.processInstanceId = String(params.processInstanceId);
    if (params?.processDefinitionKey) query.processDefinitionKey = params.processDefinitionKey;
    if (params?.businessType) query.businessType = params.businessType;
    if (params?.businessId) query.businessId = String(params.businessId);
    if (params?.status) query.status = params.status;
    const response = await httpClient.get<BackendListResponse<UserTask>>(
      `${this.baseUrl}/tasks`,
      query
    );
    return { items: response.data, ...response.pagination };
  }

  static async getTask(id: string | number): Promise<UserTask> {
    return httpClient.get(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}`);
  }

  static async claimTask(id: string | number): Promise<void> {
    await httpClient.put(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/claim`, {});
  }

  static async assignTask(id: string | number, assignee: string): Promise<void> {
    await httpClient.put(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/assign`, {
      assignee,
    });
  }

  static async completeTask(id: string | number, data: CompleteTaskRequest = {}): Promise<void> {
    await httpClient.put(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/complete`, data);
  }

  static async submitApprovalDecision(
    id: string | number,
    data: SubmitApprovalDecisionRequest
  ): Promise<void> {
    await httpClient.post(
      `${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/decisions`,
      data
    );
  }

  static async getApprovalHistory(
    processInstanceId: string | number
  ): Promise<ProcessApprovalDecision[]> {
    return httpClient.get(
      `${this.baseUrl}/process-instances/${encodeURIComponent(String(processInstanceId))}/approval-history`
    );
  }

  static async getTicketApprovalDecisions(ticketId: number): Promise<ProcessApprovalDecision[]> {
    return httpClient.get(`/api/v1/tickets/${ticketId}/approval-decisions`);
  }

  static async cancelTask(id: string | number): Promise<void> {
    await httpClient.put(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/cancel`, {});
  }

  static async setTaskVariables(
    id: string | number,
    variables: Record<string, unknown>
  ): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/variables`,
      { variables },
      { skipCamelCaseBody: true }
    );
  }

  static async createCounterSignTasks(
    id: string | number,
    data: CounterSignRequest
  ): Promise<UserTask[]> {
    return httpClient.post(
      `${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/counter-sign`,
      data
    );
  }

  static async getCounterSignStatus(id: string | number): Promise<CounterSignStatus> {
    return httpClient.get(
      `${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/counter-sign-status`
    );
  }

  static async vote(id: string | number, approved: boolean, comment?: string): Promise<void> {
    await httpClient.put(`${this.baseUrl}/tasks/${encodeURIComponent(String(id))}/vote`, {
      approved,
      comment,
    });
  }

  static async getInstanceStats(params?: {
    processDefinitionKey?: string;
    startDate?: string;
    endDate?: string;
  }): Promise<InstanceStats> {
    return httpClient.get(`${this.baseUrl}/stats/instances`, params);
  }

  static async getTaskStats(params?: {
    processDefinitionKey?: string;
    startDate?: string;
    endDate?: string;
  }): Promise<TaskStats> {
    return httpClient.get(`${this.baseUrl}/stats/tasks`, params);
  }

  static async listVersions(processDefinitionKey: string): Promise<ProcessVersion[]> {
    return httpClient.get(`${this.baseUrl}/versions`, { process_key: processDefinitionKey });
  }

  static async getVersion(key: string, version: string | number): Promise<ProcessVersion> {
    return httpClient.get(
      `${this.baseUrl}/versions/${encodeURIComponent(key)}/${encodeURIComponent(String(version))}`
    );
  }

  static async createVersion(data: CreateVersionRequest): Promise<ProcessVersion> {
    return httpClient.post(`${this.baseUrl}/versions`, data);
  }

  static async activateVersion(key: string, version: string | number): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/versions/${encodeURIComponent(key)}/${encodeURIComponent(String(version))}/activate`,
      {}
    );
  }

  static async rollbackVersion(
    key: string,
    version: string | number,
    reason: string
  ): Promise<void> {
    await httpClient.put(
      `${this.baseUrl}/versions/${encodeURIComponent(key)}/${encodeURIComponent(String(version))}/rollback`,
      { reason }
    );
  }

  static async compareVersions(
    key: string,
    baseVersion: string | number,
    targetVersion: string | number
  ): Promise<VersionComparison | null> {
    return httpClient.get(`${this.baseUrl}/versions/${encodeURIComponent(key)}/compare`, {
      base_version: String(baseVersion),
      target_version: String(targetVersion),
    });
  }

  static async getVersionChangeLogs(
    key: string,
    params?: { page?: number; pageSize?: number }
  ): Promise<VersionChangeLog[]> {
    return httpClient.get(
      `${this.baseUrl}/process-definitions/${encodeURIComponent(key)}/changelogs`,
      params
    );
  }

  static async getVersionChangeLogById(id: number): Promise<VersionChangeLog> {
    return httpClient.get(`${this.baseUrl}/process-definitions/changelogs/${id}`);
  }
}

export default BPMNWorkflowApi;
