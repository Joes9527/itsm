/**
 * FLOW-2/5/6/8: 剩余流程合并测试
 * - FLOW-2: 标准变更
 * - FLOW-5: 紧急变更
 * - FLOW-6: AI 工单建议接受
 * - FLOW-8: CMDB 影响分析
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-2: 标准变更流程', () => {
  let engineerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    engineerRole = await loginAs('engineer1');
  });

  test('T076-FLOW2 - 标准变更', async ({ apiPost }) => {
    // 创建标准变更
    const changeResp = await apiPost(engineerRole, '/api/v1/changes', {
      title: 'FLOW-2 标准变更测试',
      description: '例行系统维护',
      change_type: 'standard',
      priority: 'low',
    });

    expect(changeResp.status).toBe(200);
    expect(changeResp.data).toHaveProperty('code', 0);
    expect(changeResp.data.data?.id).toBeDefined();
  });
});

test.describe('FLOW-5: 紧急变更流程', () => {
  let engineerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    engineerRole = await loginAs('engineer1');
  });

  test('T076-FLOW5 - 紧急变更', async ({ apiPost }) => {
    // 创建紧急变更
    const changeResp = await apiPost(engineerRole, '/api/v1/changes', {
      title: 'FLOW-5 紧急变更测试',
      description: '紧急修复安全漏洞',
      change_type: 'emergency',
      priority: 'critical',
    });

    expect(changeResp.status).toBe(200);
    expect(changeResp.data).toHaveProperty('code', 0);
    expect(changeResp.data.data?.id).toBeDefined();
  });
});

test.describe('FLOW-6: AI 工单建议接受', () => {
  let engineerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    engineerRole = await loginAs('engineer1');
  });

  test('T076-FLOW6 - AI 工单建议', async ({ apiPost }) => {
    // 创建工单
    const ticketResp = await apiPost(engineerRole, '/api/v1/tickets', {
      title: 'FLOW-6 AI 测试工单',
      description: '系统运行缓慢',
      priority: 'medium',
      category: 'technical',
    });

    const ticketId = ticketResp.data.data?.id;
    expect(ticketId).toBeDefined();

    // 记录 AI 审计
    if (ticketId) {
      const auditResp = await apiPost(engineerRole, '/api/v1/ai/audit', {
        scenario: 'ticket_triage',
        inputRef: `ticket:${ticketId}`,
        promptVersion: 'e2e-v1',
        model: 'e2e-fixture',
        confidence: 0.9,
        suggestion: { summary: '建议检查数据库连接池' },
        accepted: true,
      });

      expect(auditResp.status).toBe(200);
      expect(auditResp.data).toHaveProperty('code', 0);
    }
  });
});

test.describe('FLOW-8: CMDB 影响分析', () => {
  let engineerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    engineerRole = await loginAs('engineer1');
  });

  test('T076-FLOW8 - CMDB 影响分析', async ({ apiGet }) => {
    // 查看 CI 类型
    const typesResp = await apiGet(engineerRole, '/api/v1/cmdb/ci-types');
    expect(typesResp.status).toBe(200);
    expect(typesResp.data).toHaveProperty('code', 0);

    // 查看配置项列表
    const ciListResp = await apiGet(engineerRole, '/api/v1/cmdb/cis');
    expect(ciListResp.status).toBe(200);
  });
});
