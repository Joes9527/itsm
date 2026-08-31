/**
 * 服务目录 API 服务
 */

import { httpClient } from './http-client';
import { idempotencyKeyFor } from './idempotency-key';
import type {
  ServiceItem,
  ServiceStatus,
  ServiceCategory,
  ServiceRequestStatus,
  PortalConfig,
  ServiceFavorite,
  ServiceRating,
  ServiceCatalogStats,
  ServiceAnalytics,
  CreateServiceItemRequest,
  UpdateServiceItemRequest,
  CreateServiceRequestRequest,
  ServiceQuery,
  ServiceRequestQuery,
} from '@/types/service-catalog';

export class ServiceCatalogApi {
  // ==================== 内部适配（对齐后端 /api/v1/service-catalogs & /api/v1/service-requests） ====================

  private static unsupportedFeature(feature: string): never {
    throw new Error(`${feature}暂未接入后端，请在能力开放前关闭入口或补齐服务端接口。`);
  }

  private static toBackendStatus(status?: unknown): 'enabled' | 'disabled' | undefined {
    // V0：后端服务目录状态枚举为 enabled/disabled；前端为 draft/published/retired
    if (!status) return undefined;
    const s = String(status);
    if (s === 'published') return 'enabled';
    if (s === 'enabled') return 'enabled';
    if (s === 'disabled') return 'disabled';
    return 'disabled';
  }

  private static toFrontendStatus(status?: unknown) {
    const s = String(status || '');
    return (s === 'enabled' ? 'published' : 'retired') as ServiceStatus;
  }

  private static escapeCSV(value: unknown): string {
    const text =
      value instanceof Date
        ? value.toISOString()
        : value === undefined || value === null
          ? ''
          : String(value);
    return `"${text.replace(/"/g, '""')}"`;
  }

  // 状态已经委托给关联 Ticket（值域是 new/open/in_progress/pending/resolved/closed/cancelled）；
  // 旧的 SR 专属状态（submitted/security_approved/provisioning/delivered 等）已随三级审批一起退役，
  // 这里不再做值域转换，直接透传调用方传入的 ticket 状态值。
  private static toBackendRequestStatus(status?: unknown): string | undefined {
    if (!status) return undefined;
    return String(status);
  }

   
  private static toServiceItem(raw: any): ServiceItem {
    // 后端 dto.ServiceCatalogResponse: {id,name,category,description,deliveryTime,status,ciTypeId,cloudServiceId,createdAt,updatedAt}
    return {
      id: String(raw?.id),
      name: String(raw?.name || ''),
      // 这里保留后端 category 的原始字符串（前端页面目前以中文分类做统计/图标）
       
      category: (raw?.category as ServiceCategory) || ('it_service' as ServiceCategory),
      status: ServiceCatalogApi.toFrontendStatus(raw?.status),
      shortDescription: String(raw?.description || ''),
      fullDescription: String(raw?.description || ''),
      ciTypeId: typeof raw?.ciTypeId === 'number' ? raw.ciTypeId : undefined,
      cloudServiceId: typeof raw?.cloudServiceId === 'number' ? raw.cloudServiceId : undefined,
      tags: [],
      requiresApproval: true,
      createdBy: 0,
      createdByName: '',
      createdAt: raw?.createdAt ? new Date(raw.createdAt) : new Date(),
      updatedAt: raw?.updatedAt ? new Date(raw.updatedAt) : new Date(),
      availability: {
        // 后端 deliveryTime 为 string（天/小时口径未统一）；V0先用于展示，不做严格含义
        responseTime: raw?.deliveryTime ? Number(raw.deliveryTime) : undefined,
      },
      fields: Array.isArray(raw?.fields) ? raw.fields : [],
      processDefinitionKey: raw?.processDefinitionKey || undefined,
      serviceType: raw?.serviceType || undefined,
		targetClass: raw?.targetClass,
      requiresInfraFields: Boolean(raw?.requiresInfraFields),
    };
  }

   
  private static toServiceRequest(raw: any): any {
    // Bug 修复：清理重复的 `raw?.field ?? raw?.field` fallback 表达式。
    // 这是复制粘贴残留，不会引发运行时错误，但会误导阅读并掩盖潜在缺陷。
    const catalogId = raw?.catalogId ?? raw?.serviceId;
    const requesterId = raw?.requesterId ?? raw?.requestedBy;
    const createdAt = raw?.createdAt;
    const updatedAt = raw?.updatedAt;
    const catalog = raw?.catalog || {
      id: catalogId,
      name: raw?.serviceName || raw?.title || (catalogId ? `服务 #${catalogId}` : '未知服务'),
      category: raw?.category || '',
      description: raw?.reason || '',
    };
    const requester = raw?.requester || {
      id: requesterId,
      name: raw?.requesterName || raw?.requestedByName || (requesterId ? `用户 #${requesterId}` : '-'),
      email: raw?.requestedByEmail || '',
    };

    return {
      ...raw,
      requestNumber: raw?.requestNumber || `REQ-${String(raw?.id || 0).padStart(5, '0')}`,
      serviceId: String(catalogId || ''),
      serviceName: raw?.serviceName || catalog?.name || '-',
      requesterName: raw?.requesterName || requester?.name || '-',
      requestedBy: requesterId,
      requestedByName: raw?.requestedByName || requester?.name || '-',
      catalog,
      requester,
      catalogId,
      requesterId,
      ciId: raw?.ciId,
      formData: raw?.formData ?? {},
      costCenter: raw?.costCenter,
      dataClassification: raw?.dataClassification,
      needsPublicIp: raw?.needsPublicIp ?? raw?.needsPublicIP,
      sourceIpWhitelist: raw?.sourceIpWhitelist ?? raw?.sourceIPWhitelist,
      complianceAck: raw?.complianceAck,
      currentLevel: raw?.currentLevel,
      totalLevels: raw?.totalLevels,
      expireAt: raw?.expireAt,
      createdAt,
      updatedAt,
    };
  }

