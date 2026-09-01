/**
 * FLOW-3: 重大事件多角色协作
 * Priority: P1
 *
 * 完整链路: 重大事件 → 升级 → 多团队协作 → 解决
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-3: 重大事件多角色协作', () => {
  let endUserRole: string;
  let engineerRole: string;
  let managerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    endUserRole = await loginAs('user1');
    engineerRole = await loginAs('engineer1');
    managerRole = await loginAs('manager1');
  });

  test('T071-FLOW3 - 重大事件处理流程', async ({ apiPost, apiGet }) => {
    // Step 1: 创建重大事件
    const incidentResp = await apiPost(endUserRole, '/api/v1/incidents', {
      title: 'FLOW-3 重大事件 - 核心系统故障',
      description: '核心交易系统宕机，影响所有用户',
      priority: 'critical',
      category: 'major_incident',
    });

    expect(incidentResp.status).toBe(200);
    const incidentId = incidentResp.data.data?.id;
    expect(incidentId).toBeDefined();

    // Step 2: manager 确认为重大事件
    if (incidentId) {
      const escalateResp = await apiPost(managerRole, `/api/v1/incidents/${incidentId}/major-incident`, {
        impactScope: 'critical',
        businessImpact: '核心交易系统不可用',
        communicationPlan: '每15分钟同步一次处置进展',
      });

      expect(escalateResp.status).toBe(200);
      expect(escalateResp.data).toHaveProperty('code', 0);
    }

    // Step 3: engineer 处理事件
    if (incidentId) {
      const handleResp = await apiPost(engineerRole, `/api/v1/incidents/${incidentId}/acknowledge`, {
        comment: '正在排查问题',
      });

      expect(handleResp.status).toBe(200);

      // Step 4: 事件解决
      const resolveResp = await apiPost(engineerRole, `/api/v1/incidents/${incidentId}/resolve`, {
        resolution: '已修复核心问题，系统恢复',
        resolutionCode: 'permanent_fix',
      });

      expect(resolveResp.status).toBe(200);
    }
  });
});
