# Unified Intake Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementation status (2026-09-01): COMPLETE.** Tasks 1–13 were implemented on
> `feat/kaf-delegation-transactional-delivery`; the KAF cutover is commit `16b1bae` and the
> ITSM verification commit is `f28988ec`. Exact commands, non-green repository baselines and
> release prerequisites are recorded in
> `docs/reports/2026-08-31-unified-intake-implementation-report.md`.

**Goal:** Build one authenticated, tenant-safe, idempotent and transactional WorkItem creation path for Service Request and Incident, then move ITSM Web, existing professional APIs and KAF ticket creation onto it.

**Architecture:** A new `handlers/intake` vertical slice owns create-command orchestration while professional creators own only their extension writes. One PostgreSQL transaction persists the WorkItem, professional extension, fields/CI links, immutable resolution snapshot, audit, workflow-start Outbox and completed idempotency receipt; a leased worker starts the frozen BPMN definition after commit. ITSM identity exchange maps signed KAF channel assertions to an existing tenant user and issues a short-lived, intake-only JWT.

**Tech Stack:** Go 1.24, Gin, Ent, PostgreSQL/RLS, Prometheus, Next.js/TypeScript/Jest, Python 3.12, FastAPI/httpx/Pydantic/pytest.

**Spec:** `docs/superpowers/specs/2026-08-31-unified-intake-design.md`

## Global Constraints

- Work in `/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery` on `feat/kaf-delegation-transactional-delivery`; do not touch the main worktree's unrelated dirty files or the untracked `task-*-review*.md` evidence files.
- ITSM remains authoritative for tenant, actor, requester, WorkItem, professional lifecycle, BPMN, SLA, audit and API contracts.
- `service_request_item` and `incident` are the only first-phase `recordClass` values; every created professional record has exactly one WorkItem in the same transaction.
- Request JSON may not provide `tenantId`, `actorId`, `requesterId`, role or authorization conclusions.
- Unknown intake kinds, record classes, workflow bindings, Outbox events and handlers fail closed.
- Do not add post-commit business goroutines, warning-only critical writes, long-term dual reads/writes, a second workflow engine or an async Saga.
- Catalog `target_class` is authoritative; `itsm_type` is migration input only and no runtime fallback remains after cutover.
- KAF task-scoped automation credentials remain limited to delegated task APIs and are never accepted by Intake.
- KAF changes are made in an isolated linked worktree created from `/home/administrator/actions-runner/_work/kaf/kaf`; preserve that checkout's existing `frontend/tsconfig.app.tsbuildinfo` modification.
- No production KAF or Microsoft Graph write is part of this implementation. Real-path validation targets the configured Dev environment only.

---

### Task 1: Intake Persistence, External Identity Mapping and RLS

**Files:**
- Create: `itsm-backend/ent/schema/intake_request.go`
- Create: `itsm-backend/ent/schema/intake_resolution_snapshot.go`
- Create: `itsm-backend/ent/schema/external_identity.go`
- Modify: `itsm-backend/ent/generate.go`
- Modify: `itsm-backend/ent/` generated files via one `go generate ./ent` run
- Modify: `itsm-backend/migration/migrations.go`
- Modify: `itsm-backend/migration/migrator_test.go`
- Modify: `itsm-backend/internal/bootstrap/post_schema_migrations_test.go`
- Modify: `itsm-backend/database/rls/rls_integration_test.go`

**Interfaces:**
- Produces Ent clients `tx.IntakeRequest`, `tx.IntakeResolutionSnapshot` and `client.ExternalIdentity`.
- Produces migration `020_unified_intake_rls` and SQL upsert builders enabled by `--feature sql/upsert`.

- [x] **Step 1: Write failing migration and schema contract tests**

Add assertions that migration 020 is the last registered migration, enables and forces RLS on all three new tenant tables, and creates tenant policies using `current_setting('app.current_tenant', true)`. Extend the PostgreSQL RLS probe with tenant A/B rows for `intake_requests`, `intake_resolution_snapshots` and `external_identities`.

```go
func TestUnifiedIntakeMigrationEnablesRLS(t *testing.T) {
    sql := GetMigrationSQL("020_unified_intake_rls")
    for _, table := range []string{"intake_requests", "intake_resolution_snapshots", "external_identities"} {
        require.Contains(t, sql, "ALTER TABLE "+table+" ENABLE ROW LEVEL SECURITY")
        require.Contains(t, sql, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY")
        require.Contains(t, sql, "CREATE POLICY "+table+"_tenant_isolation")
    }
}
```

- [x] **Step 2: Run the tests and confirm the missing migration failure**

Run: `cd itsm-backend && go test ./migration ./internal/bootstrap -run 'UnifiedIntake|PostSchema' -count=1`

Expected: FAIL because migration 020 and the new schema types are absent.

- [x] **Step 3: Define the Ent schemas and enable SQL upsert generation**

Use these fields and unique keys:

```go
// IntakeRequest
tenant_id int; actor_id int; channel string; operation string
idempotency_key string; request_digest string; digest_version string
status string; work_item_id optional int; created_at time.Time; completed_at optional time.Time
// unique (tenant_id, actor_id, channel, operation, idempotency_key)

// IntakeResolutionSnapshot
tenant_id int; intake_request_id int unique; work_item_id int unique
channel string; source_provider string; source_event_id optional string
source_conversation_id optional string; catalog_item_id optional int
catalog_version optional string; record_class string
cti_snapshot json.RawMessage; ci_ids []int; form_schema_version optional string
workflow_definition_id optional int; workflow_definition_key optional string
workflow_definition_version optional string; no_process bool
sla_definition_id optional int; resolver_version string; request_digest string
created_at time.Time

// ExternalIdentity
tenant_id int; provider string; workspace string; subject string; user_id int
active bool; created_at time.Time; updated_at time.Time
// unique (provider, workspace, subject), index (tenant_id, user_id)
```

Change the generator directive to:

```go
//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./schema
```

Mark source identifiers and request digests sensitive where Ent supports it. Do not store raw assertions, access tokens, chat text or form values in these tables.

- [x] **Step 4: Generate Ent code**

Run: `cd itsm-backend && go generate ./ent`

Expected: exit 0 and generated `intakerequest`, `intakeresolutionsnapshot` and `externalidentity` packages plus upsert methods.

- [x] **Step 5: Register migration 020 and RLS policies**

Add `020_unified_intake_rls` to `RegisteredMigrations` and return SQL that adds the three tenant policies after Ent schema creation. Use `NULLIF(current_setting('app.current_tenant', true), '')::bigint` and `WITH CHECK` for writes, matching migration 019's fail-closed pattern.

- [x] **Step 6: Run schema, migration and RLS-focused tests**

Run: `cd itsm-backend && go test ./ent/... ./migration ./internal/bootstrap -count=1`

