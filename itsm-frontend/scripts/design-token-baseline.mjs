#!/usr/bin/env node
/**
 * 设计 Token 存量基线审计（design token baseline audit）
 *
 * 产出一份可复现、可进 CI 评审的 JSON：任意值字号类名（`text-[Npx]`）和裸十六进制
 * 颜色字面量各自的总数、按值分布、按文件分布和行号，并把每个颜色值按 token 契约
 * 做**初步**归类（brand / status / chart / neutral / surface / unclassified）。
 *
 * 归类只表示"该值出现在契约的哪一组"，是候选提示，不是迁移结论——真正的迁移必须
 * 按调用点语义人工确认（见设计文档「新增:语义状态色 Token」）。
 *
 * 用法:
 *   node scripts/design-token-baseline.mjs          # 摘要 JSON（含扫描范围）
 *   node scripts/design-token-baseline.mjs --full   # 附带逐处行号明细
 *   npm run tokens:baseline
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);

/** itsm-frontend 根目录，即默认扫描根。 */
export const FRONTEND_ROOT = path.resolve(path.dirname(scriptPath), '..');

/** 默认纳入扫描的文件（相对 rootDir 的 glob）。 */
export const DEFAULT_INCLUDE = [
  'src/**/*.ts',
  'src/**/*.tsx',
  'src/**/*.css',
  'src/**/*.mjs',
  'postcss.config.mjs',
  'tailwind.config.mjs',
];

/**
 * 默认排除项（相对 rootDir 的 glob）。
 * 包含三类：依赖与构建产物、生成目录、token 契约自身——契约文件是字面量的合法归宿，
 * 计入基线会把"单一数据源"错误地统计成存量债务。
 */
export const DEFAULT_EXCLUDE = [
  '.jest-cache/**',
  '.next/**',
  '.swc/**',
  'build/**',
  'coverage/**',
  'dist/**',
  'node_modules/**',
  'out/**',
  'playwright-report/**',
  'public/**',
  'src/lib/design-system/colors.ts',
  'src/lib/design-system/spacing.ts',
  'src/lib/design-system/token-contract.ts',
  'src/lib/design-system/token-source.json',
  'src/styles/generated/**',
  'test-results/**',
];

/**
 * 生成文件标记。文件头部出现该标记即整体跳过，
 * 生成脚本（scripts/generate-design-tokens.mjs）必须在产物首行写入它。
 */
export const GENERATED_FILE_MARKER = '@generated';

/** 只检查文件开头这么多字节来判定是否为生成文件。 */
const GENERATED_MARKER_SCAN_BYTES = 1024;

/** 任意值字号类名：`text-[12px]` / `text-[0.75rem]`。`text-[#fff]` 这类颜色不匹配。 */
const ARBITRARY_FONT_SIZE_PATTERN = /text-\[\d+(?:\.\d+)?(?:px|rem|em)\]/g;

/** 裸十六进制颜色字面量：3 / 4 / 6 / 8 位。 */
const RAW_COLOR_PATTERN = /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b/g;

/** 初步归类的分组名，顺序固定以保证输出稳定。 */
export const COLOR_CATEGORIES = ['brand', 'status', 'chart', 'neutral', 'surface', 'unclassified'];

/** 把 glob 转成锚定的正则，支持 `**`、`*`、`?` 和 `{a,b}`。 */
function globToRegExp(glob) {
  let source = '';
  for (let i = 0; i < glob.length; i += 1) {
    const char = glob[i];
    if (char === '*') {
      if (glob[i + 1] === '*') {
        // `**/` 允许匹配零个或多个目录层级
        if (glob[i + 2] === '/') {
          source += '(?:[^/]*\\/)*';
          i += 2;
        } else {
          source += '.*';
          i += 1;
        }
      } else {
        source += '[^/]*';
      }
    } else if (char === '?') {
      source += '[^/]';
    } else if (char === '{') {
      const end = glob.indexOf('}', i);
      if (end === -1) {
        source += '\\{';
      } else {
        source += `(?:${glob
          .slice(i + 1, end)
          .split(',')
          .map(part => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
          .join('|')})`;
        i = end;
      }
    } else {
      source += char.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }
  }
  return new RegExp(`^${source}$`);
}

function matchesAny(relativePath, patterns) {
  return patterns.some(pattern => globToRegExp(pattern).test(relativePath));
}

