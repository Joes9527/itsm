# Intake reconciliation and production creation inventory

Status: accepted audit baseline for A2–A4; implementation and runtime acceptance remain pending.

Scope: A1 of the [approved implementation plan](../superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md), against [design §§6–9, 11–13](../superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md). This document records inspected source, intended reuse, and required regression targets. `reuse-reviewed` means a reviewed building block, **not** approved production behavior or passing tests. No source code was copied and no database was changed.

## Fixed baselines and evidence

| Name | Commit / state |
| --- | --- |
| M: main and local origin/main tracking ref | `5b2dd2c62358fd6e7f07d1886a2c67f750d8422f` |
| R: worktree-unified-intake-p1-reconciliation | `35d1958eb9a5bcd0cef7e28f819951566285ccb1` |
| E: feat/kaf-delegation-transactional-delivery | `0b3858d9c3afb3e24dbafe6ecaf15efbc8009d14` |
| A1 starting HEAD | `9f65ad5d770dbac22c6e6dbff2dbc1f09650a593` |
| Implementation worktree / branch | `.worktrees/sslvpn-unified-intake`, `codex/feat/sslvpn-unified-intake`; clean starting `git status --short`; tracks origin/main, ahead 3 documentation commits |
| Source worktrees | R: `.claude/worktrees/unified-intake-p1-reconciliation`; E: `.worktrees/kaf-delegation-transactional-delivery`; both registered at commits above; both clean under read-only `git -C … status --short`; neither edited |

`origin/main` here is the local remote-tracking baseline. Parent separately reported a successful fetch during this task confirming `5b2dd2c6`; A1 itself did not fetch. Source comparisons use Git objects, not potentially modified source-worktree files. `git worktree list --porcelain` recorded all registered worktrees; source worktree status was checked read-only, and Git objects remain the audited source.

Raw reproducible evidence is in system temporary directory `/tmp/itsm-a1-audit/`: `baseline.txt`, `diff-reconciliation.txt`, `diff-early.txt`, `backend.txt`, `frontend.txt`, `schemas.txt`, `expanded.txt`, `frontend-extra.txt`, and source extracts. These are local audit artifacts, not committed test evidence. Commands executed include both required three-dot name-status diffs, all three required scans, plus direct Ent/professional extension creation, transactional creator, Problem conversion, BPMN Change, HTTP routes, frontend alternate clients, and Python AI-service scans. Broad regex results include definitions, comments and tests; classification below prevents treating those as endpoints.

## Reuse decision and staging

A2 should import only the minimal reviewed infrastructure (new schemas, regenerated Ent code, common command/creator contracts, receipt/snapshot/audit primitives and transaction orchestration with tests). Put shared contracts in `handlers/common/workitemcreation`; do not carry the source dependency direction `intake → service` into professional services that must call Intake. Keep production bootstrap/router cutover in A3/A4. Do not activate partial creators or expose an endpoint whose field/identity contract is incomplete.

R is a better starting point than E for the main numbering and Incident rule changes, but R registers only Incident and Change in bootstrap. It has no public Intake handler, identity-exchange handler or workflow-start dispatcher file. R enqueues `workflow.start.requested` and adds event-ID support to the Outbox repository; this is not evidence that a production consumer exists. E has HTTP/identity/dispatcher implementations but belongs to an older authority/numbering baseline. Neither branch can be wholesale merged as complete unified creation.

All source rows below use the exact nine-column interface. Paths are relative to the repository; `backend/` abbreviates `itsm-backend/`, `frontend/` abbreviates `itsm-frontend/src/`. Test entries denote required landing packages/files, not tests executed by this audit.

