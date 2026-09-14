import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

test('portal keeps direct help and catalog reachable while knowledge integration is deferred', async ({ page }, testInfo) => {
  const errors: string[] = [];
  const searches: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    if (/\/api\/v1\/knowledge\/search/.test(request.url())) searches.push(request.url());
  });
  await loginAndReturn(page, DEFAULT_LOGIN, '/portal');
  const search = page.getByRole('searchbox', { name: '搜索知识库' });
  await expect(search).toBeDisabled();
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(page.getByRole('button', { name: '提交问题 / 寻求帮助', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '申请服务', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`portal-${width}.png`), fullPage: true });
  }
  const help = page.getByRole('button', { name: '提交问题 / 寻求帮助', exact: true });
  await help.focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/tickets\/create\?entry=help$/);
  await expect(page.getByLabel(/^标题/)).toBeVisible();
  await expect(page.getByText('选择创建目标', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '获取 AI 建议' })).toHaveCount(0);
  await page.goto('/portal');
  await page.getByRole('button', { name: '申请服务', exact: true }).click();
  await expect(page).toHaveURL(/\/service-catalog$/);
  expect(searches).toEqual([]);
  expect(errors).toEqual([]);
});
