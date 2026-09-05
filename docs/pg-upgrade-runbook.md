# PostgreSQL Upgrade Runbook

## Overview

This document defines the evidence-preserving upgrade procedure from any older
supported deployment (including PG15/PG16) to the release contract:
**PostgreSQL major 17 plus pgvector 0.8.6**.

> Never start the PG17 image against an older-major data directory or Docker
> volume. Keep the old volume immutable until the new cluster passes restore,
> schema, row-count, extension-version, and application-readiness checks.

**Current Production Version:** PostgreSQL 17.10 (Debian 17.10-1.pgdg12+1)
**Previous Version:** PostgreSQL 16.x
**Upgrade Date:** 2026-07 (documented post-upgrade)

---

## Upgrade Overview

PostgreSQL major version upgrades require careful planning:

1. **Pre-upgrade validation** - Verify current state and create backup
2. **Schema compatibility check** - Detect incompatibilities
3. **Controlled migration** - use dump/restore into a new cluster or `pg_upgrade`
4. **Post-upgrade validation** - Verify data integrity and performance

---

## Pre-Upgrade Checklist

### 1. Backup Database

```bash
# Full backup before upgrade
./scripts/backup.sh full

# Verify backup
./scripts/backup.sh verify /var/backups/itsm/itsm_full_YYYYMMDD_HHMMSS.dump
```

### 2. Check Current Version

```sql
-- Login to PostgreSQL
psql -U postgres -d itsm -c "SELECT version();"
-- Expected: PostgreSQL 16.x

-- Check active connections
psql -U postgres -c "SELECT count(*) FROM pg_stat_activity WHERE datname = 'itsm';"
```

### 3. Verify No Replication Lag

```sql
-- Check replication status (if applicable)
SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn
FROM pg_stat_replication;
```

### 4. Install PostgreSQL 17

```bash
# Add PostgreSQL GPG key
curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | sudo gpg --dearmor -o /usr/share/keyrings/postgresql-keyring.gpg

# Add repository (Debian 12)
echo "deb [signed-by=/usr/share/keyrings/postgresql-keyring.gpg] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" \
    | sudo tee /etc/apt/sources.list.d/pgdg.list

# Update and install PostgreSQL 17
sudo apt-get update
sudo apt-get install -y postgresql-17
```

Install pgvector 0.8.6 for PostgreSQL 17 and verify it appears in
`pg_available_extension_versions` before any ITSM bootstrap.

Before stopping the old cluster, record the installed extension version, vector
row count, vector index definitions/validity, and a representative similarity
query result. Store these beside the dump checksum; they are the comparison and
rollback evidence, not optional diagnostics.

## Alternative: controlled dump/restore

Create a new empty PG17 cluster rather than reusing the old data directory.
Run `pg_dump -Fc` with the old-version client, retain its checksum and source
cluster metadata, restore with `pg_restore --exit-on-error`, and compare database
objects and recorded row counts. Keep both the dump and old volume until sign-off.

---

## Upgrade Procedure (pg_upgrade)

### 1. Stop PostgreSQL

```bash
sudo systemctl stop postgresql
sudo systemctl stop postgresql@16-main  # If using custom cluster name
```

### 2. Run pg_upgrade

```bash
# Create upgrade directory
sudo -u postgres mkdir -p /var/lib/postgresql/17_upgrade

# Run pg_upgrade (binary upgrade mode)
sudo -u postgres pg_upgrade \
    --old-datadir=/var/lib/postgresql/16/main \
    --new-datadir=/var/lib/postgresql/17/main \
    --old-bindir=/usr/lib/postgresql/16/bin \
    --new-bindir=/usr/lib/postgresql/17/bin \
    --old-options="-c config_file=/etc/postgresql/16/main/postgresql.conf" \
    --new-options="-c config_file=/etc/postgresql/17/main/postgresql.conf" \
    --clone \
    --jobs=4
```

Options explained:
- `--clone` (where supported) preserves the old cluster independently. If clone
  mode is unavailable, use the default copy mode. Do not use `--link` for this
  evidence-preserving procedure because starting the new cluster can make the
  old cluster unsafe to reuse.
- `--jobs=4`: Uses 4 parallel jobs for faster upgrade

### 3. Start PostgreSQL 17

```bash
sudo systemctl start postgresql@17-main
# Or on systems with single cluster:
sudo systemctl start postgresql
```

### 4. Upgrade the pgvector extension catalog

Installing the PG17 pgvector package does not update an extension already
recorded in the restored catalog. Before ITSM bootstrap, first prove that the
installed source version has a supported update path:

