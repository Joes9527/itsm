import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

test('approval center reads current backend without executing decisions', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/approvals');
  await expect(page.getByRole('heading', { name: '审批中心', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '刷新' })).toBeEnabled();
  await expect(page.locator('#main-content').getByRole('alert')).toHaveCount(0);
  await expect(page.getByText(/当前授权范围内的审批待办：/)).toBeVisible();
});

test('approval UI handles later pages, failure/retry, claim, rejection and preview refill with isolated responses', async ({ page }, testInfo) => {
  let failRead = true;
  let failDecision = true;
  let claimed = false;
  const decisions: unknown[] = [];
  const pending = Array.from({ length: 6 }, (_, index) => ({
    id: 900101 + index, taskName: `UI审批-${index + 1}`, taskPurpose: 'approval',
    status: 'created', assignee: '', createdTime: `2026-09-01T00:00:0${6-index}Z`,
    taskDefinitionKey: 'approval', processInstanceId: 1,
  }));
  const fulfillment = Array.from({ length: 100 }, (_, index) => ({
    id: 800000 + index, taskName: '普通履约任务', taskPurpose: 'fulfillment', status: 'created',
  }));
  // All task mutations are fulfilled here, never forwarded to shared workflow records.
  await page.route('**/api/v1/bpmn/tasks**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith('/claim')) {
      claimed = true;
      await route.fulfill({ json: { code: 0, data: {} } }); return;
    }
    if (url.pathname.endsWith('/decisions')) {
      decisions.push(request.postDataJSON());
      if (failDecision) { await route.fulfill({ status: 409, json: { code: 409, message: '任务已变化，请刷新' } }); return; }
      const id = Number(url.pathname.split('/').at(-2));
      const index = pending.findIndex(task => task.id === id);
      if (index >= 0) pending.splice(index, 1);
      await route.fulfill({ json: { code: 0, data: {} } }); return;
    }
    if (request.method() !== 'GET' || !url.pathname.endsWith('/tasks')) {
      await route.abort(); return;
    }
    if (failRead) { await route.fulfill({ status: 500, json: { code: 500, message: '读取暂时失败' } }); return; }
    const items = url.searchParams.get('status') === 'created' ? [...fulfillment, ...pending.map(task => ({ ...task, assignee: claimed ? 'reviewer' : '' }))] : [];
    const pageNumber = Number(url.searchParams.get('page'));
    const pageSize = Number(url.searchParams.get('pageSize'));
    await route.fulfill({ json: { code: 0, data: { data: items.slice((pageNumber-1)*pageSize,pageNumber*pageSize), pagination: { page: pageNumber, pageSize, total: items.length } } } });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, '/approvals');
  await expect(page.locator('#main-content').getByRole('alert')).toBeVisible();
  await expect(page.getByText('暂无审批待办', { exact: true })).toHaveCount(0);
  failRead = false;
  await page.getByRole('button', { name: '刷新' }).click();
  await expect(page.getByText('UI审批-1', { exact: true })).toBeVisible();
  await expect(page.getByText('普通履约任务', { exact: true })).toHaveCount(0);
  const row = page.getByRole('row').filter({ hasText: 'UI审批-1' });
  await row.getByRole('button', { name: '领取', exact: true }).click();
  await expect(row.getByRole('button', { name: '领取', exact: true })).toHaveCount(0);
  await row.getByRole('button', { name: '拒绝', exact: true }).click();
  await page.getByRole('button', { name: '确认拒绝' }).click();
  expect(decisions).toEqual([]);
  await page.getByRole('textbox', { name: '审批意见' }).fill('请补充说明');
  await page.getByRole('button', { name: '确认拒绝' }).click();
  await expect(page.getByText('任务已变化，请刷新', { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog')).toBeVisible();
  failDecision = false;
  await page.getByRole('button', { name: '确认拒绝' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByText('UI审批-1', { exact: true })).toHaveCount(0);
  expect(decisions).toEqual([{ action: 'reject', comment: '请补充说明' }, { action: 'reject', comment: '请补充说明' }]);
  await expect(page.getByText('已拒绝', { exact: true })).toHaveCount(0);
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`approvals-${width}.png`), fullPage: true });
  }
  await page.goto('/portal');
  await expect(page.getByRole('link', { name: '查看全部审批' })).toBeVisible();
  await expect(page.getByText('UI审批-6', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: '同意批准' }).first().click();
  await expect(page.getByText('UI审批-6', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: '查看全部审批' }).click();
  await expect(page).toHaveURL(/\/approvals$/);
});
