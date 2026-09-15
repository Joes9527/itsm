/**
 * US5: sd_manager 服务台主管运营视图
 * Priority: P2
 *
 * 用户故事: 作为服务台主管，我能查看团队工作量、工单统计、服务水平、创建工单
 */
import { test, expect } from '../fixtures/auth';

test.describe('US5: sd_manager 服务台主管运营视图', () => {
  let role: string;

  test.beforeEach(async ({ loginAs }) => {
    // 登录为经理
    role = await loginAs('manager1');
  });

  test('T041 - 能访问仪表盘', async ({ apiGet }) => {
    const response = await apiGet(role, '/api/v1/dashboard/stats');
    expect(response.status).toBe(200);
  });

  test('T042 - 能查看团队工单统计', async ({ apiGet }) => {
    const response = await apiGet(role, '/api/v1/tickets/stats');
    expect(response.status).toBe(200);
  });

  test('T043 - 能查看 SLA 监控', async ({ apiPost }) => {
    const response = await apiPost(role, '/api/v1/sla/monitoring', {});
    expect(response.status).toBe(200);
  });

  test('T044 - 能查看 SLA 定义列表', async ({ apiGet }) => {
    // 验证 SLA 定义列表（manager 可能有或无 sla:read 权限）
    const response = await apiGet(role, '/api/v1/sla/definitions');
    expect(response.status).toBe(200);
  });

  test('T045 - 能查看报表中心概览', async ({ apiGet }) => {
    const response = await apiGet(role, '/api/v1/reports');
    expect(response.status).toBe(200);
  });
});
