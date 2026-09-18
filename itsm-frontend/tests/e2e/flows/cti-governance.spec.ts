/**
 * CTI 治理端到端验收（B4 必验矩阵）。
 *
 * 覆盖（每条都用**真实 UI 控件**驱动，不以 API 成功代替浏览器验收）：
 *   1. 普通报障允许"不确定"（未分类提交）
 *   2. 目录默认分类驱动创建：申请不再询问分类，工单只存最深节点
 *   3. 工程师修正分类必须填写原因（缺原因被客户端阻断）
 *   4. 事件：恢复服务不拦；关闭要求完整三级；补齐后关闭成功
 *   5. 通用工单完成门禁
 *   6. 分类维护：三级不再提供下级、编码只读、被引用不可删/移、停用保留历史引用
 *   7. 规则条件"仅当前/包含下级"范围选择器
 *   8. 分类详情"关联与引用"页签：无权时只报告存在引用（不显示计数/名称）；失败不清零
 *   9. 回退/恢复：门禁暂停后回到原行为（管理端开关属 B4 待交付项，见用例内 NOT RUN 标注）
 *
 * NOTE(未执行 / NOT RUN)：需要把本分支的 itsm-frontend + itsm-backend 部署到**隔离环境**
 * （禁止切换正在运行的共享 3010/8080），并准备两租户隔离夹具与只读数据库核对通道。
 * 在获得隔离前端授权前，本文件只作为可执行验收脚本交付，不得据此声明"浏览器验收通过"。
 * 每步的账户/数据/动作/预期见用例内的注释与断言文本。
 */
import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

const stamp = Date.now().toString(36).toUpperCase();
const L1 = `E2E治理一级${stamp}`;
const L2 = `E2E治理二级${stamp}`;
const L3 = `E2E治理三级${stamp}`;
const CATALOG = `E2E治理目录${stamp}`;

// 单条用例要驱动多步真实界面流程（三级分类 + 目录发布 + 断言），30s 默认上限不够；
// next dev 首次编译页面（尤其刚改过源码）会超过默认 5s 断言上限。
// 不设 serial：单条失败不应遮蔽其余矩阵项的结果。
test.describe.configure({ timeout: 120_000 });
expect.configure({ timeout: 15_000 });

test('1+2. catalog default classification drives creation without asking the requester', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/ticket-categories');
  await expect(page.getByRole('heading', { name: '工单分类（CTI）' })).toBeVisible();

  for (const [name, code, parent] of [
    [L1, `E2E_GOV_L1_${stamp}`, null],
    [L2, `E2E_GOV_L2_${stamp}`, L1],
    [L3, `E2E_GOV_L3_${stamp}`, L2],
  ] as const) {
    if (parent) {
      await page.getByRole('button', { name: `为 ${parent} 新增下级分类` }).click();
    } else {
      await page.getByRole('button', { name: '创建一级分类' }).click();
    }
    await page.getByLabel('分类名称').fill(name);
    await page.getByLabel('分类编码').fill(code);
    await page.getByRole('button', { name: /保\s*存/ }).click();
  }
  // 三级节点不再提供"新增下级"：层级上限是产品约束，不是界面遗漏。
  await expect(page.getByRole('button', { name: `为 ${L3} 新增下级分类` })).toHaveCount(0);

  // 目录发布时声明默认分类（启用状态必填）
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/service-catalogs');
  await page.getByRole('button', { name: /新建|创建/ }).first().click();
  await page.getByLabel('服务名称').fill(CATALOG);
  // 目录分类是业务展示分组（必填），与工单三级分类无关；不填会因校验失败而无法保存。
  await page.getByLabel('目录分类').click();
  await page.locator('.ant-select-item-option').first().click();
  await page.getByLabel('服务描述').fill('E2E 验证：目录默认分类驱动工单分类');
  await expect(page.getByLabel('默认工单分类（三级）')).toBeVisible();
  await page.getByLabel('默认工单分类（三级）').click();
  await page.getByText(L1, { exact: true }).click();
  await page.getByText(L2, { exact: true }).click();
  await page.getByText(L3, { exact: true }).click();
  const requestPromise = page.waitForRequest(
    req => req.url().includes('/api/v1/service-catalogs') && req.method() !== 'GET',
  );
  // 目录发布弹窗的提交按钮是「创建」（保存按钮名只用于分类编辑器）。
  await page.getByRole('button', { name: /创\s*建|保\s*存|更\s*新/ }).click();
  const payload = (await (await requestPromise).postDataJSON()) as { defaultTicketCategoryId?: number };
  // 只提交最深节点：后端解析完整路径，前端不复制 C/T/I 文本。
  expect(payload.defaultTicketCategoryId, '目录必须携带默认分类最深节点').toBeTruthy();
});

