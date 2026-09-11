package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"github.com/lib/pq"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Migration represents a single database migration
type Migration struct {
	Version         string
	Description     string
	AppliedAt       *time.Time
	RollbackSQL     string
	Checksum        string
	ExecutionMS     int64
	ReleaseVersion  string
	CatalogRevision *string
	EvidenceDigest  *string
}

// Migrator handles database migrations
type Migrator struct {
	db             *sql.DB
	logger         *zap.SugaredLogger
	releaseVersion string
	controlConfig  MigrationControlConfig
}

// NewMigrator creates a new Migrator instance
func NewMigrator(db *sql.DB, logger *zap.SugaredLogger, controlConfig ...MigrationControlConfig) *Migrator {
	releaseVersion := os.Getenv("ITSM_RELEASE_VERSION")
	if releaseVersion == "" {
		releaseVersion = "unversioned"
	}
	m := &Migrator{db: db, logger: logger, releaseVersion: releaseVersion}
	if len(controlConfig) == 1 {
		m.controlConfig = controlConfig[0]
	}
	return m
}

// EnsureMigrationsTable creates the migrations tracking table if it doesn't exist
func (m *Migrator) EnsureMigrationsTable(ctx context.Context) error {
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = inspectMigrationTarget(ctx, tx); err != nil {
			return err
		}
		query := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) PRIMARY KEY,
		description TEXT NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		rollback_sql TEXT
	)`
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum VARCHAR(128) NOT NULL DEFAULT '';
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS execution_ms BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS release_version VARCHAR(64) NOT NULL DEFAULT '';
 ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS catalog_revision TEXT;
 ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS evidence_digest TEXT;
	`)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// GetAppliedMigrations returns all applied migrations sorted by version
func (m *Migrator) GetAppliedMigrations(ctx context.Context) ([]Migration, error) {
	schema, err := migrationTargetSchema(ctx, m.db)
	if err != nil {
		return nil, err
	}
	return readMigrationLedger(ctx, m.db, schema)
}

// GetPendingMigrations returns migrations that haven't been applied yet
func (m *Migrator) GetPendingMigrations(ctx context.Context, available []Migration) ([]Migration, error) {
	if err := validateMigrationCatalog(RegisteredMigrations, LegacyMigrations, GetMigrationSQL); err != nil {
		return nil, fmt.Errorf("validate migration catalog: %w", err)
	}
	if err := validateAvailableMigrations(available); err != nil {
		return nil, err
	}
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}

	if err := validateMigrationLedger(applied); err != nil {
		return nil, err
	}
	appliedVersions := make(map[string]bool)
	for _, mig := range applied {
		appliedVersions[mig.Version] = true
	}

	var pending []Migration
	for _, mig := range available {
		if !appliedVersions[mig.Version] {
			pending = append(pending, mig)
		}
	}
	return pending, nil
}

