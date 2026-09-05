#!/bin/sh
set -eu

: "${ITSM_BOOTSTRAP_DB_USER:?ITSM_BOOTSTRAP_DB_USER is required}"
: "${ITSM_MIGRATION_DB_USER:?ITSM_MIGRATION_DB_USER is required}"
: "${ITSM_MIGRATION_DB_PASSWORD:?ITSM_MIGRATION_DB_PASSWORD is required}"
: "${ITSM_RUNTIME_DB_USER:?ITSM_RUNTIME_DB_USER is required}"
: "${ITSM_RUNTIME_DB_PASSWORD:?ITSM_RUNTIME_DB_PASSWORD is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"

is_canonical_role() {
  case "$1" in
    ''|[0-9]*|*[!a-z0-9_]*) return 1 ;;
  esac
  [ "${#1}" -le 63 ]
}

if ! is_canonical_role "$ITSM_BOOTSTRAP_DB_USER"; then
  echo "invalid bootstrap database role" >&2
  exit 1
fi
if ! is_canonical_role "$ITSM_MIGRATION_DB_USER"; then
  echo "invalid migration database role" >&2
  exit 1
fi
if ! is_canonical_role "$ITSM_RUNTIME_DB_USER"; then
  echo "invalid runtime database role" >&2
  exit 1
fi
if [ "$ITSM_MIGRATION_DB_USER" = "$ITSM_RUNTIME_DB_USER" ]; then
  echo "migration and runtime database roles must be distinct" >&2
  exit 1
fi
if [ "$ITSM_BOOTSTRAP_DB_USER" = "$ITSM_RUNTIME_DB_USER" ]; then
  echo "bootstrap and runtime database roles must be distinct" >&2
  exit 1
fi

bootstrap_matches="$({
  psql --set ON_ERROR_STOP=1 --tuples-only --no-align \
    --username "$ITSM_BOOTSTRAP_DB_USER" --dbname "$POSTGRES_DB" \
    --set=bootstrap_role="$ITSM_BOOTSTRAP_DB_USER" <<'SQL'
SELECT current_user = :'bootstrap_role'
   AND (SELECT rolsuper FROM pg_roles WHERE rolname = :'bootstrap_role');
SQL
} 2>/dev/null)" || {
  echo "bootstrap database role preflight failed" >&2
  exit 1
}
if [ "$bootstrap_matches" != "t" ]; then
  echo "bootstrap database role preflight failed" >&2
  exit 1
fi

psql --set ON_ERROR_STOP=1 \
  --username "$ITSM_BOOTSTRAP_DB_USER" --dbname "$POSTGRES_DB" \
  --set=bootstrap_role="$ITSM_BOOTSTRAP_DB_USER" \
  --set=migration_role="$ITSM_MIGRATION_DB_USER" \
  --set=migration_password="$ITSM_MIGRATION_DB_PASSWORD" \
  --set=runtime_role="$ITSM_RUNTIME_DB_USER" \
  --set=runtime_password="$ITSM_RUNTIME_DB_PASSWORD" <<'SQL'
SELECT format(
  'CREATE ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'migration_role', :'migration_password'
)
WHERE :'migration_role' <> :'bootstrap_role'
  AND NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'migration_role')
\gexec
SELECT format(
  'ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'migration_role', :'migration_password'
)
WHERE :'migration_role' <> :'bootstrap_role'
\gexec
SELECT format(
  'CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'runtime_role', :'runtime_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'runtime_role')
\gexec
SELECT format(
  'ALTER ROLE %I LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L',
  :'runtime_role', :'runtime_password'
)
\gexec
SQL
