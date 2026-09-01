import { test, expect } from '@playwright/test';
import { DEFAULT_LOGIN, loginAndReturn } from './auth-utils';

function getNumberEnv(name: string, fallback: number) {
  const raw = process.env[name];
  if (!raw) return fallback;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : fallback;
}

async function getHeapUsedBytes(page: import('@playwright/test').Page) {
  const result = await page.evaluate(() => {
    const perf: any = performance as any;
    const mem = perf && perf.memory ? perf.memory : null;
    return mem
      ? { usedJSHeapSize: mem.usedJSHeapSize, totalJSHeapSize: mem.totalJSHeapSize }
      : null;
  });
  return result?.usedJSHeapSize ?? null;
}

test.describe('Stability - Soak', () => {
  test.skip(!process.env.SOAK_TESTS, 'SOAK_TESTS is not enabled');

  test('Core flows should be stable under repeated usage', async ({ page }) => {
    const minutes = getNumberEnv('SOAK_DURATION_MINUTES', 5);
    const maxGrowthMb = getNumberEnv('SOAK_MAX_HEAP_GROWTH_MB', 50);
    const deadline = Date.now() + minutes * 60_000;

    await loginAndReturn(page, DEFAULT_LOGIN);
    await page.waitForLoadState('networkidle');

    const startHeap = await getHeapUsedBytes(page);
    let loops = 0;

    while (Date.now() < deadline) {
      loops += 1;

      await page.goto('/tickets');
      await page.waitForLoadState('networkidle');
      await expect(page.getByRole('heading', { name: '工单管理' })).toBeVisible({
        timeout: 10000,
      });

      const firstRow = page.locator('table tbody tr').first();
      await expect(firstRow).toBeVisible();
      await firstRow.click();
      await page.waitForLoadState('networkidle');

      await page.goto('/incidents');
      await page.waitForLoadState('networkidle');
      await expect(page.getByRole('heading', { name: '事件管理' })).toBeVisible({
        timeout: 10000,
      });

      await page.goto('/dashboard');
      await page.waitForLoadState('networkidle');
      await expect(page.getByRole('heading', { name: 'AI-Native ITSM 运营仪表盘' })).toBeVisible({
        timeout: 10000,
      });
    }

    const endHeap = await getHeapUsedBytes(page);
    expect(loops).toBeGreaterThan(0);

    if (startHeap !== null && endHeap !== null) {
      const growthMb = (endHeap - startHeap) / (1024 * 1024);
      expect(growthMb).toBeLessThanOrEqual(maxGrowthMb);
    }
  });
});
