package service_request

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

// createTestTicketForSR is a minimal ent.Ticket factory mirroring the pattern used by
// controller/ticket_controller_test.go's createTestTenantAndUserForTicket — ServiceRequest
// has no ent edge to Ticket (plain int FK, see ent/schema/servicerequest.go), so tests can
// construct the linked ticket directly without going through TicketService.
func createTestTicketForSR(t *testing.T, client *ent.Client, tenantID, requesterID int, number string) *ent.Ticket {
	t.Helper()
	tkt, err := client.Ticket.Create().
		SetTicketNumber(number).
		SetTitle("Test Ticket " + number).
		SetDescription("desc").
		SetPriority("medium").
		SetStatus("open").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	return tkt
}

// TestEntRepository_GetByTicketID_CrossTenantIsolation proves a ServiceRequest linked to a
// ticket owned by tenant A is not returned when queried with tenant B's ID — CLAUDE.md requires
// a test whenever a tenant-scoped query is touched, and GetByTicketID is one of the two new/
// changed queries flagged in the final review (final review fix wave, Fix 7).
func TestEntRepository_GetByTicketID_CrossTenantIsolation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_repo_cross_tenant?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().SetName("Tenant A").SetCode("sr-repo-tenant-a").SetDomain("a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("Tenant B").SetCode("sr-repo-tenant-b").SetDomain("b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requesterA, err := client.User.Create().
		SetUsername("repo-requester-a").SetEmail("repo-requester-a@test.com").SetName("Requester A").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)

	ticketA := createTestTicketForSR(t, client, tenantA.ID, requesterA.ID, "TKT-CROSS-A")

	repo := NewEntRepository(client)
	created, err := repo.Create(ctx, &ServiceRequest{
		TenantID:           tenantA.ID,
		TicketID:           ticketA.ID,
		CatalogID:          1,
		RequesterID:        requesterA.ID,
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
	})
	require.NoError(t, err)

	// Same-tenant lookup succeeds.
	found, err := repo.GetByTicketID(ctx, ticketA.ID, tenantA.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)

	// Cross-tenant lookup must fail closed — tenant B must not be able to fetch tenant A's SR
	// by guessing/knowing the linked ticket ID.
	_, err = repo.GetByTicketID(ctx, ticketA.ID, tenantB.ID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err), "cross-tenant GetByTicketID must return a not-found error, got: %v", err)
}

// TestEntRepository_GetByTicketID_ExcludesSoftDeleted proves a soft-deleted ServiceRequest is
// no longer returned by GetByTicketID — it was missing the DeletedAtIsNil() filter that Get/List
// already apply in this same file (final review fix wave, Fix 8). Without this, a soft-deleted SR
// would still render the panel on its linked ticket's detail page.
func TestEntRepository_GetByTicketID_ExcludesSoftDeleted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_repo_soft_deleted?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("Tenant").SetCode("sr-repo-soft-delete").SetDomain("sd.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("repo-requester-sd").SetEmail("repo-requester-sd@test.com").SetName("Requester SD").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	ticket := createTestTicketForSR(t, client, tenant.ID, requester.ID, "TKT-SOFTDEL")

	repo := NewEntRepository(client)
	created, err := repo.Create(ctx, &ServiceRequest{
		TenantID:           tenant.ID,
		TicketID:           ticket.ID,
		CatalogID:          1,
		RequesterID:        requester.ID,
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
	})
	require.NoError(t, err)

	// Confirm it's visible before soft-delete.
	found, err := repo.GetByTicketID(ctx, ticket.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)

	// Soft-delete it directly (mirrors what Repository.Delete does — sets deleted_at).
	_, err = client.ServiceRequest.UpdateOneID(created.ID).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	_, err = repo.GetByTicketID(ctx, ticket.ID, tenant.ID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err), "soft-deleted ServiceRequest must not be returned by GetByTicketID, got: %v", err)
}