Run when `RLS_TEST_DSN` is configured: `cd itsm-backend && go test -tags integration_rls -v ./database/rls/... -count=1`

Expected: all non-optional tests PASS; the RLS command must report zero skips before release acceptance.

- [x] **Step 7: Commit the persistence foundation**

```bash
git add itsm-backend/ent itsm-backend/migration itsm-backend/internal/bootstrap/post_schema_migrations_test.go itsm-backend/database/rls/rls_integration_test.go
git commit -m "feat(intake): add tenant-scoped persistence foundation"
```

---

### Task 2: Typed Command, Identity and Canonical Digest

**Files:**
- Create: `itsm-backend/handlers/intake/command.go`
- Create: `itsm-backend/handlers/intake/identity.go`
- Create: `itsm-backend/handlers/intake/canonicalize.go`
- Create: `itsm-backend/handlers/intake/errors.go`
- Create: `itsm-backend/handlers/intake/canonicalize_test.go`
- Create: `itsm-backend/handlers/intake/identity_test.go`

**Interfaces:**
- Produces `Identity`, `CreateWorkItemCommand`, `CreateWorkItemResult`, `ResolvedIntake`, `CreationPlan`, `ProfessionalReference`, `CanonicalizeCommand` and stable typed errors.
- Consumed by all later Intake tasks.

- [x] **Step 1: Write failing canonicalization and identity tests**

Cover object-key ordering, UTC timestamps, string trimming, CI set sorting/deduplication, preservation of form-array order, rejected identity fields, source/provider mismatch and digest version stability.

```go
func TestCanonicalizeCommandProducesSameDigestForEquivalentCISets(t *testing.T) {
    a := validIncidentCommand("key-1", []int{9, 3, 9})
    b := validIncidentCommand("key-1", []int{3, 9})
    _, digestA, err := CanonicalizeCommand(a)
    require.NoError(t, err)
    _, digestB, err := CanonicalizeCommand(b)
    require.NoError(t, err)
    require.Equal(t, digestA, digestB)
}
```

- [x] **Step 2: Run tests and verify missing-type failures**

Run: `cd itsm-backend && go test ./handlers/intake -run 'Canonical|Identity' -count=1`

Expected: FAIL because the package implementation is absent.

- [x] **Step 3: Implement the command and result types**

Define these exact public shapes:

```go
type Identity struct {
    TenantID int
    ActorID int
    RequesterID int
    Role string
    Channel string
    Provider string
    TokenID string
}

type CreateWorkItemCommand struct {
    IdempotencyKey string `json:"idempotencyKey"`
    IntakeKind string `json:"intakeKind"`
    Title string `json:"title"`
    Description string `json:"description,omitempty"`
    CatalogItemID *int `json:"catalogItemId,omitempty"`
    CTI *CTIInput `json:"cti,omitempty"`
    CIIDs []int `json:"ciIds,omitempty"`
    FormValues map[string]any `json:"formValues,omitempty"`
    SourceReference *SourceReference `json:"sourceReference,omitempty"`
    Incident *IncidentInput `json:"incident,omitempty"`
}

type CreateWorkItemResult struct {
    WorkItemID int `json:"workItemId"`
    Number string `json:"number"`
    RecordClass string `json:"recordClass"`
    ProfessionalReference ProfessionalReference `json:"professionalReference"`
    WorkflowStartStatus string `json:"workflowStartStatus"`
    Replayed bool `json:"replayed"`
}
```

Use `json.Decoder.DisallowUnknownFields` in the HTTP layer so hidden identity fields are rejected rather than ignored.

- [x] **Step 4: Implement canonicalization version `intake-v1`**

Build a private canonical struct that excludes the key itself but includes every semantic command field. Sort/deduplicate `CIIDs`, recursively sort map keys with `encoding/json`, preserve array order, normalize timestamps to RFC3339 UTC, hash with SHA-256 and encode lowercase hex. Return a copied normalized command; never mutate the caller's maps or slices.

- [x] **Step 5: Implement typed errors**

Define sentinel codes `InvalidCommand`, `AuthenticationRequired`, `PermissionDenied`, `ReferenceNotFound`, `IdempotencyConflict`, `DomainValidationFailed`, `InfrastructureUnavailable`, `InternalFailure`, `UnsupportedRecordClass` and `WorkflowBindingRequired`. Each error carries HTTP status, `Retryable`, safe message and optional field errors; underlying errors remain available through `Unwrap()` but are never serialized.

- [x] **Step 6: Run focused tests**

Run: `cd itsm-backend && go test ./handlers/intake -run 'Canonical|Identity|Error' -count=1`

Expected: PASS.

- [x] **Step 7: Commit typed contracts**

```bash
git add itsm-backend/handlers/intake
git commit -m "feat(intake): define typed create command"
```

---

### Task 3: Idempotency Receipt and Transaction Contributors

**Files:**
- Create: `itsm-backend/handlers/intake/idempotency_repository.go`
- Create: `itsm-backend/handlers/intake/snapshot_repository.go`
- Create: `itsm-backend/handlers/intake/audit_repository.go`
- Create: `itsm-backend/handlers/intake/idempotency_repository_test.go`
- Modify: `itsm-backend/service/field_value_service.go`
- Modify: `itsm-backend/service/field_value_service_test.go`
- Modify: `itsm-backend/service/outbox_event_repository.go`
- Modify: `itsm-backend/service/outbox_event_repository_test.go`

**Interfaces:**
- Produces `IdempotencyRepository.Claim`, `.Complete`, `.LoadCompleted`, `SnapshotRepository.Create`, `AuditRepository.RecordCreated`, `FieldValueService.CreateValuesTx`, and deterministic Outbox event IDs. `Claim` returns `ClaimInserted`, `ClaimReplay` or a typed conflict.
- Consumes Task 1 Ent clients and Task 2 command/identity types.

- [x] **Step 1: Write failing exact-replay and rollback tests**

Use SQLite for repository behavior and PostgreSQL-tagged tests for real conflict waiting. Assert one claim for 20 concurrent callers, same digest replay, different digest conflict, completed receipt requires a WorkItem, and transaction rollback removes the claim.

```go
func TestIdempotencyClaimRejectsDifferentDigest(t *testing.T) {
    tx := beginTestTx(t, client)
    _, outcome, err := repo.Claim(ctx, tx, identity, "same-key", "digest-a", "intake-v1")
    require.NoError(t, err)
    require.Equal(t, ClaimInserted, outcome)
    require.NoError(t, tx.Commit())

    tx2 := beginTestTx(t, client)
    _, _, err = repo.Claim(ctx, tx2, identity, "same-key", "digest-b", "intake-v1")
    require.ErrorIs(t, err, ErrIdempotencyConflict)
    require.NoError(t, tx2.Rollback())
}
```

