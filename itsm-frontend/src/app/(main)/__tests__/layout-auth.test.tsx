import React from 'react';
import { render, waitFor } from '@testing-library/react';
import MainLayout from '../layout';
import { httpClient } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

const push = jest.fn();
const login = jest.fn();
const logout = jest.fn();
const initPersonaByRole = jest.fn();
const setActivePersona = jest.fn();
const setCollapsed = jest.fn();
const replace = jest.fn();
const router = { push, replace };

jest.mock('next/navigation', () => ({
  usePathname: () => '/tickets',
  useRouter: () => router,
}));
jest.mock('@/lib/api/http-client', () => ({ httpClient: { get: jest.fn() } }));
jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: { getState: jest.fn() },
}));
jest.mock('@/lib/store/layout-store', () => ({
  useLayoutStore: () => ({ collapsed: false, setCollapsed }),
}));
jest.mock('@/lib/store/persona-store', () => ({
  usePersonaStore: () => ({
    activePersona: 'workspace',
    setActivePersona,
    initPersonaByRole,
  }),
}));
jest.mock('@/components/layout/Header', () => ({ Header: () => null }));
jest.mock('@/components/layout/Sidebar', () => ({ Sidebar: () => null }));
jest.mock('@/components/ui/LoadingSpinner', () => ({ LoadingSpinner: () => <div>loading</div> }));
jest.mock('@/components/common/NetworkStatus', () => ({ NetworkStatus: () => null }));
jest.mock('@/components/common/PageTransition', () => ({ __esModule: true, default: ({ children }: { children: React.ReactNode }) => children }));
jest.mock('antd', () => {
  const Container = ({ children }: { children: React.ReactNode }) => <>{children}</>;
  return {
    Layout: Object.assign(Container, { Content: Container }),
    ConfigProvider: Container,
    App: Container,
  };
});

describe('MainLayout authentication bootstrap', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.spyOn(console, 'error').mockImplementation(() => {});
    (useAuthStore.getState as jest.Mock).mockReturnValue({ login, logout });
  });

  it('fails closed immediately when /auth/me fails and never loads tenants', async () => {
    (httpClient.get as jest.Mock).mockRejectedValueOnce(new Error('unauthorized'));

    render(<MainLayout><div>protected</div></MainLayout>);

    await waitFor(() => expect(push).toHaveBeenCalledWith('/login?redirect=%2Ftickets'));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(httpClient.get).toHaveBeenCalledTimes(1);
    expect(httpClient.get).toHaveBeenCalledWith('/api/v1/auth/me');
    expect(login).not.toHaveBeenCalled();
  });

  it('fails closed when /auth/me does not return a valid actor identity', async () => {
    (httpClient.get as jest.Mock).mockResolvedValueOnce({ role: 'super_admin' });

    render(<MainLayout><div>protected</div></MainLayout>);

    await waitFor(() => expect(push).toHaveBeenCalledWith('/login?redirect=%2Ftickets'));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(httpClient.get).toHaveBeenCalledTimes(1);
    expect(login).not.toHaveBeenCalled();
  });

  it('projects only the tenant selected by the authenticated session', async () => {
    (httpClient.get as jest.Mock)
      .mockResolvedValueOnce({ id: 7, username: 'operator', role: 'manager', tenantId: 22 })
      .mockResolvedValueOnce({
        tenants: [
          { id: 11, name: 'Other', code: 'other', type: 'standard', status: 'active' },
          { id: 22, name: 'Selected', code: 'selected', type: 'standard', status: 'active' },
        ],
      });

    render(<MainLayout><div>protected</div></MainLayout>);

    await waitFor(() => expect(login).toHaveBeenCalled());
    expect(login.mock.calls[0][1]).toEqual(expect.objectContaining({ id: 22, code: 'selected' }));
    expect(login).toHaveBeenCalledWith(
      expect.objectContaining({ id: 7, tenantId: 22 }),
      expect.objectContaining({ id: 22, code: 'selected' })
    );
  });
});
