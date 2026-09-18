# Shared engineering conventions

These conventions apply to every contributor and coding agent. [AGENTS.md](../AGENTS.md) owns architecture and domain constraints; [engineering governance](agent-engineering-governance.md) owns placement, tests, branches, and delivery; [the development guide](DEVELOPMENT_GUIDE.md) owns commands and operations. This document is the shared source for API, frontend, and source-file conventions previously embedded in CLAUDE.md.

## API and DTO contracts

- Use the existing `common.Success` / `common.Fail` response helpers and established response envelope (`code`, `message`, `data`) and error constants. Do not introduce another response protocol.
- Public application JSON request/response fields and new application query parameters use `camelCase`; database/Ent schema field names use `snake_case`. Generated Go members use Go naming (for example `AssigneeID`), not snake_case. Existing external protocols and compatibility contracts follow their explicit boundary; do not silently rename them.
- Controllers return DTOs, never raw Ent entities. Define request/response types and use the owning domain's mapper; keep backend DTOs, frontend API clients/types, and contract tests aligned in the same change. Treat legacy field mismatches as contract debt, not a pattern for new APIs; follow an explicit compatibility decision for their retirement.
- Mapping belongs at the service/DTO boundary, not in frontend casing fallbacks. Reuse existing mappers instead of adding another representation. Pure mappers may live in `dto/`; mappings requiring database reads belong in the owning service. In particular, Ticket mappings are in `itsm-backend/service/ticket_service.go`, including custom-field projections.
- Lists use the owning list DTO/projection rather than leaking entity collections. Avoid per-row custom-field/database lookups; use the established list projection or batch loading when the contract needs additional fields.
- JSON names do not determine identifier types. Match the actual DTO rather than inventing string IDs. A casing transform also cannot translate business meaning: `thresholdPercentage` must match the defined contract, not be guessed from `threshold_percent`.

## Backend implementation conventions

- Use the existing structured Zap logger rather than `fmt.Println`; apply AGENTS.md's secret and sensitive-content rules to fields and errors.
- Ent schemas under `itsm-backend/ent/schema/` generate persistence code. Follow the existing generation/migration workflow; do not treat generated CRUD as the domain authorization layer.
- Reuse shared dynamic-field services (`service/field_definition_service.go`, `service/field_value_service.go`) from domain slices when needed; this established dependency does not authorize cross-domain repository access.
- Seed/migration/initialization logic must be idempotent where appropriate. Default initialization creates product templates/configuration, not fake customer business records. Never use destructive `-fresh` initialization on shared or production databases; follow the development guide for disposable test bootstrap.
- Permission resources must match the permission registry/table: for example `ticket_category`, `ticket_template`, `ticket_tag`, `service_catalog`, and `service_request`. Do not invent alternate spellings in route authorization.
- Backend tests follow existing Go table-driven/testify patterns. Select fixtures appropriate to the behavior: an isolated Ent test client can serve unit tests; it does not replace PostgreSQL tests for RLS, transactions, locking, or database-specific constraints. Follow governance for required regression coverage.

## Frontend implementation conventions

