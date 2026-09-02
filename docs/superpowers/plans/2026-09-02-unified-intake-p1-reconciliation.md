# Unified Intake × P1 Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge the Dev-verified Unified Intake implementation (`feat/kaf-delegation-transactional-delivery`) onto the merged P1 baseline (`main` at `ca29f626`) as the single authoritative creation path for Incident, Service Request, and Change, fixing every field-mapping and business-rule bug the field found along the way, with a mandatory (no-transition) `Idempotency-Key` contract and every creation entry point converged onto it.

**Architecture:** `handlers/intake` becomes the one `CreateWorkItemCommand` Application Service that all HTTP controllers, the BPMN `createIncident` callback, and Catalog-derived Service Request/Incident/Change creation call into. A shared `WorkItemCreator.CreateBase` (not each domain creator) owns allocating `tickets.ticket_number` via `workitemnumber.Allocator` and building the WorkItem row. Each professional domain's `ProfessionalCreator` (`IncidentCreator`, `ServiceRequestItemCreator`, new `ChangeCreator`) receives an already-open `*ent.Tx` from the Application Service and performs only its own domain's extension write — including any extension-level identifier `incidents.incident_number` needs, which is a **separate, already-existing** concern from the WorkItem's ticket number (see Task 4).

**Tech Stack:** Go 1.25.12, Gin, Ent, PostgreSQL, SQLite enttest, Testify, Next.js/TypeScript frontend clients.

**Spec:** `docs/superpowers/specs/2026-09-02-unified-intake-workitem-creator-remediation-design.md` (Phase 1, sections 5.1–5.6)

## Global Constraints

- Baseline is `main` at `ca29f626`. Record `git rev-parse HEAD` before starting each task.
- One business concept, one authoritative algorithm. `IncidentCreator` must call the existing `IncidentService` priority/status logic, never reimplement it. `ServiceRequestItemCreator`'s custom-field writes must use the same `entity_type="ticket"` convention as the existing `handlers/service_request/service.go`.
- `tickets.ticket_number` and `incidents.incident_number` are **two distinct, already-separate identifiers** — `workitemnumber.Allocator` (P1-A) owns the first; `IncidentService`'s existing `SequenceService`-backed generator owns the second. Do not conflate them into one allocator call; do not let any domain `ProfessionalCreator` allocate `tickets.ticket_number` itself — that is `WorkItemCreator.CreateBase`'s sole job.
- No transitional/compatibility path for any contract change. `Idempotency-Key` becomes mandatory the same day every caller (backend controllers, frontend clients, BPMN internal calls) is updated — no optional-header intermediate state.
- Every creator's `Prepare`/`CreateExtension` methods take the caller-supplied `*ent.Tx` and never open their own transaction or call `Commit`/`Rollback`.
- Migrations must not silently duplicate work already merged. `021_work_item_authority`'s `incidents`-table portion is superseded by the merged `022_drop_professional_extension_shared_fields` and must not be replayed.
- Ent schema/generated files, `router.go`, and `internal/bootstrap/app.go` are high-conflict files — this plan's tasks touch them serially in the order given; do not run tasks 2+ out of order against a fresh worktree without task 1's migration renumbering already applied.
- TDD: write the failing test first, verify it fails for the stated reason, implement, verify it passes, commit. Each task ends with its own focused test command passing and a commit.

---

## File Structure

| File | Responsibility |
|---|---|
| `itsm-backend/migrations/023_unified_intake_rls.sql` (+ dev-reset, verify) | RLS for `intake_requests`/`intake_resolution_snapshots`/`external_identities` |
| `itsm-backend/migrations/024_service_request_work_item_authority.sql` (+ dev-reset, verify) | `service_requests` shared-field drop, RLS rewrite, `service_catalogs.itsm_type` retirement |
| `itsm-backend/migrations/025_external_identity_version.sql` (+ dev-reset, verify) | Optimistic-lock version column on `external_identities` |
| `itsm-backend/handlers/intake/*.go` | Ported Application Service, Resolver, Registry, Identity, Idempotency, Snapshot, Outbox wiring (Task 3) |
| `itsm-backend/handlers/intake/work_item_creator.go` | Fixed in place: `workitemnumber.Allocator` replaces the branch's own `WorkItemNumberAllocator` (Task 4) |
| `itsm-backend/handlers/intake/incident_creator.go` | Fixed in place: correct number source, CTI mapping, priority/status reuse, full DTO field support (Tasks 5–7) |
| `itsm-backend/handlers/intake/service_request_creator.go` | Fixed in place: `entity_type` bug (Task 8) |
| `itsm-backend/handlers/intake/change_creator.go` | New `ChangeCreator`, no extension-level number needed (Task 9) |
| `itsm-backend/controller/incident_controller.go` | `/incidents` wired to Intake (Task 11) |
| `itsm-backend/handlers/service_request/service.go` | Catalog-derived Incident/Change sub-branches wired to Intake; `service_request_item` sub-branch deliberately left on its existing implementation (Task 13) |
| `itsm-backend/service/bpmn/incident_handler.go` | `createIncident` callback wired to Intake with `reporter_id` as both actor and requester (Task 12) |
| `itsm-backend/handlers/service_catalog/repository_impl.go` | Stop deriving `target_class` from `itsm_type` (Task 14) |
| `itsm-frontend/src/lib/api/incident-api.ts`, `service-request-api.ts` | Stable idempotency key generation per submission (Task 10) |

---

### Task 1: File-Level Diff Triage

**Files:**
- Create: `docs/reports/2026-09-02-unified-intake-diff-triage.md`

**Interfaces:**
- Produces: a categorized file list gating every subsequent task's scope.

- [ ] **Step 1: Produce the raw diff**

```bash
cd /home/administrator/project/itsm
git diff --name-status main...feat/kaf-delegation-transactional-delivery > /tmp/intake-diff-raw.txt
wc -l /tmp/intake-diff-raw.txt
```

- [ ] **Step 2: Categorize into the report**

Create `docs/reports/2026-09-02-unified-intake-diff-triage.md` with three sections, populated from `/tmp/intake-diff-raw.txt`:

```markdown
# Unified Intake Diff Triage

## Category A: Intake-exclusive new files (port directly)
<paste every line whose path starts with `itsm-backend/handlers/intake/`,
`itsm-backend/migrations/02[0-2]_unified_intake` or `_work_item_authority`
or `_external_identity`, `itsm-backend/ent/schema/external_identit*.go`,
`itsm-backend/ent/schema/intake_*.go`>

## Category B: Existing files with real conflicts (hand-reconcile, see Tasks 5-16)
- itsm-backend/controller/incident_controller.go
- itsm-backend/handlers/service_request/service.go
- itsm-backend/handlers/service_request/entity.go
- itsm-backend/service/bpmn/incident_handler.go
- itsm-backend/handlers/service_catalog/repository_impl.go
- itsm-backend/ent/schema/*.go (regenerate, do not hand-copy generated code)
- itsm-backend/migration/migrations.go (Task 2 handles this one explicitly)

## Category C: Unrelated historical divergence (do not port)
<paste every remaining line — expected to include stale test scripts,
docs, and any file not touched by Category A or B>
```

- [ ] **Step 3: Commit**

```bash
git add docs/reports/2026-09-02-unified-intake-diff-triage.md
git commit -m "docs: triage unified intake diff before reconciliation"
```

---

### Task 2: Migration Reconciliation (020/021/022 → 023/024/025)

**Files:**
- Create: `itsm-backend/migrations/023_unified_intake_rls.sql`, `_dev_reset.sql`, `_verify.sql`
- Create: `itsm-backend/migrations/024_service_request_work_item_authority.sql`, `_dev_reset.sql`, `_verify.sql`
- Create: `itsm-backend/migrations/025_external_identity_version.sql`, `_dev_reset.sql`, `_verify.sql`
- Modify: `itsm-backend/migration/migrations.go`
- Modify: `itsm-backend/internal/bootstrap/post_schema_migrations_test.go`

**Interfaces:**
- Produces: registered migrations `023_unified_intake_rls`, `024_service_request_work_item_authority`, `025_external_identity_version`.

- [ ] **Step 1: Write the failing registry test**

```go
func TestUnifiedIntakeMigrationsRegisteredInOrder(t *testing.T) {
	migrations := migration.PostSchemaMigrations()
	var versions []string
	for _, m := range migrations {
		versions = append(versions, m.Version)
	}
	require.Contains(t, versions, "023_unified_intake_rls")
	require.Contains(t, versions, "024_service_request_work_item_authority")
	require.Contains(t, versions, "025_external_identity_version")
	idx020 := indexOf(versions, "020_work_item_number_allocator")
	idx023 := indexOf(versions, "023_unified_intake_rls")
	idx024 := indexOf(versions, "024_service_request_work_item_authority")
	idx025 := indexOf(versions, "025_external_identity_version")
	require.True(t, idx020 < idx023 && idx023 < idx024 && idx024 < idx025)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./migration -run TestUnifiedIntakeMigrationsRegisteredInOrder -v
```

Expected: FAIL, versions not found.

- [ ] **Step 3: Extract the source SQL from the feature branch**

```bash
cd /home/administrator/project/itsm
git show feat/kaf-delegation-transactional-delivery:itsm-backend/migration/migrations.go | \
  sed -n '/case "020_unified_intake_rls":/,/case "021_work_item_authority":/p' > /tmp/020_body.txt
git show feat/kaf-delegation-transactional-delivery:itsm-backend/migration/migrations.go | \
  sed -n '/case "022_external_identity_version":/,/default:/p' > /tmp/022_body.txt
```

- [ ] **Step 4: Register `023_unified_intake_rls` (unchanged content)**

Copy the SQL body extracted in Step 3's `/tmp/020_body.txt` verbatim as the new case in `itsm-backend/migration/migrations.go`, added immediately after the existing `022_drop_professional_extension_shared_fields` case:

```go
{
	Version:     "023_unified_intake_rls",
	Description: "Enable and force tenant RLS on unified intake requests, resolution snapshots, and external identity mappings",
	RollbackSQL: "",
},
```

And in `GetMigrationSQL`:

```go
case "023_unified_intake_rls":
	return `<verbatim body from /tmp/020_body.txt>`
```

- [ ] **Step 5: Register `024_service_request_work_item_authority` (incidents portion stripped)**

The source `021_work_item_authority` SQL has this exact structure — reproduce it with the `incidents`-table guard and `DROP COLUMN`/policy block removed, keeping only `service_requests` and `service_catalogs`:

```go
{
	Version:     "024_service_request_work_item_authority",
	Description: "Make WorkItem authoritative for Service Request shared fields, replace extension RLS with ticket joins, and retire service_catalogs.itsm_type (irreversible; forward-fix only). Must apply after the itsm_type-derivation removal lands (Task 14) -- see that task's note.",
	RollbackSQL: "",
},
```

```go
case "024_service_request_work_item_authority":
	return `
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM service_requests sr
        LEFT JOIN tickets t ON t.id = sr.ticket_id
        WHERE t.id IS NULL OR t.record_class <> 'service_request_item'
    ) THEN
        RAISE EXCEPTION 'service request without an authoritative service_request_item WorkItem';
    END IF;
END $$;

UPDATE service_catalogs
SET target_class = CASE lower(trim(itsm_type))
    WHEN 'request' THEN 'service_request_item'
    WHEN 'service_request' THEN 'service_request_item'
    WHEN 'incident' THEN 'incident'
    WHEN 'change' THEN 'change_request'
    WHEN 'change_request' THEN 'change_request'
    ELSE target_class
END
WHERE target_class IS NULL OR trim(target_class) = '';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM service_catalogs
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request')
    ) THEN
        RAISE EXCEPTION 'service catalog target_class backfill is incomplete or unsupported';
    END IF;
END $$;

DROP POLICY IF EXISTS tenant_isolation_service_requests ON service_requests;
DROP POLICY IF EXISTS service_requests_tenant_isolation ON service_requests;
ALTER TABLE service_requests ALTER COLUMN ticket_id SET NOT NULL;
ALTER TABLE service_requests DROP COLUMN IF EXISTS requester_id;
ALTER TABLE service_requests DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE service_requests DROP COLUMN IF EXISTS created_at;
ALTER TABLE service_requests DROP COLUMN IF EXISTS updated_at;
CREATE POLICY tenant_isolation_service_requests ON service_requests
    USING (EXISTS (SELECT 1 FROM tickets WHERE tickets.id = service_requests.ticket_id AND tickets.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint))
    WITH CHECK (EXISTS (SELECT 1 FROM tickets WHERE tickets.id = service_requests.ticket_id AND tickets.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint));

ALTER TABLE service_catalogs ALTER COLUMN target_class SET NOT NULL;
ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_target_class_check;
ALTER TABLE service_catalogs ADD CONSTRAINT service_catalogs_target_class_check
    CHECK (target_class IN ('service_request_item', 'incident', 'change_request'));
ALTER TABLE service_catalogs DROP COLUMN IF EXISTS itsm_type;
`
```