| sourceCommit | path | entry | targetClass | actorSource | idempotencySource | transactionOwner | disposition | test |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M | backend/repository/workitemnumber/ | Allocator | all supported classes | caller | tenant + numbering period | caller transaction | already-authoritative | repository/workitemnumber allocator concurrency/rollback |
| R | backend/handlers/intake/creator.go | Registry register/get | incident, SR, change | resolved identity | caller | Intake | reuse-reviewed | creator_test.go: duplicate/unknown registration |
| R | backend/handlers/intake/command.go, canonicalize.go, identity.go | command and digest v2 | incident/catalog only | Identity | key + canonical digest | Intake | reimplement | canonicalize_test.go: every DTO field changes digest; trusted requester/source |
| R | backend/handlers/intake/service.go | Create/createAttempt | incident, SR, change | Identity actor/requester | receipt claim | Intake | reuse-reviewed | service_test.go, transaction_contributors_test.go, postgres_integration_test.go; extend for five classes |
| R | backend/handlers/intake/idempotency_repository.go | Claim/Complete/LoadCompleted | all | actor ID | tenant/actor/channel/operation/key | supplied tx | reuse-reviewed | idempotency_repository_test.go, idempotency_postgres_test.go; cross-user/channel and rollback |
| R | backend/handlers/intake/audit_repository.go, snapshot_repository.go, errors.go, metrics.go | transactional evidence and errors | all | actor/channel | receipt/digest | supplied tx | reuse-reviewed | transaction_contributors_test.go, metrics_test.go; reject secret fields |
| R | backend/handlers/intake/resolver.go | Resolve | incident/catalog | active requester, permission checker | none additional | supplied tx | reimplement | resolver_test.go: actual catalog version, class/CI/CTI/SLA access, form version changes |
| R | backend/handlers/intake/work_item_creator.go | CreateBase | incident, SR, change only | draft identity | authoritative allocator | supplied tx | reimplement | work_item_creator_test.go: add Generic/Problem, preserve priority/status and prohibit supplied-number bypass |
| R | backend/handlers/intake/incident_creator.go | Prepare/CreateExtension/AfterCommit | incident | resolved actor | Intake receipt | supplied tx plus post-commit hook | reimplement | incident tests: preserve priority matrix/category/assignee/CI/audit; move professional ownership |
| R | backend/handlers/intake/change_creator.go | Prepare/CreateExtension | change_request | resolved actor | Intake receipt | supplied tx | reimplement | change_creator_test.go: draft state, explicit priority, related WorkItems, CI validation |
| R | backend/handlers/intake/service_request_creator.go | Prepare/CreateExtension | service_request_item | resolved actor | Intake receipt | supplied tx | reimplement | SR tests: all rich fields, approval snapshot, linked CI, remove duplicate shared writes |
| R | backend/controller/incident_controller.go, backend/service/bpmn/incident_handler.go | HTTP and BPMN adapters | incident | HTTP / workflow actor | incoming header / task key | Intake | reimplement | incident_intake_adapter_test.go, bpmn/incident_handler_test.go; complete DTO + stable key |
| R | backend/handlers/service_request/service.go, handler.go | catalog Incident/Change adapter; SR legacy path | three catalog classes | requester | adapter key | split Intake/legacy SR | reimplement | service_request/intake_adapter_test.go and regression_test.go; no partial class cutover |
| R | backend/internal/bootstrap/app.go | partial registry wiring | incident/change | adapters | adapters | Intake | reimplement | bootstrap wiring tests; A3/A4 only |
| R | backend/ent/schema/{intake_request,intake_resolution_snapshot,external_identity}.go | new infrastructure schemas | all | identity | unique receipt index | Intake | reuse-reviewed | regenerated schema + real role RLS/uniqueness tests |
| R | backend/ent/schema/servicecatalog.go; handlers/service_catalog/ | target_class authority | catalog definitions | administrator | N/A | catalog service | reimplement | catalog tests and migration preflight; add version/publish validation |
| E | backend/handlers/intake/work_item_creator.go | legacy number generator | incident/SR | identity | old generated number | Intake | remove | allocator regression; use M Allocator only |
| E | backend/handlers/intake/incident_creator.go | highestLevel/open incident rule | incident | identity | receipt | Intake | remove | M Incident matrix/new-state regression; do not restore old semantics |
| E | backend/handlers/intake/handler.go, identity_exchange.go, identity_mapping_handler.go, workflow_intervention_handler.go; backend/middleware/intake_auth.go | external HTTP/auth/retry boundary | incident/SR | short-lived user identity or access token | header, assertion nonce | Intake / mapping transaction | reimplement | identity_exchange_test.go, middleware/intake_auth_test.go, router/intake_identity_routes_test.go; A5+ boundary review |
| E | backend/service/workflow_start_outbox_dispatcher.go | DispatchOnce | incident/SR | frozen actor/channel | stable event + business key | Outbox/engine | reimplement | dispatcher tests plus two processes, current claim/lease/fencing, all supported classes; A6 |
| E | backend/handlers/service_request/repository_impl.go, entity.go; backend/ent/schema/servicerequest.go | WorkItem-backed SR projection | service_request_item | WorkItem | N/A | owning transaction | reimplement | SR list/detail/callback/provisioning and RLS tests in same release as column cutover |
| R/E | backend/ent generated files | generated clients/builders | all | N/A | schema indexes | N/A | reimplement | regenerate from final schema and compile; never copy stale generated set |
| R/E | changed tests and historical report files | old branch evidence | N/A | N/A | N/A | N/A | not-production | inspect/adapt relevant tests; old success reports do not prove new baseline |
| R/E | other modified domain/bootstrap files in saved name-status diffs | unrelated or superseded changes | N/A | N/A | N/A | N/A | reimplement | no blanket reuse authorization; review exact hunk before import |

