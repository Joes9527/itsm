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
