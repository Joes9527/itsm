'use client';

import type { ReactNode } from 'react';
import React, { createContext, useContext, useState, useEffect } from 'react';
import { theme } from 'antd';
import { parseThemeMode, resolveIsDark, THEME_STORAGE_KEY } from './theme-preference';
import type { ThemeMode } from './theme-preference';
export type { ThemeMode } from './theme-preference';
import tokens from '@/design-system/theme-tokens.json';

// 主题类型

// 主题上下文类型
interface ThemeContextType {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
  isDark: boolean;
  toggleTheme: () => void;
}

// 创建主题上下文
const ThemeContext = createContext<ThemeContextType | undefined>(undefined);

// 主题提供者属性
interface ThemeProviderProps {
  children: ReactNode;
  defaultMode?: ThemeMode;
  storageKey?: string;
}

// 主题提供者组件
export const ThemeProvider: React.FC<ThemeProviderProps> = ({
  children,
  defaultMode = 'light',
  storageKey = THEME_STORAGE_KEY,
}) => {
  const [mode, setMode] = useState<ThemeMode>(defaultMode);
  const [systemDark, setSystemDark] = useState(false);
  const [restored, setRestored] = useState(false);

  useEffect(() => {
    let nextMode = defaultMode;
    try {
      const stored = window.localStorage.getItem(storageKey);
      nextMode = stored === null ? defaultMode : parseThemeMode(stored);
    } catch {
      /* Storage denial must not disable in-session switching. */
    }
    const media = window.matchMedia?.('(prefers-color-scheme: dark)');
    const updateSystem = () => setSystemDark(media?.matches ?? false);
    updateSystem();
    media?.addEventListener('change', updateSystem);
    setMode(nextMode);
    setRestored(true);
    return () => media?.removeEventListener('change', updateSystem);
  }, [defaultMode, storageKey]);

  useEffect(() => {
    if (!restored) return;
    try {
      window.localStorage.setItem(storageKey, mode);
    } catch {
      /* Session-only preference. */
    }
  }, [mode, restored, storageKey]);

  const isDark = resolveIsDark(mode, systemDark);
  const contextValue: ThemeContextType = {
    mode,
    setMode,
    isDark,
    toggleTheme: () =>
      setMode(previous => (resolveIsDark(previous, systemDark) ? 'light' : 'dark')),
  };

  // Identical server/client structure until restoration, styled by pre-paint root variables.
  return (
    <ThemeContext.Provider value={contextValue}>
      {restored ? (
        children
      ) : (
        <div
          aria-busy='true'
          aria-label='正在恢复主题'
          style={{ minHeight: '100vh', background: 'var(--color-bg-secondary)' }}
        />
      )}
    </ThemeContext.Provider>
  );
};

// 使用主题钩子
export const useTheme = (): ThemeContextType => {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error('useTheme must be used within a ThemeProvider');
  }
  return context;
};