R canonicalization has no Generic/Problem/SR typed payload; Change priority and relatedTickets are absent; catalog version is explicitly left zero in resolver. R's `writeFieldValues` sends all form values to field storage while the SR creator also writes its professional fields. Separate professional input from dynamic values. Its SR `ApprovalSnapshot` has a storage branch but is never populated in `Prepare`. Its shared writer defaults Change to `open` and `medium`, not the existing HTTP `draft` and explicit priority. These are concrete blockers, not optional polish.

## Current production and internal entrypoints

`none` under idempotency means no Intake receipt, not necessarily absence of all domain uniqueness. All live writes must converge on Intake, while domain services retain rules and provide transaction-bound operations. Preserve explicit blocked behavior for unsupported `catalog_task`; no live Catalog Task creation was found and this audit does not authorize a new feature.

| sourceCommit | path | entry | targetClass | actorSource | idempotencySource | transactionOwner | disposition | test |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M | backend/router/router.go → controller/ticket_controller.go:71 → service/ticket_service.go:124 → repository/ticket/repository_impl.go:82 | POST /tickets (manual, template, quick UI) | generic today; legacy type may classify SR without extension | authenticated user; service request requester | none | Ticket repository; fields/SLA/workflow after commit | reimplement | service/ticket_service_test.go; ticket HTTP contract, five type mappings and rollback |
| M | backend/controller/ticket_controller.go:966 | POST /tickets/:id/subtasks | generic/legacy type | auth user + parent | none | Ticket repository | reimplement | controller subtask access + transaction tests; no automatic catalog_task conversion |
| M | backend/controller/ticket_controller.go:592; service/ticket_service.go:2146,2172 | ImportTickets; controller method has no router registration found | generic | currently hardcoded requester 1 | none | each Ticket separately | not-production | import contract test before any activation; replace fixed requester and derive batch/row key if retained |
| M | backend/controller/incident_controller.go:75 → service/incident_service.go:77,184 | POST /incidents | incident | authenticated tenant/user | none | Incident service base + extension; later workflow | reimplement | service/incident_service_test.go, controller tests, PostgreSQL failure injection |
| M | backend/handlers/service_request/handler.go:121 → service.go:77,248 → repository_impl.go:70 | POST /service-requests, Requested Item branch | service_request_item | authenticated requester | none | SR service tx base+extension; fields/workflow after commit | reimplement | SR service/regression tests; rich validation, CI reuse, atomic dynamic fields/audit/start |
| M | backend/handlers/service_request/service.go:545 → internal/bootstrap/app.go:1326 | catalog Incident bridge | incident | tenant/requester parameters | none | Incident service | reimplement | SR catalog Incident contract + complete fields |
| M | backend/handlers/service_request/service.go | catalog Change falls into default SR path | must be change_request | authenticated requester | none | SR service | reimplement | catalog Change creates exactly one Change extension and no SR |
| M | backend/handlers/change/handler.go:71 → service.go:68 → repository_impl.go:364 | POST /changes | change_request | authenticated creator | none | Change repository | reimplement | handlers/change service/repository/handler tests; planned dates/CIs/relations |
| M | backend/handlers/standard_change/handler.go:451 | POST /standard-changes/:id/instantiate | change_request, subtype standard | auth user; current route super_admin | none | Change repository | reimplement | standard_change/handler_test.go; active tenant template, override fields, draft and stable key |
| M | backend/handlers/problem/handler.go:112 → service.go:Create → repository_impl.go:358 | POST /problems | problem | auth creator | none | Problem repository | reimplement | handlers/problem/service_test.go, handler_test.go; category + priority |
| M | backend/controller/incident_controller.go → handlers/problem/conversion.go:35 → repository_impl.go:createInTx | POST /incidents/:id/convert-to-problem | new problem + relation, source remains incident | auth actor + source row access | unique investigated_by relation, not Intake receipt | Problem repository includes relation+audit | reimplement | conversion_test.go existing atomicity/concurrency plus Intake replay |
| M | backend/service/bpmn/incident_handler.go:111 | incident service task create | incident | BPMN execution variables/context | none; derive instance/task/execution action | Incident service | reimplement | bpmn/incident_handler_test.go: replay, actor/tenant, no nested tx |
| M | backend/service/bpmn/change_handler.go:146 → handlers/change/service.go:84 | CreateChangeForWorkflow | change_request | workflow created_by variable | none; derive stable task key | Change repository | reimplement | bpmn/change_handler_test.go, change domain tests; trusted actor and fields |
| M | backend/service/bpmn/service_request_handler.go:32 | create_request | service_request_item | workflow | N/A | none; explicitly blocked | already-authoritative | service_request_handler_test.go: remain blocked; do not resurrect parallel SR creation |
| M | backend/internal/bootstrap/email_msgraph_wiring.go:59; connector/builtin/msgraph/coordinator.go:211 | wired tenant email poll coordinator → TicketService | generic | resolved sender user; tenant connector | external message lookup; non-atomic precheck | Ticket repository; audit/comments/attachments later | reimplement | coordinator tests, bootstrap/email_msgraph_wiring_test.go; duplicate delivery, identity, attachment recovery |
| M | backend/connector/builtin/email/service.go:17,70 | old IMAP TicketCreator interface/consumer | generic | fromEmail intended | none | implementation absent | not-production | connector/builtin/email tests; no CreateTicketFromEmail implementation or bootstrap injection found |
| M | backend/service/tool_queue.go:82; internal/bootstrap/app.go:484 | approved create_ticket invocation | generic | arguments requester_id; job tenant | invocation ID available, not propagated | lazily constructed partial TicketService | reimplement | service/tool_queue tests; trusted invocation actor, stable key, full injected Intake |
| M | backend/service/tool_registry.go:179 | direct create_ticket dispatch | generic | tool context | N/A | none; returns not implemented | already-authoritative | tool_registry_test.go: direct unapproved creation remains rejected |
| M | backend/handlers/ai/handler.go:492; service.go:340 | POST /ai/ticket/create | suggestion only | auth tenant | N/A | none; returns draft fields | not-production | handlers/ai/handler_test.go: no WorkItem persistence |
| M | backend/service/feishu_sync_service.go:123,234; bootstrap/app.go:606 | inbound task sync/webhook creates Ticket | generic | CreatorID mapping; currently falls back to first active tenant user | task GUID sync row | Feishu service base+sync record | reimplement | feishu_sync_service_test.go: no fallback actor, atomic sync evidence, duplicate webhook |
| M | backend/repository/ticket/repository_impl.go:27,62,75 | TransactionalCreator interface/implementation | generic/SR legacy class | caller | allocator only | supplied client / repository | reimplement | repository/ticket tests; retain primitive under common contract, remove alternative orchestration |
| M | backend/handlers/standard_change/handler.go:198; pkg/seeder/seeder.go:2376 | StandardChange definition creation | not WorkItem | admin/bootstrap | template identity | definition writer | not-production | standard template tests; not execution record |
| M | backend/ent/ticket_create.go | generated documentation examples | none | N/A | N/A | N/A | not-production | Ent generation/compile only |
| M | itsm-ai-service/services/llm_client.py; api/triage.py | Python scan: analyze/triage, no creation write found | none | analysis context | N/A | none | not-production | AI service regression; no second creation service |
| E | backend/router/router.go → handlers/intake/handler.go | /intake/work-items, not present in M/R | incident/SR in E | access or exchanged Intake identity | Idempotency-Key | Intake | reimplement | router/auth contracts and real PostgreSQL tests; not currently available endpoint |

