/**
 * 工单分类 API 服务
 */

import { httpClient } from './http-client';

/** CTI 的最大层级（Category → Type → Item）。 */
export const CTI_MAX_LEVEL = 3;
/** 完成质量门禁要求的完整层级。 */
export const CTI_COMPLETE_LEVEL = 3;

/**
 * CTIPathNode 是分类完整路径的只读投影（根 → 最深节点）。
 * 工单只保存所选最深节点，绝不保存第二套 C/T/I 文本权威。
 */
export interface CTIPathNode {
  id: number;
  parentId: number | null;
  level: number;
  name: string;
  code: string;
  isActive: boolean;
}

// 工单分类接口
export interface TicketCategory {
  id: number;
  name: string;
  code: string;
  description: string;
  parentId: number | null;
  level: number;
  /** 派生的根→自身完整路径（C / T / I 名称），用于维护界面路径搜索与回显。 */
  path?: string;
  /** 派生路径的节点 ID（根 → 自身）。 */
  pathIds?: number[];
  sortOrder: number;
  isActive: boolean;
  departmentId?: number | null;
  ticketCount?: number;
  children?: TicketCategory[];
  createdAt: string;
  updatedAt: string;
}

// 创建分类请求
export interface CreateCategoryRequest {
  name: string;
  code: string;
  description?: string;
  parentId?: number;
  sortOrder?: number;
  isActive?: boolean;
  departmentId?: number;
}

// 更新分类请求
export interface UpdateCategoryRequest {
  name?: string;
  code?: string;
  description?: string;
  parentId?: number;
  sortOrder?: number;
  isActive?: boolean;
  departmentId?: number;
}

export class TicketCategoryApi {
  // 获取分类列表 - 支持两种后端响应格式
  static async getCategories(params?: {
    page?: number;
    pageSize?: number;
    parentId?: number;
    isActive?: boolean;
    keyword?: string;
  }): Promise<{
    categories?: TicketCategory[];
    items?: TicketCategory[];
    total: number;
  }> {
    return httpClient.get('/api/v1/ticket-categories', params);
  }

  // 获取分类树形结构。
  // 默认只返回启用节点（选择器/申请入口）；维护界面需要看到停用节点与状态时显式要求。
  static async getCategoryTree(options?: { includeInactive?: boolean }): Promise<TicketCategory[]> {
    if (options?.includeInactive) {
      return httpClient.get('/api/v1/ticket-categories/tree', { includeInactive: true });
    }
    return httpClient.get('/api/v1/ticket-categories/tree');
  }

  // 获取单个分类
  static async getCategory(id: number): Promise<TicketCategory> {
    return httpClient.get(`/api/v1/ticket-categories/${id}`);
  }

  // 创建分类：租户由后端会话上下文唯一决定。
  static async createCategory(data: CreateCategoryRequest): Promise<TicketCategory> {
    return httpClient.post('/api/v1/ticket-categories', data);
  }

  // 更新分类
  static async updateCategory(id: number, data: UpdateCategoryRequest): Promise<TicketCategory> {
    return httpClient.put(`/api/v1/ticket-categories/${id}`, data);
  }

  // 删除分类
  static async deleteCategory(id: number): Promise<void> {
    return httpClient.delete(`/api/v1/ticket-categories/${id}`);
  }

  // 预览导入数据
  static async previewImport(formData: FormData): Promise<
    {
      name: string;
      code: string;
      description: string;
      parentCode: string;
      sortOrder: number;
      isActive: boolean;
    }[]
  > {
    return httpClient.post('/api/v1/ticket-categories/import/preview', formData);
  }

  // 执行导入
  static async executeImport(formData: FormData): Promise<{ success: number; failed: number }> {
    return httpClient.post('/api/v1/ticket-categories/import', formData);
  }
}

export default TicketCategoryApi;
