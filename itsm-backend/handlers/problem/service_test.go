package problem

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/problem"
	"itsm-backend/repository/workitemnumber"

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
	return NewEntRepository(client, workitemnumber.NewPostgreSQLAllocator())
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
	p, err := service.Create(ctx, tenantID, &Problem{
		Title: "Repeated outage", Description: "Repeated production outage", Priority: "high", CreatedBy: userID,
	})
	require.NoError(t, err)
	return p
}

func TestProblemServiceLifecycleAndTimestamps(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "lifecycle")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "lifecycle")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	assert.Equal(t, "open", p.Status)
	p, err := service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "investigating"})
	require.NoError(t, err)
	assert.Nil(t, p.ResolvedAt)
	p, err = service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "resolved"})
	require.NoError(t, err)
	require.NotNil(t, p.ResolvedAt)
	p, err = service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "investigating"})
	require.NoError(t, err)
	assert.Nil(t, p.ResolvedAt)

	_, err = service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "unknown"})
	require.ErrorContains(t, err, "invalid problem status transition")
}

func TestProblemServiceAllocatesTenantScopedWorkItemNumbers(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenantA := createProblemHandlerTenant(t, ctx, client, "allocator-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "allocator-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "allocator-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "allocator-b")

	first, err := service.Create(ctx, tenantA.ID, &Problem{Title: "First tenant A problem", Priority: "high", CreatedBy: userA.ID})
	require.NoError(t, err)
	second, err := service.Create(ctx, tenantA.ID, &Problem{Title: "Second tenant A problem", Priority: "high", CreatedBy: userA.ID})
	require.NoError(t, err)
	otherTenant, err := service.Create(ctx, tenantB.ID, &Problem{Title: "First tenant B problem", Priority: "high", CreatedBy: userB.ID})
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

	require.NoError(t, service.Delete(ctx, p.ID, tenant.ID))
	_, err := service.Get(ctx, p.ID, tenant.ID)
	require.True(t, ent.IsNotFound(err))
	list, total, err := service.List(ctx, tenant.ID, 1, 10, nil)
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

	require.NoError(t, service.AddAssociations(ctx, tenantA.ID, p.ID, userA.ID, "ticket", []int{localTicket.ID, localTicket.ID}))
	err = service.AddAssociations(ctx, tenantA.ID, p.ID, userA.ID, "ticket", []int{foreignTicket.ID})
	require.ErrorContains(t, err, "current tenant")

	withAssociations, err := service.GetWithAssociations(ctx, p.ID, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, withAssociations.Tickets, 1)
	assert.Equal(t, localTicket.ID, withAssociations.Tickets[0].ID)
}

func TestProblemServiceCreateValidation(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "create-val")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "create-val")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "database")

	// 1. Title empty or whitespace
	_, err := service.Create(ctx, tenant.ID, &Problem{Title: "", Priority: "medium", CreatedBy: user.ID})
	require.ErrorContains(t, err, "problem title is required")

	_, err = service.Create(ctx, tenant.ID, &Problem{Title: "   ", Priority: "medium", CreatedBy: user.ID})
	require.ErrorContains(t, err, "problem title is required")

	// 2. Priority invalid
	_, err = service.Create(ctx, tenant.ID, &Problem{Title: "Valid Title", Priority: "invalid_priority", CreatedBy: user.ID})
	require.ErrorContains(t, err, "invalid problem priority: invalid_priority")

	_, err = service.Create(ctx, tenant.ID, &Problem{Title: "Valid Title", Priority: "", CreatedBy: user.ID})
	require.ErrorContains(t, err, "invalid problem priority:")

	// 3. Valid creation & trimming
	p, err := service.Create(ctx, tenant.ID, &Problem{
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

func TestProblemServiceStateMachineTransitions(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "sm-transitions")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "sm-transitions")

	// Test valid transitions table
	validCases := []struct {
		from string
		to   string
	}{
		{"open", "investigating"},
		{"open", "identified"},
		{"open", "resolved"},
		{"investigating", "identified"},
		{"investigating", "resolved"},
		{"identified", "investigating"},
		{"identified", "resolved"},
		{"resolved", "investigating"},
		{"resolved", "closed"},
		{"in_progress", "identified"},
		{"in_progress", "resolved"},
	}

	for i, tc := range validCases {
		p, err := service.Create(ctx, tenant.ID, &Problem{
			Title:     fmt.Sprintf("Problem SM %d", i),
			Priority:  "medium",
			CreatedBy: user.ID,
		})
		require.NoError(t, err)

		if tc.from != "open" {
			// Direct DB update to set starting state for test
			_, err = client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus(tc.from).Save(ctx)
			require.NoError(t, err)
		}

		updated, err := service.Update(ctx, tenant.ID, p.ID, &Problem{Status: tc.to})
		require.NoError(t, err, "Transition %s -> %s should be valid", tc.from, tc.to)
		assert.Equal(t, tc.to, updated.Status)
	}

	// Test illegal transitions
	invalidCases := []struct {
		from string
		to   string
	}{
		{"closed", "open"},
		{"closed", "investigating"},
		{"closed", "resolved"},
		{"resolved", "open"},
		{"open", "invalid_status"},
		{"investigating", "open"},
		{"open", "closed"},
		{"investigating", "closed"},
		{"identified", "closed"},
		{"in_progress", "closed"},
	}

	for i, tc := range invalidCases {
		p, err := service.Create(ctx, tenant.ID, &Problem{
			Title:     fmt.Sprintf("Invalid SM %d", i),
			Priority:  "low",
			CreatedBy: user.ID,
		})
		require.NoError(t, err)

		if tc.from != "open" {
			_, err = client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus(tc.from).Save(ctx)
			require.NoError(t, err)
		}

		_, err = service.Update(ctx, tenant.ID, p.ID, &Problem{Status: tc.to})
		require.ErrorContains(t, err, "invalid problem status transition", "Transition %s -> %s should fail", tc.from, tc.to)
	}
}

