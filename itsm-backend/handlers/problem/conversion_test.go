package problem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/workitemrelation"
)

type conversionFixture struct {
	client           *ent.Client
	service          *Service
	ctx              context.Context
	tenantID         int
	actorID          int
	incidentID       int
	incidentWorkItem int
}

func newConversionFixture(t *testing.T, status string, withWorkItem bool) *conversionFixture {
	t.Helper()
	suffix := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:problem-conversion-%s?mode=memory&cache=shared&_fk=1&_busy_timeout=5000&_txlock=immediate", suffix))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, suffix)
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, suffix)

	var workItemID int
	if withWorkItem {
		workItem, err := client.Ticket.Create().
			SetTitle("Intermittent API outage").
			SetDescription("Requests intermittently return 503").
			SetType("incident").
			SetRecordClass("incident").
			SetPriority("high").
			SetTicketNumber("CONV-SRC-" + suffix).
			SetRequesterID(actor.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
		workItemID = workItem.ID
	}

	create := client.Incident.Create().
		SetTitle("Intermittent API outage").
		SetDescription("Requests intermittently return 503").
		SetStatus(status).
		SetType("incident").
		SetPriority("high").
		SetSeverity("high").
		SetImpact("high").
		SetUrgency("high").
		SetIncidentNumber("CONV-INC-" + suffix).
		SetReporterID(actor.ID).
		SetCategory("platform").
		SetTenantID(tenant.ID)
	if withWorkItem {
		create.SetWorkItemID(workItemID)
	}
	incident, err := create.Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	repo.SetSequenceService(&atomicSequenceProvider{})
	return &conversionFixture{
		client:           client,
		service:          NewService(repo, zaptest.NewLogger(t).Sugar()),
		ctx:              ctx,
		tenantID:         tenant.ID,
		actorID:          actor.ID,
		incidentID:       incident.ID,
		incidentWorkItem: workItemID,
	}
}

type atomicSequenceProvider struct {
	next atomic.Int64
}

func (p *atomicSequenceProvider) GetNextSequenceWithExpiry(context.Context, string, time.Time) (int64, error) {
	return p.next.Add(1), nil
}

type conversionCounts struct {
	workItems int
	problems  int
	relations int
	events    int
	audits    int
}

func readConversionCounts(t *testing.T, f *conversionFixture) conversionCounts {
	t.Helper()
	workItems, err := f.client.Ticket.Query().Count(f.ctx)
	require.NoError(t, err)
	problems, err := f.client.Problem.Query().Count(f.ctx)
	require.NoError(t, err)
	relations, err := f.client.WorkItemRelation.Query().Count(f.ctx)
	require.NoError(t, err)
	events, err := f.client.IncidentEvent.Query().Count(f.ctx)
	require.NoError(t, err)
	audits, err := f.client.AuditLog.Query().Count(f.ctx)
	require.NoError(t, err)
	return conversionCounts{workItems, problems, relations, events, audits}
}

func requireConversionCounts(t *testing.T, f *conversionFixture, want conversionCounts) {
	t.Helper()
	assert.Equal(t, want, readConversionCounts(t, f))
}

func TestCreateFromIncidentCreatesWorkItemsRelationAndAuditAtomically(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	req := dto.ConvertIncidentToProblemRequest{
		Title:       "API gateway stability problem",
		Description: "Sensitive diagnostic payload",
		RootCause:   "Sensitive root cause hypothesis",
	}

	created, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, req)
	require.NoError(t, err)
	require.NotNil(t, created.WorkItemID)
	assert.Equal(t, req.Title, created.Title)
	assert.Equal(t, req.Description, created.Description)
	assert.Equal(t, req.RootCause, created.RootCause)
	assert.Equal(t, "high", created.Priority)
	assert.Equal(t, "platform", created.Category)

	workItem, err := f.client.Ticket.Get(f.ctx, *created.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "problem", workItem.RecordClass)
	assert.Equal(t, f.tenantID, workItem.TenantID)

	relation, err := f.client.WorkItemRelation.Query().Where(
		workitemrelation.TenantID(f.tenantID),
		workitemrelation.SourceWorkItemID(f.incidentWorkItem),
		workitemrelation.TargetWorkItemID(*created.WorkItemID),
		workitemrelation.RelationType("investigated_by"),
		workitemrelation.DeletedAtIsNil(),
	).Only(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, f.actorID, relation.CreatedByID)

	event, err := f.client.IncidentEvent.Query().Where(
		incidentevent.IncidentID(f.incidentID),
		incidentevent.TenantID(f.tenantID),
		incidentevent.UserID(f.actorID),
		incidentevent.Source("incident_conversion"),
		incidentevent.EventType("conversion"),
		incidentevent.EventName("convert_to_problem"),
	).Only(f.ctx)
	require.NoError(t, err)
	assert.EqualValues(t, created.ID, event.Data["problem_id"])
	assert.EqualValues(t, *created.WorkItemID, event.Data["problem_work_item_id"])

	audit, err := f.client.AuditLog.Query().Where(
		auditlog.TenantID(f.tenantID),
		auditlog.UserID(f.actorID),
		auditlog.Resource("incident"),
		auditlog.Action("convert_to_problem"),
		auditlog.Path("/api/v1/incidents/:id/convert-to-problem"),
		auditlog.Method("POST"),
	).Only(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, audit.RequestBody)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &body))
	assert.EqualValues(t, f.incidentWorkItem, body["sourceWorkItemId"])
	assert.EqualValues(t, *created.WorkItemID, body["targetWorkItemId"])
	assert.NotContains(t, *audit.RequestBody, req.Description)
	assert.NotContains(t, *audit.RequestBody, req.RootCause)

	incidentEnt, err := f.client.Incident.Get(f.ctx, f.incidentID)
	require.NoError(t, err)
	legacyProblems, err := f.client.Incident.QueryProblems(incidentEnt).Count(f.ctx)
	require.NoError(t, err)
	assert.Zero(t, legacyProblems, "conversion must not write the legacy Problem-Incident edge")

	withAssociations, err := f.service.GetWithAssociations(f.ctx, created.ID, f.tenantID)
	require.NoError(t, err)
	require.Len(t, withAssociations.Incidents, 1, "converted Problem must expose its source Incident")
	assert.Equal(t, f.incidentID, withAssociations.Incidents[0].ID)
	assert.Equal(t, "CONV-INC-"+strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()), withAssociations.Incidents[0].Number)
}

