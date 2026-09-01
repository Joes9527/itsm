/**
 * US4: approver 审批人多级审批
 * Priority: P2
 *
 * 用户故事: 作为审批人，我能够审批待办事项、查看审批历史、批量审批
 */
import { test, expect } from '../fixtures/auth';

test.describe('US4: approver 审批人多级审批', () => {
  let role: string;

  test.beforeEach(async ({ loginAs }) => {
    // 登录为审批经理
    role = await loginAs('manager1');
  });

  test('T036 - 能查看待审批列表', async ({ apiGet }) => {
    const response = await apiGet(role, '/api/v1/bpmn/tasks');
    expect(response.status).toBe(200);
    expect(Array.isArray(response.data?.data?.data)).toBe(true);
  });

  test('T037 - 能审批通过工单', async ({ apiGet, apiPost }) => {
    const tasksResp = await apiGet(role, '/api/v1/bpmn/tasks');
    expect(tasksResp.status).toBe(200);

    const tasks = tasksResp.data?.data?.data || [];
    expect(tasks.length).toBeGreaterThan(0);
    const taskId = tasks[0].id;
    const approveResp = await apiPost(role, `/api/v1/bpmn/tasks/${taskId}/decisions`, {
      action: 'approve',
      comment: 'E2E 测试通过',
    });
    expect(approveResp.status).toBe(200);
  });

  test('T038 - 能审批拒绝工单', async ({ apiGet, apiPost }) => {
    const tasksResp = await apiGet(role, '/api/v1/bpmn/tasks');
    expect(tasksResp.status).toBe(200);
    const tasks = tasksResp.data?.data?.data || [];
    expect(tasks.length).toBeGreaterThan(0);
    const taskId = tasks[0].id;
    const rejectResp = await apiPost(role, `/api/v1/bpmn/tasks/${taskId}/decisions`, {
      action: 'reject',
      comment: 'E2E 测试拒绝',
    });
    expect(rejectResp.status).toBe(200);
  });

  test('T039 - 能查看审批历史', async ({ apiGet }) => {
    const tasksResp = await apiGet(role, '/api/v1/bpmn/tasks');
    expect(tasksResp.status).toBe(200);
    const tasks = tasksResp.data?.data?.data || [];
    expect(tasks.length).toBeGreaterThan(0);
    const processInstanceId = tasks[0]?.processInstanceId;
    expect(processInstanceId).toBeTruthy();
    const historyResp = await apiGet(role, `/api/v1/bpmn/process-instances/${processInstanceId}/approval-history`);
    expect(historyResp.status).toBe(200);
  });

  test('T040 - 审批人无租户管理权限', async ({ apiGet }) => {
    // 审批人不应该能管理租户
    const response = await apiGet(role, '/api/v1/tenants');
    expect(response.status).toBe(403);
  });
});
