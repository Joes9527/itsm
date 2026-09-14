package problem_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/handlers/shared/workitemmutation"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/problem"
	creation "itsm-backend/handlers/common/workitemcreation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupProblemHandlerTest(t *testing.T) (*ent.Client, *Service, context.Context) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:problem-handler-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	repo := newTestProblemRepository(client)
	return client, NewService(repo, zaptest.NewLogger(t).Sugar()), context.Background()
}

func newTestProblemRepository(client *ent.Client) *EntRepository {
	return NewEntRepository(client)
}

func createProblemHandlerTenant(t *testing.T, ctx context.Context, client *ent.Client, suffix string) *ent.Tenant {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("Problem Tenant " + suffix).
		SetCode("problem-" + suffix).
		SetDomain("problem-" + suffix + ".example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	configureProblemIntakeFixture(ctx, client, tenant.ID)
	return tenant
}

func createProblemHandlerUser(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, suffix string) *ent.User {
	t.Helper()
	user, err := client.User.Create().
		SetUsername("problem-" + suffix).
		SetEmail("problem-" + suffix + "@example.com").
		SetName("Problem User").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func createProblemHandlerCategory(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, name string) *ent.TicketCategory {
	t.Helper()
	category, err := client.TicketCategory.Create().SetName(name).SetCode(name).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return category
}

func createProblemHandlerProblem(t *testing.T, ctx context.Context, service *Service, tenantID, userID int) *Problem {
	t.Helper()
	p, err := service.SubmitCreation(ctx, tenantID, &Problem{
		Title: "Repeated outage", Description: "Repeated production outage", Priority: "high", CreatedBy: userID,
	})
	require.NoError(t, err)
	return p
}

func TestProblemServiceAllocatesTenantScopedWorkItemNumbers(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenantA := createProblemHandlerTenant(t, ctx, client, "allocator-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "allocator-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "allocator-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "allocator-b")

	first, err := service.SubmitCreation(ctx, tenantA.ID, &Problem{Title: "First tenant A problem", Priority: "high", CreatedBy: userA.ID})
	require.NoError(t, err)
	second, err := service.SubmitCreation(ctx, tenantA.ID, &Problem{Title: "Second tenant A problem", Priority: "high", CreatedBy: userA.ID})
	require.NoError(t, err)
	otherTenant, err := service.SubmitCreation(ctx, tenantB.ID, &Problem{Title: "First tenant B problem", Priority: "high", CreatedBy: userB.ID})
	require.NoError(t, err)

	firstWorkItem, err := client.Ticket.Get(ctx, *first.WorkItemID)
	require.NoError(t, err)
	secondWorkItem, err := client.Ticket.Get(ctx, *second.WorkItemID)
	require.NoError(t, err)
	otherTenantWorkItem, err := client.Ticket.Get(ctx, *otherTenant.WorkItemID)
	require.NoError(t, err)
	period := time.Now().UTC().Format("200601")
	require.Equal(t, "TKT-"+period+"-000001", firstWorkItem.TicketNumber)
	require.Equal(t, "TKT-"+period+"-000002", secondWorkItem.TicketNumber)
	require.Equal(t, "TKT-"+period+"-000001", otherTenantWorkItem.TicketNumber)
	require.Equal(t, tenantA.ID, firstWorkItem.TenantID)
	require.Equal(t, tenantB.ID, otherTenantWorkItem.TenantID)
}

func TestProblemRepositorySoftDeleteExcludedEverywhere(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "delete")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "delete")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	require.NoError(t, service.Delete(ctx, p.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID}))
	_, err := service.Get(ctx, p.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"})
	require.True(t, ent.IsNotFound(err))
	list, total, err := service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, nil)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, list)
	stats, err := service.GetStats(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Zero(t, stats.Total)

	stored, err := client.Problem.Query().Where(problem.ID(p.ID)).WithWorkItem().Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, stored.Edges.WorkItem.DeletedAt)
}

