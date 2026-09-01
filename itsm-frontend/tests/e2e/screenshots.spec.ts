/**
 * Screenshots E2E Tests
 * 使用 Playwright 自动截图 - 复用现有登录逻辑
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import { loginAndReturn } from './auth-utils';

const SCREENSHOT_DIR = path.join(__dirname, '..', '..', '..', 'docs', 'images');

// 辅助函数：登录并截图
async function loginAndScreenshot(page: any, pagePath: string, name: string) {
  await loginAndReturn(page);
  await page.waitForTimeout(2000);

  // 访问目标页面
  await page.goto(pagePath, { waitUntil: 'networkidle' });
  await page.waitForTimeout(3000);

  // 截图
  await page.screenshot({
    fullPage: true,
    path: path.join(SCREENSHOT_DIR, `${name}.png`)
  });
}

test.describe('Screenshots - 页面截图', () => {
  test('01 - login page', async ({ page }) => {
    await page.goto('/login');
    await page.waitForSelector('.ant-input', { timeout: 15000 });
    await page.waitForTimeout(2000);
    await page.screenshot({
      fullPage: true,
      path: path.join(SCREENSHOT_DIR, 'login.png')
    });
  });

  test('02 - dashboard', async ({ page }) => {
    await loginAndScreenshot(page, '/dashboard', 'dashboard');
  });

  test('03 - tickets', async ({ page }) => {
    await loginAndScreenshot(page, '/tickets', 'tickets');
  });

  test('04 - incidents', async ({ page }) => {
    await loginAndScreenshot(page, '/incidents', 'incidents');
  });

  test('05 - problems', async ({ page }) => {
    await loginAndScreenshot(page, '/problems', 'problems');
  });

  test('06 - changes', async ({ page }) => {
    await loginAndScreenshot(page, '/changes', 'changes');
  });

  test('07 - knowledge', async ({ page }) => {
    await loginAndScreenshot(page, '/knowledge', 'knowledge');
  });

  test('08 - workflow designer', async ({ page }) => {
    await loginAndScreenshot(page, '/workflow/designer', 'workflow-designer');
  });

  test('09 - workflow dashboard', async ({ page }) => {
    await loginAndScreenshot(page, '/workflow/dashboard', 'workflow-dashboard');
  });

  test('10 - sla monitor', async ({ page }) => {
    await loginAndScreenshot(page, '/sla-monitor', 'sla-monitor');
  });

  test('11 - cmdb', async ({ page }) => {
    await loginAndScreenshot(page, '/cmdb', 'cmdb');
  });

  test('12 - assets', async ({ page }) => {
    await loginAndScreenshot(page, '/assets', 'assets');
  });

  test('13 - msp management', async ({ page }) => {
    await loginAndScreenshot(page, '/msp/management', 'msp-management');
  });
});
