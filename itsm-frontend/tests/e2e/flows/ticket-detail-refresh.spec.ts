import { test, expect, Page } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

export async function guardBusinessWrites(page: Page) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request();
    if (['GET', 'HEAD', 'OPTIONS'].includes(request.method()) || new URL(request.url()).pathname.startsWith('/api/v1/auth/')) {
      await route.continue();
    } else {
      await route.abort('blockedbyclient');
    }
  });
}

const id = process.env.PLAYWRIGHT_TICKET_DETAIL_ID;
test.beforeEach(async ({ page }) => {
  test.skip(!id, 'Requires an existing readable generic ticket');
  await guardBusinessWrites(page);
});

test('real detail preserves comment draft, refreshes mounted reads once, keyboard and themes fit', async ({ page }, info) => {
  await page.route('**/api/v1/ai/triage', route => route.fulfill({ json: { code: 0, data: { suggestions: { category: 'general', priority: 'medium', confidence: 0.9, reasoning: '隔离AI验收建议' } } } }));
  const counts = new Map<string, number>();
  let aiRequests = 0;
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (/\/ai\//.test(path)) aiRequests++;
    if (request.method() === 'GET') counts.set(path, (counts.get(path) || 0) + 1);
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  const refresh = page.getByRole('button', { name: '刷新工单详情', exact: true });
  const draft = page.getByPlaceholder('输入您的评论或内部评估记录...');
  await expect(draft).toBeVisible();
  await draft.fill('浏览器验收草稿：保留上下文，不提交');
  await expect(refresh).toBeEnabled();
  const before = new Map(counts);
  await expect(page.getByText('隔离AI验收建议', { exact: true })).toBeVisible();
  const aiBefore = aiRequests;
  await refresh.click();
  await expect(refresh).not.toHaveClass(/ant-btn-loading/);
  await expect(draft).toHaveValue('浏览器验收草稿：保留上下文，不提交');
  for (const path of [`/api/v1/tickets/${id}`, `/api/v1/tickets/${id}/comments`, '/api/v1/bpmn/tasks']) {
    expect((counts.get(path) || 0) - (before.get(path) || 0), path).toBe(1);
  }
  for (const [path, count] of counts) {
    if (path.startsWith('/api/v1/')) expect(count - (before.get(path) || 0), path).toBeLessThanOrEqual(1);
  }
  expect(aiRequests).toBe(aiBefore);
  const prior = counts.get(`/api/v1/tickets/${id}`)!;
  await draft.blur();
  await page.keyboard.press('Alt+r');
  await expect.poll(() => counts.get(`/api/v1/tickets/${id}`)).toBe(prior + 1);
  await expect(refresh).not.toHaveClass(/ant-btn-loading/);
  for (const theme of ['light', 'dark']) {
    await page.evaluate(value => { localStorage.setItem('itsm-theme', value); }, theme);
    await page.reload();
    await expect(draft).toBeVisible();
    await expect(page.locator('html')).toHaveClass(new RegExp(theme));
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await expect(refresh).toHaveCount(1);
      expect(await page.evaluate(() => document.body.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: info.outputPath(`detail-${theme}-${width}.png`), fullPage: true });
      if (width === 390) {
        await page.getByRole('button', { name: /侧边栏/ }).click();
        await expect(page.getByRole('menuitem', { name: '工单管理', exact: true })).toBeVisible();
        await page.screenshot({ path: info.outputPath(`detail-${theme}-390-menu.png`), fullPage: true });
        await page.keyboard.press('Escape');
      }
    }
  }
});

test('failed comments retry locally and denied detail clears protected editors', async ({ page }) => {
  let fail = false;
  let deny = false;
  let detailReads = 0;
  await page.route(`**/api/v1/tickets/${id}`, async route => {
    detailReads++;
    if (deny) await route.fulfill({ status: 403, json: { code: 403, message: '验收权限已撤销' } });
    else await route.continue();
  });
  await page.route(`**/api/v1/tickets/${id}/comments`, async route => {
    if (fail) await route.fulfill({ status: 500, json: { code: 500, message: '验收评论读取失败' } });
    else await route.continue();
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  const draft = page.getByPlaceholder('输入您的评论或内部评估记录...');
  await expect(draft).toBeVisible();
  await draft.fill('撤权前草稿');
  fail = true;
  await page.getByRole('button', { name: '刷新工单详情' }).click();
  await expect(page.getByRole('button', { name: '重试', exact: true })).toBeVisible();
  const before = detailReads;
  fail = false;
  await page.getByRole('button', { name: '重试', exact: true }).click();
  await expect(page.getByRole('button', { name: '重试', exact: true })).toHaveCount(0);
  expect(detailReads).toBe(before);
  await expect(draft).toHaveValue('撤权前草稿');
  deny = true;
  await page.getByRole('button', { name: '刷新工单详情' }).click();
  await expect(draft).toHaveCount(0);
});

test('open edit keeps its command version across refresh and handles isolated conflict', async ({ page }) => {
  let version = 7;
  let writes = 0;
  let reads = 0;
  await page.route(`**/api/v1/tickets/${id}`, async route => {
    if (route.request().method() !== 'GET') {
      expect(route.request().postDataJSON().version).toBe(7);
      writes++;
      await route.fulfill({ status: 409, json: { code: 4090, message: '工单版本冲突，请重新打开编辑' } });
      return;
    }
    const response = await route.fetch();
    const body = await response.json();
    reads++;
    body.data.version = version;
    body.data.actions = { ...body.data.actions, edit: { allowed: true } };
    await route.fulfill({ response, json: body });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const title = page.getByPlaceholder('请输入工单标题');
  await title.fill('隔离验收编辑草稿');
  version = 8;
  const beforeRefresh = reads;
  await title.blur();
  await page.keyboard.press('Alt+r');
  await expect.poll(() => reads).toBe(beforeRefresh + 1);
  await expect(page.getByRole('button', { name: '刷新工单详情' })).not.toHaveClass(/ant-btn-loading/);
  await expect(title).toHaveValue('隔离验收编辑草稿');
  await page.getByRole('button', { name: '保存修改' }).click();
  await expect(title).not.toBeVisible();
  expect(writes).toBe(1);
});



test('server read-only actions remain disabled', async ({ page }) => {
  await page.route(`**/api/v1/tickets/${id}`, async route => {
    if (route.request().method() !== 'GET') { await route.abort(); return; }
    const response = await route.fetch();
    const body = await response.json();
    body.data.actions = Object.fromEntries(['edit', 'assign', 'delete', 'cc'].map(action => [action, { allowed: false, reason: '只读验收' }]));
    await route.fulfill({ response, json: body });
  });
  await loginAndReturn(page, DEFAULT_LOGIN, `/tickets/${id}`);
  await expect(page.getByRole('button', { name: '编辑', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: '转派分配', exact: true })).toBeDisabled();
  await page.keyboard.press('Alt+e');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '刷新工单详情' })).toBeEnabled();
});
