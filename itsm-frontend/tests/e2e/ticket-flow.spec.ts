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
      {
        await submitButton.click();
        await page.waitForTimeout(2000);
      }
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

    // Step 4: Update ticket status if controls available
    await test.step('Update ticket status', async () => {
      const statusSelect = page.locator(
        '[class*="status"], select[name*="status"], [class*="select"]'
      );
      await expect(statusSelect).toBeVisible({ timeout: 3000 });
      {
        await statusSelect.click();
        await page.waitForTimeout(500);

        // Select "In Progress" option
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Enter');

        // Save button
        const saveButton = page.locator('button:has-text("保存"), button:has-text("更新")');
        await expect(saveButton).toBeVisible();
        {
          await saveButton.click();
          await page.waitForTimeout(1000);
        }
      }
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
      {
        await filterButton.click();
        await page.waitForTimeout(500);

        // Select a status filter
        const statusOption = page
          .locator('.ant-select-dropdown:has-text("Open"), .ant-select-item:has-text("Open")')
          .first();
        await expect(statusOption).toBeVisible();
        {
          await statusOption.click();
          await page.waitForTimeout(1000);
        }
      }
    });

    // Step 4: Assign ticket (if controls available)
    await test.step('Assign ticket', async () => {
      // Click on a ticket row
      const ticketRow = page.locator('.ant-table-row, [class*="row"]').first();
      await expect(ticketRow).toBeVisible({ timeout: 5000 });
      {
        await ticketRow.click();
        await page.waitForLoadState('networkidle');

        // Look for assign button
        const assignButton = page.locator('button:has-text("分配"), button:has-text("Assign")');
        await expect(assignButton).toBeVisible();
        {
          await assignButton.click();
          await page.waitForTimeout(500);

          // Select an assignee
          const assigneeOption = page.locator('.ant-select-item, [class*="option"]').first();
          await expect(assigneeOption).toBeVisible();
          {
            await assigneeOption.click();
            await page.waitForTimeout(500);
          }
        }
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
    {
      await searchInput.fill('test');
      await page.waitForTimeout(500);

      // Press search
      await page.keyboard.press('Enter');
      await page.waitForTimeout(1000);
    }
  });

  test('should filter tickets by priority', async ({ page }) => {
    await page.goto('/tickets');
    await page.waitForLoadState('networkidle');

    // Find priority filter
    const priorityFilter = page.locator('[class*="priority"], select[name*="priority"]');
    await expect(priorityFilter).toBeVisible();
    {
      await priorityFilter.click();
      await page.waitForTimeout(300);

      // Select a priority
      await page.keyboard.press('ArrowDown');
      await page.keyboard.press('Enter');
      await page.waitForTimeout(1000);
    }
  });
});
