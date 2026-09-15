import { expect, test } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';

const API = process.env.ITSM_BACKEND_URL || 'http://localhost:8090';

async function login(request: APIRequestContext, username = 'admin', password = 'admin123') {
  const response = await request.post(`${API}/api/v1/auth/login`, { data: { username, password } });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.code).toBe(0);
  expect(body.data).not.toHaveProperty('accessToken');
  expect(body.data).not.toHaveProperty('refreshToken');
  const state = await request.storageState();
  expect(state.cookies.some(cookie => cookie.name === 'access_token' && cookie.httpOnly)).toBe(true);
  expect(state.cookies.some(cookie => cookie.name === 'refresh_token' && cookie.httpOnly)).toBe(true);
  return body.data;
}

async function csrfHeaders(request: APIRequestContext) {
  const response = await request.get(`${API}/api/v1/csrf-token`);
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.code).toBe(0);
  return { 'X-CSRF-Token': String(body.data.csrf_token) };
}

test.describe('User Management, Roles & Permissions Deep Testing', () => {
  test('login establishes an HttpOnly cookie session without JSON tokens', async ({ request }) => {
    const data = await login(request);
    expect(data.user).toHaveProperty('username', 'admin');
  });

  test('fails login with invalid credentials and creates no session', async ({ request }) => {
    const response = await request.post(`${API}/api/v1/auth/login`, {
      data: { username: 'invalid', password: 'wrongpass' },
    });
    const body = await response.json();
    expect(body.code).not.toBe(0);
    expect((await request.storageState()).cookies).toHaveLength(0);
  });

  test('lists and creates users as admin through the cookie session', async ({ request }) => {
    await login(request);
    const list = await request.get(`${API}/api/v1/users`);
    expect(list.ok()).toBeTruthy();
    const listBody = await list.json();
    expect(listBody.code).toBe(0);
    expect(Array.isArray(listBody.data.users)).toBeTruthy();

    const stamp = Date.now();
    const created = await request.post(`${API}/api/v1/users`, {
      headers: await csrfHeaders(request),
      data: {
        username: `testuser_${stamp}`,
        email: `test_${stamp}@example.com`,
        name: 'Test User',
        password: 'TestPass123',
        tenantId: 1,
        role: 'agent',
      },
    });
    expect(created.ok()).toBeTruthy();
    expect((await created.json()).code).toBe(0);
  });

  test('lists roles and manages a custom role as admin', async ({ request }) => {
    await login(request);
    const roles = await request.get(`${API}/api/v1/roles`);
    expect(roles.ok()).toBeTruthy();
    const roleBody = await roles.json();
    expect(roleBody.code).toBe(0);
    expect(roleBody.data.roles.map((role: { code?: string; name?: string }) => role.code || role.name)).toContain('admin');

    const created = await request.post(`${API}/api/v1/roles`, {
      headers: await csrfHeaders(request),
      data: {
        name: 'Custom Test Role',
        code: `custom_test_role_${Date.now()}`,
        description: 'Role for automated testing',
        permissions: ['ticket:read', 'ticket:write', 'knowledge:read'],
      },
    });
    expect(created.ok()).toBeTruthy();
    const createdBody = await created.json();
    expect(createdBody.code).toBe(0);
    await request.delete(`${API}/api/v1/roles/${createdBody.data.id}`, {
      headers: await csrfHeaders(request),
    });
  });

  test('denies an end user access to the administrator user list', async ({ request }) => {
    await login(request, 'user1', 'user123');
    const response = await request.get(`${API}/api/v1/users`);
    expect(response.status()).toBe(403);
  });

  test('rotates the cookie session through the canonical refresh endpoint', async ({ request }) => {
    await login(request);
    const before = (await request.storageState()).cookies.find(cookie => cookie.name === 'refresh_token')?.value;
    const response = await request.post(`${API}/api/v1/auth/refresh`, { data: {} });
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body.code).toBe(0);
    expect(body.data).not.toHaveProperty('accessToken');
    expect(body.data).not.toHaveProperty('refreshToken');
    const after = (await request.storageState()).cookies.find(cookie => cookie.name === 'refresh_token')?.value;
    expect(after).toBeTruthy();
    expect(after).not.toBe(before);
  });

  test('logout clears the cookie session', async ({ request }) => {
    await login(request);
    const response = await request.post(`${API}/api/v1/auth/logout`, {
      headers: await csrfHeaders(request),
    });
    expect(response.ok()).toBeTruthy();
    const cookies = (await request.storageState()).cookies;
    expect(cookies.some(cookie => cookie.name === 'access_token')).toBe(false);
    expect(cookies.some(cookie => cookie.name === 'refresh_token')).toBe(false);
  });
});
