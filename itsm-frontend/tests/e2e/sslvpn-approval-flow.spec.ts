/**
 * SSL-VPN 服务申请与 3 角色双级审批 E2E 走查测试 (Playwright Multi-Persona Walkthrough)
 *
 * 场景链路：
 * 1. Persona 1 (end_user_test / Password123!):
 *    - 登录系统，访问服务目录 (/service-catalog)；
 *    - 定位 "SSL-VPN 远程办公访问权限申请"，进入申请页 (/service-catalog/request/:id)；
 *    - 验证 3 个动态自定义字段的 UI 渲染并填充提交；
 *    - 提交后跳转工单详情页 (/tickets/:ticketId)，验证状态与审批链。
 * 2. Persona 2 (supervisor_test / Password123!):
 *    - 登录系统，访问唯一 BPMN 审批中心 (/approvals)；
 *    - 查找到当前待初审的 BPMN 任务 (UserTask_DeptManagerApproval)；
 *    - 填写初审意见 "同意申请，出差值班需要" 并完成批准。
 * 3. Persona 3 (lixin_test / Password123!):
 *    - 登录系统，访问审批中心；
 *    - 查找到流转过来的第二级技术复审任务 (UserTask_L2NetworkOpsApproval)；
 *    - 填写复审意见 "网络权限核准通过" 并完成批准；
 *    - 验证流程推进完结，工单状态流转至待分配/处理池。
 */

import { test, expect } from '@playwright/test';
import { establishSession, loginAndReturn } from './auth-utils';

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';
const TENANT_CODE = process.env.PLAYWRIGHT_TENANT_CODE || 'default';

// 测试账号配置
const USERS = {
  endUser: {
    tenantCode: TENANT_CODE,
    username: 'end_user_test',
    password: 'Password123!',
    role: 'end_user',
    name: '侯艾华',
  },
  supervisor: {
    tenantCode: TENANT_CODE,
    username: 'supervisor_test',
    password: 'Password123!',
    role: 'dept_manager',
    name: '主管初审测试账号',
  },
  lixin: {
    tenantCode: TENANT_CODE,
    username: 'lixin_test',
    password: 'Password123!',
    role: 'network_eng',
    name: '李昕/L2网络运维',
  },
};

// 3 项动态自定义字段测试数据
const SSLVPN_CUSTOM_FIELDS = {
  target_systems: '10.128.35.0/24, ERP与WMS生产系统',
  access_duration: 'days_90',
  access_reason: '因研发排障及出差值班，需远程接入内网生产环境',
};

