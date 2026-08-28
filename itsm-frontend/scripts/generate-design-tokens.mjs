#!/usr/bin/env node
/**
 * 生成规范化设计 Token CSS（generate canonical design token CSS）
 *
 * 读取唯一可序列化的 token 源（`src/lib/design-system/token-source.json`），生成
 * `src/styles/generated/design-tokens.css`：一份 `:root`/`.dark` CSS 自定义属性 + 一份
 * Tailwind v4 `@theme inline` 映射区块。产物是构建期生成文件，禁止手工编辑——修改
 * token-source.json 后重新运行本脚本即可。
 *
 * 命名约定（供后续任务/评审对照）：
 *   --color-brand-primary-{50..950}      brand.primary（品牌主色阶，含 light/dark）
 *   --color-brand-charcoal               brand.charcoal（主题无关）
 *   --color-status-{info,success,warning,error}-{50..900}
 *                                         status.*（交互/业务状态色，主题无关的单一色阶，
 *                                         与 Ant Design 的 error 语义对齐，不存在第二个
 *                                         "danger" 状态色来源）
 *   --color-neutral-{50..950}             neutral（中性色阶，含 light/dark）
 *   --color-background-* / --color-surface-* / --color-border-* / --color-text-*
 *                                         functional.*（含 light/dark）
 *   --color-chart-series-{1..6}          chart.series（图表调色板，主题无关）
 *   --color-user-defined-fallback        userDefined.fallback（用户自定义颜色校验失败时的兜底值）
 *   --spacing                            Tailwind v4 间距倍率（见下方"间距"小节的说明）
 *   --radius-{none,sm,md,lg,xl,2xl,3xl,full}      radius（Tailwind `--radius-*` 命名空间）
 *   --shadow-{none,sm,md,lg,xl,2xl,inner}         shadow（Tailwind `--shadow-*` 命名空间）
 *   --text-{xs..9xl}                     fontSize（Tailwind 字号命名空间是 `--text-*`，
 *                                         不是 `--font-size-*`）
 *   --leading-{none,tight,snug,normal,relaxed,loose}
 *                                         lineHeight（Tailwind 行高命名空间是 `--leading-*`）
 *   --font-weight-{thin..black}          fontWeight（与 Tailwind 命名空间同名，未改名）
 *
 * 间距（spacing）：token-source.json 的 spacing 表本身就是 Tailwind v4 默认间距倍率刻度
 * （4px 基准、`calc(var(--spacing) * n)`）的逐项复现——已用脚本核对每一个 key（含分数 key）
 * 均满足 value === 4px * Number(key)。因此这里只生成一个 `--spacing` 倍率变量，不逐个生成
 * `--spacing-0.5`/`--spacing-1.5` 这类按 key 命名的自定义属性：CSS 自定义属性名中的裸 `.`
 * 不是合法的标识符字符（会被解析成"标识符 + 数字 token"两段，导致声明失效），必须转义才能
 * 使用，而 Tailwind v4 的单一倍率变量恰好从设计上绕开了这个问题。
 *
 * 用法:
 *   node scripts/generate-design-tokens.mjs
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);

/** itsm-frontend 根目录。 */
export const FRONTEND_ROOT = path.resolve(path.dirname(scriptPath), '..');

/** 唯一权威 token 源——纯 JSON，Node 无需 TypeScript loader 即可读取。 */
export const SOURCE_PATH = path.join(FRONTEND_ROOT, 'src/lib/design-system/token-source.json');

/** 生成产物路径。 */
export const OUTPUT_PATH = path.join(FRONTEND_ROOT, 'src/styles/generated/design-tokens.css');

/**
 * 生成文件标记。必须出现在文件前 1024 字节内——`scripts/design-token-baseline.mjs` 的基线
 * 扫描器依赖这个约定跳过生成文件（`src/styles/generated/**` 同时也在其排除列表里，双重保险）。
 */
export const GENERATED_FILE_MARKER = '@generated';

export const GENERATED_HEADER = `/* ${GENERATED_FILE_MARKER} by scripts/generate-design-tokens.mjs — do not edit.
 * Source: src/lib/design-system/token-source.json
 * Regenerate with: node scripts/generate-design-tokens.mjs
 */`;

// 显式、稳定的 key 顺序（与 token-contract.ts 里的 TokenContract 类型保持一致），
// 不依赖 JSON/Object.keys() 的隐式排序行为（数字形 key 会被 JS 引擎重排到前面，
// 对 "50".."950" 这类刚好巧合正确，但不应该让生成器的确定性依赖这个隐式行为）。
const COLOR_SCALE_STEPS = ['50', '100', '200', '300', '400', '500', '600', '700', '800', '900'];
const EXTENDED_COLOR_SCALE_STEPS = [...COLOR_SCALE_STEPS, '950'];
const STATUS_NAMES = ['info', 'success', 'warning', 'error'];
const FUNCTIONAL_GROUPS = [
  ['background', ['primary', 'secondary', 'tertiary', 'elevated']],
  ['surface', ['primary', 'secondary', 'tertiary', 'elevated']],
  ['border', ['primary', 'secondary', 'tertiary', 'focus']],
  ['text', ['primary', 'secondary', 'tertiary', 'disabled', 'inverse']],
];
const RADIUS_KEYS = ['none', 'sm', 'md', 'lg', 'xl', '2xl', '3xl', 'full'];
const SHADOW_KEYS = ['none', 'sm', 'md', 'lg', 'xl', '2xl', 'inner'];
const FONT_SIZE_KEYS = ['xs', 'sm', 'base', 'lg', 'xl', '2xl', '3xl', '4xl', '5xl', '6xl', '7xl', '8xl', '9xl'];
const LINE_HEIGHT_KEYS = ['none', 'tight', 'snug', 'normal', 'relaxed', 'loose'];
const FONT_WEIGHT_KEYS = [
  'thin', 'extralight', 'light', 'normal', 'medium', 'semibold', 'bold', 'extrabold', 'black',
];

