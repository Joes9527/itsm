package intake

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

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

func createTestTenant(t *testing.T, client *ent.Client) *ent.Tenant {
	t.Helper()
	name := "wic_" + t.Name()
	tenant, err := client.Tenant.Create().SetName(name).SetCode(name).SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	return tenant
}

func createTestUser(t *testing.T, client *ent.Client, tenantID int) *ent.User {
	t.Helper()
	name := "wic_" + t.Name()
	user, err := client.User.Create().SetUsername(name).SetEmail(name + "@example.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)
	return user
}

func TestWorkItemCreatorUsesWorkItemNumberAllocator(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemcreator?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	tenant := createTestTenant(t, client)
	requester := createTestUser(t, client, tenant.ID)
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
	client := enttest.Open(t, "sqlite3", "file:workitemcreator_change?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	tenant := createTestTenant(t, client)
	requester := createTestUser(t, client, tenant.ID)
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