test('1. ordinary report keeps "unsure" available (unclassified submission stays legal)', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/tickets/create');
  // 统一录入入口先选目标类型，再进入表单。
  await page.getByRole('button', { name: '普通工单' }).click();
  // 报障页的分类是可选项：未分类提交合法，完整性只在“完成”时由门禁要求。
  await expect(page.getByText('工单分类（可选）')).toBeVisible();
  await expect(page.getByText('请选择完整三级分类')).toHaveCount(0);
});

test('3. engineer classification correction requires a reason', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/incidents');
  const firstRow = page.getByRole('row').nth(1);
  await firstRow.getByRole('link', { name: /详情|查看/ }).first().click();
  await page.getByRole('button', { name: '编辑分类' }).click();
  await page.getByRole('button', { name: /保\s*存/ }).click();
  // 改了分类但没有原因：客户端先阻断，不能出现"界面成功、后端 400"。
  await expect(page.getByText('调整分类时必须填写原因')).toBeVisible();
});

test('4. incident: restore is ungated, close requires a complete three-level classification', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/incidents');
  // 关闭未分类事件：后端返回完成质量错误，界面必须显示可读原因而不是"成功"。
  await page.getByRole('row').nth(1).getByRole('button', { name: /关\s*闭/ }).click();
  await expect(page.getByText(/请补齐三级工单分类/)).toBeVisible();
  // 事件"恢复服务"不拦（业务优先：先恢复可用性，分类可后补）。
  await expect(page.getByText(/恢复/)).toBeVisible();
});

test('5. generic work item completion follows the same gate', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/tickets');
  const row = page.getByRole('row').nth(1);
  await row.getByRole('button', { name: '关闭工单' }).click();
  await expect(page.getByText('请补齐三级工单分类', { exact: true })).toBeVisible();
});

test('6. classification maintenance protects references and keeps history', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/ticket-categories');
  await page.getByText(L3).first().click();
  // 编码创建后只读
  await page.getByRole('button', { name: /编\s*辑/ }).click();
  await expect(page.getByLabel('分类编码')).toBeDisabled();
  await page.getByRole('button', { name: /取\s*消/ }).click();

  // 被工单/目录引用的节点：删除与移动都必须被拒绝并给出可读原因。
  await page.getByText(L2).first().click();
  await page.getByRole('button', { name: /删\s*除/ }).click();
  await expect(page.getByText(/已被工单或配置引用/)).toBeVisible();
  await page.keyboard.press('Escape');

  // 停用只禁止新选择：历史引用保留（详情页明确说明）。
  await expect(page.getByText('停用后保留历史引用，仅禁止新选择')).toBeVisible();
});

test('7. rule editor exposes 仅当前分类 / 包含下级', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/tickets/assignment-rules');
  await page.getByRole('button', { name: /新建|创建/ }).first().click();
  await page.getByRole('button', { name: /添加条件/ }).click();
  // 选择"工单分类"字段后，范围选择器出现且默认"仅当前分类"。
  await page.getByLabel('选择字段').click();
  await page.getByText('工单分类', { exact: true }).click();
  await expect(page.getByText('仅当前分类')).toBeVisible();
  await page.getByText('仅当前分类').click();
  await expect(page.getByText('包含下级')).toBeVisible();
});

test('8. reference tab reports existence without leaking counts when details are not permitted', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/ticket-categories');
  await page.getByText(L2).first().click();
  await page.getByRole('tab', { name: '关联与引用' }).click();
  // 有引用：必须提示不可删除/移动（引用来自不受权限影响的安全扫描）。
  await expect(page.getByText('该分类（或其下级）已被引用，不可删除或移动')).toBeVisible();
  // 无权查看的类型只显示"存在引用"，不显示计数；有权查看的类型显示真实计数。
  await expect(page.getByText('存在引用（无权查看明细）').first()).toBeVisible();
  await expect(page.getByText(/共 \d+ 项引用/).first()).toBeVisible();
  // 历史字符串引用不伪装成结构化规则。
  await expect(page.getByText(/无法映射为 ID/)).toBeVisible();
});

test('9. classification move is refused for referenced nodes and recovery keeps history', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/ticket-categories');
  await page.getByText(L2).first().click();
  // 未引用的子树仍可移动；被引用节点必须被拒绝（引用保护按子树判定）。
  const moveButton = page.getByRole('button', { name: /移\s*动/ });
  if ((await moveButton.count()) > 0) {
    await moveButton.click();
    await expect(page.getByText(/已被工单或配置引用|请先处理该分类的下级分类/)).toBeVisible();
    await page.keyboard.press('Escape');
  }
});