- [x] **Step 2: Run focused tests and verify failure**

Run: `cd itsm-backend && go test ./handlers/intake ./service -run 'Idempotency|CreateValuesTx|DeterministicOutbox' -count=1`

Expected: FAIL because the repositories and tx-aware field writer are absent.

- [x] **Step 3: Implement atomic claim with generated Ent upsert**

Use generated `OnConflict(sql.ConflictColumns(...), sql.DoNothing())` inside the caller's `*ent.Tx`. If no row is inserted, load the conflicting row after PostgreSQL resolves the concurrent transaction; return replay only for matching digest/version and completed WorkItem, conflict for a different digest, and retry the whole outer transaction if a concurrent owner rolled back. Never continue after an ordinary constraint error has aborted the transaction.

- [x] **Step 4: Add tx-aware FieldValue creation**

Refactor the existing validation/marshalling loop behind:

```go
func (s *FieldValueService) CreateValuesTx(
    ctx context.Context,
    tx *ent.Tx,
    tenantID int,
    defEntityType string,
    defEntityID int,
    valueEntityType string,
    valueEntityID int,
    values map[string]any,
) error
```

`CreateValues` opens no transaction and delegates with `tx=nil`; the shared implementation selects `tx.FieldDefinition/FieldValue` when a transaction is present. Validation failure is returned, never logged and swallowed.

- [x] **Step 5: Implement Snapshot and audit writers**

`SnapshotRepository.Create` writes only the fields defined in the spec and rejects metadata keys named `title`, `description`, `requester`, `formValues`, `token`, `secret`, `authorization` or `password`. `AuditRepository.RecordCreated` writes `resource=work_item:<id>`, `action=intake.created`, path, method, status, tenant/user/request IDs and no request body.

- [x] **Step 6: Add deterministic workflow event helper**

Add `NewWorkflowStartEventID(workItemID int, definitionID int) string` returning `workflow-start:<workItemID>:<definitionID>`. Preserve `OutboxEventRepository.Enqueue(ctx, tx, event)` as the single transaction-aware writer.

- [x] **Step 7: Run focused tests**

Run: `cd itsm-backend && go test ./handlers/intake ./service -run 'Idempotency|Snapshot|Audit|CreateValuesTx|DeterministicOutbox' -count=1`

Expected: PASS.

- [x] **Step 8: Commit transaction contributors**

```bash
git add itsm-backend/handlers/intake itsm-backend/service/field_value_service.go itsm-backend/service/field_value_service_test.go itsm-backend/service/outbox_event_repository.go itsm-backend/service/outbox_event_repository_test.go
git commit -m "feat(intake): add transactional create contributors"
```

---

### Task 4: Catalog, CTI, CI, Permission and Workflow Resolution

**Files:**
- Create: `itsm-backend/handlers/intake/resolver.go`
- Create: `itsm-backend/handlers/intake/resolver_test.go`
- Modify: `itsm-backend/service/bpmn_process_binding_service.go`
- Modify: `itsm-backend/service/bpmn_process_binding_service_test.go`
- Modify: `itsm-backend/handlers/service_catalog/entity.go`
- Modify: `itsm-backend/handlers/service_catalog/repository_impl.go`
- Modify: `itsm-backend/handlers/service_catalog/repository_impl_test.go`

**Interfaces:**
- Produces `Resolver.Resolve(ctx, tx, identity, command) (*ResolvedIntake, error)` and `ResolvedWorkflowBinding` containing exact process definition ID/key/version or `NoProcess=true`.
- Consumed by Task 5 creators and Task 6 Application Service.

- [x] **Step 1: Write failing resolver table tests**

Cover enabled Catalog → `service_request_item`, Catalog → `incident`, direct Incident, disabled/cross-tenant Catalog, incomplete CTI chain, cross-tenant CI, form schema violation, permission denial, exact binding, explicit no-process and missing binding fail-closed.

- [x] **Step 2: Run tests and verify the resolver is missing**

Run: `cd itsm-backend && go test ./handlers/intake ./service ./handlers/service_catalog -run 'Resolve|TargetClass|Binding' -count=1`

Expected: FAIL.

- [x] **Step 3: Make Catalog reads transaction-aware**

Add a repository query that accepts `*ent.Tx`, filters `tenant_id`, `status IN ('active','enabled')`, returns `target_class`, a stable version derived from the row's `updated_at`, process definition key, field schema references, CI type and SLA values. It must never derive `target_class` from `itsm_type`.

- [x] **Step 4: Resolve and freeze the exact workflow definition**

Add a read-only binding method that receives the transaction client, uses Catalog `process_definition_key` first and existing binding priority second, and resolves one active `ProcessDefinition` row. Return its integer ID, key and string version. A configured `conditions.no_process=true` is the only no-process result; nil binding without that declaration returns `WorkflowBindingRequired`.

- [x] **Step 5: Implement deterministic reference and permission validation**

Resolve user active state, target-specific permission, CTI ancestry and CI tenant visibility using the same transaction. For direct Incident use `recordClass=incident`; for Catalog accept only `service_request_item` or `incident`. Generate `ResolvedIntake` with normalized command, authoritative target class, field definitions, CI entities, frozen workflow/SLA references and snapshot-safe provenance.

- [x] **Step 6: Run focused tests**

Run: `cd itsm-backend && go test ./handlers/intake ./service ./handlers/service_catalog -run 'Resolve|TargetClass|Binding' -count=1`

Expected: PASS.

- [x] **Step 7: Commit deterministic resolution**

```bash
git add itsm-backend/handlers/intake/resolver.go itsm-backend/handlers/intake/resolver_test.go itsm-backend/service/bpmn_process_binding_service.go itsm-backend/service/bpmn_process_binding_service_test.go itsm-backend/handlers/service_catalog
git commit -m "feat(intake): resolve authoritative create configuration"
```

---

### Task 5: WorkItem Base and Professional Creator Registry

**Files:**
- Create: `itsm-backend/handlers/intake/creator.go`
- Create: `itsm-backend/handlers/intake/work_item_creator.go`
- Create: `itsm-backend/handlers/intake/incident_creator.go`
- Create: `itsm-backend/handlers/intake/service_request_creator.go`
- Create: `itsm-backend/handlers/intake/creator_test.go`
- Modify: `itsm-backend/service/incident_service.go`
- Modify: `itsm-backend/service/incident_service_test.go`
- Modify: `itsm-backend/handlers/service_request/service.go`
- Modify: `itsm-backend/handlers/service_request/service_test.go`

**Interfaces:**
- Produces `CreatorRegistry.Register/Get`, `WorkItemCreator.CreateBase`, `IncidentCreator.CreateExtension`, and `ServiceRequestItemCreator.CreateExtension`.
- `ProfessionalCreator` exactly matches the design spec and accepts the caller's `*ent.Tx`.

