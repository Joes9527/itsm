# SSLVPN WS1 Migration Lineage and Fresh Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Plan Status:** Review revisions pending approval; do not execute yet.

**Goal:** Make ITSM upgrade history byte-immutable, give fresh installs a current baseline, and make readiness depend on a verifiable release manifest.

**Architecture:** Keep one executable forward-migration stream and one validation-only historical lineage manifest. Fresh databases use Ent plus current baseline assets; existing databases use immutable forward migrations. Both converge on shared invariants and singleton `schema_state`, writable only by the migration principal.

**Tech Stack:** Go 1.24, PostgreSQL, Ent, embedded JSON/SQL, Testify, Docker Compose

**Spec:** `docs/superpowers/specs/2026-09-04-sslvpn-delegation-reliability-hardening-design.md`

## Global Constraints

- Follow repository `AGENTS.md`; ITSM remains the tenant/RBAC/workflow/audit authority.
- One ledger version has exactly one published checksum; no checksum list or runtime alias.
- Historical `015_add_service_request_contact_fields` is validation-only; canonical `016` is the only executable path.
- Fresh install must not forge `schema_migrations` history.
- Runtime roles have SELECT but never INSERT/UPDATE/DELETE on `schema_state`.
- PostgreSQL is mandatory evidence for RLS, locks, constraints, identity, and recovery.
- Preserve unrelated worktree changes. Replace the inherited `HistoricalChecksums` draft; do not extend it.

---

## File Structure

- Create `itsm-backend/migration/lineage_manifest.json` and `lineage.go`: immutable provenance plus exact validation.
- Create `itsm-backend/migration/release_manifest.go` and `schema_state.go`: deterministic release identity and singleton state.
- Create `itsm-backend/migration/schema_state_privileges.go`: controlled, identifier-safe role provisioning outside generic migration SQL.
- Create `itsm-backend/migration/baseline.go`, `invariants.go`, and `sql/baseline/current.sql`: fresh path and shared checks.
- Modify `itsm-backend/migration/migrations.go`, `migrator.go`, and `bootstrap.go`: restore history and split fresh/upgrade paths.
- Modify `itsm-backend/cmd/migrate/main.go` and `internal/bootstrap/app.go`: call the correct entry point.
- Modify `itsm-backend/router/initialization_readiness.go`: consume the manifest until WS2 extracts it from router.
- Modify production Compose and operations documentation for role separation.

### Task 1: Replace checksum tolerance with immutable lineage

**Files:**
- Create: `itsm-backend/migration/lineage_manifest.json`
- Create: `itsm-backend/migration/lineage.go`
- Create: `itsm-backend/migration/lineage_test.go`
- Modify: `itsm-backend/migration/migrator.go`
- Modify: `itsm-backend/migration/migrator_test.go`

**Interfaces:**
- Produces: `LoadLineageManifest() (LineageManifest, error)` and `PublishedLineage(version string) (LineageEntry, bool)`.
- Produces: `ValidateLedgerLineage(applied []Migration) error`.
- Consumed by: `Migrator.GetPendingMigrations` before pending selection.

- [ ] **Step 1: Inspect and preserve inherited evidence**

Run:

```bash
git status --short
git diff -- itsm-backend/migration/migrations.go itsm-backend/migration/migrator.go itsm-backend/migration/migrator_test.go
```

Expected: `HistoricalChecksums` is present only as an uncommitted draft; do not commit that model.

- [ ] **Step 2: Write failing manifest tests**

```go
func TestPublishedLineageHasOneChecksumPerVersion(t *testing.T) {
    manifest, err := LoadLineageManifest()
    require.NoError(t, err)
    seen := map[string]bool{}
    for _, entry := range manifest.Entries {
        require.False(t, seen[entry.LedgerVersion])
        require.Regexp(t, `^[0-9a-f]{64}$`, entry.SQLSHA256)
        seen[entry.LedgerVersion] = true
    }
}

func TestHistoricalContact015IsValidationOnly(t *testing.T) {
    entry, ok := PublishedLineage("015_add_service_request_contact_fields")
    require.True(t, ok)
    require.False(t, entry.Executable)
    require.Equal(t, "016_add_service_request_contact_fields", entry.ForwardMigration)
}
```

