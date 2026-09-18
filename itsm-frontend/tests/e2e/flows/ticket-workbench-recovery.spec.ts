import { test, expect } from '@playwright/test';
import { loginAndReturn, mutateWithCSRF } from '../auth-utils';
import { TEST_ACCOUNTS } from '../fixtures/auth';

test('workbench attachment persistence, comments, assignment and read-only relations', async ({
  page,
}, testInfo) => {
  test.setTimeout(120_000);
  await loginAndReturn(page, TEST_ACCOUNTS.admin, '/tickets');
  let marker = `UI-WORKBENCH-${Date.now()}`;
  const created: number[] = [];
  const create = async (title: string, parentTicketId?: number) => {
    const response = await mutateWithCSRF(page.request, 'POST', '/api/v1/tickets', {
      headers: { 'Idempotency-Key': title },
      data: {
        title,
        description: 'UI workbench recovery acceptance fixture',
        type: 'ticket',
        source: 'manual',
        priority: 'low',
        parentTicketId,
      },
    });
    const body = await response.json();
    expect(response.ok(), `${response.status()}: ${body.message}`).toBeTruthy();
    expect(body.code).toBe(0);
    expect(body.data.workItemId).toBeGreaterThan(0);
    created.push(body.data.workItemId);
    return body.data.workItemId as number;
  };
  try {
    let id = Number(process.env.PLAYWRIGHT_WORKBENCH_TICKET_ID);
    if (id) {
      const fixture = await (await page.request.get(`/api/v1/tickets/${id}`)).json();
      expect(fixture.data.title).toMatch(/^UI-WORKBENCH-\d+-child$/);
      marker = fixture.data.title.replace(/-child$/, '');
    } else {
      const parent = await create(`${marker}-parent`);
      id = await create(`${marker}-child`, parent);
    }
    await page.goto(`/tickets/${id}`);
    await expect(page.getByRole('heading', { name: `${marker}-child` })).toBeVisible();
    await page.getByRole('tab', { name: /附件/ }).click();
    const filename = `${marker}-${Date.now()}-long-diagnostic-file-for-responsive-layout.txt`;
    const beforeAttachments = await (
      await page.request.get(`/api/v1/tickets/${id}/attachments`)
    ).json();
    const attachmentCount = beforeAttachments.data.attachments.length;
    const fileCard = page
      .getByRole('tabpanel', { name: /附件/ })
      .getByText(filename, { exact: true })
      .locator('..')
      .locator('..');
    const payload = 'workbench persistent attachment proof';
    await expect(page.getByRole('button', { name: '上传附件' })).toBeEnabled();
    const uploaded = page.waitForResponse(
      response =>
        response.url().endsWith(`/tickets/${id}/attachments`) &&
        response.request().method() === 'POST'
    );
    await page
      .locator('input[type=file]')
      .setInputFiles({ name: filename, mimeType: 'text/plain', buffer: Buffer.from(payload) });
    expect((await uploaded).ok()).toBeTruthy();
    await expect(
      page.getByRole('tabpanel', { name: /附件/ }).getByText(filename, { exact: true })
    ).toBeVisible();
    await page.reload();
    await page.getByRole('tab', { name: /附件/ }).click();
    await expect(
      page.getByRole('tabpanel', { name: /附件/ }).getByText(filename, { exact: true })
    ).toBeVisible();
    await fileCard.getByRole('button', { name: '预览', exact: true }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByRole('dialog').getByText(payload, { exact: true })).toBeVisible();
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const colorScheme of ['light', 'dark'] as const) {
        await page.emulateMedia({ colorScheme });
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
        ).toBe(true);
        await page.screenshot({
          path: testInfo.outputPath(`attachments-${width}-${colorScheme}.png`),
          fullPage: true,
        });
      }
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.emulateMedia({ colorScheme: 'light' });
    await page.getByRole('dialog').getByRole('button', { name: /close/i }).click();
    const downloadEvent = page.waitForEvent('download');
    await fileCard.getByRole('button', { name: '下载', exact: true }).click();
    expect((await downloadEvent).suggestedFilename()).toBe(filename);
    await fileCard.getByRole('button', { name: '删除', exact: true }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: /取\s*消/ })
      .click();
    await expect(
      page.getByRole('tabpanel', { name: /附件/ }).getByText(filename, { exact: true })
    ).toBeVisible();
    await fileCard.getByRole('button', { name: '删除', exact: true }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: /删\s*除/ })
      .click();
    await expect(
      page.getByRole('tabpanel', { name: /附件/ }).getByText(filename, { exact: true })
    ).toHaveCount(0);
    await expect(page.getByRole('tab', { name: `附件 (${attachmentCount})` })).toBeVisible();
    await page.getByRole('tab', { name: /协作沟通与评论/ }).click();
    const beforeComments = await (await page.request.get(`/api/v1/tickets/${id}/comments`)).json();
    const comment = `${marker}-${Date.now()}-comment`;
    await page.getByPlaceholder('输入您的评论或内部评估记录...').fill(comment);
    await page.getByRole('button', { name: '发送评论' }).click();
    await expect(page.getByText(comment, { exact: true })).toBeVisible();
    await expect(
      page.getByRole('tab', { name: `协作沟通与评论 (${beforeComments.data.total + 1})` })
    ).toBeVisible();
    await page.getByRole('button', { name: '转派分配' }).click();
    const visibleUsers = await (await page.request.get('/api/v1/users?pageSize=100')).json();
    expect(visibleUsers.data.users.length).toBeGreaterThan(0);
    await page
      .getByRole('combobox', { name: '分配给' })
      .fill(visibleUsers.data.users[0].username.toUpperCase());
    await expect(page.locator('.ant-select-item-option-content').first()).toBeVisible();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: /取\s*消/ })
      .click();
    await page.getByRole('tab', { name: /关联工单与资产/ }).click();
    await expect(page.getByText(new RegExp(`${marker}-parent`))).toBeVisible();
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const colorScheme of ['light', 'dark'] as const) {
        await page.emulateMedia({ colorScheme });
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
        ).toBe(true);
        await page.screenshot({
          path: testInfo.outputPath(`${width}-${colorScheme}.png`),
          fullPage: true,
        });
      }
    }
  } finally {
    for (const id of created.reverse()) {
      const response = await mutateWithCSRF(page.request, 'DELETE', `/api/v1/tickets/${id}`);
      const body = await response.json();
      if (response.status() === 403 && body.message === '工单流程流转中，不可删除') {
        testInfo.annotations.push({
          type: 'cleanup-pending',
          description: `Test fixture ${id} requires workflow completion before deletion`,
        });
      } else {
        expect(response.ok(), `cleanup own fixture ${id}: ${body.message}`).toBeTruthy();
      }
    }
  }
});
