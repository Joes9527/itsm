import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';
import { hasBrowserSession } from '@/lib/auth/browser-session';

// 需要认证的路由
const protectedRoutes = [
  '/dashboard',
  '/tickets',
  '/incidents',
  '/problems',
  '/changes',
  '/assets',
  '/releases',
  '/licenses',
  '/cmdb',
  '/service-catalog',
  '/knowledge-base',
  '/knowledge',
  '/sla',
  '/sla-dashboard',
  '/reports',
  '/workflow',
  '/users',
  '/settings',
  '/admin',
  '/enterprise',
  '/projects',
  '/applications',
  '/tags',
  '/msp',
  '/ai',
  '/approvals',
  '/improvements',
  '/installations',
  '/marketplace',
  '/my-requests',
  '/notifications',
  '/profile',
  '/service-requests',
  '/standard-changes',
  '/system',
  '/teams',
  '/templates',
  '/agent-ops-demo',
];

// 公开路由（不需要认证）
const publicRoutes = ['/login', '/register', '/forgot-password', '/reset-password'];

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

  // 检查是否为受保护的路由
  const isProtectedRoute = protectedRoutes.some(route => pathname.startsWith(route));

  // 检查是否为公开路由
  const isPublicRoute = publicRoutes.some(route => pathname.startsWith(route));

  // Edge 层只做 HttpOnly cookie 存在性粗门禁；签名、过期、tenant 与 ACL
  // 都由后端 `/auth/me` 和授权策略 fail-closed 校验。
  if (isProtectedRoute && !hasSession) {
    const loginUrl = new URL('/login', request.url);
    loginUrl.searchParams.set('redirect', pathname);
    return NextResponse.redirect(loginUrl);
  }

  // 如果已登录用户访问公开路由，重定向到仪表盘
  if (isPublicRoute && hasSession) {
    return NextResponse.redirect(new URL('/dashboard', request.url));
  }

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