/** 一个 glob 是否可能匹配该目录下的内容（用于剪枝，宁可多进不可漏）。 */
function mayContain(relativeDir, patterns) {
  const prefix = relativeDir === '' ? '' : `${relativeDir}/`;
  return patterns.some(pattern => {
    if (pattern.includes('**')) {
      const head = pattern.slice(0, pattern.indexOf('**'));
      return prefix.startsWith(head) || head.startsWith(prefix);
    }
    return pattern.startsWith(prefix) || prefix.startsWith(pattern);
  });
}

function listFiles(rootDir, include, exclude) {
  const files = [];

  const walk = relativeDir => {
    const absoluteDir = path.join(rootDir, relativeDir);
    const entries = fs.readdirSync(absoluteDir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1));

    for (const entry of entries) {
      const relativePath = relativeDir === '' ? entry.name : `${relativeDir}/${entry.name}`;
      if (matchesAny(relativePath, exclude)) continue;

      if (entry.isDirectory()) {
        if (matchesAny(`${relativePath}/`, exclude)) continue;
        if (!mayContain(relativePath, include)) continue;
        walk(relativePath);
      } else if (entry.isFile() && matchesAny(relativePath, include)) {
        files.push(relativePath);
      }
    }
  };

  walk('');
  return files.sort();
}

function isGeneratedFile(absolutePath) {
  const handle = fs.openSync(absolutePath, 'r');
  try {
    const buffer = Buffer.alloc(GENERATED_MARKER_SCAN_BYTES);
    const bytesRead = fs.readSync(handle, buffer, 0, GENERATED_MARKER_SCAN_BYTES, 0);
    return buffer.subarray(0, bytesRead).toString('utf8').includes(GENERATED_FILE_MARKER);
  } finally {
    fs.closeSync(handle);
  }
}

function isTestFile(relativePath) {
  return (
    relativePath.includes('/__tests__/') ||
    /\.(?:test|spec)\.[jt]sx?$/.test(relativePath) ||
    relativePath.startsWith('tests/')
  );
}

/** 3/4 位缩写展开成 6/8 位，便于与契约色值比对。 */
function normalizeHex(raw) {
  const value = raw.toLowerCase();
  const digits = value.slice(1);
  if (digits.length === 3 || digits.length === 4) {
    return `#${digits
      .split('')
      .map(d => d + d)
      .join('')}`;
  }
  return value;
}

/** 从 token 契约建立"色值 → 契约分组"索引。 */
function buildColorIndex(contract) {
  const index = new Map();

  const add = (value, category) => {
    if (typeof value !== 'string') return;
    const key = normalizeHex(value);
    if (!index.has(key)) index.set(key, new Set());
    index.get(key).add(category);
  };

  const addScale = (scale, category) => {
    Object.values(scale).forEach(value => add(value, category));
  };

  addScale(contract.brand.primary.light, 'brand');
  addScale(contract.brand.primary.dark, 'brand');
  add(contract.brand.charcoal, 'brand');

  Object.values(contract.status).forEach(scale => addScale(scale, 'status'));

  addScale(contract.neutral.light, 'neutral');
  addScale(contract.neutral.dark, 'neutral');

  Object.values(contract.functional).forEach(set => {
    Object.values(set).forEach(group => addScale(group, 'surface'));
  });

  contract.chart.series.forEach(value => add(value, 'chart'));

  return index;
}

function categorize(colorIndex, rawValue) {
  const categories = colorIndex.get(normalizeHex(rawValue));
  if (!categories || categories.size === 0) return ['unclassified'];
  return COLOR_CATEGORIES.filter(category => categories.has(category));
}

function scanFile(content, pattern) {
  const hits = [];
  content.split('\n').forEach((lineText, lineIndex) => {
    pattern.lastIndex = 0;
    let match;
    while ((match = pattern.exec(lineText)) !== null) {
      hits.push({ line: lineIndex + 1, column: match.index + 1, value: match[0] });
    }
  });
  return hits;
}

function sortedByKey(record) {
  return Object.fromEntries(Object.entries(record).sort(([a], [b]) => (a < b ? -1 : 1)));
}

/**
 * @typedef {Object} TokenBaselineOptions
 * @property {string} [rootDir]   扫描根目录，默认 itsm-frontend 根目录
 * @property {string[]} [include] 纳入扫描的 glob，默认 DEFAULT_INCLUDE
 * @property {string[]} [exclude] 排除的 glob，默认 DEFAULT_EXCLUDE
 */

