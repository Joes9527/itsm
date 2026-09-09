# ITSM visual unification route inventory

Final implementation inventory. Worktree: `/Users/julian/.worktrees/itsm-navigation-themes`; branch: `codex/feat/visual-theme-unification`; verified production build: `567d8718e`. Design source: `docs/superpowers/specs/2026-09-09-navigation-themes-design.md`. All 144 shipped routes have CUA appearance or canonical-redirect evidence in A/light and C/dark against the isolated read-only fixture; this is not live-backend or exhaustive interaction acceptance.

## Coverage and layout evidence

- Page files found: **146**; shipped/in-scope routes: **144**; evidenced development-only exclusions: **2**.
- `src/app/layout.tsx` is the root provider layout. `src/app/(auth)/layout.tsx` wraps authentication pages. `src/app/(main)/layout.tsx` is the effective application layout and selects its portal branch only for `/portal`; all other pages in that group use the console branch. `src/app/(main)/tickets/layout.tsx` additionally wraps every `/tickets/**` route. `/onboarding/wizard` has only the root layout.
- Route groups below are implementation batches, not menu, RBAC, or product changes.

## Shared theme, component, and audit roots

- Theme/provider roots: `src/lib/design-system/theme.tsx`, `src/lib/design-system/colors.ts`, `src/design-system/tokens/index.ts`, `src/lib/antd-theme.ts`, `src/lib/providers/AntdProvider.tsx`, `src/components/layout/ThemeHtmlClassSync.tsx`.
- Style roots: `src/styles/theme-variables.css`, `src/app/globals.css`, `src/components/ui/design-system.css` and Tailwind declarations. Current source has conflicting 48px/260px CSS layout values versus approved 60px/224px values, plus literal-color Ant Design popup overrides.
- Navigation: `src/components/layout/header/**` and `src/components/layout/sidebar/**`; top-level Header/Sidebar files are re-export entries. Shared content surfaces include layout templates/containers, `src/components/ui/**`, common stats/loading/error/status components, and domain components.
- Existing `npm run audit:ui` runs `tools/generate-ui-audit-inventory.mjs`, but scans only `src/app/(main)` and uses heuristic batches/types. It misses auth, onboarding, and root demo pages, so it must be extended before becoming the acceptance ledger. Browser foundations: `playwright.config.ts`, `tests/e2e/responsive-layout.spec.ts`, `tests/e2e/screenshot*.{ts,spec.ts}`, auth utilities, and domain/role flows.

## auth/portal/workspaces (14)

| Route | Source | Actual layout | Common components observed | Local risk | Status |
| --- | --- | --- | --- | --- | --- |
| `/forgot-password` | `src/app/(auth)/forgot-password/page.tsx` | Root > Auth | page-local Ant Design/local sections | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/login` | `src/app/(auth)/login/page.tsx` | Root > Auth | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/register` | `src/app/(auth)/register/page.tsx` | Root > Auth | page-local Ant Design/local sections | literal colors; fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/reset-password` | `src/app/(auth)/reset-password/page.tsx` | Root > Auth | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/ai/chat` | `src/app/(main)/ai/chat/page.tsx` | Root > Main (Console) | ai/AIChat | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/dashboard` | `src/app/(main)/dashboard/page.tsx` | Root > Main (Console) | dashboard/KPICards, ChartsSection, QuickActions, DashboardChartCard and seven chart components | semantic surfaces; native classic/classicDark canvas theme; series colors retained | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/executive/dashboard` | `src/app/(main)/executive/dashboard/page.tsx` | Root > Main (Console) | ai/AIConfidenceBadge | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/manager/live-board` | `src/app/(main)/manager/live-board/page.tsx` | Root > Main (Console) | ai/AIConfidenceBadge | literal colors; fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/my-requests/[requestId]` | `src/app/(main)/my-requests/[requestId]/page.tsx` | Root > Main (Console) | redirect-only Spin; target TicketDetail/ServiceRequestPanel (Task 3) | no local surface; redirect behavior unchanged | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/my-requests` | `src/app/(main)/my-requests/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/portal` | `src/app/(main)/portal/page.tsx` | Root > Main (Portal branch) | portal/HeroSearchBar, portal/ManagerPendingApprovals | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/profile` | `src/app/(main)/profile/page.tsx` | Root > Main (Console) | layout/PageHeader | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/workspace/tickets` | `src/app/(main)/workspace/tickets/page.tsx` | Root > Main (Console) | workspace/SLACountdownTimer, workspace/AISimilarSolutionsPanel, ai/AIConfidenceBadge | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/onboarding/wizard` | `src/app/onboarding/wizard/page.tsx` | Root only | page-local Ant Design/local sections | literal colors; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |

