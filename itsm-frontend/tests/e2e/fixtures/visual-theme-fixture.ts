import type { Page, Route } from '@playwright/test';

const actor = {
  id: 901,
  username: 'visual-admin',
  fullName: 'Visual Theme Admin',
  email: 'visual-admin@example.invalid',
  role: 'admin',
  tenantId: 1,
  actorTenantId: 1,
  permissions: ['ai:use'],
};

const tenant = {
  id: 1,
  code: 'visual',
  name: 'Visual Test Tenant',
  status: 'active',
};

const catalog = {
  id: 101,
  name: '标准笔记本申请',
  category: '设备与办公',
  description: '用于主题回归的固定服务目录条目',
  deliveryTime: '8',
  status: 'enabled',
  requiresApproval: false,
  createdAt: '2026-09-09T00:00:00Z',
  updatedAt: '2026-09-09T00:00:00Z',
};

function json(route: Route, data: unknown, headers: Record<string, string> = {}) {
  return route.fulfill({
    status: 200,
    contentType: 'application/json',
    headers,
    body: JSON.stringify({ code: 0, message: 'ok', data }),
  });
}

function pathOf(route: Route) {
  return new URL(route.request().url()).pathname;
}

function isLoopback(route: Route) {
  const hostname = new URL(route.request().url()).hostname;
  return hostname === '127.0.0.1' || hostname === 'localhost' || hostname === '::1';
}

function isExact(route: Route, method: string, path: string) {
  return isLoopback(route) && route.request().method() === method && pathOf(route) === path;
}

/** Exact, fail-closed transport for visual-theme browser checks. */
export async function installVisualThemeFixture(page: Page) {
  // Register fallback first: Playwright gives the most recently registered
  // matching route priority, so the exact handlers below take precedence.
  await page.route('**/api/**', async route => {
    const method = route.request().method();
    await route.fulfill({
      status: ['GET', 'HEAD'].includes(method) ? 404 : 405,
      contentType: 'application/json',
      body: JSON.stringify({ code: 404, message: 'No visual-theme fixture contract', data: null }),
    });
  });

  await page.route('**/api/v1/csrf-token', async route => {
    if (!isExact(route, 'GET', '/api/v1/csrf-token')) return route.abort('blockedbyclient');
    await json(route, { csrf_token: 'visual-theme-csrf' });
  });

  await page.route('**/api/v1/auth/login', async route => {
    if (!isExact(route, 'POST', '/api/v1/auth/login')) return route.abort('blockedbyclient');
    const payload = route.request().postDataJSON() as Record<string, unknown>;
    if (
      payload.tenantCode !== 'visual' ||
      payload.username !== 'visual-admin' ||
      payload.password !== 'local-visual-only'
    ) {
      return route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ code: 401, message: 'invalid fixture credentials', data: null }),
      });
    }
    await json(route, {}, {
      'Set-Cookie': 'access_token=visual-session; Path=/; HttpOnly; SameSite=Lax',
    });
  });

  await page.route('**/api/v1/auth/me', async route => {
    if (!isExact(route, 'GET', '/api/v1/auth/me')) return route.abort('blockedbyclient');
    const headers = await route.request().allHeaders();
    if (!headers.cookie?.includes('access_token=visual-session')) {
      return route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ code: 401, message: 'fixture session required', data: null }),
      });
    }
    await json(route, actor);
  });

  await page.route('**/api/v1/auth/tenants', async route => {
    if (!isExact(route, 'GET', '/api/v1/auth/tenants')) return route.abort('blockedbyclient');
    await json(route, { tenants: [tenant] });
  });

  await page.route('**/api/v1/auth/menus', async route => {
    if (!isExact(route, 'GET', '/api/v1/auth/menus')) return route.abort('blockedbyclient');
    await json(route, {
      main: [{
        id: 101,
        name: '服务目录',
        path: '/service-catalog',
        icon: 'BookOpen',
        sortOrder: 1,
        tenantId: 1,
        isVisible: true,
        isEnabled: true,
        children: [],
      }],
      admin: [],
    });
  });

  await page.route('**/api/v1/service-catalogs**', async route => {
    if (!isExact(route, 'GET', '/api/v1/service-catalogs')) return route.abort('blockedbyclient');
    await json(route, { catalogs: [catalog], services: [catalog], total: 1, page: 1, size: 100 });
  });

  await page.route('**/api/v1/notifications**', async route => {
    if (!isExact(route, 'GET', '/api/v1/notifications')) return route.abort('blockedbyclient');
    await json(route, { notifications: [], total: 0, page: 1, pageSize: 10 });
  });
}
