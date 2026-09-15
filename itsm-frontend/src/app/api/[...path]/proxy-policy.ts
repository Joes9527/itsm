const EXACT_PUBLIC_PATHS = new Set([
  '/api/v1/auth/login',
  '/api/v1/auth/register',
  '/api/v1/auth/refresh',
  '/api/v1/auth/forgot-password',
  '/api/v1/auth/reset-password',
  '/api/v1/auth/sso',
  '/api/v1/auth/azure/login',
  '/api/v1/auth/azure/callback',
  '/api/v1/csrf-token',
  '/api/v1/health',
  '/api/v1/connectors',
  '/api/v1/connectors/health',
]);

export function isPublicProxyPath(path: string): boolean {
  return EXACT_PUBLIC_PATHS.has(path);
}

function expiredSessionCookies(names: readonly string[], secure: boolean): string[] {
  const secureAttribute = secure ? '; Secure' : '';
  return names.map(
    name => `${name}=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax${secureAttribute}`
  );
}

// A protected-resource 401 means only the access token is stale. Keeping the
// refresh cookie lets the one canonical browser client rotate the session.
// Only an explicit refresh-session rejection invalidates both credentials;
// infrastructure failures (including 503) preserve the session for retry.
export function sessionCookieExpirations(path: string, status: number, secure: boolean): string[] {
  if (status !== 401) return [];
  if (path === '/api/v1/auth/refresh') {
    return expiredSessionCookies(['access_token', 'refresh_token'], secure);
  }
  return expiredSessionCookies(['access_token'], secure);
}
