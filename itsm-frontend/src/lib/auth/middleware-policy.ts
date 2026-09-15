const protectedRoutes = [
  '/dashboard', '/tickets', '/incidents', '/problems', '/changes', '/assets',
  '/releases', '/licenses', '/cmdb', '/service-catalog', '/knowledge-base',
  '/knowledge', '/sla', '/sla-dashboard', '/reports', '/workflow', '/users',
  '/settings', '/admin', '/enterprise', '/projects', '/applications', '/tags',
  '/msp', '/ai', '/approvals', '/improvements', '/installations', '/marketplace',
  '/my-requests', '/notifications', '/profile', '/service-requests',
  '/standard-changes', '/system', '/teams', '/templates', '/agent-ops-demo',
];

const publicAuthRoutes = ['/login', '/register', '/forgot-password', '/reset-password'];

// authPageRedirect deliberately treats a cookie as opaque presence only. A
// public authentication page never redirects based on that unverified value;
// protected pages without a cookie are sent to login and backend /auth/me is
// the authoritative validation for sessions that do have one.
export function authPageRedirect(pathname: string, hasSessionCookie: boolean): string | null {
  if (publicAuthRoutes.some(route => pathname.startsWith(route))) return null;
  if (!hasSessionCookie && protectedRoutes.some(route => pathname.startsWith(route))) {
    return `/login?redirect=${encodeURIComponent(pathname)}`;
  }
  return null;
}
