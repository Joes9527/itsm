/**
 * Admin Role E2E Tests
 * Tests features accessible to admin role
 */

import { test, expect } from '@playwright/test';
import { TEST_USERS } from './utils/test-utils';
import { loginAndReturn } from './auth-utils';

// Increase timeout for admin tests
test.describe.configure({ timeout: 60000 });

test.describe('Admin Role - Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.admin);
  });

  test('should access dashboard', async ({ page }) => {
    await page.goto('/dashboard');
    await page.waitForLoadState('networkidle');

    // Dashboard should show content
    const dashboardContent = page.locator('[class*="dashboard"], .ant-layout-content');
    await expect(dashboardContent.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access all tickets', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Should display ticket list
    const content = page.locator('.ant-table, [class*="table"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access admin panel', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');

    await expect(page).toHaveURL(/\/admin\/?$/);
    await expect(page.locator('main, .ant-layout-content').first()).toBeVisible();
  });

  test('should access user management', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    // Should display user management
    const content = page.locator('.ant-table, [class*="user"], [class*="table"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access role management', async ({ page }) => {
    await page.goto('/admin/roles');
    await page.waitForLoadState('networkidle');

    // Should display role management
    const content = page.locator('.ant-table, [class*="role"], [class*="table"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access system config', async ({ page }) => {
    await page.goto('/admin/system-config');
    await page.waitForLoadState('networkidle');

    // Should display system config or admin content
    const content = page.locator('.ant-layout-content, [class*="config"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Admin Role - Knowledge Base Management', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.admin);
  });

  test('should access knowledge base', async ({ page }) => {
    await page.goto('/knowledge');
    await page.waitForLoadState('networkidle');

    // Should display knowledge base
    const content = page.locator('[class*="knowledge"], .ant-layout-content');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should create knowledge article', async ({ page }) => {
    await page.goto('/knowledge/articles/new');
    await page.waitForLoadState('networkidle');

    // Should display form
    const form = page.locator('form, [class*="editor"]');
    await expect(form.first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Admin Role - ITAM Features', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.admin);
  });

  test('should access CMDB', async ({ page }) => {
    await page.goto('/cmdb');
    await page.waitForLoadState('networkidle');

    // Should display CMDB content
    const content = page.locator('.ant-layout-content, [class*="cmdb"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access SLA dashboard', async ({ page }) => {
    await page.goto('/sla-dashboard');
    await page.waitForLoadState('networkidle');

    // Should display SLA dashboard
    const content = page.locator('.ant-layout-content, [class*="sla"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });
});
