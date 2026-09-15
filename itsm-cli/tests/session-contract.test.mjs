import assert from 'node:assert/strict';
import { mkdtemp, readFile, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

test('CLI uses one persistent cookie session with CSRF, refresh rotation and logout cleanup', async t => {
  const sessionDir = await mkdtemp(join(tmpdir(), 'itsm-cli-session-'));
  process.env.ITSM_CREDENTIALS_DIR = sessionDir;

  let ticketGets = 0;
  let sawMutationCSRF = false;
  let sawRotatedSession = false;
  let sawLogoutCSRF = false;
  let sawCanonicalBPMNDecision = false;
  let tenantContextGets = 0;

  const server = createServer((request, response) => {
    assert.equal(request.headers.authorization, undefined);
    response.setHeader('Content-Type', 'application/json');

    if (request.url === '/api/v1/auth/login' && request.method === 'POST') {
      response.setHeader('Set-Cookie', [
        'access_token=access-one; Path=/; HttpOnly; SameSite=Lax',
        'refresh_token=refresh-one; Path=/; HttpOnly; SameSite=Lax',
      ]);
      response.end(JSON.stringify({
        code: 0,
        message: 'success',
        data: {
          user: { id: 7, username: 'admin', name: 'Admin', email: 'a@example.com', role: 'super_admin', tenantId: 3 },
        },
      }));
      return;
    }

    if (request.url === '/api/v1/auth/tenants' && request.method === 'GET') {
      tenantContextGets += 1;
      assert.match(request.headers.cookie ?? '', /access_token=access-one/);
      response.end(JSON.stringify({
        code: 0,
        message: 'success',
        data: { tenants: [{ id: 3, name: 'Tenant A', code: 'tenant-a' }] },
      }));
      return;
    }

    if (request.url === '/api/v1/csrf-token' && request.method === 'GET') {
      assert.match(request.headers.cookie ?? '', /access_token=/);
      response.setHeader('Set-Cookie', 'csrf_token=csrf-cookie; Path=/; SameSite=Lax');
      response.end(JSON.stringify({ code: 0, message: 'success', data: { csrf_token: 'csrf-header' } }));
      return;
    }

    if (request.url === '/api/v1/tickets' && request.method === 'POST') {
      sawMutationCSRF = request.headers['x-csrf-token'] === 'csrf-header'
        && /csrf_token=csrf-cookie/.test(request.headers.cookie ?? '');
      response.end(JSON.stringify({ code: 0, message: 'success', data: { id: 11, title: 'created' } }));
      return;
    }

    if (request.url?.startsWith('/api/v1/tickets') && request.method === 'GET') {
      ticketGets += 1;
      if (ticketGets === 1) {
        response.statusCode = 401;
        response.end(JSON.stringify({ code: 2001, message: 'expired', data: null }));
        return;
      }
      sawRotatedSession = /access_token=access-two/.test(request.headers.cookie ?? '')
        && /refresh_token=refresh-two/.test(request.headers.cookie ?? '');
      response.end(JSON.stringify({ code: 0, message: 'success', data: { items: [], total: 0 } }));
      return;
    }

    if (request.url === '/api/v1/auth/refresh' && request.method === 'POST') {
      assert.match(request.headers.cookie ?? '', /refresh_token=refresh-one/);
      response.setHeader('Set-Cookie', [
        'access_token=access-two; Path=/; HttpOnly; SameSite=Lax',
        'refresh_token=refresh-two; Path=/; HttpOnly; SameSite=Lax',
      ]);
      response.end(JSON.stringify({ code: 0, message: 'success', data: { user: { id: 7 } } }));
      return;
    }

    if (request.url === '/api/v1/auth/logout' && request.method === 'POST') {
      sawLogoutCSRF = request.headers['x-csrf-token'] === 'csrf-header';
      response.setHeader('Set-Cookie', [
        'access_token=; Path=/; Max-Age=0; HttpOnly',
        'refresh_token=; Path=/; Max-Age=0; HttpOnly',
      ]);
      response.end(JSON.stringify({ code: 0, message: 'success', data: {} }));
      return;
    }

    if (request.url?.startsWith('/api/v1/bpmn/process-instances') && request.method === 'GET') {
      response.end(JSON.stringify({ code: 0, message: 'success', data: { data: [], pagination: { total: 0, page: 1, pageSize: 20 } } }));
      return;
    }

    if (request.url?.startsWith('/api/v1/bpmn/tasks') && request.method === 'GET') {
      response.end(JSON.stringify({ code: 0, message: 'success', data: { data: [], pagination: { total: 0, page: 1, pageSize: 20 } } }));
      return;
    }

    if (request.url === '/api/v1/bpmn/tasks/17/decisions' && request.method === 'POST') {
      sawCanonicalBPMNDecision = true;
      response.end(JSON.stringify({ code: 0, message: 'success', data: null }));
      return;
    }

    response.statusCode = 404;
    response.end(JSON.stringify({ code: 404, message: 'not found', data: null }));
  });

  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const address = server.address();
  assert.ok(address && typeof address !== 'string');

  const [{ ApiClient }, credentials] = await Promise.all([
    import('../dist/lib/api-client.js'),
    import('../dist/lib/credentials.js'),
  ]);
  const client = new ApiClient(`http://127.0.0.1:${address.port}`);

  const login = await client.login({ username: 'admin', password: 'admin123' });
  assert.equal(login.user.username, 'admin');
  assert.equal(login.tenant.id, login.user.tenantId);
  assert.equal(tenantContextGets, 1);
  assert.equal('token' in login, false);
  assert.equal('access_token' in login, false);
  assert.equal('refresh_token' in login, false);

  const credentialsFile = join(sessionDir, 'credentials');
  assert.equal((await stat(sessionDir)).mode & 0o777, 0o700);
  assert.equal((await stat(credentialsFile)).mode & 0o777, 0o600);
  const persisted = JSON.parse(await readFile(credentialsFile, 'utf8'));
  assert.equal(typeof persisted.cookieHeader, 'string');
  assert.equal('token' in persisted, false);
  assert.equal('refreshToken' in persisted, false);

  await client.createTicket({ title: 'created', description: 'body', priority: 'low' });
  assert.equal(sawMutationCSRF, true);

  await client.listTickets();
  assert.equal(ticketGets, 2);
  assert.equal(sawRotatedSession, true);

  await client.listProcessInstances();
  await client.listUserTasks();
  await client.submitTaskDecision('17', 'approve', 'approved by CLI');
  assert.equal(sawCanonicalBPMNDecision, true);

  await client.logout();
  assert.equal(sawLogoutCSRF, true);
  assert.equal(credentials.loadCredentials(), null);
  assert.equal(credentials.isLoggedIn(), false);
});
