/**
 * 设计系统间距和网格配置
 * 提供统一的间距、网格和布局系统
 *
 * 间距/圆角/阴影/字体刻度不在本文件维护：全部派生自 `token-contract.ts`
 * （数据源 `token-source.json`）。本文件只保留由这些刻度组合出来的
 * 语义化间距、网格、布局、动画和响应式工具。
 */

import { tokenContract } from './token-contract';

// 基础间距单位（基于8px网格系统）
export const spacing = tokenContract.spacing;

// 语义化间距
export const semanticSpacing = {
  // 内边距
  padding: {
    xs: spacing[1], // 4px
    sm: spacing[2], // 8px
    md: spacing[4], // 16px
    lg: spacing[6], // 24px
    xl: spacing[8], // 32px
    '2xl': spacing[12], // 48px
    '3xl': spacing[16], // 64px
  },

  // 外边距
  margin: {
    xs: spacing[1], // 4px
    sm: spacing[2], // 8px
    md: spacing[4], // 16px
    lg: spacing[6], // 24px
    xl: spacing[8], // 32px
    '2xl': spacing[12], // 48px
    '3xl': spacing[16], // 64px
  },

  // 组件间距
  component: {
    xs: spacing[1], // 4px
    sm: spacing[2], // 8px
    md: spacing[3], // 12px
    lg: spacing[4], // 16px
    xl: spacing[6], // 24px
  },

  // 布局间距
  layout: {
    xs: spacing[4], // 16px
    sm: spacing[6], // 24px
    md: spacing[8], // 32px
    lg: spacing[12], // 48px
    xl: spacing[16], // 64px
    '2xl': spacing[24], // 96px
  },
} as const;

// 网格系统
export const grid = {
  // 断点
  breakpoints: {
    xs: '0px',
    sm: '640px',
    md: '768px',
    lg: '1024px',
    xl: '1280px',
    '2xl': '1536px',
  },

  // 容器最大宽度
  container: {
    sm: '640px',
    md: '768px',
    lg: '1024px',
    xl: '1280px',
    '2xl': '1536px',
  },

  // 网格列数
  columns: {
    xs: 4,
    sm: 6,
    md: 8,
    lg: 12,
    xl: 12,
    '2xl': 12,
  },

  // 网格间距
  gap: {
    xs: spacing[2], // 8px
    sm: spacing[3], // 12px
    md: spacing[4], // 16px
    lg: spacing[6], // 24px
    xl: spacing[8], // 32px
  },
} as const;

// 布局系统
export const layout = {
  // 页面布局
  page: {
    maxWidth: '1280px',
    padding: {
      xs: spacing[4], // 16px
      sm: spacing[6], // 24px
      md: spacing[8], // 32px
      lg: spacing[12], // 48px
    },
  },

  // 内容区域
  content: {
    maxWidth: '1024px',
    padding: {
      xs: spacing[4], // 16px
      sm: spacing[6], // 24px
      md: spacing[8], // 32px
    },
  },

  // 侧边栏
  sidebar: {
    width: {
      sm: '240px',
      md: '280px',
      lg: '320px',
    },
    collapsedWidth: '64px',
  },

  // 头部
  header: {
    height: {
      sm: '56px',
      md: '64px',
      lg: '72px',
    },
  },

  // 底部
  footer: {
    height: {
      sm: '120px',
      md: '140px',
      lg: '160px',
    },
  },
} as const;

// 圆角系统
export const borderRadius = tokenContract.radius;

// 阴影系统
export const boxShadow = tokenContract.shadow;

// 字体大小系统
export const fontSize = tokenContract.fontSize;

// 行高系统
export const lineHeight = tokenContract.lineHeight;

// 字重系统
export const fontWeight = tokenContract.fontWeight;

// 动画系统
export const animation = {
  // 持续时间
  duration: {
    fast: '150ms',
    normal: '300ms',
    slow: '500ms',
  },

  // 缓动函数
  easing: {
    linear: 'linear',
    ease: 'ease',
    easeIn: 'ease-in',
    easeOut: 'ease-out',
    easeInOut: 'ease-in-out',
    bounce: 'cubic-bezier(0.68, -0.55, 0.265, 1.55)',
    elastic: 'cubic-bezier(0.175, 0.885, 0.32, 1.275)',
  },

  // 预设动画
  presets: {
    fadeIn: {
      duration: '300ms',
      easing: 'ease-out',
      keyframes: {
        from: { opacity: '0' },
        to: { opacity: '1' },
      },
    },
    slideUp: {
      duration: '300ms',
      easing: 'ease-out',
      keyframes: {
        from: { transform: 'translateY(20px)', opacity: '0' },
        to: { transform: 'translateY(0)', opacity: '1' },
      },
    },
    scaleIn: {
      duration: '200ms',
      easing: 'ease-out',
      keyframes: {
        from: { transform: 'scale(0.95)', opacity: '0' },
        to: { transform: 'scale(1)', opacity: '1' },
      },
    },
  },
} as const;

// 响应式工具函数
export const responsive = {
  // 媒体查询
  mediaQuery: {
    xs: `@media (min-width: ${grid.breakpoints.xs})`,
    sm: `@media (min-width: ${grid.breakpoints.sm})`,
    md: `@media (min-width: ${grid.breakpoints.md})`,
    lg: `@media (min-width: ${grid.breakpoints.lg})`,
    xl: `@media (min-width: ${grid.breakpoints.xl})`,
    '2xl': `@media (min-width: ${grid.breakpoints['2xl']})`,
  },

  // 断点检查
  isBreakpoint: (breakpoint: keyof typeof grid.breakpoints, width: number): boolean => {
    const breakpointValue = parseInt(grid.breakpoints[breakpoint]);
    return width >= breakpointValue;
  },

  // 获取当前断点
  getCurrentBreakpoint: (width: number): keyof typeof grid.breakpoints => {
    const breakpoints = Object.entries(grid.breakpoints)
      .sort(([, a], [, b]) => parseInt(a) - parseInt(b))
      .reverse();

    for (const [breakpoint, value] of breakpoints) {
      if (width >= parseInt(value)) {
        return breakpoint as keyof typeof grid.breakpoints;
      }
    }

    return 'xs';
  },
};

// 导出类型
export type SpacingKey = keyof typeof spacing;
export type SemanticSpacingKey = keyof typeof semanticSpacing;
export type BreakpointKey = keyof typeof grid.breakpoints;
export type BorderRadiusKey = keyof typeof borderRadius;
export type BoxShadowKey = keyof typeof boxShadow;
export type FontSizeKey = keyof typeof fontSize;
export type LineHeightKey = keyof typeof lineHeight;
export type FontWeightKey = keyof typeof fontWeight;

export const spacingSystem = {
  spacing,
  semanticSpacing,
  grid,
  layout,
  borderRadius,
  boxShadow,
  fontSize,
  lineHeight,
  fontWeight,
  animation,
  responsive,
};

export default spacingSystem;
