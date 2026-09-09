import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Header } from '@/components/layout/header/Header';
import { Sidebar } from '@/components/layout/sidebar/Sidebar';
import { App } from 'antd';
import MainLayout from '@/app/(main)/layout';

let mockPathname = '/portal';
const mockPush = jest.fn();
let mockHasUser = false;
const mockUser = {
  id: 7,
  username: 'operator',
  name: 'Long Operator Name',
  email: '',
  role: 'manager',
};
const mockUseLayoutStore = () => {
  const [collapsed, setCollapsed] = React.useState(true);
  return { collapsed, setCollapsed };
};

jest.mock('next/navigation', () => ({
  usePathname: () => mockPathname,
  useRouter: () => ({ push: mockPush, replace: jest.fn() }),
}));
jest.mock('@/lib/store/auth-store', () => {
  const useAuthStore = Object.assign(
    () => ({
      user: mockHasUser ? mockUser : null,
      hasPermission: () => false,
      isAdmin: () => false,
    }),
    { getState: () => ({ hydrateSession: async () => mockUser, logout: jest.fn() }) }
  );
  return { useAuthStore };
});
jest.mock('@/lib/store/layout-store', () => ({
  useLayoutStore: () => mockUseLayoutStore(),
}));
jest.mock('@/lib/store/persona-store', () => ({
  usePersonaStore: () => ({
    activePersona: 'workspace',
    setActivePersona: jest.fn(),
    initPersonaByRole: jest.fn(),
  }),
}));
jest.mock('@/lib/api/menu-api', () => ({
  MENUS_UPDATED_EVENT: 'menus-updated',
  getUserMenus: async () => ({
    main: [{ id: 1, name: '工单', path: '/tickets', icon: 'Ticket', children: [] }],
    admin: [],
  }),
}));
jest.mock('@/lib/api/ticket-notification-api', () => ({
  TicketNotificationApi: {
    getUserNotifications: async () => ({ notifications: [] }),
    markNotificationRead: jest.fn(),
    markAllNotificationsRead: jest.fn(),
  },
}));
jest.mock('@/lib/design-system/theme', () => ({
  useTheme: () => ({ isDark: false, toggleTheme: jest.fn() }),
}));
jest.mock('@/lib/i18n', () => ({
  useI18n: () => ({ language: 'zh-CN', changeLanguage: jest.fn() }),
}));
jest.mock('@/lib/services/notification-ws', () => ({
  notificationWS: {
    connect: jest.fn(() => Promise.resolve()),
    disconnect: jest.fn(),
    onNotification: jest.fn(() => jest.fn()),
  },
}));

describe('navigation shell', () => {
  beforeEach(() => {
    mockPathname = '/portal';
    mockHasUser = false;
    mockPush.mockClear();
  });
  it('can render a portal header without a dead sidebar toggle', () => {
    render(
      <Header collapsed onCollapse={jest.fn()} showBreadcrumb={false} showSidebarToggle={false} />
    );

    expect(screen.queryByRole('button', { name: /侧边栏/ })).not.toBeInTheDocument();
  });

  it('keeps notification, theme, and language actions in the mobile more menu', async () => {
    render(<Header collapsed onCollapse={jest.fn()} showBreadcrumb={false} />);

    fireEvent.click(screen.getByRole('button', { name: '更多工具' }));

    expect(await screen.findByText('通知')).toBeInTheDocument();
    expect(screen.getByText('切换到暗色')).toBeInTheDocument();
    expect(screen.getByText('中文')).toBeInTheDocument();
    expect(screen.getByText('English')).toBeInTheDocument();
  });

  it('removes a collapsed sidebar from keyboard and accessibility navigation', () => {
    render(
      <App>
        <Sidebar collapsed onCollapse={jest.fn()} mobile />
      </App>
    );

    const navigation = document.getElementById('primary-navigation');
    expect(navigation).not.toBeNull();
    expect(navigation).toHaveAttribute('aria-hidden', 'true');
    expect(navigation).toHaveAttribute('inert');
  });

  it('uses real navigation components for all mobile close paths and focus containment', async () => {
    mockPathname = '/tickets';
    mockHasUser = true;
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    render(
      <MainLayout>
        <button>页面操作</button>
      </MainLayout>
    );

    let toggle = await waitFor(() => {
      const element = document.querySelector<HTMLButtonElement>('[data-sidebar-toggle]');
      expect(element).not.toBeNull();
      return element!;
    });
    if (toggle.getAttribute('aria-label') === '收起侧边栏') {
      fireEvent.click(screen.getByRole('button', { name: '关闭导航' }));
      await waitFor(() => expect(toggle).toHaveFocus());
      toggle = await screen.findByRole('button', { name: '展开侧边栏' });
    }
    fireEvent.click(toggle);
    const navigation = await screen.findByRole('navigation', { name: '主导航' });
    await waitFor(() => expect(navigation).toContainElement(document.activeElement as HTMLElement));
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    document.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(true);

    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(toggle).toHaveFocus());

    fireEvent.click(toggle);
    fireEvent.click(await screen.findByRole('button', { name: '关闭导航' }));
    await waitFor(() => expect(toggle).toHaveFocus());

    fireEvent.click(toggle);
    fireEvent.click(await screen.findByText('工单'));
    await waitFor(() => expect(toggle).toHaveFocus());
    expect(mockPush).toHaveBeenCalledWith('/tickets');
  });

  it('does not install the mobile focus trap at desktop widths', async () => {
    mockPathname = '/tickets';
    mockHasUser = true;
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
    render(
      <MainLayout>
        <button>页面操作</button>
      </MainLayout>
    );
    const toggle = await screen.findByRole('button', { name: /侧边栏/ });
    if (toggle.getAttribute('aria-label') === '展开侧边栏') fireEvent.click(toggle);
    await screen.findByRole('navigation', { name: '主导航' });

    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    document.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
  });
});
