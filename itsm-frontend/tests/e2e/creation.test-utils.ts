import { expect, type Page, type Response } from '@playwright/test';
import { mutateWithCSRF } from './auth-utils';

export function expectCreationReceipt(value: any) {
  expect(Object.keys(value).sort()).toEqual(
    [
      'workItemId',
      'number',
      'recordClass',
      'professionalReference',
      'workflowStartStatus',
      'replayed',
    ].sort()
  );
  expect(value.workItemId).toBeGreaterThan(0);
  expect(value.number).toEqual(expect.any(String));
  expect(value.number.length).toBeGreaterThan(0);
  expect(['active', 'pending', 'not_required', 'manual_intervention_required']).toContain(
    value.workflowStartStatus
  );
  return value;
}
export async function verifyCreationAndReplay(page: Page, response: Response) {
  expect(response.status()).toBe(201);
  const envelope = await response.json();
  expect(envelope.code).toBe(0);
  const receipt = expectCreationReceipt(envelope.data);
  expect(receipt.replayed).toBe(false);
  const key = response.request().headers()['idempotency-key'];
  expect(key).toBeTruthy();
  const replayResponse = await mutateWithCSRF(page.request, 'POST', response.url(), {
    data: response.request().postDataJSON(),
    headers: { 'Idempotency-Key': key },
  });
  expect(replayResponse.status()).toBe(200);
  const replay = expectCreationReceipt((await replayResponse.json()).data);
  expect(replay).toEqual({ ...receipt, replayed: true });
  const origin = new URL(response.url()).origin;
  const detail = await page.request.get(`${origin}/api/v1/tickets/${receipt.workItemId}`);
  expect(detail.status()).toBe(200);
  expect((await detail.json()).data.id).toBe(receipt.workItemId);
  return receipt;
}
