import type { Tenant } from '@/lib/api/api-config';
import { API_BASE_URL } from '@/lib/api/api-config';
import { useAuthStore } from '@/lib/store/auth-store';

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

  // 直接使用fetch进行HTTP请求，避免循环依赖
  private static async makeRequest<T>(endpoint: string, options: RequestInit): Promise<T> {
    const url = `${API_BASE_URL}${endpoint}`;

    const response = await fetch(url, {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      credentials: options.credentials || 'include', // 默认包含cookies
      ...options,
    });

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    const responseData = (await response.json()) as { code: number; message: string; data: T };

    // 检查响应码
    if (responseData.code !== 0) {
      throw new Error(responseData.message || '请求失败');
    }

    return responseData.data;
  }

  // 登出方法
  static logout() {
    const { logout } = useAuthStore.getState();
    try {
      fetch(`${API_BASE_URL}/api/v1/auth/logout`, {
        method: 'POST',
        credentials: 'include',
      }).catch(() => {});
    } finally {
      logout();
    }
  }

  // 修改login方法
  static async login(
    username: string,
    password: string,
    tenantCode?: string
  ): Promise<boolean> {
    try {
      const data = await this.makeRequest<{
        user: unknown;
        tenant?: unknown;
      }>('/api/v1/auth/login', {
        method: 'POST',
        body: JSON.stringify({
          username,
          password,
          tenantCode: tenantCode,
        }),
      });

      // 使用store管理登录状态
      const { login } = useAuthStore.getState();
      const u = data.user as any;
      const t = data.tenant as any;
      login(
        {
          id: Number(u?.id || 0),
          username: String(u?.username || username),
          role: String(u?.role || 'end_user'),
          email: String(u?.email || ''),
          name: String(u?.name || u?.fullName || ''),
          tenantId: u?.tenantId ? Number(u.tenantId) : u?.tenantId ? Number(u.tenantId) : undefined,
          department: u?.department,
          permissions: u?.permissions,
          createdAt: u?.createdAt || u?.createdAt,
          updatedAt: u?.updatedAt || u?.updatedAt,
        },
        {
          id: Number(t?.id || u?.tenantId || 1),
          name: String(t?.name || '默认租户'),
          code: String(t?.code || tenantCode || 'default'),
          type: (t?.type || 'standard') as any,
          status: (t?.status || 'active') as any,
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        } as Tenant
      );

      return true;
    } catch (error) {
      console.error('Login failed:', error);
      return false;
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
      await this.makeRequest<{ id: number; username: string; email: string; message: string }>(
        '/api/v1/auth/register',
        {
          method: 'POST',
          body: JSON.stringify({
            username: params.username,
            email: params.email,
            password: params.password,
            fullName: params.fullName,
            phone: params.phone,
            company: params.company,
            role: params.role,
          }),
        }
      );

      return true;
    } catch (error) {
      console.error('Registration failed:', error);
      return false;
    }
  }

  // 发送密码重置邮件
  static async forgotPassword(email: string, tenantCode?: string): Promise<boolean> {
    try {
      await this.makeRequest<{ message: string }>('/api/v1/auth/forgot-password', {
        method: 'POST',
        body: JSON.stringify({
          email,
          tenantCode: tenantCode,
        }),
      });

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
      await this.makeRequest<{ message: string }>('/api/v1/auth/reset-password', {
        method: 'POST',
        body: JSON.stringify({
          token: params.token,
          email: params.email,
          password: params.password,
          passwordConfirm: params.passwordConfirm,
        }),
      });

      return true;
    } catch (error) {
      console.error('Reset password failed:', error);
      return false;
    }
  }

  // 验证重置令牌
  static async validateResetToken(token: string, email: string): Promise<boolean> {
    try {
      const result = await this.makeRequest<{ valid: boolean; email: string }>(
        '/api/v1/auth/validate-reset-token',
        {
          method: 'POST',
          body: JSON.stringify({
            token,
            email,
          }),
        }
      );

      return result.valid;
    } catch (error) {
      console.error('Validate reset token failed:', error);
      return false;
    }
  }
}

export default AuthService;
