export function buildAzureLoginURL(apiURL: string): string {
  return `${apiURL}/api/v1/auth/azure/login`;
}