- [ ] **Step 6: Register `025_external_identity_version` (unchanged content)**

```go
{
	Version:     "025_external_identity_version",
	Description: "Add optimistic-lock version to tenant-scoped external identity mappings",
	RollbackSQL: "",
},
```

```go
case "025_external_identity_version":
	return `
ALTER TABLE external_identities
    ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1;
ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_version_positive;
ALTER TABLE external_identities
    ADD CONSTRAINT external_identities_version_positive CHECK (version > 0);
`
```

- [ ] **Step 7: Write the three matching `.sql` files**

For each of `023_unified_intake_rls`, `024_service_request_work_item_authority`, `025_external_identity_version`, create the plain `.sql` apply file (identical body to the `GetMigrationSQL` case above), a `_dev_reset.sql` (mirrors the pattern in the already-merged `20260901_work_item_number_allocator_dev_reset.sql` — truncate/reset only the new tables for 023 and 025; for 024, the dev-reset is a no-op with a comment explaining the column drops are one-way and dev resets happen at the database level, not per-migration), and a `_verify.sql` that raises on missing columns/constraints (mirror the style of `20260901_drop_professional_extension_shared_fields_verify.sql`).

- [ ] **Step 8: Run the registry test**

```bash
cd itsm-backend
go test ./migration -run TestUnifiedIntakeMigrationsRegisteredInOrder -v
go test ./migration ./internal/bootstrap -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add itsm-backend/migration/migrations.go itsm-backend/migrations/023_* itsm-backend/migrations/024_* itsm-backend/migrations/025_* itsm-backend/internal/bootstrap/post_schema_migrations_test.go
git commit -m "feat(intake): register migrations 023-025, drop superseded incidents portion of 021_work_item_authority"
```

---

### Task 3: Port Intake Package Foundation

**Files:**
- Create: `itsm-backend/handlers/intake/{identity,command,creator,resolver,service,handler,idempotency_repository,snapshot_repository,audit_repository,errors,metrics,work_item_creator}.go` and matching `*_test.go`
- Create: `itsm-backend/ent/schema/{intake_request,intake_resolution_snapshot,external_identity}.go`

**Interfaces:**
- Produces: `intake.Identity`, `intake.CreateWorkItemCommand`, `intake.ApplicationService`, `intake.ProfessionalCreator`, `intake.CreatorRegistry`, `intake.Handler`, `intake.WorkItemCreator`.

- [ ] **Step 1: Port every Category-A file unchanged**

```bash
cd /home/administrator/project/itsm
for f in $(git show feat/kaf-delegation-transactional-delivery --stat | grep 'handlers/intake/' | awk '{print $1}'); do
  mkdir -p "$(dirname "$f")"
  git show feat/kaf-delegation-transactional-delivery:"$f" > "$f"
done
git show feat/kaf-delegation-transactional-delivery:itsm-backend/ent/schema/intake_request.go > itsm-backend/ent/schema/intake_request.go 2>/dev/null || true
git show feat/kaf-delegation-transactional-delivery:itsm-backend/ent/schema/intake_resolution_snapshot.go > itsm-backend/ent/schema/intake_resolution_snapshot.go 2>/dev/null || true
git show feat/kaf-delegation-transactional-delivery:itsm-backend/ent/schema/external_identity.go > itsm-backend/ent/schema/external_identity.go 2>/dev/null || true
```

Use Task 1's Category A list as the authoritative file set — the exact paths above are illustrative; run against the real triage report. This step ports `work_item_creator.go` **as-is, including its own `WorkItemNumberAllocator` interface** — Task 4 fixes that interface's implementation; do not fix it in this task.

- [ ] **Step 2: Regenerate Ent**

```bash
cd itsm-backend
go generate ./ent
gofmt -l ent/ | xargs -r gofmt -w
```

- [ ] **Step 3: Run the ported unit tests**

```bash
cd itsm-backend
go test ./handlers/intake -count=1 2>&1 | tail -60
```

Expected: PASS for everything except tests that exercise the field-mapping/algorithm bugs Tasks 4-9 fix — record which tests fail and why, do not fix them here.

- [ ] **Step 4: Build check**

```bash
cd itsm-backend
go build ./handlers/intake/... 2>&1 | tail -40
```

Expected: builds cleanly (nothing in this task deletes a type yet — that starts in Task 4).

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/handlers/intake itsm-backend/ent
git commit -m "feat(intake): port Unified Intake application service onto P1 baseline unchanged (fixes follow in subsequent tasks)"
```

---

### Task 4: `WorkItemCreator` Uses `workitemnumber.Allocator` for `tickets.ticket_number`

**Files:**
- Modify: `itsm-backend/handlers/intake/work_item_creator.go`
- Modify: `itsm-backend/handlers/intake/work_item_creator_test.go`

**Interfaces:**
- Consumes: `workitemnumber.Allocator` (existing, `itsm-backend/repository/workitemnumber/allocator.go`, signature `Allocate(ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time) (string, error)`).
- Produces: `NewWorkItemCreator(numbers workitemnumber.Allocator) *WorkItemCreator`.

This is the **only** place `tickets.ticket_number` is allocated for anything Intake creates — domain creators (`IncidentCreator`, `ServiceRequestItemCreator`, `ChangeCreator`) must leave `WorkItemDraft.TicketNumber` empty and let this method fill it, exactly as the ported code already does when `draft.TicketNumber == ""`. Do not duplicate this allocation inside any domain creator.

- [ ] **Step 1: Write the failing test**

```go
func TestWorkItemCreatorUsesWorkItemNumberAllocator(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemcreator?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	tenant := createTestTenant(t, client)
	requester := createTestUser(t, client, tenant.ID)
	allocator := &stubTicketAllocator{number: "TKT-202609-000001"}
	creator := NewWorkItemCreator(allocator)

	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	ticket, err := creator.CreateBase(context.Background(), tx, &CreationPlan{
		WorkItem: WorkItemDraft{
			TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID,
			RecordClass: RecordClassIncident, Title: "VPN down",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "TKT-202609-000001", ticket.TicketNumber)
	assert.Equal(t, 1, allocator.calls)
}

type stubTicketAllocator struct {
	number string
	calls  int
	err    error
}

func (s *stubTicketAllocator) Allocate(ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time) (string, error) {
	s.calls++
	return s.number, s.err
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./handlers/intake -run TestWorkItemCreatorUsesWorkItemNumberAllocator -v
```

Expected: FAIL — `NewWorkItemCreator` currently expects the branch's own `WorkItemNumberAllocator` (`GenerateWorkItemNumber(ctx, tenantID)`), not `workitemnumber.Allocator` (`Allocate(ctx, client, tenantID, issuedAt)`).

- [ ] **Step 3: Replace the allocator type and call site**

Delete the ported `WorkItemNumberAllocator` interface and `WorkItemNumberFunc` type entirely. Change the struct and constructor:

```go
type WorkItemCreator struct {
	numbers workitemnumber.Allocator
	now     func() time.Time
}

func NewWorkItemCreator(numbers workitemnumber.Allocator) *WorkItemCreator {
	return &WorkItemCreator{numbers: numbers, now: time.Now}
}
```

In `CreateBase`, replace the allocation block:

```go
if draft.TicketNumber == "" {
	if c.numbers == nil {
		return nil, NewInternalFailure("work item number allocator is required", nil)
	}
	issuedAt := c.now().UTC()
	number, err := c.numbers.Allocate(ctx, tx.Client(), draft.TenantID, issuedAt)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not allocate work item number", err)
	}
	draft.TicketNumber = strings.TrimSpace(number)
}
```

Everything else in `CreateBase` (the validation, the `tx.Ticket.Create()...Save(ctx)` call) is unchanged — this task only touches the allocator dependency and the allocation call.

- [ ] **Step 4: Update the Registry/Application Service wiring**

Wherever `NewWorkItemCreator(...)` is constructed (in the ported `service.go` or wherever the Application Service assembles its dependencies), pass the shared `workitemnumber.Allocator` instance instead of a bespoke closure. This is the same instance already passed to `ticket.NewEntRepository`/`problem.NewEntRepository`/etc. per P1-A's bootstrap wiring — reuse it, do not construct a second one.

- [ ] **Step 5: Run and commit**

```bash
cd itsm-backend
gofmt -w handlers/intake/work_item_creator.go
go test ./handlers/intake -run TestWorkItemCreator -v
git add itsm-backend/handlers/intake/work_item_creator.go itsm-backend/handlers/intake/work_item_creator_test.go itsm-backend/handlers/intake/service.go
git commit -m "fix(intake): WorkItemCreator allocates tickets.ticket_number via workitemnumber.Allocator, the single P1-A number source"
```

---

### Task 5: Fix `IncidentCreator`'s Number Source and CTI Field Mapping

**Files:**
- Modify: `itsm-backend/service/incident_service.go` (export the existing incident-number generator)
- Modify: `itsm-backend/handlers/intake/incident_creator.go`
- Modify: `itsm-backend/handlers/intake/incident_creator_test.go`

**Interfaces:**
- Produces: `service.IncidentService.GenerateIncidentNumber(ctx, tenantID) (string, error)` (renamed from the existing unexported `generateIncidentNumber`, same body, same callers updated in place).
- Produces: `service.ResolveIncidentCategory(ctx, client, tenantID, categoryName, subcategoryName) (*int, error)` (renamed from the existing unexported `resolveIncidentCategory` in `incident_work_item_authority.go`, same body, same callers updated in place).
- Produces: `intake.IncidentNumberGenerator` and `intake.CategoryResolver` interfaces and `NewIncidentCreator(numbers IncidentNumberGenerator, categories CategoryResolver) *IncidentCreator`.

`incidents.incident_number` (format `INC-YYYYMM-NNNNNN`, generated today by `IncidentService.generateIncidentNumber` via `SequenceService` with a DB fallback) is a **different identifier from `tickets.ticket_number`** (format `TKT-YYYYMM-NNNNNN`, Task 4's concern). `IncidentCreator` must keep using the existing incident-number mechanism, not `workitemnumber.Allocator` — do not conflate the two.

`subcategory` is **not** a dead field needing a new destination column — `service/incident_work_item_authority.go:32`'s existing `resolveIncidentCategory(ctx, client, tenantID, categoryName, subcategoryName) (*int, error)` already consumes both a category and subcategory name together to resolve one `ticket_categories.id` (subcategory is a child node of category in the same tenant's category tree). `WorkItemDraft.CategoryID` is that resolved ID. `IncidentCreator` must call this exported function when the caller supplies free-text `category`/`subcategory` names (the direct `/incidents` API's shape) and fall back to the already-numeric `in.CTI.CategoryID` when the caller supplies structured CTI instead (the Catalog/KAF channel's shape) — these are two different callers of the same `WorkItemDraft.CategoryID` destination, not two competing algorithms, since only one of the two inputs is ever present for a given creation.

- [ ] **Step 1: Write the failing test for the exported generator**

```go
func TestGenerateIncidentNumberIsExported(t *testing.T) {
	svc := newTestIncidentServiceWithSequence(t) // existing test helper
	number, err := svc.GenerateIncidentNumber(context.Background(), 1)
	require.NoError(t, err)
	assert.Regexp(t, `^INC-\d{6}-\d{6}$`, number)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./service -run TestGenerateIncidentNumberIsExported -v
```

Expected: FAIL, method is unexported.

- [ ] **Step 3: Rename `generateIncidentNumber` → `GenerateIncidentNumber`**

```go
// GenerateIncidentNumber 生成事件编号，优先使用 Redis 序列。Exported so
// intake.IncidentCreator can use the same authoritative generator instead of
// a second implementation.
func (s *IncidentService) GenerateIncidentNumber(ctx context.Context, tenantID int) (string, error) {
	// body unchanged from the existing generateIncidentNumber
}
```

Update every existing internal call site (`s.generateIncidentNumber(...)` → `s.GenerateIncidentNumber(...)`) in `incident_service.go` in the same change — `rg -n "generateIncidentNumber\(" itsm-backend/service` must show zero remaining lowercase call sites afterward.

- [ ] **Step 4: Rename `resolveIncidentCategory` → `ResolveIncidentCategory`**

```go
// ResolveIncidentCategory resolves the existing string API into the one
// structured WorkItem category relation. A supplied subcategory must be an
// active child of the supplied category in the same tenant. Exported so
// intake.IncidentCreator can use the same authoritative resolver instead of
// a second implementation.
func ResolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error) {
	// body unchanged from the existing resolveIncidentCategory in incident_work_item_authority.go
}
```

Update every existing internal call site (`resolveIncidentCategory(...)` → `ResolveIncidentCategory(...)`) across `incident_service.go` and `incident_work_item_authority.go` in the same change — `rg -n "resolveIncidentCategory\("  itsm-backend/service` must show zero remaining lowercase call sites afterward.

- [ ] **Step 5: Run the service tests**

```bash
cd itsm-backend
go test ./service -run 'TestGenerateIncidentNumber|TestResolveIncidentCategory|TestIncidentService_CreateIncident' -v
```

Expected: PASS, no behavior change.

- [ ] **Step 6: Write the failing `IncidentCreator` tests**

```go
func TestIncidentCreatorResolvesCategoryFromStructuredCTI(t *testing.T) {
	generator := &stubIncidentNumberGenerator{number: "INC-202609-000001"}
	categories := &stubCategoryResolver{} // must NOT be called when CTI.CategoryID is already present
	creator := NewIncidentCreator(generator, categories)
	plan, err := creator.Prepare(context.Background(), nil, ResolvedIntake{
		Identity: Identity{TenantID: 7, ActorID: 3, RequesterID: 3},
		Command:  CreateWorkItemCommand{Title: "VPN down"},
		CTI:      ResolvedCTI{CategoryID: intPtr(55)},
	})
	require.NoError(t, err)
	extPlan := plan.ProfessionalInput.(IncidentExtensionPlan)
	assert.Equal(t, "INC-202609-000001", extPlan.IncidentNumber)
	assert.Equal(t, 1, generator.calls)
	assert.Equal(t, 55, *plan.WorkItem.CategoryID)
	assert.Zero(t, categories.calls)
	assert.Empty(t, plan.WorkItem.TicketNumber) // WorkItemCreator (Task 4) allocates this, not IncidentCreator
	assert.Equal(t, "incident", extPlan.Type) // no Type supplied -> same default IncidentService.CreateIncident uses
}

func TestIncidentCreatorResolvesCategoryFromFreeTextNames(t *testing.T) {
	generator := &stubIncidentNumberGenerator{number: "INC-202609-000002"}
	categories := &stubCategoryResolver{id: intPtr(77)}
	creator := NewIncidentCreator(generator, categories)
	plan, err := creator.Prepare(context.Background(), nil, ResolvedIntake{
		Identity: Identity{TenantID: 7, ActorID: 3, RequesterID: 3},
		Command:  CreateWorkItemCommand{Title: "VPN down", Incident: &IncidentInput{Category: "performance", Subcategory: "cpu", Type: "security_event"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 77, *plan.WorkItem.CategoryID)
	assert.Equal(t, "performance", categories.gotCategory)
	assert.Equal(t, "cpu", categories.gotSubcategory)
	extPlan := plan.ProfessionalInput.(IncidentExtensionPlan)
	assert.Equal(t, "security_event", extPlan.Type) // explicit Type passes through unchanged
}

type stubIncidentNumberGenerator struct {
	number string
	calls  int
	err    error
}

func (s *stubIncidentNumberGenerator) GenerateIncidentNumber(ctx context.Context, tenantID int) (string, error) {
	s.calls++
	return s.number, s.err
}

type stubCategoryResolver struct {
	id             *int
	err            error
	calls          int
	gotCategory    string
	gotSubcategory string
}

func (s *stubCategoryResolver) ResolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error) {
	s.calls++
	s.gotCategory, s.gotSubcategory = categoryName, subcategoryName
	return s.id, s.err
}
```

- [ ] **Step 7: Run to verify they fail**

```bash
cd itsm-backend
go test ./handlers/intake -run 'TestIncidentCreatorResolvesCategory' -v
```

Expected: FAIL — `NewIncidentCreator` still expects the deleted `IncidentNumberAllocator`/`GenerateIncidentNumberForIntake` shape, has no `CategoryResolver` parameter, and `Prepare` still writes `source`/`category`/`subcategory` onto fields P1 removed from the Incident extension.

- [ ] **Step 8: Fix the struct, constructor, and `Prepare`**

```go
type IncidentNumberGenerator interface {
	GenerateIncidentNumber(ctx context.Context, tenantID int) (string, error)
}

type CategoryResolver interface {
	ResolveIncidentCategory(ctx context.Context, client *ent.Client, tenantID int, categoryName, subcategoryName string) (*int, error)
}

type IncidentCreator struct {
	numbers    IncidentNumberGenerator
	categories CategoryResolver
	now        func() time.Time
}

func NewIncidentCreator(numbers IncidentNumberGenerator, categories CategoryResolver) *IncidentCreator {
	return &IncidentCreator{numbers: numbers, categories: categories, now: time.Now}
}

func (c *IncidentCreator) Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error) {
	number, err := c.numbers.GenerateIncidentNumber(ctx, in.Identity.TenantID)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not allocate incident number", err)
	}
	issuedAt := c.now().UTC()
	detectedAt := issuedAt
	if in.Command.Incident != nil && in.Command.Incident.DetectedAt != "" {
		detectedAt, err = time.Parse(time.RFC3339, in.Command.Incident.DetectedAt)
		if err != nil {
			return nil, NewDomainValidationFailed("incident detected time is invalid", err)
		}
		detectedAt = detectedAt.UTC()
	}
	severity := defaultLevel(incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Severity }))
	impact := defaultLevel(incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Impact }))
	urgency := defaultLevel(incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Urgency }))
	incidentType := incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Type })
	if incidentType == "" {
		incidentType = "incident" // same default IncidentService.CreateIncident applies to req.Type
	}
	source := strings.TrimSpace(in.Identity.Channel)
	if in.Command.SourceReference != nil && strings.TrimSpace(in.Command.SourceReference.Provider) != "" {
		source = strings.TrimSpace(in.Command.SourceReference.Provider)
	}
	categoryID := copyInt(in.CTI.CategoryID)
	if categoryID == nil {
		categoryName := incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Category })
		subcategoryName := incidentInputField(in.Command.Incident, func(i *IncidentInput) string { return i.Subcategory })
		if categoryName != "" {
			categoryID, err = c.categories.ResolveIncidentCategory(ctx, tx.Client(), in.Identity.TenantID, categoryName, subcategoryName)
			if err != nil {
				return nil, NewDomainValidationFailed("category is invalid", err)
			}
		}
	}
	professional := IncidentExtensionPlan{
		IncidentNumber: strings.TrimSpace(number), Type: incidentType, Severity: severity, Impact: impact,
		Urgency: urgency, DetectedAt: detectedAt,
	}
	return &CreationPlan{
		Resolved: in,
		WorkItem: WorkItemDraft{
			// TicketNumber intentionally left empty -- WorkItemCreator.CreateBase (Task 4) allocates it.
			TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
			RecordClass: RecordClassIncident, Title: in.Command.Title, Description: in.Command.Description,
			Source: source, CategoryID: categoryID, SLADefinitionID: copyInt(in.SLADefinitionID),
		},
		ProfessionalInput: professional,
	}, nil
}

