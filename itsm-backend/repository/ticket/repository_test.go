package ticket

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/repository/base"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// CreateParams is a test-only fixture shape mirroring the retired repository-level
// creation params. Real Ticket creation now goes exclusively through the shared
// Intake application (handlers/intake, tests/integration/unified_intake_fixture_test.go,
// service/ticket_service_test.go); this package's own Repository no longer owns a
// Create method, only Get/Update/Delete/List/etc, so these fixture params only need
// to carry enough fields to seed a row directly through ent for those tests.
type CreateParams struct {
	Title             string
	Description       string
	RecordClass       string
	Priority          Priority
	RequesterID       int
	ParentTicketID    *int
	CustomFieldValues map[string]interface{}
	Source            string
	CreatorEmail      string
	ExternalMessageID string
}

var repoTestTicketNumberSeq int64

func nextRepoTestTicketNumber() string {
	n := atomic.AddInt64(&repoTestTicketNumberSeq, 1)
	return fmt.Sprintf("TKT-%s-%06d", time.Now().UTC().Format("200601"), n)
}

// repoFixture sets up an in-memory SQLite repo for testing.
type repoFixture struct {
	ctx    context.Context
	client *ent.Client
	repo   Repository
	tenant *ent.Tenant
	user   *ent.User
}

func newRepoFixture(t *testing.T) *repoFixture {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:repo_test?mode=memory&cache=shared&_fk=1")
	logger := zaptest.NewLogger(t).Sugar()
	repo := NewEntRepository(client, logger)

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("alice").
		SetEmail("alice@test.com").
		SetName("Alice").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return &repoFixture{ctx: ctx, client: client, repo: repo, tenant: tenant, user: user}
}

// createTicket seeds a Ticket row directly through ent (bypassing the retired
// repository-level Create/allocator API) and reads it back through the real
// Repository.GetByID, so the rest of these tests keep exercising the same
// domain model shape/mapping as before.
func (fx *repoFixture) createTicket(ctx context.Context, params *CreateParams, tenantID int) (*Ticket, error) {
	builder := fx.client.Ticket.Create().
		SetTitle(params.Title).
		SetDescription(params.Description).
		SetRecordClass(string(params.RecordClass)).
		SetPriority(string(params.Priority)).
		SetStatus(string(StatusNew)).
		SetTicketNumber(nextRepoTestTicketNumber()).
		SetRequesterID(params.RequesterID).
		SetTenantID(tenantID)
	if params.ParentTicketID != nil {
		builder.SetParentTicketID(*params.ParentTicketID)
	}
	if len(params.CustomFieldValues) > 0 {
		builder.SetCustomFieldValues(params.CustomFieldValues)
	}
	if params.Source != "" {
		builder.SetSource(params.Source)
	}
	if params.CreatorEmail != "" {
		builder.SetCreatorEmail(params.CreatorEmail)
	}
	if params.ExternalMessageID != "" {
		builder.SetExternalMessageID(params.ExternalMessageID)
	}
	created, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	return fx.repo.GetByID(ctx, created.ID, tenantID)
}

// =====================================================================
// GetByID
// =====================================================================