/**
 * 统计设计 token 存量基线。返回结果按路径/键名排序，同输入必得同输出。
 *
 * @param {TokenBaselineOptions} [options]
 */
export function getTokenBaseline(options = {}) {
  const rootDir = fs.realpathSync(path.resolve(options.rootDir ?? FRONTEND_ROOT));
  const include = options.include ?? DEFAULT_INCLUDE;
  const exclude = options.exclude ?? DEFAULT_EXCLUDE;

  const contract = JSON.parse(
    fs.readFileSync(path.join(FRONTEND_ROOT, 'src/lib/design-system/token-source.json'), 'utf8')
  );
  const colorIndex = buildColorIndex(contract);

  const scannedFiles = [];
  const fontSizeFiles = [];
  const fontSizeByValue = {};
  const fontSizeByFileKind = { source: 0, test: 0 };
  let fontSizeTotal = 0;

  const colorFiles = [];
  const colorByValue = {};
  const colorByCategory = Object.fromEntries(COLOR_CATEGORIES.map(category => [category, 0]));
  const colorByFileKind = { source: 0, test: 0 };
  let colorTotal = 0;

  for (const relativePath of listFiles(rootDir, include, exclude)) {
    const absolutePath = path.join(rootDir, relativePath);
    if (isGeneratedFile(absolutePath)) continue;

    scannedFiles.push(relativePath);
    const content = fs.readFileSync(absolutePath, 'utf8');
    const fileKind = isTestFile(relativePath) ? 'test' : 'source';

    const fontHits = scanFile(content, ARBITRARY_FONT_SIZE_PATTERN);
    if (fontHits.length > 0) {
      fontSizeTotal += fontHits.length;
      fontSizeByFileKind[fileKind] += fontHits.length;
      fontHits.forEach(hit => {
        fontSizeByValue[hit.value] = (fontSizeByValue[hit.value] ?? 0) + 1;
      });
      fontSizeFiles.push({
        file: relativePath,
        fileKind,
        count: fontHits.length,
        occurrences: fontHits,
      });
    }

    const colorHits = scanFile(content, RAW_COLOR_PATTERN);
    if (colorHits.length > 0) {
      colorTotal += colorHits.length;
      colorByFileKind[fileKind] += colorHits.length;
      const occurrences = colorHits.map(hit => {
        const categories = categorize(colorIndex, hit.value);
        const key = normalizeHex(hit.value);
        const entry = colorByValue[key] ?? { count: 0, categories };
        entry.count += 1;
        colorByValue[key] = entry;
        categories.forEach(category => {
          colorByCategory[category] += 1;
        });
        return { ...hit, categories };
      });
      colorFiles.push({ file: relativePath, fileKind, count: colorHits.length, occurrences });
    }
  }

  return {
    rootDir,
    include,
    exclude,
    generatedFileMarker: GENERATED_FILE_MARKER,
    scannedFileCount: scannedFiles.length,
    scannedFiles,
    arbitraryFontSize: {
      total: fontSizeTotal,
      byValue: sortedByKey(fontSizeByValue),
      byFileKind: fontSizeByFileKind,
      files: fontSizeFiles,
    },
    rawColor: {
      total: colorTotal,
      byValue: sortedByKey(colorByValue),
      byCategory: sortedByKey(colorByCategory),
      byFileKind: colorByFileKind,
      files: colorFiles,
    },
  };
}

/** 摘要视图：去掉逐处明细和完整文件清单，保留范围与计数，适合贴进 CI/评审。 */
function toSummary(baseline) {
  const stripOccurrences = files =>
    files.map(({ file, fileKind, count }) => ({ file, fileKind, count }));

  const { scannedFiles, arbitraryFontSize, rawColor, ...scope } = baseline;
  void scannedFiles;

  return {
    ...scope,
    arbitraryFontSize: { ...arbitraryFontSize, files: stripOccurrences(arbitraryFontSize.files) },
    rawColor: { ...rawColor, files: stripOccurrences(rawColor.files) },
  };
}

const invokedDirectly =
  process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invokedDirectly) {
  const full = process.argv.includes('--full');
  const baseline = getTokenBaseline();
  process.stdout.write(`${JSON.stringify(full ? baseline : toSummary(baseline), null, 2)}\n`);
}
