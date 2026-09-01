import { test as base } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import { establishSession, loginAndReturn, mutateWithCSRF } from '../auth-utils';

/**
 * 角色测试账号映射
 * 与 seeder.go 中 seedRoleTestAccounts 保持一致
 */
export const TEST_ACCOUNTS = {
  admin: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'admin', password: 'admin123', role: 'admin' },
  user1: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'user1', password: 'user123', role: 'end_user' },
  security1: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'security1', password: 'security123', role: 'security' },
  engineer1: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'engineer1', password: 'eng123', role: 'technician' },
  manager1: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'manager1', password: 'mgr123', role: 'manager' },
  tenant1admin: { tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default', username: 'tenant1admin', password: 'ta123', role: 'admin' },
} as const;

export type TestRole = keyof typeof TEST_ACCOUNTS;

// 扩展 Playwright test 类型
interface TestFixtures {
  loginAs: (role: TestRole) => Promise<string>;
  apiGet: (role: string, path: string) => Promise<any>;
  apiPost: (role: string, path: string, body?: any) => Promise<any>;
  apiPostExpectStatus: (role: string, path: string, expectedStatus: number, body?: any) => Promise<any>;
}

async function establishRoleSession(request: APIRequestContext, role: TestRole) {
  const account = TEST_ACCOUNTS[role];
  const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
  await establishSession(request, account, apiURL);
}

export const test = base.extend<TestFixtures>({
  // Page-oriented login uses the canonical same-origin browser flow. API-only
  // fixtures below deliberately keep their independent backend request jar.
  loginAs: async ({ page }, use) => {
    await use(async (role: TestRole) => {
      await loginAndReturn(page, TEST_ACCOUNTS[role], '/');
      return role;
    });
  },

  apiGet: async ({ request }, use) => {
    await use(async (role: string, path: string) => {
      await establishRoleSession(request, role as TestRole);
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
      await establishRoleSession(request, role as TestRole);
      const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
      const response = await mutateWithCSRF(request, 'POST', `${apiURL}${path}`, { data: body });
      if (!response.ok()) {
        throw new Error(`POST ${path} failed: ${response.status()} ${await response.text()}`);
      }
      return {
        status: response.status(),
        data: await response.json(),
      };
    });
  },

  apiPostExpectStatus: async ({ request }, use) => {
    await use(async (role: string, path: string, expectedStatus: number, body?: any) => {
      await establishRoleSession(request, role as TestRole);
      const apiURL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
      const response = await mutateWithCSRF(request, 'POST', `${apiURL}${path}`, { data: body });
      if (response.status() !== expectedStatus) {
        throw new Error(`POST ${path}: expected ${expectedStatus}, got ${response.status()} ${await response.text()}`);
      }
      return { status: response.status(), data: await response.text() };
    });
  },
});

export { expect } from '@playwright/test';
