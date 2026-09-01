import { expiredSessionCookies, isPublicProxyPath } from './proxy-policy';

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

  it('clears both session cookies when the backend rejects an invalid cookie', () => {
    expect(expiredSessionCookies(true)).toEqual([
      'access_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure',
      'refresh_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure',
    ]);
  });
});
