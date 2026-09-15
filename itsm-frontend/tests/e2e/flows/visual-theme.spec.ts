import { expect, test, type Page } from '@playwright/test';
import { installVisualThemeFixture } from '../fixtures/visual-theme-fixture';

const credentials = {
  tenantCode: 'visual',
  username: 'visual-admin',
  password: 'local-visual-only',
};

test.use({ serviceWorkers: 'block' });

async function loginThroughNormalForm(page: Page) {
  await page.goto('/login?redirect=%2Fservice-catalog');
  const inputs = page.locator('form input');
  await expect(inputs).toHaveCount(3);
  await inputs.nth(0).fill(credentials.tenantCode);
  await inputs.nth(1).fill(credentials.username);
  await inputs.nth(2).fill(credentials.password);
  await page.getByRole('button', { name: /^登录$/ }).click();
  await expect(page).toHaveURL(/\/service-catalog$/);
  await expect(page.getByRole('heading', { name: '服务目录' })).toBeVisible();
}

async function expectPrimaryButtonPalette(page: Page) {
  const primary = page.getByRole('button', { name: '发起申请' });
  await expect(primary).toBeVisible();
  await expect(primary).toHaveCSS('background-color', 'rgb(240, 104, 32)');
  await expect(primary).toHaveCSS('color', 'rgb(255, 255, 255)');
  await expect(primary.locator('svg').first()).toHaveCSS('color', 'rgb(255, 255, 255)');
}

test.beforeEach(async ({ page }) => {
  await installVisualThemeFixture(page);
});

test('theme control changes the actual catalog and persists across reload', async ({ page }) => {
  await loginThroughNormalForm(page);
  await expect(page.locator('html')).not.toHaveClass(/\bdark\b/);
  await expectPrimaryButtonPalette(page);

  await page.getByRole('button', { name: '切换到暗色' }).click();
  await expect(page.locator('html')).toHaveClass(/\bdark\b/);
  await expect(page.getByRole('button', { name: '切换到亮色' })).toBeVisible();
  await expectPrimaryButtonPalette(page);

  await page.reload();
  await expect(page.locator('html')).toHaveClass(/\bdark\b/);
  await expect(page.getByRole('heading', { name: '服务目录' })).toBeVisible();
  await expect(page.getByText('标准笔记本申请')).toBeVisible();
  await expectPrimaryButtonPalette(page);
});

test('persisted system preference follows the dark system setting', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.addInitScript(() => window.localStorage.setItem('itsm-theme', 'system'));
  await loginThroughNormalForm(page);
  await expect(page.locator('html')).toHaveClass(/\bdark\b/);
  await expect(page.getByRole('button', { name: '切换到亮色' })).toBeVisible();
});

test('theme remains usable when its storage key cannot be read or written', async ({ page }) => {
  await page.addInitScript(() => {
    const originalGetItem = Storage.prototype.getItem;
    const originalSetItem = Storage.prototype.setItem;
    Storage.prototype.getItem = function (key: string) {
      if (key === 'itsm-theme') throw new DOMException('fixture read failure', 'SecurityError');
      return originalGetItem.call(this, key);
    };
    Storage.prototype.setItem = function (key: string, value: string) {
      if (key === 'itsm-theme') throw new DOMException('fixture write failure', 'SecurityError');
      return originalSetItem.call(this, key, value);
    };
  });

  await loginThroughNormalForm(page);
  await page.getByRole('button', { name: '切换到暗色' }).click();
  await expect(page.locator('html')).toHaveClass(/\bdark\b/);
});

test.describe('390px navigation', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('Escape closes the sidebar and restores focus to its toggle', async ({ page }) => {
    await loginThroughNormalForm(page);
    const openNavigation = page.getByRole('button', { name: '展开侧边栏' });
    await openNavigation.click();
    await expect(page.locator('#primary-navigation')).toBeVisible();
    await expect(page.getByRole('button', { name: '关闭导航' })).toBeVisible();

    await page.keyboard.press('Escape');

    await expect(page.locator('#primary-navigation')).not.toBeVisible();
    await expect(openNavigation).toBeFocused();
  });
});