Task 4 static evidence: four auth routes inherit the root ConfigProvider; exact tracked password sources were inspected (ignore-filtered file search misses them). Portal imports HeroSearchBar/ManagerPendingApprovals; workspace imports AISimilarSolutionsPanel/SLACountdownTimer/AIConfidenceBadge (semantic confidence/SLA palettes retained). AI chat imports AIChat and inherits Ant Design state surfaces. Onboarding keeps existing step/skip/persona actions. Profile uses shared PageHeader and semantic local surface colors. Shared PageContainer, PageHeader, PageLayout, BusinessPageTemplate, WorkItemShell/SLA/action helper, Button and typography migrated. `/my-requests/[requestId]` is an existing redirect-only route; its Spin already inherits theme. `/ai/chat` delegates all presentation to migrated AIChat. No route marked browser-verified. Focused existing 11-suite regression passed (37 passed, 6 pre-existing skipped); final evidence and pending browser matrix in `.superpowers/sdd/2026-09-09-visual-theme-unification/task-4-report.md`.

## tickets/catalog (17)

| Route | Source | Actual layout | Common components observed | Local risk | Status |
| --- | --- | --- | --- | --- | --- |
| `/service-catalog/detail/[id]` | `src/app/(main)/service-catalog/detail/[id]/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/service-catalog/edit/[id]` | `src/app/(main)/service-catalog/edit/[id]/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/service-catalog` | `src/app/(main)/service-catalog/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/service-catalog/request/[id]` | `src/app/(main)/service-catalog/request/[id]/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/service-requests/[id]` | `src/app/(main)/service-requests/[id]/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/service-requests` | `src/app/(main)/service-requests/page.tsx` | Root > Main (Console) | service-request/ServiceRequestList | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/templates` | `src/app/(main)/templates/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/[ticketId]` | `src/app/(main)/tickets/[ticketId]/page.tsx` | Root > Main (Console) > Tickets | ticket/TicketDetail | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/ai-create` | `src/app/(main)/tickets/ai-create/page.tsx` | Root > Main (Console) > Tickets | a2ui/A2UIFormRenderer | fixed palette classes | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/analytics` | `src/app/(main)/tickets/analytics/page.tsx` | Root > Main (Console) > Tickets | page-local Ant Design/local sections | table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/cc` | `src/app/(main)/tickets/cc/page.tsx` | Root > Main (Console) > Tickets | page-local Ant Design/local sections | fixed palette classes; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/create` | `src/app/(main)/tickets/create/page.tsx` | Root > Main (Console) > Tickets | work-item/CreationAttempts, work-item/CreationRequester | literal colors; fixed palette classes; inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/dashboard` | `src/app/(main)/tickets/dashboard/page.tsx` | Root > Main (Console) > Tickets | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets` | `src/app/(main)/tickets/page.tsx` | Root > Main (Console) > Tickets | ticket/TicketList, ticket/TicketKanban, ticket/TicketAdvancedSearch | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/templates/[id]` | `src/app/(main)/tickets/templates/[id]/page.tsx` | Root > Main (Console) > Tickets | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/templates` | `src/app/(main)/tickets/templates/page.tsx` | Root > Main (Console) > Tickets | common/CustomFieldsEditor | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |
| `/tickets/types` | `src/app/(main)/tickets/types/page.tsx` | Root > Main (Console) > Tickets | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 3) |

## operations/assets/licenses/teams/notifications (51)

| Route | Source | Actual layout | Common components observed | Local risk | Status |
| --- | --- | --- | --- | --- | --- |
| `/notifications` | `src/app/(main)/notifications/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/applications` | `src/app/(main)/applications/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/approvals` | `src/app/(main)/approvals/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; table states; special visual | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/assets/[id]/edit` | `src/app/(main)/assets/[id]/edit/page.tsx` | Root > Main (Console) | asset/AssetForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/assets/[id]` | `src/app/(main)/assets/[id]/page.tsx` | Root > Main (Console) | asset/AssetDetail | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/assets/new` | `src/app/(main)/assets/new/page.tsx` | Root > Main (Console) | asset/AssetForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/assets` | `src/app/(main)/assets/page.tsx` | Root > Main (Console) | asset/AssetList | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes/[id]/edit` | `src/app/(main)/changes/[id]/edit/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes/[id]` | `src/app/(main)/changes/[id]/page.tsx` | Root > Main (Console) | change/ChangeDetail, business/detail-tabs, work-item/WorkItemShell, work-item/WorkItemTypes | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes/[id]/pir` | `src/app/(main)/changes/[id]/pir/page.tsx` | Root > Main (Console) | layout/PageContainer | literal colors; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes/new` | `src/app/(main)/changes/new/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes` | `src/app/(main)/changes/page.tsx` | Root > Main (Console) | layout/BusinessPageTemplate, change/ChangeList, business/UnifiedKanbanBoard | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/changes/pirs` | `src/app/(main)/changes/pirs/page.tsx` | Root > Main (Console) | layout/PageContainer | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/enterprise/departments` | `src/app/(main)/enterprise/departments/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/enterprise/teams` | `src/app/(main)/enterprise/teams/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/improvements/[id]` | `src/app/(main)/improvements/[id]/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/improvements/new` | `src/app/(main)/improvements/new/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/improvements` | `src/app/(main)/improvements/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/incidents/[id]/edit` | `src/app/(main)/incidents/[id]/edit/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/incidents/[id]` | `src/app/(main)/incidents/[id]/page.tsx` | Root > Main (Console) | incident/IncidentDetail, work-item/WorkItemShell, work-item/WorkItemTypes | literal colors; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/incidents/create` | `src/app/(main)/incidents/create/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/incidents` | `src/app/(main)/incidents/page.tsx` | Root > Main (Console) | layout/BusinessPageTemplate, business/BatchActionBar, business/UnifiedKanbanBoard | literal colors; fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/installations` | `src/app/(main)/installations/page.tsx` | Root > Main (Console) | ui/Badge, ui/Button, ui/Card, ui/Input, ui/Select | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/licenses/[id]/edit` | `src/app/(main)/licenses/[id]/edit/page.tsx` | Root > Main (Console) | license/LicenseForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/licenses/[id]` | `src/app/(main)/licenses/[id]/page.tsx` | Root > Main (Console) | license/LicenseDetail | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/licenses/new` | `src/app/(main)/licenses/new/page.tsx` | Root > Main (Console) | license/LicenseForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/licenses` | `src/app/(main)/licenses/page.tsx` | Root > Main (Console) | license/LicenseList | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/marketplace/[id]` | `src/app/(main)/marketplace/[id]/page.tsx` | Root > Main (Console) | ui/Badge, ui/Button, ui/Card, ui/Tabs | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/marketplace` | `src/app/(main)/marketplace/page.tsx` | Root > Main (Console) | ui/Card, ui/Button, ui/Input, ui/Select, ui/Tabs, ui/Badge | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/msp/management` | `src/app/(main)/msp/management/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/msp` | `src/app/(main)/msp/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems/[id]/edit` | `src/app/(main)/problems/[id]/edit/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems/[id]` | `src/app/(main)/problems/[id]/page.tsx` | Root > Main (Console) | problem/ProblemDetail, problem/ProblemAssociationsTab, work-item/WorkItemShell, work-item/WorkItemTypes | literal colors; fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems/known-errors` | `src/app/(main)/problems/known-errors/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems/new` | `src/app/(main)/problems/new/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems` | `src/app/(main)/problems/page.tsx` | Root > Main (Console) | layout/BusinessPageTemplate, problem/ProblemList, business/UnifiedKanbanBoard | literal colors; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/problems/trends` | `src/app/(main)/problems/trends/page.tsx` | Root > Main (Console) | layout/PageContainer | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/projects` | `src/app/(main)/projects/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/releases/[id]/edit` | `src/app/(main)/releases/[id]/edit/page.tsx` | Root > Main (Console) | release/ReleaseForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/releases/[id]` | `src/app/(main)/releases/[id]/page.tsx` | Root > Main (Console) | release/ReleaseDetail, business/detail-tabs | fixed palette classes; inline styles | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/releases/new` | `src/app/(main)/releases/new/page.tsx` | Root > Main (Console) | release/ReleaseForm | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/releases` | `src/app/(main)/releases/page.tsx` | Root > Main (Console) | release/ReleaseList | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/sla-dashboard` | `src/app/(main)/sla-dashboard/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/sla-monitor` | `src/app/(main)/sla-monitor/page.tsx` | Root > Main (Console) | business/SLAMonitorDashboard | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/sla/definitions/[id]` | `src/app/(main)/sla/definitions/[id]/page.tsx` | Root > Main (Console) | sla/SLADetail | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/sla` | `src/app/(main)/sla/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/standard-changes` | `src/app/(main)/standard-changes/page.tsx` | Root > Main (Console) | work-item/CreationAttempts, work-item/CreationRequester | fixed palette classes; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/system/organization` | `src/app/(main)/system/organization/page.tsx` | Root > Main (Console) | common/DepartmentTree | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/system/users` | `src/app/(main)/system/users/page.tsx` | Root > Main (Console) | common/UserList | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/tags` | `src/app/(main)/tags/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |
| `/teams` | `src/app/(main)/teams/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC-MIGRATED / FIXTURE-BROWSER-VERIFIED A/C |

