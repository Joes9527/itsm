/**
 * 服务请求模块类型定义
 */

import type { ServiceRequestStatus } from '@/constants/service-request';
import type { TicketStatus } from '@/types/ticket';

// 服务目录简要信息 (用于内嵌在服务请求中)
export interface ServiceCatalogRef {
  id: number;
  name: string;
  category: string;
  description?: string;
}

// 请求者简要信息
export interface RequesterRef {
  id: number;
  username: string;
  name: string;
  email: string;
  department?: string;
}

// 服务请求实体 (对应后端 DTO dto.ServiceRequestResponse)
//
// 状态/标题/审批已经全部委托给关联的 Ticket（详见 itsm-backend/handlers/service_request）——
// ServiceRequest 自身不再持有 status/title/reason/currentLevel/totalLevels。ticketId 是
// 跳转到 /tickets/:ticketId 详情页（承载状态/审批/工作流）的唯一依据；ticketTitle/ticketStatus
// 是列表场景下后端批量回填的展示字段（非持久化，仅用于免去详情往返）。
export interface ServiceRequest {
  id: number;
  ticketId: number;
  catalogId: number;
  requesterId: number;
  formData?: Record<string, any>;
  customFields?: Array<{ name: string; label: string; value: unknown }>;

  costCenter?: string;
  dataClassification?: string;
  needsPublicIp?: boolean;
  sourceIpWhitelist?: string[];
  expireAt?: string;
  complianceAck: boolean;

  createdAt: string;
  updatedAt: string;

  // 列表展示用的关联 ticket 冗余字段（批量回填，见上方注释）
  ticketTitle?: string;
  ticketStatus?: TicketStatus | string;

  catalog?: ServiceCatalogRef; // 后端目前可能未填充，需注意
  requester?: RequesterRef; // 后端目前可能未填充，需注意
}

// 创建服务请求参数
export interface CreateServiceRequestRequest {
  catalogId: number;
  title?: string;
  reason?: string;
  formData?: Record<string, any>;

  costCenter?: string;
  dataClassification?: string;
  needsPublicIp?: boolean;
  sourceIpWhitelist?: string[];
  expireAt?: string;
  complianceAck: boolean;
}

// 审批动作请求参数
export interface ServiceRequestApprovalActionRequest {
  action: 'approve' | 'reject';
  comment?: string;
}

// 列表查询参数
export interface ServiceRequestQuery {
  page?: number;
  size?: number;
  status?: ServiceRequestStatus;
  scope?: 'me' | 'all'; // me: 我的请求, all: 管理员查看所有
}

// 列表响应
export interface ServiceRequestListResponse {
  requests: ServiceRequest[];
  total: number;
  page: number;
  size: number;
}
