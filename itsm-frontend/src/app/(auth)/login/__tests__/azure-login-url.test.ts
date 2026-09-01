import { buildAzureLoginURL } from '../azure-login-url';

describe('buildAzureLoginURL', () => {
  it('binds the selected ITSM tenant code to the Azure login request', () => {
    expect(buildAzureLoginURL('https://api.example.test', 'customer / 华东')).toBe(
      'https://api.example.test/api/v1/auth/azure/login?tenantCode=customer%20%2F%20%E5%8D%8E%E4%B8%9C'
    );
  });

  it('fails closed when no tenant code is selected', () => {
    expect(() => buildAzureLoginURL('', '   ')).toThrow('tenant code is required');
  });
});