Main's Ticket service validates references and template field formats, but logs some field-load failures and writes fields/SLA after its repository commit. Main SR validates rich infra fields and required dynamic fields, resolves approval steps, reuses an existing cloud-resource CI, and triggers workflow in a goroutine before the post-commit dynamic-field write. Preserve these legitimate semantics while making required facts atomic; do not treat their existing log-and-continue failure handling as a target requirement. Feishu currently falls back to an unrelated active tenant user if mapping fails; replace with explicit identity failure. MS Graph audit is explicitly best-effort outside Ticket commit.

## Public creation field contract

Each row is an individual JSON field of the five current Create DTOs, inspected in `dto/{ticket,incident,service,change,problem}_dto.go`. This is the required A3/A4 preservation decision, not a claim that R already maps it. All accepted fields affecting creation must enter the versioned canonical digest after normalization. `retain` preserves meaning/validation, `map` changes authoritative ownership, `reject` is explicit failure (never silent loss). Trusted actor, tenant, source, immutable class and generated numbers cannot be supplied by arbitrary clients.

| Class | public field | decision and required destination |
| --- | --- | --- |
| Generic | `title` | retain → WorkItem title; existing bounds |
| Generic | `description` | retain → WorkItem description |
| Generic | `priority` | retain → WorkItem priority including existing urgent compatibility decision |
| Generic | `type` | map → registered professional class/subtype; generic ticket/improvement preserve product meaning; reject missing professional creator |
| Generic | `typeId` | map → validated ticket type/template metadata; currently not copied into CreateParams |
| Generic | `source` | map → trusted ingress source; HTTP validates allowed source |
| Generic | `creatorEmail` | map → trusted email evidence; reject arbitrary impersonation |
| Generic | `externalMessageId` | map → trusted source reference and dedupe key |
| Generic | `conversationId` | retain → trusted email thread reference |
| Generic | `category` | map → tenant category resolution, categoryId precedence |
| Generic | `categoryId` | map → WorkItem category with tenant validation |
| Generic | `templateId` | retain → tenant template + frozen schema and workflow inputs |
| Generic | `requesterId` | map → trusted requester or explicitly authorized on-behalf; KAF uses current user |
| Generic | `assigneeId` | retain → tenant-qualified assignment |
| Generic | `parentTicketId` | map → validated WorkItem parent relation |
| Generic | `tagIds` | retain → unique tenant-qualified tag links in tx |
| Generic | `tags` | reject unsupported name list unless normalized to authorized tagIds; current CreateParams ignores it |
| Generic | `formFields` | map → validated template/ad-hoc field_values owned by ticket; preserve system workflow input separately |
| Generic | `attachments` | reject unsupported raw references until ownership/attachment binding is implemented; currently not copied to CreateParams |
| Generic | `workflowDefinitionKey` | map → authorized, frozen process binding; no arbitrary workflow bypass |
| Generic | `approvalChain` | map → trusted server-resolved approval snapshot; reject client-forged approval authority |
| Incident | `title` | retain → WorkItem title |
| Incident | `description` | retain → WorkItem description |
| Incident | `type` | retain → Incident subtype; not recordClass |
| Incident | `priority` | retain → explicit priority override, otherwise existing priority matrix |
| Incident | `severity` | retain → Incident severity |
| Incident | `impact` | retain → Incident impact and matrix input |
| Incident | `urgency` | retain → Incident urgency and matrix input |
| Incident | `category` | map → tenant category resolution onto WorkItem |
| Incident | `subcategory` | map → child category resolution; do not duplicate shared category |
| Incident | `configurationItemIds` | map → tenant/permission validated CI relationships, canonical IDs |
| Incident | `assigneeId` | retain → authorized WorkItem assignee |
| Incident | `impactAnalysis` | retain → typed professional analysis including every nested field |
| Incident | `source` | map → trusted ingress/source policy preserving monitoring/system/user meaning |
| Incident | `metadata` | retain → professional metadata with secret validation |
| Incident | `detectedAt` | retain → UTC instant; default only once for original request |
| SR | `catalogId` | map → authorized catalog and immutable target_class/version |
| SR | `title` | map → WorkItem title; preserve top-level/formData normalization |
| SR | `reason` | map → WorkItem description; preserve normalization/bounds |
| SR | `formData` | map → typed professional fields + ticket field_values + trusted approval/process context; one owner per value |
| SR | `costCenter` | retain → SR cost_center |
| SR | `dataClassification` | retain → SR data_classification with applicable infra validation |
| SR | `needsPublicIp` | retain → SR needs_public_ip |
| SR | `sourceIpWhitelist` | retain → SR source_ip_whitelist with IP/CIDR validation |
| SR | `expireAt` | retain → SR expire_at; future/policy check; distinct from later authorization effective time |
| SR | `complianceAck` | retain → SR compliance_ack; required by catalog policy |
| SR | `contactName` | retain → SR contact_name |
| SR | `contactEmail` | retain → SR contact_email and email validation |
| SR | `quantity` | retain → SR quantity and 1..1000 constraint; no silent fractional/string coercion |
| SR | `expectedAt` | retain → SR expected_at UTC |
| Change | `title` | retain → WorkItem title |
| Change | `description` | retain → WorkItem description |
| Change | `justification` | retain → Change justification |
| Change | `type` | retain → Change subtype normal/standard/emergency |
| Change | `priority` | retain → WorkItem explicit priority; missing in R command |
| Change | `impactScope` | retain → Change impact_scope |
| Change | `riskLevel` | retain → Change risk_level |
| Change | `plannedStartDate` | retain → Change planned_start_date UTC; validate window |
| Change | `plannedEndDate` | retain → Change planned_end_date UTC; validate ordering |
| Change | `implementationPlan` | retain → Change implementation_plan |
| Change | `rollbackPlan` | retain → Change rollback_plan |
| Change | `affectedCis` | map → tenant-qualified CI associations; preserve public string IDs with explicit validation |
| Change | `relatedTickets` | map → tenant-qualified structured WorkItem relations; no parallel JSON relationships; missing in R command |
| Problem | `title` | retain → WorkItem title |
| Problem | `description` | retain → WorkItem description |
| Problem | `priority` | retain → WorkItem priority |
| Problem | `category` | map → tenant category on WorkItem |
| Problem | `rootCause` | retain → Problem root_cause |
| Problem | `impact` | retain → Problem impact |
| Problem | `impactScope` | reject with structured unsupported-field error until domain owns a separate meaning; current handler drops it |