- [x] **Step 1: Write failing registry and creator tests**

Assert duplicate registration fails, unknown class returns `UnsupportedRecordClass`, each creator writes exactly one extension, a forced extension error rolls back the WorkItem, and neither creator invokes process trigger, rule engine, notification or goroutine hooks.

```go
func TestCreatorRegistryFailsClosedForUnknownClass(t *testing.T) {
    registry := NewCreatorRegistry()
    _, err := registry.Get("change_request")
    require.ErrorIs(t, err, ErrUnsupportedRecordClass)
}
```

- [x] **Step 2: Run creator tests and verify failure**

Run: `cd itsm-backend && go test ./handlers/intake ./service ./handlers/service_request -run 'Creator|CreateExtension|Atomic' -count=1`

Expected: FAIL.

- [x] **Step 3: Implement the WorkItem base writer**

Create the Ticket through `tx.Ticket.Create()` with immutable resolved class, authenticated requester/opener, tenant, normalized title/description, calculated priority, category, SLA fields and ticket number. The number allocator may consume a number outside the transaction but may not persist a partial WorkItem.

- [x] **Step 4: Implement `IncidentCreator`**

Move only creation-time Incident validation and professional writes from `IncidentService.CreateIncident`: severity, impact, urgency, incident number, source, detected time, CI edges and one creation `IncidentEvent`. Do not start rules/BPMN. Until Task 9 removes legacy columns, populate required legacy columns from the immutable plan in this branch only; no later code may read them as authoritative.

- [x] **Step 5: Implement `ServiceRequestItemCreator`**

Move Catalog-specific professional fields, approval-chain snapshot, infrastructure validation, CI link and extension write from `service_request.Service.Create`. It must not create another WorkItem, open another transaction, write FieldValue or start BPMN.

- [x] **Step 6: Run focused tests**

Run: `cd itsm-backend && go test ./handlers/intake ./service ./handlers/service_request -run 'Creator|CreateExtension|Atomic' -count=1`

Expected: PASS.

- [x] **Step 7: Commit creator boundaries**

```bash
git add itsm-backend/handlers/intake itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go itsm-backend/handlers/service_request/service.go itsm-backend/handlers/service_request/service_test.go
git commit -m "refactor(intake): extract professional creators"
```

---

### Task 6: Unified Intake Application Service

**Files:**
- Create: `itsm-backend/handlers/intake/service.go`
- Create: `itsm-backend/handlers/intake/service_test.go`
- Create: `itsm-backend/handlers/intake/postgres_integration_test.go`

**Interfaces:**
- Produces `Service.Create(ctx, identity, command) (*CreateWorkItemResult, error)`.
- Consumes Tasks 2–5 and the existing Ent client/Outbox repository.

- [x] **Step 1: Write the fault-injection and replay test matrix**

Table-drive failures at receipt claim, resolve, base WorkItem, extension, professional event, FieldValue, CI link, Snapshot, audit, Outbox and receipt completion. After each failure assert zero committed rows for the intake key. Add exact replay and 20-way concurrent create tests that assert one of every authoritative record.

- [x] **Step 2: Run service tests and verify failure**

Run: `cd itsm-backend && go test ./handlers/intake -run 'ServiceCreate|Rollback|ConcurrentReplay' -count=1`

Expected: FAIL.

- [x] **Step 3: Implement the one-transaction orchestration**

Implement this order without external calls:

```go
func (s *Service) Create(ctx context.Context, identity Identity, cmd CreateWorkItemCommand) (*CreateWorkItemResult, error) {
    normalized, digest, err := CanonicalizeCommand(cmd)
    if err != nil { return nil, err }
    for attempt := 0; attempt < 3; attempt++ {
        result, retry, err := s.createAttempt(ctx, identity, normalized, digest)
        if !retry { return result, err }
    }
    return nil, NewInfrastructureUnavailable("idempotency owner rolled back")
}
```

`createAttempt` begins one Ent transaction, claims receipt, resolves references, obtains Creator, creates WorkItem/extension/fields/CI/Snapshot/audit/Outbox, completes receipt, commits and returns `workflowStartStatus=pending` or `not_required`. On replay it rolls back the unused transaction, loads WorkItem/professional reference and projects current workflow status.

- [x] **Step 4: Prove rollback and concurrency on PostgreSQL**

Use the repository's configured integration DSN and independent connections; SQLite cannot be the only concurrency evidence. Verify exactly one completed receipt, WorkItem, extension, Snapshot and event after concurrent requests.

- [x] **Step 5: Run the complete Intake package**

Run: `cd itsm-backend && go test ./handlers/intake -count=1`

Expected: PASS.

- [x] **Step 6: Commit the application service**

```bash
git add itsm-backend/handlers/intake
git commit -m "feat(intake): create work items transactionally"
```

---

### Task 7: HTTP Contract, Router Wiring and Stable Errors

