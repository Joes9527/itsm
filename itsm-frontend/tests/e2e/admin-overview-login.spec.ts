import { expect, test } from '@playwright/test';

test('admin login lands directly on the canonical overview without a client exception', async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on('pageerror', error => pageErrors.push(error.message));

  await page.goto('/login');
  const username = page.locator('input.ant-input').first();
  const password = page.locator('input[type="password"]');
  const submit = page.locator('button[type="submit"]');

  await expect(username).toHaveValue('admin');
  await expect(password).toHaveValue('admin123');
  await expect
    .poll(() => submit.evaluate(element => Object.keys(element).some(key => key.startsWith('__reactProps$'))))
    .toBe(true);

  await username.click();
  await username.press('Control+A');
  await username.pressSequentially('admin');
  await password.click();
  await password.press('Control+A');
  await password.pressSequentially('admin123');

  const loginResponse = page.waitForResponse(
    response =>
      response.url().includes('/api/v1/auth/login') && response.request().method() === 'POST'
  );
  await submit.click();
  const response = await loginResponse;
  expect(response.ok()).toBe(true);
  expect((await response.json()).code).toBe(0);

  await expect(page).toHaveURL(/\/admin\/overview$/, { timeout: 20_000 });
  await expect(page.locator('h1').first()).toBeVisible();
  await expect(page.getByText('系统概览', { exact: true }).first()).toBeVisible();
  expect(pageErrors).toEqual([]);
});