func TestProblemServiceUpdateRejectsDirectCloseUntilResolved(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "update-close")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "update-close")

	rejectedStatuses := []string{"open", "investigating", "identified", "in_progress"}
	for _, status := range rejectedStatuses {
		t.Run(status, func(t *testing.T) {
			p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)
			if status != "open" {
				_, err := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus(status).Save(ctx)
				require.NoError(t, err)
			}

			_, err := service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "closed"})
			require.ErrorContains(t, err, "invalid problem status transition")
		})
	}

	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)
	_, err := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	updated, err := service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "closed"})
	require.NoError(t, err)
	require.Equal(t, "closed", updated.Status)
	require.NotNil(t, updated.ClosedAt)
}

func TestProblemServiceCloseProblemRejectsUntilResolved(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "close-method")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "close-method")

	rejectedStatuses := []string{"open", "investigating", "identified", "in_progress"}
	for _, status := range rejectedStatuses {
		t.Run(status, func(t *testing.T) {
			p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)
			if status != "open" {
				_, err := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus(status).Save(ctx)
				require.NoError(t, err)
			}

			_, err := service.CloseProblem(ctx, tenant.ID, p.ID, "final resolution")
			require.ErrorContains(t, err, "invalid problem status transition")
		})
	}

	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)
	_, err := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	updated, err := service.CloseProblem(ctx, tenant.ID, p.ID, "final resolution")
	require.NoError(t, err)
	require.Equal(t, "closed", updated.Status)
	require.Equal(t, "final resolution", updated.Resolution)
	require.NotNil(t, updated.ClosedAt)
}

