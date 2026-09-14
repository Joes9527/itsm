import { httpClient } from '@/lib/api/http-client';

import type { Change, ChangeListResponse, ChangeStatsResponse } from '@/lib/api/change-api';
export type { Change, ChangeListResponse };
export type ChangeStats = ChangeStatsResponse;

class ChangeService {
  private readonly baseUrl = '/api/v1/changes';

  // 获取变更列表
  async getChanges(params: {
    page?: number;
    pageSize?: number;
    status?: string;
    search?: string;
  }): Promise<ChangeListResponse> {
    return httpClient.get<ChangeListResponse>(this.baseUrl, params);
  }

  // 获取变更详情
  async getChange(id: number): Promise<Change> {
    return httpClient.request<Change>(`${this.baseUrl}/${id}`, { preserveResponseKeys: true });
  }

  // 删除变更
  async deleteChange(id: number): Promise<void> {
    return httpClient.delete(`${this.baseUrl}/${id}`);
  }

  // 获取变更统计
  async getChangeStats(): Promise<ChangeStats> {
    return httpClient.get<ChangeStats>(`${this.baseUrl}/stats`);
  }

  // 健康检查
  async healthCheck(): Promise<boolean> {
    try {
      await httpClient.get<{ status: string }>('/api/v1/health');
      return true;
    } catch {
      return false;
    }
  }
}

export const changeService = new ChangeService();
