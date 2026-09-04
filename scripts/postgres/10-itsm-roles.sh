#!/bin/sh
set -eu

: "${ITSM_MIGRATION_DB_USER:?ITSM_MIGRATION_DB_USER is required}"
: "${ITSM_RUNTIME_DB_USER:?ITSM_RUNTIME_DB_USER is required}"
: "${ITSM_RUNTIME_DB_PASSWORD:?ITSM_RUNTIME_DB_PASSWORD is required}"

case "$ITSM_MIGRATION_DB_USER" in
  *[!A-Za-z0-9_]*|'') echo "invalid migration database role" >&2; exit 1 ;;
esac
case "$ITSM_RUNTIME_DB_USER" in
  *[!A-Za-z0-9_]*|'') echo "invalid runtime database role" >&2; exit 1 ;;
esac
if [ "$ITSM_MIGRATION_DB_USER" != "$POSTGRES_USER" ]; then
  echo "POSTGRES_USER must be the configured migration database role" >&2
  exit 1
fi
if [ "$ITSM_MIGRATION_DB_USER" = "$ITSM_RUNTIME_DB_USER" ]; then
  echo "migration and runtime database roles must be distinct" >&2
  exit 1
fi

psql --set ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=runtime_role="$ITSM_RUNTIME_DB_USER" \
  --set=runtime_password="$ITSM_RUNTIME_DB_PASSWORD" <<'SQL'
SELECT format(
  'CREATE ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'runtime_role', :'runtime_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'runtime_role')
\gexec
SELECT format(
  'ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'runtime_role', :'runtime_password'
)
\gexec
SQL
