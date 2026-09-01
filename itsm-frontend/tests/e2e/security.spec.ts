import { test, expect } from '@playwright/test';
import { DEFAULT_LOGIN, loginAndReturn, loginThroughForm } from './auth-utils';

test.describe('Security - 浏览器侧安全验证', () => {
  test('RBAC should block non-admin from admin pages', async ({ page }) => {
    await loginAndReturn(page, { ...DEFAULT_LOGIN, username: 'employee', password: 'admin123' });
    const response = await page.request.get('/api/v1/users');
    expect(response.status()).toBe(403);
  });

  test('XSS payload should not execute when rendered in ticket detail', async ({ page }) => {
    await page.addInitScript(() => {
      (window as any).__xss_executed = 0;
      (window as any).__xss_mark = () => {
        (window as any).__xss_executed = 1;
      };
    });

    await loginAndReturn(page, { ...DEFAULT_LOGIN, username: 'end_user', password: 'admin123' });
    await page.goto('/tickets/create');
    await page.waitForSelector('form, [data-testid="ticket-form"]', { timeout: 15000 });

    const title = `XSS Test ${Date.now()}`;
    const payload = `<img src=x onerror="window.__xss_mark && window.__xss_mark()">`;

    await page
      .locator('input[id*="title"], input[name*="title"], input[placeholder*="标题"]')
      .first()
      .fill(title);
    await page
      .locator('textarea[id*="description"], textarea[name*="description"], textarea[placeholder*="描述"]')
      .first()
      .fill(payload);

    const createResp = page.waitForResponse(
      resp => resp.url().includes('/api/v1/tickets') && resp.request().method() === 'POST',
      { timeout: 15000 }
    );

    await page
      .locator(
        'button[type="submit"], button:has-text("提交"), button:has-text("创建"), button:has-text("创建工单")'
      )
      .first()
      .click();
    expect((await createResp).status()).toBe(200);

    await page.waitForLoadState('networkidle');
    const xss = await page.evaluate(() => (window as any).__xss_executed);
    expect(xss).toBe(0);
  });

  test('Sensitive data should not be sent via URL query on login', async ({ page }) => {
    await loginThroughForm(page, DEFAULT_LOGIN);

    expect(page.url()).not.toContain('admin123');
    expect(page.url()).not.toMatch(/password=/i);
  });
});