func TestRepository_GetByID(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Get Test",
		Description: "Desc",
		Priority:    PriorityHigh,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	found, err := fx.repo.GetByID(fx.ctx, created.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Get Test", found.Title)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	_, err := fx.repo.GetByID(fx.ctx, 99999, fx.tenant.ID)
	assert.Error(t, err)
}

func TestRepository_GetByID_WrongTenant(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Tenant Isolation",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	_, err := fx.repo.GetByID(fx.ctx, created.ID, 99999)
	assert.Error(t, err)
}

// =====================================================================
// GetByNumber
// =====================================================================

func TestRepository_GetByNumber(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "By Number",
		Description: "",
		Priority:    PriorityLow,
		RecordClass: "service_request_item",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	found, err := fx.repo.GetByNumber(fx.ctx, created.TicketNumber, fx.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, created.TicketNumber, found.TicketNumber)
}

func TestRepository_GetByNumber_NotFound(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	_, err := fx.repo.GetByNumber(fx.ctx, "TKT-DOES-NOT-EXIST", fx.tenant.ID)
	assert.Error(t, err)
}

func TestRepository_GetByNumber_WrongTenant(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "By Number Tenant",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	_, err := fx.repo.GetByNumber(fx.ctx, created.TicketNumber, 99999)
	assert.Error(t, err)
}

// =====================================================================
// Update
// =====================================================================

func TestRepository_Update(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Update Me",
		Description: "Original",
		Priority:    PriorityLow,
		RecordClass: "generic",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	title := "Updated Title"
	desc := "Updated Description"

	updated, err := fx.repo.Update(fx.ctx, created.ID, &UpdateParams{
		Title:       &title,
		Description: &desc,
		Priority:    func() *Priority { p := PriorityCritical; return &p }(),
		Version:     created.Version,
	}, fx.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Title", updated.Title)
	assert.Equal(t, "Updated Description", updated.Description)
	assert.Equal(t, PriorityCritical, updated.Priority)
}

// UpdateTx must join the caller transaction: a later failure must undo the
// ticket CAS, newly created tag and replacement of existing tag relations.
func TestRepository_UpdateTx_CallerOwnsCommit(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			fx := newRepoFixture(t)
			defer fx.client.Close()
			writer, ok := fx.repo.(interface {
				UpdateTx(context.Context, *ent.Tx, int, *UpdateParams, int) (*Ticket, error)
			})
			require.True(t, ok, "repository must support caller-owned update transactions")
			created, err := fx.createTicket(fx.ctx, &CreateParams{Title: "Original", RecordClass: "generic", Priority: PriorityLow, RequesterID: fx.user.ID}, fx.tenant.ID)
			require.NoError(t, err)
			originalTag, err := fx.client.TicketTag.Create().SetName("original").SetTenantID(fx.tenant.ID).Save(fx.ctx)
			require.NoError(t, err)
			require.NoError(t, fx.client.Ticket.UpdateOneID(created.ID).AddTagIDs(originalTag.ID).Exec(fx.ctx))
			tx, err := fx.client.Tx(fx.ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			addedTag, err := tx.TicketTag.Create().SetName("new").SetTenantID(fx.tenant.ID).Save(fx.ctx)
			require.NoError(t, err)
			title := "Updated in caller transaction"
			updated, err := writer.UpdateTx(fx.ctx, tx, created.ID, &UpdateParams{Title: &title, Version: created.Version, ReplaceTags: true, TagIDs: []int{addedTag.ID}}, fx.tenant.ID)
			require.NoError(t, err)
			require.Equal(t, created.Version+1, updated.Version)
			inside, err := tx.Ticket.Get(fx.ctx, created.ID)
			require.NoError(t, err)
			require.Equal(t, title, inside.Title)
			ids, err := inside.QueryTags().IDs(fx.ctx)
			require.NoError(t, err)
			require.Equal(t, []int{addedTag.ID}, ids)
			if commit {
				require.NoError(t, tx.Commit())
			} else {
				require.NoError(t, tx.Rollback())
			}
			outside, err := fx.client.Ticket.Get(fx.ctx, created.ID)
			require.NoError(t, err)
			ids, err = outside.QueryTags().IDs(fx.ctx)
			require.NoError(t, err)
			count, err := fx.client.TicketTag.Query().Count(fx.ctx)
			require.NoError(t, err)
			if commit {
				assert.Equal(t, title, outside.Title)
				assert.Equal(t, created.Version+1, outside.Version)
				assert.Equal(t, []int{addedTag.ID}, ids)
				assert.Equal(t, 2, count)
			} else {
				assert.Equal(t, created.Title, outside.Title)
				assert.Equal(t, created.Version, outside.Version)
				assert.Equal(t, []int{originalTag.ID}, ids)
				assert.Equal(t, 1, count)
			}
			_, err = writer.UpdateTx(fx.ctx, nil, created.ID, &UpdateParams{Version: outside.Version}, fx.tenant.ID)
			require.Error(t, err, "nil transaction must never fall back to autocommit")
		})
	}
}

