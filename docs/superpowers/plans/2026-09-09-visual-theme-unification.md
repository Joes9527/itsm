# Visual Theme Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** accepted for execution by user instruction “do it”.
**Goal:** Deliver the approved A/C visual system across shipped ITSM pages, preserving all business behavior.
**Architecture:** One authoritative theme token source feeds CSS and Ant Design; the existing ThemeProvider owns preference. Existing navigation and business components are migrated in sequential batches, with the complete route inventory tracking completion.
**Tech Stack:** Next.js 15, React 19, Ant Design 6, TypeScript, Tailwind 4, Jest, browser verification.
**Spec:** `docs/superpowers/specs/2026-09-09-navigation-themes-design.md`
**Worktree:** `/Users/julian/.worktrees/itsm-navigation-themes`
**Branch / base:** `codex/feat/visual-theme-unification` / `a25e108d2`

## Global Constraints

- A/C share structure, typography and dimensions; default light, preserve saved light/dark/system in `itsm-theme`.
- Primary background is `#F06820`; primary button text and icons are `#FFFFFF` in both themes. This explicit user choice supersedes earlier dark button text. Keep semantic success/warning/error colors.
- Font stack: `Inter, -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei', sans-serif`; no remote font download.
- Header 60px, sidebar 224px expanded / 0 collapsed; mobile cutoff `<768px`, compact tools below 992px, narrow spacing below 1200px.
- Navigation 13px; page titles 24px/600; card titles 15px/600; body/table 13px; helper 11–12px; business buttons 34px / small 29px; card radius 8px and button radius 6px.
- A backgrounds: page `#F5F6F8`, surface `#FFFFFF`, raised `#F8F9FB`; C: page `#1C2025`, surface `#252A30`, raised `#2C323A`.
- Existing ThemeProvider, API calls, tenant/RBAC, menu data, workflows and business semantics remain authoritative. No backend/database writes, schema changes, Docker, demo-data seeding, pushes or merges.
- All shipped pages, including auth, portal, workspaces, admin, detail/create/edit, are in scope. Charts/editors retain professional semantics but require theme readability. Only evidenced development/demo routes may be excluded in the inventory.
- Do not delete, rename or reorganize existing tests/history. Do not turn runtime permissions into preview/static menus. Do not add global utility-color overrides that silently recolor unrelated semantics.
- Use test-first for new behavioral logic; style-only edits use focused existing regression checks and browser verification, not tests that mirror CSS constants.
- The inventory is created before production edits. No row is “verified” without recorded browser evidence; use static-migrated/pending-browser distinctions. Shared mock fixtures may exercise browser appearance but must be labeled separately from live-backend verification.
- New production files have one responsibility. No new UI framework, dependencies or parallel theme store. Remove replaced contradictory style declarations in affected paths.

## File map and test conventions

All source paths below are relative to `itsm-frontend/` unless prefixed `docs/`. The controller copies the read-only route inventory to `docs/superpowers/plans/2026-09-09-visual-theme-route-inventory.md` before Task 1. Each migration task updates only its own inventory rows. It names exact route files and their imported component ownership; inspect those imports without unrelated refactors.

Use `npm run type-check`, `npx eslint <changed TS/TSX files>`, `npx jest --runInBand --coverage=false --runTestsByPath <named tests>` from the frontend directory. Baseline output is `/tmp/itsm-visual-baseline-types.log` and `/tmp/itsm-visual-baseline-tests.log`. Do not hide baseline failures or run auto-fixing lint across untouched files.

### Task 1: Theme foundation and preference restoration

**Files:**
- Create `src/design-system/theme-tokens.json` (authoritative semantic colors/typography/dimensions), `scripts/generate-theme-tokens.mjs`, `src/styles/generated-theme-tokens.css`.
- Modify `src/lib/design-system/theme.tsx`, `colors.ts`, `spacing.ts`, `src/design-system/tokens/index.ts`, `src/styles/theme-variables.css`, `src/styles/antd-layout-overrides.css`, `src/app/globals.css`, `tailwind.config.mjs`, `src/app/layout.tsx`, `src/lib/providers/AntdProvider.tsx`, `src/components/layout/ThemeHtmlClassSync.tsx`, `package.json`.
- Create `src/lib/design-system/theme-preference.ts`, `src/lib/design-system/__tests__/theme-preference.test.ts`, `src/lib/design-system/__tests__/theme-provider.test.tsx`; update affected existing design-system tests only when their expectations intentionally changed.