func TestProblemServiceInvestigationAndSolutions(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "investigate")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "investigate")

	p, err := service.Create(ctx, tenant.ID, &Problem{
		Title:     "Network Packet Drop",
		Priority:  "high",
		CreatedBy: user.ID,
	})
	require.NoError(t, err)

	// InvestigateProblem
	p1, err := service.InvestigateProblem(ctx, tenant.ID, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "investigating", p1.Status)

	// UpdateRootCause
	_, err = service.UpdateRootCause(ctx, tenant.ID, p.ID, "   ")
	require.ErrorContains(t, err, "rootCause is required")

	p2, err := service.UpdateRootCause(ctx, tenant.ID, p.ID, "Misconfigured MTU on switch")
	require.NoError(t, err)
	assert.Equal(t, "Misconfigured MTU on switch", p2.RootCause)

	// UpdateSolution
	_, err = service.UpdateSolution(ctx, tenant.ID, p.ID, "", "   ")
	require.ErrorContains(t, err, "solution, workaround or resolution is required")

	p3, err := service.UpdateSolution(ctx, tenant.ID, p.ID, "Reduce MTU to 1400", "Upgrade switch firmware")
	require.NoError(t, err)
	assert.Equal(t, "Reduce MTU to 1400", p3.Workaround)
	assert.Equal(t, "Upgrade switch firmware", p3.Resolution)

	_, err = service.Update(ctx, tenant.ID, p.ID, &Problem{Status: "resolved"})
	require.NoError(t, err)

	// CloseProblem
	p4, err := service.CloseProblem(ctx, tenant.ID, p.ID, "Firmware deployed and verified")
	require.NoError(t, err)
	assert.Equal(t, "closed", p4.Status)
	assert.Equal(t, "Firmware deployed and verified", p4.Resolution)
	require.NotNil(t, p4.ClosedAt)
}