func TestRepository_Update_NotFound(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	title := "Should Fail"
	_, err := fx.repo.Update(fx.ctx, 99999, &UpdateParams{
		Title: &title,
	}, fx.tenant.ID)
	assert.Error(t, err)
}

func TestRepository_Update_RejectsStaleVersion(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()
	created, err := fx.createTicket(fx.ctx, &CreateParams{
		Title: "Original", Priority: PriorityMedium, RecordClass: "generic", RequesterID: fx.user.ID,
	}, fx.tenant.ID)
	require.NoError(t, err)
	title := "Must not overwrite"
	_, err = fx.repo.Update(fx.ctx, created.ID, &UpdateParams{Title: &title, Version: created.Version + 1}, fx.tenant.ID)
	require.ErrorContains(t, err, "version conflict")
	unchanged, err := fx.repo.GetByID(fx.ctx, created.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "Original", unchanged.Title)
}

// =====================================================================
// Delete
// =====================================================================

// =====================================================================
// List
// =====================================================================

func TestRepository_List(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	for i := 0; i < 5; i++ {
		fx.createTicket(fx.ctx, &CreateParams{
			Title:       "List Ticket",
			Description: "",
			Priority:    PriorityMedium,
			RecordClass: "incident",
			RequesterID: fx.user.ID,
		}, fx.tenant.ID)
	}

	result, err := fx.repo.List(fx.ctx, fx.tenant.ID, &FilterParams{}, &base.QueryParams{})
	require.NoError(t, err)
	assert.Equal(t, 5, result.Total)
	assert.Len(t, result.Data, 5)
}

func TestRepository_List_Pagination(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	for i := 0; i < 10; i++ {
		fx.createTicket(fx.ctx, &CreateParams{
			Title:       "Page Ticket",
			Description: "",
			Priority:    PriorityLow,
			RecordClass: "incident",
			RequesterID: fx.user.ID,
		}, fx.tenant.ID)
	}

	result, err := fx.repo.List(fx.ctx, fx.tenant.ID, &FilterParams{}, &base.QueryParams{Page: 1, PageSize: 3})
	require.NoError(t, err)
	assert.Equal(t, 10, result.Total)
	assert.Len(t, result.Data, 3)
}

func TestRepository_List_TenantIsolation(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Tenant1 Ticket",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	result, err := fx.repo.List(fx.ctx, 99999, &FilterParams{}, &base.QueryParams{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Total)
	assert.Len(t, result.Data, 0)
}

func TestRepository_List_ParentTypeAndOverdueFilters(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()
	parent, err := fx.createTicket(fx.ctx, &CreateParams{
		Title: "Parent", Priority: PriorityMedium, RecordClass: "incident", RequesterID: fx.user.ID,
	}, fx.tenant.ID)
	require.NoError(t, err)
	overdue, err := fx.createTicket(fx.ctx, &CreateParams{
		Title: "Overdue problem", Priority: PriorityHigh, RecordClass: "problem", RequesterID: fx.user.ID, ParentTicketID: &parent.ID,
	}, fx.tenant.ID)
	require.NoError(t, err)
	resolved, err := fx.createTicket(fx.ctx, &CreateParams{
		Title: "Resolved problem", Priority: PriorityHigh, RecordClass: "problem", RequesterID: fx.user.ID, ParentTicketID: &parent.ID,
	}, fx.tenant.ID)
	require.NoError(t, err)
	past := time.Now().Add(-time.Hour)
	_, err = fx.client.Ticket.UpdateOneID(overdue.ID).SetSLAResolutionDeadline(past).Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.Ticket.UpdateOneID(resolved.ID).SetSLAResolutionDeadline(past).SetStatus(string(StatusResolved)).Save(fx.ctx)
	require.NoError(t, err)

	problemType := string("problem")
	result, err := fx.repo.List(fx.ctx, fx.tenant.ID, &FilterParams{
		RecordClass: &problemType, ParentTicketID: &parent.ID, IsOverdue: true,
	}, &base.QueryParams{})
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, overdue.ID, result.Data[0].ID)
}

