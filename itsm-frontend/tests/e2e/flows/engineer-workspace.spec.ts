import { test, expect } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { establishSession, loginAndReturn, mutateWithCSRF, DEFAULT_LOGIN } from '../auth-utils';

test('engineer handles a real assigned ticket and loses it after reassignment; guest cannot read queue', async ({
  page,
  request,
}, testInfo) => {
  test.setTimeout(120_000);
  const marker = `ENGINEER-WORKSPACE-${Date.now()}`;
  const password = randomBytes(24).toString('base64url');
  const users: number[] = [];
  let ticketId: number | undefined;
  await establishSession(request);
  const me = (await (await request.get('/api/v1/auth/me')).json()).data;
  const createUser = async (suffix: string, role: string) => {
    const username = `${marker.toLowerCase()}-${suffix}`;
    const response = await mutateWithCSRF(request, 'POST', '/api/v1/users', {
      data: {
        username,
        password,
        role,
        name: `${marker} ${suffix}`,
        email: `${username}@example.invalid`,
        tenantId: me.tenantId,
      },
    });
    const body = await response.json();
    expect(
      response.ok(),
      `create test account HTTP ${response.status()}: ${body.message}`
    ).toBeTruthy();
    expect(body.code).toBe(0);
    users.push(body.data.id);
    testInfo.annotations.push({
      type: 'test-account',
      description: `${username} #${body.data.id}`,
    });
    return { id: body.data.id, username, password, tenantCode: DEFAULT_LOGIN.tenantCode };
  };
  try {
    const engineer = await createUser('engineer', 'ops_engineer');
    const guest = await createUser('guest', 'guest');
    const created = await mutateWithCSRF(request, 'POST', '/api/v1/tickets', {
      headers: { 'Idempotency-Key': marker },
      data: {
        title: marker,
        description: 'Engineer workspace acceptance fixture',
        type: 'ticket',
        source: 'manual',
        priority: 'low',
        assigneeId: engineer.id,
      },
    });
    const receipt = await created.json();
    expect(created.ok(), `create ticket: ${receipt.message}`).toBeTruthy();
    expect(receipt.code).toBe(0);
    ticketId = receipt.data.workItemId;
    expect(ticketId).toBeGreaterThan(0);
    testInfo.annotations.push({ type: 'test-ticket', description: `${marker} #${ticketId}` });
    await loginAndReturn(page, engineer, '/workspace/tickets');
    const engineerMe = (await (await page.request.get('/api/v1/auth/me')).json()).data;
    expect(engineerMe.role).toBe('ops_engineer');
    expect(engineerMe.permissions).toContain('ticket:read');
    const queue = page.getByRole('complementary', { name: '个人工单队列' });
    await queue.getByRole('button', { name: new RegExp(marker) }).click();
    await expect(page.getByRole('heading', { name: new RegExp(marker) })).toBeVisible();
    const comment = `${marker} verified comment`;
    await page.getByPlaceholder('输入您的评论或内部评估记录...').fill(comment);
    const saved = page.waitForResponse(
      res =>
        res.url().endsWith(`/tickets/${ticketId}/comments`) && res.request().method() === 'POST'
    );
    await page.getByRole('button', { name: '发送评论' }).click();
    expect((await saved).ok()).toBeTruthy();
    await page.reload();
    await expect(page.getByText(comment, { exact: true })).toBeVisible();
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
    // Reassign only the fixture using the existing domain endpoint; then verify the original user's queue refresh.
    const reassigned = await mutateWithCSRF(request, 'POST', `/api/v1/tickets/${ticketId}/assign`, {
      data: { assigneeId: me.id },
    });
    expect(reassigned.ok(), `reassign fixture HTTP ${reassigned.status()}`).toBeTruthy();
    await queue.getByRole('button', { name: '刷新队列' }).click();
    await expect(queue.getByRole('button', { name: new RegExp(marker) })).toHaveCount(0);
    await expect(page.getByText(comment, { exact: true })).toHaveCount(0);
    await loginAndReturn(page, guest, '/workspace/tickets');
    const guestMe = (await (await page.request.get('/api/v1/auth/me')).json()).data;
    expect(guestMe.permissions).not.toContain('ticket:read');
    expect(guestMe.permissions).not.toContain('*');
    const denied = await page.request.get(
      `/api/v1/tickets?page=1&pageSize=20&assigneeId=${guest.id}`
    );
    expect(denied.status()).toBe(403);
    // Route-level denial is also valid: no detail content may remain visible.
    await expect(page.getByText(comment, { exact: true })).toHaveCount(0);
    await expect(page.getByRole('heading', { name: new RegExp(marker) })).toHaveCount(0);
  } finally {
    const cleanupErrors: string[] = [];
    if (ticketId) {
      try {
        const response = await mutateWithCSRF(request, 'DELETE', `/api/v1/tickets/${ticketId}`);
        const body = await response.json();
        if (response.status() === 403 && body.message === '工单流程流转中，不可删除') {
          testInfo.annotations.push({
            type: 'cleanup-pending',
            description: `${marker} #${ticketId} retained: active workflow`,
          });
        } else if (!response.ok() || body.code !== 0) {
          cleanupErrors.push(`ticket #${ticketId}: HTTP ${response.status()} code ${body.code}`);
        }
      } catch {
        cleanupErrors.push(`ticket #${ticketId}: cleanup request failed`);
      }
    }
    // Sequential requests: this cookie jar's CSRF token rotates after each successful mutation.
    for (const id of users) {
      try {
        const response = await mutateWithCSRF(request, 'PUT', `/api/v1/users/${id}/status`, {
          data: { active: false },
        });
        const body = await response.json();
        if (!response.ok() || body.code !== 0)
          cleanupErrors.push(`account #${id}: deactivation failed`);
        const persisted = await request.get(`/api/v1/users/${id}`);
        expect(persisted.ok()).toBeTruthy();
        expect((await persisted.json()).data.active).toBe(false);
      } catch {
        cleanupErrors.push(`account #${id}: deactivation or verification failed`);
      }
    }
    expect(cleanupErrors, 'All fixture cleanup attempts completed').toEqual([]);
  }
});
