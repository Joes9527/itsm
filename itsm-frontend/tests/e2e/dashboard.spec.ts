import { test, expect } from '@playwright/test';

import { loginAndReturn } from './auth-utils';

test.describe('Dashboard canonical analytics', () => {
  test('loads the dashboard projection and renders all six operator charts', async ({ page }) => {
    await loginAndReturn(page);

    const overviewResponsePromise = page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'GET' && url.pathname === '/api/v1/dashboard/overview';
    });
    const ticketStatsResponsePromise = page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'GET' && url.pathname === '/api/v1/tickets/stats';
    });

    await page.goto('/dashboard');
    const [overviewResponse, ticketStatsResponse] = await Promise.all([
      overviewResponsePromise,
      ticketStatsResponsePromise,
    ]);
    expect(overviewResponse.status()).toBe(200);
    expect(ticketStatsResponse.status()).toBe(200);

    const overviewEnvelope = await overviewResponse.json();
    expect(overviewEnvelope).toHaveProperty('code', 0);
    expect(overviewEnvelope).toHaveProperty('data');
    const ticketStatsEnvelope = await ticketStatsResponse.json();
    expect(ticketStatsEnvelope).toHaveProperty('code', 0);
    expect(ticketStatsEnvelope).toHaveProperty('data');

    await expect(
      page.getByRole('heading', { name: 'AI-Native ITSM 运营仪表盘' })
    ).toBeVisible();
    await page.getByRole('tab', { name: '全部图表' }).click();

    for (const chartTitle of [
      '工单趋势分析',
      'SLA 达成率监控',
      '事件分类分布',
      '响应时间分布',
      '团队工作负载',
      '高峰时段分析',
    ]) {
      await expect(page.getByText(chartTitle, { exact: true })).toBeVisible();
    }
  });
});
