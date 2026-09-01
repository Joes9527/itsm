/**
 * Tests for src/lib/auth module
 */

jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    delete: jest.fn(),
    patch: jest.fn(),
  },
}));

describe('token-storage', () => {
  let tokenStorage: typeof import('../token-storage');

  beforeEach(() => {
    jest.resetModules();
    localStorage.clear();
    // Clear cookies
    Object.defineProperty(document, 'cookie', {
      writable: true,
      value: '',
    });
  });

  it('getTenantCode returns stored value', async () => {
    localStorage.setItem('current_tenant_code', 'TENANT_A');
    tokenStorage = await import('../token-storage');
    expect(tokenStorage.getTenantCode()).toBe('TENANT_A');
  });

  it('getTenantId returns stored value', async () => {
    localStorage.setItem('current_tenant_id', '42');
    tokenStorage = await import('../token-storage');
    expect(tokenStorage.getTenantId()).toBe('42');
  });

  it('clearAuthStorage removes all keys', async () => {
    localStorage.setItem('current_tenant_id', '1');
    localStorage.setItem('current_tenant_code', 'X');
    tokenStorage = await import('../token-storage');
    tokenStorage.clearAuthStorage();
    expect(localStorage.getItem('current_tenant_id')).toBeNull();
    expect(localStorage.getItem('current_tenant_code')).toBeNull();
  });

  it('STORAGE_KEYS are exported correctly', async () => {
    tokenStorage = await import('../token-storage');
    expect(tokenStorage.STORAGE_KEYS.TENANT_CODE).toBe('current_tenant_code');
  });
});

describe('tenant-context', () => {
  let tenantContext: typeof import('../tenant-context');

  beforeEach(() => {
    jest.resetModules();
  });

  it('initial state is null', async () => {
    tenantContext = await import('../tenant-context');
    expect(tenantContext.getTenantId()).toBeNull();
    expect(tenantContext.getTenantCode()).toBeNull();
  });

  it('setTenantId updates tenant id', async () => {
    tenantContext = await import('../tenant-context');
    tenantContext.setTenantId(5);
    expect(tenantContext.getTenantId()).toBe(5);
  });

  it('setTenantCode updates tenant code', async () => {
    tenantContext = await import('../tenant-context');
    tenantContext.setTenantCode('ABC');
    expect(tenantContext.getTenantCode()).toBe('ABC');
  });

  it('setTenant updates both', async () => {
    tenantContext = await import('../tenant-context');
    tenantContext.setTenant(10, 'COMPANY');
    expect(tenantContext.getTenantId()).toBe(10);
    expect(tenantContext.getTenantCode()).toBe('COMPANY');
  });

  it('clearTenant resets state', async () => {
    tenantContext = await import('../tenant-context');
    tenantContext.setTenant(10, 'COMPANY');
    tenantContext.clearTenant();
    expect(tenantContext.getTenantId()).toBeNull();
    expect(tenantContext.getTenantCode()).toBeNull();
  });

  it('getState returns full state', async () => {
    tenantContext = await import('../tenant-context');
    tenantContext.setTenant(7, 'X');
    const state = tenantContext.getState();
    expect(state.tenantId).toBe(7);
    expect(state.tenantCode).toBe('X');
  });

  it('subscribe notifies on changes', async () => {
    tenantContext = await import('../tenant-context');
    const fn = jest.fn();
    const unsub = tenantContext.subscribe(fn);
    tenantContext.setTenantId(99);
    expect(fn).toHaveBeenCalledWith(expect.objectContaining({ tenantId: 99 }));
    unsub();
    tenantContext.setTenantId(100);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
