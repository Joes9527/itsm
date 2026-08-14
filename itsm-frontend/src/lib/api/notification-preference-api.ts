import { httpClient } from './http-client';

/**
 * 用户通知偏好设置接口
 */

// 通知事件类型
export interface NotificationEventType {
  code: string;
  name: string;
  description: string;
}

// 单个事件类型的偏好（4 渠道）
export interface EventTypePreference {
  eventType: string;
  emailEnabled: boolean;
  smsEnabled: boolean;
  inAppEnabled: boolean;
  pushEnabled: boolean;
  timezone?: string;
}

// 通知偏好设置类型
export interface NotificationPreference {
  id: number;
  userId: number;
  emailEnabled: boolean;
  smsEnabled: boolean;
  pushEnabled: boolean;
  wechatEnabled: boolean;
  notificationTypes: string[];
  quietHoursStart?: string;
  quietHoursEnd?: string;
  language: string;
  timezone: string;
  createdAt: string;
  updatedAt: string;
}

// 创建/更新通知偏好请求
export interface NotificationPreferenceRequest {
  emailEnabled?: boolean;
  smsEnabled?: boolean;
  pushEnabled?: boolean;
  wechatEnabled?: boolean;
  notificationTypes?: string[];
  quietHoursStart?: string;
  quietHoursEnd?: string;
  language?: string;
  timezone?: string;
}

/**
 * 通知偏好设置 API
 */
export class NotificationPreferenceApi {
  private static baseURL = '/api/v1/notification-preferences';

  /**
   * 获取当前用户的通知偏好设置列表（兼容 profile 页面）
   * 返回 { preferences: [...], eventTypes: [...] } 格式
   */
  static async getPreferences(): Promise<{
    preferences: EventTypePreference[];
    eventTypes: NotificationEventType[];
  }> {
    return httpClient.get<{
      preferences: EventTypePreference[];
      eventTypes: NotificationEventType[];
    }>(`${this.baseURL}`);
  }

  /**
   * 获取当前用户的通知偏好设置
   */
  static async getMyPreferences(): Promise<NotificationPreference> {
    return httpClient.get<NotificationPreference>(`${this.baseURL}`);
  }

  /**
   * 获取指定用户的通知偏好设置
   */
  static async getPreferencesByUserId(userId: number): Promise<NotificationPreference> {
    return httpClient.get<NotificationPreference>(`${this.baseURL}/${userId}`);
  }

  /**
   * 更新当前用户的通知偏好设置
   */
  static async updateMyPreferences(
    preferences: NotificationPreferenceRequest
  ): Promise<NotificationPreference> {
    return httpClient.put<NotificationPreference>(`${this.baseURL}/me`, preferences);
  }

  /**
   * 更新指定用户的通知偏好设置
   */
  static async updatePreferences(
    userId: number,
    preferences: NotificationPreferenceRequest
  ): Promise<NotificationPreference> {
    return httpClient.put<NotificationPreference>(`${this.baseURL}/${userId}`, preferences);
  }

  /**
   * 重置通知偏好设置为默认值
   */
  static async resetToDefault(): Promise<NotificationPreference> {
    return httpClient.post<NotificationPreference>(`${this.baseURL}/me/reset`);
  }

  /**
   * 获取通知偏好设置模板列表
   */
  static async getTemplates(): Promise<NotificationPreference[]> {
    return httpClient.get<NotificationPreference[]>(`${this.baseURL}/templates`);
  }

  /**
   * 应用通知偏好设置模板
   */
  static async applyTemplate(templateId: number): Promise<NotificationPreference> {
    return httpClient.post<NotificationPreference>(`${this.baseURL}/me/apply-template`, {
      templateId,
    });
  }

  /**
   * 批量更新通知偏好设置（按事件类型 × 4 渠道）
   */
  static async bulkUpdate(data: {
    preferences: EventTypePreference[];
  }): Promise<{ preferences: unknown[] }> {
    return httpClient.put<{ preferences: unknown[] }>(`${this.baseURL}`, data);
  }
}
