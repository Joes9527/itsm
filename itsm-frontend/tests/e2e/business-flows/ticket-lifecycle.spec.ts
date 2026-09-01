// itsm-frontend/tests/e2e/business-flows/ticket-lifecycle.spec.ts
import { test, expect } from '@playwright/test';
import { loginAs } from '../utils/test-utils';
import { TicketPage } from '../utils/page-objects/TicketPage';

test.describe('工单完整生命周期测试', () => {
  test('工单详情页面元素验证', async ({ page }) => {
    await loginAs(page, 'admin');
    const ticketPage = new TicketPage(page);
    await ticketPage.goto();

    // 检查表格是否存在
    await expect(page.locator('table')).toBeVisible();

    const ticketId = await ticketPage.getFirstTicketId();
    expect(ticketId).not.toBeNull();
    expect(Number(ticketId)).toBeGreaterThan(0);

    await ticketPage.openTicket(Number(ticketId));
    await page.waitForLoadState('domcontentloaded');

    // 验证详情页面加载
    expect(page.url()).toMatch(/\/tickets\/\d+/);
    await expect(page.getByRole('main')).toBeVisible();
  });
});