- [ ] **Step 3: Verify the tests fail**

Run: `cd itsm-backend && go test ./migration -run 'PublishedLineage|HistoricalContact' -count=1`

Expected: FAIL because the manifest API is absent.

- [ ] **Step 4: Add the manifest and loader**

Use the exact four critical rows from the approved design. Resolve remaining published versions from Git using:

```bash
lineage_commit=$(git log --all -S'Version:     "008_add_initialization_ledger"' --format='%H' -- itsm-backend/migration/migrations.go | tail -1)
test -n "$lineage_commit"
git rev-parse "$lineage_commit":itsm-backend/migration/migrations.go
git show "$lineage_commit":itsm-backend/migration/migrations.go
```

Every JSON row has this complete shape; there are no alternate checksums:

```json
{
  "ledgerVersion": "015_add_service_request_contact_fields",
  "logicalFile": "itsm-backend/migration/migrations.go",
  "gitCommit": "6e52278393896d5dc13f6a60ee83c0ae00073c8f",
  "gitBlob": "b08cce42de3482d9a9c0d90af55ba4ec87b9703b",
  "sqlSha256": "917e74af4aca87b2f40370239ed41c61c847b9c32588ffc8680ecaaad73a0b67",
  "catalog": "historical_validation_only",
  "executable": false,
  "forwardMigration": "016_add_service_request_contact_fields"
}
```

Embed with `//go:embed lineage_manifest.json`; reject missing fields, duplicate versions, malformed hashes, and executable validation-only rows.

- [ ] **Step 5: Delete multi-checksum tolerance**

Remove `Migration.HistoricalChecksums`, `containsChecksum`, and `slices.Equal`. Validate an applied row only against `PublishedLineage(version).SQLSHA256`. Executable selection remains exclusively `RegisteredMigrations`.

- [ ] **Step 6: Run and commit**

```bash
cd itsm-backend
go test ./migration -run 'Lineage|Ledger|Catalog|HistoricalContact' -count=1
git add migration/lineage_manifest.json migration/lineage.go migration/lineage_test.go migration/migrator.go migration/migrator_test.go
git commit -m "fix(migration): enforce immutable published lineage"
```

Expected: PASS and a commit containing no report or unrelated migration draft files.

### Task 2: Restore published bytes and add forward repairs

**Files:**
- Modify: `itsm-backend/migration/migrations.go`
- Modify: `itsm-backend/migration/migrator_test.go`
- Test: `itsm-backend/migration/professional_extension_migration_integration_test.go`

**Interfaces:**
- Consumes: Task 1 lineage checksums.
- Produces: forward-only `023_reconcile_change_execution_tenants` and `024_reconcile_current_rls_policies`, after confirming those versions are still free.

- [ ] **Step 1: Extract exact historical sources read-only**

```bash
git show a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b:itsm-backend/migration/migrations.go | rg -n '007_add_change_execution_tables|case "007' -A180
git show ef7b16a644d94b7fe53e3fce7b519350de070180:itsm-backend/migration/migrations.go | rg -n '009_enable_rls_tenant_isolation|case "009' -A220
git show 5898e2244d1534e588b4659dc867bdecc7e1deb8:itsm-backend/migration/migrations.go | rg -n '015_process_instance_running_unique_guard|case "015' -A80
```

Expected hashes are `1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a`, `b88712993b527f72c945e506fecbb41da54e2aeada19317dff9bc489a94ecea0`, and `624c72f3fc88b299570f556742959bc1e436574881ee080b3dcd85864d1049f6`.