- Use Next.js App Router and the established Zustand stores under `src/lib/store/`. Keep API calls in `src/lib/api/`; follow the existing module's function/class style rather than imposing a new client abstraction.
- Reuse Ant Design, Tailwind, design tokens, and existing components. Preserve accessible, responsive, dense operational screens with clear filters, statuses, owners, timestamps, and actions.
- Provide loading, empty, error, permission-denied, and success states. Do not infer authorization or professional transitions from UI state.
- Use actual exports and APIs supported by the installed Ant Design version. For example, import `Input` and `Select` from `antd`, use `Input.TextArea` for multiline input, and use `Form` for form composition; `Form.Input` / `Form.Select` and a presumed standalone `TextArea` export are not substitutes. Follow installed types when migrating props and use the existing `items` pattern for Tabs.
- Hooks such as `const [form] = Form.useForm()` remain hooks, not removed APIs. Do not copy version-migration tables without checking current package types and component usage.
- Button icons come from `@ant-design/icons`; icons outside buttons stay on `lucide-react`, and moving them is a separate concern. This is a sizing contract, not a preference: Ant Design's `resetIcon()` sets only `display`/`color`/`line-height`/`vertical-align` on `.ant-btn-icon > svg` and relies on inherited font size, while lucide writes `width`/`height` as SVG presentation attributes that outrank inheritance — so inside a button an explicit `size={N}` / `w-N h-N` always wins and Ant Design's sizing can never take effect. Icons that already carried an explicit size were never visually wrong; unsized ones were.
- Give every icon inside a button that has text `aria-hidden="true"`, and give every icon-only button an `aria-label`, `title`, or a wrapping `Tooltip`. `AntdIcon` puts `role="img"` and `aria-label={icon.name}` on its wrapper, so an un-hidden icon makes the button's accessible name an English icon name (`more`, `delete`) — worse than lucide's having no name at all.
- Run `npm run icons:check` and `npm run icons:test`; frontend CI enforces both. Rules 3 and 4 are fail-closed: an `icon={…}` that is not inline JSX, and a `<Button {...spread}>`, are violations unless the site carries an explicit `icon-gate: <reason>` mark. That mark records a review obligation, not machine-verified safety — the gate cannot see the call site, so verify the icon's origin there yourself. `grep -rn "icon-gate:" src/` is the complete exemption surface. Icons passed as a Button **child** are not seen by the gate at all; read the `check-button-icons.mjs` header before extending it.
- Use Jest/React Testing Library for component behavior and the repository's TypeScript Playwright suite for browser workflows. Run relevant tests and type checks for the affected contract or interaction; commands and verification scope are in the linked guides.

## Source naming and placement

API field names, persistence names, and source filenames follow separate conventions. Existing module layout takes precedence over unrelated renaming; do not bulk rename legacy files during a feature task.

| Source | Convention | Example |
| --- | --- | --- |
| Go | Lowercase snake_case; clear responsibility suffix where used by the module | `ticket_controller.go`, `ticket_service.go`, `ticket_dto.go`, `ticket_mapper.go` |
| Ent schema | Existing schema naming | `ticket.go` |
| Go tests | Adjacent package, `_test.go` | `ticket_service_test.go` |
| Next.js routes | Framework filenames; kebab-case segments and bracketed parameters | `page.tsx`, `[ticketId]/page.tsx` |
| React components | PascalCase | `TicketList.tsx` |
| Hooks | `use` prefix | `useTicket.ts` |
| Utilities/stores | camelCase when introducing a new module; preserve an established local convention | `formatDate.ts`, `ticketStore.ts` |
| API clients/types | Follow the existing API/type directory's convention and domain vocabulary | existing `work-item-creation.ts`; new module names must be consistent with their neighbors |
| Frontend unit/component tests | Adjacent `__tests__/`, `*.test.ts(x)` | `__tests__/TicketList.test.tsx` |

Avoid ambiguous `new`, `final`, or `temp` suffixes and mixed naming for the same responsibility. Go files do not become camelCase because JSON fields are camelCase. Do not introduce a generic frontend service layer or new folders merely to reproduce an illustrative directory tree. Long-term documentation uses lowercase kebab-case; document/test locations follow engineering governance.

## Extension implementation details

- Connector lifecycle includes installed/configured/enabled/health-checked/disabled/uninstalled states where supported. Enterprise channels reuse the marketplace/lifecycle abstractions; do not build one-off integration mechanisms.
- Skills should declare manifest, inputs, outputs, permissions, audit behavior, and evaluation hooks where possible. Plugins and CLI comply with the same API, authorization, tenant, and audit contracts as other clients.
- Use the owning BPMN, knowledge/RAG, LLM gateway, and SLA services identified by current code. Do not infer readiness or implementation paths from historical capability lists.