**Files:**
- Create: `itsm-backend/handlers/intake/handler.go`
- Create: `itsm-backend/handlers/intake/handler_test.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/router/router_test.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Modify: `itsm-backend/common/response.go`

**Interfaces:**
- Produces `POST /api/v1/intake/work-items` for normal access JWTs.
- Identity exchange tokens are added in Task 10 without changing the command contract.

- [x] **Step 1: Write failing HTTP contract tests**

Cover 201 first create, 200 replay, 400 unknown/identity fields or missing key, 401 unauthenticated, 403 target permission, 404 cross-tenant reference, 409 digest mismatch, 422 field validation and safe 500/503 bodies. Assert `requestId`, stable code and `retryable` are present and raw errors absent.

- [x] **Step 2: Run handler/router tests and verify missing route**

Run: `cd itsm-backend && go test ./handlers/intake ./router -run 'Intake|CreateWorkItem' -count=1`

Expected: FAIL with missing handler/route.

- [x] **Step 3: Implement strict decoding and ActorResolver**

Use a `json.Decoder` with `DisallowUnknownFields`, enforce one JSON value and body-size limit, derive tenant/user/role from Gin context, set requester=actor and channel=`itsm_web` for cookie auth or `itsm_api` for bearer access. Do not inspect body identity fields.

- [x] **Step 4: Wire bootstrap and route permissions**

Construct Resolver, repositories, Creator Registry and Service once in `internal/bootstrap/app.go`; add `IntakeHandler` to `RouterConfig`; register the route below auth/RBAC/TenantMiddleware with an `intake:create` DB permission plus domain permission enforced by Service. Seed/migrate `intake:create` for approved creator roles, `intake:intervene` only for operational administrator roles and `intake:identity_admin` only for tenant identity administrators using the repository's existing permission initialization path.

- [x] **Step 5: Return exact status codes**

The new Handler writes the common envelope with HTTP 201 for a first result and 200 for replay. Typed errors map according to spec; the response body never uses `err.Error()` for internal errors.

- [x] **Step 6: Run HTTP tests**

Run: `cd itsm-backend && go test ./handlers/intake ./router ./internal/bootstrap -run 'Intake|CreateWorkItem|Permission' -count=1`

Expected: PASS.

- [x] **Step 7: Commit the HTTP boundary**

```bash
git add itsm-backend/handlers/intake itsm-backend/router itsm-backend/internal/bootstrap/app.go itsm-backend/common/response.go itsm-backend/pkg/seeder
git commit -m "feat(intake): expose authenticated create endpoint"
```

---

### Task 8: Reliable Frozen-Definition BPMN Start

**Files:**
- Create: `itsm-backend/service/workflow_start_outbox_dispatcher.go`
- Create: `itsm-backend/service/workflow_start_outbox_dispatcher_test.go`
- Create: `itsm-backend/handlers/intake/workflow_intervention_handler.go`
- Create: `itsm-backend/handlers/intake/workflow_intervention_handler_test.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Modify: `itsm-backend/service/bpmn_process_engine_test.go`
- Modify: `itsm-backend/service/outbox_event_repository.go`
- Modify: `itsm-backend/service/outbox_event_repository_test.go`
- Modify: `itsm-backend/config/config.go`
- Create: `itsm-backend/config/workflow_outbox_config_test.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Modify: `itsm-backend/internal/bootstrap/kaf_outbox_lifecycle_test.go`

**Interfaces:**
- Produces `CustomProcessEngine.StartProcessByDefinitionID`, `OutboxEventRepository.MarkDead`, and `WorkflowStartOutboxDispatcher.Run`.
- Produces tenant-scoped `POST /api/v1/intake/work-items/:id/workflow-start/retry` guarded by `intake:intervene`.
- Consumes event type `workflow.start.requested` emitted by Task 6.

- [x] **Step 1: Write crash-window, duplicate and dead-letter tests**

Assert a frozen definition ID is used after a newer version becomes latest, duplicate delivery returns the existing process, callback/update failure retries without a second instance, lease expiry is reclaimed, unknown payload fails closed and max attempts marks `dead` with an audit.

- [x] **Step 2: Run focused tests and verify failure**

Run: `cd itsm-backend && go test ./service ./config ./internal/bootstrap -run 'WorkflowStart|StartProcessByDefinition|OutboxDead' -count=1`

Expected: FAIL.

- [x] **Step 3: Refactor BPMN start around an exact definition**

Extract the existing parse/create/advance logic into a private method receiving `*ent.ProcessDefinition`. Keep `StartProcess` behavior for legacy callers. Add:

```go
func (e *CustomProcessEngine) StartProcessByDefinitionID(
    ctx context.Context,
    definitionID int,
    businessKey string,
    businessType string,
    businessID int,
    variables map[string]any,
) (*ent.ProcessInstance, error)
```

Query by ID, tenant and active status. Before insert, and after the running-unique conflict, load the existing tenant/business-key instance and return it as the idempotent result.

- [x] **Step 4: Implement the dispatcher and terminal failure**

Claim only `workflow.start.requested`; validate the payload schema; establish BPMN tenant context; call the exact-definition method; mark published on success. Retry with existing lease/backoff and sanitized errors. At configured max attempts call `MarkDead`, write `intake.workflow_start.manual_intervention_required` audit and expose `dead` through the Intake workflow projection.

- [x] **Step 5: Add lifecycle configuration**

Load `WORKFLOW_OUTBOX_BATCH_SIZE`, `WORKFLOW_OUTBOX_POLL_INTERVAL` and `WORKFLOW_OUTBOX_MAX_ATTEMPTS` with bounded defaults. Start/stop the dispatcher alongside the existing KAF dispatcher; do not combine their handlers or event claims.

- [x] **Step 6: Add tenant-scoped manual intervention**

Add `OutboxEventRepository.RetryDeadWorkflowStart(ctx, tenantID, workItemID)` that conditionally moves only the matching `dead` event to `pending`, clears sanitized error/claim state, retains the original event/dedupe ID and writes an audit in the same transaction. The Handler derives tenant/actor from authentication, requires `intake:intervene`, rejects pending/published/foreign-tenant events and never accepts a caller-supplied event ID or tenant.

- [x] **Step 7: Run focused tests**

Run: `cd itsm-backend && go test ./service ./handlers/intake ./config ./internal/bootstrap -run 'WorkflowStart|StartProcessByDefinition|OutboxDead|ManualIntervention' -count=1`

Expected: PASS.

- [x] **Step 8: Commit reliable start**

```bash
git add itsm-backend/service itsm-backend/config itsm-backend/internal/bootstrap
git commit -m "feat(intake): start frozen workflows through outbox"
```

---

### Task 9: Cut Over Existing Professional APIs and Remove Duplicate Creation Paths

**Files:**
- Modify: `itsm-backend/controller/incident_controller.go`
- Modify: `itsm-backend/controller/incident_controller_test.go`
- Modify: `itsm-backend/handlers/service_request/handler.go`
- Modify: `itsm-backend/handlers/service_request/handler_test.go`
- Modify: `itsm-backend/handlers/service_request/service.go`
- Modify: `itsm-backend/service/incident_service.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Modify: `itsm-frontend/src/lib/api/incident-api.ts`
- Modify: `itsm-frontend/src/lib/api/__tests__/incident-api.test.ts`
- Modify: `itsm-frontend/src/lib/api/service-catalog-api.ts`
- Modify: `itsm-frontend/src/lib/api/__tests__/service-catalog-api.test.ts`
- Create: `itsm-frontend/src/lib/api/idempotency-key.ts`
- Create: `itsm-frontend/src/lib/api/__tests__/idempotency-key.test.ts`

**Interfaces:**
- Existing `POST /api/v1/incidents` and `POST /api/v1/service-requests` require `Idempotency-Key` and delegate to `intake.Service.Create`.
- Frontend creates one UUID per user submission and reuses it for any retry of that submission.

- [x] **Step 1: Write failing adapter and frontend header tests**

Assert both legacy URLs reject a missing key, ignore no caller identity, call the unified service once, preserve their response DTOs and create no post-commit goroutine. Frontend tests assert `Idempotency-Key` is present and stable when the same submission object retries.

- [x] **Step 2: Run adapter tests and verify failure**

