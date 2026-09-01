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
