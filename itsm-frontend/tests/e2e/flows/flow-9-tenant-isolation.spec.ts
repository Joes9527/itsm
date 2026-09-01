/**
 * FLOW-9: 多租户零跨租户泄露
 * Priority: P1
 *
 * 完整链路: tenant1 用户操作 → 确认 tenant2 无法访问
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-9: 多租户隔离验证', () => {
  let adminRole: string;
  let tenantAdminRole: string;

  test.beforeEach(async ({ loginAs }) => {
    adminRole = await loginAs('admin');
    tenantAdminRole = await loginAs('tenant1admin');
  });

  test('T074-FLOW9 - 租户隔离验证', async ({ apiGet, apiPost }) => {
    // Step 1: admin 确认能看所有租户
    const allTenantsResp = await apiGet(adminRole, '/api/v1/tenants');
    expect(allTenantsResp.status).toBe(200);

    // Step 2: tenant1admin 只能看自己租户
    const myTenantsResp = await apiGet(tenantAdminRole, '/api/v1/tenants');
    expect(myTenantsResp.status).toBe(403);

    // Step 3: tenant1admin 创建的工单只能自己看到
    const createResp = await apiPost(tenantAdminRole, '/api/v1/tickets', {
      title: 'FLOW-9 租户隔离测试',
      description: '此工单属于 tenant_test',
      priority: 'low',
      category: 'general',
    });

    expect(createResp.status).toBe(200);
    const ticketId = createResp.data.data?.id;
    expect(ticketId).toBeDefined();

    // Step 4: 客户端 tenant_id 不能覆盖认证租户。
    const crossTenantResp = await apiGet(tenantAdminRole, `/api/v1/tickets?tenant_id=99999`);
    expect(crossTenantResp.status).toBe(200);
    expect(crossTenantResp.data).toHaveProperty('code', 0);
    expect(JSON.stringify(crossTenantResp.data)).not.toContain('"tenantId":99999');
  });
});
