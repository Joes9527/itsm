package bootstrap

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/connector"
	"itsm-backend/ent"
	"itsm-backend/ent/migrate"
	"itsm-backend/service"

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

func TestPrepareTicketNotificationMigrationSQLiteUpgradesPopulatedLegacyRows(t *testing.T) {
	db := openLegacyTicketCCSQLite(t, "ticket_notification_populated_upgrade")
	ctx := context.Background()
	createLegacyTicketNotificationTable(t, db)
	legacyCreatedAt := time.Now().UTC().Add(-time.Hour)
	legacyRows := []struct {
		id      int
		channel string
		status  string
	}{
		{id: 1, channel: "email", status: "pending"},
		{id: 2, channel: "sms", status: "failed"},
		{id: 3, channel: "email", status: "sent"},
		{id: 4, channel: "in_app", status: "pending"},
		{id: 5, channel: "in_app", status: "read"},
	}
	for _, row := range legacyRows {
		_, err := db.ExecContext(ctx, `
			INSERT INTO ticket_notifications
				(id, ticket_id, user_id, type, channel, content, status, tenant_id, created_at)
			VALUES (?, 41, 73, 'cc', ?, ?, ?, 29, ?)
		`, row.id, row.channel, "legacy-content-"+row.status, row.status, legacyCreatedAt)
		require.NoError(t, err)
	}

	require.NoError(t, prepareTicketNotificationMigration(ctx, db, zap.NewNop().Sugar()))
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))

	assertLegacyTicketNotificationUpgrade(t, ctx, db, "?")
	assertMigratedTicketNotificationsArePickedUp(t, ctx, db, client, [3]string{"?", "?", "?"})
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

func createLegacyTicketNotificationTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE ticket_notifications (
			id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			ticket_id integer NOT NULL,
			user_id integer NOT NULL,
			type text NOT NULL,
			channel text NOT NULL DEFAULT 'in_app',
			content text NOT NULL,
			sent_at datetime NULL,
			read_at datetime NULL,
			status text NOT NULL DEFAULT 'pending',
			tenant_id integer NOT NULL,
			created_at datetime NOT NULL
		)
	`)
	require.NoError(t, err)
}

func assertLegacyTicketNotificationUpgrade(t *testing.T, ctx context.Context, db *sql.DB, placeholder string) {
	t.Helper()
	query := func(id int) (int, int, int, string, sql.NullString, int, sql.NullTime, sql.NullString, sql.NullTime, sql.NullString, string) {
		var ticketID, userID, tenantID int
		var status, channel, content string
		var key, owner, errorClass sql.NullString
		var attempts int
		var nextAttempt, leaseExpires sql.NullTime
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT ticket_id, user_id, tenant_id, status, delivery_key, attempt_count, next_attempt_at,
			       lease_owner, lease_expires_at, last_error_class, channel, content
			FROM ticket_notifications WHERE id = `+placeholder,
			id,
		).Scan(&ticketID, &userID, &tenantID, &status, &key, &attempts, &nextAttempt, &owner, &leaseExpires, &errorClass, &channel, &content))
		return ticketID, userID, tenantID, status, key, attempts, nextAttempt, owner, leaseExpires, errorClass, channel + ":" + content
	}

	for _, expected := range []struct {
		id        int
		channel   string
		oldStatus string
	}{
		{id: 1, channel: "email", oldStatus: "pending"},
		{id: 2, channel: "sms", oldStatus: "failed"},
	} {
		ticketID, userID, tenantID, status, key, attempts, nextAttempt, owner, leaseExpires, errorClass, preserved := query(expected.id)
		require.Equal(t, 41, ticketID)
		require.Equal(t, 73, userID)
		require.Equal(t, 29, tenantID)
		require.Equal(t, "pending", status)
		require.Equal(t, "ticket-notification-legacy-"+strconv.Itoa(expected.id), key.String)
		require.Equal(t, 0, attempts)
		require.True(t, nextAttempt.Valid)
		require.False(t, owner.Valid)
		require.False(t, leaseExpires.Valid)
		require.False(t, errorClass.Valid)
		require.Equal(t, expected.channel+":legacy-content-"+expected.oldStatus, preserved)
	}
	for _, expected := range []struct {
		id      int
		channel string
		status  string
	}{
		{id: 3, channel: "email", status: "sent"},
		{id: 4, channel: "in_app", status: "pending"},
		{id: 5, channel: "in_app", status: "read"},
	} {
		ticketID, userID, tenantID, status, key, attempts, nextAttempt, owner, leaseExpires, errorClass, preserved := query(expected.id)
		require.Equal(t, 41, ticketID)
		require.Equal(t, 73, userID)
		require.Equal(t, 29, tenantID)
		require.Equal(t, expected.status, status)
		require.False(t, key.Valid)
		require.Equal(t, 0, attempts)
		require.True(t, nextAttempt.Valid)
		require.False(t, owner.Valid)
		require.False(t, leaseExpires.Valid)
		require.False(t, errorClass.Valid)
		require.Equal(t, expected.channel+":legacy-content-"+expected.status, preserved)
	}
}

