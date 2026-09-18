import type { Page, Route } from '@playwright/test';

/** Later fixtures must fall back through this guard rather than continue to the network. */
export async function guardBusinessWrites(page: Page) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request();
    if (['GET', 'HEAD', 'OPTIONS'].includes(request.method()) || new URL(request.url()).pathname.startsWith('/api/v1/auth/')) {
      await route.fallback();
    } else {
      await route.abort('blockedbyclient');
    }
  });
}

/** Read fixtures cannot accidentally fulfill or forward a business mutation. */
export async function routeRead(page: Page, pattern: string, handler: (route: Route) => Promise<void>) {
  await page.route(pattern, async route => {
    if (route.request().method() !== 'GET') { await route.fallback(); return; }
    await handler(route);
  });
}