The domain initial states are owned by domain rules: Incident `new`, Change `draft`; Generic/SR currently use the Ticket creation default `new` (SR source R uses `open`, requiring deliberate convergence). Problem handler passes `open` but its base writer does not set status explicitly; do not assume the argument is persisted. Keep professional rules explicit during migration. Existing response DTO shared fields must project from WorkItem; input preservation must not create duplicate extension columns. Standard Change template overrides title/affectedCis and supplies the remaining template fields; Incident-to-Problem additionally inherits source priority/category/impact and creates a structured relation plus audit atomically.

## Migration inventory, conflicts and release order

The current canonical registry is `backend/migration/migrations.go:RegisteredMigrations`, ending in `022_drop_professional_extension_shared_fields`; the embedded SQL is part of the execution contract. Existing `022` operates on Incident/Problem/Change, **not** the remaining SR fields. Main SR still declares tenant_id/requester_id/created_at/updated_at (and processor/version/deleted state needing explicit ownership review). Do not change an already-applied migration's identity or replace current `020/021/022` SQL with E's same-numbered stream.

| sourceCommit | migration file / embedded entry | disposition | content and conflict | execution / validation |
| --- | --- | --- | --- | --- |
| M | migration/migrations.go:020_work_item_number_allocator | already-authoritative | tenant+period counter; tenant-scoped Ticket number uniqueness | precedes intake creation; allocator migration tests |
| M | migrations/021_add_callback_optional_declared{,_verify,_dev_reset}.sql; registry | already-authoritative | declared optional callback flag | preserve registry order and verifier |
| M | registry:022_drop_professional_extension_shared_fields | already-authoritative | professional authority for Incident/Problem/Change, canonical FKs/uniqueness/RLS; legacy workflow cleanup | preserve entire existing SQL/operational contract |
| R | migrations/023_unified_intake_rls{,_verify,_dev_reset}.sql; registry | reuse-reviewed | FORCE RLS on intake_requests/intake_resolution_snapshots/external_identities; completed receipt requires result/time | after new Ent tables; verify real limited-role reject tests; reset requires empty tables |
| R | migrations/024_service_catalog_target_class_authority{,_verify,_dev_reset}.sql; registry | reimplement | backfill from Request/Incident/Change; target_class NOT NULL/check; drop itsm_type | after all catalog readers/writers/seeder/API switch; stop on conflicting valid legacy and target values instead of silently retaining one |
| R | migrations/025_external_identity_version{,_verify,_dev_reset}.sql; registry | reuse-reviewed | add positive version bigint default 1 | after identity table; verify type/default/check; review rollback before use |
| E | registry:020_unified_intake_rls | remove | same numeric position as M allocator; equivalent intent to R 023 | select reviewed R assets; never install both identities |
| E | registry:021_work_item_authority | reimplement | combines Incident and SR shared-column drops, RLS and catalog target_class cutover; conflicts with M 021 callback and overlaps M 022/R 024 | split only still-needed SR cutover into a new migration, coordinated with A3/A4 |
| E | registry:022_external_identity_version | remove | conflicts numerically with M professional-authority migration | R 025 intent replaces it, final numbering must be rechecked |
| final A3/A4 | new SR authority migration plus verify/recovery assets (number not yet allocated) | reimplement | verify exactly one base, class, FK, tenant/requester/timestamp consistency; stop on conflicting historical values; drop duplicates and replace RLS with WorkItem relationship scope | same release batch as all SR API/worker/repository/provisioning readers and writers; empty + restored DB validation |

