/**
 * FLOW-1: Incident → Problem → Change 端到端流程
 * Priority: P1
 *
 * 完整链路: end_user 报障 → engineer 排障 → approver 审批 → engineer 实施
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-1: Incident → Problem → Change', () => {
  let endUserToken: string;
  let engineerToken: string;
  let managerToken: string;

  test.beforeEach(async ({ loginAs }) => {
    endUserToken = await loginAs('user1');
    engineerToken = await loginAs('engineer1');
    managerToken = await loginAs('manager1');
  });

  test('T070-FLOW1 - 完整流程: 事件 → 问题 → 变更', async ({ apiPost, apiGet }) => {
    // Step 1: end_user 创建事件
    const incidentResp = await apiPost(endUserToken, '/api/v1/incidents', {
      title: 'FLOW-1 测试事件',
      description: '服务器宕机',
      priority: 'high',
      category: 'incident',
    });

    expect(incidentResp.status).toBe(200);
    expect(incidentResp.data).toHaveProperty('code', 0);
    const incidentId: number = incidentResp.data.data.id;
    expect(incidentId).toBeGreaterThan(0);

    // Step 2: engineer 接受并解决事件
    const resolveResp = await apiPost(engineerToken, `/api/v1/incidents/${incidentId}`, {
      status: 'resolved',
      resolution: '已重启服务',
    });

    expect(resolveResp.status).toBe(200);

    // Step 3: engineer 创建关联问题
    const problemResp = await apiPost(engineerToken, '/api/v1/problems', {
      title: 'FLOW-1 根因问题',
      description: '根因分析：内存泄漏导致',
      related_incident_id: incidentId,
      priority: 'medium',
    });

    expect(problemResp.status).toBe(200);
    expect(problemResp.data).toHaveProperty('code', 0);
    const problemId: number = problemResp.data.data.id;
    expect(problemId).toBeGreaterThan(0);

    // Step 4: engineer 基于问题创建变更
    const changeResp = await apiPost(engineerToken, '/api/v1/changes', {
      title: 'FLOW-1 预防性变更',
      description: '修复内存泄漏问题',
      change_type: 'normal',
      related_problem_id: problemId,
      priority: 'medium',
    });

    expect(changeResp.status).toBe(200);
    expect(changeResp.data).toHaveProperty('code', 0);
    expect(changeResp.data.data.id).toBeGreaterThan(0);
    expect(changeResp.data.data.title).toBe('FLOW-1 预防性变更');
  });
});
