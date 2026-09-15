/**
 * License/Certificate Management E2E Tests
 * 许可证/证书管理模块测试
 */

import { test, expect } from '@playwright/test';
import { loginAndReturn } from './auth-utils';

test.describe('License/Certificate Management - 许可证管理', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page);
  });

  test.describe('License List - 许可证列表', () => {
    test('should navigate to license management page', async ({ page }) => {
      const listResponsePromise = page.waitForResponse(response => {
        const url = new URL(response.url());
        return response.request().method() === 'GET' && url.pathname === '/api/v1/licenses';
      });
      await page.goto('/licenses');
      await page.waitForURL(/\/licenses/);
      const listResponse = await listResponsePromise;
      expect(listResponse.status()).toBe(200);
      const envelope = await listResponse.json();
      expect(envelope).toHaveProperty('code', 0);
      expect(envelope).toHaveProperty('data');
      await expect(page.getByText('总许可证', { exact: true })).toBeVisible();
    });
  });

  test.describe('License Create - 创建许可证', () => {
    test('should display create license form', async ({ page }) => {
      await page.goto('/licenses/new');
      await page.waitForLoadState('networkidle');
      await expect(page.getByLabel('许可证名称')).toBeVisible({ timeout: 10000 });
    });
  });
});
