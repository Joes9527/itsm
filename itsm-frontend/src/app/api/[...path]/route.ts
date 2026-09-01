import type { NextRequest } from 'next/server';
import { NextResponse } from 'next/server';
import { hasBrowserSession } from '@/lib/auth/browser-session';
import { isPublicProxyPath } from './proxy-policy';

const BACKEND_BASE_URL = process.env.ITSM_BACKEND_URL || 'http://localhost:8090';

// 禁止代理的敏感路径（防止SSRF和路径遍历）
const BLOCKED_PATHS = [
  '/api/v1/admin/users', // 用户管理敏感操作
  '/api/v1/admin/config', // 配置敏感操作
  '/api/v1/system', // 系统敏感操作
];

function isPathBlocked(path: string[]): boolean {
  const fullPath = '/' + path.join('/');
  return BLOCKED_PATHS.some(blocked => fullPath.startsWith(blocked));
}

async function proxyRequest(request: NextRequest, params: Promise<{ path: string[] }>) {
  const { path } = await params;

  // 公开路径直接放行（不需要 token）
  // path 数组来自 [...path] 捕获，不含 /api 前缀
  // 例如请求 /api/v1/auth/login 时 path = ['v1', 'auth', 'login']
  const fullPath = '/api/' + path.join('/');
  if (isPublicProxyPath(fullPath)) {
    // 跳过认证检查，继续代理
  } else {
    // 认证检查
    if (!hasBrowserSession(request.cookies.get('access_token')?.value)) {
      return NextResponse.json(
        { code: 2001, message: 'Unauthorized: authentication required' },
        { status: 401 }
      );
    }
  }

  // 敏感路径检查
  if (isPathBlocked(path)) {
    return NextResponse.json(
      { code: 2003, message: 'Forbidden: this endpoint cannot be accessed through the proxy' },
      { status: 403 }
    );
  }

  const backendURL = new URL(`/api/${path.join('/')}`, BACKEND_BASE_URL);
  backendURL.search = request.nextUrl.search;

  const headers = new Headers(request.headers);
  headers.delete('host');
  headers.delete('content-length');
  // 浏览器代理不接受第二套 token header 真值；后端只收到 HttpOnly cookie。
  headers.delete('authorization');
  headers.delete('x-auth-token');

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: 'manual',
  };

  if (!['GET', 'HEAD'].includes(request.method)) {
    init.body = await request.text();
  }

  try {
    const response = await fetch(backendURL, init);
    const responseHeaders = new Headers(response.headers);
    responseHeaders.delete('content-encoding');
    responseHeaders.delete('content-length');

    // 修复 Set-Cookie 头丢失问题：
    // fetch() 返回的 Headers 对象按规范会过滤掉 Set-Cookie 头（防止 XSS 窃取），
    // 但 NextResponse 直接构造时可以重新写入。Node.js 18+ 提供 getSetCookie() 获取原始数组。
    // 必须转发后端 Set-Cookie（如 access_token/refresh_token httpOnly cookie），
    // 否则登录后浏览器无法收到 cookie，导致后续请求 401。
    const setCookies =
      (response.headers as unknown as { getSetCookie?: () => string[] }).getSetCookie?.() ?? [];
    if (setCookies.length > 0) {
      // 先删除可能从 Headers 复制过来的单个 Set-Cookie（值为合并字符串，浏览器无法解析）
      responseHeaders.delete('set-cookie');
      for (const cookie of setCookies) {
        responseHeaders.append('set-cookie', cookie);
      }
    }

    return new NextResponse(response.body, {
      status: response.status,
      headers: responseHeaders,
    });
  } catch {
    return NextResponse.json({ code: 5001, message: 'Backend request failed' }, { status: 500 });
  }
}

type RouteContext = { params: Promise<{ path: string[] }> };

export async function GET(request: NextRequest, context: RouteContext) {
  return proxyRequest(request, context.params);
}

export async function POST(request: NextRequest, context: RouteContext) {
  return proxyRequest(request, context.params);
}

export async function PUT(request: NextRequest, context: RouteContext) {
  return proxyRequest(request, context.params);
}

export async function PATCH(request: NextRequest, context: RouteContext) {
  return proxyRequest(request, context.params);
}

export async function DELETE(request: NextRequest, context: RouteContext) {
  return proxyRequest(request, context.params);
}

export const dynamic = 'force-dynamic';