func TestGetWithAssociationsOmitsDeletedConvertedIncident(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	created, err := f.service.CreateFromIncident(
		f.ctx, f.tenantID, f.incidentID, f.actorID,
		dto.ConvertIncidentToProblemRequest{Title: "Deleted source trace"},
	)
	require.NoError(t, err)

	_, err = f.client.Incident.UpdateOneID(f.incidentID).SetDeletedAt(time.Now()).Save(f.ctx)
	require.NoError(t, err)

	withAssociations, err := f.service.GetWithAssociations(f.ctx, created.ID, f.tenantID)
	require.NoError(t, err)
	assert.Empty(t, withAssociations.Incidents)
}

func TestGetWithAssociationsOmitsDeletedLegacyIncident(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	created, err := f.service.Create(f.ctx, f.tenantID, &Problem{
		Title: "Legacy association", Priority: "medium", CreatedBy: f.actorID,
	})
	require.NoError(t, err)
	require.NoError(t, f.service.AddAssociations(
		f.ctx, f.tenantID, created.ID, f.actorID, "incident", []int{f.incidentID},
	))

	_, err = f.client.Incident.UpdateOneID(f.incidentID).SetDeletedAt(time.Now()).Save(f.ctx)
	require.NoError(t, err)

	withAssociations, err := f.service.GetWithAssociations(f.ctx, created.ID, f.tenantID)
	require.NoError(t, err)
	assert.Empty(t, withAssociations.Incidents)
}

func TestCreateFromIncidentRejectsIneligibleSourceWithoutWrites(t *testing.T) {
	t.Run("cross tenant incident", func(t *testing.T) {
		f := newConversionFixture(t, "new", true)
		foreignTenant := createProblemHandlerTenant(t, f.ctx, f.client, "foreign-"+strings.ReplaceAll(t.Name(), "/", "-"))
		foreignActor := createProblemHandlerUser(t, f.ctx, f.client, foreignTenant.ID, "foreign-"+strings.ReplaceAll(t.Name(), "/", "-"))
		before := readConversionCounts(t, f)

		_, err := f.service.CreateFromIncident(f.ctx, foreignTenant.ID, f.incidentID, foreignActor.ID, dto.ConvertIncidentToProblemRequest{})
		require.ErrorContains(t, err, "incident not found")
		requireConversionCounts(t, f, before)
	})

	t.Run("closed incident", func(t *testing.T) {
		f := newConversionFixture(t, "closed", true)
		before := readConversionCounts(t, f)

		_, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
		require.ErrorContains(t, err, "closed")
		requireConversionCounts(t, f, before)
	})

	t.Run("cancelled incident", func(t *testing.T) {
		f := newConversionFixture(t, "cancelled", true)
		before := readConversionCounts(t, f)

		_, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
		require.ErrorContains(t, err, "cancelled")
		requireConversionCounts(t, f, before)
	})

	t.Run("missing source work item", func(t *testing.T) {
		f := newConversionFixture(t, "new", false)
		before := readConversionCounts(t, f)

		_, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
		require.ErrorContains(t, err, "work item")
		requireConversionCounts(t, f, before)
	})

	t.Run("foreign tenant source work item", func(t *testing.T) {
		f := newConversionFixture(t, "new", true)
		suffix := "foreign-work-item-" + strings.ReplaceAll(t.Name(), "/", "-")
		foreignTenant := createProblemHandlerTenant(t, f.ctx, f.client, suffix)
		foreignUser := createProblemHandlerUser(t, f.ctx, f.client, foreignTenant.ID, suffix)
		foreignWorkItem, err := f.client.Ticket.Create().
			SetTitle("Foreign incident work item").
			SetType("incident").
			SetRecordClass("incident").
			SetPriority("high").
			SetTicketNumber("CONV-FOREIGN-" + suffix).
			SetRequesterID(foreignUser.ID).
			SetTenantID(foreignTenant.ID).
			Save(f.ctx)
		require.NoError(t, err)
		_, err = f.client.Incident.UpdateOneID(f.incidentID).SetWorkItemID(foreignWorkItem.ID).Save(f.ctx)
		require.NoError(t, err)
		before := readConversionCounts(t, f)

		_, err = f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
		require.ErrorContains(t, err, "source work item not found")
		requireConversionCounts(t, f, before)
	})
}

