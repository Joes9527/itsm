import { TicketAttachmentApi } from '../ticket-attachment-api';
import { httpClient, ApiError } from '../http-client';
import { security } from '@/lib/security';

jest.mock('@/lib/env', () => ({ logger: { debug: jest.fn(), warn: jest.fn(), error: jest.fn() } }));
jest.mock('@/lib/security', () => ({
  security: {
    csrf: { getToken: jest.fn().mockResolvedValue('csrf-current'), clearToken: jest.fn() },
    network: { getSecureHeaders: () => ({ 'Content-Type': 'application/json' }) },
  },
}));

// Browser transport double: the API client, envelope parsing and retry policy stay real.
class TestXHR extends EventTarget {
  static sent: TestXHR[] = [];
  static replies: Array<{ status?: number; body?: unknown; event?: string }> = [];
  upload = new EventTarget();
  headers: Record<string, string> = {};
  withCredentials = false;
  timeout = 0;
  status = 200;
  statusText = 'OK';
  responseText = '';
  url = '';
  body?: Document | XMLHttpRequestBodyInit | null;
  open(_method: string, url: string) {
    this.url = url;
  }
  setRequestHeader(key: string, value: string) {
    this.headers[key] = value;
  }
  getAllResponseHeaders() {
    return 'content-type: application/json\r\n';
  }
  send(body?: Document | XMLHttpRequestBodyInit | null) {
    this.body = body;
    TestXHR.sent.push(this);
    const reply = TestXHR.replies.shift() || {};
    this.status = reply.status ?? 200;
    this.responseText = JSON.stringify(
      reply.body ?? { code: 0, data: { id: 17, fileName: 'log.txt' } }
    );
    queueMicrotask(() => {
      this.upload.dispatchEvent(
        new ProgressEvent('progress', { lengthComputable: true, loaded: 4, total: 8 })
      );
      this.dispatchEvent(new Event(reply.event || 'load'));
    });
  }
}

const file = () => new File(['diagnostics'], 'log.txt', { type: 'text/plain' });
beforeEach(() => {
  TestXHR.sent = [];
  TestXHR.replies = [];
  jest
    .spyOn(global, 'XMLHttpRequest')
    .mockImplementation(() => new TestXHR() as unknown as XMLHttpRequest);
});

it('uploads with credentials, CSRF and progress and decodes code=0', async () => {
  const progress = jest.fn();
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), progress)).resolves.toMatchObject({
    id: 17,
  });
  const request = TestXHR.sent[0];
  expect(request.url).toBe('/api/v1/tickets/101/attachments');
  expect(request.withCredentials).toBe(true);
  expect(new Headers(request.headers).get('X-CSRF-Token')).toBe('csrf-current');
  expect(new Headers(request.headers).has('Content-Type')).toBe(false);
  expect((request.body as FormData).get('file')).toBeInstanceOf(File);
  expect(progress).toHaveBeenCalledWith(50);
  expect(security.csrf.clearToken).toHaveBeenCalled();
});

it('retries an explicit CSRF rejection once but preserves ordinary permission errors', async () => {
  TestXHR.replies = [{ status: 403, body: { code: 403, message: 'CSRF token mismatch' } }, {}];
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), jest.fn())).resolves.toMatchObject(
    { id: 17 }
  );
  expect(TestXHR.sent).toHaveLength(2);
  TestXHR.replies = [{ status: 403, body: { code: 403, message: 'Permission denied' } }];
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), jest.fn())).rejects.toMatchObject({
    status: 403,
  });
  expect(TestXHR.sent).toHaveLength(3);
});

it.each(['error', 'timeout', 'abort'])(
  'settles %s without retrying an uncertain upload',
  async event => {
    TestXHR.replies = [{ event }];
    await expect(
      TicketAttachmentApi.uploadAttachment(101, file(), jest.fn())
    ).rejects.toBeInstanceOf(Error);
    expect(TestXHR.sent).toHaveLength(1);
  }
);

it('uses the authenticated common request path without progress', async () => {
  global.fetch = jest.fn().mockResolvedValue({
    ok: true,
    status: 200,
    headers: new Headers(),
    json: async () => ({ code: 0, data: { id: 19 } }),
  });
  await expect(TicketAttachmentApi.uploadAttachment(101, file())).resolves.toEqual({ id: 19 });
  expect(global.fetch).toHaveBeenCalledWith(
    '/api/v1/tickets/101/attachments',
    expect.objectContaining({
      credentials: 'include',
      headers: expect.objectContaining({ 'x-csrf-token': 'csrf-current' }),
    })
  );
});

it('refreshes an expired session once and preserves final unauthorized response', async () => {
  TestXHR.replies = [
    { status: 401, body: { code: 401, message: 'expired' } },
    { status: 401, body: { code: 401, message: 'still denied' } },
  ];
  global.fetch = jest.fn().mockResolvedValue({ ok: true, json: async () => ({ code: 0 }) });
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), jest.fn())).rejects.toMatchObject({
    status: 401,
  });
  expect(TestXHR.sent).toHaveLength(2);
  expect(global.fetch).toHaveBeenCalledTimes(1);
});

it('recovers CSRF after a session refresh without replaying permission failures', async () => {
  TestXHR.replies = [
    { status: 401, body: { code: 401, message: 'expired' } },
    { status: 403, body: { code: 403, message: 'CSRF token mismatch' } },
    {},
  ];
  global.fetch = jest.fn().mockResolvedValue({ ok: true, json: async () => ({ code: 0 }) });
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), jest.fn())).resolves.toMatchObject(
    { id: 17 }
  );
  expect(TestXHR.sent).toHaveLength(3);
  expect(global.fetch).toHaveBeenCalledTimes(1);
});

it('preserves structured server failure in the shared multipart transport', async () => {
  TestXHR.replies = [{ body: { code: 4001, message: 'invalid attachment' } }];
  const form = new FormData();
  form.append('file', file());
  await expect(
    httpClient.post('/upload', form, { onUploadProgress: jest.fn() })
  ).rejects.toBeInstanceOf(ApiError);
});

it('does not replay an upload when its caller becomes stale during CSRF recovery', async () => {
  let current = true;
  jest.mocked(security.csrf.getToken).mockResolvedValueOnce('initial').mockImplementationOnce(async () => { current = false; return 'renewed'; });
  TestXHR.replies = [{ status: 403, body: { code: 4031, message: 'CSRF token mismatch' } }, {}];
  await expect(TicketAttachmentApi.uploadAttachment(101, file(), jest.fn(), () => {
    if (!current) throw new Error('identity changed');
  })).rejects.toThrow('identity changed');
  expect(TestXHR.sent).toHaveLength(1);
});
it('loads binary previews through the authenticated shared client', async () => {
  const blob = new Blob(['preview'], { type: 'text/plain' });
  global.fetch = jest.fn().mockResolvedValue({ ok: true, status: 200, headers: new Headers(), blob: async () => blob });
  await expect(TicketAttachmentApi.previewAttachment(101, 17)).resolves.toBe(blob);
  expect(global.fetch).toHaveBeenCalledWith('/api/v1/tickets/101/attachments/17/preview', expect.objectContaining({ credentials: 'include' }));
});