/** 读取并解析 token-source.json。 */
export function readTokenSource(sourcePath = SOURCE_PATH) {
  const raw = fs.readFileSync(sourcePath, 'utf8');
  return JSON.parse(raw);
}

/**
 * 把 token 契约展开成两组有序 `{ name, value }` 条目：
 *   - root：主题无关的值 + light 主题值（写进 `:root`）
 *   - dark：仅在深色主题下不同的值（写进 `.dark`，只覆盖这些名字）
 * 顺序完全由上面的显式 key 数组决定，与 token-source.json 自身的 key 顺序无关。
 */
export function buildEntries(tokens) {
  const root = [];
  const dark = [];

  // brand
  for (const step of EXTENDED_COLOR_SCALE_STEPS) {
    root.push({ name: `--color-brand-primary-${step}`, value: tokens.brand.primary.light[step] });
    dark.push({ name: `--color-brand-primary-${step}`, value: tokens.brand.primary.dark[step] });
  }
  root.push({ name: '--color-brand-charcoal', value: tokens.brand.charcoal });

  // status（主题无关，单一色阶：不为 dark 单独覆盖）
  for (const statusName of STATUS_NAMES) {
    for (const step of COLOR_SCALE_STEPS) {
      root.push({
        name: `--color-status-${statusName}-${step}`,
        value: tokens.status[statusName][step],
      });
    }
  }

  // neutral
  for (const step of EXTENDED_COLOR_SCALE_STEPS) {
    root.push({ name: `--color-neutral-${step}`, value: tokens.neutral.light[step] });
    dark.push({ name: `--color-neutral-${step}`, value: tokens.neutral.dark[step] });
  }

  // functional
  for (const [group, keys] of FUNCTIONAL_GROUPS) {
    for (const key of keys) {
      root.push({ name: `--color-${group}-${key}`, value: tokens.functional.light[group][key] });
      dark.push({ name: `--color-${group}-${key}`, value: tokens.functional.dark[group][key] });
    }
  }

  // chart（主题无关）
  tokens.chart.series.forEach((value, index) => {
    root.push({ name: `--color-chart-series-${index + 1}`, value });
  });

  // userDefined：只有 fallback 是一个可作为 CSS 变量消费的颜色值；pattern 是校验正则，
  // 只在 TypeScript 侧（token-contract.ts / isUserDefinedColor）使用，不生成 CSS 变量。
  root.push({ name: '--color-user-defined-fallback', value: tokens.userDefined.fallback });

  // spacing：见文件头注释——只生成 Tailwind v4 的单一倍率变量。
  root.push({ name: '--spacing', value: tokens.spacing['1'] });

  // radius
  for (const key of RADIUS_KEYS) {
    root.push({ name: `--radius-${key}`, value: tokens.radius[key] });
  }

  // shadow
  for (const key of SHADOW_KEYS) {
    root.push({ name: `--shadow-${key}`, value: tokens.shadow[key] });
  }

  // fontSize -> Tailwind `--text-*` 命名空间
  for (const key of FONT_SIZE_KEYS) {
    root.push({ name: `--text-${key}`, value: tokens.fontSize[key] });
  }

  // lineHeight -> Tailwind `--leading-*` 命名空间
  for (const key of LINE_HEIGHT_KEYS) {
    root.push({ name: `--leading-${key}`, value: tokens.lineHeight[key] });
  }

  // fontWeight -> Tailwind `--font-weight-*` 命名空间（与契约命名一致，未改名）
  for (const key of FONT_WEIGHT_KEYS) {
    root.push({ name: `--font-weight-${key}`, value: tokens.fontWeight[key] });
  }

  return { root, dark };
}

function renderBlock(selector, entries) {
  const lines = entries.map(({ name, value }) => `  ${name}: ${value};`);
  return [`${selector} {`, ...lines, '}'].join('\n');
}

/**
 * `@theme inline` 逐一"转发" `:root` 里已经定义好的同名变量，而不是把值直接写进
 * `@theme`（那样 Tailwind 会在构建期把值烘焙成静态字面量，`.dark` 的覆盖就再也生效不了）。
 * 参见 Tailwind CSS 官方文档 "Referencing other variables with @theme inline"。
 */
function renderThemeInline(rootEntries) {
  const lines = rootEntries.map(({ name }) => `  ${name}: var(${name});`);
  return ['@theme inline {', ...lines, '}'].join('\n');
}

/** 生成完整 CSS 文本（不落盘，供测试直接断言字符串内容）。 */
export function renderCss(tokens) {
  const { root, dark } = buildEntries(tokens);
  return [
    GENERATED_HEADER,
    '',
    renderBlock(':root', root),
    '',
    renderBlock('.dark', dark),
    '',
    renderThemeInline(root),
    '',
  ].join('\n');
}

/** 生成并写入 `src/styles/generated/design-tokens.css`，返回写入的绝对路径。 */
export function generate({ sourcePath = SOURCE_PATH, outputPath = OUTPUT_PATH } = {}) {
  const tokens = readTokenSource(sourcePath);
  const css = renderCss(tokens);
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  fs.writeFileSync(outputPath, css, 'utf8');
  return outputPath;
}

const invokedDirectly =
  process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invokedDirectly) {
  const outputPath = generate();
  process.stdout.write(`Generated ${path.relative(FRONTEND_ROOT, outputPath)}\n`);
}