Run: `cd itsm-backend && go test ./controller ./handlers/service_request -run 'IdempotencyKey|UnifiedIntakeAdapter' -count=1`

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/incident-api.test.ts src/lib/api/__tests__/service-catalog-api.test.ts src/lib/api/__tests__/idempotency-key.test.ts`

Expected: FAIL because keys and adapters are absent.

- [x] **Step 3: Add stable frontend keys**

Implement:

```ts
export const newIdempotencyKey = (): string => crypto.randomUUID();
```

Creation methods accept an optional key parameter for a caller-owned retry loop, generate once at the start of a new submission when absent, and send it through `httpClient.post(..., {headers: {'Idempotency-Key': key}})`. Do not regenerate inside a network retry callback.

- [x] **Step 4: Convert Incident and Service Request handlers to thin adapters**

Build typed commands from their existing DTOs, derive Identity from context and call the one Intake Service. Map the professional result back through authoritative WorkItem/professional reads. Delete calls to `IncidentService.CreateIncident` and `service_request.Service.Create` from HTTP creation paths.

- [x] **Step 5: Delete the replaced creation implementation**

Remove Service Request's `createWorkItemAndExtension`, post-commit FieldValue block, `triggerWorkflowAfterServiceRequestCommit`, Catalog-to-Incident bridge and creation-only dependencies. Remove Incident's WorkItem/Incident transaction and post-create rule/BPMN goroutines while preserving lifecycle methods and the Creator's shared validation helpers in focused files.

- [x] **Step 6: Run backend and frontend focused tests**

Run: `cd itsm-backend && go test ./handlers/intake ./controller ./handlers/service_request ./service -run 'CreateIncident|ServiceRequest|UnifiedIntake|Idempotency' -count=1`

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/incident-api.test.ts src/lib/api/__tests__/service-catalog-api.test.ts src/lib/api/__tests__/idempotency-key.test.ts`

Expected: PASS.

- [x] **Step 7: Commit the cutover**

```bash
git add itsm-backend/controller/incident_controller.go itsm-backend/controller/incident_controller_test.go itsm-backend/handlers/service_request itsm-backend/service/incident_service.go itsm-backend/internal/bootstrap/app.go itsm-frontend/src/lib/api
git commit -m "refactor(intake): route professional creates through one service"
```

---

### Task 10: Remove Legacy Public-Field and Catalog Target-Class Dual Authority

**Files:**
- Modify: `itsm-backend/ent/schema/incident.go`
- Modify: `itsm-backend/ent/schema/servicerequest.go`
- Modify: `itsm-backend/ent/schema/servicecatalog.go`
- Modify: `itsm-backend/ent/` generated files via `go generate ./ent`
- Modify: `itsm-backend/migration/migrations.go`
- Modify: `itsm-backend/migration/migrator_test.go`
- Modify: `itsm-backend/service/incident_service.go`
- Modify: `itsm-backend/dto/mappers.go`
- Modify: `itsm-backend/handlers/service_request/entity.go`
- Modify: `itsm-backend/handlers/service_request/repository_impl.go`
- Modify: affected Incident/Service Request tests discovered by `rg 'Incident\.(Title|Description|Status|Priority|ReporterID|TenantID)|RequesterID' itsm-backend --glob '*.go'`

**Interfaces:**
- WorkItem becomes the sole read/write owner of shared public fields.
- Catalog runtime reads only `target_class`.

- [x] **Step 1: Add failing authority-contract tests**

Create regression tests that update WorkItem title/status/requester and verify Incident/Service Request responses reflect the WorkItem without extension synchronization. Assert Catalog create/update requires a valid `targetClass` and never derives it from `itsmType`.

- [x] **Step 2: Run authority tests and capture current dual-source failures**

Run: `cd itsm-backend && go test ./service ./handlers/service_request ./handlers/service_catalog -run 'WorkItemAuthority|TargetClassAuthority' -count=1`

Expected: FAIL because reads or schema still use duplicated fields.

- [x] **Step 3: Move all reads to WorkItem joins**

Update Incident response mapping and Service Request list/detail repository queries to fetch title, description, status, priority, requester, tenant and public timestamps from Ticket. Professional extension DTOs retain only professional fields and an explicit WorkItem reference.

- [x] **Step 4: Add irreversible migration 021 and update schemas**

Migration 021 validates every extension has a WorkItem, backfills any remaining valid association, then drops Incident shared columns (`title`, `description`, `status`, `priority`, `reporter_id`, `tenant_id`, public timestamps) and Service Request duplicates (`requester_id`, `tenant_id`, public timestamps) only after all reads moved. Replace extension-table RLS policies with fail-closed `EXISTS` policies joining `tickets.id=work_item_id`/`ticket_id` and comparing the Ticket tenant to `app.current_tenant`; do not retain tenant columns merely for RLS. Drop Catalog runtime `itsm_type` after verifying every active row has a supported `target_class`, including existing classes outside Intake such as `change_request`; Intake itself still rejects every class except `service_request_item` and `incident`. Keep professional status/time columns only when their meaning is genuinely professional and rename them explicitly if needed.

- [x] **Step 5: Regenerate Ent and fix compile-time references**

Run: `cd itsm-backend && go generate ./ent`

Run: `cd itsm-backend && go test ./service ./handlers/service_request ./handlers/service_catalog ./dto -count=1`

Expected: PASS with no generated or handwritten reference to removed columns.

- [x] **Step 6: Search for forbidden dual authority**

Run: `rg -n 'Set(Title|Description|Status|Priority|ReporterID|TenantID)\(' itsm-backend/handlers/intake/incident_creator.go itsm-backend/handlers/intake/service_request_creator.go`

Expected: no output for extension builders; shared setters appear only on the Ticket builder.

- [x] **Step 7: Commit authority convergence**

```bash
git add itsm-backend/ent itsm-backend/migration itsm-backend/service itsm-backend/dto itsm-backend/handlers/service_request itsm-backend/handlers/service_catalog itsm-backend/handlers/intake
git commit -m "refactor(workitem): remove intake field dual authority"
```

---

### Task 11: ITSM Intake Identity Exchange

**Files:**
- Create: `itsm-backend/handlers/intake/identity_exchange.go`
- Create: `itsm-backend/handlers/intake/identity_exchange_test.go`
- Create: `itsm-backend/handlers/intake/identity_mapping_handler.go`
- Create: `itsm-backend/handlers/intake/identity_mapping_handler_test.go`
- Create: `itsm-backend/middleware/intake_auth.go`
- Create: `itsm-backend/middleware/intake_auth_test.go`
- Modify: `itsm-backend/middleware/auth.go`
- Modify: `itsm-backend/config/config.go`
- Create: `itsm-backend/config/kaf_intake_config_test.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`

**Interfaces:**
- Produces `POST /api/v1/intake/identity-exchange` and a five-minute `tokenType=intake`, `aud=itsm-intake`, `scope=[intake:create]` JWT.
- Produces tenant-scoped list/create/disable mapping endpoints guarded by `intake:identity_admin`; no direct database provisioning is required.
- Consumes `ExternalIdentity(provider, workspace, subject) -> tenant + active User` and an authenticated HMAC connector assertion.

