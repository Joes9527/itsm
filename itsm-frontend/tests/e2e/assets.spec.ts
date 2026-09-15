/**
 * Asset Management E2E Tests
 * 资产管理模块测试
 */

import { test, expect } from '@playwright/test';
import { loginAndReturn } from './auth-utils';

test.describe('Asset Management - 资产管理', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page);
  });

  test.describe('Asset List - 资产列表', () => {
    test('should navigate to asset management page', async ({ page }) => {
      const listResponsePromise = page.waitForResponse(response => {
        const url = new URL(response.url());
        return response.request().method() === 'GET' && url.pathname === '/api/v1/assets';
      });
      await page.goto('/assets');
      await page.waitForURL(/\/assets/);
      const listResponse = await listResponsePromise;
      expect(listResponse.status()).toBe(200);
      const envelope = await listResponse.json();
      expect(envelope).toHaveProperty('code', 0);
      expect(envelope).toHaveProperty('data');
      await expect(page.getByRole('heading', { name: '资产管理' })).toBeVisible();
    });
  });

  test.describe('Asset Create - 创建资产', () => {
    test('should display create asset form', async ({ page }) => {
      await page.goto('/assets/new');
      await page.waitForLoadState('networkidle');
      await expect(page.getByLabel('资产编号')).toBeVisible({ timeout: 10000 });
    });
  });
});
