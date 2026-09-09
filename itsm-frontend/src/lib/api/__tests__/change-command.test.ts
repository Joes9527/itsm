import { ChangeApi } from '../change-api';
import { ApiError, httpClient } from '../http-client';

jest.mock('@/lib/security', () => ({
  security: {
    csrf: { getToken: jest.fn().mockResolvedValue('csrf'), clearToken: jest.fn() },
    network: { getSecureHeaders: () => ({ 'Content-Type': 'application/json' }) },
  },
}));
jest.mock('@/lib/env', () => ({ logger: { debug: jest.fn(), warn: jest.fn(), error: jest.fn() } }));
const meta = { expectedVersion: 7, operationId: 'observed-operation' };
const result = { workItemId: 31, version: 8, status: 'pending', replayed: false };
const reply = (status: number, data: unknown) => ({
  ok: status >= 200 && status < 300,
  status,
  headers: new Headers(),
  json: async () => ({ code: status === 409 ? 409 : 0, message: 'response', data }),
});
beforeEach(() => {
  global.fetch = jest.fn();
});

test('detail keeps semantic action/task map keys, default clients still normalize legacy DTOs', async () => {
  (fetch as jest.Mock).mockResolvedValue(
    reply(200, {
      actions: { record_outcome: { allowed: true } },
      currentTasks: { record_outcome: 'task-9' },
    })
  );
  expect(await ChangeApi.getChange(1)).toMatchObject({
    currentTasks: { record_outcome: 'task-9' },
  });
  (fetch as jest.Mock).mockResolvedValue(reply(200, { legacy_field: 'value' }));
  expect(await httpClient.get('/legacy')).toEqual({ legacyField: 'value' });
});

test('exact action facts, observed version and stable key survive identical retry', async () => {
  (fetch as jest.Mock).mockResolvedValue(
    reply(200, { progress: 'completed', taskId: 'task-9', executionKey: 'execution', result })
  );
  const request = {
    ...meta,
    taskId: 'task-9',
    outcome: 'failed' as const,
    evidence: 'check failed',
    actualEndDate: '2026-09-09T01:00:00Z',
  };
  await ChangeApi.executeAction(1, 'record_outcome', request);
  await ChangeApi.executeAction(1, 'record_outcome', request);
  for (const [url, init] of (fetch as jest.Mock).mock.calls) {
    expect(url).toContain('/changes/1/record-outcome');
    expect(JSON.parse(init.body)).toEqual(request);
    expect(init.headers['X-CSRF-Token']).toBe('csrf');
  }
});

test.each(['pending', 'processing', 'effect_applied'])(
  '200 %s preserves actual effect without inventing workflow completion',
  async progress => {
    (fetch as jest.Mock).mockResolvedValue(
      reply(200, { progress, taskId: 'task-9', executionKey: 'execution', result })
    );
    expect(
      await ChangeApi.executeAction(1, 'assess', {
        ...meta,
        taskId: 'task-9',
        evidence: 'reviewed',
      })
    ).toMatchObject({ progress, result });
  }
);

test('202 is acceptance only; 200 without actual receipt rejects', async () => {
  const progress = { progress: 'pending', taskId: 'task-9', executionKey: 'execution' };
  (fetch as jest.Mock)
    .mockResolvedValueOnce(reply(202, progress))
    .mockResolvedValueOnce(reply(200, progress));
  expect(
    await ChangeApi.executeAction(1, 'assess', { ...meta, taskId: 'task-9', evidence: 'reviewed' })
  ).toEqual(progress);
  await expect(
    ChangeApi.executeAction(1, 'assess', { ...meta, taskId: 'task-9', evidence: 'reviewed' })
  ).rejects.toThrow();
});

test('409 retains blocked durable receipt; ordinary errors remain errors', async () => {
  const data = {
    progress: 'blocked',
    taskId: 'task-9',
    executionKey: 'execution',
    reason: 'handler_contract',
    result,
  };
  (fetch as jest.Mock)
    .mockResolvedValueOnce(reply(409, data))
    .mockResolvedValueOnce(reply(409, { errorCode: 'version_conflict' }));
  expect(await ChangeApi.getTaskProgress(1, 'operation', 'assess')).toEqual(data);
  await expect(ChangeApi.executeAction(1, 'submit', meta)).rejects.toBeInstanceOf(ApiError);
});

test('PIR writes return immutable receipt and use exact change identity', async () => {
  (fetch as jest.Mock).mockResolvedValue(reply(200, { ...result, pirId: 4 }));
  expect(
    await ChangeApi.updatePIR(4, { ...meta, changeId: 1, overallResult: 'rolled_back' })
  ).toEqual({ ...result, pirId: 4 });
  await ChangeApi.deletePIR(4, { ...meta, changeId: 1 });
  expect(JSON.parse((fetch as jest.Mock).mock.calls[1][1].body)).toEqual({ ...meta, changeId: 1 });
});

test.each([
  ['submit', {}],
  ['cancel', { evidence: 'cancel reason' }],
  ['assess', { taskId: 'task', evidence: 'risk checked' }],
  ['approve', { taskId: 'task' }],
  ['reject', { taskId: 'task', evidence: 'denied' }],
  [
    'schedule',
    {
      taskId: 'task',
      plannedStartDate: '2026-09-10T01:00:00Z',
      plannedEndDate: '2026-09-10T02:00:00Z',
    },
  ],
  ['implement', { taskId: 'task' }],
  ['review', { taskId: 'task', evidence: 'reviewed', pirId: 4 }],
  ['close', { taskId: 'task', evidence: 'closed', pirId: 4 }],
] as const)('action %s sends only its concrete facts', async (action, facts) => {
  (fetch as jest.Mock).mockResolvedValue(
    reply(
      200,
      action === 'submit' || action === 'cancel'
        ? result
        : { progress: 'completed', taskId: 'task', executionKey: 'key', result }
    )
  );
  await ChangeApi.executeAction(1, action, { ...meta, ...facts });
  expect(JSON.parse((fetch as jest.Mock).mock.calls[0][1].body)).toEqual({ ...meta, ...facts });
});

test('metadata, risk, assignment and PIR creation require receipts and versioned exact bodies', async () => {
  (fetch as jest.Mock).mockResolvedValue(reply(200, result));
  await ChangeApi.updateChange(1, { ...meta, title: 'corrected title' });
  await ChangeApi.updateRisk(1, { ...meta, impactAnalysis: 'checked impact' });
  await ChangeApi.assignChange(1, { ...meta, assigneeId: 23 });
  (fetch as jest.Mock).mockResolvedValue(reply(200, { ...result, pirId: 4 }));
  await ChangeApi.createPIR(1, {
    ...meta,
    overallResult: 'failed',
    objectivesAchieved: false,
    rollbackPerformed: false,
  });
  expect((fetch as jest.Mock).mock.calls.map(([, init]) => JSON.parse(init.body))).toEqual([
    { ...meta, title: 'corrected title' },
    { ...meta, impactAnalysis: 'checked impact' },
    { ...meta, assigneeId: 23 },
    { ...meta, overallResult: 'failed', objectivesAchieved: false, rollbackPerformed: false },
  ]);
});
