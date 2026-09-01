import { test, expect } from '@playwright/test';
import { DEFAULT_LOGIN, loginAndReturn, loginThroughForm, mutateWithCSRF } from './auth-utils';

function getNumberEnv(name: string, fallback: number) {
  const raw = process.env[name];
  if (!raw) return fallback;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : fallback;
}

test.describe('Performance - 浏览器端关键指标', () => {
  test.skip(!process.env.PERF_TESTS, 'PERF_TESTS is not enabled');

  test('First screen navigation should meet budget', async ({ page }) => {
    const budgetMs = getNumberEnv('PERF_BUDGET_FIRST_SCREEN_MS', 2000);

    const start = Date.now();
    await loginThroughForm(page, DEFAULT_LOGIN);
    await page.waitForLoadState('networkidle');
    const elapsed = Date.now() - start;

    expect(elapsed).toBeLessThanOrEqual(budgetMs);
  });

  test('Ticket submit response time should meet budget', async ({ page }) => {
    const budgetMs = getNumberEnv('PERF_BUDGET_TICKET_SUBMIT_MS', 1000);

    await loginAndReturn(page, { ...DEFAULT_LOGIN, username: 'end_user', password: 'admin123' });
    await page.goto('/tickets/create');
    await expect(page.getByRole('main', { name: '创建工单页面' })).toBeVisible({ timeout: 15000 });

    await page.getByLabel('标题').fill(`Perf Ticket ${Date.now()}`);
    await page.getByLabel('详细描述').fill('performance test');

    const start = Date.now();
    const respPromise = page.waitForResponse(
      r => r.url().includes('/api/v1/tickets') && r.request().method() === 'POST',
      { timeout: 15000 }
    );

    await page.getByRole('button', { name: '创建工单', exact: true }).click();

    const response = await respPromise;
    const elapsed = Date.now() - start;

    expect(response.status()).toBe(200);

    expect(elapsed).toBeLessThanOrEqual(budgetMs);
  });

  test('Batch export 1000 tickets should meet budget (requires PERF_EXPORT_PAYLOAD_JSON)', async ({ page }) => {
    const raw = process.env.PERF_EXPORT_PAYLOAD_JSON;
    test.skip(!raw, 'PERF_EXPORT_PAYLOAD_JSON is not set');

    const budgetMs = getNumberEnv('PERF_BUDGET_EXPORT_1000_MS', 10000);
    const payload = JSON.parse(raw as string) as Record<string, unknown>;

    await loginAndReturn(page, DEFAULT_LOGIN);

    const start = Date.now();
    const response = await mutateWithCSRF(page.request, 'POST', '/api/v1/tickets/batch/export', {
      data: payload,
    });
    const elapsed = Date.now() - start;

    expect(response.status()).toBe(200);
    expect(elapsed).toBeLessThanOrEqual(budgetMs);
  });
});
