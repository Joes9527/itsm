package intake

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"

	"github.com/stretchr/testify/require"
)

type sequentialWorkItemNumbers struct{ value atomic.Int64 }

func (a *sequentialWorkItemNumbers) Allocate(context.Context, *ent.Client, int, time.Time) (string, error) {
	return fmt.Sprintf("TKT-INTAKE-%06d", a.value.Add(1)), nil
}

func newServiceUnderTest(t *testing.T, fixture *resolverFixture) *Service {
	t.Helper()
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-INTAKE-000001"}, nil, nil)))
	require.NoError(t, registry.Register(NewServiceRequestItemCreator()))
	return NewService(
		fixture.client,
		fixture.resolver(nil),
		registry,
		NewWorkItemCreator(&sequentialWorkItemNumbers{}),
	)
}

func TestServiceCreateCommitsOneAuthoritativeGraphAndReplays(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()
	service := newServiceUnderTest(t, fixture)
	cmd := fixture.catalogCommand(fixture.serviceCatalog.ID)

	created, err := service.Create(ctx, fixture.identity(), cmd)
	require.NoError(t, err)
	require.False(t, created.Replayed)
	require.Equal(t, RecordClassServiceRequestItem, created.RecordClass)
	require.Equal(t, "pending", created.WorkflowStartStatus)

	replayed, err := service.Create(ctx, fixture.identity(), cmd)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, created.WorkItemID, replayed.WorkItemID)
	require.Equal(t, created.ProfessionalReference, replayed.ProfessionalReference)

	for name, count := range map[string]int{
		"receipt":     countRows(t, fixture.client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(created.WorkItemID))),
		"work item":   countRows(t, fixture.client.Ticket.Query().Where(ticket.IDEQ(created.WorkItemID))),
		"extension":   countRows(t, fixture.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(created.WorkItemID))),
		"field value": countRows(t, fixture.client.FieldValue.Query().Where(fieldvalue.EntityTypeEQ("service_request"), fieldvalue.EntityIDEQ(created.ProfessionalReference.ID))),
		"snapshot":    countRows(t, fixture.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(created.WorkItemID))),
		"audit":       countRows(t, fixture.client.AuditLog.Query().Where(auditlog.ResourceEQ(fmt.Sprintf("work_item:%d", created.WorkItemID)))),
		"outbox":      countRows(t, fixture.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(created.WorkItemID)), outboxevent.EventTypeEQ("workflow.start.requested"))),
	} {
		require.Equal(t, 1, count, name)
	}
}

type countQuery interface {
	Count(context.Context) (int, error)
}

func countRows(t *testing.T, query countQuery) int {
	t.Helper()
	count, err := query.Count(context.Background())
	require.NoError(t, err)
	return count
}

func TestServiceCreateNoProcessSkipsOutbox(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()
	_, err := fixture.client.ProcessBinding.Update().SetConditions(map[string]any{"no_process": true}).Save(ctx)
	require.NoError(t, err)
	service := newServiceUnderTest(t, fixture)
	cmd := validIncidentCommand("no-process", nil)

	created, err := service.Create(ctx, fixture.identity(), cmd)
	require.NoError(t, err)
	require.Equal(t, "not_required", created.WorkflowStartStatus)
	require.Zero(t, countRows(t, fixture.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(created.WorkItemID)))))
}

func TestWorkflowStartStatusProjection(t *testing.T) {
	require.Equal(t, "pending", projectWorkflowStartStatus("pending"))
	require.Equal(t, "pending", projectWorkflowStartStatus("publishing"))
	require.Equal(t, "active", projectWorkflowStartStatus("published"))
	require.Equal(t, "manual_intervention_required", projectWorkflowStartStatus("dead"))
}

type failingReferenceResolver struct{ err error }

func (r failingReferenceResolver) Resolve(context.Context, *ent.Tx, Identity, CreateWorkItemCommand) (*ResolvedIntake, error) {
	return nil, r.err
}

