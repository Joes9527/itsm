/**
 * Agent/Security Role E2E Tests
 * Tests features accessible to agent/security role
 * Note: Uses security1 user from seed data (role: security)
 */

import { test, expect } from '@playwright/test';
import { TEST_USERS } from './utils/test-utils';
import { loginAndReturn } from './auth-utils';

// Increase timeout for agent tests
test.describe.configure({ timeout: 60000 });

test.describe('Agent/Security Role - Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.security);
  });

  test('should access dashboard', async ({ page }) => {
    await page.goto('/dashboard');
    await page.waitForLoadState('networkidle');

    // Dashboard should show content
    const dashboardContent = page.locator('[class*="dashboard"], .ant-layout-content');
    await expect(dashboardContent.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access tickets', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Should display ticket list
    const content = page.locator('.ant-table, [class*="table"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should view ticket details', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Try to click on a Ticket if any exists
    const firstTicket = page.locator('.ant-table-row, [class*="row"], tr').first();
    if (await firstTicket.isVisible()) {
      await firstTicket.click();
      await page.waitForLoadState('networkidle');

      // Should show ticket details
      const details = page.locator('[class*="detail"], [class*="info"]');
      await expect(details.first()).toBeVisible({ timeout: 5000 });
    }
  });
});

test.describe('Agent/Security Role - Ticket Management', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.security);
  });

  test('should access incident management', async ({ page }) => {
    await page.goto('/incidents');
    await page.waitForLoadState('networkidle');

    // Should display incident management
    const content = page.locator('.ant-layout-content, [class*="incident"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access problem management', async ({ page }) => {
    await page.goto('/problems');
    await page.waitForLoadState('networkidle');

    // Should display problem management
    const content = page.locator('.ant-layout-content, [class*="problem"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access change management', async ({ page }) => {
    await page.goto('/changes');
    await page.waitForLoadState('networkidle');

    // Should display change management
    const content = page.locator('.ant-layout-content, [class*="change"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Agent/Security Role - Service Features', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.security);
  });

  test('should access knowledge base', async ({ page }) => {
    await page.goto('/knowledge');
    await page.waitForLoadState('networkidle');

    // Should display knowledge base
    const content = page.locator('[class*="knowledge"], .ant-layout-content');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access CMDB', async ({ page }) => {
    await page.goto('/cmdb');
    await page.waitForLoadState('networkidle');

    // Should display CMDB content
    const content = page.locator('.ant-layout-content, [class*="cmdb"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access service catalog', async ({ page }) => {
    await page.goto('/service-catalog');
    await page.waitForLoadState('networkidle');

    // Should display service catalog
    const content = page.locator('[class*="catalog"], .ant-card');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });
});
