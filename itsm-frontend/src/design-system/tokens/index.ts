import tokens from '../theme-tokens.json';

/**
 * Design System Tokens
 * Centralized design constants for consistent styling across the application
 */

export const colors = {
  /** 品牌主色（与 antd colorPrimary / tailwind primary 对齐） */
  primary: tokens.brand.palette['500'],
  /** 强调色（与主色一致，保留兼容旧引用） */
  accent: tokens.brand.palette['500'],
  /** 深色标题/墨色（原误命名为 primary 的 #0f172a） */
  ink: 'var(--color-text-primary)',
  success: '#10b981',
  warning: '#f59e0b',
  danger: '#ef4444',
  surface: 'var(--color-bg-primary)',
  border: 'var(--color-border)',
  text: 'var(--color-text-primary)',
  textMuted: 'var(--color-text-secondary)',
  bgSubtle: 'var(--color-bg-tertiary)',
} as const;

export const shadows = {
  dropdown: '0 10px 40px -10px rgba(0,0,0,0.15)',
  card: '0 1px 3px 0 rgba(0, 0, 0, 0.1)',
  cardHover: '0 4px 6px -1px rgba(0, 0, 0, 0.1)',
  glow: (color: string) => `0 0 20px ${color}20`,
} as const;

export const radius = {
  sm: `${tokens.sizes.buttonRadius}px`,
  md: `${tokens.sizes.cardRadius}px`,
  lg: '16px',
  full: '9999px',
} as const;

export const spacing = {
  xs: '4px',
  sm: '8px',
  md: '16px',
  lg: '24px',
  xl: '32px',
} as const;

export const typography = {
  fontFamily: {
    sans: tokens.typography.fontFamily,
    mono: 'JetBrains Mono, Menlo, monospace',
  },
  fontSize: tokens.typography.fontSize,
} as const;

export const transitions = {
  fast: '150ms ease',
  normal: '300ms ease',
  slow: '500ms ease',
} as const;

// Legacy DESIGN object for backward compatibility with existing components
export const DESIGN = {
  colors,
  shadows,
  radius,
} as const;

export type ColorKey = keyof typeof colors;
export type ShadowKey = keyof typeof shadows;
export type RadiusKey = keyof typeof radius;
