/**
 * E2E Test Utilities
 * Provides helper functions for authentication, API calls, and test data management
 */

import type { Page} from '@playwright/test';
import { expect } from '@playwright/test';
import { loginAndReturn, logoutSession, mutateWithCSRF } from '../auth-utils';

// Test user credentials from seed data (see itsm-backend/pkg/seeder/seeder.go)
export const TEST_USERS = {
  admin: {
    tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default',
    username: 'admin',
    password: 'admin123',
    role: 'admin',
  },
  end_user: {
    tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default',
    username: 'user1',
    password: 'user123',
    role: 'end_user',
  },
  security: {
    tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default',
    username: 'security1',
    password: 'security123',
    role: 'security',
  },
  // Agent uses security1 user (has agent-like permissions)
  agent: {
    tenantCode: process.env.PLAYWRIGHT_TENANT_CODE || 'default',
    username: 'security1',
    password: 'security123',
    role: 'agent',
  },
} as const;

export type TestUserRole = keyof typeof TEST_USERS;

/**
 * Login as a specific role
 */
export async function loginAs(page: Page, role: TestUserRole): Promise<void> {
  const user = TEST_USERS[role];
  await loginAndReturn(page, user);
}

/**
 * Logout current user
 */
export async function logout(page: Page): Promise<void> {
  await logoutSession(page);
}

/**
 * Navigate to a page and wait for it to load
 */
export async function navigateTo(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await page.waitForLoadState('networkidle');
}

/**
 * Wait for a table to load with data
 */
export async function waitForTable(page: Page, timeout = 10000): Promise<void> {
  await page.waitForSelector('table, [class*="table"], [class*="list"]', { timeout });
}

/**
 * Create a ticket via API
 */
export async function createTicketViaApi(
  page: Page,
  ticketData: {
    title: string;
    description?: string;
    priority?: string;
    category?: string;
  }
): Promise<{ id: number }> {
  const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

  const response = await mutateWithCSRF(page.request, 'POST', `${apiUrl}/api/v1/tickets`, {
    data: ticketData,
  });

  if (!response.ok()) {
    const error = await response.json();
    throw new Error(`Failed to create ticket: ${error.message}`);
  }

  const data = await response.json();
  return data.data;
}

/**
 * Get current user info
 */
/**
 * Take a screenshot with name
 */
export async function takeScreenshot(page: Page, name: string): Promise<void> {
  await page.screenshot({ path: `test-results/screenshots/${name}-${Date.now()}.png`, fullPage: true });
}

/**
 * Fill and submit a form
 */
export async function fillForm(
  page: Page,
  fields: Record<string, string>
): Promise<void> {
  for (const [selector, value] of Object.entries(fields)) {
    const input = page.locator(selector);
    await input.fill(value);
  }
}

/**
 * Assert user is logged in
 */
export async function assertLoggedIn(page: Page): Promise<void> {
  // Check for user avatar/menu which indicates logged in state
  const userMenu = page.locator('[class*="user"], [class*="avatar"], button:has-text("admin")');
  await expect(userMenu.first()).toBeVisible({ timeout: 5000 });
}

/**
 * Assert user is on login page
 */
export async function assertOnLoginPage(page: Page): Promise<void> {
  await expect(page).toHaveURL(/login/);
  const loginButton = page.locator('button[type="submit"], button:has-text("登录")');
  await expect(loginButton).toBeVisible();
}

/**
 * Wait for notification/toast message
 */
export async function waitForNotification(page: Page, text?: string): Promise<void> {
  const notification = text
    ? page.locator(`.ant-message-success, .ant-message-error, [class*="notification"]:has-text("${text}")`)
    : page.locator('.ant-message-success, .ant-message-error, [class*="notification"]');
  await notification.first().waitFor({ timeout: 5000 });
}
