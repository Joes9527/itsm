import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import MainLayout from '../layout';
import { useAuthStore } from '@/lib/store/auth-store';

const push = jest.fn();
const hydrateSession = jest.fn();
const logout = jest.fn();
const initPersonaByRole = jest.fn();
const setActivePersona = jest.fn();
let collapsed = false;
const setCollapsed = jest.fn((next: boolean) => {
  collapsed = next;
});
let pathname = '/tickets';
const replace = jest.fn();
const router = { push, replace };

jest.mock('next/navigation', () => ({
  usePathname: () => pathname,
  useRouter: () => router,
}));
jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: { getState: jest.fn() },
}));
jest.mock('@/lib/store/layout-store', () => ({
  useLayoutStore: () => ({ collapsed, setCollapsed }),
}));
jest.mock('@/lib/store/persona-store', () => ({
  usePersonaStore: () => ({
    activePersona: 'workspace',
    setActivePersona,
    initPersonaByRole,
  }),
}));
jest.mock('@/components/layout/Header', () => ({
  Header: ({
    collapsed: isCollapsed,
    onCollapse,
    showSidebarToggle = true,
    sidebarToggleRef,
  }: any) =>
    showSidebarToggle ? (
      <button
        ref={sidebarToggleRef}
        data-sidebar-toggle
        onClick={() => onCollapse(!isCollapsed)}
        aria-label='展开侧边栏'
      >
        toggle
      </button>
    ) : null,
}));
jest.mock('@/components/layout/Sidebar', () => ({
  Sidebar: ({ collapsed: isCollapsed }: any) =>
    isCollapsed ? null : (
      <nav id='primary-navigation' aria-label='主导航'>
        <button>菜单项</button>
      </nav>
    ),
}));
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
    pathname = '/tickets';
    collapsed = false;
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
  });

  it('does not expose a sidebar toggle in the portal layout', async () => {
    pathname = '/portal';
    hydrateSession.mockResolvedValueOnce({ id: 7, role: 'employee' });

    render(
      <MainLayout>
        <div>portal</div>
      </MainLayout>
    );

    await screen.findByText('portal');
    expect(screen.queryByRole('button', { name: '展开侧边栏' })).not.toBeInTheDocument();
  });

  it('closes mobile navigation on Escape and returns focus to its toggle', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    collapsed = true;
    hydrateSession.mockResolvedValueOnce({ id: 7, role: 'manager' });

    const { rerender } = render(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );
    const toggle = await screen.findByRole('button', { name: '展开侧边栏' });
    fireEvent.click(toggle);
    rerender(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    rerender(
      <MainLayout>
        <div>protected</div>
      </MainLayout>
    );

    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument();
    await waitFor(() => expect(toggle).toHaveFocus());
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
      actorTenantId: 22,
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
