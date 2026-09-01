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
    await expect(page.locator('h1').first()).toContainText(/问题|problem/i);
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
    await expect(page.locator('input[name="title"], input#title')).toBeVisible();
    await expect(page.locator('textarea[name="description"]')).toBeVisible();
  });

  test('should create problem successfully', async ({ page }) => {
    const titleInput = page.locator('input[name="title"], input#title').first();
    const descInput = page.locator('textarea[name="description"], textarea').first();
    const submitBtn = page.locator('button[type="submit"]').first();

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
    await expect(page.locator('h1, .ant-card-head-title').first()).toBeVisible();
  });

  test('should display RCA section', async ({ page }) => {
    const rcaTab = page.locator('button:has-text("根因分析"), button:has-text("RCA")');
    await expect(rcaTab).toBeVisible();
    {
      await rcaTab.click();
    }
  });

  test('should link to related incidents', async ({ page }) => {
    const relatedTab = page.locator('button:has-text("关联事件")');
    await expect(relatedTab).toBeVisible();
    {
      await relatedTab.click();
      await expect(page.locator('[class*="incident"]')).toBeVisible();
    }
  });
});
