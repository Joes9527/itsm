# Task 2 report — navigation and responsive layout

Status: DONE_WITH_CONCERNS. Implementation is complete and scoped checks pass. Browser breakpoint evidence remains assigned to the controller's integration gate.

## Implemented

- Made `LAYOUT_CONFIG` authoritative for the accepted 60px header, 224px expanded sidebar, 0px collapsed sidebar, 40px menu row, and `md` (768px) mobile cutoff. Removed the obsolete 64px/256px formulas and assertions.
- Migrated the active header/sidebar/main shell from fixed charcoal/light colors to Task 1 semantic variables. Removed the animated orange glow, strong sidebar shadow, capsule user trigger, and second-line role label. Preserved the real logo, backend menu source, breadcrumbs, search, AI permission gate, notification behavior, theme/language actions, persona switching, user menu, and error feedback.
- Added the compatible `showSidebarToggle?: boolean` Header prop and disabled that action in the portal branch.
- Added compact behavior: desktop search is 175px, spacing tightens below 1200px, search becomes an icon and names truncate below 992px, and notification/theme/language actions move into the mobile More menu below 768px.
- Completed the existing fixed mobile overlay: Escape, overlay click, and menu selection close it; Escape returns focus to the toggle; Tab/Shift+Tab remain in the open navigation; main content becomes inert and hidden from assistive technology while open. Desktop behavior has no focus trap.

## TDD evidence

- RED command: `npm test -- --runInBand --coverage=false src/app/(main)/__tests__/layout-auth.test.tsx src/components/layout/__tests__/navigation-theme.test.tsx`
- RED result: the new real Header test failed because `showSidebarToggle={false}` still rendered `button[aria-label="展开侧边栏"]` (1 failed / 1 executed). The shell mobile test was written before production changes, but this first invocation treated parentheses as a Jest regex and did not execute that path, so no valid pre-implementation RED output is claimed for it.
- GREEN command: `npm test -- --runInBand --coverage=false --runTestsByPath 'src/app/(main)/__tests__/layout-auth.test.tsx' src/components/layout/__tests__/navigation-theme.test.tsx`
- GREEN result: 2 suites, 8 tests passed. This includes portal toggle visibility, Escape close/focus return, mobile More-menu access, collapsed-navigation accessibility removal, and all three existing auth bootstrap cases.

## Validation

- Focused Jest: 2 suites / 8 tests passed.
- `npm run type-check`: passed; theme token generator check reported up to date and TypeScript emitted no errors.
- Scoped ESLint over all changed TS/TSX files: passed with no output.
- `git diff --check`: passed.
- No full build, backend, database, Docker, push, or merge was run.

## Files changed

- `itsm-frontend/src/app/(main)/layout.tsx`
- `itsm-frontend/src/app/(main)/__tests__/layout-auth.test.tsx`
- `itsm-frontend/src/config/layout.config.ts`
- `itsm-frontend/src/components/layout/header/{Header.tsx,Header.module.css,PersonaSwitcher.tsx,UserMenuDropdown.tsx}`
- `itsm-frontend/src/components/layout/sidebar/{Sidebar.tsx,Sidebar.module.css,MenuItems.tsx}`
- `itsm-frontend/src/components/layout/__tests__/navigation-theme.test.tsx`

## Self-review and concerns

- The mobile shell test exercises the real MainLayout behavior but substitutes minimal Header/Sidebar renderers so API and Ant Design behavior do not obscure the shell state transitions. The separate Header test exercises the real Header and mocks router/store/service boundaries.
- Controller browser evidence at 390px confirmed open/Escape/focus-return. The same review found the collapsed Ant Design sidebar remained visible to accessibility APIs and the 768px header overflowed; this task then made collapsed navigation inert/hidden and moved desktop tools into More throughout 768–991px. Those fixes have automated coverage, while browser re-verification and the remaining 767/768, 991/992, 1024/1200/1440 geometry and long-label checks remain at the controller integration gate.
- Task 1's primary-button hover color issue reported by the controller was not touched in this task.
