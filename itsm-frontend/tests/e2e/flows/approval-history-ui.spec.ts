import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

test('decision history distinguishes error and empty history with isolated read responses', async ({ page }, testInfo) => {
  const ticketId = process.env.PLAYWRIGHT_APPROVAL_HISTORY_TICKET_ID;
  test.skip(!ticketId, 'Requires an existing readable ticket; does not create shared data');
  let failRead = true;
  await page.route(`**/api/v1/tickets/${ticketId}/approval-decisions`, async route => {
    expect(route.request().method()).toBe('GET');
    await route.fulfill(failRead
      ? { status: 500, json: { code: 500, message: '审批历史暂时不可用' } }
      : { json: { code: 0, data: [] } });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${ticketId}`);
  const history = page.locator('div').filter({ has: page.getByText('审批决策历史', { exact: true }) }).filter({ has: page.getByRole('button', { name: '重试', exact: true }) }).last();
  await expect(history.getByRole('alert')).toBeVisible();
  await expect(page.getByText('该工单未走审批流程', { exact: true })).toHaveCount(0);
  failRead = false;
  await history.getByRole('button', { name: '重试', exact: true }).click();
  await expect(page.getByText('暂无审批决策记录', { exact: true }).first()).toBeVisible();
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`approval-history-${width}.png`), fullPage: true });
  }
});
