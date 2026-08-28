/**
 * Design token contract + baseline audit tests.
 *
 * The contract is the single authoritative token source. `colors.ts` and
 * `spacing.ts` must be derived from it, and the baseline audit script must
 * report deterministic, reproducibly-scoped counts for the migration work.
 */

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  tokenContract,
  isUserDefinedColor,
  STATUS_TOKEN_NAMES,
} from '../token-contract';
import { colors, darkColors } from '../colors';
import {
  spacing,
  borderRadius,
  boxShadow,
  fontSize,
  lineHeight,
  fontWeight,
} from '../spacing';
import {
  getTokenBaseline,
  COLOR_CATEGORIES,
  DEFAULT_INCLUDE,
  DEFAULT_EXCLUDE,
  GENERATED_FILE_MARKER,
  FRONTEND_ROOT,
} from '../../../../scripts/design-token-baseline.mjs';

/** Recursively collect every object key in the contract. */
function collectKeys(value: unknown, out: string[] = []): string[] {
  if (Array.isArray(value)) {
    value.forEach(item => collectKeys(item, out));
    return out;
  }
  if (value !== null && typeof value === 'object') {
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      out.push(key);
      collectKeys(child, out);
    }
  }
  return out;
}

describe('token contract', () => {
  it('exposes every token group the design system contract requires', () => {
    expect(Object.keys(tokenContract).sort()).toEqual(
      [
        'brand',
        'chart',
        'fontSize',
        'fontWeight',
        'functional',
        'lineHeight',
        'neutral',
        'radius',
        'shadow',
        'spacing',
        'status',
        'userDefined',
      ].sort()
    );
  });

  it('defines the brand primary scale for both themes', () => {
    expect(tokenContract.brand.primary.light[500]).toBe('#F06820');
    expect(tokenContract.brand.primary.dark[500]).toBe('#F06820');
    expect(tokenContract.brand.primary.light[50]).toBe('#fff5f0');
    expect(tokenContract.brand.primary.light[950]).toBe('#4A1D02');
    expect(tokenContract.brand.charcoal).toBe('#2A2A2A');
  });

  it('defines exactly the four semantic status colors', () => {
    expect(Object.keys(tokenContract.status).sort()).toEqual([
      'error',
      'info',
      'success',
      'warning',
    ]);
    expect(STATUS_TOKEN_NAMES).toEqual(['info', 'success', 'warning', 'error']);
    expect(tokenContract.status.info[500]).toBe('#0ea5e9');
    expect(tokenContract.status.success[500]).toBe('#22c55e');
    expect(tokenContract.status.warning[500]).toBe('#f59e0b');
    expect(tokenContract.status.error[500]).toBe('#ef4444');
  });

  it('has no second "danger" status color source anywhere in the contract', () => {
    const keys = collectKeys(tokenContract).map(key => key.toLowerCase());
    expect(keys).not.toContain('danger');
  });

  it('defines a chart series palette of distinct colors', () => {
    expect(Array.isArray(tokenContract.chart.series)).toBe(true);
    expect(tokenContract.chart.series.length).toBeGreaterThan(0);
    tokenContract.chart.series.forEach(color => {
      expect(color).toMatch(/^#[0-9a-fA-F]{6}$/);
    });
    expect(new Set(tokenContract.chart.series).size).toBe(tokenContract.chart.series.length);
  });

  it('validates user-defined colors instead of replacing them', () => {
    expect(isUserDefinedColor('#f06820')).toBe(true);
    expect(isUserDefinedColor('#FFF')).toBe(true);
    expect(isUserDefinedColor('rgba(0, 0, 0, 0.5)')).toBe(true);
    expect(isUserDefinedColor('hsl(210 100% 50%)')).toBe(true);
    expect(isUserDefinedColor('not-a-color')).toBe(false);
    expect(isUserDefinedColor('#12345')).toBe(false);
    expect(isUserDefinedColor('')).toBe(false);
    expect(isUserDefinedColor(tokenContract.userDefined.fallback)).toBe(true);
  });

  it('is serializable so Node build scripts can consume it without a TypeScript loader', () => {
    expect(JSON.parse(JSON.stringify(tokenContract))).toEqual(tokenContract);
  });
});

describe('token contract is the single source for colors.ts and spacing.ts', () => {
  it('derives the light and dark color palettes from the contract', () => {
    expect(colors.primary).toBe(tokenContract.brand.primary.light);
    expect(colors.neutral).toBe(tokenContract.neutral.light);
    expect(colors.semantic).toBe(tokenContract.status);
    expect(colors.functional).toBe(tokenContract.functional.light);
    expect(colors.charcoal).toBe(tokenContract.brand.charcoal);

    expect(darkColors.primary).toBe(tokenContract.brand.primary.dark);
    expect(darkColors.neutral).toBe(tokenContract.neutral.dark);
    expect(darkColors.functional).toBe(tokenContract.functional.dark);
  });

  it('derives the spacing, radius, shadow and typography scales from the contract', () => {
    expect(spacing).toBe(tokenContract.spacing);
    expect(borderRadius).toBe(tokenContract.radius);
    expect(boxShadow).toBe(tokenContract.shadow);
    expect(fontSize).toBe(tokenContract.fontSize);
    expect(lineHeight).toBe(tokenContract.lineHeight);
    expect(fontWeight).toBe(tokenContract.fontWeight);
  });
});

describe('getTokenBaseline', () => {
  let fixtureRoot: string;

  const write = (relativePath: string, content: string) => {
    const absolute = path.join(fixtureRoot, relativePath);
    fs.mkdirSync(path.dirname(absolute), { recursive: true });
    fs.writeFileSync(absolute, content, 'utf8');
  };

  beforeAll(() => {
    fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'token-baseline-'));

    write(
      'src/components/Card.tsx',
      [
        'export const Card = () => (',
        '  <span className="text-[11px] text-[10px]" style={{ color: "#22c55e" }} />',
        ');',
        'export const brandInk = "#F06820";',
      ].join('\n')
    );
    write(
      'src/components/Chart.tsx',
      'export const COLORS = ["#1890ff", "#52c41a"];\nexport const decorative = "#667eea";\n'
    );
    write('src/components/__tests__/Card.test.tsx', 'export const fixtureColor = "#ff0000";\n');
    write('src/styles/app.css', '.a { color: #0ea5e9; font-size: 13px; }\n');
    write('src/README.md', 'Ignored because markdown is not in the include list: #123456\n');

    // Build output / caches / generated files must never contribute to the baseline.
    write('.next/static/chunk.tsx', 'const a = "#abcdef"; const b = "text-[11px]";\n');
    write('coverage/lcov-report/block.css', '.x { color: #abcdef; }\n');
    write('.jest-cache/perf/x.ts', 'export const c = "#abcdef";\n');
    write('node_modules/pkg/index.ts', 'export const d = "#abcdef";\n');
    write(
      'src/styles/generated/design-tokens.css',
      `/* ${GENERATED_FILE_MARKER} by scripts/generate-design-tokens.mjs */\n:root { --color-status-info-500: #0ea5e9; }\n`
    );
    write(
      'src/lib/generated-elsewhere.ts',
      `/* ${GENERATED_FILE_MARKER} */\nexport const e = "#abcdef";\n`
    );
  });

  afterAll(() => {
    fs.rmSync(fixtureRoot, { recursive: true, force: true });
  });

  it('reports the scope it used so the count is reproducible', () => {
    const baseline = getTokenBaseline({ rootDir: fixtureRoot });
    expect(baseline.rootDir).toBe(fs.realpathSync(fixtureRoot));
    expect(baseline.include).toEqual(DEFAULT_INCLUDE);
    expect(baseline.exclude).toEqual(DEFAULT_EXCLUDE);
    expect(baseline.generatedFileMarker).toBe(GENERATED_FILE_MARKER);
    expect(COLOR_CATEGORIES).toEqual([
      'brand',
      'status',
      'chart',
      'neutral',
      'surface',
      'unclassified',
    ]);
  });

  it('excludes build output, coverage, jest cache, vendor and generated files', () => {
    expect(DEFAULT_EXCLUDE).toEqual(expect.arrayContaining(['.next/**', 'coverage/**', '.jest-cache/**']));

    const baseline = getTokenBaseline({ rootDir: fixtureRoot });
    const scanned = baseline.scannedFiles;

    expect(scanned).toEqual(
      expect.arrayContaining([
        'src/components/Card.tsx',
        'src/components/Chart.tsx',
        'src/components/__tests__/Card.test.tsx',
        'src/styles/app.css',
      ])
    );
    scanned.forEach(file => {
      expect(file.startsWith('.next/')).toBe(false);
      expect(file.startsWith('coverage/')).toBe(false);
      expect(file.startsWith('.jest-cache/')).toBe(false);
      expect(file.startsWith('node_modules/')).toBe(false);
    });
    expect(scanned).not.toContain('src/styles/generated/design-tokens.css');
    expect(scanned).not.toContain('src/lib/generated-elsewhere.ts');
    expect(scanned).not.toContain('src/README.md');
    expect(baseline.scannedFileCount).toBe(scanned.length);
  });

  it('counts arbitrary font-size classes per value and per file', () => {
    const { arbitraryFontSize } = getTokenBaseline({ rootDir: fixtureRoot });

    expect(arbitraryFontSize.total).toBe(2);
    expect(arbitraryFontSize.byValue).toEqual({ 'text-[10px]': 1, 'text-[11px]': 1 });
    expect(arbitraryFontSize.byFileKind).toEqual({ source: 2, test: 0 });
    expect(arbitraryFontSize.files).toEqual([
      {
        file: 'src/components/Card.tsx',
        fileKind: 'source',
        count: 2,
        occurrences: [
          { line: 2, column: 20, value: 'text-[11px]' },
          { line: 2, column: 32, value: 'text-[10px]' },
        ],
      },
    ]);
  });

  it('counts raw color literals and classifies them against the contract', () => {
    const { rawColor } = getTokenBaseline({ rootDir: fixtureRoot });

    expect(rawColor.total).toBe(7);
    expect(rawColor.byValue['#22c55e']).toEqual({ count: 1, categories: ['status'] });
    expect(rawColor.byValue['#1890ff']).toEqual({ count: 1, categories: ['chart'] });
    expect(rawColor.byValue['#667eea']).toEqual({ count: 1, categories: ['unclassified'] });

    // A literal can be a candidate for more than one group (#F06820 is both the brand
    // primary and the focus border), which is exactly why the call site — not the value —
    // decides the migration target.
    expect(rawColor.byValue['#f06820']).toEqual({ count: 1, categories: ['brand', 'surface'] });

    expect(rawColor.byCategory).toEqual({
      brand: 1,
      chart: 2,
      neutral: 0,
      status: 2,
      surface: 1,
      unclassified: 2,
    });
    expect(rawColor.byFileKind).toEqual({ source: 6, test: 1 });

    const testFile = rawColor.files.find(f => f.file === 'src/components/__tests__/Card.test.tsx');
    expect(testFile).toEqual({
      file: 'src/components/__tests__/Card.test.tsx',
      fileKind: 'test',
      count: 1,
      occurrences: [{ line: 1, column: 30, value: '#ff0000', categories: ['unclassified'] }],
    });
  });

  it('honors caller-supplied include and exclude scopes', () => {
    const widened = getTokenBaseline({ rootDir: fixtureRoot, include: ['**/*.css'], exclude: [] });

    // The generated file is skipped by its marker even with an empty exclude list.
    expect(widened.include).toEqual(['**/*.css']);
    expect(widened.scannedFiles).toEqual([
      'coverage/lcov-report/block.css',
      'src/styles/app.css',
    ]);
    expect(widened.rawColor.total).toBe(2);

    const narrowed = getTokenBaseline({
      rootDir: fixtureRoot,
      include: ['**/*.css'],
      exclude: ['coverage/**'],
    });

    expect(narrowed.scannedFiles).toEqual(['src/styles/app.css']);
    expect(narrowed.rawColor.total).toBe(1);
  });

  it('is deterministic across runs', () => {
    expect(getTokenBaseline({ rootDir: fixtureRoot })).toEqual(
      getTokenBaseline({ rootDir: fixtureRoot })
    );
  });

  it('excludes the token source files from the real repository baseline', () => {
    const baseline = getTokenBaseline();

    expect(baseline.rootDir).toBe(fs.realpathSync(FRONTEND_ROOT));
    expect(baseline.scannedFiles).not.toContain('src/lib/design-system/colors.ts');
    expect(baseline.scannedFiles).not.toContain('src/lib/design-system/spacing.ts');
    expect(baseline.scannedFiles).not.toContain('src/lib/design-system/token-contract.ts');
    expect(baseline.scannedFiles.length).toBeGreaterThan(0);
    expect(baseline.arbitraryFontSize.total).toBeGreaterThan(0);
    expect(baseline.rawColor.total).toBeGreaterThan(0);
  });
});
