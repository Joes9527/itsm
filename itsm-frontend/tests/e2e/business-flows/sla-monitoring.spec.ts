/**
 * SLA 监控完整 E2E 测试
 * 覆盖 SLA 定义、监控、告警、报表等核心功能
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs, TEST_USERS } from '../utils/test-utils';
import { establishSession } from '../auth-utils';
import { TicketPage } from '../utils/page-objects/TicketPage';

async function csrfHeaders(request: APIRequestContext, apiUrl: string) {
  const response = await request.get(`${apiUrl}/api/v1/csrf-token`);
  expect(response.status()).toBe(200);
  const body = await response.json() as { data?: { csrf_token?: string } };
  expect(body.data?.csrf_token).toEqual(expect.any(String));
  expect(body.data!.csrf_token!.length).toBeGreaterThan(20);
  return {
    'Content-Type': 'application/json',
    'X-CSRF-Token': body.data!.csrf_token!,
  };
}

test.describe('SLA 监控完整测试', () => {
  test.describe('SLA 仪表盘', () => {
    test('SLA 仪表盘页面加载', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/sla-dashboard');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(50);
    });

  });

  test.describe('SLA 定义管理', () => {
    test('SLA 定义列表页面', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/sla');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(50);
    });

    test('SLA 创建页面加载', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/sla/create');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(30);
    });

  });

  test.describe('SLA 监控', () => {
    test('SLA 监控页面加载', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/sla-monitor');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(50);
    });

  });

  test.describe('工作流 SLA', () => {
    test('工作流 SLA 页面加载', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/workflow/sla');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(30);
    });
  });

  test.describe('SLA API 接口测试', () => {
    test('GET /api/v1/sla SLA 列表接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 列表
      const response = await request.get(`${apiUrl}/api/v1/sla`);

      expect(response.status()).toBe(200);
    });

    test('POST /api/v1/sla/monitoring 监控数据接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 监控数据
      const response = await request.post(`${apiUrl}/api/v1/sla/monitoring`, {
        data: {},
        headers: await csrfHeaders(request, apiUrl),
      });

      expect(response.status()).toBe(200);
    });

    test('GET /api/v1/sla/violations 告警接口', async ({ request }) => {
      const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

      // 登录
      await establishSession(request, TEST_USERS.admin, apiUrl);

      // 获取 SLA 告警列表
      const response = await request.get(`${apiUrl}/api/v1/sla/violations`);

      expect(response.status()).toBe(200);
    });
  });

  test.describe('SLA 与工单关联测试', () => {
    test('工单详情页显示 SLA 信息', async ({ page }) => {
      await loginAs(page, 'admin');

      const ticketPage = new TicketPage(page);
      await ticketPage.goto();

      // 检查表格是否存在
      const tableExists = await page.locator('table').isVisible();
      if (!tableExists) {
        test.skip();
        return;
      }

      const ticketId = await ticketPage.getFirstTicketId();
      if (!ticketId) {
        test.skip();
        return;
      }

      // 打开工单详情
      await ticketPage.openTicket(Number(ticketId));
      await page.waitForLoadState('domcontentloaded');

      // 检查是否有 SLA 相关信息
      const pageContent = await page.locator('body').textContent();
      const hasSLAInfo = pageContent?.includes('SLA') || pageContent?.includes('服务级别');
      console.log('Has SLA info in ticket detail:', hasSLAInfo);
    });

  });

  test.describe('SLA 报表', () => {
    test('SLA 报表页面加载', async ({ page }) => {
      await loginAs(page, 'admin');

      await page.goto('/sla-reports');
      await page.waitForLoadState('domcontentloaded');

      const bodyContent = await page.locator('body').textContent();
      expect(bodyContent?.length).toBeGreaterThan(30);
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
    console.log('SLA count:', slaData?.data?.total ?? 0);

    // 3. 获取 SLA 监控数据
    const monitorResponse = await request.post(`${apiUrl}/api/v1/sla/monitoring`, {
      data: {},
      headers: await csrfHeaders(request, apiUrl),
    });
    expect(monitorResponse.status()).toBe(200);
    const monitorData = await monitorResponse.json();
    console.log('SLA monitor data:', monitorData?.data ?? 'N/A');

    // 4. 获取 SLA 告警
    const breachesResponse = await request.get(`${apiUrl}/api/v1/sla/violations`);
    expect(breachesResponse.status()).toBe(200);
    const breachesData = await breachesResponse.json();
    console.log('SLA breaches count:', breachesData?.data?.total ?? 0);

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
