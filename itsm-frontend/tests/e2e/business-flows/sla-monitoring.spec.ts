/**
 * SLA 监控完整 E2E 测试
 * 覆盖 SLA 定义、监控、告警、报表等核心功能
 */
import { test, expect } from '@playwright/test';
import { loginAs, TEST_USERS } from '../utils/test-utils';
import { establishSession, mutateWithCSRF } from '../auth-utils';
import { TicketPage } from '../utils/page-objects/TicketPage';

test.describe('SLA 监控完整测试', () => {
  test('SLA monitor loads and refreshes the canonical monitoring projection', async ({ page }) => {
    await loginAs(page, 'admin');

    const initialResponsePromise = page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'POST' && url.pathname === '/api/v1/sla/monitoring';
    });
    await page.goto('/sla-monitor');
    const initialResponse = await initialResponsePromise;
    expect(initialResponse.status()).toBe(200);
    const initialEnvelope = await initialResponse.json();
    expect(initialEnvelope).toHaveProperty('code', 0);
    expect(initialEnvelope).toHaveProperty('data');
    await expect(page.getByRole('heading', { name: 'SLA实时监控' })).toBeVisible();
    await expect(page.getByText('SLA总数', { exact: true })).toBeVisible();

    const refreshResponsePromise = page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'POST' && url.pathname === '/api/v1/sla/monitoring';
    });
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    const refreshResponse = await refreshResponsePromise;
    expect(refreshResponse.status()).toBe(200);
    const refreshEnvelope = await refreshResponse.json();
    expect(refreshEnvelope).toHaveProperty('code', 0);
    expect(refreshEnvelope).toHaveProperty('data');
    await expect(page.getByText('总体合规率', { exact: true })).toBeVisible();
  });

  test.describe('SLA API 接口测试', () => {
    test('GET /api/v1/sla SLA 列表接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 列表
      const response = await request.get(`${apiUrl}/api/v1/sla`);

      expect(response.status()).toBe(200);
      const body = await response.json();
      expect(body).toHaveProperty('code', 0);
      expect(body).toHaveProperty('data');
    });

    test('POST /api/v1/sla/monitoring 监控数据接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 监控数据
      const response = await mutateWithCSRF(request, 'POST', `${apiUrl}/api/v1/sla/monitoring`, {
        data: {},
      });

      expect(response.status()).toBe(200);
      const body = await response.json();
      expect(body).toHaveProperty('code', 0);
      expect(body).toHaveProperty('data');
    });

    test('GET /api/v1/sla/violations 告警接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 告警列表
      const response = await request.get(`${apiUrl}/api/v1/sla/violations`);

      expect(response.status()).toBe(200);
      const body = await response.json();
      expect(body).toHaveProperty('code', 0);
      expect(body).toHaveProperty('data');
    });
  });

  test.describe('SLA 与工单关联测试', () => {
    test('工单详情页显示 SLA 信息', async ({ page }) => {
      await loginAs(page, 'admin');

      const ticketPage = new TicketPage(page);
      await ticketPage.goto();

      // 检查表格是否存在
      await expect(page.locator('table')).toBeVisible();

      const ticketId = await ticketPage.getFirstTicketId();
      expect(ticketId).not.toBeNull();
      expect(Number(ticketId)).toBeGreaterThan(0);

      // 打开工单详情
      await ticketPage.openTicket(Number(ticketId));
      await page.waitForLoadState('domcontentloaded');

      // 检查是否有 SLA 相关信息
      await expect(page.getByText(/SLA|服务级别/).first()).toBeVisible();
    });
  });
});

/**
 * SLA 监控 API 集成测试
 */
test.describe('SLA 监控 API 集成测试', () => {
  test('完整的 SLA 监控流程 API', async ({ request }) => {
    const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

    // 1. 登录
    await establishSession(request, TEST_USERS.admin, apiUrl);

    // 2. 获取 SLA 列表
    const slaResponse = await request.get(`${apiUrl}/api/v1/sla`);
    expect(slaResponse.status()).toBe(200);
    const slaData = await slaResponse.json();
    expect(slaData).toHaveProperty('code', 0);
    expect(slaData).toHaveProperty('data');

    // 3. 获取 SLA 监控数据
    const monitorResponse = await mutateWithCSRF(request, 'POST', `${apiUrl}/api/v1/sla/monitoring`, {
      data: {},
    });
    expect(monitorResponse.status()).toBe(200);
    const monitorData = await monitorResponse.json();
    expect(monitorData).toHaveProperty('code', 0);
    expect(monitorData).toHaveProperty('data');

    // 4. 获取 SLA 告警
    const breachesResponse = await request.get(`${apiUrl}/api/v1/sla/violations`);
    expect(breachesResponse.status()).toBe(200);
    const breachesData = await breachesResponse.json();
    expect(breachesData).toHaveProperty('code', 0);
    expect(breachesData).toHaveProperty('data');

    // 每个端点都必须满足唯一的成功契约。
  });

  test('SLA 权限验证', async ({ request }) => {
    const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

    // 以普通用户登录
    await establishSession(request, TEST_USERS.end_user, apiUrl);

    // 尝试访问 SLA 管理接口
    const response = await request.get(`${apiUrl}/api/v1/sla`);

    expect(response.status()).toBe(403);
  });
});
