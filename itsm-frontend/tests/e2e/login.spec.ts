/**
 * Login Page E2E Tests
 * 测试登录页面的功能和交互
 */

import { test, expect } from '@playwright/test';
import { DEFAULT_LOGIN, loginThroughForm } from './auth-utils';

test.describe('Login Page - 无需认证', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login');
    // 等待页面完全加载
    await page.waitForLoadState('domcontentloaded');
  });

  test('should display login form', async ({ page }) => {
    await expect(page.getByRole('textbox', { name: '租户代码' })).toBeVisible({ timeout: 15000 });
    await expect(page.getByRole('textbox', { name: '用户名' })).toBeVisible();
    await expect(page.getByRole('button', { name: '登录' })).toBeVisible();
  });

  test('should display username field', async ({ page }) => {
    // 等待 Ant Design 表单渲染
    await page.waitForSelector('.ant-input, input.ant-input', { timeout: 15000 });
    const inputs = page.locator('input.ant-input');
    await expect(inputs.first()).toBeVisible();
  });

  test('should display password field', async ({ page }) => {
    await page.waitForSelector('input[type="password"]', { timeout: 15000 });
    await expect(page.locator('input[type="password"]')).toBeVisible();
  });

  test('should display login button', async ({ page }) => {
    await page.waitForSelector('button[type="submit"]', { timeout: 15000 });
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test('should show validation error when submitting empty form', async ({ page }) => {
    await page.waitForSelector('button[type="submit"]', { timeout: 15000 });
    await page.click('button[type="submit"]');
    await expect(page.getByText('请输入租户代码', { exact: true })).toBeVisible();
    // 页面仍然在登录页
    await expect(page).toHaveURL(/\/login/);
  });

  test('should redirect to dashboard on successful login', async ({ page }) => {
    await loginThroughForm(page, DEFAULT_LOGIN);

    // 验证 dashboard 页面正常加载（不在登录页）
    await expect(page).not.toHaveURL(/\/login/, { timeout: 20000 });
    await page.waitForLoadState('networkidle');
  });
});

test.describe('Dashboard Page - 需要认证', () => {
  test.use({ storageState: undefined as any });

  test('should redirect to login when not authenticated', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/\/login/);
  });

  test('should display dashboard after login', async ({ page }) => {
    await loginThroughForm(page, DEFAULT_LOGIN);
    await page.goto('/dashboard');
    await page.waitForLoadState('networkidle');

    // 等待跳转
    await page.waitForURL(/\/(dashboard|tickets)/, { timeout: 20000 });

    // 验证不在登录页
    await expect(page).not.toHaveURL(/\/login/);
  });
});
