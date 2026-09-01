/**
 * 统一的认证状态管理 Store
 * 合并了 tenant 支持和 permissions 系统
 * 使用 Zustand 管理当前页面内由 `/auth/me` 验证的会话投影。
 */

import { create } from 'zustand';
import type { User, Tenant } from '@/lib/api/api-config';
import { httpClient } from '@/lib/api/http-client';

// ===================================
// 类型定义
// ===================================

// 使用 api-config 中的 User 和 Tenant 定义，避免重复定义

interface AuthState {
  // 状态
  user: User | null;
  currentTenant: Tenant | null;
  isAuthenticated: boolean;
  isLoading: boolean;

  // 认证操作
  hydrateSession: () => Promise<User>;
  logout: () => void;
  updateUser: (user: Partial<User>) => void;
  setLoading: (loading: boolean) => void;

  // 权限检查
  hasPermission: (permission: string) => boolean;
  hasRole: (role: string) => boolean;
  isAdmin: () => boolean;
}

// ===================================
// Store 定义
// ===================================

export const useAuthStore = create<AuthState>()((set, get) => ({
  // 初始状态
  user: null,
  currentTenant: null,
  isAuthenticated: false,
  isLoading: false,

  // `/auth/me` 与其精确匹配的授权租户是浏览器会话投影的唯一来源。
  hydrateSession: async () => {
    set({ isLoading: true });
    try {
      const user = await httpClient.get<User>('/api/v1/auth/me');
      const actorID = Number(user?.id);
      if (!Number.isInteger(actorID) || actorID <= 0 || !String(user?.username || '').trim()) {
        throw new Error('Invalid authenticated actor response');
      }
      const role = String(user?.role || '').trim();
      if (!role) {
        throw new Error('Invalid authenticated actor role');
      }

      const tenantID = Number(user?.tenantId);
      if (!Number.isInteger(tenantID) || tenantID <= 0) {
        throw new Error('Invalid authenticated tenant identity');
      }

      const response = await httpClient.get<{ tenants: Tenant[] }>('/api/v1/auth/tenants');
      const tenant = Array.isArray(response?.tenants)
        ? response.tenants.find(candidate => Number(candidate.id) === tenantID)
        : undefined;
      if (!tenant || !String(tenant.code || '').trim() || tenant.status !== 'active') {
        throw new Error('Authenticated tenant is not authorized');
      }

      const sessionUser: User = {
        ...user,
        id: actorID,
        username: String(user.username).trim(),
        role,
        tenantId: tenantID,
      };
      set({
        user: sessionUser,
        currentTenant: tenant,
        isAuthenticated: true,
        isLoading: false,
      });
      return sessionUser;
    } catch (error) {
      set({
        user: null,
        currentTenant: null,
        isAuthenticated: false,
        isLoading: false,
      });
      throw error;
    }
  },

  // 登出操作
  logout: () => {
    set({
      user: null,
      isAuthenticated: false,
      isLoading: false,
      currentTenant: null,
    });
  },

  // 更新用户信息
  updateUser: (userData: Partial<User>) => {
    const { user } = get();
    if (user) {
      set({
        user: { ...user, ...userData },
      });
    }
  },

  // 设置加载状态
  setLoading: (loading: boolean) => {
    set({ isLoading: loading });
  },

  // 检查用户权限
  hasPermission: (permission: string) => {
    const { user } = get();
    return user?.permissions?.includes(permission) || false;
  },

  // 检查用户角色
  hasRole: (role: string) => {
    const { user } = get();
    return user?.role === role;
  },

  // 检查是否为管理员
  isAdmin: () => {
    const { user } = get();
    return user?.role === 'admin' || user?.role === 'super_admin';
  },
}));

// ===================================
// 租户管理 Store
// ===================================

interface TenantState {
  tenants: Tenant[];
  loading: boolean;
  error: string | null;
  setTenants: (tenants: Tenant[]) => void;
  addTenant: (tenant: Tenant) => void;
  updateTenant: (id: number, tenant: Partial<Tenant>) => void;
  removeTenant: (id: number) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
}