func TestProblemAssociationsEnforceTenantBoundary(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenantA := createProblemHandlerTenant(t, ctx, client, "association-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "association-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "association-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "association-b")
	p := createProblemHandlerProblem(t, ctx, service, tenantA.ID, userA.ID)

	localTicket, err := client.Ticket.Create().
		SetTitle("Local ticket").SetTicketNumber("PRB-LOCAL").SetRequesterID(userA.ID).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)
	foreignTicket, err := client.Ticket.Create().
		SetTitle("Foreign ticket").SetTicketNumber("PRB-FOREIGN").SetRequesterID(userB.ID).SetTenantID(tenantB.ID).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, applyProblemRelation(service, ctx, tenantA.ID, userA.ID, *p.WorkItemID, localTicket.ID, 1, "related_to", "local-link", false))
	err = applyProblemRelation(service, ctx, tenantA.ID, userA.ID, *p.WorkItemID, foreignTicket.ID, 2, "related_to", "foreign-link", false)
	require.Error(t, err)

	withAssociations, err := service.Get(ctx, p.ID, workitemmutation.Meta{TenantID: tenantA.ID, ActorID: userA.ID, Source: "http"})
	require.NoError(t, err)
	require.Len(t, withAssociations.Relations, 1)
	assert.Equal(t, localTicket.ID, withAssociations.Relations[0].Target.WorkItemID)
}

func TestProblemServiceCreateValidation(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "create-val")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "create-val")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "database")

	// 1. Title empty or whitespace
	_, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "", Priority: "medium", CreatedBy: user.ID})
	require.ErrorIs(t, err, creation.ErrInvalidCommand)

	_, err = service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "   ", Priority: "medium", CreatedBy: user.ID})
	require.ErrorIs(t, err, creation.ErrInvalidCommand)

	// 2. Priority invalid
	_, err = service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "Valid Title", Priority: "invalid_priority", CreatedBy: user.ID})
	require.ErrorIs(t, err, creation.ErrDomainValidationFailed)

	defaulted, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "Valid Title", Priority: "", CreatedBy: user.ID})
	require.NoError(t, err)
	require.Equal(t, "medium", defaulted.Priority)

	// 3. Valid creation & trimming
	p, err := service.SubmitCreation(ctx, tenant.ID, &Problem{
		Title:       "   Database Memory Leak   ",
		Description: "OOM killer triggered on db host",
		Priority:    "critical",
		Category:    "database",
		Impact:      "high",
		CreatedBy:   user.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "Database Memory Leak", p.Title)
	assert.Equal(t, "open", p.Status)
	assert.Equal(t, tenant.ID, p.TenantID)
	assert.Equal(t, "critical", p.Priority)
}

func TestProblemServiceListAndFilters(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "list-filter")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "list-filter")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "auth")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "storage")

	p1, err := service.SubmitCreation(ctx, tenant.ID, &Problem{
		Title: "CPU Spike in Auth Service", Priority: "critical", Category: "auth", CreatedBy: user.ID,
	})
	require.NoError(t, err)

	p2, err := service.SubmitCreation(ctx, tenant.ID, &Problem{
		Title: "Disk Full on Node 2", Priority: "low", Category: "storage", CreatedBy: user.ID,
	})
	require.NoError(t, err)

	_, err = client.Ticket.UpdateOneID(*p2.WorkItemID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	// List all
	list, total, err := service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, list, 2)

	// Filter by status
	list, total, err = service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, map[string]interface{}{"status": "resolved"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p2.ID, list[0].ID)

	// Filter by priority
	list, total, err = service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, map[string]interface{}{"priority": "critical"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p1.ID, list[0].ID)

	// Filter by category
	list, total, err = service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, map[string]interface{}{"category": "storage"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p2.ID, list[0].ID)

	// Filter by keyword
	list, total, err = service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 1, 10, map[string]interface{}{"keyword": "Auth Service"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p1.ID, list[0].ID)

	// Pagination size limits
	_, total, err = service.List(ctx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID, Source: "http"}, 0, 0, nil) // normalized to page 1, size 10
	require.NoError(t, err)
	assert.Equal(t, 2, total)
}