```sql
SELECT source, target, path
FROM pg_extension_update_paths('vector')
WHERE source = (SELECT extversion FROM pg_extension WHERE extname = 'vector')
  AND target = '0.8.6';
```

If the installed version is not already 0.8.6 and this query returns no usable
path, stop and restore; do not drop/recreate the extension around live vector
data. With a verified backup and update path, execute the controlled catalog
upgrade as the extension owner:

```sql
BEGIN;
ALTER EXTENSION vector UPDATE TO '0.8.6';
SELECT extversion = '0.8.6' AS exact_version
FROM pg_extension WHERE extname = 'vector';
COMMIT;
```

### 5. Run Vacuum/Analyze

```bash
# As postgres user, run vacuumdb to update statistics
sudo -u postgres vacuumdb --all --analyze-in-stages

# Check for bloated tables
sudo -u postgres vacuumdb --all --analyze 2>&1 | tail -20
```

---

## Post-Upgrade Validation

### 1. Verify Version

```sql
psql -U postgres -d itsm -c "SELECT version();"
-- Should show: PostgreSQL 17.10

SELECT version FROM pg_available_extension_versions
WHERE name = 'vector' AND version = '0.8.6';
SELECT extversion FROM pg_extension WHERE extname = 'vector';
-- Both release checks must resolve to 0.8.6.

-- Every vector-backed index must remain valid and ready; compare indexdef with
-- the captured pre-upgrade inventory.
SELECT n.nspname, t.relname AS table_name, i.relname AS index_name,
       x.indisvalid, x.indisready, pg_get_indexdef(i.oid)
FROM pg_index x
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE pg_get_indexdef(i.oid) ~ '(vector_| hnsw| ivfflat)';

-- Compare this count with the signed pre-upgrade record and prove stored
-- embeddings remain readable by the operator selected sample.
SELECT count(*) AS vector_rows,
       count(*) FILTER (WHERE embedding IS NOT NULL) AS populated_embeddings
FROM public.vectors;
SELECT id, embedding <=> embedding AS self_distance
FROM public.vectors WHERE embedding IS NOT NULL ORDER BY id LIMIT 10;
```

### 2. Check Data Integrity

```sql
-- Check for corrupt tables
SELECT schemaname, tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size
FROM pg_tables
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC LIMIT 20;

-- Verify row counts match expected values
-- (Compare with pre-upgrade counts if available)
```

### 3. Test Application Connectivity

```bash
# Test backend connectivity
curl -s http://localhost:8090/api/v1/health | jq .

# Test database queries
psql -U postgres -d itsm -c "SELECT COUNT(*) FROM users;"
psql -U postgres -d itsm -c "SELECT COUNT(*) FROM tenant;"
```

### 4. Check for Deprecation Warnings

```bash
# Check PostgreSQL logs for warnings
sudo journalctl -u postgresql@17-main --since "1 hour ago" | grep -i warning
sudo tail -100 /var/log/postgresql/postgresql-17-main.log | grep -i warning
```

---

## Rollback Procedure

If the upgrade fails or critical issues are found:

Do not attempt an in-place pgvector downgrade. Stop the PG17 cluster, preserve
its logs and failed validation output, and restore the old immutable cluster or
the verified backup. Re-run the recorded row-count, vector-index, and sample
similarity checks before returning traffic; the old volume remains the rollback
authority until sign-off.

### 1. Stop PostgreSQL 17

```bash
sudo systemctl stop postgresql@17-main
```

### 2. Restore from Backup

```bash
# Use the restore script
./scripts/restore.sh full /var/backups/itsm/itsm_full_YYYYMMDD_HHMMSS.dump
```

### 3. Reinstall PostgreSQL 16

```bash
sudo apt-get install -y postgresql-16
sudo systemctl start postgresql@16-main
```

---

## Known Issues / Notes

### 1. RLS Behavior Changes
PostgreSQL 17 maintains backward compatibility with RLS policies. No action required.

### 2. Monitoring Adjustments
Update any monitoring queries that reference deprecated system views if applicable.

### 3. Connection Pooling
If using PgBouncer or pgpool, verify compatibility with PostgreSQL 17.

---

## CI/CD Integration

This upgrade runbook is validated by the `pg-disaster-recovery.yml` workflow which:
- Runs weekly backup/restore drills
- Validates RLS enforcement after upgrades
- Tests fencing token fault injection

---

## Sign-Off

| Role | Name | Date | Signature |
|------|------|------|-----------|
| DBA Lead | | | |
| Platform Lead | | | |
| Security | | | |