// Ant Design 主题配置
export const getAntdTheme = (isDark: boolean) => {
  const palette = tokens.themes[isDark ? 'dark' : 'light'];
  const primary = tokens.brand.palette[500];
  const foreground = tokens.common['--color-primary-foreground'];
  const surface = palette['--color-bg-primary'];
  const raised = palette['--color-bg-tertiary'];
  const text = palette['--color-text-primary'];
  const selected = palette['--color-selected-bg'];
  const selectedText = palette['--color-selected-text'];
  const control = {
    controlHeight: tokens.sizes.button,
    controlHeightSM: tokens.sizes.buttonSmall,
    borderRadius: tokens.sizes.buttonRadius,
    fontSize: parseInt(tokens.typography.fontSize.base, 10),
  };
  return {
    algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      ...control,
      colorPrimary: primary,
      colorPrimaryHover: tokens.common['--color-primary-hover'],
      colorPrimaryActive: primary,
      colorPrimaryBg: selected,
      colorPrimaryBgHover: selected,
      colorPrimaryText: selectedText,
      colorPrimaryTextHover: text,
      colorTextLightSolid: foreground,
      colorSuccess: tokens.common['--color-success'],
      colorWarning: tokens.common['--color-warning'],
      colorError: tokens.common['--color-error'],
      colorInfo: tokens.common['--color-info'],
      colorBgContainer: surface,
      colorBgLayout: palette['--color-bg-secondary'],
      colorBgElevated: surface,
      colorText: text,
      colorTextSecondary: palette['--color-text-secondary'],
      colorTextTertiary: palette['--color-text-secondary'],
      colorTextQuaternary: palette['--color-text-disabled'],
      colorTextDisabled: palette['--color-text-disabled'],
      colorBorder: palette['--color-border'],
      colorBorderSecondary: palette['--color-border'],
      colorLink: selectedText,
      colorLinkHover: text,
      colorLinkActive: text,
      colorFillAlter: raised,
      borderRadiusLG: tokens.sizes.cardRadius,
      fontFamily: tokens.typography.fontFamily,
      fontSizeSM: parseInt(tokens.typography.fontSize.xs, 10),
      fontSizeLG: parseInt(tokens.typography.fontSize.lg, 10),
      fontSizeHeading1: parseInt(tokens.typography.pageTitle, 10),
      fontSizeHeading2: parseInt(tokens.typography.pageTitle, 10),
      fontSizeHeading3: parseInt(tokens.typography.cardTitle, 10),
      fontWeightStrong: tokens.typography.strong,
      lineHeight: 1.5,
      controlOutline: selected,
      controlOutlineWidth: 2,
    },
    components: {
      Button: {
        ...control,
        primaryColor: foreground,
        primaryShadow: 'none',
        defaultShadow: 'none',
        fontWeight: 400,
      },
      Input: control,
      Select: {
        ...control,
        optionSelectedBg: selected,
        optionSelectedColor: selectedText,
        optionActiveBg: raised,
      },
      DatePicker: control,
      Card: {
        borderRadiusLG: tokens.sizes.cardRadius,
        headerFontSize: parseInt(tokens.typography.cardTitle, 10),
        bodyPadding: tokens.sizes.cardPadding,
        headerBg: surface,
        boxShadow: 'none',
      },
      Table: {
        headerBg: raised,
        headerColor: palette['--color-text-secondary'],
        rowHoverBg: raised,
        rowSelectedBg: selected,
        rowSelectedHoverBg: selected,
        borderColor: palette['--color-border'],
        cellFontSize: control.fontSize,
        cellPaddingBlock: 14,
      },
      Dropdown: {
        colorBgElevated: surface,
        controlItemBgHover: raised,
        controlItemBgActive: selected,
        colorText: text,
      },
      Modal: { contentBg: surface, headerBg: surface, titleColor: text },
      Drawer: { colorBgElevated: surface, colorText: text },
      Typography: { titleMarginTop: 0, titleMarginBottom: 12 },
      Menu: {
        itemBg: surface,
        subMenuItemBg: surface,
        itemSelectedBg: selected,
        itemSelectedColor: selectedText,
        itemHoverBg: raised,
        itemColor: text,
      },
    },
  };
};

// 主题配置组件属性
interface ThemeConfigProps {
  children: ReactNode;
}

// 主题配置组件：仅作为 theme → antd 主题的应用边界占位，
// 真正注入 antd ConfigProvider 的逻辑由 AntdProvider 完成。
// 这里必须透传 children，否则会丢掉 layout 中后续的整棵子树导致页面空白。
export const ThemeConfig: React.FC<ThemeConfigProps> = ({ children }) => {
  return <>{children}</>;
};

// CSS 变量生成器
export const generateCSSVariables = (isDark: boolean): Record<string, string> => {
  const prefix = (name: string, values: Record<string, string>) =>
    Object.fromEntries(Object.entries(values).map(([key, value]) => [`--${name}-${key}`, value]));
  return {
    ...tokens.common,
    ...prefix('color-primary', tokens.brand.palette),
    ...prefix('font-size', tokens.typography.fontSize),
    '--font-family-base': tokens.typography.fontFamily,
    '--font-size-page-title': tokens.typography.pageTitle,
    '--font-size-card-title': tokens.typography.cardTitle,
    '--font-size-helper': tokens.typography.helper,
    '--header-height': `${tokens.sizes.header}px`,
    '--sidebar-width': `${tokens.sizes.sidebar}px`,
    '--sidebar-collapsed-width': `${tokens.sizes.sidebarCollapsed}px`,
    '--control-height': `${tokens.sizes.button}px`,
    '--control-height-sm': `${tokens.sizes.buttonSmall}px`,
    '--card-padding': `${tokens.sizes.cardPadding}px`,
    ...Object.fromEntries(
      Object.entries(tokens.aliases).map(([key, value]) => [key, `var(${value})`])
    ),
    ...tokens.themes[isDark ? 'dark' : 'light'],
  };
};

// 应用 CSS 变量到文档
export const applyCSSVariables = (isDark: boolean) => {
  const variables = generateCSSVariables(isDark);
  const root = document.documentElement;

  Object.entries(variables).forEach(([key, value]) => {
    root.style.setProperty(key, value);
  });

  // 添加主题类名
  root.classList.toggle('dark', isDark);
  root.classList.toggle('light', !isDark);
  root.style.colorScheme = isDark ? 'dark' : 'light';
};

export default ThemeProvider;
