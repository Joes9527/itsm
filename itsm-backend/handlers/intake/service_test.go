package intake

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/incidentruleexecution"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/repository/workitemnumber"
	itsmservice "itsm-backend/service"

	"go.uber.org/zap/zaptest"
	"github.com/stretchr/testify/require"
)

type recordingFieldValueWriter struct {
	tenantID     int
	definition   string
	definitionID int
	entityType   string
	entityID     int
	values       map[string]any
}

func (r *recordingFieldValueWriter) CreateValuesTx(_ context.Context, _ *ent.Tx, tenantID int, definition string, definitionID int, entityType string, entityID int, values map[string]any) error {
	r.tenantID = tenantID
	r.definition = definition
	r.definitionID = definitionID
	r.entityType = entityType
	r.entityID = entityID
	r.values = values
	return nil
}

type sequentialWorkItemNumbers struct{ value atomic.Int64 }

func (a *sequentialWorkItemNumbers) Allocate(context.Context, *ent.Client, int, time.Time) (string, error) {
	return fmt.Sprintf("TKT-INTAKE-%06d", a.value.Add(1)), nil
}

func newServiceUnderTest(t *testing.T, fixture *resolverFixture) *Service {
	t.Helper()
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-INTAKE-000001"}, nil, nil, nil)))
	require.NoError(t, registry.Register(NewChangeCreator()))
	require.NoError(t, registry.Register(NewServiceRequestItemCreator()))
	return NewService(
		fixture.client,
		fixture.resolver(nil),
		registry,
		NewWorkItemCreator(&sequentialWorkItemNumbers{}),
	)
}

func newServiceWithIncidentRuleAutomation(t *testing.T, fixture *resolverFixture) *Service {
	t.Helper()
	registry := NewCreatorRegistry()
	incidentService := itsmservice.NewIncidentService(fixture.client, zaptest.NewLogger(t).Sugar(), workitemnumber.NewPostgreSQLAllocator())
	require.NoError(t, registry.Register(
		NewIncidentCreator(staticIncidentNumberGenerator{number: "INC-INTAKE-000001"}, nil, nil, nil).
			WithPostCommit(incidentService),
	))
	require.NoError(t, registry.Register(NewChangeCreator()))
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
		"field value": countRows(t, fixture.client.FieldValue.Query().Where(fieldvalue.EntityTypeEQ("ticket"), fieldvalue.EntityIDEQ(created.WorkItemID))),
		"snapshot":    countRows(t, fixture.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(created.WorkItemID))),
		"audit":       countRows(t, fixture.client.AuditLog.Query().Where(auditlog.ResourceEQ(fmt.Sprintf("work_item:%d", created.WorkItemID)))),
		"outbox":      countRows(t, fixture.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(created.WorkItemID)), outboxevent.EventTypeEQ("workflow.start.requested"))),
	} {
		require.Equal(t, 1, count, name)
	}
}