// =====================================================================
// BatchDelete
// =====================================================================

// =====================================================================
// Exists
// =====================================================================

func TestRepository_Exists(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Exists Check",
		Description: "",
		Priority:    PriorityLow,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	exists, err := fx.repo.Exists(fx.ctx, created.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = fx.repo.Exists(fx.ctx, 99999, fx.tenant.ID)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRepository_Exists_WrongTenant(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Exists Tenant",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	exists, err := fx.repo.Exists(fx.ctx, created.ID, 99999)
	require.NoError(t, err)
	assert.False(t, exists)
}

// =====================================================================
// UpdateStatus
// =====================================================================

func TestRepository_UpdateStatus(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Status Update",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "generic",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	updated, err := fx.repo.UpdateStatus(fx.ctx, created.ID, StatusOpen, fx.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusOpen, updated.Status)
}

func TestRepository_UpdateStatus_NotFound(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	_, err := fx.repo.UpdateStatus(fx.ctx, 99999, StatusOpen, fx.tenant.ID)
	assert.Error(t, err)
}

// =====================================================================
// AssignTicket
// =====================================================================

func TestRepository_AssignTicket(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	created, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Assign Test",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "generic",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	updated, err := fx.repo.AssignTicket(fx.ctx, created.ID, fx.user.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.NotNil(t, updated.AssigneeID)
	assert.Equal(t, fx.user.ID, *updated.AssigneeID)
}

func TestRepository_AssignTicket_NotFound(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	_, err := fx.repo.AssignTicket(fx.ctx, 99999, fx.user.ID, fx.tenant.ID)
	assert.Error(t, err)
}

// =====================================================================
// CountByStatus / CountByPriority
// =====================================================================

func TestRepository_CountByStatus(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	for i := 0; i < 3; i++ {
		tkt, _ := fx.createTicket(fx.ctx, &CreateParams{
			Title:       "Count Status",
			Description: "",
			Priority:    PriorityMedium,
			RecordClass: "generic",
			RequesterID: fx.user.ID,
		}, fx.tenant.ID)
		fx.repo.UpdateStatus(fx.ctx, tkt.ID, StatusOpen, fx.tenant.ID)
	}

	fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Count Status New",
		Description: "",
		Priority:    PriorityLow,
		RecordClass: "generic",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	counts, err := fx.repo.CountByStatus(fx.ctx, fx.tenant.ID)
	require.NoError(t, err)
	assert.Contains(t, counts, StatusNew)
	assert.Contains(t, counts, StatusOpen)
}

func TestRepository_CountByPriority(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Priority Count",
		Description: "",
		Priority:    PriorityCritical,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	counts, err := fx.repo.CountByPriority(fx.ctx, fx.tenant.ID)
	require.NoError(t, err)
	assert.Contains(t, counts, PriorityCritical)
}

// =====================================================================
// FindByAssignee / FindByRequester
// =====================================================================

func TestRepository_FindByAssignee(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	tkt, _ := fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Assign Find",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "generic",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)
	fx.repo.AssignTicket(fx.ctx, tkt.ID, fx.user.ID, fx.tenant.ID)

	tickets, err := fx.repo.FindByAssignee(fx.ctx, fx.user.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(tickets), 1)
}

func TestRepository_FindByRequester(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Requester Find",
		Description: "",
		Priority:    PriorityLow,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	tickets, err := fx.repo.FindByRequester(fx.ctx, fx.user.ID, fx.tenant.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(tickets), 1)
}

func TestRepository_FindByRequester_WrongTenant(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()

	fx.createTicket(fx.ctx, &CreateParams{
		Title:       "Requester Tenant",
		Description: "",
		Priority:    PriorityMedium,
		RecordClass: "incident",
		RequesterID: fx.user.ID,
	}, fx.tenant.ID)

	tickets, err := fx.repo.FindByRequester(fx.ctx, fx.user.ID, 99999)
	require.NoError(t, err)
	assert.Empty(t, tickets)
}

// =====================================================================
// State Machine (domain model)
// =====================================================================

func TestTicketModel_CanTransitionTo(t *testing.T) {
	tests := []struct {
		from     Status
		to       Status
		expected bool
	}{
		{StatusNew, StatusOpen, true},
		{StatusNew, StatusCancelled, true},
		{StatusNew, StatusResolved, false},
		{StatusOpen, StatusInProgress, true},
		{StatusOpen, StatusPending, true},
		{StatusOpen, StatusResolved, true},
		{StatusInProgress, StatusPending, true},
		{StatusInProgress, StatusResolved, true},
		{StatusResolved, StatusClosed, true},
		{StatusResolved, StatusOpen, true}, // Reopen
		{StatusClosed, StatusOpen, false},  // Cannot reopen from closed
		{StatusCancelled, StatusOpen, false},
	}

	for _, tt := range tests {
		name := string(tt.from) + "_to_" + string(tt.to)
		t.Run(name, func(t *testing.T) {
			model := &Ticket{Status: tt.from}
			result := model.CanTransitionTo(tt.to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTicketModel_IsFinalState(t *testing.T) {
	assert.True(t, (&Ticket{Status: StatusClosed}).IsFinalState())
	assert.True(t, (&Ticket{Status: StatusCancelled}).IsFinalState())
	assert.False(t, (&Ticket{Status: StatusNew}).IsFinalState())
	assert.False(t, (&Ticket{Status: StatusOpen}).IsFinalState())
	assert.False(t, (&Ticket{Status: StatusResolved}).IsFinalState())
}

func TestTicketModel_StateError(t *testing.T) {
	err := &StateError{CurrentStatus: StatusNew, Message: "cannot resolve ticket from current status"}
	assert.Contains(t, err.Error(), "cannot resolve ticket")
}

func TestRepositoryCanonicalIdentityFilters(t *testing.T) {
	fx := newRepoFixture(t)
	defer fx.client.Close()
	incident, err := fx.createTicket(fx.ctx, &CreateParams{Title: "incident", RecordClass: "incident", Priority: PriorityMedium, RequesterID: fx.user.ID}, fx.tenant.ID)
	require.NoError(t, err)
	generic, err := fx.createTicket(fx.ctx, &CreateParams{Title: "generic", RecordClass: "generic", Priority: PriorityMedium, RequesterID: fx.user.ID}, fx.tenant.ID)
	require.NoError(t, err)
	fx.client.Ticket.UpdateOneID(generic.ID).SetGenericSubtype("improvement").ExecX(fx.ctx)
	for _, tt := range []struct {
		class, subtype string
		want           int
	}{{"incident", "", incident.ID}, {"generic", "improvement", generic.ID}} {
		filter := &FilterParams{RecordClass: &tt.class}
		if tt.subtype != "" {
			filter.GenericSubtype = &tt.subtype
		}
		result, err := fx.repo.List(fx.ctx, fx.tenant.ID, filter, &base.QueryParams{Page: 1, PageSize: 20})
		require.NoError(t, err)
		require.Len(t, result.Data, 1)
		require.Equal(t, tt.want, result.Data[0].ID)
	}
	subtype := "improvement"
	_, err = fx.repo.Update(fx.ctx, incident.ID, &UpdateParams{GenericSubtype: &subtype, Version: incident.Version}, fx.tenant.ID)
	require.ErrorContains(t, err, "owning domain command")
}