**Interfaces:**
- Preserve `ThemeMode`, `useTheme()`, `setMode(mode)`, `toggleTheme()`, `getAntdTheme(isDark)`. `toggleTheme()` becomes resolved light/dark flip; `setMode('system')` remains supported.
- Export `parseThemeMode(value: unknown): ThemeMode`, `resolveIsDark(mode: ThemeMode, systemDark: boolean): boolean` from theme-preference.ts. Shared bootstrap code uses these semantics without a second persistence mechanism.
- CSS consumers use `--color-bg-primary` surface, `--color-bg-secondary` page, `--color-bg-tertiary` raised, `--color-text-primary`, `--color-text-secondary`, `--color-border`, `--color-primary`, `--color-primary-foreground`, `--color-selected-bg`, `--color-selected-text`, `--font-family-base`. Preserve existing equivalent CSS variable names as references, not independent literals; record exact mapping in report.

- [ ] Run focused baseline design tests. Add failing behavioral tests before provider edits. Example required assertions:
```ts
expect(parseThemeMode('invalid')).toBe('light');
expect(resolveIsDark('system', true)).toBe(true);
expect(resolveIsDark('light', true)).toBe(false);
// Provider harness renders mode/isDark and buttons calling setMode/toggleTheme.
// Set localStorage itsm-theme=dark before mount; assert rendered dark, then
// toggle and assert light; unmount/remount must retain light. Storage throws
// must not prevent in-session switching. Simulated media changes affect
// system mode but never explicitly selected light/dark.
```
- [ ] Build one JSON schema with brand, light/dark semantic maps, font and component sizes. Generator emits deterministic CSS for :root/.dark and supports `--check`; package scripts generate before dev/build and check in validation. TS and Tailwind read same JSON. Existing palette families needed by semantic charts may remain, but duplicate surface/brand/font literals are replaced with imports/references.
- [ ] Resolve stored preference before writing; tolerate unavailable storage and invalid values. Apply root class/color-scheme before first paint through safe bootstrap using same resolver; provider shows theme-neutral structure until restored, without new warning suppression or CSP weakening.
- [ ] Wire Ant Design Button/Card/Table/Input/Select/Dropdown/Modal/Drawer and typography to the shared tokens; primary buttons and their hover/active labels stay white. Remove conflicting global primary and hardcoded light popup rules rather than adding a final blanket override. Maintain portal-rendered popup theming.
- [ ] Run new tests and generation consistency, scoped lint/type checks. Commit foundation and document interfaces, RED/GREEN evidence, baseline issues and actual source-of-truth mapping in report.

### Task 2: Navigation and responsive layout

**Files:** `src/app/(main)/layout.tsx`, `src/config/layout.config.ts`, `src/components/layout/header/{Header.tsx,Header.module.css,GlobalSearch.tsx,PersonaSwitcher.tsx,UserMenuDropdown.tsx}`, `src/components/layout/sidebar/{Sidebar.tsx,Sidebar.module.css,MenuItems.tsx}`, existing active shared layout references. Create `src/components/layout/__tests__/navigation-theme.test.tsx` and focused mobile behavior test adjacent to the responsible component.

**Interfaces:** Consume Task 1 variables and existing `useTheme`. Existing `Header` props remain compatible; add explicit `showSidebarToggle?: boolean` so portal does not expose a dead action. Sidebar keeps backend menus and `collapsed`, `onCollapse`, `mobile` contract. Layout dimensions stay authoritative in `LAYOUT_CONFIG`.

- [ ] Add test-first for mobile open/Escape/close/focus return and portal toggle visibility, using actual navigation component with only API/router boundaries mocked. Preserve layout-auth tests. Example behavior:
```tsx
// render the active layout harness at width 390 with authenticated fixture;
// activate sidebar toggle, assert navigation visible, press Escape,
// assert navigation hidden and toggle focused; desktop width must not
// expose a modal focus trap. Portal must have no sidebar toggle.
```
- [ ] Replace fixed charcoal/white styles with semantic tokens. Remove glow animation, capsule user trigger and second-line role from header only. Use 175px search, single-line names, consistent icons/radii/heights; preserve notifications/language/search/workspace/user/other current actions and error feedback.
- [ ] Set 60px/224px/0 dimensions and actual main layout offset; remove obsolete 64px formula assertions. Coordinate `<768` mobile overlay, 992 compact tools, 1200 spacing. On mobile use existing fixed sidebar overlay with focus containment, Escape/overlay/menu close and trigger focus return; background non-interactive. Keep portal side-nav-free. No new Drawer implementation.
- [ ] Validate 767/768/991/992 transitions and four design widths; test long labels, keyboard search, all tools accessible through mobile More menu. Run focused tests, lint/type checks, commit and report.

