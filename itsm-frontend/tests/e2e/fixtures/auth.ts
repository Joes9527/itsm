import { test as base } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';

/**
 * 角色测试账号映射
 * 与 seeder.go 中 seedRoleTestAccounts 保持一致
 */
export const TEST_ACCOUNTS = {
  admin: { username: 'admin', password: 'admin123', role: 'admin' },
  user1: { username: 'user1', password: 'user123', role: 'end_user' },
  security1: { username: 'security1', password: 'security123', role: 'security' },
  engineer1: { username: 'engineer1', password: 'eng123', role: 'technician' },
  manager1: { username: 'manager1', password: 'mgr123', role: 'manager' },
  tenant1admin: { username: 'tenant1admin', password: 'ta123', role: 'admin' },
} as const;

export type TestRole = keyof typeof TEST_ACCOUNTS;

// 扩展 Playwright test 类型
interface TestFixtures {
  loginAs: (role: TestRole) => Promise<string>;
  apiGet: (role: string, path: string) => Promise<any>;
  apiPost: (role: string, path: string, body?: any) => Promise<any>;
}

async function establishSession(request: APIRequestContext, role: TestRole) {
  const account = TEST_ACCOUNTS[role];
  const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
  const response = await request.post(`${apiURL}/api/v1/auth/login`, {
    data: { username: account.username, password: account.password },
  });
  if (!response.ok()) throw new Error(`Login failed for ${role}: ${response.status()}`);

  const json = (await response.json()) as { data?: Record<string, unknown> };
  const data = json.data ?? {};
  if ('access_token' in data || 'accessToken' in data || 'refresh_token' in data || 'refreshToken' in data) {
    throw new Error(`Login response for ${role} exposed a JWT`);
  }
}

export const test = base.extend<TestFixtures>({
  loginAs: async ({ request }, use) => {
    await use(async (role: TestRole) => {
      await establishSession(request, role);
      return role;
    });
  },

  apiGet: async ({ request }, use) => {
    await use(async (role: string, path: string) => {
      await establishSession(request, role as TestRole);
      const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
      const response = await request.get(`${apiURL}${path}`);
      return {
        status: response.status(),
        data: response.ok() ? await response.json() : await response.text(),
      };
    });
  },

  apiPost: async ({ request }, use) => {
    await use(async (role: string, path: string, body?: any) => {
      await establishSession(request, role as TestRole);
      const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
      const response = await request.post(`${apiURL}${path}`, {
        headers: {
          'Content-Type': 'application/json',
        },
        data: body,
      });
      return {
        status: response.status(),
        data: response.ok() ? await response.json() : await response.text(),
      };
    });
  },
});

export { expect } from '@playwright/test';
