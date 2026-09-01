# Canonical fresh bootstrap migration contract — report

## Root cause

The registered catalog is post-Ent-schema, but `cmd/migrate -up` ran it without Ent schema creation and `-fresh` recreated an empty database then ran it without Ent schema. On disposable PostgreSQL, original `-up` failed at 007 (`changes` absent); after Ent schema, it reached 009 and failed on obsolete `sla_policies`.

009 itself was stale: two fixed targets (`sla_policies`, `approval_workflows`) are absent, current `sla_definitions` and many direct-tenant tables were omitted, and its `app.current_tenant_id` GUC did not match the runtime RLS boundary’s `app.current_tenant`.

## Implementation

- Added fail-closed shared `migration.RunCanonicalBootstrap`: prepare → Ent schema → ledger/post-schema migrations → optional seed.
- Bootstrap and `cmd/migrate -fresh` use that order. `-up` is explicitly post-schema only.
- `-fresh` is development/test/local-only, requires `ITSM_ALLOW_DESTRUCTIVE_FRESH=true` plus exact `ITSM_FRESH_DATABASE=$DB_NAME`, and quotes the DDL target identifier.
- Deleted the duplicate root destructive `migrate_fresh.go` path; active docs and Makefile now call `cmd/migrate`.
- Rewrote active 009 in place (the local environment has no applied history) as a current-schema, deterministic, identifier-safe direct-tenant catalog reconciler. It creates exactly `tenant_isolation_<table>` with the runtime `app.current_tenant` predicate, rejects invalid/unknown policy shapes, removes old policy forms, and drops obsolete `get_current_tenant_id()`.
- Retired 010 from `RegisteredMigrations` into `LegacyMigrations`; its SQL remains retrievable for historical/checksum inspection. 019’s specialized forced KAF policy is unchanged.
- No 021/022 registration or P1-A authority changes. Tables without direct `tenant_id` (including professional extensions) remain for their future indirect-policy schema wave.

## Files

`migration/bootstrap.go`, `migration/bootstrap_test.go`, `migration/migrations.go`, `migration/migrator_test.go`, `internal/bootstrap/app.go`, `internal/bootstrap/post_schema_migrations_test.go`, `cmd/migrate/main.go`, `cmd/migrate/main_test.go`; deleted `migrate_fresh.go`; updated `Makefile`, `CLAUDE.md`, and migration documentation.

## RED / GREEN

RED:

1. Disposable local PostgreSQL original `go run -tags migrate ./cmd/migrate -up` failed at 007: `pq: relation "changes" does not exist (42P01)`.
2. Ent schema followed by original stream failed at 009: `pq: relation "sla_policies" does not exist (42P01)`.
3. New order/guard tests initially failed because `RunCanonicalBootstrap` and `validateFreshTarget` did not exist; new 009/010 contract tests then failed against stale active definitions.

GREEN:

```bash
go test ./migration ./internal/bootstrap -run 'TestTenantRLSReconcilerUsesTheCurrentSchemaAndRuntimeGUC|TestTicketTypesMigrationIsRetiredFromTheActivePostSchemaStream|TestRunPostSchemaMigrationsAppliesVersion007' -count=1
go test -tags migrate ./cmd/migrate -count=1
```

Both passed.

## Live PostgreSQL

Only local disposable `itsm-postgres-dev` at `127.0.0.1:5432` was used; no shared `192.168.31.66` host or credentials were used. Each generated DB was dropped.

```bash
DEPLOYMENT_MODE=development ITSM_ALLOW_DESTRUCTIVE_FRESH=true ITSM_FRESH_DATABASE="$DB_NAME" DB_HOST=127.0.0.1 ... go run -tags migrate ./cmd/migrate -fresh
```

Fresh applied `007, 008, 009, 011–019`, seeded, and completed. Catalog verification returned `118/118` direct-tenant base tables with enabled, exact-one policies and `0/0` legacy function/pilot policy. A non-owner `SET ROLE` probe allowed same-tenant insert/read (`1`), rejected cross-tenant write, returned `0` for cross-tenant read, and returned `0` with the GUC unset. The second `-up` printed `Applied 0 migration(s)` and `-status` reported `No pending migrations`.

## Verification

