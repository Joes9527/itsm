//go:build integration_postgres

package intake

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"
	itsmservice "itsm-backend/service"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type postgresIncidentNumbers struct{ value atomic.Int64 }

func (a *postgresIncidentNumbers) GenerateIncidentNumber(context.Context, int) (string, error) {
	return fmt.Sprintf("INC-PG-%d-%06d", time.Now().UnixNano(), a.value.Add(1)), nil
}

func TestServicePostgresConcurrentReplayCommitsOneAuthoritativeGraph(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("INTAKE_POSTGRES_TEST_DSN not set")
	}
	client, err := ent.Open("postgres", dsn)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, client.Schema.Create(ctx), "INTAKE_POSTGRES_TEST_DSN must point to a disposable database")

	suffix := fmt.Sprint(time.Now().UnixNano())
	tenant, err := client.Tenant.Create().SetName("Intake PG " + suffix).SetCode("intake-pg-" + suffix).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().SetUsername("intake-pg-" + suffix).SetEmail("intake-pg-" + suffix + "@example.com").
		SetName("Concurrent requester").SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().SetDeploymentID("intake-pg-deploy-" + suffix).
		SetDeploymentName("Intake concurrent workflow").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().SetKey("intake-pg-flow-" + suffix).SetName("Intake concurrent workflow").
		SetVersion("1").SetBpmnXML([]byte("<definitions/>")).SetIsActive(true).SetIsLatest(true).
		SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("incident").
		SetProcessDefinitionKey(definition.Key).SetProcessVersion(1).SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	resolver := NewResolver(itsmservice.NewProcessBindingService(client), PermissionCheckFunc(func(*ent.Client, Identity, string, string) bool { return true }))
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(NewIncidentCreator(&postgresIncidentNumbers{}, nil, nil, nil)))
	require.NoError(t, registry.Register(NewChangeCreator()))
	service := NewService(client, resolver, registry, NewWorkItemCreator(&sequentialWorkItemNumbers{}))
	identity := Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: "end_user", Channel: "itsm_web", TokenID: "pg-concurrent-" + suffix}
	command := validIncidentCommand("pg-concurrent-"+suffix, nil)
	command.Title = "Concurrent intake " + suffix

	const callers = 20
	type result struct {
		value *CreateWorkItemResult
		err   error
	}
	results := make(chan result, callers)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			value, createErr := service.Create(ctx, identity, command)
			results <- result{value: value, err: createErr}
		}()
	}
	ready.Wait()
	close(start)

	created, replayed, workItemID := 0, 0, 0
	for range callers {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.value)
		if workItemID == 0 {
			workItemID = result.value.WorkItemID
		}
		require.Equal(t, workItemID, result.value.WorkItemID)
		if result.value.Replayed {
			replayed++
		} else {
			created++
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, callers-1, replayed)

	receipts, err := client.IntakeRequest.Query().Where(intakerequest.IdempotencyKeyEQ(command.IdempotencyKey), intakerequest.StatusEQ("completed")).All(ctx)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, workItemID, *receipts[0].WorkItemID)
	assertPostgresCount(t, client.Ticket.Query().Where(ticket.IDEQ(workItemID)), 1)
	assertPostgresCount(t, client.Incident.Query().Where(incident.WorkItemIDEQ(workItemID)), 1)
	createdIncident, err := client.Incident.Query().Where(incident.WorkItemIDEQ(workItemID)).Only(ctx)
	require.NoError(t, err)
	assertPostgresCount(t, client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(createdIncident.ID)), 1)
	assertPostgresCount(t, client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(workItemID)), 1)
	assertPostgresCount(t, client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(workItemID)), outboxevent.EventTypeEQ(workflowStartEventType)), 1)
	assertPostgresCount(t, client.AuditLog.Query().Where(auditlog.ResourceEQ(fmt.Sprintf("work_item:%d", workItemID))), 1)
}

func assertPostgresCount(t *testing.T, query countQuery, expected int) {
	t.Helper()
	count, err := query.Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, expected, count)
}
