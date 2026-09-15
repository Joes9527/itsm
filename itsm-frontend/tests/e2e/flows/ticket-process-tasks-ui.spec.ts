import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/**', async route => {
    const request = route.request();
    if (['GET', 'HEAD', 'OPTIONS'].includes(request.method()) || new URL(request.url()).pathname.startsWith('/api/v1/auth/')) await route.continue();
    else await route.abort('blockedbyclient');
  });
});

test('ticket shows scoped process tasks with isolated read responses', async ({ page }, testInfo) => {
  const id = process.env.PLAYWRIGHT_PROCESS_TASK_TICKET_ID;
  test.skip(!id, 'Requires an existing readable generic ticket');
  let fail = true;
  await page.route('**/api/v1/bpmn/tasks?**', async route => {
    const url = new URL(route.request().url());
    if (url.searchParams.get('businessId') !== id) { await route.continue(); return; }
    expect(route.request().method()).toBe('GET');
    expect(url.searchParams.get('businessType')).toBe('generic');
    await route.fulfill(fail ? { status: 500, json: { code: 500, message: '任务读取暂时失败' } } : {
      json: { code: 0, data: { data: [{ id: 999001, businessType: 'generic', businessId: Number(id), taskName: '请求受理', status: 'created', assignee: 'Helpdesk A', taskPurpose: '' }], pagination: { page: 1, pageSize: 100, total: 1 } } },
    });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  const panel = page.getByRole('region', { name: '当前流程任务' });
  await expect(panel.getByRole('alert')).toBeVisible();
  fail = false;
  await panel.getByRole('button', { name: '重试' }).click();
  await panel.getByRole('button', { name: /当前任务/ }).click();
  await expect(panel.getByRole('button', { name: '领取任务' })).toHaveCount(0);
  await expect(panel.getByText('请求受理', { exact: true })).toBeVisible();
  await expect(panel.getByText('Helpdesk A', { exact: true })).toBeVisible();
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`process-tasks-${width}.png`), fullPage: true });
  }
});


test('claims and completes a simple task with isolated mutations', async ({ page }, testInfo) => {
  const id = process.env.PLAYWRIGHT_PROCESS_TASK_TICKET_ID;
  test.skip(!id, 'Requires an existing readable generic ticket');
  let claimed = false;
  let completed = false;
  let writes = 0;
  await page.route('**/api/v1/bpmn/tasks**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith('/999001/claim')) {
      expect(request.method()).toBe('PUT'); claimed = true; writes++;
      await route.fulfill({ json: { code: 0, data: {} } }); return;
    }
    if (url.pathname.endsWith('/999001/complete')) {
      expect(request.method()).toBe('PUT'); expect(request.postDataJSON()).toEqual({}); completed = true; writes++;
      await route.fulfill({ json: { code: 0, data: {} } }); return;
    }
    if (request.method() !== 'GET') { await route.abort(); return; }
    if (url.searchParams.get('businessId') !== id) { await route.continue(); return; }
    const tasks = [{ id: 999001, businessType: 'generic', businessId: Number(id), taskName: '请求受理', taskType: 'user_task', taskPurpose: '', status: completed ? 'completed' : claimed ? 'assigned' : 'created', assignee: claimed ? 'Helpdesk A' : '', uiActions: { claim: !claimed && !completed, complete: claimed && !completed } }];
    await route.fulfill({ json: { code: 0, data: { data: tasks, pagination: { page: 1, pageSize: 100, total: tasks.length } } } });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  const panel = page.getByRole('region', { name: '当前流程任务' });
  await panel.getByRole('button', { name: '领取任务' }).click();
  await expect(panel.getByText('状态：已分配', { exact: true })).toBeVisible();
  await panel.getByRole('button', { name: '完成任务' }).click();
  expect(writes).toBe(1);
  await page.getByRole('button', { name: '确认完成' }).click();
  await expect(panel.getByText('当前账号暂无可见的活动任务')).toBeVisible();
  expect(writes).toBe(2);
  const history = panel.getByRole('button', { name: /历史任务/ });
  await expect(history).toHaveAttribute('aria-expanded', 'false');
  await expect(panel.getByText('请求受理', { exact: true })).toHaveCount(0);
  await history.click();
  await expect(panel.getByText('请求受理', { exact: true })).toBeVisible();
  await expect(panel.getByRole('button', { name: '完成任务' })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('task-history-expanded.png'), fullPage: true });
});
