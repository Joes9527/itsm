/**
 * Tests for src/lib/providers
 */

jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn(), post: jest.fn(), put: jest.fn(), delete: jest.fn(), patch: jest.fn() },
}));

jest.mock('@ant-design/nextjs-registry', () => ({
  AntdRegistry: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

jest.mock('antd', () => ({
  ConfigProvider: jest.fn(({ children }: { children: React.ReactNode }) => <>{children}</>),
  App: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

jest.mock('antd/locale/zh_CN', () => ({}));

jest.mock('@/lib/design-system/theme', () => ({
  useTheme: jest.fn(() => ({ isDark: false })),
  getAntdTheme: jest.fn(() => ({})),
}));

jest.mock('@tanstack/react-query', () => ({
  QueryClient: jest.fn(() => ({})),
  QueryClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

jest.mock('@tanstack/react-query-devtools', () => ({
  ReactQueryDevtools: () => null,
}));

import React from 'react';
import { render } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { useTheme, getAntdTheme } from '@/lib/design-system/theme';
import { AntdProvider } from '../AntdProvider';
import { QueryProvider } from '../QueryProvider';

describe('AntdProvider', () => {
  it('keeps the theme config stable until the active theme changes', () => {
    const light = { token: { colorPrimary: '#123456' } };
    const dark = { token: { colorPrimary: '#abcdef' } };
    (useTheme as jest.Mock).mockReturnValue({ isDark: false });
    (getAntdTheme as jest.Mock).mockImplementation(isDark => isDark ? dark : light);
    const currentConfig = () => (ConfigProvider as unknown as jest.Mock).mock.calls.at(-1)[0].theme;
    const { rerender } = render(<AntdProvider><div>First</div></AntdProvider>);
    expect(currentConfig()).toBe(light);
    rerender(<AntdProvider><div>Changed child</div></AntdProvider>);
    expect(currentConfig()).toBe(light);
    expect(getAntdTheme).toHaveBeenCalledTimes(1);
    (useTheme as jest.Mock).mockReturnValue({ isDark: true });
    rerender(<AntdProvider><div>Dark</div></AntdProvider>);
    expect(currentConfig()).toBe(dark);
    expect(getAntdTheme).toHaveBeenCalledTimes(2);
  });

  it('renders children', () => {
    const { getByText } = render(
      <AntdProvider>
        <div>Hello</div>
      </AntdProvider>
    );
    expect(getByText('Hello')).toBeDefined();
  });
});

describe('QueryProvider', () => {
  it('renders children', () => {
    const { getByText } = render(
      <QueryProvider>
        <div>World</div>
      </QueryProvider>
    );
    expect(getByText('World')).toBeDefined();
  });
});