// ApplyMigration applies a single migration
func (m *Migrator) ApplyMigration(ctx context.Context, mig Migration) error {
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		if err := validateActiveMigration(mig); err != nil {
			return err
		}
		sql := GetMigrationSQL(mig.Version)

		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()
		applied, err := inspectMigrationTarget(ctx, tx)
		if err != nil {
			return err
		}
		if err = validateMigrationLedger(applied); err != nil {
			return err
		}
		if err := blockAutomaticWorkItemRetirement(ctx, tx, mig.Version); err != nil {
			return err
		}

		seen := map[string]bool{}
		for _, a := range applied {
			seen[a.Version] = true
		}
		for _, registered := range RegisteredMigrations {
			if !seen[registered.Version] {
				if registered.Version != mig.Version {
					return fmt.Errorf("apply requires next registered migration %s", registered.Version)
				}
				break
			}
		}
		if seen[mig.Version] {
			return fmt.Errorf("migration %s is already applied", mig.Version)
		}

		m.logger.Infow("Applying migration", "version", mig.Version, "description", mig.Description)

		started := time.Now()
		// Execute migration SQL
		if _, err := tx.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("failed to execute migration SQL: %w", err)
		}

		// Record migration
		executionMS := time.Since(started).Milliseconds()
		_, err = tx.ExecContext(ctx, `
		INSERT INTO schema_migrations
			(version, description, applied_at, rollback_sql, checksum, execution_ms, release_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, mig.Version, mig.Description, time.Now(), mig.RollbackSQL,
			checksumSQL(sql), executionMS, m.releaseVersion)
		if err != nil {
			return fmt.Errorf("failed to record migration: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		m.logger.Infow("Migration applied successfully", "version", mig.Version)
		return nil
	})
}

func validateMigrationCatalog(active, legacy []Migration, sqlForVersion func(string) string) error {
	if sqlForVersion == nil {
		return fmt.Errorf("migration SQL resolver is required")
	}
	seen := make(map[string]string, len(active)+len(legacy))
	validateSet := func(kind string, migrations []Migration, requireSQL bool) error {
		previous := ""
		for _, migration := range migrations {
			if strings.TrimSpace(migration.Version) == "" || strings.TrimSpace(migration.Description) == "" {
				return fmt.Errorf("%s migration must have version and description", kind)
			}
			if previousKind, exists := seen[migration.Version]; exists {
				return fmt.Errorf("duplicate migration version %q in %s and %s catalogs", migration.Version, previousKind, kind)
			}
			if previous != "" && migration.Version <= previous {
				return fmt.Errorf("%s migrations must be strictly ordered: %q follows %q", kind, migration.Version, previous)
			}
			if requireSQL && strings.TrimSpace(sqlForVersion(migration.Version)) == "" {
				return fmt.Errorf("active migration %q has empty SQL", migration.Version)
			}
			seen[migration.Version] = kind
			previous = migration.Version
		}
		return nil
	}
	if err := validateSet("legacy", legacy, false); err != nil {
		return err
	}
	return validateSet("active", active, true)
}

func allKnownMigrations() map[string]Migration {
	known := make(map[string]Migration, len(RegisteredMigrations)+len(LegacyMigrations))
	for _, migration := range LegacyMigrations {
		known[migration.Version] = migration
	}
	for _, migration := range RegisteredMigrations {
		known[migration.Version] = migration
	}
	return known
}

func validateMigrationLedger(applied []Migration) error {
	known := allKnownMigrations()
	activeIndex := make(map[string]int, len(RegisteredMigrations))
	for index, migration := range RegisteredMigrations {
		activeIndex[migration.Version] = index
	}
	seen := make(map[string]struct{}, len(applied))
	appliedActive := make(map[int]struct{}, len(RegisteredMigrations))
	for _, migration := range applied {
		knownMigration, ok := known[migration.Version]
		if !ok {
			return fmt.Errorf("migration ledger contains unknown version %q", migration.Version)
		}
		if _, duplicate := seen[migration.Version]; duplicate {
			return fmt.Errorf("migration ledger contains duplicate version %q", migration.Version)
		}
		expected := checksumSQL(GetMigrationSQL(knownMigration.Version))
		if migration.Checksum != expected {
			return fmt.Errorf("migration checksum mismatch for %s: applied=%s current=%s", migration.Version, migration.Checksum, expected)
		}
		if index, active := activeIndex[migration.Version]; active {
			appliedActive[index] = struct{}{}
		}
		seen[migration.Version] = struct{}{}
	}
	missingActive := false
	for index, migration := range RegisteredMigrations {
		if _, applied := appliedActive[index]; !applied {
			missingActive = true
			continue
		}
		if missingActive {
			return fmt.Errorf("migration ledger active stream is not a continuous prefix: %q is applied after an earlier gap", migration.Version)
		}
	}
	return nil
}

func validateAvailableMigrations(available []Migration) error {
	if len(available) != len(RegisteredMigrations) {
		return fmt.Errorf("active migration stream is incomplete: got %d migrations, want %d", len(available), len(RegisteredMigrations))
	}
	for index, migration := range available {
		expected := RegisteredMigrations[index]
		if migration.Version != expected.Version {
			return fmt.Errorf("active migration stream is not in canonical order at index %d: got %q want %q", index, migration.Version, expected.Version)
		}
		if expected.Description != migration.Description || expected.RollbackSQL != migration.RollbackSQL {
			return fmt.Errorf("active migration %q does not match the registered catalog", migration.Version)
		}
	}
	return nil
}

func validateActiveMigration(migration Migration) error {
	if err := validateMigrationCatalog(RegisteredMigrations, LegacyMigrations, GetMigrationSQL); err != nil {
		return fmt.Errorf("validate migration catalog: %w", err)
	}
	for _, registered := range RegisteredMigrations {
		if registered.Version == migration.Version {
			if registered.Description != migration.Description || registered.RollbackSQL != migration.RollbackSQL {
				return fmt.Errorf("active migration %q does not match the registered catalog", migration.Version)
			}
			return nil
		}
	}
	return fmt.Errorf("migration %q is not in the active catalog", migration.Version)
}

func checksumSQL(sql string) string {
	if sql == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

// RollbackMigration rolls back a single migration
func (m *Migrator) RollbackMigration(ctx context.Context, mig Migration) error {
	known, ok := allKnownMigrations()[mig.Version]
	if !ok || known.RollbackSQL != mig.RollbackSQL || known.Description != mig.Description {
		return fmt.Errorf("rollback migration does not match registered catalog")
	}
	return m.ReverseMigrations(ctx, OpDown, []string{mig.Version})
}

// Status returns the current migration status
func (m *Migrator) Status(ctx context.Context, available []Migration) ([]Migration, []Migration, error) {
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return nil, nil, err
	}

	pending, err := m.GetPendingMigrations(ctx, available)
	if err != nil {
		return nil, nil, err
	}

	return applied, pending, nil
}

// RunMigrations runs all pending migrations
func (m *Migrator) RunMigrations(ctx context.Context, available []Migration) (int, error) {
	pending, err := m.GetPendingMigrations(ctx, available)
	if err != nil {
		return 0, err
	}

	if len(pending) == 0 {
		m.logger.Info("No pending migrations")
		return 0, nil
	}

	appliedCount := 0
	for _, mig := range pending {
		if err := m.ApplyMigration(ctx, mig); err != nil {
			return appliedCount, fmt.Errorf("failed to apply migration %s: %w", mig.Version, err)
		}
		appliedCount++
	}

	return appliedCount, nil
}

// DryRun returns the SQL that would be executed without actually running it
func (m *Migrator) DryRun(ctx context.Context, mig Migration) (string, error) {
	if mig.Version == "001_initial_schema" {
		return "-- Initial schema handled by Ent", nil
	}

	sql := GetMigrationSQL(mig.Version)
	if sql == "" {
		return "-- No SQL to execute", nil
	}

	return sql, nil
}

// migrationQuery is shared by read-only inspection and locked transaction entry.
type migrationQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type migrationLockContextKey struct{}
type migrationLockOwnership struct {
	owner  *Migrator
	schema string
	active atomic.Bool
}

// WithMigrationLock serializes every migration writer in the database/schema.
// The session owns the lock across bootstrap callbacks that use separate DB
// connections. Only nested calls on this Migrator inherit ownership; callers
// must propagate ctx and must not start concurrent work inside the callback.
func (m *Migrator) WithMigrationLock(ctx context.Context, fn func(context.Context) error) error {
	if ownership, ok := ctx.Value(migrationLockContextKey{}).(*migrationLockOwnership); ok && ownership.owner == m && ownership.active.Load() {
		return fn(ctx)
	}
	if m.db.Stats().MaxOpenConnections == 1 {
		return fmt.Errorf("migration session lock requires at least two database connections")
	}
	var conn *sql.Conn
	var schema string
	var lockKey int64
	for {
		var err error
		conn, err = m.db.Conn(ctx)
		if err != nil {
			return err
		}
		schema, err = migrationTargetSchema(ctx, conn)
		if err != nil {
			conn.Close()
			return err
		}
		if err = conn.QueryRowContext(ctx, `SELECT hashtextextended(current_database() || ':' || current_schema(), 0)`).Scan(&lockKey); err != nil {
			conn.Close()
			return err
		}
		var acquired bool
		if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&acquired); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			conn.Close()
			return err
		}
		if acquired {
			break
		}
		// A contender must not pin a callback-pool connection while waiting for
		// another owner. Return it before a cancellable retry, including when a
		// different Migrator instance or process holds the same schema lock.
		if err = conn.Close(); err != nil {
			return err
		}
		retry := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			retry.Stop()
			return ctx.Err()
		case <-retry.C:
		}
	}
	defer conn.Close()

	defer func() {
		// A cancelled operation must still release its session lock before pooling.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	ownership := &migrationLockOwnership{owner: m, schema: schema}
	ownership.active.Store(true)
	defer ownership.active.Store(false)
	return fn(context.WithValue(ctx, migrationLockContextKey{}, ownership))
}

func migrationTargetSchema(ctx context.Context, q migrationQuery) (string, error) {
	var schema sql.NullString
	var configured string
	if err := q.QueryRowContext(ctx, `SELECT current_schema(), current_setting('search_path')`).Scan(&schema, &configured); err != nil {
		return "", err
	}
	if !schema.Valid || schema.String == "" {
		return "", fmt.Errorf("migration target schema is missing")
	}
	// Permit PostgreSQL's default only when it resolves to public. Explicit
	// targets must be exactly one existing schema: never fall through to a decoy.
	trimmed := strings.TrimSpace(configured)
	if !(trimmed == schema.String || trimmed == pq.QuoteIdentifier(schema.String) || trimmed == `"$user", public` && schema.String == "public") {
		return "", fmt.Errorf("migration target requires a single explicit schema; search_path is ambiguous or missing")
	}
	if strings.HasPrefix(schema.String, "pg_") || schema.String == "information_schema" {
		return "", fmt.Errorf("system schema is not a migration target")
	}
	if ownership, ok := ctx.Value(migrationLockContextKey{}).(*migrationLockOwnership); ok && ownership.active.Load() && schema.String != ownership.schema {
		return "", fmt.Errorf("migration target differs from locked schema")
	}
	return schema.String, nil
}

// InspectMigrationTarget is read-only even for old ledgers. It must run before
// ALTER ADD COLUMN, schema preparation, Ent creation, reconciliation, or seed.
func (m *Migrator) InspectMigrationTarget(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = inspectMigrationTarget(ctx, tx)
	return err
}

func inspectMigrationTarget(ctx context.Context, q migrationQuery) ([]Migration, error) {
	schema, err := migrationTargetSchema(ctx, q)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='schema_migrations' AND c.relkind='r')`, schema).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		var objects int
		if err = q.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=$1::regnamespace) + (SELECT count(*) FROM pg_proc WHERE pronamespace=$1::regnamespace) + (SELECT count(*) FROM pg_type WHERE typnamespace=$1::regnamespace)`, schema).Scan(&objects); err != nil {
			return nil, err
		}
		if objects != 0 {
			return nil, fmt.Errorf("existing migration target is missing schema_migrations ledger")
		}
		return nil, nil
	}
	applied, err := readMigrationLedger(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	if _, err = PlanMigrations(ControlledMigrationCatalog(), applied, OpUp, nil); err != nil {
		return nil, err
	}
	for _, a := range applied {
		if a.Version == WorkItemPrepareVersion {
			if err := verifyPreparationReceipt(ctx, q, schema, *a.EvidenceDigest); err != nil {
				return nil, err
			}
		}
		if a.Version == WorkItemRetireVersion {
			return nil, fmt.Errorf("controlled stage %s has no executable structure verifier registered", a.Version)
		}
	}
	return applied, nil
}

func readMigrationLedger(ctx context.Context, q migrationQuery, schema string) ([]Migration, error) {
	rows, err := q.QueryContext(ctx, `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=$1 AND table_name='schema_migrations'`, schema)
	if err != nil {
		return nil, err
	}
	columns := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err = rows.Scan(&name, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		columns[name] = kind
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	allowed := map[string]string{"version": "character varying", "description": "text", "applied_at": "timestamp without time zone", "rollback_sql": "text", "checksum": "character varying", "execution_ms": "bigint", "release_version": "character varying", "catalog_revision": "text", "evidence_digest": "text"}
	for name, kind := range columns {
		want, ok := allowed[name]
		if !ok || kind != want {
			return nil, fmt.Errorf("unknown migration ledger column layout: %s (%s)", name, kind)
		}
	}
	for _, name := range []string{"version", "description", "applied_at", "rollback_sql"} {
		if columns[name] == "" {
			return nil, fmt.Errorf("migration ledger missing required column %s", name)
		}
	}
	optional := func(name, fallback string) string {
		if columns[name] != "" {
			return pq.QuoteIdentifier(name)
		}
		return fallback
	}
	query := `SELECT version,description,applied_at,rollback_sql,` + optional("checksum", "''::text") + `,` + optional("execution_ms", "0::bigint") + `,` + optional("release_version", "''::text") + `,` + optional("catalog_revision", "NULL::text") + `,` + optional("evidence_digest", "NULL::text") + ` FROM ` + pq.QuoteIdentifier(schema) + `.schema_migrations ORDER BY version`
	rows, err = q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applied []Migration
	for rows.Next() {
		var a Migration
		var rollback sql.NullString
		if err = rows.Scan(&a.Version, &a.Description, &a.AppliedAt, &rollback, &a.Checksum, &a.ExecutionMS, &a.ReleaseVersion, &a.CatalogRevision, &a.EvidenceDigest); err != nil {
			return nil, err
		}
		if rollback.Valid {
			a.RollbackSQL = rollback.String
		}
		applied = append(applied, a)
	}
	return applied, rows.Err()
}

// InspectRuntimeMigrations never treats disabled AutoMigrate as readiness.
// Preparation and every ordinary receipt are required; retirement stays manual.
func (m *Migrator) InspectRuntimeMigrations(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	applied, err := inspectMigrationTarget(ctx, tx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range applied {
		seen[a.Version] = true
	}
	for _, d := range ControlledMigrationCatalog() {
		if d.Stage != StageRetire && !seen[d.Migration.Version] {
			return fmt.Errorf("runtime requires migration %s", d.Migration.Version)
		}
	}
	return nil
}

// ReverseMigrations preflights the entire request before the first write and
// repeats it under the same schema lock. All selected reversals are atomic.
func (m *Migrator) ReverseMigrations(ctx context.Context, operation MigrationOperation, versions []string) error {
	if operation != OpDown && operation != OpReset {
		return fmt.Errorf("invalid reverse operation %s", operation)
	}
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return err
	}
	if _, err = PlanMigrations(ControlledMigrationCatalog(), applied, operation, versions); err != nil {
		return err
	}
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		applied, err := inspectMigrationTarget(ctx, tx)
		if err != nil {
			return err
		}
		plan, err := PlanMigrations(ControlledMigrationCatalog(), applied, operation, versions)
		if err != nil {
			return err
		}
		for _, mig := range plan.Executable {
			if _, err = tx.ExecContext(ctx, mig.RollbackSQL); err != nil {
				return fmt.Errorf("rollback %s: %w", mig.Version, err)
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=$1`, mig.Version); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

// InspectEmptyMigrationTarget admits fresh bootstrap only for a verified empty
// schema. Existing history must use forward/reverse migration or recovery.
func (m *Migrator) InspectEmptyMigrationTarget(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema, err := migrationTargetSchema(ctx, tx)
	if err != nil {
		return err
	}
	var objects int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=$1::regnamespace) + (SELECT count(*) FROM pg_proc WHERE pronamespace=$1::regnamespace) + (SELECT count(*) FROM pg_type WHERE typnamespace=$1::regnamespace)`, schema).Scan(&objects); err != nil {
		return err
	}
	if objects != 0 {
		return fmt.Errorf("fresh requires an empty migration target; use controlled migration or recovery for existing history")
	}
	return nil
}