R SQL assets include three files per migration exactly as listed by brace notation: forward `.sql`, `_verify.sql`, `_dev_reset.sql`. E's intake migrations are embedded registry SQL at the inspected commit; do not assume same-name disk assets exist. `023/024/025` are free in the inspected M registry only; recheck concurrent migration registrations immediately before implementation. Create required tables first, apply additive identity/intake schema, keep current M authority stream, then coordinate catalog and SR destructive cutovers with complete readers/writers. Additive A2 schema import is not authorization to execute column removal or activate partially migrated endpoints. Dev reset is not a production rollback; run neither during A1.

SR conflict checks must compare current duplicate tenant/requester/timestamps with the related Ticket, including orphan and duplicate extension checks; invalid/mismatched rows require an explicit remediation list. R's 024 only backfills invalid/unset target_class and would accept an already-valid target_class conflicting with itsm_type; the final migration must detect that ambiguity before dropping the legacy column. E's 021 has existence/class guards but does not establish full equality of SR duplicate fields before dropping them.

## Frontend call-site coverage and scan reconciliation

The table below accounts for every hit of the required frontend scan, including test hits. `reimplement` on an API client means propagate a stable submission key and preserve its payload contract in the same release as the endpoint requirement; do not generate a new key inside every retry. Wrappers are not additional server persistence implementations. Existing UI success text must respect pending/blocked workflow startup.

