/**
 * Playwright 认证工具。
 * 会话只通过后端 Set-Cookie 建立，测试代码不读取或写入 JWT。
 */

import type { Page } from '@playwright/test';
import { test as base, expect } from '@playwright/test';

export async function loginAndReturn(
  page: Page,
  username: string = 'admin',
  password: string = 'admin123',
  landingPath: string = '/dashboard'
) {
  const appURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
  await page.context().clearCookies();

  // BrowserContext.request 与 page 共享 cookie jar；使用同源代理保留
  // 真实的 HttpOnly/SameSite/Set-Cookie 契约。
  const loginResponse = await page.request.post(`${appURL}/api/v1/auth/login`, {
    data: { username, password },
    timeout: 30_000,
  });
  if (!loginResponse.ok()) {
    throw new Error(`登录失败: HTTP ${loginResponse.status()}`);
  }

  const loginJson = (await loginResponse.json()) as { data?: Record<string, unknown> };
  const responseData = loginJson.data ?? {};
  if (
    'accessToken' in responseData ||
    'access_token' in responseData ||
    'refreshToken' in responseData ||
    'refresh_token' in responseData
  ) {
    throw new Error('登录失败: JWT 不得出现在 JSON 响应中');
  }

  const cookies = await page.context().cookies(appURL);
  if (!cookies.some(cookie => cookie.name === 'access_token' && cookie.httpOnly)) {
    throw new Error('登录失败: 未签发 HttpOnly access_token cookie');
  }

  await page.goto(landingPath, { waitUntil: 'domcontentloaded' });
  return page;
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
