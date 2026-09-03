#!/usr/bin/env node
/**
 * 生成规范化设计 Token CSS（generate canonical design token CSS）
 *
 * 读取唯一可序列化的 token 源（`src/lib/design-system/token-source.json`），生成
 * `src/styles/generated/design-tokens.css`：一份 `:root`/`.dark` CSS 自定义属性 + 一份
 * Tailwind v4 `@theme inline` 映射区块。产物是构建期生成文件，禁止手工编辑——修改
 * token-source.json 后重新运行本脚本即可。
 *
 * ## 两套变量名：raw 与 theme（修复 `--x: var(--x)` 自引用死循环）
 *
 * 每个 token 都有两个名字：
 *   - `rawName`（`--token-*` 前缀）——实际持有取值，写进 `:root`/`.dark`。
 *   - `themeName`（如 `--color-brand-primary-500`、`--radius-sm`、`--text-xs`）——
 *     Tailwind v4 的主题命名空间名字，只出现在 `@theme inline` 里，值是
 *     `var(--token-*)`，即转发到对应的 rawName。
 *
 * 如果 rawName 与 themeName 相同（早期实现就是这么写的：`--color-brand-primary-500:
 * var(--color-brand-primary-500);`），Tailwind 在生产构建里对 `@theme inline` 的输出是
 * 一条**未加 @layer** 的 `:host,:root{...}` 规则，与本文件手写的 `:root{...}` 同为
 * 0-1-0 特异度，按源码顺序在压缩产物里排在最后，会整体覆盖掉手写的那份声明——覆盖后的
 * 声明是 `--x: var(--x)`，对同一属性的自引用，按 CSS Custom Properties 规范判定为处于
 * 依赖环（cycle），计算值落到 guaranteed-invalid，等于这个变量在生产环境完全失效。
 * `next dev` 因为保留 `@layer`、未加 layer 的规则天然优先级更高，不会暴露这个问题——
 * 必须对着 `next build` 的产物验证，不能只看 `npm run dev`/单测。
 * 用两个不同的名字彻底避免这个自引用环。
 *
 * ## rem 换算：仅 --radius-*、--text-*、--spacing 这三组
 *
 * `tailwind.config.mjs` 从未被任何 `@config` 指令加载（全仓库 `grep -rn "@config"` 零命中），
 * 所以在本脚本存在之前，`rounded-*`/`text-*`/`p-*`/`m-*`/`gap-*`/`w-*` 等工具类实际上一直在用
 * Tailwind v4 的内置默认刻度（rem 单位）。一旦上面的自引用死循环修复、这里生成的
 * `--radius-*`/`--text-*`/`--spacing` 真正在层叠中胜出，如果直接照搬 token-source.json 里的
 * px 字面量（`radius.sm = "2px"`、`fontSize.xs = "12px"`、`spacing["1"] = "4px"`），会把这三个
 * Tailwind 主题命名空间从 rem 悄悄改成 px——数值上 100% 缩放下不可见，但用户放大浏览器默认
 * 字号时不再跟着缩放，是一次可访问性倒退，spec 的 Non-Goals 里没有为这类改动开口子。
 *
 * 因此只在**本脚本生成 CSS 的这一层**把 `--radius-*`/`--text-*`/`--spacing` 的值从 px 换算成
 * 16px 基准下的等值 rem 字符串（例如 `4px` → `0.25rem`），不改 token-source.json 本身——
 * `theme.tsx` 的 `getAntdTheme()` 仍然对同一批 px 字符串做 `parseInt(value, 10)` 喂给
 * antd（antd 要的是裸整数 px），把源头改成 rem 字符串会让 `parseInt("0.25rem", 10)` 变成
 * `0`，静默破坏 antd 的取值。`--shadow-*` 是多段 `box-shadow` 字符串（offset/blur/spread
 * 混合，没有单一长度可换算），且与 Tailwind v4 自己的默认阴影刻度一样用 px，不做换算；
 * `--font-weight-*`/`--leading-*` 本就是无单位数值，同样不涉及换算。
 *
 * ## 命名约定（供后续任务/评审对照，均为 themeName；对应 rawName 见 toRawName()）：
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
 *   --spacing                            Tailwind v4 间距倍率，已转换为 rem（见上方说明）
 *   --radius-{none,sm,md,lg,xl,2xl,3xl,full}      radius，已转换为 rem
 *   --shadow-{none,sm,md,lg,xl,2xl,inner}         shadow（Tailwind `--shadow-*` 命名空间，px，不换算）
 *   --text-{xs..9xl}                     fontSize，已转换为 rem（Tailwind 字号命名空间是
 *                                         `--text-*`，不是 `--font-size-*`）
 *   --leading-{none,tight,snug,normal,relaxed,loose}
 *                                         lineHeight（Tailwind 行高命名空间是 `--leading-*`，无单位）
 *   --font-weight-{thin..black}          fontWeight（与 Tailwind 命名空间同名，未改名，无单位）
 *
 * 间距（spacing）：token-source.json 的 spacing 表本身就是 Tailwind v4 默认间距倍率刻度
 * （4px/0.25rem 基准、`calc(var(--spacing) * n)`）的逐项复现——已核对每一个 key（含分数 key）
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

/** raw 变量名前缀——见文件头注释「两套变量名」。 */
export const RAW_VAR_PREFIX = '--token-';