func TestServiceCreateChangeRequestCommitsAndReplaysSameProfessionalReference(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()
	service := newServiceUnderTest(t, fixture)
	cmd := fixture.catalogCommand(fixture.changeCatalog.ID)
	cmd.IdempotencyKey = "change-replay-key"
	cmd.Title = "Upgrade core switch"
	cmd.Description = "Apply approved firmware and routing changes"
	cmd.FormValues = nil
	cmd.Change = &ChangeInput{
		Type:               "normal",
		RiskLevel:          "high",
		ImpactScope:        "network-core",
		Justification:      "Required for vendor security fix",
		ImplementationPlan: "Drain traffic, apply firmware, validate routing",
		RollbackPlan:       "Reboot to prior image",
	}

	created, err := service.Create(ctx, fixture.identity(), cmd)
	require.NoError(t, err)
	require.False(t, created.Replayed)
	require.Equal(t, RecordClassChangeRequest, created.RecordClass)
	require.Equal(t, "change", created.ProfessionalReference.Type)
	require.Equal(t, "pending", created.WorkflowStartStatus)

	replayed, err := service.Create(ctx, fixture.identity(), cmd)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, created.WorkItemID, replayed.WorkItemID)
	require.Equal(t, created.ProfessionalReference, replayed.ProfessionalReference)

	storedChange, err := fixture.client.Change.Query().Where(change.WorkItemIDEQ(created.WorkItemID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, storedChange.ID, created.ProfessionalReference.ID)

	for name, count := range map[string]int{
		"receipt":   countRows(t, fixture.client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(created.WorkItemID))),
		"work item": countRows(t, fixture.client.Ticket.Query().Where(ticket.IDEQ(created.WorkItemID))),
		"change":    countRows(t, fixture.client.Change.Query().Where(change.WorkItemIDEQ(created.WorkItemID))),
		"snapshot":  countRows(t, fixture.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(created.WorkItemID))),
		"audit":     countRows(t, fixture.client.AuditLog.Query().Where(auditlog.ResourceEQ(fmt.Sprintf("work_item:%d", created.WorkItemID)))),
		"outbox":    countRows(t, fixture.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(created.WorkItemID)), outboxevent.EventTypeEQ("workflow.start.requested"))),
	} {
		require.Equal(t, 1, count, name)
	}
}

func TestServiceCreateIncidentExecutesRulesAfterCommitAndDoesNotReplayThem(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()
	service := newServiceWithIncidentRuleAutomation(t, fixture)

	_, err := fixture.client.IncidentRule.Create().
		SetName("Collect intake creation metric").
		SetRuleType("metric").
		SetConditions(map[string]interface{}{"severity": []string{"high"}}).
		SetActions([]map[string]interface{}{{
			"type": "collect_metric", "metric_type": "automation", "metric_name": "intake_rule_applied",
			"metric_value": 1.0, "unit": "count",
		}}).
		SetIsActive(true).
		SetTenantID(fixture.tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	command := validIncidentCommand("intake-rule-replay", nil)
	created, err := service.Create(ctx, fixture.identity(), command)
	require.NoError(t, err)
	require.False(t, created.Replayed)
	require.Equal(t, RecordClassIncident, created.RecordClass)

	executionCount, err := fixture.client.IncidentRuleExecution.Query().
		Where(incidentruleexecution.IncidentIDEQ(created.ProfessionalReference.ID)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, executionCount, "incident rules must execute once after the intake transaction commits")

	replayed, err := service.Create(ctx, fixture.identity(), command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)

	executionCount, err = fixture.client.IncidentRuleExecution.Query().
		Where(incidentruleexecution.IncidentIDEQ(created.ProfessionalReference.ID)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, executionCount, "replaying the same intake request must not execute rules twice")
}

func TestWriteFieldValuesUsesTicketEntityTypeForServiceRequest(t *testing.T) {
	fixture := newResolverFixture(t)
	recorder := &recordingFieldValueWriter{}
	service := &Service{fieldValues: recorder}
	tx, err := fixture.client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	resolved := &ResolvedIntake{
		Identity:    Identity{TenantID: fixture.tenant.ID},
		RecordClass: RecordClassServiceRequestItem,
		Catalog:     &ResolvedCatalog{ID: fixture.serviceCatalog.ID},
		Command:     CreateWorkItemCommand{FormValues: map[string]any{"k": "v"}},
	}

	err = service.writeFieldValues(context.Background(), tx, resolved, 101, &ProfessionalReference{Type: "service_request", ID: 202})
	require.NoError(t, err)
	require.Equal(t, fixture.tenant.ID, recorder.tenantID)
	require.Equal(t, "service_catalog", recorder.definition)
	require.Equal(t, fixture.serviceCatalog.ID, recorder.definitionID)
	require.Equal(t, "ticket", recorder.entityType)
	require.Equal(t, 101, recorder.entityID)
	require.Equal(t, map[string]any{"k": "v"}, recorder.values)
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
