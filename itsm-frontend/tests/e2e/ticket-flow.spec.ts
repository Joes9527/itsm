/**
 * Ticket Flow E2E Tests
 * Tests complete ticket lifecycle from creation to closure
 */

import { test, expect } from '@playwright/test';
import { TEST_USERS } from './utils/test-utils';
import { loginAndReturn } from './auth-utils';

// Increase timeout for ticket flow tests
test.describe.configure({ timeout: 90000 });

test.describe('Ticket Lifecycle - End User Creates Ticket', () => {
  test('end user should be able to create a ticket', async ({ page }) => {
    // Step 1: Login as end user
    await test.step('Login as end_user', async () => {
      await loginAndReturn(page, TEST_USERS.end_user);
    });

    // Step 2: Navigate to create ticket page
    await test.step('Navigate to create ticket page', async () => {
      await page.goto('/tickets/create');
      await page.waitForLoadState('networkidle');

      // Check for form
      const form = page.locator('form');
      await expect(form).toBeVisible({ timeout: 10000 });
    });

    // Step 3: Fill in ticket details
    await test.step('Fill in ticket details', async () => {
      // Fill title
      const titleInput = page.locator(
        'input[id*="title"], input[name*="title"], input[placeholder*="标题"]'
      );
      await expect(titleInput).toBeVisible();
      {
        await titleInput.fill('E2E Test Ticket - ' + Date.now());
      }

      // Fill description
      const descInput = page.locator(
        'textarea[id*="description"], textarea[name*="description"], textarea[placeholder*="描述"]'
      );
      await expect(descInput).toBeVisible();
      {
        await descInput.fill('This is a test ticket created by E2E test');
      }

      // Select priority if available
      const prioritySelect = page.locator('[class*="priority"], select[name*="priority"]');
      await expect(prioritySelect).toBeVisible();
      {
        await prioritySelect.click();
        await page.waitForTimeout(300);
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Enter');
      }

      // Select category if available
      const categorySelect = page.locator('[class*="category"], select[name*="category"]');
      await expect(categorySelect).toBeVisible();
      {
        await categorySelect.click();
        await page.waitForTimeout(300);
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Enter');
      }
    });

    // Step 4: Submit ticket
    await test.step('Submit ticket', async () => {
      const submitButton = page.locator(
        'button[type="submit"], button:has-text("提交"), button:has-text("创建")'
      );
      await expect(submitButton).toBeVisible();
      const createResponse = page.waitForResponse(
        response =>
          response.request().method() === 'POST' &&
          /\/api\/v1\/tickets$/.test(new URL(response.url()).pathname)
      );
      await submitButton.click();
      expect((await createResponse).status()).toBe(200);
    });

    // Step 5: Verify ticket created (check URL or success message)
    await test.step('Verify ticket created', async () => {
      await expect(page).toHaveURL(/\/tickets\/\d+$/);
    });
  });
});

test.describe('Ticket Lifecycle - Agent Processes Ticket', () => {
  test('agent should be able to view and update ticket status', async ({ page }) => {
    // Step 1: Login as agent
    await test.step('Login as agent', async () => {
      await loginAndReturn(page, TEST_USERS.agent);
    });

    // Step 2: Navigate to tickets list
    await test.step('Navigate to tickets list', async () => {
      await page.goto('/tickets');
      await page.waitForLoadState('networkidle');
    });

    // Step 3: View ticket details
    await test.step('View ticket details', async () => {
      // Look for a ticket to click on
      const ticketRow = page.locator('.ant-table-row, [class*="row"]').first();
      await expect(ticketRow).toBeVisible({ timeout: 5000 });
      {
        await ticketRow.click();
        await page.waitForLoadState('networkidle');

        // Should show ticket details
        const details = page.locator('[class*="detail"], [class*="info"], .ant-card');
        await expect(details.first()).toBeVisible({ timeout: 5000 });
      }
    });

    // Step 4: Update ticket status through the canonical edit command.
    await test.step('Update ticket status', async () => {
      await page.getByRole('button', { name: '编辑', exact: true }).click();
      const modal = page.getByRole('dialog', { name: /编辑工单/ });
      await expect(modal).toBeVisible();
      await modal.getByLabel('状态').click();
      await page.getByRole('option', { name: '处理中' }).click();

      const updateResponse = page.waitForResponse(
        response =>
          response.request().method() === 'PUT' &&
          /\/api\/v1\/tickets\/\d+$/.test(new URL(response.url()).pathname)
      );
      await modal.getByRole('button', { name: '保存修改' }).click();
      expect((await updateResponse).status()).toBe(200);
      await expect(modal).not.toBeVisible();
      await expect(page.getByText('处理中', { exact: true }).first()).toBeVisible();
    });
  });
});

