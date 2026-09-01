import React from 'react';
import { render, waitFor } from '@testing-library/react';
import MainLayout from '../layout';
import { useAuthStore } from '@/lib/store/auth-store';

const push = jest.fn();
const hydrateSession = jest.fn();
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
jest.mock('@/components/common/PageTransition', () => ({
  __esModule: true,
  default: ({ children }: { children: React.ReactNode }) => children,
}));
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
    (useAuthStore.getState as jest.Mock).mockReturnValue({ hydrateSession, logout });
  });

  it('fails closed immediately when /auth/me fails and never loads tenants', async () => {
    hydrateSession.mockRejectedValueOnce(new Error('unauthorized'));

    render(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );

    await waitFor(() => expect(push).toHaveBeenCalledWith('/login?redirect=%2Ftickets'));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(hydrateSession).toHaveBeenCalledTimes(1);
  });

  it('fails closed when /auth/me does not return a valid actor identity', async () => {
    hydrateSession.mockRejectedValueOnce(new Error('Invalid authenticated actor response'));

    render(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );

    await waitFor(() => expect(push).toHaveBeenCalledWith('/login?redirect=%2Ftickets'));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(hydrateSession).toHaveBeenCalledTimes(1);
  });

  it('projects only the tenant selected by the authenticated session', async () => {
    hydrateSession.mockResolvedValueOnce({
      id: 7,
      username: 'operator',
      email: '',
      name: '',
      role: 'manager',
      tenantId: 22,
    });

    render(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );

    await waitFor(() => expect(hydrateSession).toHaveBeenCalledTimes(1));
    expect(push).not.toHaveBeenCalled();
  });
});
