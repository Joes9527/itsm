import { test, expect } from '@playwright/test';
import { Journey } from './workitem-convergence';

type FakeResponse = { status: () => number; text: () => Promise<string>; json: () => Promise<unknown> };

function reply(status: number, body: unknown): FakeResponse {
  const text = JSON.stringify(body);
  return { status: () => status, text: async () => text, json: async () => JSON.parse(text) };
}

// Exercises the real Journey.changeTask contract boundary with a fake HTTP
// transport. The owning Change task API may accept with 200 or 202 and exposes
// continuation state through GET /changes/:id/task-progress.
function harness(options: { mutationStatus?: number; progressStatuses?: number[]; progressValues?: string[] }) {
  const mutationStatus = options.mutationStatus ?? 202;
  const progressStatuses = options.progressStatuses ?? [202];
  const progressValues = options.progressValues ?? ['processing'];
  let reads = 0;
  let completed = false;
  const request = {
    get: async (url: string) => {
      if (url.includes('/bpmn/tasks')) {
        return reply(200, { code: 0, data: { data: [{ taskDefinitionKey: 'Activity_Assessment', status: 'pending', taskId: 'T1', businessId: 11, businessType: 'change_request' }] } });
      }
      if (url.includes('/task-progress')) {
        const index = Math.min(reads, progressValues.length - 1);
        const value = progressValues[index];
        const status = progressStatuses[Math.min(reads, progressStatuses.length - 1)];
        reads += 1;
        if (value === 'completed' || value === 'effect_applied') completed = true;
        return reply(status, { code: status === 409 ? 409 : 0, data: {
          progress: value, taskId: 'T1', executionKey: 'E1',
          ...(value === 'completed' ? { result: { workItemId: 11, version: 2, status: 'submitted', replayed: false } } : {}),
        } });
      }
      if (/\/api\/v1\/changes\/11$/.test(url)) {
        return reply(200, { code: 0, data: { version: completed ? 2 : 1 } });
      }
      throw new Error('unexpected GET ' + url);
    },
  } as any;
  const journey = new Journey(request, 'http://api.test');
  (journey as any).raw = async () => {
    if (mutationStatus === 200) completed = true;
    return reply(mutationStatus, { code: 0, data: {
      progress: mutationStatus === 200 ? 'completed' : 'processing', taskId: 'T1', executionKey: 'E1',
      ...(mutationStatus === 200 ? { result: { workItemId: 11, version: 2, status: 'submitted', replayed: false } } : {}),
    } });
  };
  const change = { domain: 'changes' as const, id: 11, workItemId: 11, number: 'CHG0000011' };
  return { journey, change, reads: () => reads };
}

test('changeTask accepts 202 and polls task-progress to completion', async () => {
  const h = harness({ mutationStatus: 202, progressStatuses: [202, 200], progressValues: ['processing', 'completed'] });
  const result = await h.journey.changeTask(h.change, 'assess', 'Activity_Assessment', { evidence: 'isolated' });
  expect(result.progress).toBe('completed');
  expect(result.result.version).toBe(2);
  expect(h.reads()).toBe(2);
});

test('changeTask keeps the completed 200 receipt path', async () => {
  const h = harness({ mutationStatus: 200 });
  const result = await h.journey.changeTask(h.change, 'assess', 'Activity_Assessment', { evidence: 'isolated' });
  expect(result.progress).toBe('completed');
  expect(result.result.version).toBe(2);
  expect(h.reads()).toBe(0);
});

test('changeTask fails closed on blocked continuation', async () => {
  const h = harness({ mutationStatus: 202, progressStatuses: [409], progressValues: ['blocked'] });
  await expect(h.journey.changeTask(h.change, 'assess', 'Activity_Assessment', { evidence: 'isolated' }))
    .rejects.toThrow(/Change task blocked/);
});
