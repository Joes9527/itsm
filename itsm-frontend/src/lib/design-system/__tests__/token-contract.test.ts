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
import {
  buildEntries,
  renderCss,
  readTokenSource,
  generate,
  toRawName,
  pxToRem,
  RAW_VAR_PREFIX,
  OUTPUT_PATH,
  GENERATED_FILE_MARKER as CSS_GENERATED_FILE_MARKER,
} from '../../../../scripts/generate-design-tokens.mjs';

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

describe('generate-design-tokens.mjs', () => {
  const tokens = readTokenSource();
  const css = renderCss(tokens);

  /** Every `name: value;` declaration line inside a given top-level block. */
  function declarationsIn(blockSelector: string): Map<string, string> {
    const blockRe = new RegExp(
      `${blockSelector.replace(/[.:]/g, '\\$&')} \\{\\n([\\s\\S]*?)\\n\\}`
    );
    const match = css.match(blockRe);
    expect(match).not.toBeNull();
    const body = match![1];
    const decls = new Map<string, string>();
    for (const line of body.split('\n')) {
      const declMatch = line.match(/^\s*(--[a-zA-Z0-9-]+):\s*(.+);\s*$/);
      if (declMatch) {
        decls.set(declMatch[1], declMatch[2]);
      }
    }
    return decls;
  }

  /**
   * Resolve a name to its final literal value by following a chain of `var(--other)`
   * forwards (e.g. `--color-brand-primary-500` -> `var(--token-color-brand-primary-500)`
   * -> `#F06820`). Every entry in `:root`/`.dark` is either a literal raw value or a single
   * forward to its raw counterpart, so one hop is all that's ever needed in practice, but
   * this follows an arbitrary chain and throws on a cycle rather than looping forever.
   */
  function resolve(map: Map<string, string>, name: string): string | undefined {
    let current = name;
    const seen = new Set<string>();
    for (;;) {
      const value = map.get(current);
      if (value === undefined) return undefined;
      const varMatch = value.match(/^var\((--[a-zA-Z0-9-]+)\)$/);
      if (!varMatch) return value;
      if (seen.has(current)) throw new Error(`resolve(): cycle detected resolving ${name}`);
      seen.add(current);
      current = varMatch[1];
    }
  }

  /** Every `--name: var(--other);` forwarding declaration anywhere in the whole generated CSS. */
  function allVarForwards(): Array<{ name: string; ref: string }> {
    return [...css.matchAll(/^\s*(--[a-zA-Z0-9-]+):\s*var\((--[a-zA-Z0-9-]+)\);\s*$/gm)].map(m => ({
      name: m[1],
      ref: m[2],
    }));
  }

  it('starts with the @generated marker inside the first 1024 bytes', () => {
    expect(CSS_GENERATED_FILE_MARKER).toBe('@generated');
    const head = css.slice(0, 1024);
    expect(head).toContain('@generated');
    expect(head).toContain('do not edit');
  });

  it('defines both a raw declaration and a forwarding theme declaration per variable, with no duplicate names, in :root', () => {
    const root = declarationsIn(':root');
    const rootStart = css.indexOf(':root {');
    const rootBody = css.slice(rootStart, rootStart + css.slice(rootStart).indexOf('\n}\n'));
    const names = [...rootBody.matchAll(/^\s*(--[a-zA-Z0-9-]+):/gm)].map(m => m[1]);

    const seen = new Set<string>();
    names.forEach(name => {
      expect(seen.has(name)).toBe(false);
      seen.add(name);
    });
    expect(root.size).toBe(names.length);

    const rawNames = names.filter(n => n.startsWith(RAW_VAR_PREFIX));
    const themeNames = names.filter(n => !n.startsWith(RAW_VAR_PREFIX));
    expect(rawNames.length).toBeGreaterThan(100);
    // Exactly one theme (Tailwind-facing) name per raw (value-holding) name.
    expect(themeNames.length).toBe(rawNames.length);
    themeNames.forEach(themeName => {
      const rawName = toRawName(themeName);
      expect(rawNames).toContain(rawName);
      expect(root.get(themeName)).toBe(`var(${rawName})`);
    });
  });

  it('toRawName prefixes with --token- and pxToRem converts at a 16px root', () => {
    expect(toRawName('--color-brand-primary-500')).toBe('--token-color-brand-primary-500');
    expect(toRawName('--spacing')).toBe('--token-spacing');
    expect(() => toRawName('color-x')).toThrow();

    expect(pxToRem('4px')).toBe('0.25rem');
    expect(pxToRem('2px')).toBe('0.125rem');
    expect(pxToRem('16px')).toBe('1rem');
    expect(pxToRem('0px')).toBe('0rem');
    expect(() => pxToRem('1rem')).toThrow();
    expect(() => pxToRem('not-a-length')).toThrow();
  });

  it('CRITICAL REGRESSION GUARD: no declaration anywhere in the generated CSS is self-referential', () => {
    // `--x: var(--x);` is a self-referential dependency cycle. Per the CSS Custom
    // Properties spec every property in a cycle computes to the guaranteed-invalid value,
    // so in a production build (where an unlayered rule — such as the one Tailwind's
    // @theme inline processing emits — beats any equally-specific layered/hand-written
    // declaration in the cascade, regardless of source order) a self-referencing token
    // would silently resolve to nothing. `next dev` does not expose this because it
    // preserves @layer, so this must be asserted from the generator's own output, not
    // eyeballed against a dev server — and it must cover every var()-forwarding
    // declaration in the file (:root, .dark, and @theme inline alike), not just the
    // @theme inline block, since :root/.dark also contain theme-name-forwarding
    // declarations now (see next test for why).
    const forwards = allVarForwards();
    // :root theme forwards + .dark theme forwards + @theme inline forwards, all >100 each.
    expect(forwards.length).toBeGreaterThan(300);
    forwards.forEach(({ name, ref }) => {
      expect(ref).not.toBe(name);
    });
  });

  it('every @theme inline themeName forwards to a rawName that is ALSO unconditionally declared directly in :root/.dark', () => {
    // Empirically verified against a real `npm run build`: Tailwind's @theme inline does
    // NOT unconditionally publish a themeName as a literal CSS declaration onto :root/:host
    // — it only reliably does so for names actually referenced by a scanned utility class.
    // `rounded-sm` is not used anywhere in this repo, and the compiled bundle had *no*
    // declaration of --radius-sm at all; the already-used `.rounded-md` utility compiled to
    // `border-radius:var(--radius-md,.375rem)`, silently falling back to Tailwind's own
    // stock default (.375rem) instead of this project's 0.25rem, because nothing else in
    // the cascade defined --radius-md either. So the generator must not rely on @theme
    // inline to make a themeName resolvable — it has to declare the themeName directly
    // in :root/.dark too (see renderBlock()'s doc comment). This test locks that in.
    const themeMatch = css.match(/@theme inline \{\n([\s\S]*?)\n\}/);
    expect(themeMatch).not.toBeNull();
    const entries = [...themeMatch![1].matchAll(/^\s*(--[a-zA-Z0-9-]+):\s*var\((--[a-zA-Z0-9-]+)\);\s*$/gm)].map(
      m => ({ themeName: m[1], rawVarRef: m[2] })
    );
    expect(entries.length).toBeGreaterThan(100);

    const root = declarationsIn(':root');
    entries.forEach(({ themeName, rawVarRef }) => {
      expect(rawVarRef).toBe(toRawName(themeName));
      expect(root.get(themeName)).toBe(`var(${rawVarRef})`);
    });
  });

  it('maps --color-brand-primary-500 to the frozen brand color in both :root and .dark, resolved through the raw name', () => {
    const root = declarationsIn(':root');
    const dark = declarationsIn('.dark');
    expect(resolve(root, '--color-brand-primary-500')).toBe('#F06820');
    expect(resolve(dark, '--color-brand-primary-500')).toBe('#F06820');
    // Light/dark genuinely differ for other steps of the same scale.
    expect(resolve(root, '--color-brand-primary-50')).toBe('#fff5f0');
    expect(resolve(dark, '--color-brand-primary-50')).toBe('#4A1D02');
    expect(resolve(root, '--color-brand-charcoal')).toBe('#2A2A2A');
  });

  it('defines the semantic status variables as a single theme-independent scale', () => {
    const root = declarationsIn(':root');
    const dark = declarationsIn('.dark');
    expect(resolve(root, '--color-status-info-500')).toBe('#0ea5e9');
    expect(resolve(root, '--color-status-success-500')).toBe('#22c55e');
    expect(resolve(root, '--color-status-warning-500')).toBe('#f59e0b');
    expect(resolve(root, '--color-status-error-500')).toBe('#ef4444');
    // Status colors are not themed: .dark must not redeclare/override them (neither the
    // theme name nor its raw counterpart).
    expect(dark.has('--color-status-info-500')).toBe(false);
    expect(dark.has(toRawName('--color-status-info-500'))).toBe(false);
    expect(dark.has('--color-status-success-500')).toBe(false);
    expect(dark.has(toRawName('--color-status-success-500'))).toBe(false);
    expect(dark.has('--color-status-warning-500')).toBe(false);
    expect(dark.has(toRawName('--color-status-warning-500'))).toBe(false);
    expect(dark.has('--color-status-error-500')).toBe(false);
    expect(dark.has(toRawName('--color-status-error-500'))).toBe(false);
  });

  it('defines light and dark values for neutral and functional groups', () => {
    const root = declarationsIn(':root');
    const dark = declarationsIn('.dark');
    expect(resolve(root, '--color-neutral-50')).toBe(tokenContract.neutral.light[50]);
    expect(resolve(dark, '--color-neutral-50')).toBe(tokenContract.neutral.dark[50]);
    expect(resolve(root, '--color-background-primary')).toBe(
      tokenContract.functional.light.background.primary
    );
    expect(resolve(dark, '--color-background-primary')).toBe(
      tokenContract.functional.dark.background.primary
    );
    expect(resolve(root, '--color-surface-elevated')).toBe(
      tokenContract.functional.light.surface.elevated
    );
    expect(resolve(dark, '--color-surface-elevated')).toBe(
      tokenContract.functional.dark.surface.elevated
    );
    expect(resolve(root, '--color-border-focus')).toBe(tokenContract.functional.light.border.focus);
    expect(resolve(root, '--color-text-primary')).toBe(tokenContract.functional.light.text.primary);
    expect(resolve(dark, '--color-text-primary')).toBe(tokenContract.functional.dark.text.primary);
  });

  it('defines the chart palette and user-defined fallback as theme-independent variables', () => {
    const root = declarationsIn(':root');
    const dark = declarationsIn('.dark');
    tokenContract.chart.series.forEach((value, index) => {
      expect(resolve(root, `--color-chart-series-${index + 1}`)).toBe(value);
    });
    expect(dark.has('--color-chart-series-1')).toBe(false);
    expect(resolve(root, '--color-user-defined-fallback')).toBe(tokenContract.userDefined.fallback);
  });

  it('converts spacing/radius/text to rem in the generated CSS while token-source.json stays px', () => {
    const root = declarationsIn(':root');

    // token-source.json (Task 1's frozen contract) must remain untouched px strings —
    // theme.tsx's getAntdTheme() still does parseInt(value, 10) on these for antd, which
    // would silently break (parseInt('0.25rem', 10) === 0) if the source itself changed.
    expect(tokenContract.spacing['1']).toBe('4px');
    expect(tokenContract.radius.sm).toBe('2px');
    expect(tokenContract.radius.md).toBe('4px');
    expect(tokenContract.fontSize.xs).toBe('12px');
    expect(tokenContract.fontSize.sm).toBe('14px');

    // The generated CSS converts px -> rem at a 16px root only for the three Tailwind
    // namespaces that actually govern utility-class output (--spacing/--radius-*/--text-*).
    // Tailwind v4's own --spacing multiplier reproduces every value in tokens.spacing
    // (calc(var(--spacing) * n)); no per-key --spacing-<n> variables are generated because
    // several keys ("0.5", "1.5", "2.5", "3.5") contain a literal '.', which is not a valid
    // bare CSS custom-property-name character.
    expect(resolve(root, '--spacing')).toBe('0.25rem');
    expect(root.has(toRawName('--spacing-0.5'))).toBe(false);
    expect(root.has('--spacing-md')).toBe(false);

    expect(resolve(root, '--radius-sm')).toBe('0.125rem');
    expect(resolve(root, '--radius-md')).toBe('0.25rem');
    expect(resolve(root, '--radius-full')).toBe(pxToRem(tokenContract.radius.full));
    expect(resolve(root, '--text-sm')).toBe('0.875rem');
    expect(resolve(root, '--text-xs')).toBe('0.75rem');
    expect(resolve(root, '--text-base')).toBe('1rem');

    // Shadow stays px: multi-part box-shadow strings have no single length to convert,
    // and Tailwind v4's own default shadow scale is px-based too.
    expect(resolve(root, '--shadow-md')).toBe(tokenContract.shadow.md);
    expect(resolve(root, '--shadow-none')).toBe('none');
    expect(resolve(root, '--shadow-md')).toEqual(expect.stringContaining('px'));

    // lineHeight/fontWeight are unitless — unaffected by the rem conversion.
    expect(resolve(root, '--leading-tight')).toBe(tokenContract.lineHeight.tight);
    expect(resolve(root, '--font-weight-bold')).toBe(tokenContract.fontWeight.bold);

    // Legacy px-named vars are never emitted (superseded by the Tailwind namespaces above).
    expect(root.has('--font-size-sm')).toBe(false);
    expect(root.has('--line-height-tight')).toBe(false);
  });

  it('maps canonical color and typography variables into a Tailwind v4 @theme inline block', () => {
    const themeMatch = css.match(/@theme inline \{\n([\s\S]*?)\n\}/);
    expect(themeMatch).not.toBeNull();
    const themeBody = themeMatch![1];

    expect(themeBody).toContain(
      `--color-brand-primary-500: var(${toRawName('--color-brand-primary-500')});`
    );
    expect(themeBody).toContain(
      `--color-status-success-500: var(${toRawName('--color-status-success-500')});`
    );
    expect(themeBody).toContain(
      `--color-status-info-500: var(${toRawName('--color-status-info-500')});`
    );
    expect(themeBody).toContain(
      `--color-status-warning-500: var(${toRawName('--color-status-warning-500')});`
    );
    expect(themeBody).toContain(
      `--color-status-error-500: var(${toRawName('--color-status-error-500')});`
    );
    expect(themeBody).toContain(`--radius-sm: var(${toRawName('--radius-sm')});`);
    expect(themeBody).toContain(`--text-sm: var(${toRawName('--text-sm')});`);
    expect(themeBody).toContain(`--leading-tight: var(${toRawName('--leading-tight')});`);
    expect(themeBody).toContain(`--font-weight-bold: var(${toRawName('--font-weight-bold')});`);
  });

  it('does not emit legacy alias names such as --color-bg-elevated', () => {
    expect(css).not.toContain('--color-bg-elevated');
    // Other pre-existing abbreviated/legacy names that are superseded by canonical names.
    // (None of these are substrings of the --token-* raw names either, e.g. "--token-color-
    // brand-primary-500" does not contain the contiguous substring "--color-primary-".)
    expect(css).not.toMatch(/--color-primary-\d/); // superseded by --color-brand-primary-*
    expect(css).not.toMatch(/--color-success-\d/); // superseded by --color-status-success-*
    expect(css).not.toMatch(/--font-size-/); // superseded by --text-*
    expect(css).not.toMatch(/--line-height-/); // superseded by --leading-*
    expect(css).not.toMatch(/--border-radius-/); // superseded by --radius-*
    expect(css).not.toMatch(/--box-shadow-/); // superseded by --shadow-*
  });

  it('is a pure function of token-source.json: re-rendering produces byte-identical output', () => {
    expect(renderCss(readTokenSource())).toBe(css);
  });

  it('generate() writes the deterministic CSS to disk and is idempotent', () => {
    const fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'generate-design-tokens-'));
    try {
      const outputPath = path.join(fixtureRoot, 'design-tokens.css');
      generate({ outputPath });
      const first = fs.readFileSync(outputPath, 'utf8');
      generate({ outputPath });
      const second = fs.readFileSync(outputPath, 'utf8');
      expect(second).toBe(first);
      expect(first).toBe(css);
    } finally {
      fs.rmSync(fixtureRoot, { recursive: true, force: true });
    }
  });

  it('the committed generated file at OUTPUT_PATH matches what the generator currently produces', () => {
    expect(fs.existsSync(OUTPUT_PATH)).toBe(true);
    expect(fs.readFileSync(OUTPUT_PATH, 'utf8')).toBe(css);
  });
});