func TestServiceCreateRollbackFaultMatrix(t *testing.T) {
	tests := []struct {
		name        string
		mutation    string
		useIncident bool
		failResolve bool
	}{
		{name: "receipt claim", mutation: "receipt claim"},
		{name: "reference resolution", failResolve: true},
		{name: "work item", mutation: "work item"},
		{name: "professional extension", mutation: "service request"},
		{name: "professional event", mutation: "incident event", useIncident: true},
		{name: "field value", mutation: "field value"},
		{name: "configuration item link", mutation: "incident CI link", useIncident: true},
		{name: "snapshot", mutation: "snapshot"},
		{name: "audit", mutation: "audit"},
		{name: "outbox", mutation: "outbox"},
		{name: "receipt completion", mutation: "receipt completion"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			service := newServiceUnderTest(t, fixture)
			if test.failResolve {
				service.resolver = failingReferenceResolver{err: NewInfrastructureUnavailable("injected resolver failure", errors.New("injected"))}
			} else {
				installIntakeMutationFailure(fixture.client, test.mutation)
			}
			key := "rollback-" + test.name
			command := fixture.catalogCommand(fixture.serviceCatalog.ID)
			command.IdempotencyKey = key
			command.Title = "Rollback " + test.name
			if test.useIncident {
				command = validIncidentCommand(key, []int{fixture.ciID})
				command.Title = "Rollback " + test.name
			}

			_, err := service.Create(context.Background(), fixture.identity(), command)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInfrastructureUnavailable)
			assertNoIntakeGraph(t, fixture.client, key, command.Title)
		})
	}
}

func installIntakeMutationFailure(client *ent.Client, target string) {
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			matches := false
			switch typed := mutation.(type) {
			case *ent.IntakeRequestMutation:
				matches = (target == "receipt claim" && typed.Op().Is(ent.OpCreate)) ||
					(target == "receipt completion" && typed.Op().Is(ent.OpUpdate|ent.OpUpdateOne))
			case *ent.TicketMutation:
				matches = target == "work item" && typed.Op().Is(ent.OpCreate)
			case *ent.ServiceRequestMutation:
				matches = target == "service request" && typed.Op().Is(ent.OpCreate)
			case *ent.IncidentMutation:
				matches = target == "incident CI link" && typed.Op().Is(ent.OpUpdate|ent.OpUpdateOne)
			case *ent.IncidentEventMutation:
				matches = target == "incident event" && typed.Op().Is(ent.OpCreate)
			case *ent.FieldValueMutation:
				matches = target == "field value" && typed.Op().Is(ent.OpCreate)
			case *ent.IntakeResolutionSnapshotMutation:
				matches = target == "snapshot" && typed.Op().Is(ent.OpCreate)
			case *ent.AuditLogMutation:
				matches = target == "audit" && typed.Op().Is(ent.OpCreate)
			case *ent.OutboxEventMutation:
				matches = target == "outbox" && typed.Op().Is(ent.OpCreate)
			}
			if matches {
				return nil, fmt.Errorf("injected %s failure", target)
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func assertNoIntakeGraph(t *testing.T, client *ent.Client, key, title string) {
	t.Helper()
	counts := map[string]int{
		"receipt":         countRows(t, client.IntakeRequest.Query().Where(intakerequest.IdempotencyKeyEQ(key))),
		"work item":       countRows(t, client.Ticket.Query().Where(ticket.TitleEQ(title))),
		"service request": countRows(t, client.ServiceRequest.Query()),
		"incident":        countRows(t, client.Incident.Query().Where(incident.HasWorkItemWith(ticket.TitleEQ(title)))),
		"incident event":  countRows(t, client.IncidentEvent.Query().Where(incidentevent.DescriptionContains("INC-INTAKE"))),
		"field value":     countRows(t, client.FieldValue.Query()),
		"snapshot":        countRows(t, client.IntakeResolutionSnapshot.Query()),
		"audit":           countRows(t, client.AuditLog.Query()),
		"outbox":          countRows(t, client.OutboxEvent.Query()),
	}
	for name, count := range counts {
		require.Zero(t, count, name)
	}
}