test.describe('Ticket Lifecycle - Admin Manages Tickets', () => {
  test('admin should be able to view and manage all tickets', async ({ page }) => {
    // Step 1: Login as admin
    await test.step('Login as admin', async () => {
      await loginAndReturn(page, TEST_USERS.admin);
    });

    // Step 2: Navigate to tickets
    await test.step('Navigate to tickets', async () => {
      await page.goto('/tickets');
      await page.waitForLoadState('networkidle');

      // Should display ticket list
      const content = page.locator('.ant-table, [class*="table"]');
      await expect(content.first()).toBeVisible({ timeout: 10000 });
    });

    // Step 3: Filter tickets by status
    await test.step('Filter tickets by status', async () => {
      const filterButton = page.locator(
        '[class*="filter"], button:has-text("筛选"), button:has-text("过滤")'
      );
      await expect(filterButton).toBeVisible();
      await filterButton.click();

      const statusOption = page
        .locator('.ant-select-dropdown:has-text("Open"), .ant-select-item:has-text("Open")')
        .first();
      await expect(statusOption).toBeVisible();
      const filterResponse = page.waitForResponse(
        response =>
          response.request().method() === 'GET' &&
          new URL(response.url()).pathname === '/api/v1/tickets'
      );
      await statusOption.click();
      expect((await filterResponse).status()).toBe(200);
      await expect(statusOption).not.toBeVisible();
    });

    // Step 4: Assign ticket (if controls available)
    await test.step('Assign ticket', async () => {
      // Click on a ticket row
      const ticketRow = page.locator('.ant-table-row, [class*="row"]').first();
      await expect(ticketRow).toBeVisible({ timeout: 5000 });
      {
        await ticketRow.click();
        await page.waitForLoadState('networkidle');

        const assignButton = page.getByRole('button', { name: /转派分配/ });
        await expect(assignButton).toBeVisible();
        await assignButton.click();
        const modal = page.getByRole('dialog', { name: /分配工单/ });
        await expect(modal).toBeVisible();
        await modal.getByLabel('分配给').click();
        const assigneeOption = page.getByRole('option').first();
        await expect(assigneeOption).toBeVisible();
        const assigneeLabel = (await assigneeOption.textContent())?.trim();
        expect(assigneeLabel?.length).toBeGreaterThan(0);
        await assigneeOption.click();

        const assignResponse = page.waitForResponse(
          response =>
            response.request().method() === 'POST' &&
            /\/api\/v1\/tickets\/\d+\/assign$/.test(new URL(response.url()).pathname)
        );
        await modal.getByRole('button', { name: '确认分配' }).click();
        expect((await assignResponse).status()).toBe(200);
        await expect(modal).not.toBeVisible();
        await expect(page.getByText('工单分配成功')).toBeVisible();
      }
    });
  });
});

test.describe('Ticket Search and Filter', () => {
  test.beforeEach(async ({ page }) => {
    // Login as admin to have access to all tickets
    await loginAndReturn(page, TEST_USERS.admin);
  });

  test('should search tickets by keyword', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Find search input - use .first() to avoid matching multiple elements
    const searchInput = page
      .locator('input[placeholder*="搜索"], input[placeholder*="Search"], input[type="search"]')
      .first();
    await expect(searchInput).toBeVisible();
    const searchResponse = page.waitForResponse(response => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/v1/tickets' &&
        url.searchParams.get('keyword') === 'test'
      );
    });
    await searchInput.fill('test');
    expect((await searchResponse).status()).toBe(200);

    const rows = page.locator('table tbody tr');
    if ((await rows.count()) === 0) {
      await expect(page.locator('.ant-empty')).toBeVisible();
    } else {
      for (const row of await rows.all()) {
        await expect(row).toContainText(/test/i);
      }
    }
  });

  test('should filter tickets by priority', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    await page.getByRole('button', { name: '过滤器' }).click();
    const priorityFilter = page.locator('.ant-select').filter({ hasText: '优先级' }).first();
    await expect(priorityFilter).toBeVisible();
    await priorityFilter.click();
    const highOption = page.getByRole('option', { name: /高优先级/ });
    await expect(highOption).toBeVisible();
    const priorityResponse = page.waitForResponse(response => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/v1/tickets' &&
        url.searchParams.get('priority') === 'high'
      );
    });
    await highOption.click();
    expect((await priorityResponse).status()).toBe(200);

    const rows = page.locator('table tbody tr');
    if ((await rows.count()) === 0) {
      await expect(page.locator('.ant-empty')).toBeVisible();
    } else {
      for (const row of await rows.all()) {
        await expect(row).toContainText(/高/);
      }
    }
  });
});
