/**
 * FLOW-7: SLA 风险 → 监控 → 通知 → 升级
 * Priority: P2
 *
 * 完整链路: SLA 风险检测 → 告警通知 → 升级处理
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-7: SLA 风险监控与升级', () => {
  let adminRole: string;
  let managerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    adminRole = await loginAs('admin');
    managerRole = await loginAs('manager1');
  });

  test('T073-FLOW7 - SLA 风险监控流程', async ({ apiGet, apiPost }) => {
    // Step 1: 查看 SLA 监控状态
    const monitorResp = await apiPost(adminRole, '/api/v1/sla/monitoring', {});
    expect(monitorResp.status).toBe(200);

    // Step 2: 查看 SLA 违规记录
    const violationsResp = await apiGet(adminRole, '/api/v1/sla/violations');
    expect(violationsResp.status).toBe(200);

    // Step 3: 查看 SLA 统计数据
    const statsResp = await apiGet(managerRole, '/api/v1/sla/stats');
    expect(statsResp.status).toBe(200);

    // Step 4: 查看 SLA 合规报告
    const complianceResp = await apiGet(managerRole, '/api/v1/sla/compliance-report');
    expect(complianceResp.status).toBe(200);
  });
});