describe('globals.css token import wiring', () => {
  const globalsCssPath = path.join(FRONTEND_ROOT, 'src/app/globals.css');
  const globalsCss = fs.readFileSync(globalsCssPath, 'utf8');

  // Anchored to actual `@import` statement lines (not just a bare substring search) so this
  // isn't fooled by the descriptive comment directly above the import, which necessarily
  // mentions "theme-variables.css" and `@import "tailwindcss"` in prose *before* the real
  // import lines it's describing.
  function importLineIndex(pattern: RegExp): number {
    const match = globalsCss.match(pattern);
    expect(match).not.toBeNull();
    expect(match!.index).not.toBeUndefined();
    return match!.index as number;
  }

  it('imports the generated design-tokens.css before @import "tailwindcss"', () => {
    // Regression guard for a silent production-only failure: importing the generated file
    // *after* `@import "tailwindcss";` builds successfully (exit code 0) but the compiled
    // CSS in .next/static/css/*.css drops every :root/.dark declaration from the generated
    // file entirely, leaving only the @theme inline block's now-dangling var() reference.
    // Verified empirically against a real `npm run build`, not just dev-server behavior.
    const generatedImportIndex = importLineIndex(/^@import url\('\.\.\/styles\/generated\/design-tokens\.css'\);$/m);
    const tailwindImportIndex = importLineIndex(/^@import "tailwindcss";$/m);
    expect(generatedImportIndex).toBeLessThan(tailwindImportIndex);
  });

  it('imports the generated design-tokens.css before theme-variables.css', () => {
    // theme-variables.css's compatibility aliases forward to the canonical --color-*/
    // --radius-*/--text-*/etc. names via var(), which Tailwind's @theme inline re-emits
    // onto :root/.dark; the generated file's own :root/.dark block must still be reachable.
    const generatedImportIndex = importLineIndex(/^@import url\('\.\.\/styles\/generated\/design-tokens\.css'\);$/m);
    const themeVariablesImportIndex = importLineIndex(/^@import url\('\.\.\/styles\/theme-variables\.css'\);$/m);
    expect(generatedImportIndex).toBeLessThan(themeVariablesImportIndex);
  });
});