func TestDeleteConvertedProblemSoftDeletesInvestigationRelation(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	created, err := f.service.CreateFromIncident(
		f.ctx, f.tenantID, f.incidentID, f.actorID,
		dto.ConvertIncidentToProblemRequest{Title: "Temporary investigation"},
	)
	require.NoError(t, err)
	require.NotNil(t, created.WorkItemID)

	require.NoError(t, f.service.Delete(f.ctx, created.ID, f.tenantID))
	live, err := f.client.WorkItemRelation.Query().Where(
		workitemrelation.TenantID(f.tenantID),
		workitemrelation.SourceWorkItemID(f.incidentWorkItem),
		workitemrelation.TargetWorkItemID(*created.WorkItemID),
		workitemrelation.RelationType("investigated_by"),
		workitemrelation.DeletedAtIsNil(),
	).Exist(f.ctx)
	require.NoError(t, err)
	require.False(t, live)

	recreated, err := f.service.CreateFromIncident(
		f.ctx, f.tenantID, f.incidentID, f.actorID,
		dto.ConvertIncidentToProblemRequest{Title: "Replacement investigation"},
	)
	require.NoError(t, err)
	require.NotEqual(t, created.ID, recreated.ID)
}

func TestCreateFromIncidentRollsBackWhenRelationAlreadyExists(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	existing, err := f.service.Create(f.ctx, f.tenantID, &Problem{
		Title: "Existing investigation", Priority: "high", CreatedBy: f.actorID,
	})
	require.NoError(t, err)
	require.NotNil(t, existing.WorkItemID)
	_, err = f.client.WorkItemRelation.Create().
		SetTenantID(f.tenantID).
		SetSourceWorkItemID(f.incidentWorkItem).
		SetTargetWorkItemID(*existing.WorkItemID).
		SetRelationType("investigated_by").
		SetCreatedByID(f.actorID).
		Save(f.ctx)
	require.NoError(t, err)
	before := readConversionCounts(t, f)

	_, err = f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
	require.ErrorContains(t, err, "already")
	requireConversionCounts(t, f, before)
}

func TestCreateFromIncidentConcurrentRequestsCreateOneProblem(t *testing.T) {
	f := newConversionFixture(t, "new", true)
	before := readConversionCounts(t, f)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes int
	var failures []error
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures = append(failures, err)
		}
	}
	assert.Equal(t, 1, successes, "concurrent conversion errors: %v", failures)
	after := readConversionCounts(t, f)
	assert.Equal(t, before.workItems+1, after.workItems)
	assert.Equal(t, before.problems+1, after.problems)
	assert.Equal(t, before.relations+1, after.relations)
	assert.Equal(t, before.events+1, after.events)
	assert.Equal(t, before.audits+1, after.audits)
}

func TestCreateFromIncidentRollsBackOnSideEffectFailure(t *testing.T) {
	tests := []struct {
		name        string
		installHook func(*ent.Client)
	}{
		{
			name: "relation",
			installHook: func(client *ent.Client) {
				client.WorkItemRelation.Use(failingMutationHook("injected relation failure"))
			},
		},
		{
			name: "incident event",
			installHook: func(client *ent.Client) {
				client.IncidentEvent.Use(failingMutationHook("injected incident event failure"))
			},
		},
		{
			name: "audit log",
			installHook: func(client *ent.Client) {
				client.AuditLog.Use(failingMutationHook("injected audit log failure"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConversionFixture(t, "new", true)
			before := readConversionCounts(t, f)
			tt.installHook(f.client)

			_, err := f.service.CreateFromIncident(f.ctx, f.tenantID, f.incidentID, f.actorID, dto.ConvertIncidentToProblemRequest{})
			require.Error(t, err)
			assert.ErrorContains(t, err, "injected")
			requireConversionCounts(t, f, before)
		})
	}
}

func failingMutationHook(message string) ent.Hook {
	return func(ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New(message)
		})
	}
}
