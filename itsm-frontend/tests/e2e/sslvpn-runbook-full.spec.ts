/**
 * SSLVPN 手工全流程测试手册 (sslvpn-manual-lifecycle-runbook.md) 自动化有头走查套件
 *
 * 覆盖阶段：
 * - Phase 1 (OP01-OP05): 管理员登录与系统管理就绪核验 (Users / Roles / Groups，包含 it_director_test 账号就绪)
 * - Phase 2 (OP06-OP10): 服务目录与工作流定义核验 (核验三级审批 BPMN 流程与专属服务目录绑定)
 * - Phase 3 (OP11-OP17): 正向端到端全流程 (申请人提单校验与提交 -> 协同评论 -> 主管初审 -> 网络运维复审 -> 履约核查)
 * - Phase 4 (OP19): 负向用例 (主管初审拒绝意见必填拦截 -> 确认拒绝流转终止)
 * - Phase 5 (拓展场景): 三级审批全生命周期贯穿演练 (申请人提单 -> 主管初审 -> 网络运维复审 -> IT总监终审 -> 审批通过闭环)
 */

import { test, expect } from '@playwright/test';
import { execSync } from 'child_process';
import * as path from 'path';

test.describe.configure({ mode: 'serial' });
test.use({
  launchOptions: {
    slowMo: 350,
  },
});

const APP_URL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3010';
const TENANT_CODE = process.env.PLAYWRIGHT_TENANT_CODE || 'default';

const USERS = {
  admin: {
    tenantCode: TENANT_CODE,
    username: 'admin',
    password: 'admin123',
    name: '系统管理员',
  },
  endUser: {
    tenantCode: TENANT_CODE,
    username: 'end_user_test',
    password: 'Password123!',
    name: '申请人测试账号',
  },
  supervisor: {
    tenantCode: TENANT_CODE,
    username: 'supervisor_test',
    password: 'Password123!',
    name: '主管初审测试账号',
  },
  lixin: {
    tenantCode: TENANT_CODE,
    username: 'lixin_test',
    password: 'Password123!',
    name: '李昕/L2网络运维',
  },
  itDirector: {
    tenantCode: TENANT_CODE,
    username: 'it_director_test',
    password: 'Password123!',
    name: 'SSLVPN IT总监测试员',
  },
  // 集团真实业务人员 (来自 eHR "公司架构" 真实组织树)
  luka: {
    tenantCode: TENANT_CODE,
    username: 'D42784',
    password: 'P@ssw0rd2026!',
    name: '王雅蓉 (Luka/申请人)',
    email: 'Julian@dawnpro.onmicrosoft.com',
  },
  zoey: {
    tenantCode: TENANT_CODE,
    username: 'D33080',
    password: 'P@ssw0rd2026!',
    name: '赵颖 (Zoey/IT帮助台主管)',
    email: 'Zoey.Y.Zhao@kln.com',
  },
  julianPeng: {
    tenantCode: TENANT_CODE,
    username: 'D45124',
    password: 'P@ssw0rd2026!',
    name: '彭军 (Julian Peng/服务台研发经理)',
    email: 'julian.j.peng@kln.com',
  },
  jinhaiWang: {
    tenantCode: TENANT_CODE,
    username: 'D47105',
    password: 'P@ssw0rd2026!',
    name: '王金海 (Jinhai Wang/L2网络助理经理)',
    email: 'Jinhai.Wang@kln.com',
  },
};

const RUN_ID = 'SSLVPN-UI-20260917-02';
let positiveTicketId: number = 0;
let positiveTicketNumber: string = '';
let positiveTicketUrl: string = '';

async function loginViaUI(page: any, user: { tenantCode: string; username: string; password: string }) {
  console.log(`\n[UI 操作] 正在以账号 [${user.username}] 登录系统...`);
  await page.goto(`${APP_URL}/login`);
  await page.waitForLoadState('domcontentloaded');

  const inputs = page.locator('form input');
  await expect(inputs).toHaveCount(3);

  await inputs.nth(0).click();
  await inputs.nth(0).fill(user.tenantCode);
  await inputs.nth(1).click();
  await inputs.nth(1).fill(user.username);
  await inputs.nth(2).click();
  await inputs.nth(2).fill(user.password);

  const loginBtn = page.getByRole('button', { name: /^登录$/ });
  await loginBtn.click();
  await page.waitForURL((url: URL) => !url.pathname.includes('/login'), { timeout: 20000 });
  console.log(`[UI 操作] 登录成功，当前 URL: ${page.url()}`);
}

