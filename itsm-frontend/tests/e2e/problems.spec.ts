/**
 * Problem Management E2E Tests
 * 问题管理端到端测试
 */

import { test, expect } from '@playwright/test';
import { loginAndReturn } from './auth-utils';

test.describe('Problem List - 问题列表', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page);
    await page.goto('/problems', { waitUntil: 'domcontentloaded' });
  });

  test('should display problem list page', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '问题管理' })).toBeVisible();
  });

  test('should display problem table', async ({ page }) => {
    await expect(page.locator('.ant-table')).toBeVisible();
  });

  test('should filter by status', async ({ page }) => {
    const statusFilter = page.locator('select[name="status"]').first();
    await expect(statusFilter).toBeVisible();
    {
      await statusFilter.selectOption('investigation');
    }
  });

  test('should navigate to problem detail', async ({ page }) => {
    const viewBtn = page
      .locator('button.ant-btn')
      .filter({ has: page.locator('.lucide-eye') })
      .first();
    await expect(viewBtn).toBeVisible();
    await viewBtn.click();
    await expect(page).toHaveURL(/\/problems\/\d+/);
  });
});

test.describe('Problem Create - 创建问题', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page);
    await page.goto('/problems/new');
  });

  test('should display create problem form', async ({ page }) => {
    await expect(page.getByLabel('问题标题')).toBeVisible();
    await expect(page.getByLabel('详细描述')).toBeVisible();
  });

  test('should create problem successfully', async ({ page }) => {
    const titleInput = page.getByLabel('问题标题');
    const descInput = page.getByLabel('详细描述');
    const submitBtn = page.getByRole('button', { name: '创建问题' });

    await expect(titleInput).toBeVisible();
    await expect(descInput).toBeVisible();
    await expect(submitBtn).toBeVisible();

    const createResponsePromise = page.waitForResponse(resp => {
      return resp.url().includes('/api/v1/problems') && resp.request().method() === 'POST';
    });

    await titleInput.fill(`E2E 问题 - ${Date.now()}`);
    await descInput.fill('这是一个用于 E2E 的创建问题测试描述。');
    await submitBtn.click();

    const createResp = await createResponsePromise;
    expect(createResp.status()).toBe(200);
  });
});

test.describe('Problem Detail - 问题详情', () => {
  test.beforeEach(async ({ page }) => {
    await loginAndReturn(page);
    await page.goto('/problems', { waitUntil: 'domcontentloaded' });

    const viewBtn = page
      .locator('button.ant-btn')
      .filter({ has: page.locator('.lucide-eye') })
      .first();
    await expect(viewBtn).toBeVisible();
    await viewBtn.click();
  });

  test('should display problem detail', async ({ page }) => {
    await expect(page.getByRole('button', { name: '返回列表' })).toBeVisible();
  });

});