func incidentInputField(input *IncidentInput, get func(*IncidentInput) string) string {
	if input == nil {
		return ""
	}
	return get(input)
}
```

Add `Category string`, `Subcategory string`, and `Type string` (free text, matching `dto.CreateIncidentRequest`'s existing `category`/`subcategory`/`type` shape) to `IncidentInput` in this step. Add a matching `Type string` field to `IncidentExtensionPlan`.

Fixed in this same step, per the spec's field-mapping findings:
- `WorkItemDraft.CategoryID` correctly prefers structured `in.CTI.CategoryID` (Catalog/KAF channel) and falls back to `ResolveIncidentCategory` from free-text `category`/`subcategory` names (direct `/incidents` API channel) — the previous buggy `in.CTI.ItemID` reference is gone, and `subcategory` is no longer treated as a dead field: it is a real input to the existing category-tree resolver, exactly matching what `IncidentService.CreateIncident` already does with the same two strings today.
- `source` semantics live only on `WorkItemDraft` (`tickets.source`) — `IncidentExtensionPlan` no longer carries `source`/`category`/`subcategory`, since `ent/schema/incident.go` no longer has those columns.
- `IncidentExtensionPlan.Type` defaults to `"incident"` when the caller supplies none, mirroring `IncidentService.CreateIncident`'s existing `incidentType := req.Type; if incidentType == "" { incidentType = "incident" }` (`service/incident_service.go:155-158`) — this is the Incident extension's own `type` column (`security_event`/`alert`/etc.), a separate concept from `WorkItemDraft.RecordClass`, which stays `"incident"` unconditionally.

- [ ] **Step 9: Fix `CreateExtension` to drop `SetSource`/`SetCategory`/`SetSubcategory`**

```go
func (c *IncidentCreator) CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error) {
	if tx == nil || workItem == nil || plan == nil {
		return nil, NewInternalFailure("incident extension transaction, work item, and plan are required", nil)
	}
	input, ok := plan.ProfessionalInput.(IncidentExtensionPlan)
	if !ok {
		return nil, NewDomainValidationFailed("incident creation plan is invalid", nil)
	}
	saved, err := tx.Incident.Create().
		SetWorkItemID(workItem.ID).
		SetType(input.Type).
		SetSeverity(input.Severity).
		SetImpact(input.Impact).
		SetUrgency(input.Urgency).
		SetIncidentNumber(input.IncidentNumber).
		SetDetectedAt(input.DetectedAt).
		SetIsAutomated(false).
		Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create incident extension", err)
	}
	return &ProfessionalReference{Type: "incident", ID: saved.ID}, nil
}
```

- [ ] **Step 10: Run the tests**

```bash
cd itsm-backend
gofmt -w handlers/intake/incident_creator.go service/incident_service.go service/incident_work_item_authority.go
go test ./handlers/intake -run 'TestIncidentCreator' -v
go test ./service -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add itsm-backend/handlers/intake/incident_creator.go itsm-backend/handlers/intake/incident_creator_test.go itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go itsm-backend/service/incident_work_item_authority.go itsm-backend/service/incident_work_item_authority_test.go
git commit -m "fix(intake): IncidentCreator uses IncidentService's existing incident-number generator and category resolver (both distinct from tickets.ticket_number), fixes CTI/free-text category mapping, drops fields P1 already removed"
```

---

### Task 6: `IncidentCreator` Reuses the Existing Priority Matrix and Status

**Files:**
- Modify: `itsm-backend/service/incident_service.go:138-160` (extract a shared function)
- Modify: `itsm-backend/handlers/intake/incident_creator.go`
- Modify: `itsm-backend/handlers/intake/incident_creator_test.go`

**Interfaces:**
- Produces: `service.ResolveIncidentPriority(ctx context.Context, matrixService *PriorityMatrixService, tenantID int, explicitPriority, impact, urgency string) string` — an exported, side-effect-free function both `IncidentService.CreateIncident` and `IncidentCreator` call, so there is exactly one implementation.

- [ ] **Step 1: Write the failing test for the extracted function**

```go
func TestResolveIncidentPriorityPrefersExplicitValue(t *testing.T) {
	got := service.ResolveIncidentPriority(context.Background(), nil, 1, "critical", "medium", "medium")
	assert.Equal(t, "critical", got)
}

