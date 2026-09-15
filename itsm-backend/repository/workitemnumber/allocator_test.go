package workitemnumber

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestPostgreSQLAllocator_AllocatesSequentialTenantMonthlyNumbers(t *testing.T) {
	client := openAllocatorTestClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx := context.Background()
	issuedAt := time.Date(2026, 9, 1, 0, 30, 0, 0,
		time.FixedZone("UTC+8", 8*60*60))

	n1, err := allocator.Allocate(ctx, client, 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202608-000001", n1)

	n2, err := allocator.Allocate(ctx, client, 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202608-000002", n2)

	n3, err := allocator.Allocate(ctx, client, 202, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202608-000001", n3)

	sequences, err := client.WorkItemNumberSequence.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, sequences, 2)
	require.Equal(t, int64(2), sequenceValue(t, sequences, 101, "202608"))
	require.Equal(t, int64(1), sequenceValue(t, sequences, 202, "202608"))
}

func TestPostgreSQLAllocator_UsesIndependentUTCMonths(t *testing.T) {
	client := openAllocatorTestClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx := context.Background()

	august, err := allocator.Allocate(ctx, client, 101, time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "TKT-202608-000001", august)

	september, err := allocator.Allocate(ctx, client, 101, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "TKT-202609-000001", september)
}

func TestPostgreSQLAllocator_RejectsInvalidInputs(t *testing.T) {
	client := openAllocatorTestClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx := context.Background()
	issuedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		client   *ent.Client
		tenantID int
		issuedAt time.Time
		wantErr  string
	}{
		{
			name:     "nil client",
			tenantID: 1,
			issuedAt: issuedAt,
			wantErr:  "work item number allocator requires an Ent client",
		},
		{
			name:     "zero tenant",
			client:   client,
			tenantID: 0,
			issuedAt: issuedAt,
			wantErr:  "work item number allocator requires a positive tenant id",
		},
		{
			name:     "negative tenant",
			client:   client,
			tenantID: -1,
			issuedAt: issuedAt,
			wantErr:  "work item number allocator requires a positive tenant id",
		},
		{
			name:     "zero issuedAt",
			client:   client,
			tenantID: 1,
			wantErr:  "work item number allocator requires issuedAt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := allocator.Allocate(ctx, tt.client, tt.tenantID, tt.issuedAt)
			require.EqualError(t, err, tt.wantErr)
		})
	}

	count, err := client.WorkItemNumberSequence.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestPostgreSQLAllocator_RollsBackWithCallerTransaction(t *testing.T) {
	client := openAllocatorTestClient(t)
	allocator := NewPostgreSQLAllocator()
	ctx := context.Background()
	issuedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	number, err := allocator.Allocate(ctx, tx.Client(), 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202609-000001", number)
	require.NoError(t, tx.Rollback())

	nextTx, err := client.Tx(ctx)
	require.NoError(t, err)
	number, err = allocator.Allocate(ctx, nextTx.Client(), 101, issuedAt)
	require.NoError(t, err)
	require.Equal(t, "TKT-202609-000001", number)
	require.NoError(t, nextTx.Commit())
}

func openAllocatorTestClient(t *testing.T) *ent.Client {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	return enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name))
}

func sequenceValue(t *testing.T, sequences []*ent.WorkItemNumberSequence, tenantID int, period string) int64 {
	t.Helper()
	for _, sequence := range sequences {
		if sequence.TenantID == tenantID && sequence.Period == period {
			return sequence.LastValue
		}
	}
	require.FailNowf(t, "missing sequence", "tenant=%d period=%s", tenantID, period)
	return 0
}
