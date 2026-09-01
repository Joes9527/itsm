/**
 * Playwright 认证工具。
 * 会话只通过后端 Set-Cookie 建立，测试代码不读取或写入 JWT。
 */

import type { APIRequestContext, Page } from '@playwright/test';
import { test as base, expect } from '@playwright/test';

export interface LoginCredentials {
  tenantCode: string;
  username: string;
  password: string;
}

export const DEFAULT_LOGIN: LoginCredentials = {
  tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default',
  username: 'admin',
  password: 'admin123',
};

export async function establishSession(
  request: APIRequestContext,
  credentials: LoginCredentials = DEFAULT_LOGIN,
  appURL: string = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000'
): Promise<void> {
  const loginResponse = await request.post(`${appURL}/api/v1/auth/login`, {
    data: credentials,
    timeout: 30_000,
  });
  if (!loginResponse.ok()) {
    throw new Error(`登录失败: HTTP ${loginResponse.status()}`);
  }
  const loginJson = (await loginResponse.json()) as { code?: number; data?: Record<string, unknown> };
  if (loginJson.code !== 0) {
    throw new Error(`登录失败: code ${String(loginJson.code)}`);
  }
  const responseData = loginJson.data ?? {};
  if (
    'accessToken' in responseData ||
    'access_token' in responseData ||
    'refreshToken' in responseData ||
    'refresh_token' in responseData
  ) {
    throw new Error('登录失败: JWT 不得出现在 JSON 响应中');
  }
}

export async function loginThroughForm(
  page: Page,
  credentials: LoginCredentials = DEFAULT_LOGIN
): Promise<void> {
  await page.goto('/login');
  const inputs = page.locator('form input');
  await expect(inputs).toHaveCount(3);
  await inputs.nth(0).fill(credentials.tenantCode);
  await inputs.nth(1).fill(credentials.username);
  await inputs.nth(2).fill(credentials.password);
  const responsePromise = page.waitForResponse(
    response => response.url().includes('/api/v1/auth/login') && response.request().method() === 'POST'
  );
  await page.getByRole('button', { name: /^登录$/ }).click();
  const response = await responsePromise;
  if (response.status() !== 200) {
    throw new Error(`登录失败: HTTP ${response.status()}`);
  }
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/, { timeout: 20_000 });
}

export async function loginAndReturn(
  page: Page,
  credentials: LoginCredentials = DEFAULT_LOGIN,
  landingPath: string = '/dashboard'
) {
  const appURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
  await page.context().clearCookies();

  // BrowserContext.request 与 page 共享 cookie jar。
  await establishPageSession(page, credentials, appURL);

  await page.goto(landingPath, { waitUntil: 'domcontentloaded' });
  return page;
}

export async function establishPageSession(
  page: Page,
  credentials: LoginCredentials = DEFAULT_LOGIN,
  appURL: string = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000'
): Promise<void> {
  await establishSession(page.request, credentials, appURL);
  const cookies = await page.context().cookies(appURL);
  if (!cookies.some(cookie => cookie.name === 'access_token' && cookie.httpOnly)) {
    throw new Error('登录失败: 未签发 HttpOnly access_token cookie');
  }
}

export async function logoutSession(page: Page): Promise<void> {
  const appURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
  const csrfResponse = await page.request.get(`${appURL}/api/v1/csrf-token`);
  if (!csrfResponse.ok()) throw new Error(`CSRF bootstrap failed: HTTP ${csrfResponse.status()}`);
  const csrfJSON = (await csrfResponse.json()) as { code?: number; data?: { csrf_token?: string } };
  const csrfToken = csrfJSON.data?.csrf_token;
  if (csrfJSON.code !== 0 || !csrfToken) throw new Error('CSRF bootstrap returned no token');
  const response = await page.request.post(`${appURL}/api/v1/auth/logout`, {
    headers: { 'X-CSRF-Token': csrfToken },
    data: {},
  });
  if (!response.ok()) throw new Error(`登出失败: HTTP ${response.status()}`);
  const cookies = await page.context().cookies(appURL);
  if (cookies.some(cookie => cookie.name === 'access_token' || cookie.name === 'refresh_token')) {
    throw new Error('登出失败: session cookies remain');
  }
}

export const test = base.extend<{
  authenticatedPage: Page;
}>({
  authenticatedPage: async ({ page }, use) => {
    await loginAndReturn(page);
    await use(page);
  },
});

export { expect };
