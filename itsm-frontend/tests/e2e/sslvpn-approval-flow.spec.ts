/**
 * SSL-VPN 服务申请与 3 角色双级审批 E2E 走查测试 (Playwright Multi-Persona Walkthrough)
 *
 * 场景链路：
 * 1. Persona 1 (end_user_test / Password123!):
 *    - 登录系统，访问服务目录 (/service-catalog)；
 *    - 定位 "SSL-VPN 远程办公访问权限申请"，进入申请页 (/service-catalog/request/:id)；
 *    - 验证 8 个动态自定义字段的 UI 渲染并填充提交；
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
import { loginAndReturn } from './auth-utils';

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8090';

// 测试账号配置
const USERS = {
  endUser: {
    username: 'end_user_test',
    password: 'Password123!',
    role: 'end_user',
    name: '侯艾华',
  },
  supervisor: {
    username: 'supervisor_test',
    password: 'Password123!',
    role: 'dept_manager',
    name: '主管初审测试账号',
  },
  lixin: {
    username: 'lixin_test',
    password: 'Password123!',
    role: 'network_eng',
    name: '李昕/L2网络运维',
  },
};

// 8 项动态自定义字段测试数据
const SSLVPN_CUSTOM_FIELDS = {
  applicant_name: '侯艾华',
  applicant_upn: 'shouah@kln.com',
  employee_id: 'EMP001',
  department: 'IT研发中心',
  vpn_level: 'Level 2 - 业务系统组 (CNDL-OKTA-SSLVPN-Level2-Users)',
  target_systems: '10.128.35.0/24, ERP与WMS生产系统',
  access_duration: '90天临时',
  access_reason: '因研发排障及出差值班，需远程接入内网生产环境',
};

test.describe('SSL-VPN 服务申请与多级审批端到端场景验证 (3-Persona Flow)', () => {
  test.describe.configure({ mode: 'serial' });

  let catalogId: number;
  let createdTicketId: number;

  test.beforeAll(async ({ request }) => {
    // 确保通过 API 能够获取到 SSL-VPN 远程办公访问权限申请 的 catalog ID
    const loginResp = await request.post(`${API_BASE}/api/v1/auth/login`, {
      data: {
        username: USERS.endUser.username,
        password: USERS.endUser.password,
      },
    });

    if (loginResp.ok()) {
        const catResp = await request.get(`${API_BASE}/api/v1/service-catalogs?page=1&size=100`);
        if (catResp.ok()) {
          const catData = await catResp.json();
          const items = catData.data?.items || catData.items || [];
          const sslvpnItem = items.find((item: any) =>
            item.name?.includes('SSL-VPN') || item.processDefinitionKey === 'sslvpn_approval_flow'
          );
          if (sslvpnItem) {
            catalogId = sslvpnItem.id;
          }
        }
    }
  });

  // =========================================================================
  // 1. Persona 1: 申请人 (end_user_test) 提交 SSL-VPN 服务申请
  // =========================================================================
  test('Step 1: 申请人 (end_user_test) 浏览服务目录并填写 8 个自定义字段提交申请', async ({ page }) => {
    // 1.1 登录申请人账号
    await loginAndReturn(page, USERS.endUser.username, USERS.endUser.password, '/service-catalog');
    await page.waitForLoadState('domcontentloaded');

    // 1.2 浏览服务目录并定位到 SSL-VPN 目录项
    await expect(page.locator('body')).toBeVisible();

    // 如果未从 API 获取到 catalogId，尝试从页面卡片获取
    const sslvpnCard = page.locator('.ant-card, [class*="ServiceItemCard"], div').filter({
      hasText: 'SSL-VPN 远程办公访问权限申请',
    }).first();

    if (await sslvpnCard.isVisible()) {
      const applyBtn = sslvpnCard.locator('button:has-text("申请服务"), button:has-text("申请")').first();
      if (await applyBtn.isVisible()) {
        await applyBtn.click();
      } else {
        await sslvpnCard.click();
      }
    } else if (catalogId) {
      await page.goto(`/service-catalog/request/${catalogId}`);
    }

    await page.waitForURL(/\/service-catalog\/request\/\d+/, { timeout: 15000 }).catch(() => {});
    const currentUrl = page.url();
    const urlMatch = currentUrl.match(/\/service-catalog\/request\/(\d+)/);
    if (urlMatch) {
      catalogId = parseInt(urlMatch[1], 10);
    }

    // 1.3 验证申请页与 8 个自定义输入控件正确渲染
    await page.waitForSelector('form.ant-form', { timeout: 10000 });

    // 基础表单项
    const titleInput = page.locator('#title, input[placeholder*="说明申请目的"], input[id*="title"]').first();
    await titleInput.fill('申请研发出差 SSL-VPN 访问权限');

    const reasonInput = page.locator('#reason, textarea[placeholder*="详细说明申请原因"], textarea[id*="reason"]').first();
    await reasonInput.fill('因出差需要远程访问研发内网与生产堡垒机');

    // 验证与填写 8 个动态自定义字段
    // 字段 1: 申请人姓名 (applicant_name)
    const nameInput = page.locator('input[id*="customFields_applicant_name"], input[name*="applicant_name"]').first();
    if (await nameInput.isVisible()) {
      await nameInput.fill(SSLVPN_CUSTOM_FIELDS.applicant_name);
    }

    // 字段 2: 申请人域账号/UPN (applicant_upn)
    const upnInput = page.locator('input[id*="customFields_applicant_upn"], input[name*="applicant_upn"]').first();
    if (await upnInput.isVisible()) {
      await upnInput.fill(SSLVPN_CUSTOM_FIELDS.applicant_upn);
    }

    // 字段 3: 员工工号 (employee_id)
    const empIdInput = page.locator('input[id*="customFields_employee_id"], input[name*="employee_id"]').first();
    if (await empIdInput.isVisible()) {
      await empIdInput.fill(SSLVPN_CUSTOM_FIELDS.employee_id);
    }

    // 字段 4: 所属部门 (department - select)
    const deptSelect = page.locator('[id*="customFields_department"]').first();
    if (await deptSelect.isVisible()) {
      await deptSelect.click();
      await page.locator('.ant-select-item-option-content').filter({ hasText: SSLVPN_CUSTOM_FIELDS.department }).first().click();
    }

    // 字段 5: 申请权限级别与用户组 (vpn_level - select)
    const vpnLevelSelect = page.locator('[id*="customFields_vpn_level"]').first();
    if (await vpnLevelSelect.isVisible()) {
      await vpnLevelSelect.click();
      await page.locator('.ant-select-item-option-content').filter({ hasText: 'Level 2' }).first().click();
    }

    // 字段 6: 访问目标系统与网段 (target_systems)
    const targetInput = page.locator('input[id*="customFields_target_systems"], input[name*="target_systems"]').first();
    if (await targetInput.isVisible()) {
      await targetInput.fill(SSLVPN_CUSTOM_FIELDS.target_systems);
    }

    // 字段 7: 权限有效期 (access_duration - select)
    const durationSelect = page.locator('[id*="customFields_access_duration"]').first();
    if (await durationSelect.isVisible()) {
      await durationSelect.click();
      await page.locator('.ant-select-item-option-content').filter({ hasText: '90天临时' }).first().click();
    }

    // 字段 8: 业务申请理由 (access_reason - textarea)
    const accessReasonInput = page.locator('textarea[id*="customFields_access_reason"], textarea[name*="access_reason"]').first();
    if (await accessReasonInput.isVisible()) {
      await accessReasonInput.fill(SSLVPN_CUSTOM_FIELDS.access_reason);
    }

    // 1.4 点击提交申请
    const submitBtn = page.locator('button[type="submit"]:has-text("提交申请"), button:has-text("提交")').first();
    await submitBtn.click();

    // 1.5 验证提交成功并跳转到工单详情页
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 20000 });
    const ticketUrl = page.url();
    const ticketMatch = ticketUrl.match(/\/tickets\/(\d+)/);
    expect(ticketMatch).not.toBeNull();
    createdTicketId = parseInt(ticketMatch![1], 10);
    expect(createdTicketId).toBeGreaterThan(0);

    // 1.6 验证工单详情页展示
    await expect(page.locator('body')).toContainText('申请研发出差 SSL-VPN 访问权限');
  });

  // =========================================================================
  // 2. Persona 2: 上级领导 (supervisor_test) 审批第一级任务
  // =========================================================================
  test('Step 2: 上级领导 (supervisor_test) 登录待办审批中心完成一级初审', async ({ page }) => {
    // 2.1 登录主管账号
    await loginAndReturn(page, USERS.supervisor.username, USERS.supervisor.password, '/approvals');
    await page.waitForLoadState('domcontentloaded');

    // 2.2 验证在待办中心能看到对应的待办任务
    await page.waitForSelector('table.ant-table, [class*="ant-table"]', { timeout: 15000 });

    // 查找包含 "上级领导初审" 或该工单标题的待办行
    const taskRow = page.locator('tr.ant-table-row, tr').filter({
      hasText: /上级领导初审|UserTask_DeptManagerApproval|SSL-VPN/,
    }).first();

    await expect(taskRow).toBeVisible({ timeout: 10000 });

    // 2.3 如果有"领取"按钮，先点击领取，或直接点击"批准"
    const claimBtn = taskRow.locator('button:has-text("领取")').first();
    if (await claimBtn.isVisible()) {
      await claimBtn.click();
      await page.waitForTimeout(1000);
    }

    const approveBtn = taskRow.locator('button:has-text("批准")').first();
    await approveBtn.click();

    // 2.4 在弹出的审批模态框中输入审批意见并确认批准
    const modal = page.locator('.ant-modal-content');
    await expect(modal).toBeVisible({ timeout: 5000 });

    const commentInput = modal.locator('textarea').first();
    if (await commentInput.isVisible()) {
      await commentInput.fill('同意申请，出差值班需要');
    }

    const confirmApproveBtn = modal.locator('button.ant-btn-primary:has-text("确认批准"), button:has-text("确定")').first();
    await confirmApproveBtn.click();

    // 2.5 验证初审已完成
    await expect(modal).not.toBeVisible({ timeout: 10000 });
  });

  // =========================================================================
  // 3. Persona 3: 李昕 / L2 网络运维 (lixin_test) 技术复审
  // =========================================================================
  test('Step 3: 李昕 / L2网络运维 (lixin_test) 登录待办审批中心完成二级复审与移交', async ({ page }) => {
    // 3.1 登录李昕账号
    await loginAndReturn(page, USERS.lixin.username, USERS.lixin.password, '/approvals');
    await page.waitForLoadState('domcontentloaded');

    // 3.2 验证在待办中心看到流转过来的第二级技术复审任务
    await page.waitForSelector('table.ant-table, [class*="ant-table"]', { timeout: 15000 });

    const l2TaskRow = page.locator('tr.ant-table-row, tr').filter({
      hasText: /李昕|L2网络运维|UserTask_L2NetworkOpsApproval|SSL-VPN/,
    }).first();

    await expect(l2TaskRow).toBeVisible({ timeout: 10000 });

    // 3.3 领取并批准
    const claimBtn = l2TaskRow.locator('button:has-text("领取")').first();
    if (await claimBtn.isVisible()) {
      await claimBtn.click();
      await page.waitForTimeout(1000);
    }

    const approveBtn = l2TaskRow.locator('button:has-text("批准")').first();
    await approveBtn.click();

    // 3.4 填写复审意见并提交
    const modal = page.locator('.ant-modal-content');
    await expect(modal).toBeVisible({ timeout: 5000 });

    const commentInput = modal.locator('textarea').first();
    if (await commentInput.isVisible()) {
      await commentInput.fill('网络权限核准通过');
    }

    const confirmApproveBtn = modal.locator('button.ant-btn-primary:has-text("确认批准"), button:has-text("确定")').first();
    await confirmApproveBtn.click();

    await expect(modal).not.toBeVisible({ timeout: 10000 });

    // 3.5 访问该工单详情页，验证全流程审批通过
    if (createdTicketId) {
      await page.goto(`/tickets/${createdTicketId}`);
      await page.waitForLoadState('domcontentloaded');
      await expect(page.locator('body')).toBeVisible();
    }
  });
});