### Task 3: Ticket and service catalog sample pages

**Files:** `src/app/(main)/tickets/page.tsx`, `src/components/ticket/{TicketList.tsx,TicketAdvancedSearch.tsx,TicketKanban.tsx}`, `src/app/(main)/service-catalog/page.tsx`, `src/app/(main)/service-catalog/components/ServiceItemCard.tsx`, local imported styling components and exact descendant routes listed under tickets/catalog in inventory. Optional colocated CSS modules for page-specific presentation only.

**Interfaces:** Consume Task 1 tokens and existing API/hooks; preserve query filters, pagination, card/list switch, approvals/sort, menus and all existing handlers. No API contract changes.

- [ ] Run existing focused ticket/catalog tests (identify from inventory); record baseline. Capture current sample structure before edits.
- [ ] Apply approved preview density: 24px titles, 13px content, restrained statistics, flat service icons, 8px cards, consistent fields/buttons. Replace literal light surfaces with semantic variables and remove conflicting old classes. Use existing category/icon selection unchanged, only its visual presentation changes.
```tsx
// Presentation pattern, using existing real data and handlers:
<section style={{ background: 'var(--color-bg-primary)',
  color: 'var(--color-text-primary)', borderRadius: 8 }}>
  {children}
</section>
// Do not introduce this wrapper where an existing Card already supplies it.
```
- [ ] Preserve all columns and actions; no fake metrics. Card grid uses `repeat(auto-fit,minmax(min(240px,100%),1fr))` with desktop max four columns when space permits; list view wraps on mobile. Table scroll remains in table container.
- [ ] Exercise actual sample components in both themes with empty/loading/error/long labels and dropdowns; label fixtures as fixtures. Run affected regressions and lint/type check, update sample inventory rows honestly, commit.

### Task 4: Shared business components, authentication and persona pages

**Files:** inventory group auth/portal/workspaces and imported components; `src/components/layout/{PageContainer.tsx,PageHeader.tsx,PageLayout.tsx,BusinessPageTemplate.tsx}`, `src/components/work-item/WorkItemShell.tsx`, `src/components/ui/{typography.css,Button.tsx}` if present; `src/app/(auth)/{layout.tsx,login/page.tsx,register/page.tsx}`, `src/lib/antd-theme.ts` and its actual callers.

**Interfaces:** Existing component props unchanged. Auth ConfigProviders inherit Task 1 resolved theme; no hardcoded `getAntdTheme(false)` active path. Shared primitives use CSS semantic variables instead of a second ThemeProvider.

- [ ] Run existing WorkItemShell, BusinessPageTemplate, auth tests. Add a behavioral regression only if removing nested theme providers exposes a propagation bug.
- [ ] Migrate shared surfaces/title/toolbar/button/table primitives without broad CSS selectors targeting every div. Remove static light auth overrides and adapt actual auth/portal/workspace layouts and local cards. Keep portal composition and login form/redirect logic intact.
```tsx
// Local ConfigProvider may retain locale but must inherit theme:
<ConfigProvider locale={zhCN}>{children}</ConfigProvider>
// No theme={getAntdTheme(false)} beneath the root theme provider.
```
- [ ] Follow exact inventory rows for other persona/onboarding/dashboard routes; preserve chart semantic colors. Verify primary white labels, modal fields, stored-dark login and persona transitions. Update rows and run scoped checks; commit.

### Task 5: Service operations and asset pages

**Files:** inventory operations group: incident/problem/change/release/service-request/approval/SLA and asset/license/team/notification routes plus their imported domain components. Includes all detail/create/edit descendants explicitly listed in inventory. Excludes shared foundation already owned by previous tasks except concrete integration fix.

**Interfaces:** All domain APIs, state machines, validation and permission checks unchanged; surface/table/card/action styles consume shared semantic variables.

- [ ] Inspect inventory files and nearest tests; run affected baseline. For each file classify literal color by surface vs business status before replacement.
- [ ] Replace hardcoded neutral light surfaces/text/borders with semantic styles; use 13px body and coherent heading/card sizes; convert brand-only gradients/actions to orange/white while leaving risk, priority and status colors semantic. Preserve empty/loading/error and modal forms.
```tsx
// Replace neutral surface, never semantic status coloring:
style={{ background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)' }}
// Existing <Tag color={statusColor}> remains domain-owned.
```
- [ ] Verify each inventory row A/C or record precise pending browser reason. Run focused domain regressions and scoped lint/type checks; commit migration with exact file/route evidence in report.

