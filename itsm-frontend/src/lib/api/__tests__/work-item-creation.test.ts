import { httpClient } from '../http-client';
import { security } from '@/lib/security';
jest.mock('@/lib/security', () => ({
  security: {
    csrf: { getToken: jest.fn().mockResolvedValue('csrf'), clearToken: jest.fn() },
    network: { getSecureHeaders: () => ({ 'Content-Type': 'application/json' }) },
  },
}));
const receipt = {
  workItemId: 41,
  number: 'WI-41',
  recordClass: 'generic',
  professionalReference: { type: '', id: 0 },
  workflowStartStatus: 'pending',
  replayed: false,
};
const response = (data: unknown, status = 200, code = 0): Response =>
  ({
    ok: status < 400,
    status,
    headers: new Headers({ 'X-Request-Id': 'rid-41' }),
    json: async () => ({ code, message: status === 403 ? 'CSRF token invalid' : 'conflict', data }),
    clone: () => response(data, status, code),
  }) as Response;
const fetchMock = jest.fn();
beforeEach(() => {
  global.fetch = fetchMock;
  fetchMock.mockReset();
  jest.mocked(security.csrf.getToken).mockResolvedValue('csrf');
});
it('retains structured error metadata after authentication refresh', async () => {
  const data = {
    errorCode: 'catalog_version_conflict',
    retryable: false,
    fieldErrors: { office_location: 'changed' },
  };
  fetchMock
    .mockResolvedValueOnce(response({}, 401))
    .mockResolvedValueOnce(response({}))
    .mockResolvedValueOnce(response(data, 409, 4001));
  await expect(httpClient.post('/create', {}, { skipCamelCaseBody: true })).rejects.toMatchObject({
    status: 409,
    code: 4001,
    requestId: 'rid-41',
    ...data,
  });
});
it.each(['csrf', 'auth'])(
  'preserves the confirmed key and exact body across %s retry',
  async retry => {
    fetchMock.mockResolvedValueOnce(response({}, retry === 'csrf' ? 403 : 401));
    if (retry === 'auth') fetchMock.mockResolvedValueOnce(response({}));
    fetchMock.mockResolvedValueOnce(response(receipt, 201));
    const body = { formData: { office_location: { team_name: '研发' } } };
    await expect(
      httpClient.post('/create', body, {
        headers: { 'Idempotency-Key': 'confirmed' },
        skipCamelCaseBody: true,
      })
    ).resolves.toEqual(receipt);
    const calls = fetchMock.mock.calls.filter(([url]) => url.endsWith('/create'));
    expect(calls).toHaveLength(2);
    for (const [, init] of calls) {
      expect(init.body).toBe(JSON.stringify(body));
      expect(init.headers['Idempotency-Key']).toBe('confirmed');
    }
  }
);
it('asserts submission context after CSRF work and before initial fetch', async () => {
  let current = 'old';
  jest.mocked(security.csrf.getToken).mockImplementationOnce(async () => {
    current = 'new';
    return 'csrf';
  });
  fetchMock.mockResolvedValue(response(receipt));
  await expect(
    httpClient.post(
      '/create',
      {},
      {
        assertSubmissionContext: () => {
          if (current !== 'old') throw new Error('context changed');
        },
      }
    )
  ).rejects.toThrow('context changed');
  expect(fetchMock).not.toHaveBeenCalled();
});
it.each(['csrf', 'auth'])(
  'asserts context again immediately before %s retry fetch',
  async retry => {
    let current = 'old';
    fetchMock.mockImplementationOnce(async () => {
      current = 'new';
      return response({}, retry === 'csrf' ? 403 : 401);
    });
    if (retry === 'auth') fetchMock.mockResolvedValueOnce(response({}));
    fetchMock.mockResolvedValue(response(receipt));
    await expect(
      httpClient.post(
        '/create',
        {},
        {
          assertSubmissionContext: () => {
            if (current !== 'old') throw new Error('context changed');
          },
        }
      )
    ).rejects.toThrow('context changed');
    expect(fetchMock.mock.calls.filter(([url]) => url.endsWith('/create'))).toHaveLength(1);
  }
);

