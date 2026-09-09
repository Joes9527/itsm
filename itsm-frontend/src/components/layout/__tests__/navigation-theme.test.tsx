import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { Header } from '@/components/layout/header/Header';
import { Sidebar } from '@/components/layout/sidebar/Sidebar';
import { App } from 'antd';

jest.mock('next/navigation', () => ({
  usePathname: () => '/portal',
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: null, hasPermission: () => false, isAdmin: () => false }),
}));
jest.mock('@/lib/design-system/theme', () => ({
  useTheme: () => ({ isDark: false, toggleTheme: jest.fn() }),
}));
jest.mock('@/lib/i18n', () => ({
  useI18n: () => ({ language: 'zh-CN', changeLanguage: jest.fn() }),
}));
jest.mock('@/lib/services/notification-ws', () => ({
  notificationWS: {
    connect: jest.fn(),
    disconnect: jest.fn(),
    onNotification: jest.fn(() => jest.fn()),
  },
}));

describe('navigation shell', () => {
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
});