  // ==================== 服务项管理 ====================

  /**
   * 获取服务列表
   */
  static async getServices(query?: ServiceQuery): Promise<{
    services: ServiceItem[];
    total: number;
  }> {
    const page = query?.page ?? 1;
    const size = query?.pageSize ?? 10;
    const category = query?.category ? String(query.category) : undefined;
    const status = ServiceCatalogApi.toBackendStatus(query?.status);

    const resp = await httpClient.get<{
      catalogs: unknown[];
      services: unknown[];
      total: number;
      page: number;
      size: number;
    }>('/api/v1/service-catalogs', {
      page,
      size,
      ...(category ? { category } : {}),
      ...(status ? { status } : {}),
    });

    // 优先使用 services 字段，否则降级使用 catalogs
    const rawItems = resp.services || resp.catalogs || [];

    let services = rawItems.map(ServiceCatalogApi.toServiceItem);
    // 后端当前不支持 search；先在前端做兜底过滤
    if (query?.search) {
      const q = query.search.toLowerCase();
      services = services.filter(
        s =>
          (s.name || '').toLowerCase().includes(q) ||
          (s.shortDescription || '').toLowerCase().includes(q)
      );
    }

    return { services, total: resp.total || 0 };
  }

  /**
   * 获取单个服务
   */
  static async getService(id: string): Promise<ServiceItem> {
    const resp = await httpClient.get<any>(`/api/v1/service-catalogs/${id}`);
    return ServiceCatalogApi.toServiceItem(resp);
  }

  /**
   * 创建服务
   */
  static async createService(request: CreateServiceItemRequest): Promise<ServiceItem> {
    const payload = {
      name: request.name,
      category: String(request.category),
      description: request.shortDescription || request.fullDescription || '',
      ciTypeId: request.ciTypeId,
      cloudServiceId: request.cloudServiceId,
      deliveryTime: String(
        request.availability?.responseTime ?? request.availability?.resolutionTime ?? 1
      ),
      status: ServiceCatalogApi.toBackendStatus(request.status) || 'enabled',
      fields: request.fields,
      processDefinitionKey: request.processDefinitionKey,
      serviceType: request.serviceType ? String(request.serviceType) : undefined,
		targetClass: request.targetClass,
    };
    const resp = await httpClient.post<any>('/api/v1/service-catalogs', payload);
    return ServiceCatalogApi.toServiceItem(resp);
  }