- [x] **Step 1: Write failing exchange and route-isolation tests**

Cover valid signed assertion, expired timestamp, reused nonce, invalid HMAC, unknown/disabled mapping, tenant/workspace mismatch, inactive user, five-minute expiry, correct audience/scope/channel and rejection of the resulting token on every non-Intake route. Mapping tests cover tenant-scoped list/create/disable, duplicate provider/workspace/subject, cross-tenant user rejection, permission denial and audit persistence.

- [x] **Step 2: Run identity tests and verify failure**

Run: `cd itsm-backend && go test ./handlers/intake ./middleware ./config ./router -run 'IdentityExchange|IntakeToken' -count=1`

Expected: FAIL.

- [x] **Step 3: Load dedicated exchange configuration**

Add `KAF_INTAKE_EXCHANGE_SECRET`, maximum assertion age of 60 seconds and token TTL fixed/bounded to five minutes. Fail startup when exchange is enabled without a secret. Never reuse `KAF_WEBHOOK_SECRET` or the automation JWT.

- [x] **Step 4: Verify a canonical connector assertion**

The request contains `provider`, `subject`, `channel`, `workspace`, `eventId`, `issuedAt`, `nonce` and `signature`. Canonicalize those fields in a fixed newline-delimited order and verify HMAC-SHA256 with `subtle.ConstantTimeCompare`. Store the nonce in Redis with a 60-second NX TTL; fail closed if replay protection is unavailable. Apply the existing IP/client rate limiter before signature verification and audit bounded denial codes.

- [x] **Step 5: Map identity and issue an intake-only JWT**

Resolve exactly one active `ExternalIdentity` by provider/workspace/subject and take tenant from that mapping, then load the mapped User in the same tenant; query current role/permissions from DB; issue claims with random JTI, internal user/tenant, channel, provider, `tokenType=intake`, audience `itsm-intake` and scope `intake:create`. Audit success and denial without storing assertion/signature. The assertion contains no tenant field and cannot override the mapping.

- [x] **Step 6: Add audited mapping lifecycle endpoints**

Under normal JWT/RBAC/TenantMiddleware expose `GET /api/v1/intake/external-identities`, `POST /api/v1/intake/external-identities` and `POST /api/v1/intake/external-identities/:id/disable`. The create DTO accepts provider, workspace, subject and target user ID as administrator configuration, verifies the user belongs to the authenticated tenant and writes `intake.identity_mapping.created` audit in the same transaction. Disable uses tenant + row ID + optimistic version, writes `intake.identity_mapping.disabled`, and never deletes evidence. Responses never include secrets or signed assertions.

- [x] **Step 7: Add isolated Intake authentication**

Extract common JWT parsing without loosening `AuthMiddleware`. `IntakeAuthMiddleware` accepts normal `access` tokens or exact `intake` tokens; the general middleware continues to reject `tokenType=intake`. Register exchange outside user JWT auth but behind HMAC verification, and register `/intake/work-items` in an Intake-only group with RBAC/TenantMiddleware.

- [x] **Step 8: Run focused security tests**

Run: `cd itsm-backend && go test ./handlers/intake ./middleware ./config ./router -run 'IdentityExchange|IntakeToken' -count=1`

Expected: PASS.

- [x] **Step 9: Commit identity exchange**

```bash
git add itsm-backend/handlers/intake itsm-backend/middleware itsm-backend/config itsm-backend/router itsm-backend/internal/bootstrap/app.go
git commit -m "feat(intake): exchange verified channel identities"
```

---

### Task 12: KAF Typed Intake Client and Ticket-Create Cutover

**Files:**
- Create in KAF worktree: `src/acp/itsm_intake/__init__.py`
- Create in KAF worktree: `src/acp/itsm_intake/contracts.py`
- Create in KAF worktree: `src/acp/itsm_intake/client.py`
- Create in KAF worktree: `tests/test_itsm_intake_client.py`
- Modify in KAF worktree: `src/acp/config.py`
- Modify in KAF worktree: `src/acp/mcp/tools/itsm.py`
- Modify in KAF worktree: `src/acp/orchestration/shared/escalation_subgraph.py`
- Modify in KAF worktree: `src/acp/chat/turns.py`
- Modify in KAF worktree: `src/acp/routers/chat.py`
- Modify in KAF worktree: `src/acp/tools/governance.py`
- Modify in KAF worktree: `tests/test_ticket_create_fallback.py`
- Modify in KAF worktree: `tests/test_graph_send_mail_tool.py`
- Modify in KAF worktree: every source/test Procedure contract returned by `rg -l 'ticket_create|employee_id.*ticket' src tests scripts/procedures`

**Interfaces:**
- Produces `ItsmIntakeClient.exchange_identity(assertion)` and `.create_work_item(token, command)`.
- `ticket_create` requires typed `intake_kind` plus structured Catalog/CTI/Incident input and derives identity/source/idempotency from turn audit context.

- [x] **Step 1: Create an isolated KAF worktree and verify baseline dirt**

From `/home/administrator/actions-runner/_work/kaf/kaf`, create a linked branch/worktree named `feat/unified-intake-client` using the `using-git-worktrees` skill. Confirm the original checkout's `frontend/tsconfig.app.tsbuildinfo` remains modified only there and is not copied into the new commit.

- [x] **Step 2: Write failing typed-client and tool tests**

Cover canonical HMAC assertion, one token exchange per create attempt, stable key from `channel:workspace:eventId`, exact payload replay after timeout, 409 digest conflict, no `employeeId`/tenant/requester fields, missing channel identity fail-closed, and ITSM outage returning failure without email-as-success.

```python
@pytest.mark.asyncio
async def test_create_retries_exact_payload_after_response_timeout(fake_http):
    client = ItsmIntakeClient(settings=test_settings(), http=fake_http)
    command = CreateWorkItemCommand(
        idempotencyKey="teams:it-support:message-42",
        intakeKind="incident",
        title="VPN unavailable",
    )
    first = await client.create_from_context(command, verified_context())
    second = await client.create_from_context(command, verified_context())
    assert first.work_item_id == second.work_item_id
    assert fake_http.create_bodies[0] == fake_http.create_bodies[1]
```

- [x] **Step 3: Run focused KAF tests and verify failure**

Run: `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src .venv/bin/python -m pytest tests/test_itsm_intake_client.py tests/test_ticket_create_fallback.py tests/test_graph_send_mail_tool.py -q`

Expected: FAIL because `acp.itsm_intake` does not exist.

- [x] **Step 4: Implement typed contracts and client**

