import { httpClient } from '@/lib/api/http-client';

export interface ServiceRequest {
  id: number;
  catalogId: number;
  requesterId: number;
  status:
    | 'submitted'
    | 'manager_approved'
    | 'it_approved'
    | 'security_approved'
    | 'provisioning'
    | 'delivered'
    | 'failed'
    | 'rejected'
    | 'cancelled';
  title?: string;
  reason?: string;
  formData?: Record<string, unknown>;
  costCenter?: string;
  dataClassification?: 'public' | 'internal' | 'confidential' | 'restricted';
  needsPublicIp?: boolean;
  sourceIpWhitelist?: string[];
  expireAt?: string | null;
  complianceAck?: boolean;
  currentLevel?: number;
  totalLevels?: number;
  version: number;
  processorId?: number;
  approvedAt?: string;
  startedAt?: string;
  completedAt?: string;
  completionNote?: string;
  lastError?: string;
  createdAt: string;
  catalog?: {
    id: number;
    name: string;
    category: string;
    description: string;
    deliveryTime: string;
  };
  requester?: {
    id: number;
    username: string;
    name: string;
    email: string;
    department: string;
  };
}

export interface ServiceRequestListResponse {
  requests: ServiceRequest[];
  total: number;
  page: number;
  size: number;
}

export interface CreateServiceRequestRequest {
  catalogId: number;
  title?: string;
  reason?: string;
  formData?: Record<string, unknown>;
  costCenter?: string;
  dataClassification?: 'public' | 'internal' | 'confidential' | 'restricted';
  needsPublicIp?: boolean;
  sourceIpWhitelist?: string[];
  expireAt?: string;
  complianceAck: boolean;
}

class ServiceRequestAPI {
  private normalizeRequest(raw: ServiceRequest): ServiceRequest {
    return {
      ...raw,
      formData: raw?.formData ?? {},
    };
  }

  private normalizeList(raw: ServiceRequestListResponse): ServiceRequestListResponse {
    if (!Array.isArray(raw.requests)) {
      throw new Error('Invalid service request list contract: requests is required');
    }
    const requests = raw.requests.map(item => this.normalizeRequest(item));
    return {
      requests,
      total: raw.total,
      page: raw.page,
      size: raw.size,
    };
  }

  // Get current user's service request list
  async getUserServiceRequests(
    params: {
      page?: number;
      size?: number;
      status?: string;
    } = {}
  ): Promise<ServiceRequestListResponse> {
    const searchParams = new URLSearchParams();

    if (params.page) searchParams.append('page', params.page.toString());
    if (params.size) searchParams.append('size', params.size.toString());
    if (params.status && params.status !== 'all') searchParams.append('status', params.status);

    const resp = await httpClient.get<ServiceRequestListResponse>(
      `/api/v1/service-requests/me?${searchParams.toString()}`
    );
    return this.normalizeList(resp);
  }

  // Get service request details
  async getServiceRequestDetails(id: number): Promise<ServiceRequest> {
    const resp = await httpClient.get<ServiceRequest>(`/api/v1/service-requests/${id}`);
    return this.normalizeRequest(resp);
  }

  // Health check
  async healthCheck(): Promise<{ status: string }> {
    // backend exposes public health endpoint under /api/v1/health
    return httpClient.get<{ status: string }>('/api/v1/health');
  }

  // ==================== Provisioning Tasks ====================

  // Start provisioning for a service request
  async startProvisioning(serviceRequestId: number): Promise<{ task: ProvisioningTask }> {
    return httpClient.post<{ task: ProvisioningTask }>(
      `/api/v1/service-requests/${serviceRequestId}/provision`, {}
    );
  }

  // List provisioning tasks for a service request
  async listProvisioningTasks(serviceRequestId: number): Promise<ProvisioningTask[]> {
    return httpClient.get<ProvisioningTask[]>(
      `/api/v1/service-requests/${serviceRequestId}/provisioning-tasks`
    );
  }

  // Execute a provisioning task
  async executeProvisioningTask(taskId: number): Promise<ProvisioningTask> {
    return httpClient.post<ProvisioningTask>(`/api/v1/provisioning-tasks/${taskId}/execute`, {});
  }

}

export interface ProvisioningTask {
  id: number;
  serviceRequestId: number;
  provider: string;
  resourceType: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed';
  payload?: Record<string, unknown>;
  result?: Record<string, unknown>;
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
}

export const serviceRequestAPI = new ServiceRequestAPI();