test.describe('SSL-VPN 服务申请与多级审批端到端场景验证 (3-Persona Flow)', () => {
  test.describe.configure({ mode: 'serial' });

  let catalogId: number;
  let createdTicketId: number;

  test.beforeAll(async ({ request }) => {
    // 确保通过 API 能够获取到 SSL-VPN 远程办公访问权限申请 的 catalog ID
    await establishSession(request, USERS.endUser, API_BASE);
    const catResp = await request.get(`${API_BASE}/api/v1/service-catalogs?page=1&size=100`);
    expect(catResp.status()).toBe(200);
    const catData = await catResp.json();
    expect(catData.code).toBe(0);
    const items = catData.data?.items ?? catData.items ?? [];
    const sslvpnItem = items.find(
      (item: any) =>
        item.name?.includes('SSL-VPN') || item.processDefinitionKey === 'sslvpn_approval_flow'
    );
    expect(sslvpnItem?.id, 'SSL-VPN catalog item must be seeded').toBeGreaterThan(0);
    catalogId = sslvpnItem!.id;
  });

  // =========================================================================
  // 1. Persona 1: 申请人 (end_user_test) 提交 SSL-VPN 服务申请
  // =========================================================================
  test('Step 1: 申请人 (end_user_test) 浏览服务目录并填写 3 个自定义字段提交申请', async ({
    page,
  }) => {
    // 1.1 登录申请人账号
    await loginAndReturn(page, USERS.endUser, '/service-catalog');
    await page.waitForLoadState('domcontentloaded');

    // 1.2 浏览服务目录并定位到 SSL-VPN 目录项
    await expect(page.getByRole('heading', { name: '服务目录' })).toBeVisible();

    // 如果未从 API 获取到 catalogId，尝试从页面卡片获取
    const sslvpnCard = page
      .locator('.ant-card')
      .filter({
        hasText: 'SSL-VPN 远程办公访问权限申请',
      })
      .first();

    await expect(sslvpnCard).toBeVisible();
    const applyBtn = sslvpnCard.getByRole('button', { name: '申请服务', exact: true });
    await expect(applyBtn).toBeVisible();
    await applyBtn.click();

    await page.waitForURL(/\/service-catalog\/request\/\d+/, { timeout: 15000 });
    const currentUrl = page.url();
    const urlMatch = currentUrl.match(/\/service-catalog\/request\/(\d+)/);
    expect(urlMatch).not.toBeNull();
    catalogId = parseInt(urlMatch![1], 10);

    // 1.3 验证申请页与 3 个自定义输入控件正确渲染
    // 基础表单项
    const titleInput = page.locator('#title');
    await titleInput.fill('申请研发出差 SSL-VPN 访问权限');

    const reasonInput = page.locator('#reason');
    await reasonInput.fill('因出差需要远程访问研发内网与生产堡垒机');

    // 身份和授权目标由认证映射与目录策略提供。
    // 业务字段: 访问目标系统与网段 (target_systems)
    const targetInput = page.locator('#customFields_target_systems');
    await expect(targetInput).toBeVisible();
    {
      await targetInput.fill(SSLVPN_CUSTOM_FIELDS.target_systems);
    }

    // 字段 7: 权限有效期 (access_duration - select)
    const durationSelect = page.locator('[id*="customFields_access_duration"]').first();
    await expect(durationSelect).toBeVisible();
    {
      await durationSelect.click();
      await page
        .locator('.ant-select-item-option-content')
        .filter({ hasText: '90天临时' })
        .first()
        .click();
    }

    // 业务字段: 业务申请理由 (access_reason - textarea)
    const accessReasonInput = page.locator('#customFields_access_reason');
    await expect(accessReasonInput).toBeVisible();
    {
      await accessReasonInput.fill(SSLVPN_CUSTOM_FIELDS.access_reason);
    }

    // 1.4 点击提交申请
    const submitBtn = page.getByRole('button', { name: '提交申请', exact: true });
    await submitBtn.click();

    // 1.5 验证提交成功并跳转到工单详情页
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 20000 });
    const ticketUrl = page.url();
    const ticketMatch = ticketUrl.match(/\/tickets\/(\d+)/);
    expect(ticketMatch).not.toBeNull();
    createdTicketId = parseInt(ticketMatch![1], 10);
    expect(createdTicketId).toBeGreaterThan(0);

    // 1.6 验证工单详情页展示
    await expect(
      page.getByRole('main').getByText('申请研发出差 SSL-VPN 访问权限', { exact: true })
    ).toBeVisible();
  });

  // =========================================================================
  // 2. Persona 2: 上级领导 (supervisor_test) 审批第一级任务
  // =========================================================================
  test('Step 2: 上级领导 (supervisor_test) 登录待办审批中心完成一级初审', async ({ page }) => {
    // 2.1 登录主管账号
    await loginAndReturn(page, USERS.supervisor, '/approvals');
    await page.waitForLoadState('domcontentloaded');

    // 2.2 验证在待办中心能看到对应的待办任务
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    // 查找包含 "上级领导初审" 或该工单标题的待办行
    const taskRow = page
      .getByRole('row')
      .filter({
        hasText: /上级领导初审|UserTask_DeptManagerApproval|SSL-VPN/,
      })
      .first();

    await expect(taskRow).toBeVisible({ timeout: 10000 });

    // 2.3 如果有"领取"按钮，先点击领取，或直接点击"批准"
    const claimBtn = taskRow.getByRole('button', { name: '领取', exact: true });
    await expect(claimBtn).toBeVisible();
    {
      await claimBtn.click();
    }

    const approveBtn = taskRow.getByRole('button', { name: '批准', exact: true });
    await expect(approveBtn).toBeVisible();
    await approveBtn.click();

    // 2.4 在弹出的审批模态框中输入审批意见并确认批准
    const modal = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal).toBeVisible({ timeout: 5000 });

    const commentInput = modal.getByPlaceholder('可填写审批意见');
    await expect(commentInput).toBeVisible();
    {
      await commentInput.fill('同意申请，出差值班需要');
    }

    const confirmApproveBtn = modal.getByRole('button', { name: '确认批准', exact: true });
    await confirmApproveBtn.click();

    // 2.5 验证初审已完成
    await expect(modal).not.toBeVisible({ timeout: 10000 });
  });

  // =========================================================================
  // 3. Persona 3: 李昕 / L2 网络运维 (lixin_test) 技术复审
  // =========================================================================
  test('Step 3: 李昕 / L2网络运维 (lixin_test) 登录待办审批中心完成二级复审与移交', async ({
    page,
  }) => {
    // 3.1 登录李昕账号
    await loginAndReturn(page, USERS.lixin, '/approvals');
    await page.waitForLoadState('domcontentloaded');

    // 3.2 验证在待办中心看到流转过来的第二级技术复审任务
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    const l2TaskRow = page
      .getByRole('row')
      .filter({
        hasText: /李昕|L2网络运维|UserTask_L2NetworkOpsApproval|SSL-VPN/,
      })
      .first();

    await expect(l2TaskRow).toBeVisible({ timeout: 10000 });

    // 3.3 领取并批准
    const claimBtn = l2TaskRow.getByRole('button', { name: '领取', exact: true });
    await expect(claimBtn).toBeVisible();
    {
      await claimBtn.click();
    }

    const approveBtn = l2TaskRow.getByRole('button', { name: '批准', exact: true });
    await expect(approveBtn).toBeVisible();
    await approveBtn.click();

    // 3.4 填写复审意见并提交
    const modal = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal).toBeVisible({ timeout: 5000 });

    const commentInput = modal.getByPlaceholder('可填写审批意见');
    await expect(commentInput).toBeVisible();
    {
      await commentInput.fill('网络权限核准通过');
    }

    const confirmApproveBtn = modal.getByRole('button', { name: '确认批准', exact: true });
    await confirmApproveBtn.click();

    await expect(modal).not.toBeVisible({ timeout: 10000 });

    // 3.5 访问该工单详情页，验证全流程审批通过
    await page.goto(`/tickets/${createdTicketId}`);
    await page.waitForLoadState('domcontentloaded');
    await expect(
      page.getByRole('main').getByText('申请研发出差 SSL-VPN 访问权限', { exact: true })
    ).toBeVisible();
  });
});