  /**
   * 更新服务
   */
  static async updateService(id: string, request: UpdateServiceItemRequest): Promise<ServiceItem> {
    const payload: Record<string, unknown> = {};
    if (request.name !== undefined) payload.name = request.name;
    if (request.category !== undefined) payload.category = String(request.category);
    if (request.shortDescription !== undefined || request.fullDescription !== undefined) {
      payload.description = request.shortDescription || request.fullDescription || '';
    }
    if (request.availability?.responseTime !== undefined) {
      payload.deliveryTime = String(request.availability.responseTime);
    }
    if (request.ciTypeId !== undefined) payload.ciTypeId = request.ciTypeId;
    if (request.cloudServiceId !== undefined) payload.cloudServiceId = request.cloudServiceId;
    if (request.fields !== undefined) payload.fields = request.fields;
    if (request.processDefinitionKey !== undefined) {
      payload.processDefinitionKey = request.processDefinitionKey;
    }
    if (request.serviceType !== undefined) {
      payload.serviceType = String(request.serviceType);
    }
	payload.targetClass = request.targetClass;
    const st = ServiceCatalogApi.toBackendStatus(request.status);
    if (st) payload.status = st;

    const resp = await httpClient.put<any>(`/api/v1/service-catalogs/${id}`, payload);
    return ServiceCatalogApi.toServiceItem(resp);
  }

  /**
   * 删除服务
   */
  static async deleteService(id: string): Promise<void> {
    return httpClient.delete(`/api/v1/service-catalogs/${id}`);
  }

  /**
   * 发布服务
   */
  static async publishService(id: string): Promise<ServiceItem> {
	const current = await ServiceCatalogApi.getService(id);
    const resp = await httpClient.put<any>(`/api/v1/service-catalogs/${id}`, {
      status: 'enabled',
	  targetClass: current.targetClass,
    });
    return ServiceCatalogApi.toServiceItem(resp);
  }

  /**
   * 停用服务
   */
  static async retireService(id: string): Promise<ServiceItem> {
	const current = await ServiceCatalogApi.getService(id);
    const resp = await httpClient.put<any>(`/api/v1/service-catalogs/${id}`, {
      status: 'disabled',
	  targetClass: current.targetClass,
    });
    return ServiceCatalogApi.toServiceItem(resp);
  }

  /**
   * 复制服务
   */
  static async cloneService(id: string, name: string): Promise<ServiceItem> {
    const src = await ServiceCatalogApi.getService(id);
    const { id: _omit, ...rest } = src;
    return ServiceCatalogApi.createService({
      ...rest,
      name,
    });
  }

  // ==================== 服务请求管理 ====================

  /**
   * 获取服务请求列表
   */
  static async getServiceRequests(query?: ServiceRequestQuery): Promise<{
    requests: unknown[];
    total: number;
  }> {
    const page = query?.page ?? 1;
    const size = query?.pageSize ?? 10;
    const requestedStatus = query?.status ? String(query.status) : undefined;
    const status = ServiceCatalogApi.toBackendRequestStatus(requestedStatus);

    const resp = await httpClient.get<any>('/api/v1/service-requests/me', {
      page,
      size,
      ...(status ? { status } : {}),
    });
    const rawRequests = resp.requests || resp.items || [];
    return {
      requests: rawRequests.map(ServiceCatalogApi.toServiceRequest),
      total: resp.total || 0,
    };
  }

  /**
   * 获取单个服务请求
   */
  static async getServiceRequest(id: number): Promise<any> {
    const resp = await httpClient.get<any>(`/api/v1/service-requests/${id}`);
    return ServiceCatalogApi.toServiceRequest(resp);
  }

  /**
   * 按关联的 ticketId 查服务请求（供工单详情页的 ServiceRequestPanel 用）
   */
  static async getServiceRequestByTicketId(ticketId: number): Promise<any> {
    const resp = await httpClient.get<any>(`/api/v1/service-requests/by-ticket/${ticketId}`);
    return ServiceCatalogApi.toServiceRequest(resp);
  }

