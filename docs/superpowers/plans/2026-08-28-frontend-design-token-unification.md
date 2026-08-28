# Frontend Design Token Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish one build-compatible token source for the frontend, generate the CSS and Tailwind v4 consumption layers from it, migrate semantic usages safely, and prevent new unclassified style literals.

**Architecture:** Keep typed TypeScript exports for React and Ant Design consumers, but introduce a serializable build-time token source consumed by a generator. The generator emits the canonical CSS variables and Tailwind v4 `@theme` entry; theme bootstrap sets the light/dark class before hydration, while React synchronizes subsequent changes. Migrations classify call sites by semantic role instead of replacing colors by hexadecimal value.

**Tech Stack:** Next.js 15 App Router, React 19, TypeScript, Ant Design 6, Tailwind CSS 4, PostCSS, ESLint 9, Jest, Playwright.

**Spec:** [docs/superpowers/specs/2026-08-27-frontend-design-token-unification-design.md](docs/superpowers/specs/2026-08-27-frontend-design-token-unification-design.md)

## Global Constraints

- HTTP/API behavior and backend code are out of scope.
- CSS variable names must have one canonical contract; do not retain duplicate aliases indefinitely.
- Do not call `document` from the server component `app/layout.tsx`.
- Do not globally replace hexadecimal values without classifying the call site.
- `error` is the semantic status name used at the Ant Design boundary; `danger` may be an action variant, not a second error color source.
- Dynamic business colors must use CSS variables, Ant Design tokens, or an approved palette; do not construct dynamic Tailwind class names.
- Preserve user-configured colors and validate their format at the boundary.
- Tests and generated output must be excluded or explicitly allow-listed by the style guard.
- Use `npm run lint:check` for read-only lint verification; do not use the auto-fixing `npm run lint` as the zero-violation proof.
- `html`/`:root` class toggling (driven by `theme.tsx`'s `isDark` state) is the sole dark-mode activation mechanism. The `@media (prefers-color-scheme: dark) { :root.dark { ... } }` block in `globals.css` (~lines 1651-1737) is a second, conflicting activation path that fires only when both the OS prefers dark AND the `.dark` class is present, producing different values than the plain `.dark` block for the same variables (e.g. `--color-info-500` differs). Delete this block outright — do not merge its values into the class-based block. Confirmed with the user during spec review.

---

### Task 1: Freeze The Token Contract And Baseline

**Files:**
- Create: `itsm-frontend/src/lib/design-system/token-contract.ts`
- Create: `itsm-frontend/scripts/design-token-baseline.mjs`
- Create: `itsm-frontend/src/lib/design-system/__tests__/token-contract.test.ts`
- Modify: `itsm-frontend/src/lib/design-system/colors.ts`
- Modify: `itsm-frontend/src/lib/design-system/spacing.ts`
- Modify: `itsm-frontend/package.json`

**Interfaces:**
- Produces `TokenContract` with `brand`, `status`, `chart`, `userDefined`, `spacing`, `radius`, `shadow`, `fontSize`, `lineHeight`, and `fontWeight` groups.
- Produces `getTokenBaseline({ rootDir, include, exclude })`, returning deterministic counts and file lists for arbitrary font classes and raw colors.

- [ ] **Step 1: Write failing contract tests**

  Assert that the contract contains `brand.primary`, `status.info`, `status.success`, `status.warning`, `status.error`, and `chart.series`; assert that no second `danger` status color source exists; assert that the baseline excludes `.next`, `coverage`, `.jest-cache`, and generated files.

- [ ] **Step 2: Run the focused tests and confirm failure**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand`

  Expected: FAIL because the contract and baseline functions are not defined.

- [ ] **Step 3: Implement the serializable contract and baseline script**

  Choose one serializable source format that Node can consume without a TypeScript loader. Keep `colors.ts` and `spacing.ts` as typed exports derived from or validated against that source. Define the exact include/exclude globs and print JSON suitable for CI review.

- [ ] **Step 4: Run the focused tests and record the baseline**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand && node scripts/design-token-baseline.mjs`

  Expected: PASS and deterministic baseline JSON with the scope printed by the script.

- [ ] **Step 5: Add the baseline command without changing the existing lint command**

  Add a named package script for the baseline/check command and verify `npm run` exposes it.

### Task 2: Generate Canonical CSS And Tailwind v4 Tokens

**Files:**
- Create: `itsm-frontend/scripts/generate-design-tokens.mjs`
- Create: `itsm-frontend/src/styles/generated/design-tokens.css`
- Modify: `itsm-frontend/src/app/globals.css`
- Modify: `itsm-frontend/postcss.config.mjs`
- Modify: `itsm-frontend/package.json`
- Test: `itsm-frontend/src/lib/design-system/__tests__/token-contract.test.ts`

**Interfaces:**
- `node scripts/generate-design-tokens.mjs` reads the serializable contract and writes deterministic CSS.
- Generated CSS contains `:root` and `.dark` values plus the Tailwind v4 `@theme inline` mappings.

- [ ] **Step 1: Add generator tests for names and both modes**

  Assert that generated output contains one definition for each canonical variable, includes light and dark values, maps `--color-brand-primary-500` and semantic status variables in `@theme inline`, and does not emit legacy aliases such as `--color-bg-elevated` unless they are explicitly canonical.

- [ ] **Step 2: Run the focused tests and confirm failure**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand`

  Expected: FAIL because the generated file and generator output are absent.

- [ ] **Step 3: Implement deterministic CSS generation**

  Generate the canonical variable names, stable ordering, and a Tailwind v4 CSS entry. Keep generated files marked as generated and do not hand-edit them.

- [ ] **Step 4: Replace duplicate variable declarations**

  Import the generated CSS from the global entry, then remove or migrate duplicate color, spacing, radius, shadow, and theme declarations from `src/styles/theme-variables.css` and the later variable blocks in `globals.css`. Preserve only non-token application rules and explicit component overrides. Delete the `@media (prefers-color-scheme: dark) { :root.dark { ... } }` block in `globals.css` in full as part of this step (see Global Constraints) — it is a second dark-mode activation mechanism, not a value duplicate to reconcile.

- [ ] **Step 5: Run generator and focused validation**

  Run: `cd itsm-frontend && node scripts/generate-design-tokens.mjs && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand`

  Expected: PASS; a second generator run produces no diff.

### Task 3: Make Theme Bootstrap Hydration-Safe

**Files:**
- Create: `itsm-frontend/src/components/layout/ThemeBootstrapScript.tsx`
- Modify: `itsm-frontend/src/app/layout.tsx`
- Modify: `itsm-frontend/src/components/layout/ThemeHtmlClassSync.tsx`
- Modify: `itsm-frontend/src/lib/design-system/theme.tsx`
- Test: `itsm-frontend/src/lib/design-system/__tests__/design-system.test.ts`

**Interfaces:**
- `ThemeBootstrapScript` emits a before-hydration script that reads the existing storage key and system preference, then sets only `html.dark` or `html.light` and `color-scheme`.
- `ThemeHtmlClassSync` remains the post-hydration synchronizer.
- `applyCSSVariables` is removed from the startup contract or reduced to an explicitly client-only utility used by a tested client boundary; it is never called from `layout.tsx`.

- [ ] **Step 1: Add tests for stored light, stored dark, and system preference**

  Test that the bootstrap source selects the stored mode first, falls back to `matchMedia`, and never references `document` during server module evaluation.

- [ ] **Step 2: Run the focused theme tests and confirm failure**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/design-system.test.ts --runInBand`

  Expected: FAIL for the new bootstrap behavior.

- [ ] **Step 3: Implement the bootstrap and client synchronization**

  Place the script in the root layout using the Next.js supported before-interactive mechanism. Keep token values in generated CSS so the script only selects the mode and cannot drift from the token source.

- [ ] **Step 4: Run focused tests and a production build check**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/design-system.test.ts --runInBand && npm run type-check`

  Expected: PASS with no server-side `document` error.

### Task 4: Align Ant Design And CSS Override Consumers

**Files:**
- Modify: `itsm-frontend/src/lib/design-system/theme.tsx`
- Modify: `itsm-frontend/src/lib/providers/AntdProvider.tsx`
- Modify: `itsm-frontend/src/app/globals.css`
- Modify: `itsm-frontend/src/styles/antd-layout-overrides.css`
- Test: `itsm-frontend/src/lib/design-system/__tests__/design-system.test.ts`

**Interfaces:**
- `getAntdTheme(isDark)` consumes only the canonical typed token exports.
- Component overrides use canonical CSS variables or Ant Design theme tokens; no undefined variable names remain.

- [ ] **Step 1: Add assertions for primary, status, background, border, radius, and shadow mappings**

  Cover both light and dark modes and assert that the Ant Design status values agree with the semantic contract rather than historical literal names.

- [ ] **Step 2: Implement the mappings and remove stale aliases**

  Replace hardcoded token values in the theme and CSS overrides with canonical variables or contract exports. Keep component-specific visual rules where they are not design tokens.

- [ ] **Step 3: Run focused tests and type-check**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/design-system.test.ts --runInBand && npm run type-check`

  Expected: PASS.

### Task 5: Migrate Semantic Color Call Sites

**Files:**
- Modify: the classified files under `itsm-frontend/src/app`, `itsm-frontend/src/components`, `itsm-frontend/src/lib`, and `itsm-frontend/src/constants`
- Create or modify: `itsm-frontend/src/lib/design-system/color-usage.ts`
- Test: affected Jest tests and `itsm-frontend/src/lib/design-system/__tests__/token-contract.test.ts`

**Interfaces:**
- `color-usage.ts` exposes static class names or CSS variable helpers for semantic status, brand, chart, and user-defined colors.
- Call sites use static Tailwind classes such as the generated status classes, Ant Design `useToken()` where a component token is required, or CSS variables for chart/user values.

- [ ] **Step 1: Generate a classification inventory**

  List every candidate historical color occurrence with file, expression type, and classification. Mark branding, user palette, chart series, status, test fixture, and intentional one-off values separately. Do not classify by hexadecimal value alone.

- [ ] **Step 2: Add regression tests for each consumer category**

  Cover at least one status badge, one Ant Design statistic/style object, one chart palette, one user-configurable color, and one test fixture. Assert the intended token source rather than the old literal.

- [ ] **Step 3: Migrate status consumers**

  Replace only semantically verified status uses. Preserve display colors for branding, chart sequences, CI type palettes, and user-selected values through approved palettes or validated values.

- [ ] **Step 4: Run affected tests after each module batch**

  Run the relevant Jest file(s) after each module batch, then run: `cd itsm-frontend && npm run type-check`

  Expected: PASS with no dynamic Tailwind class construction.

### Task 6: Migrate Arbitrary Typography And Establish Guards

**Files:**
- Modify: the 23 files containing `text-[10px]`, `text-[11px]`, or `text-[13px]`
- Create: `itsm-frontend/scripts/check-design-token-diff.mjs`
- Modify: `itsm-frontend/eslint.config.mjs`
- Modify: `itsm-frontend/package.json`
- Test: `itsm-frontend/src/lib/design-system/__tests__/token-contract.test.ts`

**Interfaces:**
- Business metadata labels use `text-xs` unless a documented semantic exception exists.
- `node scripts/check-design-token-diff.mjs` checks changed lines in tracked source files and honors the explicit allow-list.

- [ ] **Step 1: Add guard tests for allowed and forbidden examples**

  Test that a new arbitrary pixel font class and an unapproved raw color fail, while token source, generated CSS, user-defined color validation, chart palette, and test fixture examples pass.

- [ ] **Step 2: Run guard tests and confirm failure**

  Run: `cd itsm-frontend && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand`

  Expected: FAIL until the diff checker and rule configuration exist.

- [ ] **Step 3: Replace the 118 arbitrary font occurrences by semantic review**

  Convert the approved metadata/label cases to `text-xs`; record any genuine exception in the allow-list with a reason. Do not change unrelated arbitrary values merely because they contain pixels.

- [ ] **Step 4: Implement ESLint and diff guards**

  Use ESLint for full-tree TS/TSX restrictions after the baseline is clean. Use the diff checker for newly added CSS, `.mjs`, and other source literals that ESLint does not parse. Ensure the checker reports file and line for every violation.

- [ ] **Step 5: Run focused guard validation**

  Run: `cd itsm-frontend && npm run lint:check && node scripts/check-design-token-diff.mjs && npx jest src/lib/design-system/__tests__/token-contract.test.ts --runInBand`

  Expected: PASS with zero unapproved violations.

### Task 7: Full Validation And Visual Regression Evidence

**Files:**
- Modify: `itsm-frontend/tests/e2e/` only if a focused visual/theme check is missing
- Create: `docs/superpowers/reports/2026-08-28-frontend-design-token-unification-validation.md`
- Modify: the design spec only for verified final counts and commands

- [ ] **Step 1: Run the complete frontend checks**

  Run:

  ```bash
  cd itsm-frontend
  npm run type-check
  npm run lint:check
  npm run test:unit
  npm run build
  ```

  Expected: all commands pass.

- [ ] **Step 2: Run focused browser checks**

  Use the existing Playwright setup to verify login, ticket detail, Dashboard, service catalog, and admin pages at 390px and desktop width in both light and dark modes. Check theme switching, dropdowns, status tags, charts, and metadata text wrapping.

- [ ] **Step 3: Record evidence and remaining exceptions**

  Record commands, browser viewport sizes, screenshots, baseline counts, allow-listed exceptions, and any visual differences in the validation report.

- [ ] **Step 4: Update the spec with measured completion values**

  Replace provisional quantities with the generated baseline and final checker output. Keep backlog items separate from the completion criteria.

## Plan Self-Review

- The spec's single-source goal is covered by Tasks 1 and 2.
- Tailwind v4 compatibility is covered by Task 2; no task assumes direct Node execution of TypeScript.
- Hydration-safe theme initialization is covered by Task 3.
- CSS naming and duplicate variable cleanup are covered by Tasks 2 and 4.
- Semantic color classification is covered by Task 5; global hexadecimal replacement is explicitly prohibited.
- Typography migration and future enforcement are covered by Task 6.
- Reproducible counts, unit tests, type-check, lint, build, and responsive light/dark browser checks are covered by Task 7.
- The plan contains no open-ended implementation placeholders; each task has concrete files, interfaces, commands, and expected outcomes.
