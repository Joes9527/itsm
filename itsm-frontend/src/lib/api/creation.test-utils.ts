import type { CreateWorkItemResult, CreationRequestOptions } from './work-item-creation';
export const creationReceipt: CreateWorkItemResult = {
  workItemId: 41,
  number: 'WI-41',
  recordClass: 'generic',
  professionalReference: { type: '', id: 0 },
  workflowStartStatus: 'pending',
  replayed: false,
};
export const creationOptions: CreationRequestOptions = {
  idempotencyKey: 'confirmed-key',
  assertSubmissionContext: () => undefined,
};
export const creationHttpOptions = {
  headers: { 'Idempotency-Key': 'confirmed-key' },
  skipCamelCaseBody: true,
  assertSubmissionContext: creationOptions.assertSubmissionContext,
};
