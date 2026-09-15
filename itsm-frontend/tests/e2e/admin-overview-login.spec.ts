import { expect, test } from '@playwright/test';
import { DEFAULT_LOGIN } from './auth-utils';

test('admin login lands directly on the canonical overview without a client exception', async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on('pageerror', error => pageErrors.push(error.message));

  await page.goto('/login');
  const inputs = page.locator('form input');
  await expect(inputs).toHaveCount(3);
  const tenantCode = inputs.nth(0);
  const username = inputs.nth(1);
  const password = inputs.nth(2);
  const submit = page.getByRole('button', { name: /^登录$/ });

  await expect(username).toHaveValue('admin');
  await expect(password).toHaveValue('admin123');
  await expect
    .poll(() => submit.evaluate(element => Object.keys(element).some(key => key.startsWith('__reactProps$'))))
    .toBe(true);

  await tenantCode.click();
  await tenantCode.pressSequentially(DEFAULT_LOGIN.tenantCode);

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
