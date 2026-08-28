/**
 * 设计系统颜色配置
 * 提供统一的颜色变量和主题支持
 *
 * 色值不在本文件维护：全部派生自 `token-contract.ts`（数据源 `token-source.json`），
 * 这样 React/antd、构建期 CSS 生成脚本和 Tailwind 消费的是同一份取值。
 */

import { tokenContract } from './token-contract';

// 基础颜色定义（浅色主题）
export const colors = {
  // 主色调 — KLN Brand Orange
  primary: tokenContract.brand.primary.light,
  charcoal: tokenContract.brand.charcoal,

  // 中性色
  neutral: tokenContract.neutral.light,

  // 语义色（契约中的 status 组）
  semantic: tokenContract.status,

  // 功能色
  functional: tokenContract.functional.light,
} as const;

// 暗色主题颜色
export const darkColors = {
  // 主色调（暗色主题下反转色阶：50 最浅）
  primary: tokenContract.brand.primary.dark,
  charcoal: tokenContract.brand.charcoal,

  // 中性色（暗色主题）
  neutral: tokenContract.neutral.dark,

  // 功能色（暗色主题）
  functional: tokenContract.functional.dark,
} as const;

// 颜色使用指南
export const colorUsage = {
  // 主色调使用
  primary: {
    description: '品牌主色，用于主要操作按钮、链接、选中状态等',
    usage: ['主要按钮', '链接', '选中状态', '进度条', '品牌标识'],
    avoid: ['大面积背景', '文本颜色', '边框颜色'],
  },

  // 中性色使用
  neutral: {
    description: '用于文本、边框、背景等基础元素',
    usage: ['文本颜色', '边框颜色', '背景颜色', '分割线'],
    avoid: ['强调元素', '交互状态'],
  },

  // 语义色使用
  semantic: {
    success: {
      description: '成功状态，用于成功提示、完成状态等',
      usage: ['成功提示', '完成状态', '确认按钮', '进度完成'],
      avoid: ['错误状态', '警告状态'],
    },
    warning: {
      description: '警告状态，用于警告提示、注意事项等',
      usage: ['警告提示', '注意事项', '待处理状态'],
      avoid: ['错误状态', '成功状态'],
    },
    error: {
      description: '错误状态，用于错误提示、危险操作等',
      usage: ['错误提示', '危险操作', '删除按钮', '验证失败'],
      avoid: ['成功状态', '警告状态'],
    },
    info: {
      description: '信息状态，用于信息提示、帮助说明等',
      usage: ['信息提示', '帮助说明', '中性操作'],
      avoid: ['强调操作', '状态指示'],
    },
  },
} as const;

// 颜色对比度检查工具
export const contrastChecker = {
  // 计算颜色对比度
  getContrastRatio(color1: string, color2: string): number {
    const getLuminance = (color: string): number => {
      const rgb = this.hexToRgb(color);
      if (!rgb) return 0;

      const { r, g, b } = rgb;
      const [rs, gs, bs] = [r, g, b].map(c => {
        c = c / 255;
        return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
      });

      return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
    };

    const l1 = getLuminance(color1);
    const l2 = getLuminance(color2);
    const lighter = Math.max(l1, l2);
    const darker = Math.min(l1, l2);

    return (lighter + 0.05) / (darker + 0.05);
  },

  // 检查对比度是否满足WCAG标准
  checkWCAGCompliance(
    color1: string,
    color2: string
  ): {
    AA: boolean;
    AAA: boolean;
    ratio: number;
  } {
    const ratio = this.getContrastRatio(color1, color2);
    return {
      AA: ratio >= 4.5, // WCAG AA标准
      AAA: ratio >= 7, // WCAG AAA标准
      ratio,
    };
  },

  // 十六进制颜色转RGB
  hexToRgb(hex: string): { r: number; g: number; b: number } | null {
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    return result
      ? {
          r: parseInt(result[1], 16),
          g: parseInt(result[2], 16),
          b: parseInt(result[3], 16),
        }
      : null;
  },

  // 获取推荐的颜色组合
  getRecommendedCombinations(): Array<{
    background: string;
    text: string;
    ratio: number;
    compliance: 'AA' | 'AAA';
  }> {
    const combinations = [
      { background: colors.functional.background.primary, text: colors.functional.text.primary },
      { background: colors.functional.background.secondary, text: colors.functional.text.primary },
      { background: colors.primary[500], text: colors.functional.text.inverse },
      { background: colors.semantic.success[500], text: colors.functional.text.inverse },
      { background: colors.semantic.warning[500], text: colors.functional.text.inverse },
      { background: colors.semantic.error[500], text: colors.functional.text.inverse },
    ];

    return combinations.map(combo => {
      const ratio = this.getContrastRatio(combo.background, combo.text);
      return {
        ...combo,
        ratio,
        compliance: ratio >= 7 ? 'AAA' : ratio >= 4.5 ? 'AA' : 'AA',
      };
    });
  },
};

// 主题配置
export const themeConfig = {
  light: {
    colors: colors,
    name: 'light',
    description: '浅色主题，适合日间使用',
  },
  dark: {
    colors: darkColors,
    name: 'dark',
    description: '深色主题，适合夜间使用',
  },
} as const;

// 导出类型
export type ColorTheme = keyof typeof themeConfig;
export type ColorPalette = typeof colors;
export type SemanticColor = keyof typeof colors.semantic;
export type FunctionalColor = keyof typeof colors.functional;

export default colors;
