/**
 * 设计 Token 权威契约（Design token contract）
 *
 * `token-source.json` 是全前端唯一的 token 数据源：
 *   - 本文件把它包装成带类型的 TypeScript 导出，供 React / Ant Design 消费；
 *   - `scripts/*.mjs` 构建脚本直接 `JSON.parse` 同一个文件，无需 TypeScript loader；
 *   - `colors.ts` / `spacing.ts` 从本契约派生，不再各自维护字面量。
 *
 * 语义边界（迁移必须按调用点语义判断，禁止按十六进制值全局替换）：
 *   - `brand`       品牌色，只用于品牌标识、主操作、聚焦态；
 *   - `status`      交互/业务状态色，`error` 与 Ant Design API 对齐；不存在第二个 `danger` 状态色源；
 *   - `chart`       图表序列调色板，不是状态语义；
 *   - `userDefined` 用户输入或后端配置的颜色：保留原值，只做格式校验（见 `isUserDefinedColor`）。
 */

import tokenSource from './token-source.json';

/** 状态色/中性色色阶步进。 */
export type ColorScaleStep = 50 | 100 | 200 | 300 | 400 | 500 | 600 | 700 | 800 | 900;

/** 品牌色与中性色额外提供 950 步进。 */
export type ExtendedColorScaleStep = ColorScaleStep | 950;

export type ColorScale = Record<ColorScaleStep, string>;
export type ExtendedColorScale = Record<ExtendedColorScaleStep, string>;

/** 同一角色在 light / dark 两套主题下的取值。 */
export interface ThemedColorScale {
  light: ExtendedColorScale;
  dark: ExtendedColorScale;
}

export interface BrandTokens {
  primary: ThemedColorScale;
  charcoal: string;
}

/** 语义状态色名称。`danger` 只能是动作变体，不是第二个状态色源。 */
export type StatusTokenName = 'info' | 'success' | 'warning' | 'error';

export type StatusTokens = Record<StatusTokenName, ColorScale>;

export interface FunctionalColorSet {
  background: { primary: string; secondary: string; tertiary: string; elevated: string };
  surface: { primary: string; secondary: string; tertiary: string; elevated: string };
  border: { primary: string; secondary: string; tertiary: string; focus: string };
  text: { primary: string; secondary: string; tertiary: string; disabled: string; inverse: string };
}

export interface FunctionalTokens {
  light: FunctionalColorSet;
  dark: FunctionalColorSet;
}

export interface ChartTokens {
  /** 图表序列调色板，按序列索引取用。 */
  series: string[];
}

export interface UserDefinedTokens {
  /** 用户/后端提供颜色的格式白名单（正则源串，供 TS 与 Node 脚本共用）。 */
  pattern: string;
  /** 校验失败时的兜底展示色。 */
  fallback: string;
}

export type SpacingTokenKey =
  | 'px'
  | '0'
  | '0.5'
  | '1'
  | '1.5'
  | '2'
  | '2.5'
  | '3'
  | '3.5'
  | '4'
  | '5'
  | '6'
  | '7'
  | '8'
  | '9'
  | '10'
  | '11'
  | '12'
  | '14'
  | '16'
  | '20'
  | '24'
  | '28'
  | '32'
  | '36'
  | '40'
  | '44'
  | '48'
  | '52'
  | '56'
  | '60'
  | '64'
  | '72'
  | '80'
  | '96';

export type RadiusTokenKey = 'none' | 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl' | 'full';

export type ShadowTokenKey = 'none' | 'sm' | 'md' | 'lg' | 'xl' | '2xl' | 'inner';

export type FontSizeTokenKey =
  | 'xs'
  | 'sm'
  | 'base'
  | 'lg'
  | 'xl'
  | '2xl'
  | '3xl'
  | '4xl'
  | '5xl'
  | '6xl'
  | '7xl'
  | '8xl'
  | '9xl';

export type LineHeightTokenKey = 'none' | 'tight' | 'snug' | 'normal' | 'relaxed' | 'loose';

export type FontWeightTokenKey =
  | 'thin'
  | 'extralight'
  | 'light'
  | 'normal'
  | 'medium'
  | 'semibold'
  | 'bold'
  | 'extrabold'
  | 'black';

export interface TokenContract {
  brand: BrandTokens;
  status: StatusTokens;
  neutral: ThemedColorScale;
  functional: FunctionalTokens;
  chart: ChartTokens;
  userDefined: UserDefinedTokens;
  spacing: Record<SpacingTokenKey, string>;
  radius: Record<RadiusTokenKey, string>;
  shadow: Record<ShadowTokenKey, string>;
  fontSize: Record<FontSizeTokenKey, string>;
  lineHeight: Record<LineHeightTokenKey, string>;
  fontWeight: Record<FontWeightTokenKey, string>;
}

/** 唯一权威 token 契约。 */
export const tokenContract: TokenContract = tokenSource;

/**
 * 语义状态色名称，顺序即契约顺序（info → success → warning → error）。
 * 生成脚本与迁移工具按此顺序输出，保证产物稳定。
 */
export const STATUS_TOKEN_NAMES: StatusTokenName[] = ['info', 'success', 'warning', 'error'];

const userDefinedColorPattern = new RegExp(tokenContract.userDefined.pattern);

/**
 * 校验用户输入 / 后端配置颜色的格式。
 * 通过校验的值原样保留展示，不得被替换为状态色 token。
 */
export function isUserDefinedColor(value: unknown): boolean {
  return typeof value === 'string' && userDefinedColorPattern.test(value);
}

export default tokenContract;