type migratedNotificationRecorder struct {
	messageIDs []string
}

type migratedNotificationConnector struct {
	name     string
	typeName connector.ConnectorType
	recorder *migratedNotificationRecorder
}

func (c *migratedNotificationConnector) Manifest() connector.Manifest {
	return connector.Manifest{
		Name:                c.name,
		Version:             "1.0.0",
		Title:               "Migrated notification test connector",
		Type:                c.typeName,
		Capabilities:        []connector.Capability{connector.CapSendMessage},
		RequiredPermissions: []string{"connector:write"},
	}
}

func (c *migratedNotificationConnector) Init(context.Context, connector.Config) error { return nil }
func (c *migratedNotificationConnector) Send(_ context.Context, message *connector.Message) error {
	c.recorder.messageIDs = append(c.recorder.messageIDs, message.ID)
	return nil
}
func (c *migratedNotificationConnector) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true}
}
func (c *migratedNotificationConnector) Close() error { return nil }

func assertMigratedTicketNotificationsArePickedUp(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	client *ent.Client,
	placeholders [3]string,
) {
	t.Helper()
	tenant := client.Tenant.Create().
		SetName("Migrated Notification Tenant").
		SetCode("migrated-notification-tenant").
		SetDomain("migrated-notification.test").
		SetStatus("active").
		SaveX(ctx)
	recipient := client.User.Create().
		SetUsername("migrated-notification-recipient").
		SetEmail("migrated-notification@test.invalid").
		SetPhone("10000000000").
		SetPasswordHash("x").
		SetName("Migrated Recipient").
		SetTenantID(tenant.ID).
		SetActive(true).
		SaveX(ctx)
	ticket := client.Ticket.Create().
		SetTitle("Migrated notification ticket").
		SetTicketNumber("MIGRATED-NOTIFICATION-1").
		SetStatus("open").
		SetRequesterID(recipient.ID).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	_, err := db.ExecContext(ctx, `
		UPDATE ticket_notifications
		SET tenant_id = `+placeholders[0]+`, ticket_id = `+placeholders[1]+`, user_id = `+placeholders[2]+`
		WHERE id IN (1, 2)
	`, tenant.ID, ticket.ID, recipient.ID)
	require.NoError(t, err)

	recorder := &migratedNotificationRecorder{}
	registry := connector.NewRegistry()
	for _, config := range []struct {
		name     string
		typeName connector.ConnectorType
	}{
		{name: "email", typeName: connector.TypeEmail},
		{name: "sms", typeName: connector.TypeSMS},
	} {
		connectorConfig := config
		registry.Register(func() connector.Connector {
			return &migratedNotificationConnector{
				name: connectorConfig.name, typeName: connectorConfig.typeName, recorder: recorder,
			}
		})
	}
	manager := connector.NewManager(registry, zap.NewNop().Sugar())
	t.Cleanup(manager.CloseAll)
	for _, channel := range []string{"email", "sms"} {
		require.NoError(t, manager.Provision(ctx, connector.Config{
			TenantID: tenant.ID,
			Name:     channel,
			Type:     connector.ConnectorType(channel),
			Provider: "migration-test",
			Enabled:  true,
		}))
	}
	notifications := service.NewTicketNotificationService(client, zap.NewNop().Sugar())
	notifications.SetConnectorManager(manager)
	completed, err := notifications.ProcessPendingDeliveries(ctx, "migration-test-worker", 10)
	require.NoError(t, err)
	require.Equal(t, 2, completed)
	require.ElementsMatch(t, []string{
		"ticket-notification-legacy-1",
		"ticket-notification-legacy-2",
	}, recorder.messageIDs)
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
