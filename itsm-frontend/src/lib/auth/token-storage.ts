/**
 * 租户上下文存储。
 *
 * 安全说明：
 * - Access token 和 Refresh token 存储在 httpOnly cookies 中（由后端设置）
 * - 前端无法直接读取 token 值，认证真值由后端 `/auth/me` 验证
 * - Tenant 信息存储在 localStorage 中（非敏感数据）
 *
 * 重要：Token 永远不存储在 localStorage 中！所有认证检查依赖 httpOnly cookie。
 *
 * 存储键名：
 * - tenant code:   current_tenant_code
 * - tenant id:     current_tenant_id
 */
export const STORAGE_KEYS = {
  TENANT_CODE: 'current_tenant_code',
  TENANT_ID: 'current_tenant_id',
} as const;

function safeGet(key: string): string | null {
  if (typeof window === 'undefined') return null;
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeRemove(key: string): void {
  if (typeof window === 'undefined') return;
  try {
    localStorage.removeItem(key);
  } catch {
    // ignore
  }
}

export function getTenantCode(): string | null {
  return safeGet(STORAGE_KEYS.TENANT_CODE);
}

export function getTenantId(): string | null {
  return safeGet(STORAGE_KEYS.TENANT_ID);
}

export function clearAuthStorage(): void {
  safeRemove(STORAGE_KEYS.TENANT_ID);
  safeRemove(STORAGE_KEYS.TENANT_CODE);
  // 清理 Zustand persist 的 auth-storage key（避免跨用户/跨租户残留 user 信息）
  safeRemove('auth-storage');
}
