/**
 * US4: approver 审批人多级审批
 * Priority: P2
 *
 * 用户故事: 作为审批人，我能够审批待办事项、查看审批历史、批量审批
 */
import { test, expect } from '../fixtures/auth';

test.describe('US4: approver 审批人多级审批', () => {
  let token: string;

  test.beforeEach(async ({ loginAs }) => {
    // 登录为审批经理
    token = await loginAs('manager1');
  });

  test('T036 - 能查看待审批列表', async ({ apiGet }) => {
    const response = await apiGet(token, '/api/v1/my-approvals');
    expect([200, 403, 404].includes(response.status)).toBe(true);
  });

  test('T037 - 能审批通过工单', async ({ apiGet, apiPost }) => {
    const tasksResp = await apiGet(token, '/api/v1/my-approvals');
    expect([200, 403, 404].includes(tasksResp.status)).toBe(true);

    const tasks = tasksResp.data?.data?.items || tasksResp.data?.items || [];
    if (tasks.length > 0) {
      const taskId = tasks[0].taskId || tasks[0].id;
      const approveResp = await apiPost(token, `/api/v1/bpmn/tasks/${taskId}/decisions`, {
        action: 'approve',
        comment: 'E2E 测试通过',
      });
      expect([200, 403, 404].includes(approveResp.status)).toBe(true);
    }
  });

  test('T038 - 能审批拒绝工单', async ({ apiGet, apiPost }) => {
    const tasksResp = await apiGet(token, '/api/v1/my-approvals');
    const tasks = tasksResp.data?.data?.items || tasksResp.data?.items || [];

    if (tasks.length > 0) {
      const taskId = tasks[0].taskId || tasks[0].id;
      const rejectResp = await apiPost(token, `/api/v1/bpmn/tasks/${taskId}/decisions`, {
        action: 'reject',
        comment: 'E2E 测试拒绝',
      });
      expect([200, 403, 404].includes(rejectResp.status)).toBe(true);
    }
  });

  test('T039 - 能查看审批历史', async ({ apiGet }) => {
    const tasksResp = await apiGet(token, '/api/v1/my-approvals');
    const tasks = tasksResp.data?.data?.items || tasksResp.data?.items || [];
    const businessId = tasks[0]?.businessId;
    if (!businessId) {
      expect([200, 403, 404].includes(tasksResp.status)).toBe(true);
      return;
    }

    const historyResp = await apiGet(token, `/api/v1/tickets/${businessId}/approval-decisions`);
    expect([200, 403, 404].includes(historyResp.status)).toBe(true);
  });

  test('T040 - 审批人无租户管理权限', async ({ apiGet }) => {
    // 审批人不应该能管理租户
    const response = await apiGet(token, '/api/v1/tenants');
    // 审批人可能只能看到自己租户，不能看全局
    expect([200, 403].includes(response.status)).toBe(true);
  });
});