```bash
go test ./... -count=1
go build ./...
go build -tags migrate ./cmd/migrate
go vet ./migration
go vet -tags migrate ./cmd/migrate
git diff --check
```

All passed. `go vet ./internal/bootstrap` was also attempted and retains a pre-existing unrelated copylock warning at `internal/bootstrap/ticket_cc_index_migration_test.go:354`.

## Self-review and concerns

No `sla_policies` compatibility table, no `IF EXISTS` skip for the stale target, and no dual GUC/policy path were added. 009 is schema-bound and non-forced; broad forced RLS, role separation, and the RLS-aware Unit of Work remain Phase-2 work. 019 remains the only specialized forced policy in this scope.

## Review round 1 remediation

### Implementation

- `-fresh` now normalizes and explicitly reconfirms its host, port, and database target. It rejects invalid ports, `postgres`, `template0`, `template1`, and the known shared `192.168.31.66` host before opening the maintenance connection. The normalized target is also the one used for the subsequent drop/create/bootstrap.
- `seedTicketTypes` now uses only the current Ent `TicketType` descriptor. The retired `custom_fields` and `approval_chain` writes and the raw-DB/table-exists skip path are gone. `verifyITILTemplates` requires at least 12 tenant ticket types; CLI `-fresh` uses `SeedProduction`, so any missing administrator or ticket-type seed causes the canonical bootstrap to fail.
- `database.InitDatabase` is connection/client construction only. All pgvector, vectors, and AI-feedback DDL lives in `database.PrepareBootstrapInfrastructure`, which returns every error. Both the bootstrap job and CLI fresh invoke it through the same `CanonicalBootstrap.Prepare` phase. The marketplace default and legacy knowledge-author compatibility ALTER paths were deleted.
- Migration catalog validation now fails closed for missing identity, duplicate versions, ordering violations, and active empty SQL. The actual ledger rejects unknown/duplicate versions and every checksum mismatch. The executable stream must be exactly the registered active stream; `ApplyMigration` refuses any legacy, unknown, or non-executable phase instead of silently returning success.

### RED / GREEN evidence

RED was observed with the new focused tests before the implementation: fresh target validation accepted a host/port mismatch and the catalog validator did not exist. The first strict fresh run without `ADMIN_PASSWORD` also stopped at `seed bootstrap data: verify administrator: required production seed is missing`, proving the CLI no longer reports a partial seed as success.

GREEN focused verification:

```bash
go test -tags migrate ./cmd/migrate
go test ./migration ./database ./pkg/seeder ./internal/bootstrap
go vet ./migration ./database ./pkg/seeder
go vet -tags migrate ./cmd/migrate
git diff --check
```

The test commands and applicable vet checks passed. `go vet ./internal/bootstrap` still reports the pre-existing unrelated copylock warning at `internal/bootstrap/ticket_cc_index_migration_test.go:354`.

`go test ./... -count=1`, `go build ./...`, and `go build -tags migrate ./cmd/migrate` also passed after the fix. A separately started broad `-race` command was stopped during its slow unrelated service/controller compilation; it is not used as verification evidence for this migration-focused task.

### Live disposable PostgreSQL

Using only local `127.0.0.1:5432`, a generated disposable database was passed through `-fresh` with the development guard and all three explicit target confirmations. It completed with `12` ticket types, `12` migration-ledger rows (`007,008,009,011-019`), and `118` direct-tenant RLS policies. A second `-up` printed `Applied 0 migration(s)` and status reported no pending migrations. The generated database was dropped after verification. No shared host or credential persistence was used.

An adverse CLI probe set `DB_HOST` and `ITSM_FRESH_HOST` to `192.168.31.66`; it exited `1` with `Refusing fresh bootstrap: -fresh refuses shared host` before any connection or DDL. A separate fresh local database accepted an injected `999_unknown_probe` ledger row only long enough to verify `-status` exited `1` with `migration ledger contains unknown version`; that database was also dropped.

### Concerns

The first live strict-seed attempt intentionally lacked the required production administrator password and failed closed; the succeeding disposable run provided a temporary non-persisted administrator password. Existing `go vet ./internal/bootstrap` copylock noise is outside this change. No 021/022 registration, allocator authority change, compatibility table, or policy dual path was introduced.