/** themeName（如 `--color-brand-primary-500`）→ rawName（如 `--token-color-brand-primary-500`）。 */
export function toRawName(themeName) {
  if (!themeName.startsWith('--')) {
    throw new Error(`toRawName: expected a "--"-prefixed CSS custom property name, got ${JSON.stringify(themeName)}`);
  }
  return `${RAW_VAR_PREFIX}${themeName.slice(2)}`;
}

/**
 * 把 `"<number>px"` 换算成 16px 基准下的等值 rem 字符串，如 `"4px"` → `"0.25rem"`。
 * 只用于 --radius-*、--text-*、--spacing（见文件头注释「rem 换算」），不改 token-source.json。
 */
export function pxToRem(pxValue, base = 16) {
  const match = /^(-?\d*\.?\d+)px$/.exec(pxValue);
  if (!match) {
    throw new Error(`pxToRem: expected a "<number>px" string, got ${JSON.stringify(pxValue)}`);
  }
  const rem = Number(match[1]) / base;
  // Avoid float noise (e.g. 0.1 + 0.2) while still trimming trailing zeros.
  const formatted = Number(rem.toFixed(6)).toString();
  return `${formatted}rem`;
}

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

/** 追加一条 `{ themeName, rawName, value }` 条目。 */
function push(list, themeName, value) {
  list.push({ themeName, rawName: toRawName(themeName), value });
}

/**
 * 把 token 契约展开成两组有序 `{ themeName, rawName, value }` 条目：
 *   - root：主题无关的值 + light 主题值（写进 `:root`，用 rawName）
 *   - dark：仅在深色主题下不同的值（写进 `.dark`，只覆盖这些名字，用 rawName）
 * 顺序完全由上面的显式 key 数组决定，与 token-source.json 自身的 key 顺序无关。
 * `--radius-*`/`--text-*`/`--spacing` 的 value 在这里就已经是 rem 字符串（见 pxToRem）。
 */
export function buildEntries(tokens) {
  const root = [];
  const dark = [];

  // brand
  for (const step of EXTENDED_COLOR_SCALE_STEPS) {
    push(root, `--color-brand-primary-${step}`, tokens.brand.primary.light[step]);
    push(dark, `--color-brand-primary-${step}`, tokens.brand.primary.dark[step]);
  }
  push(root, '--color-brand-charcoal', tokens.brand.charcoal);

  // status（主题无关，单一色阶：不为 dark 单独覆盖）
  for (const statusName of STATUS_NAMES) {
    for (const step of COLOR_SCALE_STEPS) {
      push(root, `--color-status-${statusName}-${step}`, tokens.status[statusName][step]);
    }
  }

  // neutral
  for (const step of EXTENDED_COLOR_SCALE_STEPS) {
    push(root, `--color-neutral-${step}`, tokens.neutral.light[step]);
    push(dark, `--color-neutral-${step}`, tokens.neutral.dark[step]);
  }

  // functional
  for (const [group, keys] of FUNCTIONAL_GROUPS) {
    for (const key of keys) {
      push(root, `--color-${group}-${key}`, tokens.functional.light[group][key]);
      push(dark, `--color-${group}-${key}`, tokens.functional.dark[group][key]);
    }
  }

  // chart（主题无关）
  tokens.chart.series.forEach((value, index) => {
    push(root, `--color-chart-series-${index + 1}`, value);
  });

  // userDefined：只有 fallback 是一个可作为 CSS 变量消费的颜色值；pattern 是校验正则，
  // 只在 TypeScript 侧（token-contract.ts / isUserDefinedColor）使用，不生成 CSS 变量。
  push(root, '--color-user-defined-fallback', tokens.userDefined.fallback);

  // spacing：见文件头注释——只生成 Tailwind v4 的单一倍率变量，换算成 rem。
  push(root, '--spacing', pxToRem(tokens.spacing['1']));

  // radius：换算成 rem。
  for (const key of RADIUS_KEYS) {
    push(root, `--radius-${key}`, pxToRem(tokens.radius[key]));
  }

  // shadow：多段 box-shadow 字符串，不做单位换算（与 Tailwind v4 默认阴影刻度一样用 px）。
  for (const key of SHADOW_KEYS) {
    push(root, `--shadow-${key}`, tokens.shadow[key]);
  }

  // fontSize -> Tailwind `--text-*` 命名空间，换算成 rem。
  for (const key of FONT_SIZE_KEYS) {
    push(root, `--text-${key}`, pxToRem(tokens.fontSize[key]));
  }

  // lineHeight -> Tailwind `--leading-*` 命名空间（无单位，不换算）。
  for (const key of LINE_HEIGHT_KEYS) {
    push(root, `--leading-${key}`, tokens.lineHeight[key]);
  }

  // fontWeight -> Tailwind `--font-weight-*` 命名空间（与契约命名一致，未改名；无单位，不换算）。
  for (const key of FONT_WEIGHT_KEYS) {
    push(root, `--font-weight-${key}`, tokens.fontWeight[key]);
  }

  return { root, dark };
}

