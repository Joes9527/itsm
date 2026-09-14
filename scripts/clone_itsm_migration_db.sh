#!/usr/bin/env bash
# 克隆 itsm dev 库为专用迁移库，保证 itsm 测试库不被写入。
# 用法: bash scripts/clone_itsm_migration_db.sh [目标库名] [源库名]
set -euo pipefail

CONTAINER="${PG_CONTAINER:-itsm-postgres-dev}"
PG_USER="${PG_USER:-itsm_user}"
TARGET_DB="${1:-itsm_migration_20260914}"
SOURCE_DB="${2:-itsm}"

psql_on() { docker exec "$CONTAINER" psql -U "$PG_USER" -d "$1" -t -A -c "$2"; }

exists=$(psql_on postgres "select 1 from pg_database where datname='${TARGET_DB}';")
if [ -n "$exists" ]; then
  echo "[skip] 目标库 ${TARGET_DB} 已存在，不覆盖。"
  exit 0
fi

echo "[1/3] 尝试 CREATE DATABASE ... TEMPLATE ${SOURCE_DB}"
if psql_on postgres "CREATE DATABASE ${TARGET_DB} TEMPLATE ${SOURCE_DB};" >/dev/null 2>&1; then
  echo "      模板克隆成功"
else
  echo "      模板克隆失败（源库存在连接），回退 pg_dump/pg_restore"
  psql_on postgres "CREATE DATABASE ${TARGET_DB};" >/dev/null
  docker exec "$CONTAINER" sh -c "pg_dump -U ${PG_USER} -Fc ${SOURCE_DB} > /tmp/${SOURCE_DB}.dump"
  docker exec "$CONTAINER" sh -c "pg_restore -U ${PG_USER} -d ${TARGET_DB} --no-owner /tmp/${SOURCE_DB}.dump"
  docker exec "$CONTAINER" sh -c "rm -f /tmp/${SOURCE_DB}.dump"
fi

echo "[2/3] 校验关键表行数（源 vs 目标）"
for tbl in ticket_categories sla_definitions field_definitions process_definitions; do
  src=$(psql_on "$SOURCE_DB" "select count(*) from ${tbl};")
  dst=$(psql_on "$TARGET_DB" "select count(*) from ${tbl};")
  if [ "$src" != "$dst" ]; then
    echo "  [FAIL] ${tbl}: 源=${src} 目标=${dst}"; exit 1
  fi
  echo "  [OK] ${tbl}: ${dst}"
done

echo "[3/3] 完成。连接串: postgresql://${PG_USER}:***@localhost:5432/${TARGET_DB}"
