package bootstrap

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/migrate"

	entsql "entgo.io/ent/dialect/sql"
	sqlschema "entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const legacyTicketCCIndex = "ticketcc_tenant_id_ticket_id_user_id"

func TestPrepareTicketCCIndexMigrationSQLiteSkipsMissingTableAndIndex(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:ticket_cc_missing?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, prepareTicketCCIndexMigration(context.Background(), db, zap.NewNop().Sugar()))
}

func TestPrepareTicketCCIndexMigrationSQLiteReplacesLegacyUniqueIndex(t *testing.T) {
	db := openLegacyTicketCCSQLite(t, "ticket_cc_legacy_index")
	ctx := context.Background()
	createLegacyTicketCCTable(t, db)
	_, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX `+legacyTicketCCIndex+` ON ticket_ccs (tenant_id, ticket_id, user_id)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES (73, 11, 29, '2026-08-31 12:00:00', true, 41)
	`)
	require.NoError(t, err)

	migrateTicketCCSQLite(t, db)
	assertSQLiteActiveTicketCCIndex(t, db)

	_, err = db.ExecContext(ctx, `UPDATE ticket_ccs SET is_active = false`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES (73, 12, 29, '2026-08-31 13:00:00', true, 41)
	`)
	require.NoError(t, err, "inactive history must allow an ordinary re-add")
	_, err = db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES (73, 13, 29, '2026-08-31 14:00:00', true, 41)
	`)
	require.Error(t, err, "a second active ordinary relation must be rejected")

	_, err = db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, delivery_key, added_at, is_active, ticket_id)
		VALUES (74, 11, 29, 'callback-key', '2026-08-31 15:00:00', false, 41)
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, delivery_key, added_at, is_active, ticket_id)
		VALUES (74, 12, 29, 'callback-key', '2026-08-31 16:00:00', false, 41)
	`)
	require.Error(t, err, "callback delivery uniqueness must remain enforced")
}

func TestPrepareTicketCCIndexMigrationSQLiteAllowsHistoricalInactiveDuplicates(t *testing.T) {
	db := openLegacyTicketCCSQLite(t, "ticket_cc_historical_duplicates")
	ctx := context.Background()
	createLegacyTicketCCTable(t, db)
	for _, addedBy := range []int{11, 12, 13} {
		_, err := db.ExecContext(ctx, `
			INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
			VALUES (73, ?, 29, '2026-08-31 12:00:00', false, 41)
		`, addedBy)
		require.NoError(t, err)
	}

	migrateTicketCCSQLite(t, db)
	assertSQLiteActiveTicketCCIndex(t, db)

	_, err := db.ExecContext(ctx, `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES (73, 14, 29, '2026-08-31 13:00:00', true, 41)
	`)
	require.NoError(t, err, "historical inactive duplicates must allow one active re-add")
	var rows, activeRows, nullKeys int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_ccs`).Scan(&rows))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_ccs WHERE is_active`).Scan(&activeRows))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_ccs WHERE delivery_key IS NULL`).Scan(&nullKeys))
	require.Equal(t, 4, rows)
	require.Equal(t, 1, activeRows)
	require.Equal(t, rows, nullKeys)
}

func openLegacyTicketCCSQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func createLegacyTicketCCTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE ticket_ccs (
			id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			user_id integer NOT NULL,
			added_by integer NOT NULL,
			tenant_id integer NOT NULL,
			added_at datetime NOT NULL,
			is_active bool NOT NULL DEFAULT true,
			ticket_id integer NOT NULL
		)
	`)
	require.NoError(t, err)
}

func migrateTicketCCSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, prepareTicketCCIndexMigration(ctx, db, zap.NewNop().Sugar()))
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	table := *migrate.TicketCcsTable
	table.ForeignKeys = nil
	require.NoError(t, migrate.Create(ctx, client.Schema, []*sqlschema.Table{&table}))
}

func assertSQLiteActiveTicketCCIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	var definition string
	require.NoError(t, db.QueryRowContext(context.Background(), `
		SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?
	`, legacyTicketCCIndex).Scan(&definition))
	normalized := strings.ToLower(strings.Join(strings.Fields(definition), " "))
	require.Contains(t, normalized, "unique index")
	require.Contains(t, normalized, "where is_active")
}
