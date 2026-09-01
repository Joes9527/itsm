import { buildAzureLoginURL } from '../azure-login-url';

describe('buildAzureLoginURL', () => {
  it('uses the deployment-bound Azure login endpoint without a browser tenant override', () => {
    expect(buildAzureLoginURL('https://api.example.test')).toBe(
      'https://api.example.test/api/v1/auth/azure/login'
    );
  });

  it('supports the same-origin proxy without adding query parameters', () => {
    expect(buildAzureLoginURL('')).toBe('/api/v1/auth/azure/login');
  });
});
