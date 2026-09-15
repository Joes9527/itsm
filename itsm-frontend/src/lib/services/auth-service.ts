import { useAuthStore } from '@/lib/store/auth-store';
import { httpClient } from '@/lib/api/http-client';
import type { User } from '@/lib/api/api-config';

export class AuthService {
  static getCurrentUser() {
    const { user } = useAuthStore.getState();
    return user;
  }

  // 检查是否已认证
  static isAuthenticated(): boolean {
    const { isAuthenticated } = useAuthStore.getState();
    if (isAuthenticated) return true;
    return false;
  }

  static async logout(): Promise<void> {
    await httpClient.post('/api/v1/auth/logout', {});
    useAuthStore.getState().logout();
  }

  // 修改login方法
  static async login(username: string, password: string, tenantCode?: string): Promise<User> {
    await httpClient.post('/api/v1/auth/login', { username, password, tenantCode });
    try {
      return await useAuthStore.getState().hydrateSession();
    } catch (error) {
      // A cookie session that cannot be projected authoritatively must not remain usable.
      await httpClient.post('/api/v1/auth/logout', {});
      throw error;
    }
  }

  // 注册
  static async register(params: {
    username: string;
    email: string;
    password: string;
    fullName: string;
    phone?: string;
    company?: string;
    role?: string;
  }): Promise<boolean> {
    try {
      await httpClient.post('/api/v1/auth/register', params);

      return true;
    } catch (error) {
      console.error('Registration failed:', error);
      return false;
    }
  }

  // 发送密码重置邮件
  static async forgotPassword(email: string, tenantCode?: string): Promise<boolean> {
    try {
      await httpClient.post('/api/v1/auth/forgot-password', { email, tenantCode });

      return true;
    } catch (error) {
      console.error('Forgot password request failed:', error);
      return false;
    }
  }

  // 重置密码
  static async resetPassword(params: {
    token: string;
    email: string;
    password: string;
    passwordConfirm: string;
  }): Promise<boolean> {
    try {
      await httpClient.post('/api/v1/auth/reset-password', params);

      return true;
    } catch (error) {
      console.error('Reset password failed:', error);
      return false;
    }
  }

  // 验证重置令牌
  static async validateResetToken(token: string, email: string): Promise<boolean> {
    try {
      const result = await httpClient.post<{ valid: boolean; email: string }>(
        '/api/v1/auth/validate-reset-token',
        { token, email }
      );

      return result.valid;
    } catch (error) {
      console.error('Validate reset token failed:', error);
      return false;
    }
  }
}

export default AuthService;