Use Pydantic models with `extra='forbid'`. Read `ITSM_KAF_URL` and new `ITSM_KAF_INTAKE_EXCHANGE_SECRET`; never read `ITSM_KAF_AUTOMATION_TOKEN`. Build assertions from `get_audit_context()` keys `channel`, `identity_provider`, `identity_key_value`, `external_message_id` and `external_conversation_id`; KAF Web uses provider `kaf_user` and stable internal user subject. Propagate the WebSocket frame's client `message_id`/`idempotency_key` into turn context, falling back to the already-created turn trace ID only for a new submission; channel gateways continue to use their verified external message ID. Sign only the canonical assertion, exchange it, then POST the typed command with the short-lived token.

- [x] **Step 5: Replace `ticket_create` behavior**

Remove Gazellio create import and `_send_ticket_create_fallback_mail` from `ticket_create`. The tool no longer accepts an authoritative employee ID. It requires `intake_kind`, `summary`, optional Catalog/CTI/CI/form/incident inputs, obtains identity from audit context, and returns the Go ITSM WorkItem ID/number/replay status. Preserve unrelated Gazellio read/password tools in `src/acp/itsm/`.

- [x] **Step 6: Update structured callers and remove creation fallbacks**

Update escalation/procedure/tool contracts to pass the structured Intake kind already produced by agent/operation metadata. Do not add keyword classification or category-to-Catalog hardcoding. Delete Web Form create, direct `cticode` create and mail-success fallback functions/tests that only served `ticket_create`; email notification remains a separate notification tool.

- [x] **Step 7: Run focused KAF validation**

Run: `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src .venv/bin/python -m pytest tests/test_itsm_intake_client.py tests/test_ticket_create_fallback.py tests/test_graph_send_mail_tool.py tests/test_sr_ticket_preview.py tests/test_incident_ticket_fallback.py -q`

Run: `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src .venv/bin/python -m ruff check src/acp/itsm_intake src/acp/mcp/tools/itsm.py src/acp/orchestration/shared/escalation_subgraph.py tests/test_itsm_intake_client.py`

Run: `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src .venv/bin/python -m ruff format --check src/acp/itsm_intake src/acp/mcp/tools/itsm.py src/acp/orchestration/shared/escalation_subgraph.py tests/test_itsm_intake_client.py`

Expected: all focused tests/lint/format checks PASS.

- [x] **Step 8: Commit the KAF cutover in the KAF worktree**

```bash
git add src/acp/itsm_intake src/acp/config.py src/acp/mcp/tools/itsm.py src/acp/orchestration/shared/escalation_subgraph.py src/acp/chat/turns.py src/acp/routers/chat.py src/acp/tools/governance.py tests scripts/procedures
git commit -m "feat(itsm): create work items through unified intake"
```

Record the KAF commit in the ITSM implementation report; do not vendor or subtree-copy KAF code into this repository.

---

### Task 13: Metrics, End-to-End Evidence and Release Verification

**Files:**
- Create: `itsm-backend/handlers/intake/metrics.go`
- Create: `itsm-backend/handlers/intake/metrics_test.go`
- Create: `itsm-backend/handlers/intake/e2e_test.go`
- Modify: `itsm-backend/database/rls/rls_integration_test.go`
- Create: `docs/reports/2026-08-31-unified-intake-implementation-report.md`
- Modify: `docs/DEVELOPMENT_GUIDE.md`
- Modify: `.env.example` and deployment examples that already document KAF settings

**Interfaces:**
- Produces observable counters/histograms and a reproducible implementation report with exact commits, commands, counts and known limitations.

- [x] **Step 1: Write failing metric and E2E assertions**

Assert metrics for first create, replay, conflict, latency, workflow pending/retry/dead and identity exchange denial. Add real HTTP + PostgreSQL E2E for ITSM access JWT Service Request/Incident, KAF exchange token, exact replay, forged identity rejection and BPMN outage/recovery.

- [x] **Step 2: Run focused E2E and verify missing metrics**

Run: `cd itsm-backend && go test ./handlers/intake -run 'Metrics|E2E' -count=1`

Expected: FAIL until metrics and fixtures are implemented.

- [x] **Step 3: Implement bounded metrics and documentation**

Register labels only for channel, record class and bounded result enums; never label by tenant, user, WorkItem, key or error string. Document identity exchange settings, workflow Outbox settings, migration order, RLS preflight, worker health and manual retry procedure.

- [x] **Step 4: Run ITSM focused and full verification**

Run: `cd itsm-backend && go test ./handlers/intake ./controller ./handlers/service_request ./service ./router ./middleware ./migration ./internal/bootstrap -count=1`

Run: `cd itsm-backend && go test ./... -count=1`

Run: `cd itsm-backend && go build ./...`

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/incident-api.test.ts src/lib/api/__tests__/service-catalog-api.test.ts src/lib/api/__tests__/idempotency-key.test.ts`

Run: `cd itsm-frontend && npm run build`

Expected: zero failures.

- [x] **Step 5: Run mandatory PostgreSQL RLS verification**

Run: `cd itsm-backend && RLS_TEST_DSN="$RLS_TEST_DSN" go test -tags integration_rls -v ./database/rls/... -count=1`

Expected: PASS with zero skips. If `RLS_TEST_DSN` is absent, release acceptance is blocked rather than inferred.

- [x] **Step 6: Run KAF focused and repository-wide verification**

In the KAF worktree run the focused commands from Task 12, followed by:

`ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src .venv/bin/python -m pytest -q`

Report exact passed/failed/skipped/xfailed counts and exit code. Any modified-scope failure blocks completion; pre-existing unrelated repository failures are recorded without describing the suite as green.

- [x] **Step 7: Run Dev real-path acceptance without production writes**

Create one Service Request and one Incident through authenticated APIs, repeat each exact payload/key, verify one WorkItem/extension/Snapshot/Outbox/process per command, stop/restart the workflow worker to prove recovery, and execute one KAF channel assertion/exchange/create flow. Query only IDs/counts and sanitized state. Do not call Graph or KAF PROD.

- [x] **Step 8: Write the implementation report**

Record ITSM/KAF commit IDs, migration heads, commands, exact test counts, E2E IDs, replay results, workflow state transitions, RLS zero-skip evidence, failures and recovery. Do not include JWTs, HMAC secrets, raw assertions, chat text or form secrets.

- [x] **Step 9: Commit metrics, docs and evidence**

```bash
git add itsm-backend/handlers/intake itsm-backend/database/rls/rls_integration_test.go docs/DEVELOPMENT_GUIDE.md docs/reports/2026-08-31-unified-intake-implementation-report.md .env.example
git commit -m "docs(intake): record unified intake verification"
```

- [x] **Step 10: Final branch integrity check**

Run: `git diff --check`

Run: `git status --short`

Run: `git log --oneline --decorate f6b22161..HEAD`

Expected: no staged/modified implementation files, only the pre-existing untracked review evidence; commit history contains the design, this plan and the task commits. Do not merge, push or modify the main worktree without a separate user instruction.
