//go:build integration

package ticket

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"itsm-backend/ent"
	entmigrate "itsm-backend/ent/migrate"
	"itsm-backend/ent/workitemnumbersequence"
	appmigration "itsm-backend/migration"
	"itsm-backend/repository/workitemnumber"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestEntRepository_CreatePostgreSQLRollsBackAllocationAfterTicketInsertFailure(t *testing.T) {
	client := openPostgreSQLTicketRepositoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const tenantID = 101
	const requesterID = 501
	period := time.Now().UTC().Format("200601")
	firstNumber := fmt.Sprintf("TKT-%s-000001", period)
	existing, err := client.Ticket.Create().
		SetTitle("conflicting ticket").
		SetDescription("forces the Ticket insert to fail after allocation").
		SetType(string(TypeIncident)).
		SetPriority(string(PriorityLow)).
		SetTicketNumber(firstNumber).
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		SetStatus(string(StatusNew)).
		Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client, zap.NewNop().Sugar(), workitemnumber.NewPostgreSQLAllocator())
	_, err = repo.Create(ctx, &CreateParams{
		Title:       "failed after allocation",
		Description: "the unique ticket number collision is intentional",
		Priority:    PriorityMedium,
		Type:        TypeIncident,
		RequesterID: requesterID,
	}, tenantID)
	require.Error(t, err)

	sequenceCount, err := client.WorkItemNumberSequence.Query().Where(
		workitemnumbersequence.TenantID(tenantID),
		workitemnumbersequence.Period(period),
	).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, sequenceCount, "a failed Ticket insert must roll back the allocator state")

	require.NoError(t, client.Ticket.DeleteOne(existing).Exec(ctx))
	created, err := repo.Create(ctx, &CreateParams{
		Title:       "successful after rollback",
		Description: "the allocator may reuse its rolled-back first number",
		Priority:    PriorityMedium,
		Type:        TypeIncident,
		RequesterID: requesterID,
	}, tenantID)
	require.NoError(t, err)
	require.Equal(t, firstNumber, created.TicketNumber)
}

func openPostgreSQLTicketRepositoryClient(t *testing.T) *ent.Client {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	schemaName := "ticket_repository_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
	require.NoError(t, err)

	scopedDB, err := sql.Open("postgres", postgresTicketRepositoryDSN(t, dsn, schemaName))
	require.NoError(t, err)
	require.NoError(t, scopedDB.PingContext(ctx))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scopedDB)))
	ticketsTable := *entmigrate.TicketsTable
	ticketsTable.ForeignKeys = nil
	require.NoError(t, entmigrate.Create(
		ctx,
		client.Schema,
		[]*schema.Table{entmigrate.WorkItemNumberSequencesTable, &ticketsTable},
		entmigrate.WithForeignKeys(false),
	))
	_, err = scopedDB.ExecContext(ctx, appmigration.GetMigrationSQL("020_work_item_number_allocator"))
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		require.NoError(t, client.Close())
		_, dropErr := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		require.NoError(t, dropErr)
		require.NoError(t, adminDB.Close())
	})
	return client
}

func postgresTicketRepositoryDSN(t *testing.T, dsn, schemaName string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schemaName)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schemaName
}