## knowledge/cmdb/workflows (25)

| Route | Source | Actual layout | Common components observed | Local risk | Status |
| --- | --- | --- | --- | --- | --- |
| `/cmdb/ci-types` | `src/app/(main)/cmdb/ci-types/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | redirect retained; target /admin/cmdb-types owned by Task7 | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/ci` | `src/app/(main)/cmdb/ci/page.tsx` | Root > Main (Console) | cmdb/CIList, ui/ManagementPageHeader | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cis/[id]/edit` | `src/app/(main)/cmdb/cis/[id]/edit/page.tsx` | Root > Main (Console) | cmdb/CIEditorForm, cmdb/useUnsavedChangesGuard, cmdb/ci-editor-shared, ui/ManagementPageHeader | special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cis/[id]` | `src/app/(main)/cmdb/cis/[id]/page.tsx` | Root > Main (Console) | cmdb/ci-detail/CIDetail | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cis/create` | `src/app/(main)/cmdb/cis/create/page.tsx` | Root > Main (Console) | cmdb/CIEditorForm, cmdb/useUnsavedChangesGuard, cmdb/ci-editor-shared, ui/ManagementPageHeader | special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cloud-accounts` | `src/app/(main)/cmdb/cloud-accounts/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cloud-resources` | `src/app/(main)/cmdb/cloud-resources/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/cloud-services` | `src/app/(main)/cmdb/cloud-services/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb` | `src/app/(main)/cmdb/page.tsx` | Root > Main (Console) | cmdb/CSDMHub | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/reconciliation` | `src/app/(main)/cmdb/reconciliation/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/registry` | `src/app/(main)/cmdb/registry/page.tsx` | Root > Main (Console) | ui/ManagementPageHeader, ui/StatsOverview | literal colors; fixed palette classes; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/relationships` | `src/app/(main)/cmdb/relationships/page.tsx` | Root > Main (Console) | cmdb/CIRelationshipManager, ui/ManagementPageHeader | inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/cmdb/topology` | `src/app/(main)/cmdb/topology/page.tsx` | Root > Main (Console) | layout/PageContainer | literal colors; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/knowledge/articles/[id]` | `src/app/(main)/knowledge/articles/[id]/page.tsx` | Root > Main (Console) | knowledge/ArticleDetail | inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/knowledge/articles/new` | `src/app/(main)/knowledge/articles/new/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/knowledge` | `src/app/(main)/knowledge/page.tsx` | Root > Main (Console) | knowledge/ArticleList | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/knowledge/reviews` | `src/app/(main)/knowledge/reviews/page.tsx` | Root > Main (Console) | layout/PageContainer, common/SafeContent | literal colors; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/audit` | `src/app/(main)/workflow/audit/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/bottlenecks` | `src/app/(main)/workflow/bottlenecks/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/dashboard` | `src/app/(main)/workflow/dashboard/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/designer` | `src/app/(main)/workflow/designer/page.tsx` | Root > Main (Console) | workflow/designer | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/instances` | `src/app/(main)/workflow/instances/page.tsx` | Root > Main (Console) | ui/FilterToolbarCard, ui/LoadingEmptyError, ui/ManagementPageHeader, ui/StatsOverview | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/sla` | `src/app/(main)/workflow/sla/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/ticket-approval` | `src/app/(main)/workflow/ticket-approval/page.tsx` | Root > Main (Console) | workflow/BPMNDesigner | fixed palette classes; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |
| `/workflow/versions` | `src/app/(main)/workflow/versions/page.tsx` | Root > Main (Console) | ui/FilterToolbarCard, ui/LoadingEmptyError, ui/ManagementPageHeader, ui/StatsOverview | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C |

Task6 static treatment: both ReactFlow hosts/nodes/labels/controls/minimap use semantic theme colors; topology and modeler data/layout are preserved. BPMN retains its white drawing sheet and dark inherited canvas text; custom and library chrome is theme-aware. This is bounded visualization treatment, not a route exclusion. Appearance and interactions remain pending controller browser evidence. The six route files without local styling changes inherit their migrated active components or shared Antd foundation (including cloud-services).

## admin/reports (37)

| Route | Source | Actual layout | Common components observed | Local risk | Status |
| --- | --- | --- | --- | --- | --- |
| `/admin/approval-chains` | `src/app/(main)/admin/approval-chains/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/cmdb-types` | `src/app/(main)/admin/cmdb-types/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/config-inheritance` | `src/app/(main)/admin/config-inheritance/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/connectors` | `src/app/(main)/admin/connectors/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/department-processes` | `src/app/(main)/admin/department-processes/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/departments` | `src/app/(main)/admin/departments/page.tsx` | Root > Main (Console) | common/OrgDepartmentTree | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/escalation-matrices` | `src/app/(main)/admin/escalation-matrices/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/escalation-rules` | `src/app/(main)/admin/escalation-rules/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/groups` | `src/app/(main)/admin/groups/page.tsx` | Root > Main (Console) | common/BusinessStatsGrid | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/menus` | `src/app/(main)/admin/menus/page.tsx` | Root > Main (Console) | layout/sidebar/icons | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/overview` | `src/app/(main)/admin/overview/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/permissions` | `src/app/(main)/admin/permissions/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/process-routing` | `src/app/(main)/admin/process-routing/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/roles` | `src/app/(main)/admin/roles/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/service-catalogs` | `src/app/(main)/admin/service-catalogs/page.tsx` | Root > Main (Console) | business/BatchActionBar, common/CustomFieldsEditor | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/sla-definitions` | `src/app/(main)/admin/sla-definitions/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/sla-templates` | `src/app/(main)/admin/sla-templates/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/system-config` | `src/app/(main)/admin/system-config/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/teams` | `src/app/(main)/admin/teams/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/tenants` | `src/app/(main)/admin/tenants/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/ticket-categories` | `src/app/(main)/admin/ticket-categories/page.tsx` | Root > Main (Console) | ui/LoadingSkeleton | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/tickets/assignment-rules` | `src/app/(main)/admin/tickets/assignment-rules/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/tickets/automation-rules` | `src/app/(main)/admin/tickets/automation-rules/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/users` | `src/app/(main)/admin/users/page.tsx` | Root > Main (Console) | common/OrgDepartmentTree | literal colors; fixed palette classes; inline styles; table states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/admin/workflows` | `src/app/(main)/admin/workflows/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; table states; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/change-success` | `src/app/(main)/reports/change-success/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/changes` | `src/app/(main)/reports/changes/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/cmdb-quality` | `src/app/(main)/reports/cmdb-quality/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/incident-trends` | `src/app/(main)/reports/incident-trends/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/incidents` | `src/app/(main)/reports/incidents/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports` | `src/app/(main)/reports/page.tsx` | Root > Main (Console) | business/AdvancedReporting, ui/ManagementPageHeader | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/problem-efficiency` | `src/app/(main)/reports/problem-efficiency/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/problems` | `src/app/(main)/reports/problems/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/service-catalog-usage` | `src/app/(main)/reports/service-catalog-usage/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/sla-performance` | `src/app/(main)/reports/sla-performance/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; fixed palette classes; inline styles; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/sla` | `src/app/(main)/reports/sla/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | shared component/theme dependency; inspect rendered states | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |
| `/reports/tickets` | `src/app/(main)/reports/tickets/page.tsx` | Root > Main (Console) | page-local Ant Design/local sections | literal colors; special visual | STATIC MIGRATED / FIXTURE BROWSER VERIFIED A/C (Task 7) |

## Development-only exclusions

| Route | Evidence | Treatment |
| --- | --- | --- |
| `/agent-ops-demo` | `src/app/agent-ops-demo/page.tsx` labels itself `PROTOTYPE 01`, uses isolated `prototype.module.css`, hard-coded scenario data, and demo-only reset/approve/execute state. | Exclude from shipped visual acceptance. |
| `/tickets/prototype` | `src/app/(main)/tickets/prototype/page.tsx` exports `TicketWorkbenchPrototypePage` and contains an explicit `mockTicket` fixture and prototype/simulated-data comments. | Exclude from shipped page acceptance; shared shell changes still must compile. |

`/onboarding/wizard` remains in scope: it is a first-run user flow that returns users to the product; mock instructional content does not make the route development-only.

## Scoped implementation file groups

1. Theme authority/first paint: root layout, design-system colors/tokens/theme, Ant Design provider/theme, theme variables, globals, Tailwind entry, and theme tests.
2. Navigation shell: layout config, MainLayout, header/sidebar implementations and CSS modules, responsive and focus tests.
3. Shared content primitives: page templates/containers, UI primitives, loading/error/empty/status/stat components, popup/table/card/button rules.
4. Auth/portal/workspaces: auth pages/components, portal, dashboards/personas, workspace, onboarding, my requests/profile/notifications.
5. Tickets/catalog: ticket layout/pages/components, WorkItem shared components, service catalog/requests, templates.
6. Operations: incidents, problems, changes, releases, SLA, approvals, improvements, assets, licenses, applications, projects, installations, marketplace, teams/tags/system/enterprise/MSP residuals.
7. Knowledge/CMDB/workflow: pages and domain components; verify graphs, topology, BPMN, editors, and rich content without redesigning them.
8. Admin/reports: dense tables, trees, charts, drawers and modals across admin and reporting routes.
9. Acceptance tooling: extend the audit generator to all shipped routes, preserve explicit exclusions/evidence, add A/C plus viewport/state fields, and keep screenshots outside commits.

## Baseline verification commands

Run from `itsm-frontend`. These are planned commands; this inventory does not claim they passed.

- `npm run audit:ui`
- `npm run type-check`
- `npm run lint:check`
- `npm run test:unit -- --runInBand`
- `npm run build`
- `PLAYWRIGHT_SKIP_CHANNELS=1 npx playwright test tests/e2e/responsive-layout.spec.ts --project=chromium`

Add exact-path tests for theme persistence, historical system mode, invalid/unavailable storage, first paint, theme toggle, mobile Escape/overlay/focus return/trap, and navigation actions. Visually exercise every shipped route in A and C; cover representative layout/component families at 390, 767, 768, 991, 992, 1024, 1200, and 1440px. Cover representative loading, failure, empty, long text, disabled/loading controls, popups, table scrolling, charts/topology/BPMN/editor states. Static source scanning is triage evidence only.

## Task 7 static migration and residual conventions

All 37 admin/reports page sources and their active local presentation imports were reconciled. Four report aliases (`changes`, `incidents`, `problems`, `sla`) retain their existing redirects; destinations own presentation. Routes already consisting solely of themed Ant Design controls or migrated shared headers required no artificial local edit. Actual TSX import closure: 57 files; no inactive `EnhancedSystemConfig.tsx` or unused `reports/components/*` implementations were included. Shared data, permission/menu trees, APIs, chart data/series colors and professional status mappings remain unchanged.

| Supplemental active state | Source | Status |
| --- | --- | --- |
| Route error | `src/app/error.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7) |
| Root failure without layout/providers | `src/app/global-error.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7); SSR bootstrap and client-mount preference regressions pass |
| Not found | `src/app/not-found.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7) |
| Main loading convention | `src/app/(main)/loading.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7); shared collapsed state, mobile zero sidebar reservation |
| Root common error boundary with diagnostics | `src/components/common/ErrorBoundary.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7) |
| Auth-check/loading helpers and main footer | `src/components/ui/LoadingSpinner.tsx`, `src/app/(main)/layout.tsx` | STATIC MIGRATED / PENDING BROWSER (Task 7) |

Task 7 validation: existing baseline 6 suites / 52 tests passed; final scoped regression 9 suites / 81 tests passed, including 8 new standalone-root-error tests and navigation-theme coverage. Type-check, generated theme consistency, scoped ESLint and whitespace checks pass. Details in `.superpowers/sdd/2026-09-09-visual-theme-unification/task-7-report.md`. Browser acceptance and live backend validation remain separate and pending.

## Final fixture-browser evidence

- Production build `567d8718e` passed. The final whole-branch review and Task 8 fix review passed with no actionable implementation finding.
- The controller observed all **144/144** shipped routes in both A/light and C/dark through actual application navigation. Routes ended in a normal rendered state, an honest fixture-empty state, or their canonical redirect. Desktop checks found no document-level horizontal overflow.
- Focused renderer evidence includes `/cmdb/topology` with a visible 436px ReactFlow graph and selected CI, and `/workflow/designer?id=visual_approval_flow` with a valid BPMN diagram, readable dark palette/context pad/popup, and selection/property state retained across the theme switch.
- At 390x844, login, service catalog, tickets, portal, admin users, and ticket reports rendered in both themes without document overflow; the mobile More-menu theme action worked. Earlier breakpoint, Escape, overlay, focus-return, and focus-containment evidence remains applicable.
- Evidence used an isolated exact-contract, authenticated, read-only fixture. It does not validate live backend integration, business writes, exports, SSO, every form action, or every deep component interaction. Raw untranslated labels and honest empty fixture states remain existing product/content limitations. The authored focused Playwright suite was not executed; system preference and storage-failure behavior is supported by unit coverage, not claimed as browser evidence.