func TestProblemServiceListAndFilters(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "list-filter")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "list-filter")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "auth")
	createProblemHandlerCategory(t, ctx, client, tenant.ID, "storage")

	p1, err := service.Create(ctx, tenant.ID, &Problem{
		Title: "CPU Spike in Auth Service", Priority: "critical", Category: "auth", CreatedBy: user.ID,
	})
	require.NoError(t, err)

	p2, err := service.Create(ctx, tenant.ID, &Problem{
		Title: "Disk Full on Node 2", Priority: "low", Category: "storage", CreatedBy: user.ID,
	})
	require.NoError(t, err)

	_, err = service.Update(ctx, tenant.ID, p2.ID, &Problem{Status: "resolved"})
	require.NoError(t, err)

	// List all
	list, total, err := service.List(ctx, tenant.ID, 1, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, list, 2)

	// Filter by status
	list, total, err = service.List(ctx, tenant.ID, 1, 10, map[string]interface{}{"status": "resolved"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p2.ID, list[0].ID)

	// Filter by priority
	list, total, err = service.List(ctx, tenant.ID, 1, 10, map[string]interface{}{"priority": "critical"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p1.ID, list[0].ID)

	// Filter by category
	list, total, err = service.List(ctx, tenant.ID, 1, 10, map[string]interface{}{"category": "storage"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p2.ID, list[0].ID)

	// Filter by keyword
	list, total, err = service.List(ctx, tenant.ID, 1, 10, map[string]interface{}{"keyword": "Auth Service"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, p1.ID, list[0].ID)

	// Pagination size limits
	list, total, err = service.List(ctx, tenant.ID, 0, 0, nil) // normalized to page 1, size 10
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

	incidentWorkItem, err := client.Ticket.Create().SetTitle("I1").SetType("incident").SetRecordClass("incident").
		SetTicketNumber("T-INC-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	incident1, err := client.Incident.Create().SetIncidentNumber("INC-001").
		SetWorkItemID(incidentWorkItem.ID).Save(ctx)
	require.NoError(t, err)

	changeWorkItem, err := client.Ticket.Create().SetTitle("C1").SetType("change").SetRecordClass("change_request").
		SetTicketNumber("T-CHG-001").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	change1, err := client.Change.Create().SetWorkItemID(changeWorkItem.ID).Save(ctx)
	require.NoError(t, err)

	// Add associations
	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "ticket", []int{ticket1.ID}))
	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "incident", []int{incident1.ID}))
	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "change", []int{change1.ID}))

	// Invalid related type
	err = service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "unknown", []int{1})
	require.ErrorContains(t, err, "unsupported related type")

	// Empty related IDs
	err = service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "ticket", []int{})
	require.ErrorContains(t, err, "at least one related id is required")

	// Verify loaded associations
	pWithAssoc, err := service.GetWithAssociations(ctx, p.ID, tenant.ID)
	require.NoError(t, err)
	assert.Len(t, pWithAssoc.Tickets, 1)
	assert.Len(t, pWithAssoc.Incidents, 1)
	assert.Len(t, pWithAssoc.Changes, 1)

	// Remove associations
	require.NoError(t, service.RemoveAssociation(ctx, tenant.ID, p.ID, "ticket", ticket1.ID))
	require.NoError(t, service.RemoveAssociation(ctx, tenant.ID, p.ID, "incident", incident1.ID))
	require.NoError(t, service.RemoveAssociation(ctx, tenant.ID, p.ID, "change", change1.ID))

	err = service.RemoveAssociation(ctx, tenant.ID, p.ID, "ticket", 0)
	require.ErrorContains(t, err, "invalid related id")

	err = service.RemoveAssociation(ctx, tenant.ID, p.ID, "unsupported", ticket1.ID)
	require.ErrorContains(t, err, "unsupported related type")

	pAfterRemove, err := service.GetWithAssociations(ctx, p.ID, tenant.ID)
	require.NoError(t, err)
	assert.Empty(t, pAfterRemove.Tickets)
	assert.Empty(t, pAfterRemove.Incidents)
	assert.Empty(t, pAfterRemove.Changes)
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
	_, err := service.Get(ctx, problemA.ID, tenantB.ID)
	require.True(t, ent.IsNotFound(err))

	_, err = service.GetWithAssociations(ctx, problemA.ID, tenantB.ID)
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to UPDATE Problem A
	_, err = service.Update(ctx, tenantB.ID, problemA.ID, &Problem{Title: "Hacked Title"})
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to DELETE Problem A
	err = service.Delete(ctx, problemA.ID, tenantB.ID)
	require.ErrorContains(t, err, "problem not found")

	// Tenant B tries to Investigate Problem A
	_, err = service.InvestigateProblem(ctx, tenantB.ID, problemA.ID)
	require.True(t, ent.IsNotFound(err))

	// Tenant B tries to Close Problem A
	_, err = service.CloseProblem(ctx, tenantB.ID, problemA.ID, "resolution")
	require.True(t, ent.IsNotFound(err))

	// Tenant B List should not include Problem A
	listB, totalB, err := service.List(ctx, tenantB.ID, 1, 10, nil)
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
	_, err := service.Create(ctx, tenant.ID, &Problem{Title: "P1", Priority: "critical", CreatedBy: user.ID})
	require.NoError(t, err)

	// Investigating + high
	p2, err := service.Create(ctx, tenant.ID, &Problem{Title: "P2", Priority: "high", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = service.InvestigateProblem(ctx, tenant.ID, p2.ID)
	require.NoError(t, err)

	// Resolved + medium
	p3, err := service.Create(ctx, tenant.ID, &Problem{Title: "P3", Priority: "medium", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = service.Update(ctx, tenant.ID, p3.ID, &Problem{Status: "resolved"})
	require.NoError(t, err)

	// Closed + low
	p4, err := service.Create(ctx, tenant.ID, &Problem{Title: "P4", Priority: "low", CreatedBy: user.ID})
	require.NoError(t, err)
	_, err = service.Update(ctx, tenant.ID, p4.ID, &Problem{Status: "resolved"})
	require.NoError(t, err)
	_, err = service.CloseProblem(ctx, tenant.ID, p4.ID, "Done")
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