  /**
   * 创建服务请求
   *
   * 返回值透传后端 dto.ServiceRequestResponse（不经过 toServiceRequest 适配），其中
   * ticketId 是提交成功后创建的关联 Ticket ID——调用方（提交表单页）据此跳转到
   * /tickets/:ticketId，服务请求已经不再有独立详情页。
   */
  static async createServiceRequest(
    request: CreateServiceRequestRequest,
    idempotencyKey?: string
  ): Promise<{ ticketId: number } & Record<string, any>> {
    const key = idempotencyKeyFor(request, idempotencyKey);
    // 前端 CreateServiceRequestRequest: { serviceId, formData, ... }
    // 后端 CreateServiceRequestRequest: { catalog_id, title, reason, form_data, ... , compliance_ack }
    const reason =
      (request.formData && (request.formData.reason || request.formData.notes)) ||
      request.additionalNotes ||
      '';

    const title = (request.formData && (request.formData.title || request.formData.name)) || '';

    // V0：最小字段集合。复杂字段（成本中心/分级/到期/公网白名单）可先从 formData 透传，后续再做强校验与表单化。
    const payload: unknown = {
      catalogId: Number(request.serviceId),
      title: title ? String(title) : undefined,
      reason,
      formData: request.formData || {},
      // 合规确认绝不能静默默认为已勾选——没有 ?? true 兜底，调用方忘传就是 false。
      complianceAck: Boolean(request.formData?.complianceAck),
      dataClassification: String(request.formData?.dataClassification || 'internal'),
      needsPublicIp: Boolean(request.formData?.needsPublicIp || false),
      sourceIpWhitelist: Array.isArray(request.formData?.sourceIpWhitelist)
        ? request.formData?.sourceIpWhitelist
        : undefined,
      costCenter: request.formData?.costCenter
        ? String(request.formData?.costCenter)
        : undefined,
      expireAt: request.formData?.expireAt ? request.formData?.expireAt : undefined,
      // 通用层字段：所有 service_type 都适用，真正落到后端 ContactName/ContactEmail/
      // Quantity/ExpectedAt 列。直接映射到新增列，不再经过 formData JSON 兜底路径
      // （见 docs/superpowers/specs/2026-08-21-service-catalog-request-form-redesign-design.md
      // §3.5），所以从 request 顶层读取而不是 request.formData。
      contactName: request.contactName ? String(request.contactName) : undefined,
      contactEmail: request.contactEmail ? String(request.contactEmail) : undefined,
      quantity: request.quantity ? Number(request.quantity) : undefined,
      expectedAt: request.expectedAt ? request.expectedAt : undefined,
    };

    return httpClient.post<{ ticketId: number } & Record<string, any>>(
      '/api/v1/service-requests',
      payload,
      { headers: { 'Idempotency-Key': key } }
    );
  }

  // cancelServiceRequest/approveServiceRequest/rejectServiceRequest/completeServiceRequest/
  // getPendingApprovalCount 已经移除——它们打在 Task 1 删除的
  // /api/v1/service-requests/:id/status 和 /api/v1/service-requests/:id/approval 及
  // /api/v1/service-requests/approvals/pending 路由上（SR 自己的审批阶段/终态操作整体退休，
  // 状态/审批全部委托给关联 Ticket）。删除前确认过没有真实调用方：唯一的调用方
  // src/app/(main)/service-catalog/approvals/page.tsx 已经改造成重定向到 /approvals/pending。

  /**
   * 获取服务请求详情（包含审批历史）
   */
  static async getServiceRequestDetail(id: number): Promise<any> {
    const response = await httpClient.get(`/api/v1/service-requests/${id}`);
    return response;
  }

  // ==================== 收藏和评分 ====================

  /**
   * 添加收藏
   */
  static async addFavorite(serviceId: string): Promise<ServiceFavorite> {
     
    const _serviceId = serviceId;
    return ServiceCatalogApi.unsupportedFeature('服务收藏');
  }

  /**
   * 取消收藏
   */
  static async removeFavorite(serviceId: string): Promise<void> {
     
    const _serviceId = serviceId;
    ServiceCatalogApi.unsupportedFeature('服务收藏');
  }

  /**
   * 获取收藏列表
   */
  static async getFavorites(): Promise<ServiceFavorite[]> {
    // 后端未提供收藏列表接口。读取路径返回空列表，写入路径必须显式失败，避免用户误以为收藏已保存。
    return [];
  }

  /**
   * 评分服务
   */
  static async rateService(
    serviceId: string,
    rating: number,
    comment?: string
  ): Promise<ServiceRating> {
     
    const _args = { serviceId, rating, comment };
    return ServiceCatalogApi.unsupportedFeature('服务评分');
  }