### Task 6: Knowledge, CMDB and workflow surfaces

**Files:** inventory knowledge/cmdb/workflow group and imported `src/components/{knowledge,cmdb,workflow}` presentation files, actual chart/topology/editor containers.

**Interfaces:** Existing workflow execution/modeler data, retrieval, graph structures and editing behavior unchanged. Use existing library theme props or canvas host CSS for readability; no new graph layout or rule inference.

- [ ] Run relevant existing UI/domain tests. Inspect editor/provider theme API in installed types before modifying integration.
- [ ] Migrate page/card/table/modal/filter backgrounds and typography, plus graph/editor chrome, keeping graph semantic strokes and node meaning. Ensure text on graph/canvas remains readable in C; retain white drawing sheets if the library only supports white, explicitly style readable chrome and record bounded visualization treatment rather than silently marking entire route excluded.
```tsx
// Theme a container; use the visualization library's established appearance API:
<div style={{ background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)' }} />
// Do not invert/color-filter canvas output or recolor business data by keywords.
```
- [ ] Verify representative graph/modeler/knowledge interactions using non-mutating fixtures where needed; preserve persisted data. Update all rows and run scoped checks; commit.

### Task 7: Administration, reports and residual shipped routes

**Files:** inventory admin/reports and all unassigned shipped routes; actual imported admin/report/shared presentation components not yet migrated. Inventory is exhaustive; do not exclude pages merely because not one of the samples.

**Interfaces:** Admin RBAC, menus and settings APIs unchanged; report series and chart business colors unchanged; shared tokens flow from root.

- [ ] Run relevant existing tests; inspect remaining static styles through inventory and import graph.
- [ ] Migrate admin/forms/reports dashboards and any residual neutral backgrounds/text/cards/buttons, including modals/popovers. Keep operator controls and permission-based visibility intact. Use same replacement discipline as Task 5 and no bulk recoloring of semantic blue/red/green tokens.
- [ ] Reconcile inventory against actual route files: every shipped route has an owner, migration record and verification status; no duplicates or missing dynamic descendants. Classify any genuine dev-only routes with source evidence.
- [ ] Run scoped lint/type checks and affected tests, update rows, commit and report.

### Task 8: Integrated validation and delivery evidence

**Files:** `tests/e2e/flows/visual-theme.spec.ts`, optional `tests/e2e/fixtures/visual-theme.ts` using mocked transport only; route inventory; design status; permanent developer instructions only if generation workflow changed. No screenshots/logs committed.

**Interfaces:** Use isolated local frontend port 3017, not an existing user process. Fixture backend interception is allowed for UI-only verification, clearly separate from real integration. No credentials in artifacts or logs.

- [ ] Add durable focused browser regression cases for theme persistence, sample page UI, mobile focus/overlay and white primary text. Test mocked transport contracts through actual pages, not reimplemented preview HTML. Browser UI interaction uses available CUA APIs; any terminal browser tooling must follow current tool permissions.
```ts
// Acceptance assertions to retain in the UI suite:
await expect(page.getByRole('button', { name: /发起申请/ })).toHaveCSS('color', 'rgb(255, 255, 255)');
// Set theme via UI, reload, assert html.dark and the same page title remain.
// At 390px open sidebar, Escape closes and focus returns to trigger.
```
- [ ] Run generated token consistency, full frontend type-check, build and the relevant test set once. Compare baseline errors rather than masking failures. Launch the isolated frontend, visually inspect A/C for both samples and one representative per distinct page layout/component family; complete all route A/C appearance checks with precise fixture/live labels.
- [ ] Check loading/error/empty/long text, default and saved theme, system preference, storage denial, auth/portal/console, 390/768/1024/1440 and 767/768/991/992 boundaries, dropdowns/focus and primary button white text. Resolve implementation regressions with focused tests.
- [ ] Update inventory with evidence paths (local screenshot references remain non-committed output links), final status and honest limitations. A failed or inaccessible row remains pending; do not claim full completion until resolved. Update spec implemented only if all acceptance passed. `git diff --check`, commit, full report for final independent branch review.

## Execution bookkeeping

Use `/Users/julian/.agents/skills/subagent-driven-development/scripts/sdd-workspace` for this plan; one task brief/report/review package per task. Fresh implementer per task, sequential only. Record BASE before every dispatch. Complete each task only after independent spec and quality review. Final review covers branch from `a25e108d2`; do not push, merge or deploy without separate authorization.
