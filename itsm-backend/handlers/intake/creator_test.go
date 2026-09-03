package intake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	itsmservice "itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type staticWorkItemNumberAllocator struct{ number string }

func (a staticWorkItemNumberAllocator) Allocate(context.Context, *ent.Client, int, time.Time) (string, error) {
	return a.number, nil
}

type staticIncidentNumberGenerator struct{ number string }

func (a staticIncidentNumberGenerator) GenerateIncidentNumber(context.Context, int) (string, error) {
	return a.number, nil
}

type recordingCategoryResolver struct {
	id          *int
	calls       int
	category    string
	subcategory string
}

func (r *recordingCategoryResolver) ResolveIncidentCategory(_ context.Context, _ *ent.Client, _ int, category, subcategory string) (*int, error) {
	r.calls++
	r.category = category
	r.subcategory = subcategory
	return r.id, nil
}

type recordingAssigneeValidator struct {
	calls      int
	assigneeID int
	tenantID   int
	err        error
}

func (r *recordingAssigneeValidator) ValidateIncidentAssignee(_ context.Context, _ *ent.Client, assigneeID, tenantID int) error {
	r.calls++
	r.assigneeID = assigneeID
	r.tenantID = tenantID
	return r.err
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
	creator := NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-1"}, nil, nil, nil)
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
	creator := NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-202608-000001"}, nil, nil, nil)
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

func TestIncidentCreatorResolvesCategoryFromStructuredCTI(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	generator := staticIncidentNumberGenerator{number: "INC-202609-000001"}
	categories := &recordingCategoryResolver{}
	creator := NewIncidentCreator(generator, categories, nil, nil)
	categoryID := 55

	plan, err := creator.Prepare(context.Background(), tx, ResolvedIntake{
		Identity:    Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command:     CreateWorkItemCommand{Title: "VPN down"},
		RecordClass: RecordClassIncident,
		CTI:         ResolvedCTI{CategoryID: &categoryID},
	})

	require.NoError(t, err)
	profession := plan.ProfessionalInput.(IncidentExtensionPlan)
	require.NotNil(t, plan.WorkItem.CategoryID)
	require.Equal(t, categoryID, *plan.WorkItem.CategoryID)
	require.Equal(t, "INC-202609-000001", profession.IncidentNumber)
	require.Equal(t, "incident", profession.Type)
	require.Empty(t, plan.WorkItem.TicketNumber)
	require.Zero(t, categories.calls)
}

func TestIncidentCreatorResolvesCategoryFromFreeTextNames(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	generator := staticIncidentNumberGenerator{number: "INC-202609-000002"}
	resolvedCategoryID := 77
	categories := &recordingCategoryResolver{id: &resolvedCategoryID}
	creator := NewIncidentCreator(generator, categories, nil, nil)

	plan, err := creator.Prepare(context.Background(), tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command: CreateWorkItemCommand{
			Title: "VPN down",
			Incident: &IncidentInput{
				Category: "performance", Subcategory: "cpu", Type: "security_event",
			},
		},
		RecordClass: RecordClassIncident,
	})

	require.NoError(t, err)
	require.NotNil(t, plan.WorkItem.CategoryID)
	require.Equal(t, resolvedCategoryID, *plan.WorkItem.CategoryID)
	require.Equal(t, "performance", categories.category)
	require.Equal(t, "cpu", categories.subcategory)
	profession := plan.ProfessionalInput.(IncidentExtensionPlan)
	require.Equal(t, "security_event", profession.Type)
}

func TestIncidentCreatorRejectsSubcategoryWithoutCategory(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	categories := CategoryResolverFunc(func(_ context.Context, _ *ent.Client, _ int, category, subcategory string) (*int, error) {
		require.Empty(t, category)
		require.Equal(t, "cpu", subcategory)
		return nil, fmt.Errorf("category is required when subcategory is supplied")
	})
	creator := NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-202609-000003"}, categories, nil, nil)

	_, err = creator.Prepare(context.Background(), tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command: CreateWorkItemCommand{
			Title: "VPN down", Incident: &IncidentInput{Subcategory: "cpu"},
		},
		RecordClass: RecordClassIncident,
	})

	require.ErrorIs(t, err, ErrDomainValidationFailed)
}

func TestIncidentCreatorMatchesIncidentServicePriorityAndInitialStatus(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	matrix := itsmservice.NewPriorityMatrixService(zaptest.NewLogger(t).Sugar())
	require.NoError(t, matrix.SetMatrix(tenant.ID, itsmservice.PriorityMatrix{
		"high": {"high": "critical"},
	}))
	creator := NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-202609-000004"}, nil, matrix, nil)

	plan, err := creator.Prepare(context.Background(), tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command: CreateWorkItemCommand{
			Title: "VPN down", Incident: &IncidentInput{Impact: "high", Urgency: "high"},
		},
		RecordClass: RecordClassIncident,
	})

	require.NoError(t, err)
	require.Equal(t, itsmservice.ResolveIncidentPriority(context.Background(), matrix, tenant.ID, "", "high", "high"), plan.WorkItem.Priority)
	require.Equal(t, string(common.IncidentStatusNew), plan.WorkItem.Status)
}

func TestIncidentCreatorCarriesFullDTOFieldSet(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	otherActor, err := client.User.Create().
		SetUsername(t.Name() + "-assignee").
		SetEmail(t.Name() + "-assignee@example.com").
		SetName("Assignee").
		SetPasswordHash("hash").
		SetRole("agent").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	validator := &recordingAssigneeValidator{}
	creator := NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-202609-000005"}, nil, nil, validator)

	plan, err := creator.Prepare(ctx, tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command: CreateWorkItemCommand{
			Title: "t",
			Incident: &IncidentInput{
				Impact:         "high",
				Urgency:        "high",
				AssigneeID:     intPtr(otherActor.ID),
				ImpactAnalysis: map[string]interface{}{"scope": "regional"},
				Metadata:       map[string]interface{}{"source_ticket": "legacy-1"},
			},
		},
		RecordClass: RecordClassIncident,
	})

	require.NoError(t, err)
	require.Equal(t, 1, validator.calls)
	require.Equal(t, otherActor.ID, validator.assigneeID)
	require.Equal(t, tenant.ID, validator.tenantID)
	require.NotNil(t, plan.WorkItem.AssigneeID)
	assert.Equal(t, otherActor.ID, *plan.WorkItem.AssigneeID)
	extPlan := plan.ProfessionalInput.(IncidentExtensionPlan)
	assert.Equal(t, "regional", extPlan.ImpactAnalysis["scope"])
	assert.Equal(t, "legacy-1", extPlan.Metadata["source_ticket"])
}

func intPtr(value int) *int {
	return &value
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
