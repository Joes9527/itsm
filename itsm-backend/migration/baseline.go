package migration

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	"itsm-backend/ent"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

const CurrentBaselineAssetName = "fresh-baseline/2026-09-04/current.sql"

const (
	prepareApplyMarker   = "-- +itsm prepare apply"
	prepareVerifyMarker  = "-- +itsm prepare verify"
	baselineApplyMarker  = "-- +itsm baseline apply"
	baselineVerifyMarker = "-- +itsm baseline verify"
)

//go:embed sql/baseline/current.sql
var currentBaselineSQL string

type currentBaselineParts struct {
	PrepareApply   string
	PrepareVerify  string
	BaselineApply  string
	BaselineVerify string
}

// CurrentBaselineSQL returns the immutable embedded fresh-install asset used
// to derive the release-manifest checksum.
func CurrentBaselineSQL() string {
	return currentBaselineSQL
}

func parseCurrentBaseline(data []byte) (currentBaselineParts, error) {
	text := string(data)
	markers := []string{prepareApplyMarker, prepareVerifyMarker, baselineApplyMarker, baselineVerifyMarker}
	positions := make([]int, len(markers))
	for index, marker := range markers {
		if strings.Count(text, marker) != 1 {
			return currentBaselineParts{}, fmt.Errorf("baseline section %q must occur exactly once", marker)
		}
		positions[index] = strings.Index(text, marker)
		if index > 0 && positions[index] <= positions[index-1] {
			return currentBaselineParts{}, fmt.Errorf("baseline section %q is out of order", marker)
		}
	}
	section := func(index int) string {
		start := positions[index] + len(markers[index])
		end := len(text)
		if index+1 < len(markers) {
			end = positions[index+1]
		}
		return strings.TrimSpace(text[start:end])
	}
	parts := currentBaselineParts{
		PrepareApply:   section(0),
		PrepareVerify:  section(1),
		BaselineApply:  section(2),
		BaselineVerify: section(3),
	}
	if parts.PrepareApply == "" || parts.PrepareVerify == "" || parts.BaselineApply == "" || parts.BaselineVerify == "" {
		return currentBaselineParts{}, fmt.Errorf("baseline sections must not be empty")
	}
	return parts, nil
}

func loadCurrentBaseline() (currentBaselineParts, error) {
	parts, err := parseCurrentBaseline([]byte(currentBaselineSQL))
	if err != nil {
		return currentBaselineParts{}, fmt.Errorf("load embedded current baseline: %w", err)
	}
	return parts, nil
}

// PrepareCurrentInfrastructure installs the PostgreSQL extension and raw
// tables required before Ent can create the current schema.
func PrepareCurrentInfrastructure(ctx context.Context, db BootstrapConnection) error {
	if db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	phase, err := verifyFreshBootstrapTargetPhase(ctx, db, CurrentRelease())
	if err != nil {
		return fmt.Errorf("verify fresh bootstrap target before DDL: %w", err)
	}
	if phase >= freshTargetPrepared {
		return nil
	}
	parts, err := loadCurrentBaseline()
	if err != nil {
		return err
	}
	if err := applyVerifiedBaselineSection(ctx, db, "prepare infrastructure", parts.PrepareApply, parts.PrepareVerify); err != nil {
		return err
	}
	return verifyFreshPhaseCatalog(ctx, db, freshTargetPrepared)
}

// CreateCurrentEntSchema creates only the current Ent-owned objects on the
// pinned bootstrap connection. Verified committed Ent/current phases are
// skipped; an unprepared or drifted target is rejected before schema writes.
func CreateCurrentEntSchema(ctx context.Context, db BootstrapConnection) error {
	if db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	phase, err := verifyFreshBootstrapTargetPhase(ctx, db, CurrentRelease())
	if err != nil {
		return fmt.Errorf("verify fresh Ent target before DDL: %w", err)
	}
	if phase >= freshTargetEntSchema {
		return nil
	}
	if phase != freshTargetPrepared {
		return fmt.Errorf("fresh Ent schema requires a verified prepared phase")
	}
	client, err := NewEntClientOnConnection(db)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Schema.Create(ctx); err != nil {
		return fmt.Errorf("create current Ent schema: %w", err)
	}
	if err := VerifyFreshMigrationHistory(ctx, db); err != nil {
		return err
	}
	if err := verifyFreshPhaseCatalog(ctx, db, freshTargetEntSchema); err != nil {
		return fmt.Errorf("verify committed fresh Ent phase: %w", err)
	}
	return nil
}

