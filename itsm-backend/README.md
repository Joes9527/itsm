# ITSM Backend

## Quick Start

1. Prerequisites: Go 1.21+, PostgreSQL 17, pgvector 0.8.6
2. Configure env or `itsm-backend/config.yaml`; optional: `.env` (see project root `.env.example`)
3. Choose deployment mode with `DEPLOYMENT_MODE=private|saas|saas_msp`
3. Install deps and build:

```
make setup
```

1. Run:

```
cd itsm-backend && go run .
```

API base: `http://localhost:8090/api/v1`

## Initialization

- `ITSM_BOOTSTRAP_ONLY=true`: run one-shot migration + seed and exit
- `ITSM_BOOTSTRAP_MODE=fresh|upgrade`: choose a non-destructive empty install or
  an exact cataloged forward upgrade. `fresh` does not drop a database.
- `ITSM_AUTO_MIGRATE=true`: enable schema migration during bootstrap
- `ITSM_AUTO_SEED=true`: enable idempotent seed during bootstrap
- `ITSM_MIGRATION_DB_USER`, `ITSM_RUNTIME_DB_USER`, and
  `ITSM_BOOTSTRAP_DB_USER` are mandatory canonical lowercase PostgreSQL role
  identifiers. Runtime is distinct; bootstrap is the sole declared superuser
  boundary and may equal migration for a locally owned cluster. Init uses the
  migration DSN; API and Worker use only the runtime DSN.

In Docker Compose, the recommended flow is:

1. For an existing volume, run the idempotent `itsm-role-provision` profile.
2. For an empty database only, run one explicit `ITSM_BOOTSTRAP_MODE=fresh`
   init job; the steady default remains `upgrade`.
3. `itsm-backend` starts after init completes; Frontend proxies browser
   requests through same-origin `/api`.

For a standalone database, run init explicitly with the migration principal,
then start API/Worker with the runtime principal. The release preflight requires
PostgreSQL major 17 and exactly pgvector 0.8.6. Never mount a PG15/PG16 data
directory directly under PG17. Preserve the old volume and a verified backup,
then use a controlled `pg_dump`/`pg_restore` into a new PG17 cluster or a supported
`pg_upgrade` procedure before running `ITSM_BOOTSTRAP_MODE=upgrade`.

For browser compatibility on localhost, auth cookies are host-only and only marked
`Secure` when the request is actually served over HTTPS.

## Swagger Docs

After server starts, open:

- Swagger UI: `http://localhost:8090/swagger/index.html`
- OpenAPI JSON: `http://localhost:8090/docs/swagger.json`

Generate docs locally:

```
# Install once
GO111MODULE=on go install github.com/swaggo/swag/cmd/swag@latest
$(go env GOPATH)/bin/swag init -g main.go -o ./docs
```

## Multi-tenancy & Auth

- JWT Bearer in `Authorization`
- `X-Tenant-Code` header supported; tenant id validated against JWT