func TestProblemServiceAssociationsLifecycle(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "assoc-lc")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "assoc-lc")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	ticket1, err := client.Ticket.Create().
		SetTitle("T1").SetTicketNumber("T-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	incidentWorkItem, err := client.Ticket.Create().SetTitle("I1").SetRecordClass("incident").
		SetTicketNumber("T-INC-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	incident1, err := client.Incident.Create().
		SetWorkItemID(incidentWorkItem.ID).Save(ctx)
	require.NoError(t, err)

	changeWorkItem, err := client.Ticket.Create().SetTitle("C1").SetRecordClass("change_request").
		SetTicketNumber("T-CHG-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	change1, err := client.Change.Create().SetWorkItemID(changeWorkItem.ID).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, ticket1.ID, 1, "related_to", "ticket-add", false))
	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, incident1.WorkItemID, *p.WorkItemID, 1, "investigated_by", "incident-add", false))
	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, change1.WorkItemID, 2, "resolved_by_change", "change-add", false))
	err = applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, ticket1.ID, 3, "unknown", "unknown", false)
	require.Error(t, err)
	err = applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, 0, 3, "related_to", "empty", false)
	require.Error(t, err)
	pWithAssoc, err := service.Get(ctx, p.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID})
	require.NoError(t, err)
	require.Len(t, pWithAssoc.Relations, 3)
	types := []string{}
	for _, v := range pWithAssoc.Relations {
		types = append(types, v.Type)
	}
	assert.ElementsMatch(t, []string{"related_to", "investigated_by", "resolved_by_change"}, types)
	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, ticket1.ID, 3, "related_to", "ticket-remove", true))
	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, incident1.WorkItemID, *p.WorkItemID, 2, "investigated_by", "incident-remove", true))
	require.NoError(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, change1.WorkItemID, 4, "resolved_by_change", "change-remove", true))
	require.Error(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, 0, 5, "related_to", "zero-remove", true))
	require.Error(t, applyProblemRelation(service, ctx, tenant.ID, user.ID, *p.WorkItemID, ticket1.ID, 5, "unsupported", "bad-remove", true))
	after, err := service.Get(ctx, p.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: user.ID})
	require.NoError(t, err)
	assert.Empty(t, after.Relations)
}

func TestProblemServiceCrossTenantIsolation(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()

	tenantA := createProblemHandlerTenant(t, ctx, client, "iso-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "iso-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "iso-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "iso-b")

	problemA := createProblemHandlerProblem(t, ctx, service, tenantA.ID, userA.ID)
	problemB := createProblemHandlerProblem(t, ctx, service, tenantB.ID, userB.ID)

	// Tenant B tries to GET Problem A
	_, err := service.Get(ctx, problemA.ID, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID, Source: "http"})
	require.True(t, ent.IsNotFound(err))

	_, err = service.Get(ctx, problemA.ID, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID, Source: "http"})
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to UPDATE Problem A
	_, err = service.Update(ctx, tenantB.ID, problemA.ID, &Problem{Title: "Hacked Title"})
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to DELETE Problem A
	err = service.Delete(ctx, problemA.ID, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID})
	require.ErrorContains(t, err, "problem not found")

	// Tenant B tries to Investigate Problem A
	_, err = service.Get(ctx, problemA.ID, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID, Source: "http"})
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to Close Problem A
	_, err = service.Get(ctx, problemA.ID, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID, Source: "http"})
	require.True(t, ent.IsNotFound(err))

	// Tenant B List should not include Problem A
	listB, totalB, err := service.List(ctx, workitemmutation.Meta{TenantID: tenantB.ID, ActorID: userB.ID, Source: "http"}, 1, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, totalB)
	assert.Equal(t, problemB.ID, listB[0].ID)
}

func TestProblemServiceStats(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()

	tenant := createProblemHandlerTenant(t, ctx, client, "stats")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "stats")

	// Open + critical
	_, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "P1", Priority: "critical", CreatedBy: user.ID})
	require.NoError(t, err)

	// Investigating + high
	p2, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "P2", Priority: "high", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(*p2.WorkItemID).SetStatus("investigating").Save(ctx)
	require.NoError(t, err)

	// Resolved + medium
	p3, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "P3", Priority: "medium", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(*p3.WorkItemID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	// Closed + low
	p4, err := service.SubmitCreation(ctx, tenant.ID, &Problem{Title: "P4", Priority: "low", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(*p4.WorkItemID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(*p4.WorkItemID).SetStatus("closed").Save(ctx)
	require.NoError(t, err)

	stats, err := service.GetStats(ctx, tenant.ID)
	require.NoError(t, err)

	assert.Equal(t, 4, stats.Total)
	assert.Equal(t, 1, stats.Open)
	assert.Equal(t, 1, stats.InProgress)
	assert.Equal(t, 1, stats.Resolved)
	assert.Equal(t, 1, stats.Closed)
	assert.Equal(t, 2, stats.HighPriority) // critical + high
}
