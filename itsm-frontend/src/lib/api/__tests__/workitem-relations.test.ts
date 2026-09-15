import { ChangeApi } from '../change-api';
import { StandardChangeApi } from '../standard-change-api';
import { WorkItemRelationsApi } from '../workitem-relations';
import { httpClient } from '../http-client';
import { IncidentAPI } from '../incident-api';
import { creationOptions, creationHttpOptions, creationReceipt } from '../creation.test-utils';
jest.mock('../http-client', () => ({
  httpClient: { get: jest.fn(), post: jest.fn(), request: jest.fn() },
}));
const body = {
  sourceWorkItemId: 101,
  targetWorkItemId: 202,
  relationType: 'resolved_by_change' as const,
  expectedVersion: 7,
  operationId: 'confirmed-operation',
  metadata: { required: true },
};
beforeEach(() => jest.clearAllMocks());
it('uses WorkItem identity and preserves exact typed add and delete payloads', async () => {
  const receipt = { workItemId: 101, version: 8, status: 'investigating', replayed: false };
  (httpClient.post as jest.Mock).mockResolvedValue(receipt);
  expect(await WorkItemRelationsApi.add(body)).toEqual(receipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/work-items/101/relations', body, {
    skipCamelCaseBody: true,
  });
  await WorkItemRelationsApi.remove(body);
  expect(httpClient.request).toHaveBeenCalledWith('/api/v1/work-items/101/relations', {
    method: 'DELETE',
    body: JSON.stringify(body),
    skipCamelCaseBody: true,
  });
});
it('reads sole relation projection without synthesizing empty denial', async () => {
  (httpClient.get as jest.Mock).mockRejectedValue(new Error('denied'));
  await expect(WorkItemRelationsApi.list(101)).rejects.toThrow('denied');
  expect(httpClient.get).toHaveBeenCalledWith('/api/v1/work-items/101/relations');
});
it('retains caller-observed conversion version at professional boundary', async () => {
  (httpClient.post as jest.Mock).mockResolvedValue(creationReceipt);
  const payload = { title: 'Investigate', expectedVersion: 7 };
  expect(await IncidentAPI.convertToProblem(9, payload, creationOptions)).toEqual(creationReceipt);
  expect(httpClient.post).toHaveBeenCalledWith(
    '/api/v1/incidents/9/convert-to-problem',
    payload,
    creationHttpOptions
  );
});

it('normal and standard Change creation preserve atomic source bindings and immutable receipts', async () => {
  const sourceRelations = [
    {
      sourceWorkItemId: 101,
      expectedVersion: 7,
      relationType: 'resolved_by_change' as const,
      metadata: { required: true },
    },
  ];
  (httpClient.post as jest.Mock).mockResolvedValue(creationReceipt);
  const payload = {
    title: 'Repair',
    description: 'Repair service',
    justification: 'Root cause',
    type: 'normal' as const,
    priority: 'high' as const,
    impactScope: 'low' as const,
    riskLevel: 'low' as const,
    implementationPlan: 'Apply',
    rollbackPlan: 'Restore',
    affectedCis: [],
    sourceRelations,
  };
  expect(await ChangeApi.createChange(payload, creationOptions)).toEqual(creationReceipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/changes', payload, creationHttpOptions);
  expect(await StandardChangeApi.instantiate(42, { sourceRelations }, creationOptions)).toEqual(
    creationReceipt
  );
  expect(httpClient.post).toHaveBeenCalledWith(
    '/api/v1/standard-changes/42/instantiate',
    { sourceRelations },
    creationHttpOptions
  );
  expect(httpClient.request).not.toHaveBeenCalled();
});
