export function buildAzureLoginURL(apiURL: string, tenantCode: string): string {
  const selectedTenant = tenantCode.trim();
  if (!selectedTenant) {
    throw new Error('tenant code is required');
  }
  return `${apiURL}/api/v1/auth/azure/login?tenantCode=${encodeURIComponent(selectedTenant)}`;
}
