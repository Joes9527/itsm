import { test, expect } from '@playwright/test';
import { loginAndReturn } from './auth-utils';
import { verifyCreationAndReplay } from './creation.test-utils';

// Requires an authorized super_admin session and an active Standard Change template.
// Creation prerequisites (Catalog/process definitions) are provisioned separately.
test('Standard Change creation uses the professional reference and replays one receipt', async ({ page }) => {
  await loginAndReturn(page);
  await page.goto('/standard-changes');
  const instantiate = page.getByRole('button', { name: '从模板创建变更' }).first();
  await expect(instantiate).toBeVisible();
  await instantiate.click();
  const dialog = page.getByRole('dialog');
  const title = `E2E Standard Change ${Date.now()}`;
  await dialog.getByLabel('变更标题').fill(title);
  const pending = page.waitForResponse(response => response.request().method() === 'POST' && /\/standard-changes\/\d+\/instantiate$/.test(new URL(response.url()).pathname));
  await dialog.getByRole('button', { name: '创建变更' }).click();
  const receipt = await verifyCreationAndReplay(page, await pending);
  expect(receipt.professionalReference.type).toBe('change');
  expect(receipt.professionalReference.id).toBeGreaterThan(0);
  await expect(page).toHaveURL(new RegExp(`/changes/${receipt.professionalReference.id}$`));
  await expect(page.getByText(title, { exact: true }).first()).toBeVisible();
});

test('Incident-to-Problem creation preserves the source and navigates by the new professional ID', async ({ page }) => {
  await loginAndReturn(page);
  await page.goto('/incidents/create');
  const title = `E2E Source Incident ${Date.now()}`;
  await page.getByTestId('incident-title-input').fill(title);
  await page.getByTestId('incident-description-input').fill('持续服务异常，需要创建关联问题进行根因分析。');
  const sourcePending = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/incidents');
  await page.getByTestId('incident-submit-button').click();
  const source = await verifyCreationAndReplay(page, await sourcePending);
  expect(source.professionalReference.type).toBe('incident');
  await page.goto(`/incidents/${source.professionalReference.id}`);
  await page.getByRole('button', { name: '转为问题' }).click();
  const pending = page.waitForResponse(response => response.request().method() === 'POST' && /\/incidents\/\d+\/convert-to-problem$/.test(new URL(response.url()).pathname));
  await page.getByRole('dialog', { name: '创建关联问题' }).getByRole('button', { name: /确定|OK/ }).click();
  const receipt = await verifyCreationAndReplay(page, await pending);
  expect(receipt.professionalReference.type).toBe('problem');
  expect(receipt.professionalReference.id).toBeGreaterThan(0);
  expect(receipt.workItemId).not.toBe(source.workItemId);
  await expect(page).toHaveURL(new RegExp(`/problems/${receipt.professionalReference.id}$`));
  const original = await page.request.get(`/api/v1/incidents/${source.professionalReference.id}`);
  expect(original.status()).toBe(200);
  expect((await original.json()).data.title).toBe(title);
});
