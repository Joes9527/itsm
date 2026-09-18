#!/usr/bin/env bash
# Clone the ITSM dev database into a dedicated migration database.
#
# Guarantees (see docs/review/2026-09-14-workitem-config-migration-handoff.md):
#   * every database/table identifier is validated before docker or SQL is touched;
#   * an existing target is never reported as success unless it passes completeness
#     verification (marker table + presence and row counts of all verified tables);
#   * a failed or half-finished clone exits non-zero and stays failed on rerun;
#   * source == target is rejected.
#
# Usage: bash scripts/clone_itsm_migration_db.sh [target_db] [source_db]
#
# Environment:
#   PG_CONTAINER          container running PostgreSQL (default itsm-postgres-dev)
#   PG_USER               database role (default itsm_user)
#   RECREATE_INCOMPLETE=1 drop and re-clone a target that fails verification
#                         (default 0: fail closed, never overwrite silently)
set -euo pipefail

CONTAINER="${PG_CONTAINER:-itsm-postgres-dev}"
PG_USER="${PG_USER:-itsm_user}"
TARGET_DB="${1:-itsm_migration_20260914}"
SOURCE_DB="${2:-itsm}"
RECREATE_INCOMPLETE="${RECREATE_INCOMPLETE:-0}"

# Row counts and presence of all of these must match between source and clone.
# A four-table spot check does not prove a complete clone.
VERIFY_TABLES=(
  ticket_categories
  ticket_templates
  field_definitions
  sla_definitions
  service_catalogs
  process_definitions
  process_deployments
  process_bindings
)

# Written only after a fully verified clone so a rerun can prove completeness.
CLONE_MARKER_TABLE="_migration_clone_manifest"

fail() { echo "[FAIL] $*" >&2; exit 1; }
reject() { echo "[FAIL] $*" >&2; exit 2; }
info() { echo "$*"; }

validate_sql_identifier() {
  local label="$1" value="$2"
  if [[ ! "$value" =~ ^[a-z_][a-z0-9_]{0,62}$ ]]; then
    reject "invalid ${label}: '${value}' (expected lowercase [a-z_][a-z0-9_]{0,62})"
  fi
}

validate_container_name() {
  local value="$1"
  if [[ ! "$value" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]]; then
    reject "invalid container name: '${value}'"
  fi
}

# --- validation happens before any docker invocation -------------------------
validate_container_name "$CONTAINER"
validate_sql_identifier "database identifier" "$TARGET_DB"
validate_sql_identifier "database identifier" "$SOURCE_DB"
validate_sql_identifier "database role" "$PG_USER"
if [[ "$TARGET_DB" == "$SOURCE_DB" ]]; then
  reject "target and source databases must differ: '${TARGET_DB}'"
fi
for tbl in "${VERIFY_TABLES[@]}"; do
  validate_sql_identifier "verification table" "$tbl"
done

psql_on() { docker exec "$CONTAINER" psql -U "$PG_USER" -d "$1" -t -A -c "$2"; }

database_exists() {
  local exists
  exists="$(psql_on postgres "select 1 from pg_database where datname='${1}';" 2>/dev/null || true)"
  [[ -n "$exists" ]]
}

# Prints "table=count" per line; returns non-zero if any table is missing/unreadable.
table_counts() {
  local db="$1" out="" tbl count
  for tbl in "${VERIFY_TABLES[@]}"; do
    count="$(psql_on "$db" "select count(*) from ${tbl};" 2>/dev/null || return 1)"
    out+="${tbl}=${count}"$'\n'
  done
  printf '%s' "$out"
}

# Complete == marker recorded this source AND all verified tables exist with the
# same row counts as the current source.
verify_clone() {
  local target="$1" src_counts dst_counts marker_source
  marker_source="$(psql_on "$target" "select source_db from ${CLONE_MARKER_TABLE} order by verified_at desc limit 1;" 2>/dev/null || true)"
  marker_source="${marker_source%%$'\n'*}"
  [[ "$marker_source" == "$SOURCE_DB" ]] || return 1
  src_counts="$(table_counts "$SOURCE_DB")" || return 1
  dst_counts="$(table_counts "$target")" || return 1
  [[ "$src_counts" == "$dst_counts" ]] || return 1
}

ensure_marker() {
  local target="$1" counts="$2"
  psql_on "$target" "create table if not exists ${CLONE_MARKER_TABLE} (source_db text not null, verified_at timestamptz not null default now(), table_counts text);" >/dev/null
  psql_on "$target" "insert into ${CLONE_MARKER_TABLE} (source_db, table_counts) values ('${SOURCE_DB}', '${counts//$'\n'/;}');" >/dev/null
}

clone_target() {
  info "[1/3] creating ${TARGET_DB} from ${SOURCE_DB}"
  if psql_on postgres "CREATE DATABASE ${TARGET_DB} TEMPLATE ${SOURCE_DB};" >/dev/null 2>&1; then
    info "      template clone succeeded"
    return 0
  fi

  info "      template clone failed (source in use); falling back to pg_dump/pg_restore"
  psql_on postgres "CREATE DATABASE ${TARGET_DB};" >/dev/null

  local dump="/tmp/${SOURCE_DB}.dump"
  if ! docker exec "$CONTAINER" sh -c "pg_dump -U ${PG_USER} -Fc ${SOURCE_DB} > ${dump}"; then
    fail "pg_dump failed; target ${TARGET_DB} is incomplete and must not be used"
  fi
  if ! docker exec "$CONTAINER" sh -c "pg_restore -U ${PG_USER} -d ${TARGET_DB} --no-owner ${dump}"; then
    docker exec "$CONTAINER" sh -c "rm -f ${dump}" >/dev/null 2>&1 || true
    fail "pg_restore failed; target ${TARGET_DB} is incomplete and must not be used"
  fi
  docker exec "$CONTAINER" sh -c "rm -f ${dump}" >/dev/null 2>&1 || true
}

main() {
  info "source=${SOURCE_DB} target=${TARGET_DB} container=${CONTAINER}"

  if database_exists "$TARGET_DB"; then
    if verify_clone "$TARGET_DB"; then
      info "[skip] target database ${TARGET_DB} already present and verified; not overwriting"
      exit 0
    fi
    if [[ "$RECREATE_INCOMPLETE" == "1" ]]; then
      info "[recreate] dropping unverified target ${TARGET_DB} (RECREATE_INCOMPLETE=1)"
      psql_on postgres "DROP DATABASE ${TARGET_DB};" >/dev/null
    else
      fail "target database ${TARGET_DB} exists but failed completeness verification (missing marker or table/count mismatch); refusing to report success. Set RECREATE_INCOMPLETE=1 to drop and re-clone."
    fi
  fi

  clone_target

  info "[2/3] recording verification marker"
  local counts
  counts="$(table_counts "$TARGET_DB")" || fail "target ${TARGET_DB} is missing verified tables after clone"
  ensure_marker "$TARGET_DB" "$counts"

  info "[3/3] verifying clone completeness"
  verify_clone "$TARGET_DB" || fail "clone verification failed for ${TARGET_DB}; target must not be used"
  info "[ok] target database ${TARGET_DB} cloned and verified"
}

main "$@"
