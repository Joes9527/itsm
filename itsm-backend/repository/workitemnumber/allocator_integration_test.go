//go:build integration

package workitemnumber

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	entmigrate "itsm-backend/ent/migrate"
	"itsm-backend/ent/workitemnumbersequence"
	appmigration "itsm-backend/migration"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const allocatorIntegrationTimeout = 2 * time.Minute

func TestPostgreSQLAllocator_ConcurrentTenantMonthlyAllocation(t *testing.T) {
	client := openPostgreSQLAllocatorIntegrationClient(t)
	allocator := NewPostgreSQLAllocator()
	issuedAt := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), allocatorIntegrationTimeout)
	defer cancel()

	type allocation struct {
		tenantID int
		number   string
		err      error
	}
	const allocationsPerTenant = 64
	results := make(chan allocation, allocationsPerTenant*2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, tenantID := range []int{101, 202} {
		for range allocationsPerTenant {
			workers.Add(1)
			go func(tenantID int) {
				defer workers.Done()
				<-start
				tx, err := client.Tx(ctx)
				if err != nil {
					results <- allocation{tenantID: tenantID, err: err}
					return
				}
				number, err := allocator.Allocate(ctx, tx.Client(), tenantID, issuedAt)
				if err != nil {
					_ = tx.Rollback()
					results <- allocation{tenantID: tenantID, err: err}
					return
				}
				if err = tx.Commit(); err != nil {
					results <- allocation{tenantID: tenantID, err: err}
					return
				}
				results <- allocation{tenantID: tenantID, number: number}
			}(tenantID)
		}
	}
	close(start)
	workers.Wait()
	close(results)

	byTenant := map[int][]string{101: {}, 202: {}}
	for result := range results {
		require.NoError(t, result.err)
		byTenant[result.tenantID] = append(byTenant[result.tenantID], result.number)
	}
	for tenantID, numbers := range byTenant {
		requireExactMonthlyRange(t, numbers, "TKT-202609-", allocationsPerTenant)
		row, err := client.WorkItemNumberSequence.Query().Where(
			workitemnumbersequence.TenantID(tenantID),
			workitemnumbersequence.Period("202609"),
		).Only(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(allocationsPerTenant), row.LastValue)
	}

	count, err := client.WorkItemNumberSequence.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestPostgreSQLAllocator_RollsBackWithWorkItem(t *testing.T) {
	client := openPostgreSQLAllocatorIntegrationClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx, cancel := context.WithTimeout(context.Background(), allocatorIntegrationTimeout)
	defer cancel()
	issuedAt := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	number, err := allocator.Allocate(ctx, tx.Client(), 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202609-000001", number)
	_, err = tx.Client().Ticket.Create().
		SetTitle("rolled back WorkItem").
		SetTicketNumber(number).
		SetRequesterID(501).
		SetTenantID(101).
		SetCreatedAt(issuedAt).
		SetUpdatedAt(issuedAt).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	ticketCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, ticketCount)
	sequenceCount, err := client.WorkItemNumberSequence.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, sequenceCount)

	nextTx, err := client.Tx(ctx)
	require.NoError(t, err)
	number, err = allocator.Allocate(ctx, nextTx.Client(), 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202609-000001", number)
	require.NoError(t, nextTx.Rollback())
}

func TestPostgreSQLAllocator_FailsClosedWhenMonthlySequenceIsExhausted(t *testing.T) {
	client := openPostgreSQLAllocatorIntegrationClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx, cancel := context.WithTimeout(context.Background(), allocatorIntegrationTimeout)
	defer cancel()
	issuedAt := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	row, err := client.WorkItemNumberSequence.Create().
		SetTenantID(101).
		SetPeriod("202609").
		SetLastValue(999999).
		Save(ctx)
	require.NoError(t, err)

	number, err := allocator.Allocate(ctx, client, 101, issuedAt)
	require.Empty(t, number)
	require.ErrorContains(t, err, "increment work item number sequence")
	require.Equal(t, int64(999999), client.WorkItemNumberSequence.GetX(ctx, row.ID).LastValue)
}

func openPostgreSQLAllocatorIntegrationClient(t *testing.T) *ent.Client {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), allocatorIntegrationTimeout)
	defer cancel()

	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	schemaName := "workitem_number_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
	require.NoError(t, err)

	scopedDB, err := sql.Open("postgres", postgresDSNWithSearchPath(t, dsn, schemaName))
	require.NoError(t, err)
	// Keep the database pool below common PostgreSQL max_connections limits while
	// all 128 goroutines still begin and commit their own Ent transaction.
	scopedDB.SetMaxOpenConns(32)
	scopedDB.SetMaxIdleConns(16)
	require.NoError(t, scopedDB.PingContext(ctx))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scopedDB)))
	// TicketsTable carries references to the complete application schema even
	// when foreign-key creation is disabled. This focused fixture needs the
	// generated Ticket columns and indexes, but none of its unrelated edges.
	// Clear foreign keys on a test-local copy so Ent can install the faithful
	// table shape without requiring every referenced domain table.
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
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), allocatorIntegrationTimeout)
		defer cleanupCancel()
		require.NoError(t, client.Close())
		_, dropErr := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		require.NoError(t, dropErr)
		require.NoError(t, adminDB.Close())
	})
	return client
}

func postgresDSNWithSearchPath(t *testing.T, dsn, schemaName string) string {
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

func requireExactMonthlyRange(t *testing.T, numbers []string, prefix string, count int) {
	t.Helper()
	require.Len(t, numbers, count)
	suffixes := make([]int, 0, len(numbers))
	seen := make(map[int]struct{}, len(numbers))
	for _, number := range numbers {
		require.Truef(t, strings.HasPrefix(number, prefix), "unexpected number %q", number)
		suffixText := strings.TrimPrefix(number, prefix)
		require.Len(t, suffixText, 6)
		suffix, err := strconv.Atoi(suffixText)
		require.NoError(t, err)
		require.NotContains(t, seen, suffix)
		seen[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
	}
	sort.Ints(suffixes)
	for i, suffix := range suffixes {
		require.Equal(t, i+1, suffix)
	}
}
