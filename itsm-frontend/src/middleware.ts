import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';
import { hasBrowserSession } from '@/lib/auth/browser-session';
import { authPageRedirect } from '@/lib/auth/middleware-policy';

// API路由（需要特殊处理）
const apiRoutes = ['/api'];

/**
 * Next.js 中间件
 * 处理路由保护和认证检查
 */
export function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const hasSession = hasBrowserSession(request.cookies.get('access_token')?.value);

  // 检查是否为API路由
  if (apiRoutes.some(route => pathname.startsWith(route))) {
    // API路由的认证检查由后端处理
    return NextResponse.next();
  }

  // Edge 层只做 HttpOnly cookie 存在性粗门禁；签名、过期、tenant 与 ACL
  // 都由后端 `/auth/me` 和授权策略 fail-closed 校验。
  const redirect = authPageRedirect(pathname, hasSession);
  if (redirect) return NextResponse.redirect(new URL(redirect, request.url));

  // 根路径：已登录跳转工作台，未登录直接跳转登录页（不再展示介绍页）
  if (pathname === '/') {
    return NextResponse.redirect(new URL(hasSession ? '/dashboard' : '/login', request.url));
  }

  return NextResponse.next();
}

// 配置中间件匹配的路径
export const config = {
  matcher: [
    /*
     * 匹配所有路径除了:
     * - api (API routes)
     * - _next/static (static files)
     * - _next/image (image optimization files)
     * - favicon.ico (favicon file)
     * - public folder
     */
    '/((?!api|_next/static|_next/image|favicon.ico|public).*)',
  ],
};
