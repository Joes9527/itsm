package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Migration represents a single database migration
type Migration struct {
	Version        string
	Description    string
	AppliedAt      *time.Time
	RollbackSQL    string
	Checksum       string
	ExecutionMS    int64
	ReleaseVersion string
}

// Migrator handles database migrations
type Migrator struct {
	db             *sql.DB
	logger         *zap.SugaredLogger
	releaseVersion string
}

// NewMigrator creates a new Migrator instance
func NewMigrator(db *sql.DB, logger *zap.SugaredLogger) *Migrator {
	releaseVersion := os.Getenv("ITSM_RELEASE_VERSION")
	if releaseVersion == "" {
		releaseVersion = "unversioned"
	}
	return &Migrator{db: db, logger: logger, releaseVersion: releaseVersion}
}

// EnsureMigrationsTable creates the migrations tracking table if it doesn't exist
func (m *Migrator) EnsureMigrationsTable(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) PRIMARY KEY,
		description TEXT NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		rollback_sql TEXT
	)`
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx, `
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum VARCHAR(128) NOT NULL DEFAULT '';
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS execution_ms BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS release_version VARCHAR(64) NOT NULL DEFAULT '';
	`)
	return err
}

// GetAppliedMigrations returns all applied migrations sorted by version
func (m *Migrator) GetAppliedMigrations(ctx context.Context) ([]Migration, error) {
	query := `SELECT version, description, applied_at, rollback_sql, checksum, execution_ms, release_version
		FROM schema_migrations ORDER BY version`
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query migrations: %w", err)
	}
	defer rows.Close()

	var migrations []Migration
	for rows.Next() {
		var mig Migration
		var rollback sql.NullString
		if err := rows.Scan(
			&mig.Version, &mig.Description, &mig.AppliedAt, &rollback,
			&mig.Checksum, &mig.ExecutionMS, &mig.ReleaseVersion,
		); err != nil {
			return nil, fmt.Errorf("failed to scan migration: %w", err)
		}
		if rollback.Valid {
			mig.RollbackSQL = rollback.String
		}
		migrations = append(migrations, mig)
	}
	return migrations, rows.Err()
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
	if err := validateActiveMigration(mig); err != nil {
		return err
	}
	sql := GetMigrationSQL(mig.Version)

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

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
	seen := make(map[string]struct{}, len(applied))
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
		seen[migration.Version] = struct{}{}
	}
	return nil
}

func validateAvailableMigrations(available []Migration) error {
	if len(available) != len(RegisteredMigrations) {
		return fmt.Errorf("active migration stream is incomplete: got %d migrations, want %d", len(available), len(RegisteredMigrations))
	}
	active := make(map[string]Migration, len(RegisteredMigrations))
	for _, migration := range RegisteredMigrations {
		active[migration.Version] = migration
	}
	seen := make(map[string]struct{}, len(available))
	for _, migration := range available {
		expected, ok := active[migration.Version]
		if !ok {
			return fmt.Errorf("unknown active migration %q", migration.Version)
		}
		if _, duplicate := seen[migration.Version]; duplicate {
			return fmt.Errorf("duplicate active migration %q", migration.Version)
		}
		if expected.Description != migration.Description || expected.RollbackSQL != migration.RollbackSQL {
			return fmt.Errorf("active migration %q does not match the registered catalog", migration.Version)
		}
		seen[migration.Version] = struct{}{}
	}
	for _, migration := range RegisteredMigrations {
		if _, present := seen[migration.Version]; !present {
			return fmt.Errorf("active migration stream omits registered migration %q", migration.Version)
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
	if mig.RollbackSQL == "" {
		return fmt.Errorf("no rollback SQL defined for migration %s", mig.Version)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	m.logger.Infow("Rolling back migration", "version", mig.Version)

	// Execute rollback SQL
	if _, err := tx.ExecContext(ctx, mig.RollbackSQL); err != nil {
		return fmt.Errorf("failed to execute rollback: %w", err)
	}

	// Remove migration record
	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = $1`, mig.Version); err != nil {
		return fmt.Errorf("failed to remove migration record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	m.logger.Infow("Migration rolled back successfully", "version", mig.Version)
	return nil
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