- [ ] **Step 2: Write failing forward-repair tests**

```go
func TestCurrentRLSRepairUsesWorkItemTenantAuthority(t *testing.T) {
    sql := GetMigrationSQL("024_reconcile_current_rls_policies")
    require.Contains(t, sql, "work_item.tenant_id")
    require.Contains(t, sql, "current_setting('app.current_tenant', true)")
    require.NotContains(t, sql, "changes.tenant_id")
}
```

- [ ] **Step 3: Restore and verify immutable SQL**

Apply the displayed 007, 009, and process-instance 015 SQL with `apply_patch`. Do not improve comments or formatting. Add an exact checksum assertion for each.

- [ ] **Step 4: Complete forward-only repairs**

Keep 023’s six updates deriving tenant from `changes.work_item_id -> tickets.tenant_id`. Add 024 to recreate current RLS policies and verify their predicates. Do not edit 007/009 to carry new semantics.

- [ ] **Step 5: Run and commit**

```bash
cd itsm-backend
go test ./migration -run 'PublishedMigration|ChangeExecutionTenant|CurrentRLS|ProfessionalExtension' -count=1
git add migration/migrations.go migration/migrator_test.go migration/professional_extension_migration_integration_test.go
git commit -m "fix(migration): restore history and add forward tenant repairs"
```

### Task 3: Add release manifest and singleton schema state

**Files:**
- Create: `itsm-backend/migration/release_manifest.go`
- Create: `itsm-backend/migration/release_manifest_test.go`
- Create: `itsm-backend/migration/schema_state.go`
- Create: `itsm-backend/migration/schema_state_test.go`
- Create: `itsm-backend/migration/schema_state_privileges.go`
- Create: `itsm-backend/migration/schema_state_privileges_test.go`
- Create: `itsm-backend/migration/schema_state_privileges_integration_test.go`
- Modify: `itsm-backend/migration/migrations.go`
- Modify: `itsm-backend/cmd/migrate/main.go`

**Interfaces:**
- Produces: `CurrentRelease() ReleaseManifest`, `ReleaseManifest.Checksum() (string, error)`.
- Produces: `ReadSchemaState(context.Context, DBTX)`, `PromoteSchemaState(context.Context, DBTX, ReleaseManifest)`, and `VerifySchemaState(SchemaState, ReleaseManifest)`.
- Produces: `LoadSchemaStateRoles(getenv func(string) string) (SchemaStateRoles, error)` and `ApplySchemaStatePrivileges(context.Context, *sql.DB, SchemaStateRoles) error`.
- Produces: forward migration `025_schema_release_state`; `CurrentRelease().SchemaVersion` is `025_schema_release_state`.

- [ ] **Step 1: Write failing checksum/state tests**

```go
func TestReleaseManifestChecksumIsStable(t *testing.T) {
    first, err := CurrentRelease().Checksum()
    require.NoError(t, err)
    second, err := CurrentRelease().Checksum()
    require.NoError(t, err)
    require.Equal(t, first, second)
    require.Regexp(t, `^[0-9a-f]{64}$`, first)
}
```

Also test ID 2 rejection and every release/schema/baseline/checksum mismatch.

- [ ] **Step 2: Implement canonical manifest hashing**

```go
type ReleaseManifest struct {
    ReleaseID            string          `json:"releaseId"`
    SchemaVersion        string          `json:"schemaVersion"`
    BaselineVersion      string          `json:"baselineVersion"`
    EntSchemaFingerprint string          `json:"entSchemaFingerprint"`
    Assets               []ReleaseAsset  `json:"assets"`
    SeedComponents       []SeedComponent `json:"seedComponents"`
}
```

Sort assets/components before canonical JSON hashing. Source seed names/version from `seeder.ProductionComponentNames` and `seeder.CurrentTenantTemplateVersion`.

- [ ] **Step 3: Implement singleton state without dynamic DCL**