export const useTenantStore = create<TenantState>(set => ({
  tenants: [],
  loading: false,
  error: null,
  setTenants: tenants => set({ tenants }),
  addTenant: tenant => set(state => ({ tenants: [...state.tenants, tenant] })),
  updateTenant: (id, updatedTenant) =>
    set(state => ({
      tenants: state.tenants.map(tenant =>
        tenant.id === id ? { ...tenant, ...updatedTenant } : tenant
      ),
    })),
  removeTenant: id =>
    set(state => ({
      tenants: state.tenants.filter(tenant => tenant.id !== id),
    })),
  setLoading: loading => set({ loading }),
  setError: error => set({ error }),
}));

// ===================================
// 权限常量
// ===================================

export const PERMISSIONS = {
  // 工单权限
  TICKET_VIEW: 'ticket:view',
  TICKET_CREATE: 'ticket:create',
  TICKET_UPDATE: 'ticket:update',
  TICKET_DELETE: 'ticket:delete',
  TICKET_ASSIGN: 'ticket:assign',
  TICKET_CLOSE: 'ticket:close',

  // 用户权限
  USER_VIEW: 'user:view',
  USER_CREATE: 'user:create',
  USER_UPDATE: 'user:update',
  USER_DELETE: 'user:delete',

  // 事件权限
  INCIDENT_VIEW: 'incident:view',
  INCIDENT_CREATE: 'incident:create',
  INCIDENT_UPDATE: 'incident:update',
  INCIDENT_DELETE: 'incident:delete',

  // 系统权限
  SYSTEM_CONFIG: 'system:config',
  SYSTEM_LOGS: 'system:logs',

  // 报告权限
  REPORT_VIEW: 'report:view',
  REPORT_EXPORT: 'report:export',

  // 审计权限
  AUDIT_VIEW: 'audit:view',
  AUDIT_EXPORT: 'audit:export',
} as const;

// 角色常量
export const ROLES = {
  SUPER_ADMIN: 'super_admin',
  ADMIN: 'admin',
  MANAGER: 'manager',
  AGENT: 'agent',
  TECHNICIAN: 'technician',
  END_USER: 'end_user',
  USER: 'user', // 兼容旧版本
} as const;

// ===================================
// 权限检查 Hook
// ===================================

export const usePermissions = () => {
  const hasPermission = useAuthStore(state => state.hasPermission);
  const hasRole = useAuthStore(state => state.hasRole);
  const isAdmin = useAuthStore(state => state.isAdmin);

  return {
    // 基础权限检查
    hasPermission,
    hasRole,
    isAdmin,

    // 工单权限
    canViewTickets: () => hasPermission(PERMISSIONS.TICKET_VIEW) || isAdmin(),
    canCreateTickets: () => hasPermission(PERMISSIONS.TICKET_CREATE) || isAdmin(),
    canUpdateTickets: () => hasPermission(PERMISSIONS.TICKET_UPDATE) || isAdmin(),
    canDeleteTickets: () => hasPermission(PERMISSIONS.TICKET_DELETE) || isAdmin(),
    canAssignTickets: () => hasPermission(PERMISSIONS.TICKET_ASSIGN) || isAdmin(),

    // 用户权限
    canViewUsers: () => hasPermission(PERMISSIONS.USER_VIEW) || isAdmin(),
    canManageUsers: () => hasPermission(PERMISSIONS.USER_CREATE) || isAdmin(),

    // 事件权限
    canViewIncidents: () => hasPermission(PERMISSIONS.INCIDENT_VIEW) || isAdmin(),
    canManageIncidents: () => hasPermission(PERMISSIONS.INCIDENT_CREATE) || isAdmin(),

    // 报告权限
    canViewReports: () => hasPermission(PERMISSIONS.REPORT_VIEW) || isAdmin(),
    canExportReports: () => hasPermission(PERMISSIONS.REPORT_EXPORT) || isAdmin(),

    // 角色检查
    isSuperAdmin: () => hasRole(ROLES.SUPER_ADMIN),
    isManager: () => hasRole(ROLES.MANAGER),
    isAgent: () => hasRole(ROLES.AGENT),
    isTechnician: () => hasRole(ROLES.TECHNICIAN),
    isEndUser: () => hasRole(ROLES.END_USER) || hasRole(ROLES.USER),
  };
};

export type { AuthState, TenantState };