import { TicketApi } from '../ticket-api';
import { IncidentAPI } from '../incident-api';
import { ProblemApi } from '../problem-api';
import { ChangeApi } from '../change-api';
import { StandardChangeApi } from '../standard-change-api';
import { ServiceCatalogApi } from '../service-catalog-api';
import { professionalCreationPath, creationReceiptMessage } from '../work-item-creation';
const options = { idempotencyKey: 'confirmed-key', assertSubmissionContext: jest.fn() };
it.each([
  ['ticket', '/api/v1/tickets', (body: any) => TicketApi.createTicket(body, options)],
  ['incident', '/api/v1/incidents', (body: any) => IncidentAPI.createIncident(body, options)],
  ['problem', '/api/v1/problems', (body: any) => ProblemApi.createProblem(body, options)],
  ['change', '/api/v1/changes', (body: any) => ChangeApi.createChange(body, options)],
  [
    'standard change',
    '/api/v1/standard-changes/7/instantiate',
    (body: any) => StandardChangeApi.instantiate(7, body, options),
  ],
  [
    'conversion',
    '/api/v1/incidents/8/convert-to-problem',
    (body: any) => IncidentAPI.convertToProblem(8, body, options),
  ],
  [
    'catalog',
    '/api/v1/service-requests',
    (body: any) => ServiceCatalogApi.createServiceRequest(body, options),
  ],
])(
  '%s posts exact confirmed fields and returns six-field receipt for new/replay',
  async (_name, path, create) => {
    const payload = {
      title: 'title',
      requesterId: 12,
      formData: { office_location: 'A' },
      catalogId: 2,
      recordClass: 'incident',
      catalogVersion: 'v1',
      formSchemaVersion: 's1',
      incident: { severity: 'high', subcategory: 'cpu', metadata: { raw_key: 'value' } },
    };
    for (const status of [201, 200]) {
      const expected = { ...receipt, replayed: status === 200 };
      fetchMock.mockResolvedValueOnce(response(expected, status));
      await expect(create(payload)).resolves.toEqual(expected);
      const [url, init] = fetchMock.mock.calls.at(-1)!;
      expect(url).toContain(path);
      expect(JSON.parse(init.body)).toEqual(payload);
      expect(init.headers['Idempotency-Key']).toBe('confirmed-key');
    }
  }
);
it('requires positive matching professional reference and explains committed process status', () => {
  const result = {
    ...receipt,
    workflowStartStatus: 'manual_intervention_required' as const,
    recordClass: 'change_request' as const,
    professionalReference: { type: 'change', id: 88 },
  };
  expect(professionalCreationPath(result, 'change')).toBe('/changes/88');
  expect(() => professionalCreationPath(result, 'problem')).toThrow('已创建');
  expect(() =>
    professionalCreationPath(
      { ...result, professionalReference: { type: 'change', id: 0 } },
      'change'
    )
  ).toThrow();
  expect(creationReceiptMessage(result)).toContain('WI-41 已创建，流程启动需要人工处理');
  expect(creationReceiptMessage(receipt as any)).toContain('流程启动排队中');
});
it.each(['initial', 'envelope', 'csrf'])(
  'preserves structured metadata on %s failure path',
  async path => {
    const details = {
      errorCode: 'field_validation_failed',
      retryable: false,
      fieldErrors: [{ field: 'office_location', message: 'required' }],
    };
    if (path === 'csrf') fetchMock.mockResolvedValueOnce(response({}, 403));
    fetchMock.mockResolvedValueOnce(response(details, path === 'envelope' ? 200 : 422, 4001));
    await expect(httpClient.post('/create', {})).rejects.toMatchObject({
      status: path === 'envelope' ? 200 : 422,
      code: 4001,
      ...details,
      requestId: 'rid-41',
    });
  }
);
it('plain permission 403 never repeats a create request', async () => {
  fetchMock.mockResolvedValueOnce({
    ...response({}, 403, 4031),
    clone: () => ({ json: async () => ({ message: 'permission denied' }) }),
  });
  await expect(
    httpClient.post('/create', {}, { headers: { 'Idempotency-Key': 'once' } })
  ).rejects.toMatchObject({ status: 403 });
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
it('a cancelled creation rejects as unknown instead of returning a success-like null', async () => {
  fetchMock.mockRejectedValueOnce(new DOMException('timeout', 'AbortError'));
  await expect(httpClient.post('/create', {}, options)).rejects.toMatchObject({
    name: 'AbortError',
  });
});
it('preserves improvement subtype on the wire while receiving a Generic WorkItem receipt', async () => {
  fetchMock.mockResolvedValueOnce(response(receipt, 201));
  const payload = { type: 'improvement', title: '改进服务指标', description: '为服务增加可追踪的度量指标', priority: 'medium' };
  const result = await TicketApi.createTicket(payload, options);
  expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual(payload);
  expect(result.recordClass).toBe('generic');
});

it('rejects a blank caller key before any creation fetch', async () => {
  await expect(TicketApi.createTicket({ title: 'draft' } as any, { ...options, idempotencyKey: '  ' })).rejects.toThrow('缺少已确认申请标识');
  expect(fetchMock).not.toHaveBeenCalled();
});

it('retains the guard and exact confirmed body through the fetch-style request entry', async () => {
  const guard = jest.fn();
  const body = JSON.stringify({ formData: { office_location: 'A' } });
  fetchMock.mockResolvedValueOnce(response(receipt, 201));
  await expect(httpClient.request('/create', {
    method: 'POST', body, skipCamelCaseBody: true,
    headers: { 'Idempotency-Key': 'existing-confirmation' }, assertSubmissionContext: guard,
  })).resolves.toEqual(receipt);
  expect(guard).toHaveBeenCalledTimes(1);
  expect(fetchMock.mock.calls[0][1].body).toBe(body);
  expect(fetchMock.mock.calls[0][1].headers['Idempotency-Key']).toBe('existing-confirmation');
});

it('preserves structured errors for existing object-style update callers with query parameters', async () => {
  const details = { errorCode: 'version_conflict', retryable: false, fieldErrors: { version: 'stale' } };
  fetchMock.mockResolvedValueOnce(response(details, 409, 4001));
  await expect(httpClient.request({ url: '/existing-record?view=full', method: 'PUT', params: { version: 2, omitted: undefined }, data: { title: 'edited' } })).rejects.toMatchObject({ status: 409, code: 4001, requestId: 'rid-41', ...details });
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(fetchMock.mock.calls[0][0]).toContain('/existing-record?view=full&version=2');
  expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ title: 'edited' });
});

it('treats unparseable successful creation responses as unknown and preserves the original request key', async () => {
  fetchMock.mockResolvedValueOnce({ ...response(receipt, 201), json: async () => { throw new SyntaxError('truncated'); } });
  await expect(TicketApi.createTicket({ title: 'confirmed' } as any, options)).rejects.toThrow('提交结果未知');
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(fetchMock.mock.calls[0][1].headers['Idempotency-Key']).toBe('confirmed-key');
});

it('does not retry an unparseable permission 403 when CSRF token retrieval also fails', async () => {
  jest.mocked(security.csrf.getToken).mockRejectedValueOnce(new Error('CSRF unavailable'));
  const malformed = { ...response({}, 403), json: async () => { throw new SyntaxError('HTML error'); }, clone: () => ({ json: async () => { throw new SyntaxError('HTML error'); } }) };
  fetchMock.mockResolvedValueOnce(malformed);
  await expect(TicketApi.createTicket({ title: 'confirmed' } as any, options)).rejects.toMatchObject({ status: 403, requestId: 'rid-41' });
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
