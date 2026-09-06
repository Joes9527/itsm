jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn() },
}));

import { useAuthStore } from '../auth-store';
import { httpClient } from '@/lib/api/http-client';

const mockGet = httpClient.get as jest.Mock;

describe('canonical authenticated session hydration', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockReset();
    useAuthStore.setState({
      user: null,
      currentTenant: null,
      isAuthenticated: false,
      isLoading: false,
    });
  });

  it('hydrates only from /auth/me and the exactly matching tenant', async () => {
    mockGet
      .mockResolvedValueOnce({
        id: 42,
        username: 'operator',
        email: 'operator@example.com',
        name: 'Operator',
        role: 'agent',
        tenantId: 7,
        actorTenantId: 7,
        permissions: ['ticket:read'],
      })
      .mockResolvedValueOnce({
        tenants: [
          {
            id: 7,
            name: 'Authorized Tenant',
            code: 'authorized',
            type: 'standard',
            status: 'active',
            createdAt: '2026-01-01T00:00:00Z',
            updatedAt: '2026-01-01T00:00:00Z',
          },
        ],
      });

    const user = await useAuthStore.getState().hydrateSession();

    expect(mockGet.mock.calls).toEqual([['/api/v1/auth/me'], ['/api/v1/auth/tenants']]);
    expect(user).toMatchObject({ id: 42, tenantId: 7 });
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: true,
      currentTenant: { id: 7, code: 'authorized' },
    });
  });

  it('fails closed when /auth/me has no positive tenant identity', async () => {
    useAuthStore.setState({
      user: {
        id: 99,
        username: 'stale',
        email: '',
        name: '',
        tenantId: 99,
        actorTenantId: 99,
        role: 'end_user',
      },
      isAuthenticated: true,
    });
    mockGet.mockResolvedValueOnce({ id: 42, username: 'operator', role: 'agent' });

    await expect(useAuthStore.getState().hydrateSession()).rejects.toThrow(
      'Invalid authenticated tenant identity'
    );
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      currentTenant: null,
      isAuthenticated: false,
    });
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('fails closed when /auth/me omits the authoritative role', async () => {
    mockGet.mockResolvedValueOnce({ id: 42, username: 'operator', tenantId: 7 });

    await expect(useAuthStore.getState().hydrateSession()).rejects.toThrow(
      'Invalid authenticated actor role'
    );
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('fails closed when the session tenant is absent from authorized tenants', async () => {
    mockGet
      .mockResolvedValueOnce({
        id: 42,
        username: 'operator',
        role: 'agent',
        tenantId: 7,
        actorTenantId: 7,
      })
      .mockResolvedValueOnce({
        tenants: [{ id: 8, name: 'Other', code: 'other', type: 'standard', status: 'active' }],
      });

    await expect(useAuthStore.getState().hydrateSession()).rejects.toThrow(
      'Authenticated tenant is not authorized'
    );
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });
  it('preserves the server native actor tenant when the selected customer differs', async () => {
    mockGet
      .mockResolvedValueOnce({
        id: 42,
        username: 'operator',
        role: 'msp_tech',
        tenantId: 7,
        actorTenantId: 2,
      })
      .mockResolvedValueOnce({
        tenants: [{ id: 7, name: 'Customer', code: 'customer', status: 'active' }],
      });
    const user = await useAuthStore.getState().hydrateSession();
    expect(user).toMatchObject({ id: 42, tenantId: 7, actorTenantId: 2 });
    expect(useAuthStore.getState().currentTenant?.id).toBe(7);
  });

  it.each(
    [undefined, 0, -1, 1.5, true, false, '7', [7], {}, Number.MAX_SAFE_INTEGER + 1].map(
      actorTenantId => ({ actorTenantId })
    )
  )(
    'fails closed for invalid actorTenantId $actorTenantId',
    async ({ actorTenantId }) => {
      useAuthStore.setState({
        user: {
          id: 99,
          username: 'stale',
          email: '',
          name: '',
          tenantId: 99,
          actorTenantId: 99,
          role: 'end_user',
        },
        currentTenant: {
          id: 99,
          name: 'Stale tenant',
          code: 'stale',
          type: 'standard',
          status: 'active',
          createdAt: '2026-01-01T00:00:00Z',
          updatedAt: '2026-01-01T00:00:00Z',
        },
        isAuthenticated: true,
      });
      mockGet
        .mockResolvedValueOnce({
          id: 42,
          username: 'operator',
          role: 'msp_tech',
          tenantId: 7,
          actorTenantId,
        })
        .mockResolvedValueOnce({
          tenants: [{ id: 7, name: 'Customer', code: 'customer', status: 'active' }],
        });
      await expect(useAuthStore.getState().hydrateSession()).rejects.toThrow(
        'Invalid authenticated native tenant identity'
      );
      expect(useAuthStore.getState()).toMatchObject({
        user: null,
        currentTenant: null,
        isAuthenticated: false,
      });
      expect(mockGet.mock.calls).toEqual([['/api/v1/auth/me']]);
    }
  );
});
