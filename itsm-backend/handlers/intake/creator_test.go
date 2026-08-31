package intake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type staticWorkItemNumberAllocator struct{ number string }

func (a staticWorkItemNumberAllocator) GenerateWorkItemNumber(context.Context, int) (string, error) {
	return a.number, nil
}

type staticIncidentNumberAllocator struct{ number string }

func (a staticIncidentNumberAllocator) GenerateIncidentNumberForIntake(context.Context, int) (string, error) {
	return a.number, nil
}

type failingProfessionalCreator struct{}

func (failingProfessionalCreator) RecordClass() string { return RecordClassIncident }
func (failingProfessionalCreator) Prepare(context.Context, *ent.Tx, ResolvedIntake) (*CreationPlan, error) {
	return nil, errors.New("forced prepare failure")
}
func (failingProfessionalCreator) CreateExtension(context.Context, *ent.Tx, *ent.Ticket, *CreationPlan) (*ProfessionalReference, error) {
	return nil, errors.New("forced extension failure")
}

func newCreatorFixture(t *testing.T) (*ent.Client, *ent.Tenant, *ent.User) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name))
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName(name).SetCode(name).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername(name).SetEmail(name + "@example.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	return client, tenant, user
}

func TestCreatorRegistryFailsClosedForDuplicateAndUnknownClass(t *testing.T) {
	registry := NewCreatorRegistry()
	creator := NewIncidentCreator(staticIncidentNumberAllocator{number: "INC-1"})
	require.NoError(t, registry.Register(creator))
	require.ErrorIs(t, registry.Register(creator), ErrUnsupportedRecordClass)
	_, err := registry.Get("change_request")
	require.ErrorIs(t, err, ErrUnsupportedRecordClass)
	require.Same(t, creator, mustCreator(t, registry, RecordClassIncident))
}

func mustCreator(t *testing.T, registry *CreatorRegistry, recordClass string) ProfessionalCreator {
	t.Helper()
	creator, err := registry.Get(recordClass)
	require.NoError(t, err)
	return creator
}

func TestIncidentCreatorCreatesOneExtensionAndCreationEvent(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	resolved := ResolvedIntake{
		Identity:    Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID, Channel: "itsm_web"},
		Command:     CreateWorkItemCommand{Title: "Database outage", Description: "Primary unavailable", Incident: &IncidentInput{Severity: "critical", Impact: "high", Urgency: "critical", DetectedAt: "2026-08-31T10:30:00Z"}},
		RecordClass: RecordClassIncident,
	}
	creator := NewIncidentCreator(staticIncidentNumberAllocator{number: "INC-202608-000001"})
	plan, err := creator.Prepare(ctx, tx, resolved)
	require.NoError(t, err)
	workItem, err := NewWorkItemCreator(staticWorkItemNumberAllocator{number: "TKT-202608-000001"}).CreateBase(ctx, tx, plan)
	require.NoError(t, err)
	reference, err := creator.CreateExtension(ctx, tx, workItem, plan)
	require.NoError(t, err)
	require.Equal(t, "incident", reference.Type)
	require.NoError(t, tx.Commit())

	ticketCount, err := client.Ticket.Query().Where(ticket.IDEQ(workItem.ID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, ticketCount)
	incidentCount, err := client.Incident.Query().Where(incident.WorkItemIDEQ(workItem.ID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, incidentCount)
	createdIncident, err := client.Incident.Query().Where(incident.WorkItemIDEQ(workItem.ID)).Only(ctx)
	require.NoError(t, err)
	events, err := createdIncident.QueryIncidentEvents().All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "creation", events[0].EventType)
}

func TestIncidentCreatorCalculatesLowPriorityWithoutArtificialFloor(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	creator := NewIncidentCreator(staticIncidentNumberAllocator{number: "INC-LOW"})
	plan, err := creator.Prepare(ctx, tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID, Channel: "itsm_web"},
		Command: CreateWorkItemCommand{Title: "Minor degradation", Incident: &IncidentInput{
			Severity: "low", Impact: "low", Urgency: "low",
		}},
		RecordClass: RecordClassIncident,
	})
	require.NoError(t, err)
	require.Equal(t, "low", plan.WorkItem.Priority)
	require.NoError(t, tx.Rollback())
}

func TestServiceRequestItemCreatorCreatesExactlyOneExtension(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	catalog, err := client.ServiceCatalog.Create().SetName("Access").SetStatus("active").SetIsActive(true).
		SetTargetClass(RecordClassServiceRequestItem).SetServiceType("custom").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	resolved := ResolvedIntake{
		Identity:    Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID, Channel: "itsm_web"},
		Command:     CreateWorkItemCommand{Title: "Access request", Description: "Need repository access", FormValues: map[string]any{"contact_name": "Requester", "quantity": 2}},
		RecordClass: RecordClassServiceRequestItem,
		Catalog:     &ResolvedCatalog{ID: catalog.ID, TargetClass: RecordClassServiceRequestItem, ServiceType: "custom"},
	}
	creator := NewServiceRequestItemCreator()
	plan, err := creator.Prepare(ctx, tx, resolved)
	require.NoError(t, err)
	workItem, err := NewWorkItemCreator(staticWorkItemNumberAllocator{number: "TKT-202608-000002"}).CreateBase(ctx, tx, plan)
	require.NoError(t, err)
	reference, err := creator.CreateExtension(ctx, tx, workItem, plan)
	require.NoError(t, err)
	require.Equal(t, "service_request", reference.Type)
	require.NoError(t, tx.Commit())

	requestCount, err := client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItem.ID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, requestCount)
	stored, err := client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItem.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Quantity)
	require.Empty(t, stored.FormData, "dynamic form values are written by FieldValueService, not duplicated in the extension")
}

func TestCreatorFailureRollsBackWorkItem(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	plan := &CreationPlan{WorkItem: WorkItemDraft{
		TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID,
		RecordClass: RecordClassIncident, Title: "Rollback", Priority: "medium",
	}}
	workItem, err := NewWorkItemCreator(staticWorkItemNumberAllocator{number: "TKT-ROLLBACK"}).CreateBase(ctx, tx, plan)
	require.NoError(t, err)
	_, err = (failingProfessionalCreator{}).CreateExtension(ctx, tx, workItem, plan)
	require.ErrorContains(t, err, "forced extension failure")
	require.NoError(t, tx.Rollback())

	count, err := client.Ticket.Query().Where(ticket.TicketNumberEQ("TKT-ROLLBACK")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestServiceRequestItemCreatorValidatesInfrastructureAndTimestamps(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	creator := NewServiceRequestItemCreator()
	base := ResolvedIntake{
		Identity:    Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID, Channel: "itsm_web"},
		Command:     CreateWorkItemCommand{Title: "VM request", FormValues: map[string]any{}},
		RecordClass: RecordClassServiceRequestItem,
		Catalog:     &ResolvedCatalog{ID: 99, TargetClass: RecordClassServiceRequestItem, ServiceType: "vm"},
	}

	_, err = creator.Prepare(ctx, tx, base)
	require.ErrorIs(t, err, ErrDomainValidationFailed)

	base.Catalog.ServiceType = "custom"
	base.Command.FormValues["expected_at"] = "not-a-timestamp"
	_, err = creator.Prepare(ctx, tx, base)
	require.ErrorIs(t, err, ErrDomainValidationFailed)
	require.NoError(t, tx.Rollback())
}
