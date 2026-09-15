import { isPublicProxyPath, sessionCookieExpirations } from './proxy-policy';

describe('same-origin API proxy authentication policy', () => {
  it.each(['/api/v1/auth/azure/login', '/api/v1/auth/azure/callback'])(
    'allows canonical public Azure route %s without a session cookie',
    path => {
      expect(isPublicProxyPath(path)).toBe(true);
    }
  );

  it('does not make other auth paths public by prefix', () => {
    expect(isPublicProxyPath('/api/v1/auth/azure/login/extra')).toBe(false);
    expect(isPublicProxyPath('/api/v1/auth/me')).toBe(false);
  });

  it('preserves refresh while expiring access after an ordinary protected request returns 401', () => {
    expect(sessionCookieExpirations('/api/v1/tickets', 401, true)).toEqual([
      'access_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure',
    ]);
  });

  it('clears both cookies only when canonical refresh rejects the refresh session', () => {
    expect(sessionCookieExpirations('/api/v1/auth/refresh', 401, true)).toEqual([
      'access_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure',
      'refresh_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure',
    ]);
  });

  it('preserves both cookies when refresh infrastructure is unavailable', () => {
    expect(sessionCookieExpirations('/api/v1/auth/refresh', 503, true)).toEqual([]);
  });
});
