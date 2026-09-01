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
      await page.goto('/licenses');
      await page.waitForURL(/\/licenses/);
      await expect(page.locator('h1, h2').first()).toBeVisible();
    });

    test('should display license list page', async ({ page }) => {
      await page.goto('/licenses');
      await page.waitForLoadState('networkidle');
      await expect(page.locator('body')).toBeVisible();
    });
  });

  test.describe('License Create - 创建许可证', () => {
    test('should display create license form', async ({ page }) => {
      await page.goto('/licenses/new');
      await page.waitForLoadState('networkidle');
      await expect(page.locator('form, .ant-form').first()).toBeVisible({ timeout: 10000 });
    });
  });
});
