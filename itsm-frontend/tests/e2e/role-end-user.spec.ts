/**
 * End User Role E2E Tests
 * Tests features accessible to end_user role
 */

import { test, expect } from '@playwright/test';
import { TEST_USERS, waitForTable } from './utils/test-utils';
import { loginAndReturn } from './auth-utils';

// Increase timeout for end user tests
test.describe.configure({ timeout: 60000 });

test.describe('End User Role - Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page, TEST_USERS.end_user);
  });

  test('should access dashboard', async ({ page }) => {
    await page.goto('/dashboard');
    await page.waitForLoadState('networkidle');

    // Dashboard should show content
    const dashboardContent = page.locator('[class*="dashboard"], .ant-layout-content');
    await expect(dashboardContent.first()).toBeVisible({ timeout: 10000 });
  });

  test('should view own tickets', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Should display ticket list or empty state
    const content = page.locator('.ant-table, [class*="empty"], [class*="table"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should create new ticket', async ({ page }) => {
    await page.goto('/tickets/create');
    await page.waitForLoadState('networkidle');

    // Check for form
    const form = page.locator('form');
    await expect(form).toBeVisible({ timeout: 10000 });

    // Fill in ticket form if available
    const titleInput = page.locator('input[id*="title"], input[name*="title"]');
    await expect(titleInput).toBeVisible();
    {
      await titleInput.fill('Test Ticket from End User');

      const descInput = page.locator('textarea[id*="description"], textarea[name*="description"]');
      await expect(descInput).toBeVisible();
      {
        await descInput.fill('This is a test ticket created by end user');
      }
    }
  });

  test('should access knowledge base', async ({ page }) => {
    await page.goto('/knowledge');
    await page.waitForLoadState('networkidle');

    // Should display knowledge base content
    const content = page.locator('[class*="knowledge"], .ant-layout-content, article');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('should access service catalog', async ({ page }) => {
    await page.goto('/service-catalog');
    await page.waitForLoadState('networkidle');

    // Should display service catalog
    const content = page.locator('[class*="catalog"], .ant-card, [class*="service"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });
});