func TestResolveIncidentPriorityFallsBackToMatrix(t *testing.T) {
	matrix := newTestPriorityMatrixService(t) // existing test helper in incident_service_test.go
	got := service.ResolveIncidentPriority(context.Background(), matrix, 1, "", "high", "high")
	assert.NotEmpty(t, got)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./service -run TestResolveIncidentPriority -v
```

Expected: FAIL, function undefined.

- [ ] **Step 3: Extract the function from `CreateIncident`'s existing priority-calculation block**

```go
// ResolveIncidentPriority is the single authority for incident priority: an
// explicit caller-supplied value always wins, otherwise the tenant's
// priority matrix computes it from impact/urgency, falling back to "medium"
// if the matrix is unavailable or errors. IncidentService.CreateIncident and
// intake.IncidentCreator both call this -- there is no second algorithm.
func ResolveIncidentPriority(ctx context.Context, matrixService *PriorityMatrixService, tenantID int, explicitPriority, impact, urgency string) string {
	if explicitPriority != "" {
		return explicitPriority
	}
	if matrixService == nil {
		return "medium"
	}
	calculated, err := matrixService.CalculatePriority(tenantID, impact, urgency)
	if err != nil {
		return "medium"
	}
	return calculated
}
```

Update `IncidentService.CreateIncident`'s existing inline priority block to call this instead:

```go
priority := ResolveIncidentPriority(ctx, s.priorityMatrixService, tenantID, req.Priority, impact, urgency)
```

- [ ] **Step 4: Run the service tests**

```bash
cd itsm-backend
go test ./service -run 'TestResolveIncidentPriority|TestIncidentService_CreateIncident' -v
```

Expected: PASS, no behavior change for existing `CreateIncident` callers.

- [ ] **Step 5: Add priority and status to `IncidentCreator`**

`IncidentCreator` needs a `*service.PriorityMatrixService` dependency added to the `numbers`/`categories` pair Task 5 introduced:

```go
type IncidentCreator struct {
	numbers    IncidentNumberGenerator
	categories CategoryResolver
	matrix     *service.PriorityMatrixService
	now        func() time.Time
}

func NewIncidentCreator(numbers IncidentNumberGenerator, categories CategoryResolver, matrix *service.PriorityMatrixService) *IncidentCreator {
	return &IncidentCreator{numbers: numbers, categories: categories, matrix: matrix, now: time.Now}
}
```

In `Prepare`, add priority resolution and the explicit status, into the `WorkItemDraft` literal:

```go
priority := service.ResolveIncidentPriority(ctx, c.matrix, in.Identity.TenantID, explicitPriority(in.Command.Incident), impact, urgency)
```

```go
Status: string(common.IncidentStatusNew), Priority: priority,
```

Add the small accessor:

```go
func explicitPriority(input *IncidentInput) string {
	if input == nil {
		return ""
	}
	return input.ExplicitPriority
}
```

Add `ExplicitPriority string` to `IncidentInput` (distinct from `Severity` — this is Task 7's DTO-completeness field, added here because `Prepare` needs it now; Task 7 adds the remaining fields `AssigneeID`/`ImpactAnalysis`/`Metadata`).

- [ ] **Step 6: Write the regression test proving parity with `IncidentService`**

```go
func TestIncidentCreatorMatchesIncidentServicePriorityAndStatus(t *testing.T) {
	f := newIntakeFixture(t) // existing fixture helper
	matrix := newTestPriorityMatrixService(t)
	creator := NewIncidentCreator(f.numberGenerator, f.categoryResolver, matrix)
	plan, err := creator.Prepare(context.Background(), f.tx, ResolvedIntake{
		Identity: Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID},
		Command:  CreateWorkItemCommand{Title: "t", Incident: &IncidentInput{Impact: "high", Urgency: "high"}},
	})
	require.NoError(t, err)
	directPriority := service.ResolveIncidentPriority(context.Background(), matrix, f.tenant.ID, "", "high", "high")
	assert.Equal(t, directPriority, plan.WorkItem.Priority)
	assert.Equal(t, "new", plan.WorkItem.Status)
}
```

- [ ] **Step 7: Run and commit**

```bash
cd itsm-backend
go test ./handlers/intake ./service -run 'TestIncidentCreator|TestResolveIncidentPriority' -v
git add itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go itsm-backend/handlers/intake/incident_creator.go itsm-backend/handlers/intake/incident_creator_test.go
git commit -m "fix(intake): IncidentCreator reuses IncidentService's priority matrix and 'new' status, deletes Intake's own simplified algorithm"
```

---

### Task 7: `dto.CreateIncidentRequest` Field Completeness in `IncidentInput`

**Files:**
- Modify: `itsm-backend/handlers/intake/command.go`
- Modify: `itsm-backend/handlers/intake/incident_creator.go`
- Modify: `itsm-backend/handlers/intake/incident_creator_test.go`
- Modify: `itsm-backend/service/incident_service.go` (export the assignee validator)

**Interfaces:**
- Produces: extended `IncidentInput` carrying every field `dto.CreateIncidentRequest` supports today.
- Produces: `service.IncidentService.ValidateIncidentAssignee(ctx, client *ent.Client, assigneeID, tenantID int) error` (renamed from the existing unexported `validateIncidentAssignee`).

- [ ] **Step 1: Write the failing test**

```go
func TestIncidentCreatorCarriesFullDTOFieldSet(t *testing.T) {
	f := newIntakeFixture(t)
	creator := NewIncidentCreator(f.numberGenerator, f.categoryResolver, nil, f.assigneeValidator)
	plan, err := creator.Prepare(context.Background(), f.tx, ResolvedIntake{
		Identity: Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID},
		Command: CreateWorkItemCommand{
			Title: "t",
			Incident: &IncidentInput{
				Impact: "high", Urgency: "high",
				AssigneeID:     intPtr(f.otherActor.ID),
				ImpactAnalysis: map[string]interface{}{"scope": "regional"},
				Metadata:       map[string]interface{}{"source_ticket": "legacy-1"},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, f.otherActor.ID, *plan.WorkItem.AssigneeID)
	extPlan := plan.ProfessionalInput.(IncidentExtensionPlan)
	assert.Equal(t, "regional", extPlan.ImpactAnalysis["scope"])
	assert.Equal(t, "legacy-1", extPlan.Metadata["source_ticket"])
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./handlers/intake -run TestIncidentCreatorCarriesFullDTOFieldSet -v
```

Expected: FAIL, `IncidentInput` has no `AssigneeID`/`ImpactAnalysis`/`Metadata` fields, `WorkItemDraft` has no `AssigneeID`.

- [ ] **Step 3: Extend `IncidentInput`, `IncidentExtensionPlan`, `WorkItemDraft`**

```go
type IncidentInput struct {
	Type             string                 `json:"type,omitempty"`
	Severity         string                 `json:"severity,omitempty"`
	ExplicitPriority string                 `json:"priority,omitempty"`
	Impact           string                 `json:"impact,omitempty"`
	Urgency          string                 `json:"urgency,omitempty"`
	DetectedAt       string                 `json:"detectedAt,omitempty"`
	Category         string                 `json:"category,omitempty"`
	Subcategory      string                 `json:"subcategory,omitempty"`
	AssigneeID       *int                   `json:"assigneeId,omitempty"`
	ImpactAnalysis   map[string]interface{} `json:"impactAnalysis,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}
```

This adds `AssigneeID`/`ImpactAnalysis`/`Metadata` to the `IncidentInput` Task 5 and Task 6 already established (`Category`/`Subcategory`/`Type` from Task 5, `ExplicitPriority` from Task 6) — do not drop those fields by redefining the struct from scratch. Add `ImpactAnalysis`, `Metadata` to `IncidentExtensionPlan`; add `AssigneeID *int` to `WorkItemDraft` (confirm it is not already present from the Task 3 port before adding a duplicate field).

- [ ] **Step 4: Export the assignee validator and wire it into `IncidentCreator`**

```go
// ValidateIncidentAssignee 校验目标处理人存在、激活且属于同一租户。Exported so
// intake.IncidentCreator can reuse the same check IncidentService.CreateIncident
// already performs -- there is no second assignee-validation implementation.
func (s *IncidentService) ValidateIncidentAssignee(ctx context.Context, client *ent.Client, assigneeID, tenantID int) error {
	// body unchanged from the existing validateIncidentAssignee
}
```

Update the existing internal call site inside `CreateIncident` from `s.validateIncidentAssignee(...)` to `s.ValidateIncidentAssignee(...)`.

```go
type AssigneeValidator interface {
	ValidateIncidentAssignee(ctx context.Context, client *ent.Client, assigneeID, tenantID int) error
}

type IncidentCreator struct {
	numbers    IncidentNumberGenerator
	categories CategoryResolver
	matrix     *service.PriorityMatrixService
	assignees  AssigneeValidator
	now        func() time.Time
}

func NewIncidentCreator(numbers IncidentNumberGenerator, categories CategoryResolver, matrix *service.PriorityMatrixService, assignees AssigneeValidator) *IncidentCreator {
	return &IncidentCreator{numbers: numbers, categories: categories, matrix: matrix, assignees: assignees, now: time.Now}
}
```

This is `IncidentCreator`'s final constructor shape — Tasks 5, 6, and 7 each added one dependency in place; every later task that constructs an `IncidentCreator` (Task 9's registry wiring, any test fixture) uses this four-argument form.

In `Prepare`:

```go
if in.Command.Incident != nil && in.Command.Incident.AssigneeID != nil {
	if err := c.assignees.ValidateIncidentAssignee(ctx, tx.Client(), *in.Command.Incident.AssigneeID, in.Identity.TenantID); err != nil {
		return nil, NewDomainValidationFailed("assignee is invalid", err)
	}
}
```

and add `AssigneeID: copyInt(incidentAssigneeID(in.Command.Incident))` to the `WorkItemDraft` literal, with:

```go
func incidentAssigneeID(input *IncidentInput) *int {
	if input == nil {
		return nil
	}
	return input.AssigneeID
}
```

In `CreateExtension`, add:

```go
SetImpactAnalysis(input.ImpactAnalysis).
SetMetadata(input.Metadata)
```

- [ ] **Step 5: Run and commit**

```bash
cd itsm-backend
gofmt -w handlers/intake service/incident_service.go
go test ./handlers/intake ./service -run 'TestIncidentCreator|TestIncidentService' -count=1
git add itsm-backend/handlers/intake itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go
git commit -m "feat(intake): carry assigneeId/impactAnalysis/metadata through IncidentCreator, matching existing CreateIncidentRequest fields"
```

---

### Task 8: `ServiceRequestItemCreator` Custom Field `entity_type` Fix

**Files:**
- Modify: `itsm-backend/handlers/intake/service.go`
- Modify: `itsm-backend/handlers/intake/service_test.go`

**Interfaces:**
- Consumes: `service.NewFieldValueService` (existing, unchanged signature).

- [ ] **Step 1: Write the failing test**

```go
func TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest(t *testing.T) {
	f := newIntakeFixture(t)
	recorder := &recordingFieldValueWriter{}
	svc := &Service{fieldValues: recorder}
	resolved := &ResolvedIntake{
		Identity: Identity{TenantID: f.tenant.ID}, RecordClass: RecordClassServiceRequestItem,
		Catalog: &ResolvedCatalog{ID: 9}, Command: CreateWorkItemCommand{FormValues: map[string]any{"k": "v"}},
	}
	err := svc.writeFieldValues(context.Background(), f.tx, resolved, 101, &ProfessionalReference{Type: "service_request", ID: 202})
	require.NoError(t, err)
	assert.Equal(t, "ticket", recorder.valueType)
	assert.Equal(t, 101, recorder.valueID)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./handlers/intake -run TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest -v
```

Expected: FAIL, current code asserts `"service_request"`/`professional.ID`.

- [ ] **Step 3: Fix `writeFieldValues`**

```go
func (s *Service) writeFieldValues(ctx context.Context, tx *ent.Tx, resolved *ResolvedIntake, workItemID int, professional *ProfessionalReference) error {
	if resolved.Catalog == nil || len(resolved.Command.FormValues) == 0 {
		return nil
	}
	return s.fieldValues.CreateValuesTx(
		ctx, tx, resolved.Identity.TenantID, "service_catalog", resolved.Catalog.ID,
		"ticket", workItemID, resolved.Command.FormValues,
	)
}
```

The `valueType, valueID := "ticket", workItemID` branch logic and the now-dead `professional.ID`-based branch are removed entirely — there is exactly one destination for custom field values, matching `handlers/service_request/service.go:224`.

- [ ] **Step 4: Run and commit**

```bash
cd itsm-backend
go test ./handlers/intake -run TestWriteFieldValues -v
git add itsm-backend/handlers/intake/service.go itsm-backend/handlers/intake/service_test.go
git commit -m "fix(intake): write service request custom fields against ticket/WorkItem id, matching the existing authoritative read path"
```

---

### Task 9: New `ChangeCreator` (No Extension-Level Number Needed)

**Files:**
- Create: `itsm-backend/handlers/intake/change_creator.go`
- Create: `itsm-backend/handlers/intake/change_creator_test.go`
- Modify: `itsm-backend/handlers/intake/command.go` (add `RecordClassChangeRequest`, `IntakeKindChange`, `ChangeInput`)
- Modify: `itsm-backend/handlers/intake/resolver.go:106` (accept the new record class)

**Interfaces:**
- Produces: `NewChangeCreator() *ChangeCreator` implementing `ProfessionalCreator` — **no allocator dependency**, since `ent/schema/change.go` has no extension-level identifier field (only `work_item_id`); `tickets.ticket_number` is handled entirely by `WorkItemCreator` (Task 4).

- [ ] **Step 1: Add the command types**

```go
const RecordClassChangeRequest = "change_request"

type ChangeInput struct {
	Justification      string   `json:"justification,omitempty"`
	Type               string   `json:"type,omitempty"`
	ImpactScope        string   `json:"impactScope,omitempty"`
	RiskLevel          string   `json:"riskLevel,omitempty"`
	PlannedStartDate   string   `json:"plannedStartDate,omitempty"`
	PlannedEndDate     string   `json:"plannedEndDate,omitempty"`
	ImplementationPlan string   `json:"implementationPlan,omitempty"`
	RollbackPlan       string   `json:"rollbackPlan,omitempty"`
	AffectedCIs        []string `json:"affectedCis,omitempty"`
}
```

Add `Change *ChangeInput` to `CreateWorkItemCommand`.

- [ ] **Step 2: Write the failing creator test**

```go
func TestChangeCreatorCreatesRealChangeExtension(t *testing.T) {
	f := newIntakeFixture(t)
	creator := NewChangeCreator()
	plan, err := creator.Prepare(context.Background(), f.tx, ResolvedIntake{
		Identity: Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID},
		Command: CreateWorkItemCommand{
			Title: "Upgrade router firmware",
			Change: &ChangeInput{Type: "normal", RiskLevel: "medium", ImpactScope: "low"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, plan.WorkItem.TicketNumber) // WorkItemCreator allocates this, ChangeCreator does not

	// WorkItemCreator.CreateBase would normally build this row; the test builds
	// it directly to isolate ChangeCreator.CreateExtension's behavior.
	workItem, err := f.tx.Ticket.Create().
		SetTitle(plan.WorkItem.Title).SetType("change").SetRecordClass(RecordClassChangeRequest).
		SetPriority("medium").SetStatus("draft").SetTicketNumber("TKT-202609-000099").
		SetRequesterID(plan.WorkItem.RequesterID).SetOpenedByID(plan.WorkItem.ActorID).
		SetTenantID(plan.WorkItem.TenantID).Save(context.Background())
	require.NoError(t, err)
	ref, err := creator.CreateExtension(context.Background(), f.tx, workItem, plan)
	require.NoError(t, err)
	assert.Equal(t, "change", ref.Type)
	saved, err := f.tx.Change.Get(context.Background(), ref.ID)
	require.NoError(t, err)
	assert.Equal(t, "medium", saved.RiskLevel)
	assert.Equal(t, workItem.ID, saved.WorkItemID)
}
```

- [ ] **Step 3: Run to verify it fails**

```bash
cd itsm-backend
go test ./handlers/intake -run TestChangeCreatorCreatesRealChangeExtension -v
```

Expected: FAIL, `NewChangeCreator` undefined.

- [ ] **Step 4: Implement `ChangeCreator`, porting the extension-write logic from `handlers/change/repository_impl.go:383-393`**

```go
package intake

import (
	"strings"
	"time"

	"itsm-backend/ent"
)

type ChangeExtensionPlan struct {
	Justification, Type, ImpactScope, RiskLevel, ImplementationPlan, RollbackPlan string
	PlannedStartDate, PlannedEndDate                                             *time.Time
	AffectedCIs                                                                  []string
}

type ChangeCreator struct{}

func NewChangeCreator() *ChangeCreator { return &ChangeCreator{} }

func (c *ChangeCreator) RecordClass() string { return RecordClassChangeRequest }

func (c *ChangeCreator) Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error) {
	change := in.Command.Change
	if change == nil {
		change = &ChangeInput{}
	}
	plannedStart, err := parseOptionalTime(change.PlannedStartDate)
	if err != nil {
		return nil, NewDomainValidationFailed("plannedStartDate is invalid", err)
	}
	plannedEnd, err := parseOptionalTime(change.PlannedEndDate)
	if err != nil {
		return nil, NewDomainValidationFailed("plannedEndDate is invalid", err)
	}
	return &CreationPlan{
		Resolved: in,
		WorkItem: WorkItemDraft{
			// TicketNumber intentionally left empty -- WorkItemCreator.CreateBase (Task 4) allocates it.
			TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
			RecordClass: RecordClassChangeRequest, Title: in.Command.Title, Description: in.Command.Description,
			SLADefinitionID: copyInt(in.SLADefinitionID),
		},
		ProfessionalInput: ChangeExtensionPlan{
			Justification: change.Justification, Type: defaultString(change.Type, "normal"),
			ImpactScope: defaultString(change.ImpactScope, "medium"), RiskLevel: defaultString(change.RiskLevel, "medium"),
			ImplementationPlan: change.ImplementationPlan, RollbackPlan: change.RollbackPlan,
			PlannedStartDate: plannedStart, PlannedEndDate: plannedEnd, AffectedCIs: change.AffectedCIs,
		},
	}, nil
}

func (c *ChangeCreator) CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error) {
	input, ok := plan.ProfessionalInput.(ChangeExtensionPlan)
	if !ok {
		return nil, NewDomainValidationFailed("change creation plan is invalid", nil)
	}
	saved, err := tx.Change.Create().
		SetJustification(input.Justification).
		SetType(input.Type).
		SetImpactScope(input.ImpactScope).
		SetRiskLevel(input.RiskLevel).
		SetWorkItemID(workItem.ID).
		SetImplementationPlan(input.ImplementationPlan).
		SetRollbackPlan(input.RollbackPlan).
		SetNillablePlannedStartDate(input.PlannedStartDate).
		SetNillablePlannedEndDate(input.PlannedEndDate).
		SetAffectedCis(input.AffectedCIs).
		Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create change extension", err)
	}
	return &ProfessionalReference{Type: "change", ID: saved.ID}, nil
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func parseOptionalTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
```

- [ ] **Step 5: Register in the Creator Registry and widen the Resolver's whitelist**

In `resolver.go`, change:

```go
if catalog.TargetClass != RecordClassServiceRequestItem && catalog.TargetClass != RecordClassIncident {
```

to:

```go
if catalog.TargetClass != RecordClassServiceRequestItem && catalog.TargetClass != RecordClassIncident && catalog.TargetClass != RecordClassChangeRequest {
```

Register `NewChangeCreator()` in the Creator Registry alongside `IncidentCreator`/`ServiceRequestItemCreator`, at the same construction site Task 4 updated for the shared `workitemnumber.Allocator` (`internal/bootstrap/app.go` or wherever the Task 3 port put Application Service assembly).

- [ ] **Step 6: Run and commit**

```bash
cd itsm-backend
go test ./handlers/intake -run TestChangeCreator -v
go build ./...
git add itsm-backend/handlers/intake itsm-backend/internal/bootstrap/app.go
git commit -m "feat(intake): add ChangeCreator, closing the professional-domain violation where Catalog change_request items created a ServiceRequest extension"
```

---

### Task 10: Frontend Idempotency Key Generation

**Files:**
- Modify: `itsm-frontend/src/lib/api/incident-api.ts`
- Modify: `itsm-frontend/src/lib/api/service-request-api.ts`
- Create: `itsm-frontend/src/lib/api/__tests__/idempotency-key.test.ts`
- Create: `itsm-frontend/src/lib/utils/idempotencyKey.ts`

**Interfaces:**
- Produces: `generateIdempotencyKey(): string`, used once per logical submission and reused across retries of that same submission.

- [ ] **Step 1: Write the failing test**

```typescript
import { generateIdempotencyKey } from '../../utils/idempotencyKey';

describe('generateIdempotencyKey', () => {
  it('produces a stable-format unique key', () => {
    const a = generateIdempotencyKey();
    const b = generateIdempotencyKey();
    expect(a).not.toEqual(b);
    expect(a).toMatch(/^[0-9a-f-]{36}$/);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-frontend
npx jest src/lib/api/__tests__/idempotency-key.test.ts
```

Expected: FAIL, module not found.

- [ ] **Step 3: Implement**

```typescript
export function generateIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  // Fallback for environments without crypto.randomUUID (older test runners).
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}
```

- [ ] **Step 4: Wire into `incident-api.ts` and `service-request-api.ts`**

Each create function must accept the key generated by the *caller* (the form component), not generate it internally per HTTP call — this is what makes retries idempotent. Find the current `createIncident(payload)` signature and change it to:

```typescript
async createIncident(payload: CreateIncidentPayload, idempotencyKey: string): Promise<IncidentResponse> {
  return httpClient.post('/incidents', payload, {
    headers: { 'Idempotency-Key': idempotencyKey },
  });
}
```

Apply the identical pattern to `service-request-api.ts`'s create function. Update every call site (incident creation form, service request creation form) to call `generateIdempotencyKey()` once when the form is opened/submission begins, store it in component state, and pass the same value on every retry — not regenerate it inside a retry handler.

- [ ] **Step 5: Run frontend tests and type-check**

```bash
cd itsm-frontend
npx jest src/lib/api/__tests__/idempotency-key.test.ts src/lib/api/__tests__/incident-api.test.ts src/lib/api/__tests__/service-request-api.test.ts
npm run type-check
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add itsm-frontend/src/lib/utils/idempotencyKey.ts itsm-frontend/src/lib/api/incident-api.ts itsm-frontend/src/lib/api/service-request-api.ts itsm-frontend/src/lib/api/__tests__/idempotency-key.test.ts
git commit -m "feat(frontend): generate stable per-submission Idempotency-Key for incident/service-request creation"
```

---

### Task 11: `/incidents` Wired to Intake, Idempotency-Key Mandatory

**Files:**
- Modify: `itsm-backend/controller/incident_controller.go`
- Create: `itsm-backend/controller/incident_intake_adapter_test.go`
- Modify: `itsm-backend/internal/bootstrap/app.go` (constructor wiring only)

**Interfaces:**
- Consumes: `intake.ApplicationService.Create(ctx, intake.Identity, intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error)`.
- Produces: `IncidentController.intakeService`, `IncidentController.incidentCreateReader` (reads back the created incident to preserve the existing response shape).

- [ ] **Step 1: Write the failing tests**

```go
type recordingIncidentIntake struct {
	calls    int
	identity intake.Identity
	command  intake.CreateWorkItemCommand
	result   *intake.CreateWorkItemResult
	err      error
}

func (f *recordingIncidentIntake) Create(_ context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	f.calls++
	f.identity = identity
	f.command = command
	return f.result, f.err
}

type stubIncidentCreateReader struct {
	response *dto.IncidentResponse
	err      error
}

func (f stubIncidentCreateReader) GetIncident(context.Context, int, int) (*dto.IncidentResponse, error) {
	return f.response, f.err
}

func incidentCreateRouter(controller *IncidentController) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 19)
		c.Set("user_id", 73)
		c.Set("role", "manager")
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 19})
		c.Next()
	})
	router.POST("/api/v1/incidents", controller.CreateIncident)
	return router
}

func TestIncidentCreateRequiresIdempotencyKey(t *testing.T) {
	creator := &recordingIncidentIntake{}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	router := incidentCreateRouter(controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"VPN unavailable"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, creator.calls)
}

func TestIncidentCreateMapsFullFieldSet(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 303, RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 404},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 404}}
	router := incidentCreateRouter(controller)

	body := `{"title":"t","priority":"critical","assigneeId":9,"source":"monitoring","impactAnalysis":{"scope":"regional"},"metadata":{"k":"v"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotNil(t, creator.command.Incident)
	assert.Equal(t, "critical", creator.command.Incident.ExplicitPriority)
	assert.Equal(t, 9, *creator.command.Incident.AssigneeID)
	require.NotNil(t, creator.command.SourceReference)
	assert.Equal(t, "monitoring", creator.command.SourceReference.Provider)
	assert.Equal(t, "regional", creator.command.Incident.ImpactAnalysis["scope"])
	assert.Nil(t, creator.command.CTI) // free-text category/subcategory never populates CTI on this HTTP path
}

func TestIncidentCreateMapsCategoryTypeAndDetectedAt(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 303, RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 404},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 404}}
	router := incidentCreateRouter(controller)

	body := `{"title":"t","type":"security_event","category":"performance","subcategory":"cpu","detectedAt":"2026-09-01T10:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-2")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotNil(t, creator.command.Incident)
	assert.Equal(t, "security_event", creator.command.Incident.Type)
	assert.Equal(t, "performance", creator.command.Incident.Category)
	assert.Equal(t, "cpu", creator.command.Incident.Subcategory)
	assert.Equal(t, "2026-09-01T10:00:00Z", creator.command.Incident.DetectedAt)
}

func TestIncidentCreateMapsIntakeErrorToResponse(t *testing.T) {
	creator := &recordingIncidentIntake{err: intake.NewPermissionDenied("actor cannot create incidents", nil)}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{}
	router := incidentCreateRouter(controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"t"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-3")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "actor cannot create incidents")
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./controller -run TestIncidentCreate -v
```

Expected: FAIL — current `CreateIncident` calls `c.incidentService.CreateIncident` directly, has no header check, and has no field mapping to `intake.CreateWorkItemCommand`.

- [ ] **Step 3: Replace `CreateIncident`'s body**

```go
func (c *IncidentController) CreateIncident(ctx *gin.Context) {
	idempotencyKey := strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		common.Fail(ctx, common.ParamErrorCode, "Idempotency-Key header is required")
		return
	}
	var req dto.CreateIncidentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数无效")
		return
	}
	tenantID, ok := c.resolveTenantID(ctx)
	if !ok {
		return
	}
	userID, err := middleware.GetUserID(ctx)
	if err != nil {
		common.Fail(ctx, common.AuthFailedCode, "获取用户ID失败")
		return
	}
	if c.intakeService == nil || c.incidentCreateReader == nil {
		common.Fail(ctx, common.InternalErrorCode, "incident intake service not configured")
		return
	}
	identity := intake.Identity{TenantID: tenantID, ActorID: userID, RequesterID: userID, Channel: "itsm_web"}
	var detectedAt string
	if req.DetectedAt != nil {
		detectedAt = req.DetectedAt.UTC().Format(time.RFC3339)
	}
	command := intake.CreateWorkItemCommand{
		IdempotencyKey: idempotencyKey,
		IntakeKind:     intake.IntakeKindIncident,
		Title:          req.Title,
		Description:    req.Description,
		CIIDs:          req.ConfigurationItemIDs,
		Incident: &intake.IncidentInput{
			Type: req.Type, Severity: req.Severity, ExplicitPriority: req.Priority, Impact: req.Impact, Urgency: req.Urgency,
			Category: req.Category, Subcategory: req.Subcategory, DetectedAt: detectedAt,
			AssigneeID: req.AssigneeID, ImpactAnalysis: mapImpactAnalysis(req.ImpactAnalysis), Metadata: req.Metadata,
		},
	}
	if strings.TrimSpace(req.Source) != "" {
		command.SourceReference = &intake.SourceReference{Provider: req.Source, EventID: fmt.Sprintf("web-%d", time.Now().UnixNano())}
	}

	result, err := c.intakeService.Create(ctx.Request.Context(), identity, command)
	if err != nil {
		respondIntakeError(ctx, err)
		return
	}
	response, err := c.incidentCreateReader.GetIncident(ctx.Request.Context(), result.ProfessionalReference.ID, tenantID)
	if err != nil {
		common.Fail(ctx, common.InternalErrorCode, "创建成功但读取详情失败")
		return
	}
	common.Success(ctx, response)
}
```

`dto.CreateIncidentRequest` (`dto/incident_dto.go:52-68`) has no numeric category ID field at all — `Category`/`Subcategory` are both free-text `string` (not pointers), exactly matching the `IncidentInput.Category`/`Subcategory` fields Task 5 added. There is therefore no `command.CTI` to populate here: this HTTP path always goes through `IncidentCreator`'s free-text branch (`ResolveIncidentCategory`), and `command.CTI` stays nil — only the Catalog/BPMN-derived creation paths (Task 12/13, which already hold a structured CTI) populate `command.CTI.CategoryID`. `req.DetectedAt` is `*time.Time` (not a string) — it is formatted to RFC3339 above because `IncidentInput.DetectedAt` (Task 5) is a `string` field that `IncidentCreator.Prepare` parses back with `time.Parse(time.RFC3339, ...)`. `req.Type` maps straight through to `IncidentInput.Type`, matching `IncidentService.CreateIncident`'s existing `incidentType := req.Type` handling (`service/incident_service.go:155`) — dropping it here would silently discard every non-default incident type (`security_event`, `alert`) submitted through this endpoint.

- [ ] **Step 4: Add `respondIntakeError` and `mapImpactAnalysis` helpers**

`handlers/intake/errors.go` (ported in Task 3) defines `IntakeError` — not `Error` — with an exported `Code ErrorCode` **field** (not a method). It also carries an `HTTPStatus int` field, but that is `errors.go`'s own internal policy (`errorPolicy(code)`, e.g. `DomainValidationFailed` → 422) for callers outside this codebase's response convention — it must **not** be written to the response here. CLAUDE.md's `common.Fail(c, code, msg)` is the one authoritative HTTP-status computation for every ITSM controller response (`common/response.go:49-73`'s own `code → http.Status` switch), and that switch has no 422 case at all; using `appErr.HTTPStatus` directly would run a second, competing status computation for the same response, which is exactly the duplicate-algorithm pattern this plan exists to remove. So `respondIntakeError` translates `ErrorCode` to one of the existing `common.*Code` business codes and lets `common.Fail` derive the status, the same as every other controller in this codebase:

```go
func respondIntakeError(ctx *gin.Context, err error) {
	var appErr *intake.IntakeError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case intake.AuthenticationRequired:
			common.Fail(ctx, common.AuthFailedCode, appErr.Message)
		case intake.PermissionDenied:
			common.Fail(ctx, common.ForbiddenCode, appErr.Message)
		case intake.ReferenceNotFound:
			common.Fail(ctx, common.NotFoundCode, appErr.Message)
		case intake.IdempotencyConflict:
			common.Fail(ctx, common.ConflictCode, appErr.Message)
		case intake.InvalidCommand, intake.DomainValidationFailed, intake.UnsupportedRecordClass, intake.WorkflowBindingRequired:
			common.Fail(ctx, common.ParamErrorCode, appErr.Message)
		case intake.InfrastructureUnavailable:
			common.Fail(ctx, common.ServiceUnavailableCode, appErr.Message)
		default: // InternalFailure and any future code this controller doesn't know yet
			common.Fail(ctx, common.InternalErrorCode, "创建事件失败")
		}
		return
	}
	common.Fail(ctx, common.InternalErrorCode, "创建事件失败")
}

func mapImpactAnalysis(v *dto.ImpactAnalysis) map[string]interface{} {
	if v == nil {
		return nil
	}
	out := map[string]interface{}{}
	b, err := json.Marshal(v)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}
```

- [ ] **Step 5: Add `intakeService`/`incidentCreateReader` fields and setters**

```go
type incidentIntakeService interface {
	Create(context.Context, intake.Identity, intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error)
}

type incidentCreateReader interface {
	GetIncident(ctx context.Context, id, tenantID int) (*dto.IncidentResponse, error)
}

// added to IncidentController struct:
//   intakeService        incidentIntakeService
//   incidentCreateReader incidentCreateReader

func (c *IncidentController) SetIntakeService(s incidentIntakeService) { c.intakeService = s }
func (c *IncidentController) SetIncidentCreateReader(r incidentCreateReader) { c.incidentCreateReader = r }
```

Wire both in `internal/bootstrap/app.go` at the point `IncidentController` is constructed — pass the shared Intake `ApplicationService` instance and a thin reader backed by `IncidentService.GetIncident`.

- [ ] **Step 6: Run the full controller test file**

```bash
cd itsm-backend
go test ./controller -run TestIncidentCreate -v
go build ./...
```

Expected: PASS and clean build once Step 3's category-field reconciliation and Step 4's real error-type names are confirmed against the ported `resolver.go`/`errors.go`.

- [ ] **Step 7: Commit**

```bash
git add itsm-backend/controller/incident_controller.go itsm-backend/controller/incident_intake_adapter_test.go itsm-backend/internal/bootstrap/app.go
git commit -m "feat(incident): route /incidents through CreateWorkItemCommand, require Idempotency-Key, carry the full existing DTO field set"
```

---

### Task 12: BPMN `createIncident` Callback Wired to Intake (Actor = Requester = `reporter_id`)

**Files:**
- Modify: `itsm-backend/service/bpmn/incident_handler.go:33-128` (struct, constructor, `createIncident`)
- Modify: `itsm-backend/service/bpmn/incident_handler_test.go:500-526` (`TestIncidentServiceTaskHandler_CreateIncident_DelegatesToInjectedService` no longer matches the rewritten body)
- Modify: `itsm-backend/internal/bootstrap/app.go` (wire the shared Intake `ApplicationService` into the handler; this is a *different* `app.go` call site than `srIncidentBridge`, which Task 13 removes)

The real `createIncident` (`incident_handler.go:88-128`) calls `h.incidentService.CreateIncident(ctx, &dto.CreateIncidentRequest{...}, tenantID, reporterID)`, where `incidentService IncidentDomainServiceInterface` is a handler-local interface (`incident_handler.go:21-30`) also used by `assignIncident`/`escalateIncident`/etc. — those other actions keep using it unchanged; only `createIncident` stops calling `CreateIncident` on it. The existing `dto.CreateIncidentRequest` literal already carries `Type: incidentType` (from `variables["type"]`) — the earlier draft of this task dropped that field; it is restored below.

**Interfaces:**
- Consumes: `intake.ApplicationService.Create`.
- Produces: a new `intakeCreator` interface local to `service/bpmn` (Go does not allow reusing an unexported interface across packages, so this cannot be "the same type" as `controller`'s `incidentIntakeService` from Task 11 — it is a separate, identically-shaped interface, satisfied by the same concrete `*intake.ApplicationService`), plus `IncidentServiceTaskHandler.SetIntakeService(...)`, mirroring the existing `SetIncidentService` pattern in this same file.

- [ ] **Step 1: Write the failing test**

Follows the file's existing test conventions exactly (`context.WithValue(ctx, BPMNTenantIDContextKey, tenantID)` for tenant, plain `int` for `reporter_id` — see the existing `TestIncidentServiceTaskHandler_CreateIncident_DelegatesToInjectedService` this replaces):

```go
type recordingIntake struct {
	calls    int
	identity intake.Identity
	command  intake.CreateWorkItemCommand
	result   *intake.CreateWorkItemResult
	err      error
}

func (f *recordingIntake) Create(_ context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	f.calls++
	f.identity = identity
	f.command = command
	return f.result, f.err
}

func TestIncidentServiceTaskHandler_CreateIncident_UsesReporterAsActorAndRequester(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	recorder := &recordingIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 9, Number: "TKT-202609-000009",
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 7},
	}}
	handler.SetIntakeService(recorder)

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 1)
	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "create_incident",
		"title":       "测试事件",
		"type":        "security_event",
		"reporter_id": 3,
	})
	require.NoError(t, err)
	require.Equal(t, 1, recorder.calls)
	assert.Equal(t, 3, recorder.identity.ActorID)
	assert.Equal(t, 3, recorder.identity.RequesterID)
	require.NotNil(t, recorder.command.Incident)
	assert.Equal(t, "security_event", recorder.command.Incident.Type)
	assert.Equal(t, 7, result.OutputVars["incident_id"])
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./service/bpmn -run TestIncidentServiceTaskHandler_CreateIncident_UsesReporterAsActorAndRequester -v
```

Expected: FAIL — `IncidentServiceTaskHandler` has no `SetIntakeService` method or `intakeService` field yet.

- [ ] **Step 3: Rewrite `createIncident`**

```go
func (h *IncidentServiceTaskHandler) createIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	if _, durable := BPMNCallbackExecutionKey(ctx); durable {
		return nil, fmt.Errorf("持久化回调必须使用流程实例中的既有事件目标")
	}
	title, _ := variables["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("事件标题不能为空")
	}
	description, _ := variables["description"].(string)
	incidentType, _ := variables["type"].(string)
	priority, _ := variables["priority"].(string)
	severity, _ := variables["severity"].(string)
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	reporterID := GetIntFromVars(variables, "reporter_id")
	if reporterID <= 0 {
		return nil, fmt.Errorf("reporter_id 缺失，无法确定创建人")
	}
	if h.intakeService == nil {
		return nil, fmt.Errorf("intake service 未注入，无法创建事件")
	}
	identity := intake.Identity{TenantID: tenantID, ActorID: reporterID, RequesterID: reporterID, Channel: "bpmn"}
	command := intake.CreateWorkItemCommand{
		IdempotencyKey: bpmnCreateIncidentIdempotencyKey(ctx, variables),
		IntakeKind:     intake.IntakeKindIncident,
		Title:          title,
		Description:    description,
		Incident:       &intake.IncidentInput{Type: incidentType, Severity: severity, ExplicitPriority: priority},
	}
	result, err := h.intakeService.Create(ctx, identity, command)
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}
	h.logger.Infow("Incident created via BPMN", "work_item_id", result.WorkItemID)
	return &CallbackEffect{Status: CallbackEffectApplied,
		Message:    fmt.Sprintf("事件 %d 已创建", result.ProfessionalReference.ID),
		OutputVars: map[string]interface{}{"incident_id": result.ProfessionalReference.ID, "incident_number": result.Number},
	}, nil
}

// bpmnCreateIncidentIdempotencyKey derives a stable key from the BPMN
// execution context so a retried callback (crash, redelivery) never creates
// a second Incident for the same trigger.
func bpmnCreateIncidentIdempotencyKey(ctx context.Context, variables map[string]interface{}) string {
	if key, ok := BPMNCallbackExecutionKey(ctx); ok && key != "" {
		return "bpmn-create-incident:" + key
	}
	taskID, _ := variables["task_id"].(string)
	return "bpmn-create-incident:" + taskID
}
```

Add to `incident_handler.go`, following the exact shape of the existing `IncidentDomainServiceInterface`/`incidentService`/`SetIncidentService` triplet at lines 21-51:

```go
type intakeCreator interface {
	Create(ctx context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error)
}

// added to IncidentServiceTaskHandler struct: intakeService intakeCreator

// SetIntakeService 注入 Unified Intake 应用服务，由 bootstrap 在其构造完成后调用。
func (h *IncidentServiceTaskHandler) SetIntakeService(svc intakeCreator) { h.intakeService = svc }
```

Add the `"itsm-backend/handlers/intake"` import. In `internal/bootstrap/app.go`, call `incidentTaskHandler.SetIntakeService(intakeApplicationService)` alongside the existing `SetIncidentService` wiring for this handler — reuse the same `*intake.ApplicationService` instance Task 11 wires into `IncidentController`, do not construct a second one.

- [ ] **Step 4: Update the now-stale test and run**

`TestIncidentServiceTaskHandler_CreateIncident_DelegatesToInjectedService` (`incident_handler_test.go:511-526`) asserts against `fake.lastCreateReq`/`fake.lastCreateUserID` on the old `IncidentDomainServiceInterface` path — delete it, replaced by Step 1's test above. `TestIncidentServiceTaskHandler_CreateIncident_RequiresInjectedService` (`incident_handler_test.go:500-509`) needs no change: it never calls `SetIncidentService`/`SetIntakeService`, and the rewritten `createIncident`'s nil-check on `h.intakeService` still returns an error containing `"未注入"`.

```bash
cd itsm-backend
go test ./service/bpmn -run TestIncidentServiceTaskHandler_CreateIncident -v
git add itsm-backend/service/bpmn/incident_handler.go itsm-backend/service/bpmn/incident_handler_test.go itsm-backend/internal/bootstrap/app.go
git commit -m "fix(bpmn): route createIncident callback through CreateWorkItemCommand using reporter_id as both actor and requester"
```

---

### Task 13: Catalog-Derived Incident and Change Creation Wired to Intake (`service_request_item` Path Unchanged)

The real production entry point is a single function, `Service.Create(ctx, tenantID, requesterID, catalogID int, reqData *ServiceRequest) (*ServiceRequest, error)` (`handlers/service_request/service.go:72`), called by exactly one HTTP handler, `Handler.Create` (`handler.go:120`, `POST /service-requests`). It already branches internally on `cat.TargetClass`: `isIncidentCatalog(cat.TargetClass)` diverts to `s.createIncidentFromCatalog(...)` (line 95, which calls `s.incidentSvc.CreateIncident`, an `IncidentCreator` interface implemented in production by `srIncidentBridge` in `internal/bootstrap/app.go:1336-1353`); every other `target_class`, including `change_request`, falls through to the generic body that unconditionally builds a `ServiceRequest` extension via `s.createWorkItemAndExtension` (confirmed: `mapTargetClassToTicketType` labels a change-target-class Ticket's `Type` as `"change"`, but the extension row created is still `ServiceRequest`, never `ent.Change`). An earlier draft of this plan invented a `Service.CreateFromCatalog`/`CreateFromCatalogRequest` API and split this into two separate tasks against two separate (nonexistent) branches — neither exists; this task replaces both with one change against the one real function, per the decision to route only Incident and Change through Intake in Phase 1 and leave the `service_request_item` branch (with its still-unported approval-chain/dynamic-required-field/cloud-resource-CI logic) on its existing implementation, unconverted, for a later phase.

**Files:**
- Modify: `itsm-backend/handlers/service_request/service.go` (dispatch, new `createFromCatalogViaIntake`, delete `IncidentCreator` interface/`incidentSvc` field/`createIncidentFromCatalog`)
- Modify: `itsm-backend/handlers/service_request/handler.go` (thread the `Idempotency-Key` header down; `service_request_item` submissions still work without it)
- Modify: `itsm-backend/handlers/service_request/regression_test.go` (replace `fakeIncidentCreator`-based `TestService_Create_IncidentCatalog_NoServiceRequestRowCreated`; add the Change equivalent)
- Modify: `itsm-backend/internal/bootstrap/app.go` (delete `srIncidentBridge`; wire the shared Intake `ApplicationService` into `service_request.NewService`)
- Modify: `itsm-backend/service/incident_service_test.go:300-310` (comment only — stop referencing the now-deleted `srIncidentBridge`/`IncidentCreator` bridge)

**Interfaces:**
- Consumes: `intake.ApplicationService.Create`, `intake.RecordClassChangeRequest`/`ChangeInput` (Task 9).
- Produces: `Service.Create`'s new `idempotencyKey string` trailing parameter (all three call sites — `internal/bootstrap/app.go`, `tests/integration/service_catalog_fields_test.go:49`, `tests/e2e/sslvpn_scenario_test.go:139` — pass positional args; the two test call sites already pass `nil` for the old `incidentSvc` slot and need a trailing `""` added, nothing else, since SSLVPN's scenario never hits the Incident/Change branches and doesn't need a real key).

- [ ] **Step 1: Write the failing tests**

Follows `regression_test.go`'s existing enttest-per-test pattern (see `TestService_Create_IncidentCatalog_NoServiceRequestRowCreated`, which this replaces) rather than a shared fixture helper, since no such helper exists in this package today:

```go
type recordingIntake struct {
	calls    int
	identity intake.Identity
	command  intake.CreateWorkItemCommand
	result   *intake.CreateWorkItemResult
	err      error
}

func (f *recordingIntake) Create(_ context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	f.calls++
	f.identity = identity
	f.command = command
	return f.result, f.err
}

func TestService_Create_IncidentCatalog_RoutesThroughIntakeNoServiceRequestRow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_incident_intake_diversion?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-incident-intake").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("incident-requester").SetEmail("incident-requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "系统故障上报", "运维", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).
		SetItsmType("Incident").SetTargetClass(service_catalog.TargetClassIncident).Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	recorder := &recordingIntake{result: &intake.CreateWorkItemResult{
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 4242},
	}}
	svc := NewService(srRepo, scRepo, cmdbRepo, client, workitemnumber.NewPostgreSQLAllocator(), logger, ticketSvc, nil, recorder)

	result, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		FormData: map[string]interface{}{"title": "生产环境服务器宕机", "reason": "紧急"},
	}, "idem-incident-1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, recorder.calls)
	assert.Equal(t, intake.IntakeKindCatalogItem, recorder.command.IntakeKind)
	require.NotNil(t, recorder.command.CatalogItemID)
	assert.Equal(t, catalog.ID, *recorder.command.CatalogItemID)
	assert.Equal(t, "idem-incident-1", recorder.command.IdempotencyKey)
	assert.Equal(t, requester.ID, recorder.identity.ActorID)
	assert.Equal(t, requester.ID, recorder.identity.RequesterID)
	assert.Equal(t, "生产环境服务器宕机", recorder.command.Title)
	assert.Equal(t, 4242, result.ID, "stub ServiceRequest borrows ID field to carry the professional reference ID, same contract createIncidentFromCatalog used")

	_, total, err := srRepo.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "Incident diversion must not create a ServiceRequest row")
}

func TestService_Create_ChangeCatalog_RoutesThroughIntakeCreatesRealChange(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_change_intake_diversion?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-change-intake").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("change-requester").SetEmail("change-requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "变更申请", "运维", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).
		SetTargetClass(service_catalog.TargetClassChangeRequest).Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	recorder := &recordingIntake{result: &intake.CreateWorkItemResult{
		ProfessionalReference: intake.ProfessionalReference{Type: "change", ID: 55},
	}}
	svc := NewService(srRepo, scRepo, cmdbRepo, client, workitemnumber.NewPostgreSQLAllocator(), logger, ticketSvc, nil, recorder)

	result, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		FormData: map[string]interface{}{"title": "升级路由器固件", "reason": "计划维护"},
	}, "idem-change-1")
	require.NoError(t, err)
	assert.Equal(t, intake.IntakeKindCatalogItem, recorder.command.IntakeKind)
	assert.Equal(t, 55, result.ID)

	_, total, err := srRepo.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "Change diversion must not create a ServiceRequest row")
}

func TestService_Create_RequiresIdempotencyKeyForIncidentAndChange(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_intake_diversion_missing_key?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-missing-key").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "系统故障上报", "运维", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).SetTargetClass(service_catalog.TargetClassIncident).Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	recorder := &recordingIntake{}
	svc := NewService(srRepo, scRepo, cmdbRepo, client, workitemnumber.NewPostgreSQLAllocator(), logger, ticketSvc, nil, recorder)

	_, err = svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		FormData: map[string]interface{}{"title": "t"},
	}, "")
	require.Error(t, err)
	assert.Zero(t, recorder.calls, "Intake must never be called without an idempotency key")
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd itsm-backend
go test ./handlers/service_request -run 'TestService_Create_IncidentCatalog_RoutesThroughIntake|TestService_Create_ChangeCatalog_RoutesThroughIntake|TestService_Create_RequiresIdempotencyKeyForIncidentAndChange' -v
```

Expected: FAIL to compile — `Service.Create` has no `idempotencyKey` parameter yet, `NewService`'s last parameter is still `IncidentCreator`-typed (a `*recordingIntake` does not satisfy it), and `createIncidentFromCatalog` still builds a plain `dto.CreateIncidentRequest`, not a `CreateWorkItemCommand`.

- [ ] **Step 3: Replace the `IncidentCreator` dependency and add the Intake dispatch branch**

Delete `IncidentCreator` (lines 31-33) and the `incidentSvc IncidentCreator` field (line 44); replace with:

```go
type catalogIntakeCreator interface {
	Create(ctx context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error)
}

type Service struct {
	repo           Repository
	scRepo         service_catalog.Repository
	cmdbRepo       cmdb.Repository
	client         *ent.Client
	numberAllocator workitemnumber.Allocator
	logger         *zap.SugaredLogger
	ticketSvc      TicketServiceInterface
	chainResolver  *service.ApprovalChainResolver
	intakeService  catalogIntakeCreator
}
```

(Keep every other existing field exactly as-is; only the last field/parameter changes name and type, from `incidentSvc IncidentCreator` to `intakeService catalogIntakeCreator`.) Update `NewService`'s signature and body the same way — same position, new name/type:

```go
func NewService(repo Repository, scRepo service_catalog.Repository, cmdbRepo cmdb.Repository, entClient *ent.Client, allocator workitemnumber.Allocator, logger *zap.SugaredLogger, ticketSvc TicketServiceInterface, chainResolver *service.ApprovalChainResolver, intakeService catalogIntakeCreator) *Service {
	return &Service{
		repo: repo, scRepo: scRepo, cmdbRepo: cmdbRepo, client: entClient, numberAllocator: allocator,
		logger: logger, ticketSvc: ticketSvc, chainResolver: chainResolver, intakeService: intakeService,
	}
}
```

Replace the dispatch at `service.go:95` (`if isIncidentCatalog(cat.TargetClass) { return s.createIncidentFromCatalog(...) }`) with:

```go
if isIncidentCatalog(cat.TargetClass) || cat.TargetClass == service_catalog.TargetClassChangeRequest {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, common.NewBadRequestError("Idempotency-Key header is required", nil)
	}
	return s.createFromCatalogViaIntake(ctx, tenantID, requesterID, cat, reqData, idempotencyKey)
}
```

Add `idempotencyKey string` as `Create`'s new trailing parameter (`func (s *Service) Create(ctx context.Context, tenantID, requesterID int, catalogID int, reqData *ServiceRequest, idempotencyKey string) (*ServiceRequest, error)`); every other line of `Create`'s existing body (title validation, infra fields, dynamic-field validation, approval-chain resolution, `createWorkItemAndExtension`) is untouched — `idempotencyKey` is unused past the branch above for `service_request_item`, which is exactly the point: that path's contract does not change.

- [ ] **Step 4: Delete `createIncidentFromCatalog`, add `createFromCatalogViaIntake`**

```go
// createFromCatalogViaIntake routes Catalog items whose target_class is
// incident or change_request through the Unified Intake ApplicationService,
// which resolves the concrete ProfessionalCreator (IncidentCreator /
// ChangeCreator) from cat.TargetClass itself (see resolver.go's
// catalog.TargetClass -> resolved.RecordClass assignment) -- this function
// does not need to branch on TargetClass again. service_request_item stays
// on the existing rich Create body below, untouched by this function.
func (s *Service) createFromCatalogViaIntake(ctx context.Context, tenantID, requesterID int, cat *service_catalog.ServiceCatalog, reqData *ServiceRequest, idempotencyKey string) (*ServiceRequest, error) {
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	description := reqData.reason()
	if description == "" {
		description = title
	}
	if s.intakeService == nil {
		return nil, common.NewInternalError("intake service not configured", nil)
	}
	identity := intake.Identity{TenantID: tenantID, ActorID: requesterID, RequesterID: requesterID, Channel: "service_catalog"}
	command := intake.CreateWorkItemCommand{
		IdempotencyKey: idempotencyKey,
		IntakeKind:     intake.IntakeKindCatalogItem,
		CatalogItemID:  &cat.ID,
		Title:          title,
		Description:    description,
	}
	result, err := s.intakeService.Create(ctx, identity, command)
	if err != nil {
		return nil, mapIntakeErrorToAppError(err)
	}
	// Stub, non-persisted ServiceRequest carrying the professional reference ID
	// in the borrowed ID field -- the same response contract
	// createIncidentFromCatalog used, now extended to cover Change too.
	return &ServiceRequest{
		ID: result.ProfessionalReference.ID, TenantID: tenantID, CatalogID: cat.ID, RequesterID: requesterID,
	}, nil
}

// mapIntakeErrorToAppError translates an *intake.IntakeError into this
// package's existing common.AppError convention so failServiceRequest (in
// handler.go) needs no separate branch for Intake-originated errors -- one
// error-response path, not two.
func mapIntakeErrorToAppError(err error) error {
	var appErr *intake.IntakeError
	if !errors.As(err, &appErr) {
		return common.NewInternalError("创建失败", err)
	}
	switch appErr.Code {
	case intake.AuthenticationRequired:
		return common.NewUnauthorizedError(appErr.Message)
	case intake.PermissionDenied:
		return common.NewForbiddenError(appErr.Message)
	case intake.ReferenceNotFound:
		return common.NewNotFoundError(appErr.Message)
	case intake.IdempotencyConflict:
		return common.NewConflictError("request", appErr.Message)
	case intake.InvalidCommand, intake.DomainValidationFailed, intake.UnsupportedRecordClass, intake.WorkflowBindingRequired:
		return common.NewBadRequestError(appErr.Message, nil)
	default:
		return common.NewInternalError("创建失败", appErr)
	}
}
```

Delete `createIncidentFromCatalog` (`service.go:535-557`) entirely — it is fully replaced.

`ChangeInput` is intentionally left nil in `command` above: `reqData.FormData` (the generic Catalog dynamic-field bag) has no confirmed mapping to `ChangeInput`'s `Justification`/`ImpactScope`/`RiskLevel`/etc. today, the same way the Incident branch never populated `Severity`/`Impact`/`Urgency` from the Catalog form either — `ChangeCreator.Prepare`'s existing `defaultString(..., "normal"/"medium")` fallback applies, exactly as it does for a nil `Command.Change`. This is a recorded, deliberate simplification, not a gap left open to future judgment.

- [ ] **Step 5: Thread the header through `Handler.Create`**

```go
idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
...
created, err := h.service.Create(c.Request.Context(), tenantID, userID, req.CatalogID, domainReq, idempotencyKey)
```

Add the `idempotencyKey` line right after `normalizeCreateServiceRequest(&req)` in `handler.go:120-178`; the rest of `Handler.Create` (binding, `failServiceRequest`, the post-create `Get`) is unchanged. This makes the header available for every `/service-requests` submission without requiring it for `service_request_item` ones (Step 3's check only fires inside the Incident/Change branch).

- [ ] **Step 6: Fix `internal/bootstrap/app.go` wiring**

Delete `srIncidentBridge` (`app.go:1336-1353`) and the `incidentBridge := &srIncidentBridge{svc: incidentService}` line; pass the shared Intake `ApplicationService` instance (the same one wired into `IncidentController` in Task 11 and `IncidentServiceTaskHandler` in Task 12) as `service_request.NewService`'s last argument instead:

```go
srService := service_request.NewService(srRepo, scRepo, cmdbRepo, client, numberAllocator, sugar, ticketService, chainResolver, intakeApplicationService)
```

Update `tests/integration/service_catalog_fields_test.go:49` and `tests/e2e/sslvpn_scenario_test.go:139`: both already pass `nil` for this parameter and keep compiling unchanged (an interface parameter accepts `nil` regardless of type); add the new trailing `""` argument to every `svc.Create(...)` call in both files (SSLVPN's scenario never hits the Incident/Change branch, so an empty key is harmless there).

- [ ] **Step 7: Clean up the stale comment in `incident_service_test.go`**

`incident_service_test.go:300-310`'s comment on `TestIncidentService_CreateIncident_ServiceCatalogDivertedPath_AlsoCreatesWorkItem` describes "该路径通过 IncidentCreator 接口（生产环境由 internal/bootstrap/app.go 的 srIncidentBridge 适配）" — `srIncidentBridge` no longer exists after Step 6. Reword the comment to say the Catalog-diverted Incident path now goes through `intake.IncidentCreator` (Tasks 5-7) instead of `IncidentService.CreateIncident` directly, and that this test still independently exercises `CreateIncident`'s own WorkItem-creation behavior — it does not change what the test calls or asserts, only the comment's description of the production call path.

- [ ] **Step 8: Run and commit**

```bash
cd itsm-backend
go test ./handlers/service_request -run 'TestService_Create' -v
go test ./service -run TestIncidentService_CreateIncident_ServiceCatalogDivertedPath -v
go build ./...
git add itsm-backend/handlers/service_request/service.go itsm-backend/handlers/service_request/handler.go itsm-backend/handlers/service_request/regression_test.go itsm-backend/internal/bootstrap/app.go itsm-backend/tests/integration/service_catalog_fields_test.go itsm-backend/tests/e2e/sslvpn_scenario_test.go itsm-backend/service/incident_service_test.go
git commit -m "fix(service-request): route Catalog-derived Incident and Change creation through CreateWorkItemCommand, creating a real ent.Change extension; service_request_item path unchanged"
```

---

### Task 14: Retire `service_catalogs.itsm_type` Dependency

**Files:**
- Modify: `itsm-backend/handlers/service_catalog/repository_impl.go:20-40,105-125`
- Modify: `itsm-backend/handlers/service_catalog/repository_impl_test.go`
- Modify: `itsm-backend/cmd/backfill_servicecatalog_target_class/main.go` (confirm retirement eligibility)

**Interfaces:**
- Produces: `repository_impl.go`'s Create/Update writing `target_class` directly from the caller-supplied value, no longer computing it from `catalog.ITSMType`.

This task must land **before** Task 2's migration `024` is applied against any real database (that migration drops the `itsm_type` column) — sequence commits accordingly even though this task is numbered after Task 2 in this plan for narrative flow; Task 2's migration file itself carries a comment noting this dependency.

- [ ] **Step 1: Write the failing test**

Follows this file's existing per-test `enttest.Open` pattern (see `TestEntRepository_Create_SyncsTargetClassFromITSMType`, which this task's Step 3 change supersedes — that test's whole premise, deriving `target_class` from `ITSMType`, goes away, so it must be replaced, not left alongside the new behavior) — the domain type constructed is `ServiceCatalog`, not a `Catalog`:

```go
func TestEntRepository_Create_WritesTargetClassDirectlyNoITSMTypeDerivation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_direct?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "变更申请", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1,
		TargetClass: TargetClassChangeRequest,
	})
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, created.TargetClass, "target_class must come from the caller-supplied value, not a derivation from ITSMType")

	fetched, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, fetched.TargetClass)
}

func TestEntRepository_Create_RejectsMissingTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_required?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	_, err := repo.Create(ctx, &ServiceCatalog{Name: "变更申请", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd itsm-backend
go test ./handlers/service_catalog -run 'TestEntRepository_Create_WritesTargetClassDirectlyNoITSMTypeDerivation|TestEntRepository_Create_RejectsMissingTargetClass' -v
```

Expected: `TestEntRepository_Create_WritesTargetClassDirectlyNoITSMTypeDerivation` FAILs (current code silently derives `target_class` via `ComputeTargetClass(catalog.ITSMType)`, ignoring the caller-supplied value); `TestEntRepository_Create_RejectsMissingTargetClass` FAILs (current code never validates `target_class` is required, since it always computes a fallback).

- [ ] **Step 3: Fix `Create` and `Update`**

Replace both `SetTargetClass(ComputeTargetClass(catalog.ITSMType))` call sites with direct validation and use of `catalog.TargetClass`:

```go
if catalog.TargetClass != "service_request_item" && catalog.TargetClass != "incident" && catalog.TargetClass != "change_request" {
	return nil, fmt.Errorf("target class is required and must be one of service_request_item, incident, change_request")
}
// ...
SetTargetClass(catalog.TargetClass)
```

Delete `ComputeTargetClass` once both call sites no longer reference it — confirm with `rg ComputeTargetClass` that no other caller remains before deleting. Delete `TestEntRepository_Create_SyncsTargetClassFromITSMType` and `TestEntRepository_Update_SelfHealsTargetClassFromCurrentITSMType` (`repository_impl_test.go`) — both tests' entire premise (deriving/self-healing `target_class` from `ITSMType`) is exactly the behavior this step removes; they cannot pass once `ComputeTargetClass` is gone, and there is no dual-mode to keep them alive under.

- [ ] **Step 4: Confirm `cmd/backfill_servicecatalog_target_class` retirement**

```bash
cd itsm-backend
rg -n "backfill_servicecatalog_target_class" --glob '*.go' .
```

If the only references are the command's own `main.go`/tests, delete the command directory in this task (its job — backfilling `target_class` from `itsm_type` for pre-existing rows — is superseded by migration `024`'s own `UPDATE service_catalogs SET target_class = ...` statement, which covers the same backfill atomically). If other code still invokes it, leave it and record why in the commit message.

- [ ] **Step 5: Run and commit**

```bash
cd itsm-backend
go test ./handlers/service_catalog -count=1
go build ./...
git add itsm-backend/handlers/service_catalog/repository_impl.go itsm-backend/handlers/service_catalog/repository_impl_test.go
git commit -m "fix(service-catalog): stop deriving target_class from itsm_type, require it explicitly, ahead of migration 024's column drop"
```

---

### Task 15: Full Regression and Schema Diff Verification

**Files:**
- Create: `docs/reports/2026-09-02-unified-intake-p1-verification.md`

**Interfaces:**
- Produces: the acceptance-criteria evidence spec §5.6 requires.

- [ ] **Step 1: `rg` sweeps for dead patterns**

```bash
cd itsm-backend
rg -n "GenerateIncidentNumberForIntake|WorkItemNumberFunc" --glob '*.go' .
rg -n "highestLevel\(" --glob '*.go' handlers/intake
rg -n 'Status: "open"' --glob '*.go' handlers/intake
rg -n "ComputeTargetClass" --glob '*.go' .
rg -n "generateIncidentNumber\(|validateIncidentAssignee\(" --glob '*.go' service/
rg -n "srIncidentBridge" --glob '*.go' .
rg -n "\.CreateIncident\(" --glob '*.go' . | grep -v _test.go
```

Expected: all zero for the first five. The last command (`CreateIncident` production call sites) is a **check, not an assertion of zero** — after Tasks 11/12/13, every known production caller of `IncidentService.CreateIncident` (`/incidents` controller, BPMN callback, `srIncidentBridge`) has moved to Intake; if this command comes back empty, `IncidentService.CreateIncident` (`service/incident_service.go:~77-230`) is now dead code with zero production callers. Do not delete it in this task — its extracted pieces (`GenerateIncidentNumber`/`ResolveIncidentCategory`/`ResolveIncidentPriority`/`ValidateIncidentAssignee`) are Intake's authoritative source, and `CreateIncident` itself still has direct unit-test coverage this plan doesn't touch. Record the finding (dead or not) in the verification report as an explicit follow-up decision for the next phase, rather than silently leaving it unexamined or unilaterally deleting a still-tested method as a side effect of this task.

- [ ] **Step 2: Full backend suite**

```bash
cd itsm-backend
go test ./... -count=1 2>&1 | tee /tmp/phase1-final-test.log
go build ./...
```

Expected: all packages `ok`, build exits 0.

- [ ] **Step 3: PostgreSQL integration and real HTTP E2E** (ported from the feature branch, re-run against the reconciled code)

```bash
cd itsm-backend
go test -tags integration_postgres -v ./handlers/intake -run E2E -count=1
go test -tags integration_rls -v ./database/rls/... -count=1
```

Expected: PASS, zero skips, matching the original branch's report numbers as a floor (not a ceiling — new Change/field-mapping coverage adds cases beyond the original count).

- [ ] **Step 4: Frontend verification**

```bash
cd itsm-frontend
npm run type-check
npx jest --silent
```

Expected: `tsc` zero errors; jest all suites pass (coverage-threshold exit code is a pre-existing, unrelated gap per this session's earlier baseline — do not treat it as a regression unless the percentage drops below the baseline recorded before this plan started).

- [ ] **Step 5: Ent schema full diff (spec §5.2's deferred check)**

```bash
cd /home/administrator/project/itsm
git diff --stat main feat/kaf-delegation-transactional-delivery -- itsm-backend/ent/schema
```

Manually confirm every listed schema file's conflict was addressed by an earlier task (Incident, Change, Problem already confirmed clean during spec work; Service Request extension and the three new Intake tables are the only ones this plan actually changes).

- [ ] **Step 6: Write the verification report and commit**

```markdown
# Unified Intake × P1 Phase 1 Verification

Baseline: <git rev-parse HEAD>
Merge target: main

## Commands and results
<paste every command from Steps 1-5 with exit codes>

## Acceptance criteria (spec §5.6) checklist
1. [ ] workitemnumber.Allocator sole source for tickets.ticket_number; incidents.incident_number confirmed as the separate, intentional identifier it always was
2. [ ] migrations 023/024/025 apply cleanly, itsm_type dropped, target_class NOT NULL + CHECK
3. [ ] Incident priority/status parity with IncidentService, full DTO field coverage
4. [ ] Service Request custom fields readable via entity_type=ticket path
5. [ ] Idempotency-Key mandatory everywhere, four test classes covered
6. [ ] All enumerated creation entry points converged
7. [ ] Change creates real ent.Change extension
8. [ ] Original Unified Intake test suite green on reconciled code
9. [ ] go test/build/tsc all green
10. [ ] Ent schema diff reviewed, no missed conflicts
```

```bash
git add docs/reports/2026-09-02-unified-intake-p1-verification.md
git commit -m "docs: record Phase 1 final verification evidence against spec 5.6 acceptance criteria"
```

## Completion Criteria

- Every row in spec §5.6's acceptance criteria has a checked box in the verification report, each backed by a command and its actual output — not asserted from memory.
- `git diff --check`, `go build ./...`, `go test ./... -count=1`, and frontend `tsc --noEmit` all exit 0 on the final commit.
- No file in the repository still constructs a `ServiceRequest` extension for a `change_request`-targeted Catalog item, still calls a deleted Intake-specific number allocator, or still derives `target_class` from `itsm_type`.
- `tickets.ticket_number` and `incidents.incident_number` remain two distinct identifiers, each with exactly one authoritative generator (`workitemnumber.Allocator` and `IncidentService.GenerateIncidentNumber` respectively) — neither Incident, Service Request, nor Change creators allocate a ticket number themselves.