// ApplyCurrentBaseline installs current non-Ent assets without replaying or
// recording any published upgrade migration.
func ApplyCurrentBaseline(ctx context.Context, db BootstrapConnection) error {
	if db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	phase, err := verifyFreshBootstrapTargetPhase(ctx, db, CurrentRelease())
	if err != nil {
		return fmt.Errorf("verify fresh baseline target before DDL: %w", err)
	}
	if phase == freshTargetCurrentRelease {
		return nil
	}
	if phase != freshTargetEntSchema {
		return fmt.Errorf("current baseline requires a verified Ent schema phase")
	}
	parts, err := loadCurrentBaseline()
	if err != nil {
		return err
	}
	if err := applyVerifiedBaselineSection(ctx, db, "current baseline", parts.BaselineApply, parts.BaselineVerify); err != nil {
		return err
	}
	if err := VerifyFreshMigrationHistory(ctx, db); err != nil {
		return err
	}
	if err := verifyFreshPhaseCatalog(ctx, db, freshTargetCurrentRelease); err != nil {
		return fmt.Errorf("verify committed fresh current release phase: %w", err)
	}
	return nil
}

func applyVerifiedBaselineSection(ctx context.Context, db BootstrapConnection, name, applySQL, verifySQL string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", name, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, applySQL); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, verifySQL); err != nil {
		return fmt.Errorf("verify %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}

// VerifyFreshMigrationHistory rejects accidental use of the fresh path on a
// database with any committed upgrade history. An absent ledger is valid only
// before the current baseline creates the exact empty ledger.
func VerifyFreshMigrationHistory(ctx context.Context, db DBTX) error {
	if db == nil {
		return fmt.Errorf("fresh migration history store is required")
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `
		/* fresh_migration_history_relation */
		SELECT to_regclass(format('%I.schema_migrations', current_schema())) IS NOT NULL
	`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect fresh migration history: %w", err)
	}
	if !exists {
		return nil
	}
	var count int64
	if err := db.QueryRowContext(ctx, `
		/* fresh_migration_history_rows */
		SELECT COUNT(*) FROM schema_migrations
	`).Scan(&count); err != nil {
		return fmt.Errorf("count fresh migration history: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("fresh database must not contain migration history")
	}
	return nil
}

// NewEntClientOnConnection binds Ent schema creation and seed transactions to
// the same dedicated connection that owns the bootstrap advisory lock.
func NewEntClientOnConnection(conn BootstrapConnection) (*ent.Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("bootstrap connection is required")
	}
	driver := &pinnedEntDriver{
		Driver: entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: conn}),
		conn:   conn,
	}
	return ent.NewClient(ent.Driver(driver)), nil
}

type pinnedEntDriver struct {
	*entsql.Driver
	conn BootstrapConnection
}

func (driver *pinnedEntDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	return driver.BeginTx(ctx, nil)
}

func (driver *pinnedEntDriver) BeginTx(ctx context.Context, options *sql.TxOptions) (dialect.Tx, error) {
	tx, err := driver.conn.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &pinnedEntTx{
		Driver: entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx}),
		tx:     tx,
	}, nil
}

// The orchestration owns the pinned connection. Closing a short-lived Ent
// client must not release the advisory lock before the remaining phases run.
func (*pinnedEntDriver) Close() error { return nil }

type pinnedEntTx struct {
	*entsql.Driver
	tx *sql.Tx
}

func (tx *pinnedEntTx) Commit() error   { return tx.tx.Commit() }
func (tx *pinnedEntTx) Rollback() error { return tx.tx.Rollback() }