```sql
CREATE TABLE IF NOT EXISTS schema_state (
  id smallint PRIMARY KEY CHECK (id = 1),
  release_id varchar(128) NOT NULL,
  schema_version varchar(255) NOT NULL,
  baseline_version varchar(64) NOT NULL,
  release_manifest_checksum char(64) NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

Migration `025_schema_release_state` contains only deterministic table/index DDL. It does not interpolate role names and does not grant to a generic or hardcoded runtime role. `PromoteSchemaState` runs only after invariants.

- [ ] **Step 4: Implement the controlled privilege-provisioning interface**

`cmd/migrate` loads `ITSM_MIGRATION_DB_USER` and `ITSM_RUNTIME_DB_USER` from its secret-backed environment. Reject empty values and equal values before opening the provisioning phase. Query `current_user` and the `schema_state` owner; both must equal the configured migration role. Errors identify only `migration role` or `runtime role`, never a username, DSN, generated SQL, or environment value.

```go
type SchemaStateRoles struct {
    MigrationRole string
    RuntimeRole   string
}

func LoadSchemaStateRoles(getenv func(string) string) (SchemaStateRoles, error)
func ApplySchemaStatePrivileges(ctx context.Context, db *sql.DB, roles SchemaStateRoles) error
```

Build the narrowly scoped `REVOKE INSERT, UPDATE, DELETE` and `GRANT SELECT` statements with `pq.QuoteIdentifier`; do not use string replacement or bind parameters for identifiers. The migration role creates and owns `schema_state`. Invoke this provisioning step after migration 025 exists and before schema-state promotion.

- [ ] **Step 5: Prove privileges with two real PostgreSQL roles**

The integration test creates unique migration/runtime roles and a dedicated test database, runs migration 025 and privilege provisioning as the migration role, then reconnects as the runtime role. Assert `SELECT` succeeds and `INSERT`, `UPDATE`, and `DELETE` each fail with insufficient privilege. Also assert an empty role, identical roles, wrong current executor, and wrong table owner fail closed with sanitized errors.

- [ ] **Step 6: Run and commit**

```bash
cd itsm-backend
go test ./migration -run 'ReleaseManifest|SchemaState|Privileges' -count=1
go test ./migration -run 'Postgres.*SchemaStatePrivileges' -count=1
git add migration/release_manifest.go migration/release_manifest_test.go migration/schema_state.go migration/schema_state_test.go migration/schema_state_privileges.go migration/schema_state_privileges_test.go migration/schema_state_privileges_integration_test.go migration/migrations.go cmd/migrate/main.go
git commit -m "feat(migration): record verified schema release state"
```

### Task 4: Split fresh baseline from upgrades

**Files:**
- Create: `itsm-backend/migration/sql/baseline/current.sql`
- Create: `itsm-backend/migration/baseline.go`
- Create: `itsm-backend/migration/invariants.go`
- Create: `itsm-backend/migration/baseline_integration_test.go`
- Modify: `itsm-backend/migration/bootstrap.go`
- Modify: `itsm-backend/migration/bootstrap_test.go`
- Modify: `itsm-backend/cmd/migrate/main.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`

**Interfaces:**
- Produces: `RunFreshBootstrap(context.Context, FreshBootstrap) error`.
- Produces: `RunUpgrade(context.Context, UpgradeBootstrap) error`.
- Produces: `VerifyCurrentSchema(context.Context, DBTX, ReleaseManifest) error`.
- Removes: `RunCanonicalBootstrap` after all callers migrate.

- [ ] **Step 1: Write failing trace tests**

Assert exact traces:

```text
fresh: lock, prepare, ent-schema, baseline, invariants, promote-state, seed
upgrade: lock, validate-lineage, forward-migrations, invariants, promote-state
restart-after-forward-commit: validate-history, invariants, promote-state
```

Assert fresh databases contain no forged historical rows.

- [ ] **Step 2: Implement re-entrant baseline and invariants**

The embedded baseline contains current non-Ent indexes, policies, constraints, triggers, and initialization infrastructure. Each operation is followed by catalog verification; an existing object with the wrong definition fails closed.

- [ ] **Step 3: Implement advisory-lock orchestration**

Acquire a fixed application-specific PostgreSQL advisory lock on one dedicated connection for the full run. On restart, validate committed history and component attempts, rerun uncommitted steps, and promote state only after final invariants.

- [ ] **Step 4: Replace old callers and delete old API**

`cmd/migrate fresh/reset` calls `RunFreshBootstrap`; upgrade/bootstrap calls `RunUpgrade`. Production API/Worker keep auto-migrate disabled.

Run: `rg -n 'RunCanonicalBootstrap' itsm-backend`

Expected: no matches.

- [ ] **Step 5: Run and commit**

```bash
cd itsm-backend
go test ./migration ./internal/bootstrap -count=1
go test ./migration -run 'Postgres.*(Fresh|Upgrade|RLS|Interruption)' -count=1
git add migration cmd/migrate/main.go internal/bootstrap/app.go
git commit -m "refactor(migration): separate fresh baseline from upgrades"
```

### Task 5: Wire manifest-aware readiness and release evidence

**Files:**
- Modify: `itsm-backend/router/initialization_readiness.go`
- Modify: `itsm-backend/router/router_test.go`
- Modify: `docker-compose.prod.yml`
- Modify: `docs/DEVELOPMENT_GUIDE.md`
- Modify: `docs/dev-commands-reference.md`
- Modify: `docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md`

**Interfaces:**
- Consumes: Task 3 state APIs.
- Produces: readiness fields `releaseId`, `schemaVersion`, `baselineVersion`, `releaseManifestChecksum`.

- [ ] **Step 1: Write failing readiness tests**

Cover missing state, every mismatched field, incomplete platform components, and exact success. Reasons are bounded and contain no DSN/SQL.

- [ ] **Step 2: Replace the last-migration-row check**

Use `ReadSchemaState` plus exact manifest verification, then verify all production initialization components. WS2 will move this Gin-independent logic into a shared package.

- [ ] **Step 3: Verify production role separation**

Keep migration on `${ITSM_MIGRATION_DB_USER}` and API/Worker on `${ITSM_RUNTIME_DB_USER}`. Compose must pass both role identifiers to the migration job, validate they are non-empty and different, and use `${ITSM_MIGRATION_DB_USER}` as the migration connection user. Against the controlled database, `SELECT` from `schema_state` as runtime must succeed; `INSERT`, `UPDATE`, and `DELETE` must each return permission denied. Record only role categories and SQLSTATE, never identifiers or connection details.

- [ ] **Step 4: Run the WS1 gate**

```bash
cd itsm-backend
go test ./migration ./internal/initialization ./internal/bootstrap ./router ./pkg/seeder -count=1
go test ./migration -run 'Postgres.*(Fresh|Upgrade|RLS|Interruption)' -count=1
go vet ./migration ./internal/initialization ./internal/bootstrap ./router
cd ..
docker compose -f docker-compose.prod.yml config --no-interpolate
git diff --check
```

Expected: all checks pass. Run only migration status/dry-run read operations until the database owner separately approves upgrade writes.

- [ ] **Step 5: Record and commit evidence**

Record commit IDs, release checksum, PostgreSQL output, dry-run pending list, and runtime-role denial in the report. Keep `Conditional No-Go` while WS2–WS4 remain open.

```bash
git add itsm-backend/router/initialization_readiness.go itsm-backend/router/router_test.go docker-compose.prod.yml docs/DEVELOPMENT_GUIDE.md docs/dev-commands-reference.md docs/reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md
git commit -m "docs: record SSLVPN migration hardening evidence"
```