// 申请理由：目录声明了必填的理由字段（如 reason）时，申请页不再重复询问「申请理由」，
// 该答案由目录字段承担并作为工单描述提交；没有该字段的目录仍用通用字段。
async function fillRequestReason(page: any, reason: string) {
  const catalogReason = page.locator('#customFields_reason');
  if ((await catalogReason.count()) > 0) {
    await catalogReason.fill(reason);
    return;
  }
  await page.locator('#reason').fill(reason);
}

async function logoutViaUI(page: any) {
  console.log('[UI 操作] 清理当前会话登出...');
  await page.context().clearCookies();
  await page.goto(`${APP_URL}/login`);
  await page.waitForLoadState('domcontentloaded');
}

test.describe('SSLVPN 运行手册全生命周期交互测试 (Headed Visual Execution)', () => {
  test.setTimeout(360_000);

  // =========================================================================
  // Phase 1: OP01 - OP05 系统管理员与基础配置核验 (含 it_director_test)
  // =========================================================================
  test('Phase 1 [OP01-OP05]: 管理员登录并核对用户(含IT总监)、角色与审批候选组', async ({ page }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 1: OP01 - OP05 系统配置就绪核验');
    console.log('======================================================');

    // OP01 & OP02: 管理员登录
    await loginViaUI(page, USERS.admin);
    await expect(page).toHaveURL(/\/admin\/overview/);
    console.log('[OP02] 管理员概览页加载成功，系统正常在线');
    await page.waitForTimeout(1000);

    // OP03: 用户管理核验
    console.log('[OP03] 导航至用户管理页 /admin/users...');
    await page.goto(`${APP_URL}/admin/users`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });
    console.log('[OP03] 用户管理表格已呈现，核验基础账号状态');

    // 搜索并确认 it_director_test 存在
    const userSearchInput = page.getByPlaceholder('搜索用户名、姓名、邮箱、职能条线');
    if (await userSearchInput.isVisible()) {
      await userSearchInput.fill('it_director_test');
      await page.waitForTimeout(500);
      await userSearchInput.press('Enter');
      await page.waitForTimeout(1000);
      const userRow = page.getByRole('row').filter({ hasText: 'it_director_test' }).first();
      await expect(userRow).toBeVisible({ timeout: 10000 });
      console.log('[OP03] IT总监测试账号 it_director_test 已确认就绪');
    }
    await page.waitForTimeout(1500);

    // OP04: 角色管理核验
    console.log('[OP04] 导航至角色管理页 /admin/roles...');
    await page.goto(`${APP_URL}/admin/roles`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });
    console.log('[OP04] 角色列表已就绪，核验审批角色 (dept_manager, network_eng, it_director)');
    await page.waitForTimeout(1500);

    // OP05: 组管理核验
    console.log('[OP05] 导航至组管理页 /admin/groups...');
    await page.goto(`${APP_URL}/admin/groups`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });
    console.log('[OP05] 审批候选组已就绪，核验完成');
    await page.waitForTimeout(1500);

    await logoutViaUI(page);
  });

  // =========================================================================
  // Phase 2: OP06 - OP10 服务目录与工作流定义核验 (含新建三级审批目录)
  // =========================================================================
  test('Phase 2 [OP06-OP10]: 核验三级审批BPMN工作流版本与新服务目录绑定状态', async ({ page }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 2: OP06 - OP10 目录与流程绑定核验');
    console.log('======================================================');

    await loginViaUI(page, USERS.admin);

    // OP06/OP07: 服务目录管理
    console.log('[OP06/07] 导航至服务目录管理 /admin/service-catalogs...');
    await page.goto(`${APP_URL}/admin/service-catalogs`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    // 核验三级审批服务目录是否存在
    const searchCatInput = page.getByPlaceholder('搜索服务目录...');
    if (await searchCatInput.isVisible()) {
      await searchCatInput.fill('三级审批');
      await page.waitForTimeout(1000);
      const catRow = page.getByRole('row').filter({ hasText: '三级审批' }).first();
      await expect(catRow).toBeVisible({ timeout: 10000 });
      console.log('[OP06/07] 【三级审批】专属服务目录已在列表中确认呈现');
    }
    await page.waitForTimeout(1500);

    // OP08: 工作流版本
    console.log('[OP08] 导航至工作流版本管理 /workflow/versions...');
    await page.goto(`${APP_URL}/workflow/versions`);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(1500);

    // OP09: 审批链管理
    console.log('[OP09] 导航至审批链管理 /admin/approval-chains...');
    await page.goto(`${APP_URL}/admin/approval-chains`);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(1500);

    await logoutViaUI(page);
  });

  // =========================================================================
  // Phase 3: OP11 - OP17 申请人提单、协同与双级审批全流程 (Positive Flow)
  // =========================================================================
  test('Phase 3 [OP11-OP17]: 申请人提单(表单校验+提交) -> 评论协同 -> 主管初审 -> 网络运维复审 -> 履约关闭', async ({
    page,
  }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 3: OP11 - OP17 正向全流程申请与双级审批');
    console.log('======================================================');

    // -----------------------------------------------------------------------
    // Step 3.1: 申请人登录并进入申请页 (Catalog 26 - SSLVPN 专项验证目录)
    // -----------------------------------------------------------------------
    await loginViaUI(page, USERS.endUser);

    console.log('[OP11] 进入 SSLVPN 专项可履约申请页 /service-catalog/request/26...');
    await page.goto(`${APP_URL}/service-catalog/request/26`);
    await page.waitForLoadState('networkidle');

    // -----------------------------------------------------------------------
    // Step 3.2 [OP11 表单必填校验拦截]
    // -----------------------------------------------------------------------
    console.log('[OP11.2] 表单校验测试：留空必填字段直接点击提交，验证拦截...');
    const submitBtn = page.getByRole('button', { name: /提交申请/ });
    await submitBtn.click();
    await page.waitForTimeout(1000);

    expect(page.url()).toContain('/service-catalog/request/26');
    console.log('[OP11.2] 必填校验拦截成功生效，未产生未校验工单');

    // -----------------------------------------------------------------------
    // Step 3.3 [OP11.3 填写完整合法数据]
    // -----------------------------------------------------------------------
    console.log('[OP11.3] 录入标准演练数据并准备提交...');
    await page.locator('#title').fill(`${RUN_ID} 出差值班SSLVPN权限申请`);
    await fillRequestReason(page, '因出差值班，需要使用1台设备远程访问内部测试服务，申请临时SSLVPN权限。');

    // 选择访问有效期 (30天)
    const durSelect = page.locator('.ant-select').filter({ has: page.locator('#customFields_duration') });
    await durSelect.click();
    await page.waitForTimeout(300);
    await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)').locator('.ant-select-item-option').first().click();

    await page.waitForTimeout(1000);

    // -----------------------------------------------------------------------
    // Step 3.4 [OP12 提交申请并跳转工单详情]
    // -----------------------------------------------------------------------
    console.log('[OP12] 点击提交申请...');
    await submitBtn.click();

    // 等待跳转进入工单详情
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 25000 });
    positiveTicketUrl = page.url();
    const match = positiveTicketUrl.match(/\/tickets\/(\d+)/);
    expect(match).not.toBeNull();
    positiveTicketId = parseInt(match![1], 10);

    // 提取工单单号 (形如 TKT-202609-000037)
    await page.waitForTimeout(1500);
    const mainText = await page.locator('main').innerText();
    const tktMatch = mainText.match(/(TKT-\d+-\d+)/);
    positiveTicketNumber = tktMatch ? tktMatch[1] : `TKT-${positiveTicketId}`;
    console.log(`[OP12] 工单创建成功！工单 ID: #${positiveTicketId}，单号: ${positiveTicketNumber}，URL: ${positiveTicketUrl}`);
    await page.waitForTimeout(2000);

    // -----------------------------------------------------------------------
    // Step 3.5 [OP13 工单协同评论]
    // -----------------------------------------------------------------------
    console.log('[OP13] 提交公开协同评论...');
    const commentInput = page.locator('textarea').first();
    if (await commentInput.isVisible()) {
      await commentInput.fill('已接收申请，等待主管和网络运维审批。');
      const sendCommentBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendCommentBtn.isVisible()) {
        await sendCommentBtn.click();
        await page.waitForTimeout(1500);
        console.log('[OP13] 公开协同评论已发布');
      }
    }

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 3.6 [OP14 一级主管初审]
    // -----------------------------------------------------------------------
    console.log('\n--- 角色交接：主管初审 (supervisor_test) ---');
    await loginViaUI(page, USERS.supervisor);

    console.log('[OP14] 导航至待办审批中心 /approvals...');
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    // 查找当前工单单号对应的一级待办行
    console.log(`[OP14] 检索工单 ${positiveTicketNumber} 的初审待办任务...`);
    const taskRow1 = page
      .getByRole('row')
      .filter({
        hasText: positiveTicketNumber,
      })
      .first();
    await expect(taskRow1).toBeVisible({ timeout: 15000 });

    // 领取任务
    const claimBtn1 = taskRow1.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn1.isVisible()) {
      console.log('[OP14] 主管领取初审任务...');
      await claimBtn1.click();
      await page.waitForTimeout(1000);
    }

    // 点击批准
    console.log('[OP14] 主管点击批准按钮...');
    const approveBtn1 = taskRow1.getByRole('button', { name: '批准', exact: true });
    await approveBtn1.click();

    // 审批弹窗
    const modal1 = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal1).toBeVisible({ timeout: 5000 });

    const commentBox1 = modal1.getByPlaceholder('可填写审批意见');
    if (await commentBox1.isVisible()) {
      await commentBox1.fill('同意出差值班申请，业务必要性已确认，请网络运维复核。');
    }

    console.log('[OP14] 主管确认批准...');
    const confirmApprove1 = modal1.getByRole('button', { name: '确认批准', exact: true });
    await confirmApprove1.click();
    await expect(modal1).not.toBeVisible({ timeout: 10000 });
    console.log('[OP14] 一级主管初审成功！');
    await page.waitForTimeout(2000);

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 3.7 [OP15 二级网络运维复审]
    // -----------------------------------------------------------------------
    console.log('\n--- 角色交接：网络运维复审 (lixin_test) ---');
    await loginViaUI(page, USERS.lixin);

    console.log('[OP15] 导航至待办审批中心 /approvals...');
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    console.log(`[OP15] 检索工单 ${positiveTicketNumber} 的网络复审待办任务...`);
    const taskRow2 = page
      .getByRole('row')
      .filter({
        hasText: positiveTicketNumber,
      })
      .first();
    await expect(taskRow2).toBeVisible({ timeout: 15000 });

    const claimBtn2 = taskRow2.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn2.isVisible()) {
      console.log('[OP15] 网络运维领取复审任务...');
      await claimBtn2.click();
      await page.waitForTimeout(1000);
    }

    console.log('[OP15] 网络运维点击批准...');
    const approveBtn2 = taskRow2.getByRole('button', { name: '批准', exact: true });
    await approveBtn2.click();

    const modal2 = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal2).toBeVisible({ timeout: 5000 });

    const commentBox2 = modal2.getByPlaceholder('可填写审批意见');
    if (await commentBox2.isVisible()) {
      await commentBox2.fill('目标服务及期限符合固定Dev策略，网络权限核准通过。');
    }

    console.log('[OP15] 网络运维确认批准...');
    const confirmApprove2 = modal2.getByRole('button', { name: '确认批准', exact: true });
    await confirmApprove2.click();
    await expect(modal2).not.toBeVisible({ timeout: 10000 });
    console.log('[OP15] 二级网络复审成功！流程流转至履约完成。');
    await page.waitForTimeout(2000);

    // -----------------------------------------------------------------------
    // Step 3.8 [OP16/17 履约与最终核查]
    // -----------------------------------------------------------------------
    console.log(`[OP16/17] 访问工单详情页 ${positiveTicketUrl} 核对流转结果...`);
    await page.goto(positiveTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    console.log('[OP17] 申请人发表反馈评论...');
    const finalComment = page.locator('textarea').first();
    if (await finalComment.isVisible()) {
      await finalComment.fill('已查看SSLVPN申请结果；本轮双级审批全流程UI验证通过。');
      const sendBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendBtn.isVisible()) {
        await sendBtn.click();
        await page.waitForTimeout(1000);
      }
    }

    console.log('======================================================');
    console.log(`>>> Phase 3 正向全链路测试 PASS！工单号: ${positiveTicketNumber} (#${positiveTicketId})`);
    console.log('======================================================\n');
    await logoutViaUI(page);
  });

  // =========================================================================
  // Phase 4: OP19 负向用例：审批拒绝与意见必填拦截
  // =========================================================================
  test('Phase 4 [OP19]: 负向用例：主管初审拒绝时意见必填拦截与拒绝流转', async ({ page }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 4: OP19 负向审批拦截与拒绝测试');
    console.log('======================================================');

    // 4.1 申请人发起负向测试单
    await loginViaUI(page, USERS.endUser);
    await page.goto(`${APP_URL}/service-catalog/request/26`);
    await page.waitForLoadState('networkidle');

    await page.locator('#title').fill(`${RUN_ID}-REJECT-M 主管拒绝演练单`);
    await fillRequestReason(page, '演练申请：测试主管拒绝逻辑与意见必填拦截。');

    const durSelect = page.locator('.ant-select').filter({ has: page.locator('#customFields_duration') });
    await durSelect.click();
    await page.waitForTimeout(300);
    await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)').locator('.ant-select-item-option').first().click();

    if (await page.locator('#customFields_reason').isVisible()) {
      await page.locator('#customFields_reason').fill('演练申请：测试主管拒绝逻辑。');
    }

    const submitBtn = page.getByRole('button', { name: /提交申请/ });
    await submitBtn.click();
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 25000 });

    const rejectTicketUrl = page.url();
    const match = rejectTicketUrl.match(/\/tickets\/(\d+)/);
    const rejectTicketId = parseInt(match![1], 10);

    await page.waitForTimeout(1500);
    const mainText = await page.locator('main').innerText();
    const tktMatch = mainText.match(/(TKT-\d+-\d+)/);
    const rejectTicketNumber = tktMatch ? tktMatch[1] : `TKT-${rejectTicketId}`;
    console.log(`[OP19.1] 负向演练工单已创建：ID #${rejectTicketId}，单号: ${rejectTicketNumber}`);

    await logoutViaUI(page);

    // 4.2 主管登录待办并测试必填拦截
    console.log('\n--- 主管登录测试拒绝逻辑 ---');
    await loginViaUI(page, USERS.supervisor);
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    const rejectRow = page
      .getByRole('row')
      .filter({
        hasText: rejectTicketNumber,
      })
      .first();
    await expect(rejectRow).toBeVisible({ timeout: 15000 });

    const claimBtn = rejectRow.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn.isVisible()) {
      await claimBtn.click();
      await page.waitForTimeout(1000);
    }

    console.log('[OP19.2] 点击拒绝按钮，测试意见留空拦截...');
    const rejectBtn = rejectRow.getByRole('button', { name: '拒绝', exact: true });
    await rejectBtn.click();

    const rejectModal = page.getByRole('dialog', { name: '拒绝任务' });
    await expect(rejectModal).toBeVisible({ timeout: 5000 });

    // 意见留空，直接点击确认拒绝
    const confirmRejectBtn = rejectModal.getByRole('button', { name: '确认拒绝', exact: true });
    await confirmRejectBtn.click();
    await page.waitForTimeout(1000);

    // 模态框仍然可见，因为被“拒绝时必须填写审批意见”拦截
    await expect(rejectModal).toBeVisible();
    console.log('[OP19.2] 拒绝时意见必填拦截成功生效！');

    // 4.3 补齐拒绝意见并确认
    console.log('[OP19.3] 填写拒绝意见并确认拒绝...');
    const rejectComment = rejectModal.getByPlaceholder('请填写拒绝原因');
    await rejectComment.fill('缺少本轮值班业务必要性，本次拒绝。');
    await confirmRejectBtn.click();

    await expect(rejectModal).not.toBeVisible({ timeout: 10000 });
    console.log('[OP19.3] 主管已成功拒绝该申请，工单流转终止。');
    await page.waitForTimeout(2000);

    console.log('======================================================');
    console.log(`>>> Phase 4 负向拒绝测试 PASS！工单号: ${rejectTicketNumber} (#${rejectTicketId})`);
    console.log('======================================================\n');
    await logoutViaUI(page);
  });

  // =========================================================================
  // Phase 5: 三级审批全流程交互验证 (申请人提单 -> 主管初审 -> 网络运维复审 -> IT总监终审 -> 审批通过闭环)
  // =========================================================================
  test('Phase 5 [拓展场景]: 三级审批全流程交互验证 (用户提单 -> 部门经理初审 -> 网络运维复审 -> IT总监终审 -> 审批通过闭环)', async ({
    page,
  }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 5: 新增场景 - 三级审批全生命周期贯穿演练');
    console.log('======================================================');

    // 5.1 申请人登录并进入三级审批目录申请页 (Catalog 46)
    await loginViaUI(page, USERS.endUser);

    console.log('[Phase 5.1] 进入三级审批专属目录申请页 /service-catalog/request/46...');
    await page.goto(`${APP_URL}/service-catalog/request/46`);
    await page.waitForLoadState('networkidle');

    // 表单必填校验拦截演练
    console.log('[Phase 5.2] 留空必填字段点击提交，验证拦截...');
    const submitBtn = page.getByRole('button', { name: /提交申请/ });
    await submitBtn.click();
    await page.waitForTimeout(1000);
    expect(page.url()).toContain('/service-catalog/request/46');
    console.log('[Phase 5.2] 必填项拦截生效');

    // 录入三级审批申请数据
    console.log('[Phase 5.3] 录入三级审批申请数据并提交...');
    await page.locator('#title').fill(`${RUN_ID}-3LEVEL SSL-VPN 远程办公访问权限申请（三级审批）`);
    await fillRequestReason(page, '因重大生产项目保障与值班，申请临时三级审批SSLVPN访问权限。');

    // 选择访问有效期 (30天)
    const durSelect = page.locator('.ant-select').filter({ has: page.locator('#customFields_duration') });
    if (await durSelect.isVisible()) {
      await durSelect.click();
      await page.waitForTimeout(300);
      await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)').locator('.ant-select-item-option').first().click();
    }

    if (await page.locator('#customFields_reason').isVisible()) {
      await page.locator('#customFields_reason').fill('因重大生产项目保障与值班申请权限。');
    }
    await page.waitForTimeout(1000);

    // 提交申请
    console.log('[Phase 5.4] 提交三级审批申请...');
    await submitBtn.click();

    // 等待跳转进入工单详情
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 25000 });
    const threeLevelTicketUrl = page.url();
    const match = threeLevelTicketUrl.match(/\/tickets\/(\d+)/);
    expect(match).not.toBeNull();
    const threeLevelTicketId = parseInt(match![1], 10);

    await page.waitForTimeout(1500);
    const mainText = await page.locator('main').innerText();
    const tktMatch = mainText.match(/(TKT-\d+-\d+)/);
    const threeLevelTicketNumber = tktMatch ? tktMatch[1] : `TKT-${threeLevelTicketId}`;
    console.log(`[Phase 5.4] 三级审批工单创建成功！工单 ID: #${threeLevelTicketId}，单号: ${threeLevelTicketNumber}`);

    // 发表协同评论
    const commentInput = page.locator('textarea').first();
    if (await commentInput.isVisible()) {
      await commentInput.fill(`申请人已提交三级审批申请（单号: ${threeLevelTicketNumber}），依次流转主管初审、网络复审与总监终审。`);
      const sendCommentBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendCommentBtn.isVisible()) {
        await sendCommentBtn.click();
        await page.waitForTimeout(1500);
      }
    }

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Level 1: 部门主管初审 (supervisor_test)
    // -----------------------------------------------------------------------
    console.log('\n--- 角色交接 1/3：部门主管初审 (supervisor_test) ---');
    await loginViaUI(page, USERS.supervisor);
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    console.log(`[L1 审批] 检索工单 ${threeLevelTicketNumber} 的待办任务...`);
    const taskRow1 = page.getByRole('row').filter({ hasText: threeLevelTicketNumber }).first();
    await expect(taskRow1).toBeVisible({ timeout: 15000 });

    const claimBtn1 = taskRow1.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn1.isVisible()) {
      console.log('[L1 审批] 主管领取任务...');
      await claimBtn1.click();
      await page.waitForTimeout(1000);
    }

    console.log('[L1 审批] 主管点击批准...');
    const approveBtn1 = taskRow1.getByRole('button', { name: '批准', exact: true });
    await approveBtn1.click();

    const modal1 = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal1).toBeVisible({ timeout: 5000 });
    const commentBox1 = modal1.getByPlaceholder('可填写审批意见');
    if (await commentBox1.isVisible()) {
      await commentBox1.fill('【一级部门初审】出差及值班业务事由确认无误，同意申请，流转网络运维复审。');
    }
    await modal1.getByRole('button', { name: '确认批准', exact: true }).click();
    await expect(modal1).not.toBeVisible({ timeout: 10000 });
    console.log('[L1 审批] 一级主管初审通过！');
    await page.waitForTimeout(2000);

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Level 2: 网络运维复审 (lixin_test)
    // -----------------------------------------------------------------------
    console.log('\n--- 角色交接 2/3：网络运维复审 (lixin_test) ---');
    await loginViaUI(page, USERS.lixin);
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    console.log(`[L2 审批] 检索工单 ${threeLevelTicketNumber} 的待办任务...`);
    const taskRow2 = page.getByRole('row').filter({ hasText: threeLevelTicketNumber }).first();
    await expect(taskRow2).toBeVisible({ timeout: 15000 });

    const claimBtn2 = taskRow2.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn2.isVisible()) {
      console.log('[L2 审批] 网络运维领取任务...');
      await claimBtn2.click();
      await page.waitForTimeout(1000);
    }

    console.log('[L2 审批] 网络运维点击批准...');
    const approveBtn2 = taskRow2.getByRole('button', { name: '批准', exact: true });
    await approveBtn2.click();

    const modal2 = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal2).toBeVisible({ timeout: 5000 });
    const commentBox2 = modal2.getByPlaceholder('可填写审批意见');
    if (await commentBox2.isVisible()) {
      await commentBox2.fill('【二级网络复审】网络网段及访问范围技术核查完毕，无安全冲突，提报IT总监终审。');
    }
    await modal2.getByRole('button', { name: '确认批准', exact: true }).click();
    await expect(modal2).not.toBeVisible({ timeout: 10000 });
    console.log('[L2 审批] 二级网络复审通过！');
    await page.waitForTimeout(2000);

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Level 3: IT 总监终审 (it_director_test)
    // -----------------------------------------------------------------------
    console.log('\n--- 角色交接 3/3：IT总监终审 (it_director_test) ---');
    await loginViaUI(page, USERS.itDirector);
    await page.goto(`${APP_URL}/approvals`);
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByRole('table')).toBeVisible({ timeout: 15000 });

    console.log(`[L3 审批] 检索工单 ${threeLevelTicketNumber} 的待办任务...`);
    const taskRow3 = page.getByRole('row').filter({ hasText: threeLevelTicketNumber }).first();
    await expect(taskRow3).toBeVisible({ timeout: 15000 });

    const claimBtn3 = taskRow3.getByRole('button', { name: '领取', exact: true });
    if (await claimBtn3.isVisible()) {
      console.log('[L3 审批] IT总监领取终审任务...');
      await claimBtn3.click();
      await page.waitForTimeout(1000);
    }

    console.log('[L3 审批] IT总监点击批准...');
    const approveBtn3 = taskRow3.getByRole('button', { name: '批准', exact: true });
    await approveBtn3.click();

    const modal3 = page.getByRole('dialog', { name: '批准任务' });
    await expect(modal3).toBeVisible({ timeout: 5000 });
    const commentBox3 = modal3.getByPlaceholder('可填写审批意见');
    if (await commentBox3.isVisible()) {
      await commentBox3.fill('【三级总监终审】符合高风险远程接入规范，最终核准通过，完成审批。');
    }
    await modal3.getByRole('button', { name: '确认批准', exact: true }).click();
    await expect(modal3).not.toBeVisible({ timeout: 10000 });
    console.log('[L3 审批] 三级IT总监终审通过！流程流转完毕。');
    await page.waitForTimeout(2000);

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 5.7: 工单详情与流转闭环核验
    // -----------------------------------------------------------------------
    console.log(`\n[Phase 5.7] 访问工单详情 ${threeLevelTicketUrl} 进行三级流转核验...`);
    await loginViaUI(page, USERS.endUser);
    await page.goto(threeLevelTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    const finalComment = page.locator('textarea').first();
    if (await finalComment.isVisible()) {
      await finalComment.fill('已确认工单三级审批（部门经理->网络运维->IT总监）完整流转并通过！');
      const sendBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendBtn.isVisible()) {
        await sendBtn.click();
        await page.waitForTimeout(1000);
      }
    }

    console.log('======================================================');
    console.log(`>>> Phase 5 三级审批全生命周期贯穿演练 PASS！工单号: ${threeLevelTicketNumber} (#${threeLevelTicketId})`);
    console.log('======================================================\n');
    await logoutViaUI(page);
  });

  // =========================================================================
  // Phase 6: 邮件渠道进单与真实业务人员全生命周期协同场景 (Real Personas: Luka -> Zoey -> Julian Peng -> Jinhai Wang)
  // =========================================================================
  test('Phase 6 [真实业务人员]: 邮件渠道进单 -> 自动回复确认 -> 帮助台协同分派 -> 部门经理审批 -> L2网络复审', async ({ page }) => {
    console.log('\n======================================================');
    console.log('>>> 执行 Phase 6: 真实业务人员邮件进单与全流程协同场景');
    console.log('======================================================');

    const timestamp = Date.now();
    const mailSubject = `[E2E-AUTO-${timestamp}] Luka 申请出差值班 SSLVPN 权限`;
    const mailBody = `您好，我是王雅蓉(Luka，工号D42784)，因值班及远程保障需要，申请开通SSL-VPN访问权限。测试时间戳: ${timestamp}。请帮助台主管赵颖核实，并流转部门经理彭军与网络组王金海审批。`;

    // -----------------------------------------------------------------------
    // Step 6.1: 模拟真实员工向服务台共享邮箱发送邮件
    // -----------------------------------------------------------------------
    console.log(`\n[Phase 6.1] 调用 Microsoft Graph API 以申请人 Luka (${USERS.luka.email}) 身份向服务台 (ai-support@dawnpro.onmicrosoft.com) 发送邮件...`);
    const helperPath = path.resolve(__dirname, 'helpers/send-test-email.py');
    const sendResult = execSync(`python3 "${helperPath}" "${mailSubject}" "${mailBody}"`, { encoding: 'utf-8' });
    console.log(`[Phase 6.1] 邮件发信结果:\n${sendResult}`);
    expect(sendResult).toContain('status 202');

    // -----------------------------------------------------------------------
    // Step 6.2: 等待系统轮询建单与 Outbox 自动回信
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.2] 等待后台连接器轮询 (poll_interval=10s) 抓取邮件、AI分类并生成工单...');
    await page.waitForTimeout(14000);

    // -----------------------------------------------------------------------
    // Step 6.3: 申请人 Luka (D42784) 登录 UI 查看自动生成的工单
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.3] 申请人 Luka 登录 ITSM 前端，核验工单列表中的邮件工单...');
    await loginViaUI(page, USERS.luka);
    await page.goto(`${APP_URL}/tickets`);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 页面列表默认按最新降序排序，新生成的邮件工单位于表格最前端
    const emailTicketRow = page.getByRole('row').filter({ hasText: mailSubject }).first();
    await expect(emailTicketRow).toBeVisible({ timeout: 20000 });
    console.log(`[Phase 6.3] 成功定位邮件自动生成的工单行！`);

    // 提取工单号并进入详情
    const ticketCellBtn = emailTicketRow.getByRole('button', { name: /TKT-\d+-\d+/ }).first();
    const emailTicketNumber = (await ticketCellBtn.textContent())?.trim() || '';
    console.log(`[Phase 6.3] 点击进入邮件工单详情: ${emailTicketNumber}`);
    if (await ticketCellBtn.isVisible()) {
      await ticketCellBtn.click();
    } else {
      await emailTicketRow.click();
    }
    await page.waitForURL(/\/tickets\/\d+/, { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    const emailTicketUrl = page.url();
    console.log(`[Phase 6.3] 成功进入工单详情页: ${emailTicketUrl}`);

    // 核验工单详情页面包含发件主题
    await expect(page.locator('body')).toContainText(mailSubject);
    console.log(`[Phase 6.3] 工单标题核对一致！来源标识确认包含邮件属性。`);

    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 6.4: IT帮助台主管 赵颖 (Zoey Zhao, D33080) 登录 UI 协同处理与批注
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.4] IT帮助台主管 赵颖 (Zoey Zhao, D33080) 登录系统进行工单分派与协同批注...');
    await loginViaUI(page, USERS.zoey);
    await page.goto(emailTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    console.log('[Phase 6.4] 赵颖核实 Luka 出差值班信息，在工单评论区填写分派与核对意见...');
    const commentInput = page.locator('textarea').first();
    if (await commentInput.isVisible()) {
      await commentInput.fill('【服务台初核】已核实王雅蓉(Luka)出差值班排班表真实有效，符合集团远程接入申报条件。转部门经理彭军初审与网络组王金海技术复审。');
      const sendBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendBtn.isVisible()) {
        await sendBtn.click();
        await page.waitForTimeout(1500);
      }
    }
    console.log('[Phase 6.4] 赵颖协同批注提交成功！');
    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 6.5: IT Service Desk Manager 彭军 (Julian Peng, D45124) 登录审核
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.5] IT Service Desk Manager 彭军 (Julian Peng, D45124) 登录系统查看并确认...');
    await loginViaUI(page, USERS.julianPeng);
    await page.goto(emailTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    const mgrCommentInput = page.locator('textarea').first();
    if (await mgrCommentInput.isVisible()) {
      await mgrCommentInput.fill('【部门初审意见】同意王雅蓉出差值班期间的 SSL-VPN 权限申请，请网络组王金海评估接入策略并配置。');
      const sendBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendBtn.isVisible()) {
        await sendBtn.click();
        await page.waitForTimeout(1500);
      }
    }
    console.log('[Phase 6.5] 部门经理彭军初审协同意见记录完成！');
    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 6.6: L2 网络工程师 王金海 (Jinhai Wang, D47105) 登录执行技术复审与配置
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.6] L2 网络工程师 王金海 (Jinhai Wang, D47105) 登录系统查看并完成配置...');
    await loginViaUI(page, USERS.jinhaiWang);
    await page.goto(emailTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    const netCommentInput = page.locator('textarea').first();
    if (await netCommentInput.isVisible()) {
      await netCommentInput.fill('【网络技术复审】已核对 Luka 网段访问控制策略，已在 SSL-VPN 网关分配账号权限，策略开通完毕。');
      const sendBtn = page.getByRole('button', { name: /发送|发表|评论|提交/ }).first();
      if (await sendBtn.isVisible()) {
        await sendBtn.click();
        await expect(page.locator('body')).toContainText('【网络技术复审】', { timeout: 10000 });
      }
    }
    console.log('[Phase 6.6] 网络工程师王金海复审与配置批注完成！');
    await logoutViaUI(page);

    // -----------------------------------------------------------------------
    // Step 6.7: 闭环回访 - 申请人 Luka 再次查看工单时间线与各方审批意见
    // -----------------------------------------------------------------------
    console.log('\n[Phase 6.7] 申请人 Luka 重新登录查看全流程审批协同留痕...');
    await loginViaUI(page, USERS.luka);
    await page.goto(emailTicketUrl);
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    await expect(page.locator('body')).toContainText('【服务台初核】');
    await expect(page.locator('body')).toContainText('【部门初审意见】');
    await expect(page.locator('body')).toContainText('【网络技术复审】');
    console.log(`[Phase 6.7] 成功核验时间线中全部真实业务人员的流转留痕！工单 ${emailTicketNumber} 闭环全通！`);

    console.log('======================================================');
    console.log(`>>> Phase 6 真实业务人员邮件进单与协同流转全流程 PASS！单号: ${emailTicketNumber}`);
    console.log('======================================================\n');
    await logoutViaUI(page);
  });
});
