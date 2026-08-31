package service_request

import (
	"context"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/service_catalog"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func createServiceRequestFixture(t *testing.T, client *ent.Client, tenantID, requesterID, catalogID int, title, costCenter string) *ServiceRequest {
	t.Helper()
	ctx := context.Background()
	workItem, err := client.Ticket.Create().
		SetTicketNumber("TKT-" + strings.ReplaceAll(title, " ", "-")).
		SetTitle(title).
		SetDescription("test request").
		SetStatus("open").
		SetType("service_request").
		SetRecordClass("service_request_item").
		SetPriority("medium").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	extension, err := client.ServiceRequest.Create().
		SetTicketID(workItem.ID).
		SetCatalogID(catalogID).
		SetCostCenter(costCenter).
		Save(ctx)
	require.NoError(t, err)
	created, err := NewEntRepository(client).Get(ctx, extension.ID, tenantID)
	require.NoError(t, err)
	return created
}
func TestService_GetByTicketID_ReturnsLinkedServiceRequest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_get_by_ticket?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-get-by-ticket").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester6").SetEmail("requester6@test.com").SetName("Requester6").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-getbyticket", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "", service_catalog.TargetClassServiceRequestItem)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	created := createServiceRequestFixture(t, client, tenant.ID, requester.ID, catalog.ID, "申请一台云主机-GetByTicket", "CC-GETBYTICKET")
	require.Greater(t, created.TicketID, 0)

	fetched, err := svc.GetByTicketID(ctx, created.TicketID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, catalog.ID, fetched.CatalogID)
	assert.Equal(t, "CC-GETBYTICKET", fetched.CostCenter)
	assert.Equal(t, created.TicketID, fetched.TicketID)
}

// TestService_List_BatchLoadsLinkedTicketSummary 证明 List 给每条记录批量回填了关联 ticket
// 的 title/status（/my-requests 列表页据此展示，因为 ServiceRequest 表本身已经不存 status/title
// 了——委托给 Ticket）。批量加载：一次 Ticket.Query().Where(IDIn(...)) 而不是逐条查。
func TestService_List_BatchLoadsLinkedTicketSummary(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_list_ticket_summary?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-list-summary").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester7").SetEmail("requester7@test.com").SetName("Requester7").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-list", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "", service_catalog.TargetClassServiceRequestItem)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	created := createServiceRequestFixture(t, client, tenant.ID, requester.ID, catalog.ID, "申请一台云主机-List测试", "")
	require.Greater(t, created.TicketID, 0)

	list, total, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, "申请一台云主机-List测试", list[0].TicketTitle)
	assert.NotEmpty(t, list[0].TicketStatus)

	tkt, err := client.Ticket.Get(ctx, created.TicketID)
	require.NoError(t, err)
	assert.Equal(t, tkt.Status, list[0].TicketStatus)
}

// TestService_AttachTicketSummaries_DoesNotLeakCrossTenant proves attachTicketSummaries (the
// batch-load helper List uses to fill in TicketTitle/TicketStatus) does not leak a tenant-B
// ticket's title/status into a tenant-A service request's list entry — CLAUDE.md requires a test
// for any tenant-scoped query added/changed (final review fix wave, Fix 7). Ent IDs are global
// (not tenant-scoped sequences), so it's plausible for a tenant-A record's ticket_id column to
// numerically collide with a real ticket ID owned by tenant B; the batch query must still filter
// by tenant.
func TestService_AttachTicketSummaries_DoesNotLeakCrossTenant(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_attach_summaries_cross_tenant?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().SetName("Tenant A").SetCode("sr-attach-tenant-a").SetDomain("a2.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("Tenant B").SetCode("sr-attach-tenant-b").SetDomain("b2.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requesterB, err := client.User.Create().
		SetUsername("attach-requester-b").SetEmail("attach-requester-b@test.com").SetName("Requester B").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenantB.ID).Save(ctx)
	require.NoError(t, err)

	// A real ticket that belongs to tenant B, with a title that must never surface on a
	// tenant-A list entry.
	ticketB, err := client.Ticket.Create().
		SetTicketNumber("TKT-ATTACH-B").
		SetTitle("Tenant B 私密工单标题").
		SetDescription("desc").
		SetPriority("medium").
		SetStatus("open").
		SetRequesterID(requesterB.ID).
		SetTenantID(tenantB.ID).
		Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	// A tenant-A ServiceRequest whose ticket_id happens to equal tenant B's ticket ID —
	// simulating the collision scenario without depending on ent's ID allocation order.
	list := []*ServiceRequest{
		{ID: 1, TenantID: tenantA.ID, TicketID: ticketB.ID},
	}

	svc.attachTicketSummaries(ctx, tenantA.ID, list)

	assert.Empty(t, list[0].TicketTitle, "must not leak tenant B's ticket title into a tenant A list entry")
	assert.Empty(t, list[0].TicketStatus, "must not leak tenant B's ticket status into a tenant A list entry")
}

// TestServiceRequest_ApprovalDegradedToSingleNodeBPMN locks in, as an explicit assertion rather
// than a silent omission, that the original 3-level approval design (currentLevel/totalLevels,
// see the design docs under prd/) has intentionally degraded to single-node BPMN approval as
// part of this delegation refactor: ServiceRequest no longer tracks approval level/step at all —
// that responsibility now belongs entirely to the linked Ticket's BPMN process instance. This is
// a documented transitional decision, not an oversight (final review fix wave, Fix 9).