  /**
   * 获取服务评分
   */
  static async getServiceRatings(
    serviceId: string,
    params?: {
      page?: number;
      pageSize?: number;
    }
  ): Promise<{
    ratings: ServiceRating[];
    total: number;
    avgRating: number;
  }> {
    return { ratings: [], total: 0, avgRating: 0 };
  }

  /**
   * 标记评分有用
   */
  static async markRatingHelpful(ratingId: string): Promise<void> {
     
    const _ratingId = ratingId;
    ServiceCatalogApi.unsupportedFeature('评分有用标记');
  }

  // ==================== 门户配置 ====================

  /**
   * 获取门户配置
   */
  static async getPortalConfig(): Promise<PortalConfig> {
    // 本地只提供只读默认配置；未接后端的能力默认关闭，避免页面展示不可持久化操作。
    return {
      id: 'default',
      name: '默认门户',
      branding: {
        primaryColor: '#1890ff',
      },
      homepage: {},
      features: {
        enableSearch: true,
        enableRating: false,
        enableFavorites: false,
        enableNotifications: false,
        showServicePrice: false,
        showServiceOwner: true,
      },
      updatedAt: new Date(),
    };
  }

  /**
   * 更新门户配置
   */
  static async updatePortalConfig(config: Partial<PortalConfig>): Promise<PortalConfig> {
     
    const _config = config;
    return ServiceCatalogApi.unsupportedFeature('门户配置更新');
  }

  // ==================== 统计和分析 ====================

  /**
   * 获取服务目录统计
   */
  static async getCatalogStats(): Promise<ServiceCatalogStats> {
    // 调用后端实际统计接口
    const resp = await httpClient.get<{
      totalServices: number;
      publishedServices: number;
      categories: Record<string, number>;
    }>('/api/v1/service-catalogs/stats');

    return {
      totalServices: resp.totalServices || 0,
      publishedServices: resp.publishedServices || 0,
      totalRequests: 0,
      pendingRequests: 0,
      completedRequests: 0,
      servicesByCategory: {} as Record<ServiceCategory, number>,
      requestsByStatus: {} as Record<ServiceRequestStatus, number>,
      topServices: [],
      recentRequests: [],
      trends: [],
    };
  }

  /**
   * 获取服务分析
   */
  static async getServiceAnalytics(
    serviceId: string,
    params?: {
      startDate?: string;
      endDate?: string;
    }
  ): Promise<ServiceAnalytics> {
    // 后端暂未实现服务分析，返回空数据
     
    const _unused = serviceId;
    return {
      serviceId,
      period: {
        start: params?.startDate
          ? new Date(params.startDate)
          : new Date(Date.now() - 30 * 24 * 60 * 60 * 1000),
        end: params?.endDate ? new Date(params.endDate) : new Date(),
      },
      metrics: {
        totalRequests: 0,
        completedRequests: 0,
        avgCompletionTime: 0,
        completionRate: 0,
        avgRating: 0,
        totalViews: 0,
      },
      requestTrend: [],
      userSatisfaction: [],
      peakHours: [],
    };
  }

  /**
   * 记录服务浏览
   */
  static async recordServiceView(serviceId: string): Promise<void> {
    // V0：不做
    return;
  }

  /**
   * 导出服务目录
   */
  static async exportCatalog(format: 'excel' | 'pdf'): Promise<Blob> {
    const response = await ServiceCatalogApi.getServices({ page: 1, pageSize: 1000 });
    const header = [
      'ID',
      '服务名称',
      '分类',
      '状态',
      '描述',
      '交付时间',
      '创建时间',
      '更新时间',
    ];
    const rows = response.services.map(service => [
      service.id,
      service.name,
      service.category,
      service.status,
      service.shortDescription,
      service.availability?.responseTime || '',
      service.createdAt,
      service.updatedAt,
    ]);
    const csv = [header, ...rows]
      .map(row => row.map(ServiceCatalogApi.escapeCSV).join(','))
      .join('\n');
    const type = format === 'pdf' ? 'text/csv;charset=utf-8' : 'text/csv;charset=utf-8';
    return new Blob([`\uFEFF${csv}`], { type });
  }
}

export default ServiceCatalogApi;
export const ServiceCatalogAPI = ServiceCatalogApi;
