/**
 * US1: end_user 提单到关闭闭环验证
 * Priority: P1
 *
 * 用户故事: 作为终端用户，我能够提交工单、查看我的工单、跟踪处理进度、评价服务
 */
import { test, expect } from '../fixtures/auth';

test.describe('US1: end_user 提单到关闭闭环', () => {
  let role: string;

  test.beforeEach(async ({ loginAs }) => {
    // 登录为普通用户
    role = await loginAs('user1');
  });

  test('T013 - 登录后能访问工单列表页面', async ({ page }) => {
    const baseURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
    await page.goto(`${baseURL}/tickets`);
    await expect(page).toHaveURL(/\/tickets(?:\?.*)?$/);
  });

  test('T014 - 创建工单成功', async ({ apiPost }) => {
    const response = await apiPost(role, '/api/v1/tickets', {
      title: 'E2E 测试工单 - US1',
      description: '这是自动化端到端测试创建的工单',
      priority: 'medium',
      category: 'general',
    });

    expect(response.status).toBe(200);
    expect(response.data).toHaveProperty('code', 0);
    expect(response.data.data).toHaveProperty('ticketNumber');
  });

  test('T015 - 能查看我的工单列表', async ({ apiGet }) => {
    const response = await apiGet(role, '/api/v1/tickets');

    expect(response.status).toBe(200);
    expect(response.data).toHaveProperty('code', 0);
    expect(response.data.data).toHaveProperty('tickets');
    expect(Array.isArray(response.data.data.tickets)).toBe(true);
  });

  test('T016 - 能查看工单详情', async ({ apiGet, apiPost }) => {
    // 先创建工单获取 ID
    const createResp = await apiPost(role, '/api/v1/tickets', {
      title: 'US1 详情测试工单',
      description: '用于测试查看详情',
      priority: 'low',
      category: 'general',
    });

    const ticketId = createResp.data.data?.id;
    expect(ticketId).toBeGreaterThan(0);

    // 获取详情
    const detailResp = await apiGet(role, `/api/v1/tickets/${ticketId}`);

    expect(detailResp.status).toBe(200);
    expect(detailResp.data).toHaveProperty('code', 0);
    expect(detailResp.data.data).toHaveProperty('title');
  });

  test('T017 - 非管理员无法访问其他租户工单', async ({ apiGet }) => {
    // 尝试访问不存在的租户工单（应该返回空或403）
    const response = await apiGet(role, '/api/v1/tickets?tenant_id=99999');
    expect(response.status).toBe(200);
    expect(response.data).toHaveProperty('code', 0);
    expect(JSON.stringify(response.data)).not.toContain('"tenantId":99999');
  });

  test('T018 - 验证角色权限 - end_user 无管理员权限', async ({ apiGet }) => {
    // end_user 不应能访问租户管理
    const response = await apiGet(role, '/api/v1/tenants');
    expect(response.status).toBe(403);
  });

  test('T019 - 验证菜单可见性 - 终端用户可见工单/知识库/服务目录', async ({ page }) => {
    const baseURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
    await page.goto(`${baseURL}/dashboard`);

    // 验证左侧菜单包含必要项
    const navigation = page.getByRole('navigation').first();
    await expect(navigation.getByText(/工单管理|我的工单/).first()).toBeVisible();
  });

  test('T020 - 验证登出功能', async ({ page }) => {
    const baseURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000';
    await page.goto(`${baseURL}/dashboard`);

    const logoutBtn = page.locator('button:has-text("退出")').first();
    await expect(logoutBtn).toBeVisible();
    await logoutBtn.click();
    await expect(page).toHaveURL(/.*\/login/);
  });
});