| sourceCommit | path | entry | targetClass | actorSource | idempotencySource | transactionOwner | disposition | test |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M | `itsm-frontend/src/lib/hooks/useTicketsQuery.ts:141` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/hooks/__tests__/useTickets.test.ts:135` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/hooks/useTickets.ts:181` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/__tests__/api-integration.test.ts:168` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/__tests__/api-integration.test.ts:207` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/services/__tests__/incident-service.test.ts:82` | test fixture | incident | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/services/__tests__/ticket-service.test.ts:87` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/services/__tests__/ticket-service.test.ts:296` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/services/ticket-service.ts:189` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/services/incident-service.ts:156` | client declaration/call | incident | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/services/ticket-service-v2.ts:159` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/api/ticket-api.ts:22` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/api/service-request-api.ts:120` | client declaration/call | service_request_item/catalog | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/api/incident-api.ts:395` | client declaration/call | incident | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/api/service-catalog-api.ts:332` | client declaration/call | service_request_item/catalog | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/components/business/IncidentManagement.tsx:1072` | client declaration/call | incident | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/components/business/ticket-modal/TicketModalContainer.tsx:28` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/lib/api/__tests__/ticket-api.test.ts:52` | test fixture | generic/legacy type | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/api/__tests__/service-request-api.test.ts:45` | test fixture | service_request_item/catalog | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/api/__tests__/incident-api.test.ts:55` | test fixture | incident | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/api/__tests__/service-catalog-api.test.ts:142` | test fixture | service_request_item/catalog | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/api/__tests__/service-catalog-api.test.ts:152` | test fixture | service_request_item/catalog | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/lib/api/__tests__/service-catalog-api.test.ts:175` | test fixture | service_request_item/catalog | fixture | fixture | server domain | not-production | retain/adapt fixture contract |
| M | `itsm-frontend/src/components/business/ticket-modal/services/ticket-modal-service.ts:21` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/app/(main)/incidents/create/page.tsx:113` | client declaration/call | incident | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/app/(main)/tickets/create/page.tsx:377` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/app/(main)/improvements/new/page.tsx:20` | client declaration/call | generic/legacy type | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |
| M | `itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx:123` | client declaration/call | service_request_item/catalog | authenticated browser | none; stable submission key required | server domain | reimplement | adjacent API/hook/component contract; browser retry/refresh |

Additional inspected routes/client families beyond the seed expression:

| sourceCommit | path | entry | targetClass | actorSource | idempotencySource | transactionOwner | disposition | test |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M | frontend/app/(main)/problems/new/page.tsx; lib/api/problem-api.ts:139; lib/services/problem-service.ts:111 | Problem create | problem | browser | none | Problem repository | reimplement | problem API/page contract and E2E |
| M | frontend/app/(main)/changes/new/page.tsx; lib/api/change-api.ts:260; lib/services/change-service.ts:134 | Change create | change_request | browser | none | Change repository | reimplement | change API/page contract and E2E |
| M | frontend/app/(main)/standard-changes/page.tsx:164; lib/api/standard-change-api.ts:120 | instantiate template | change_request | browser | none | Change repository | reimplement | standard-change contract including change_id versus changeId response |
| M | frontend/components/incident/IncidentDetail.tsx:335; lib/api/incident-api.ts:497 | convertToProblem | problem | browser | relation uniqueness | Problem repository | reimplement | conversion API and E2E |
| M | frontend/lib/api/ticket-api.ts:150; lib/services/ticket-service-v2.ts:434 | createSubtask | generic/legacy type | browser | none | Ticket repository | reimplement | parent permission and stable-key contract |
| M | frontend/lib/api/change-api.ts:332 | /changes/templates/:id/create client | change_request | browser | none | no matching route found | not-production | remove/route-correct before activation; standard-changes instantiate is wired |
| M | frontend/lib/services/ticket-template-service.ts; lib/api/ticket-api.ts template APIs; standard-change-api.ts create definition | definition creation | not WorkItem | admin | N/A | template service | not-production | definitions remain separate from execution |

All required backend seed hits are covered by the current-entrypoint table: interface declarations and bridge methods belong to their named caller chains; all seven `ent/ticket_create.go` hits are generated comments. Expanded Ent scan additionally found extension writes in SR/Incident/Problem/Change repositories, StandardChange definition writes and seeder definitions; those are classified above. Non-create frontend hits (comments, ratings, assignments, workflow transitions, relations, notifications, views, prediction, PIR) are operations on existing records, not new WorkItems. No Python direct creation was found in the inspected `itsm-ai-service` tree; KAF's separate repository is a later work package and is not claimed audited here.

The Problem new page additionally sends `assigneeId`, which is absent from `CreateProblemRequest`; the adapter must either add an authorized typed mapping or reject this input explicitly. The current page must not imply assignment was saved.

## Validation and remaining gates

A1 validation is static: source object inspection, required scans with explicit classification, field list extraction checked against all five Create DTO structs, migration content/order comparison, and `git diff --check`. This documentation change adds no production behavior, so it does not run mutating database tests. Parent independently reported the four baseline ITSM package tests passed; that is not a new test run by A1 and not proof of Intake completeness.

The imported PostgreSQL tests use build tag `integration_postgres` and `INTAKE_POSTGRES_TEST_DSN`; the master plan's generic `integration` command must not be treated as executing those tests. Verify exact tags after import, explicitly supply an isolated database, and report skipped tagged suites. Relevant existing source test names are `idempotency_postgres_test.go` and `postgres_integration_test.go`; new tests must cover all five classes, every field's digest contribution, rollback of all required facts, replay, cross-actor/tenant denial and process start recovery.

A2 review must confirm compile-safe common-package dependencies and the source-hunk selection; A3/A4 must close every live entrypoint and preserve all mapped fields; A5+ must finish identity/HTTP/version binding; A6 must supply the real workflow-start consumer. Complete creation acceptance requires fresh tests and running API/worker evidence, not this inventory or either source branch's reports.
