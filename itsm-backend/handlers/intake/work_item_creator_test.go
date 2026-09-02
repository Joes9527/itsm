package intake

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTicketAllocator struct {
	number string
	calls  int
	err    error
}

func (s *stubTicketAllocator) Allocate(ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time) (string, error) {
	s.calls++
	return s.number, s.err
}

func TestWorkItemCreatorUsesWorkItemNumberAllocator(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	allocator := &stubTicketAllocator{number: "TKT-202609-000001"}
	creator := NewWorkItemCreator(allocator)

	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	ticket, err := creator.CreateBase(context.Background(), tx, &CreationPlan{
		WorkItem: WorkItemDraft{
			TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID,
			RecordClass: RecordClassIncident, Title: "VPN down",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "TKT-202609-000001", ticket.TicketNumber)
	assert.Equal(t, 1, allocator.calls)
}

func TestWorkItemCreatorAcceptsChangeRequestRecordClass(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	allocator := &stubTicketAllocator{number: "TKT-202609-000002"}
	creator := NewWorkItemCreator(allocator)

	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	ticket, err := creator.CreateBase(context.Background(), tx, &CreationPlan{
		WorkItem: WorkItemDraft{
			TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID,
			RecordClass: RecordClassChangeRequest, Title: "Upgrade router firmware",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, RecordClassChangeRequest, ticket.RecordClass)
	assert.Equal(t, "change", ticket.Type)
}
