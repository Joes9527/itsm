/**
 * FLOW-4: 服务请求 Catalog → Submit → canonical BPMN ProcessTask
 * Priority: P1
 *
 * 本用例只验证当前产品已实现的权威边界；交付动作没有独立 canonical
 * ProcessTask command 前，不伪造 ticket status 写入来假装完成履约。
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-4: 服务请求完整流程', () => {
  let endUserRole: string;
  let managerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    endUserRole = await loginAs('user1');
    managerRole = await loginAs('manager1');
  });

  test('T072-FLOW4 - 服务请求全流程', async ({ apiGet, apiPost }) => {
    // Step 1: 用户浏览服务目录
    const catalogResp = await apiGet(endUserRole, '/api/v1/service-catalogs');
    expect(catalogResp.status).toBe(200);
    expect(catalogResp.data).toHaveProperty('code', 0);
    const catalogs = catalogResp.data.data?.items ?? catalogResp.data.data?.data ?? [];
    expect(catalogs.length).toBeGreaterThan(0);
    const catalogId = catalogs[0].id;

    // Step 2: 通过唯一 Service Request intake 提交，而不是创建 generic ticket。
    const requestResp = await apiPost(endUserRole, '/api/v1/service-requests', {
      catalogId,
      title: 'FLOW-4 服务请求 - 申请新服务器',
      reason: '需要申请一台新的应用服务器',
      complianceAck: true,
      formData: {},
    });

    expect(requestResp.status).toBe(200);
    expect(requestResp.data).toHaveProperty('code', 0);
    const requestId = requestResp.data.data?.id;
    const workItemId = requestResp.data.data?.ticketId;
    expect(requestId).toBeDefined();
    expect(workItemId).toBeDefined();

    // Step 3: manager 只通过 canonical ProcessTask decision command 审批。
    const tasksResp = await apiGet(
      managerRole,
      `/api/v1/bpmn/tasks?businessType=service_request&businessId=${workItemId}&page=1&pageSize=20`
    );
    expect(tasksResp.status).toBe(200);
    const tasks = tasksResp.data.data?.data ?? [];
    expect(tasks.length).toBeGreaterThan(0);
    const approvalTask = tasks.find((task: { taskPurpose?: string }) => task.taskPurpose === 'approval') ?? tasks[0];
    const approvalResp = await apiPost(managerRole, `/api/v1/bpmn/tasks/${approvalTask.id}/decisions`, {
      action: 'approve',
      comment: 'FLOW-4 canonical BPMN approval',
    });
    expect(approvalResp.status).toBe(200);
    expect(approvalResp.data).toHaveProperty('code', 0);

    const persisted = await apiGet(endUserRole, `/api/v1/service-requests/${requestId}`);
    expect(persisted.status).toBe(200);
    expect(persisted.data.data?.ticketId).toBe(workItemId);
  });
});
