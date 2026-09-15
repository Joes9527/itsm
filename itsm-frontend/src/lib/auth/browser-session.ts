// Browser routing only performs a coarse session-presence check. JWT signature,
// claims, tenant and authorization validation remain backend responsibilities.
export function hasBrowserSession(accessTokenCookie: string | undefined): boolean {
  return Boolean(accessTokenCookie);
}
