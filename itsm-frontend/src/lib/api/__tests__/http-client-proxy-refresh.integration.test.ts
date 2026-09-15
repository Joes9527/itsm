import type { NextRequest } from 'next/server';

import { httpClient } from '@/lib/api/http-client';

const nodeText = require('util') as {
  TextEncoder: typeof TextEncoder;
  TextDecoder: typeof TextDecoder;
};
const nodeStreams = require('stream/web') as Record<string, unknown>;
Object.assign(globalThis, nodeText, nodeStreams);
if (typeof globalThis.structuredClone !== 'function') {
  globalThis.structuredClone = <T>(value: T): T => value;
}
const edgePrimitives = require('next/dist/compiled/@edge-runtime/primitives') as {
  Request: typeof Request;
  Response: typeof Response;
  Headers: typeof Headers;
};
Object.assign(globalThis, {
  Request: edgePrimitives.Request,
  Response: edgePrimitives.Response,
  Headers: edgePrimitives.Headers,
});
const { GET, POST } = require('@/app/api/[...path]/route') as typeof import('@/app/api/[...path]/route');

type CookieJar = Map<string, string>;

function responseWithCookies(body: unknown, status: number, cookies: string[] = []): Response {
  const response = new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
  Object.defineProperty(response.headers, 'getSetCookie', {
    value: () => cookies,
  });
  return response;
}

function setCookieLines(headers: Headers): string[] {
  const native = (headers as unknown as { getSetCookie?: () => string[] }).getSetCookie?.();
  if (native?.length) return native;
  const combined = headers.get('set-cookie');
  return combined ? combined.split(/,(?=\s*(?:access_token|refresh_token)=)/) : [];
}

function applyCookies(jar: CookieJar, headers: Headers): void {
  for (const line of setCookieLines(headers)) {
    const [pair, ...attributes] = line.trim().split(';');
    const separator = pair.indexOf('=');
    const name = pair.slice(0, separator);
    const value = pair.slice(separator + 1);
    const expired = attributes.some(attribute => attribute.trim().toLowerCase() === 'max-age=0');
    if (expired) jar.delete(name);
    else jar.set(name, value);
  }
}

describe('httpClient through the real same-origin proxy', () => {
  it('retains refresh after a resource 401, rotates both cookies, and retries with the new access cookie', async () => {
    const jar: CookieJar = new Map([
      ['access_token', 'expired-access'],
      ['refresh_token', 'valid-refresh'],
    ]);
    let protectedCalls = 0;
    let refreshCalls = 0;

    const dispatch = jest.fn(async (input: string | URL | Request, init: RequestInit = {}) => {
      const rawURL = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(rawURL, 'http://frontend.test');

      if (url.origin === 'http://localhost:8090') {
        const cookie = new Headers(init.headers).get('cookie') ?? '';
        if (url.pathname === '/api/v1/auth/refresh') {
          refreshCalls += 1;
          expect(cookie).toContain('refresh_token=valid-refresh');
          return responseWithCookies({ code: 0, data: { user: { id: 1 } } }, 200, [
            'access_token=rotated-access; Path=/; HttpOnly; SameSite=Lax',
            'refresh_token=rotated-refresh; Path=/; HttpOnly; SameSite=Lax',
          ]);
        }
        if (url.pathname === '/api/v1/tickets/1') {
          protectedCalls += 1;
          if (protectedCalls === 1) {
            expect(cookie).toContain('access_token=expired-access');
            return responseWithCookies({ code: 2001, message: 'expired' }, 401);
          }
          expect(cookie).toContain('access_token=rotated-access');
          expect(cookie).toContain('refresh_token=rotated-refresh');
          return responseWithCookies({ code: 0, data: { id: 1 } }, 200);
        }
        throw new Error(`unexpected backend request ${url.pathname}`);
      }

      const headers = new Headers(init.headers);
      if (jar.size > 0) {
        headers.set('cookie', [...jar].map(([name, value]) => `${name}=${value}`).join('; '));
      }
      const request = {
        nextUrl: url,
        cookies: { get: (name: string) => jar.has(name) ? { name, value: jar.get(name)! } : undefined },
        headers,
        method: init.method ?? 'GET',
        text: async () => typeof init.body === 'string' ? init.body : '',
      } as unknown as NextRequest;
      const path = url.pathname.replace(/^\/api\//, '').split('/');
      const context = { params: Promise.resolve({ path }) };
      const response = request.method === 'POST'
        ? await POST(request, context)
        : await GET(request, context);
      applyCookies(jar, response.headers);
      return response;
    });
    global.fetch = dispatch as unknown as typeof fetch;

    await expect(httpClient.get<{ id: number }>('/api/v1/tickets/1')).resolves.toEqual({ id: 1 });
    expect(protectedCalls).toBe(2);
    expect(refreshCalls).toBe(1);
    expect(jar).toEqual(new Map([
      ['access_token', 'rotated-access'],
      ['refresh_token', 'rotated-refresh'],
    ]));
  });
});