/**
 * 每条 entry 在块里生成两行：先是持有真实取值的 rawName，再是转发到 rawName 的
 * themeName（`--X: var(--token-X);`，两个不同的名字，不是自引用）。
 *
 * 为什么 themeName 也要在这里手写一遍，而不是只指望 `@theme inline` 那份转发去发布它：
 * 经生产构建实测（`npm run build` 产物里逐个 grep），`@theme inline` 对一个 themeName
 * 是否会在编译产物里以字面量声明的形式发布到 `:host,:root`，取决于扫描到的源码里有没有
 * 用到由它生成的工具类——比如仓库里从未出现过 `rounded-sm`，编译产物里就完全找不到任何
 * 地方声明 `--radius-sm`，已经在用的 `.rounded-md{border-radius:var(--radius-md,.375rem)}`
 * 这类工具类里，`var()` 的第二个参数是 Tailwind **自己的默认值**（`.375rem`，不是这里
 * 换算出的 `0.25rem`）——因为 `--radius-md` 全局都没有被声明，只能落回这个内置 fallback。
 * 也就是说不能假设 `@theme inline` 会无条件把每个 themeName 都发布成一条真正生效的 CSS
 * 声明；必须由本文件自己在 `:root`/`.dark` 里把 themeName 也显式声明一遍，这样不管
 * Tailwind 内部要不要再发布一份，themeName 在层叠里都保证有一份指向 rawName 的、正确的
 * 声明来源。
 */
function renderBlock(selector, entries) {
  const lines = entries.flatMap(({ rawName, themeName, value }) => [
    `  ${rawName}: ${value};`,
    `  ${themeName}: var(${rawName});`,
  ]);
  return [`${selector} {`, ...lines, '}'].join('\n');
}

/**
 * `@theme inline` 仍然需要单独生成一份：这是 Tailwind v4 唯一用来"登记"一个 CSS 变量属于
 * 哪个主题命名空间（`--color-*`/`--radius-*`/`--text-*`/...）的入口，只有登记过的名字，
 * Tailwind 才会在扫描到匹配的工具类（如业务代码写了 `bg-brand-primary-500`）时按需生成对应
 * 的工具类规则。这里同样把 themeName 转发到 rawName，不直接把值写进 `@theme`（那样 Tailwind
 * 会在构建期把值烘焙成静态字面量，`.dark` 的覆盖就再也生效不了），也不能让 themeName 转发到
 * 它自己（那是一个自引用环，在生产构建的压缩产物里会让这个变量在层叠中判定为
 * guaranteed-invalid——见文件头注释）。这份声明与上面 renderBlock() 里手写的 themeName
 * 转发是同一个表达式，出现两次是有意为之：一份保证 CSS 变量一定生效，另一份保证 Tailwind
 * 认得这个名字、会按需生成工具类。参见 Tailwind CSS 官方文档
 * "Referencing other variables with @theme inline"。
 */
function renderThemeInline(rootEntries) {
  const lines = rootEntries.map(({ themeName, rawName }) => `  ${themeName}: var(${rawName});`);
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
