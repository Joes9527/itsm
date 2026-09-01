jest.mock('@/lib/api/http-client', () => ({
  httpClient: { post: jest.fn() },
}));

jest.mock('@/lib/store/auth-store', () => {
  const hydrateSession = jest.fn();
  const logout = jest.fn();
  return {
    useAuthStore: {
      getState: () => ({
        user: { id: 1, username: 'operator' },
        isAuthenticated: true,
        hydrateSession,
        logout,
      }),
    },
    mockHydrateSession: hydrateSession,
    mockLogoutProjection: logout,
  };
});

import { AuthService } from '../auth-service';
import { httpClient } from '@/lib/api/http-client';

const mockPost = httpClient.post as jest.Mock;
const authStoreMock = jest.requireMock('@/lib/store/auth-store') as {
  mockHydrateSession: jest.Mock;
  mockLogoutProjection: jest.Mock;
};
const { mockHydrateSession, mockLogoutProjection } = authStoreMock;

describe('AuthService canonical session commands', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('establishes cookies then hydrates exclusively from the canonical session endpoints', async () => {
    const authoritativeUser = {
      id: 9,
      username: 'operator',
      email: 'operator@example.com',
      name: 'Operator',
      role: 'agent',
      tenantId: 7,
    };
    mockPost.mockResolvedValueOnce(undefined);
    mockHydrateSession.mockResolvedValueOnce(authoritativeUser);

    await expect(AuthService.login('operator', 'secret', 'tenant-a')).resolves.toEqual(
      authoritativeUser
    );
    expect(mockPost).toHaveBeenCalledWith('/api/v1/auth/login', {
      username: 'operator',
      password: 'secret',
      tenantCode: 'tenant-a',
    });
    expect(mockHydrateSession).toHaveBeenCalledTimes(1);
  });

  it('revokes a cookie session whose authoritative hydration fails', async () => {
    mockPost.mockResolvedValue(undefined);
    mockHydrateSession.mockRejectedValueOnce(new Error('missing tenant authority'));

    await expect(AuthService.login('operator', 'secret', 'tenant-a')).rejects.toThrow(
      'missing tenant authority'
    );
    expect(mockPost.mock.calls).toEqual([
      ['/api/v1/auth/login', { username: 'operator', password: 'secret', tenantCode: 'tenant-a' }],
      ['/api/v1/auth/logout', {}],
    ]);
  });

  it('clears the local projection only after backend logout succeeds', async () => {
    mockPost.mockResolvedValueOnce(undefined);
    await AuthService.logout();
    expect(mockPost).toHaveBeenCalledWith('/api/v1/auth/logout', {});
    expect(mockLogoutProjection).toHaveBeenCalledTimes(1);
  });

  it('keeps the local projection when backend logout fails', async () => {
    mockPost.mockRejectedValueOnce(new Error('logout unavailable'));
    await expect(AuthService.logout()).rejects.toThrow('logout unavailable');
    expect(mockLogoutProjection).not.toHaveBeenCalled();
  });

  it('registers through the canonical cookie-aware HTTP client', async () => {
    mockPost.mockResolvedValueOnce(undefined);
    const params = {
      username: 'new-user',
      email: 'new@example.com',
      password: 'secret123',
      fullName: 'New User',
    };
    await expect(AuthService.register(params)).resolves.toBe(true);
    expect(mockPost).toHaveBeenCalledWith('/api/v1/auth/register', params);
  });
});
