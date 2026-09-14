import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

test('ticket shows scoped process tasks with isolated read responses', async ({ page }, testInfo) => {
  const id = process.env.PLAYWRIGHT_PROCESS_TASK_TICKET_ID;
  test.skip(!id, 'Requires an existing readable generic ticket');
  let fail = true;
  await page.route('**/api/v1/bpmn/tasks?**', async route => {
    const url = new URL(route.request().url());
    if (url.searchParams.get('businessId') !== id) { await route.continue(); return; }
    expect(route.request().method()).toBe('GET');
    expect(url.searchParams.get('businessType')).toBe('ticket');
    await route.fulfill(fail ? { status: 500, json: { code: 500, message: '任务读取暂时失败' } } : {
      json: { code: 0, data: { data: [{ id: 999001, businessType: 'ticket', businessId: Number(id), taskName: '请求受理', status: 'created', assignee: 'Helpdesk A', taskPurpose: '' }], pagination: { page: 1, pageSize: 100, total: 1 } } },
    });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  const panel = page.getByRole('region', { name: '当前流程任务' });
  await expect(panel.getByRole('alert')).toBeVisible();
  fail = false;
  await panel.getByRole('button', { name: '重试' }).click();
  await expect(panel.getByText('请求受理', { exact: true })).toBeVisible();
  await expect(panel.getByText('Helpdesk A', { exact: true })).toBeVisible();
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`process-tasks-${width}.png`), fullPage: true });
  }
});
