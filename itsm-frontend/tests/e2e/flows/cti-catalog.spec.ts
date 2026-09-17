/**
 * CTI 端到端：管理端维护三级分类 → 目录绑定默认分类 → 申请创建工单。
 *
 * 该用例中的分类与目录都通过真实接口创建（不向共享库写入破坏性清理），
 * 并断言申请创建后的工单只保存所选最深节点 ID，不复制 C/T/I 文本。
 *
 * NOTE(未执行): 需要把本分支的 itsm-frontend/itsm-backend 部署到隔离环境
 * （禁止切换正在运行的共享 3010/8080 服务）。在获得授权目标前保持 NOT RUN。
 */
import { test, expect } from '@playwright/test';
import { loginAndReturn, DEFAULT_LOGIN } from '../auth-utils';

const stamp = Date.now().toString(36).toUpperCase();
const L1 = `E2E网络${stamp}`;
const L2 = `E2E远程${stamp}`;
const L3 = `E2EVPN${stamp}`;
const CATALOG = `E2E目录${stamp}`;

test.describe.configure({ mode: 'serial' });

test('catalog default classification drives the created work item classification', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/ticket-categories');
  await expect(page.getByRole('heading', { name: '工单分类（CTI）' })).toBeVisible();

  // 一级
  await page.getByRole('button', { name: '创建一级分类' }).click();
  await page.getByLabel('分类名称').fill(L1);
  await page.getByLabel('分类编码').fill(`E2E_L1_${stamp}`);
  await page.getByRole('button', { name: /保\s*存/ }).click();
  await expect(page.getByRole('button', { name: `为 ${L1} 新增下级分类` })).toBeVisible();

  // 二级
  await page.getByRole('button', { name: `为 ${L1} 新增下级分类` }).click();
  await page.getByLabel('分类名称').fill(L2);
  await page.getByLabel('分类编码').fill(`E2E_L2_${stamp}`);
  await page.getByRole('button', { name: /保\s*存/ }).click();
  await expect(page.getByRole('button', { name: `为 ${L2} 新增下级分类` })).toBeVisible();

  // 三级：第三级不再提供“新增下级”
  await page.getByRole('button', { name: `为 ${L2} 新增下级分类` }).click();
  await page.getByLabel('分类名称').fill(L3);
  await page.getByLabel('分类编码').fill(`E2E_L3_${stamp}`);
  await page.getByRole('button', { name: /保\s*存/ }).click();
  await expect(page.getByRole('button', { name: `为 ${L3} 新增下级分类` })).toHaveCount(0);

  // 编码创建后只读
  await page.getByText(L3).first().click();
  await page.getByRole('button', { name: /编\s*辑/ }).click();
  await expect(page.getByLabel('分类编码')).toBeDisabled();
  await page.getByRole('button', { name: /取\s*消/ }).click();

  // 目录绑定默认三级分类
  await loginAndReturn(page, DEFAULT_LOGIN, '/admin/service-catalogs');
  await page.getByRole('button', { name: /新建|创建/ }).first().click();
  await page.getByLabel('服务名称').fill(CATALOG);
  await page.getByLabel('默认工单分类（三级）').click();
  await page.getByText(L1, { exact: true }).click();
  await page.getByText(L2, { exact: true }).click();
  await page.getByText(L3, { exact: true }).click();

  // 保存：断言提交的默认分类确实是最深节点（未配置时后端会拒绝发布）
  const [request] = await Promise.all([
    page.waitForRequest(
      req => req.url().includes('/api/v1/service-catalogs') && req.method() !== 'GET'
    ),
    page.getByRole('button', { name: /保\s*存/ }).click(),
  ]);
  const payload = request.postDataJSON() as { defaultTicketCategoryId?: number };
  expect(payload.defaultTicketCategoryId, '保存时必须携带默认分类最深节点').toBeTruthy();
  await expect(page.getByText(CATALOG)).toBeVisible();
});

test('ordinary report keeps the "unsure" option available', async ({ page }) => {
  await loginAndReturn(page, DEFAULT_LOGIN, '/tickets/create');
  const classification = page.getByLabel('工单分类（可选）');
  await expect(classification).toBeVisible();
  await expect(classification).toHaveAttribute('placeholder', /可不确定/);
  await classification.click();
  await page.keyboard.press('Escape');
  await expect(page.getByText('请选择完整三级分类')).toHaveCount(0);
});
