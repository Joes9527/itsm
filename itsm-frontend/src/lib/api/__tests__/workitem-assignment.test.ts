import {
  assignIncidentWorkItem,
  assignProblemWorkItem,
  assignChangeWorkItem,
} from '../workitem-assignment';
import { httpClient } from '../http-client';
jest.mock('../http-client', () => ({
  httpClient: { post: jest.fn(), put: jest.fn(), request: jest.fn() },
  ApiError: class extends Error {},
}));
const input = { assigneeId: 8, reason: 'handover', version: 7, operationId: 'operation-1' };
beforeEach(() => jest.clearAllMocks());
test('Incident route and payload retain professional ID/version/reason', async () => {
  await assignIncidentWorkItem(4, input);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/incidents/4/assign', input);
});
test('Problem maps only assignmentReason and retains version', async () => {
  await assignProblemWorkItem(4, input);
  expect(httpClient.put).toHaveBeenCalledWith('/api/v1/problems/4', {
    assigneeId: 8,
    assignmentReason: 'handover',
    version: 7,
    operationId: 'operation-1',
  });
});
test('Change maps expectedVersion without a version alias', async () => {
  await assignChangeWorkItem(4, input);
  expect(httpClient.request).toHaveBeenCalledWith(
    '/api/v1/changes/4/assign',
    expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({
        assigneeId: 8,
        operationId: 'operation-1',
        assignmentReason: 'handover',
        expectedVersion: 7,
      }),
    })
  );
  const body = JSON.parse((httpClient.request as jest.Mock).mock.calls[0][1].body as string);
  expect(body).not.toHaveProperty('version');
  expect(body).not.toHaveProperty('reason');
});
