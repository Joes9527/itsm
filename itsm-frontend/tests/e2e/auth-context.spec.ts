import { expect, test } from './fixtures/auth';

test('page role login establishes the session in the browser context', async ({
  page,
  loginAs,
}) => {
  await loginAs('user1');

  const appURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
  const me = await page.request.get(`${appURL}/api/v1/auth/me`);
  expect(me.status()).toBe(200);
  const payload = await me.json();
  expect(payload.data?.username).toBe('user1');

  await page.goto('/tickets');
  await expect(page).toHaveURL(/\/tickets(?:\?.*)?$/);
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/);
});
