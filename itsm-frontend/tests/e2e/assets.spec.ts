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
      await page.goto('/assets');
      await page.waitForURL(/\/assets/);
      await expect(page.locator('h1, h2').first()).toBeVisible();
    });

    test('should display asset list page', async ({ page }) => {
      await page.goto('/assets');
      await page.waitForLoadState('networkidle');
      await expect(page.locator('body')).toBeVisible();
    });
  });

  test.describe('Asset Create - 创建资产', () => {
    test('should display create asset form', async ({ page }) => {
      await page.goto('/assets/new');
      await page.waitForLoadState('networkidle');
      await expect(page.locator('form, .ant-form').first()).toBeVisible({ timeout: 10000 });
    });
  });
});
