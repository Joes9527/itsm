//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/migrate"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processtask"

	entgo "entgo.io/ent"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const postgresIntegrationTimeout = 10 * time.Second

type postgresClaimFixture struct {
	tenantID      int
	otherTenantID int
	actorIDs      [2]int
	taskID        int
	taskKey       string
	instanceID    int
	otherTaskID   int
	otherInstance int
}

type postgresClaimLoad struct {
	worker   string
	taskID   int
	tenantID int
	assignee string
	status   string
}

type postgresClaimResult struct {
	actorID int
	err     error
}

type postgresProcessTaskSnapshot struct {
	ID                   int
	TaskID               string
	ProcessInstanceID    int
	ProcessDefinitionKey string
	TaskDefinitionKey    string
	TaskName             string
	TaskType             string
	Assignee             string
	CandidateUsers       string
	CandidateGroups      string
	Status               string
	Priority             string
	DueDate              time.Time
	CreatedTime          time.Time
	AssignedTime         time.Time
	StartedTime          time.Time
	CompletedTime        time.Time
	FormKey              string
	TaskVariables        map[string]interface{}
	AggregationVersion   int
	Description          string
	CorrelationID        string
	ParentTaskID         string
	RootTaskID           string
	TenantID             int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func snapshotPostgresProcessTask(row *ent.ProcessTask) postgresProcessTaskSnapshot {
	variables := make(map[string]interface{}, len(row.TaskVariables))
	for key, value := range row.TaskVariables {
		variables[key] = value
	}
	return postgresProcessTaskSnapshot{
		ID: row.ID, TaskID: row.TaskID, ProcessInstanceID: row.ProcessInstanceID,
		ProcessDefinitionKey: row.ProcessDefinitionKey, TaskDefinitionKey: row.TaskDefinitionKey,
		TaskName: row.TaskName, TaskType: row.TaskType, Assignee: row.Assignee,
		CandidateUsers: row.CandidateUsers, CandidateGroups: row.CandidateGroups,
		Status: row.Status, Priority: row.Priority, DueDate: row.DueDate,
		CreatedTime: row.CreatedTime, AssignedTime: row.AssignedTime,
		StartedTime: row.StartedTime, CompletedTime: row.CompletedTime,
		FormKey: row.FormKey, TaskVariables: variables, Description: row.Description,
		AggregationVersion: row.AggregationVersion,
		CorrelationID:      row.CorrelationID, ParentTaskID: row.ParentTaskID,
		RootTaskID: row.RootTaskID, TenantID: row.TenantID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

type postgresTaskRaceResult struct {
	command BPMNTaskCommand
	err     error
}

type postgresProcessVariableRaceResult struct {
	value string
	err   error
}

type postgresProcessInstanceLoadBarrier struct {
	instanceID int
	worker     string
	arrived    chan string
	release    chan struct{}
}

func (b *postgresProcessInstanceLoadBarrier) interceptor() ent.Interceptor {
	var waitOnce sync.Once
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err != nil {
				return value, err
			}
			rows, ok := value.([]*ent.ProcessInstance)
			if !ok {
				return value, nil
			}
			for _, row := range rows {
				if row.ID != b.instanceID {
					continue
				}
				waitOnce.Do(func() {
					b.arrived <- b.worker
					select {
					case <-b.release:
					case <-ctx.Done():
					}
				})
				break
			}
			return value, nil
		})
	})
}

type postgresClaimLoadBarrier struct {
	taskID  int
	arrived chan postgresClaimLoad
	release chan struct{}
}

type postgresTaskMutationBarrier struct {
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *postgresTaskMutationBarrier) hook() ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.ProcessTaskMutation); ok {
				b.once.Do(func() {
					close(b.arrived)
					select {
					case <-b.release:
					case <-ctx.Done():
					}
				})
			}
			return next.Mutate(ctx, mutation)
		})
	}
}

func openBPMNPostgresIntegrationClient(t *testing.T) (*ent.Client, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	require.NoError(t, db.PingContext(context.Background()))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client, db
}

func migrateBPMNPostgresIntegrationTables(t *testing.T, client *ent.Client, tables ...*entschema.Table) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	require.NoError(t, migrate.Create(ctx, client.Schema, tables))
}

func registerPostgresBPMNFixtureCleanup(t *testing.T, db *sql.DB, tenantIDs *[]int) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cancel()
		for _, tenantID := range *tenantIDs {
			for _, statement := range []string{
				"DELETE FROM process_audit_logs WHERE tenant_id = $1",
				"DELETE FROM process_tasks WHERE tenant_id = $1",
				"DELETE FROM process_instances WHERE tenant_id = $1",
				"DELETE FROM process_definitions WHERE tenant_id = $1",
				"DELETE FROM process_deployments WHERE tenant_id = $1",
				"DELETE FROM users WHERE tenant_id = $1",
				"DELETE FROM tenants WHERE id = $1",
			} {
				_, err := db.ExecContext(ctx, statement, tenantID)
				require.NoError(t, err)
			}
		}
	})
}

func createPostgresIntegrationTenant(t *testing.T, client *ent.Client, namespace string) *ent.Tenant {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("BPMN integration " + namespace).
		SetCode("bpmn-integration-" + namespace).
		SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	return tenant
}

func seedPostgresClaimFixture(t *testing.T, client *ent.Client, db *sql.DB) postgresClaimFixture {
	t.Helper()
	ctx := context.Background()
	namespace := uuid.NewString()
	tenantIDs := make([]int, 0, 2)
	registerPostgresBPMNFixtureCleanup(t, db, &tenantIDs)
	tenant := createPostgresIntegrationTenant(t, client, namespace)
	tenantIDs = append(tenantIDs, tenant.ID)
	otherTenant := createPostgresIntegrationTenant(t, client, namespace+"-other")
	tenantIDs = append(tenantIDs, otherTenant.ID)

	actors := [2]*ent.User{}
	for i := range actors {
		actorNamespace := namespace + "-actor-" + strconv.Itoa(i+1)
		actor, err := client.User.Create().
			SetUsername(actorNamespace).
			SetEmail(actorNamespace + "@example.test").
			SetName("PostgreSQL claimant " + strconv.Itoa(i+1)).
			SetPasswordHash("integration-test-only").
			SetRole("service_agent").
			SetActive(true).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
		actors[i] = actor
	}

	createTask := func(taskNamespace string, fixtureTenant *ent.Tenant) (*ent.ProcessTask, *ent.ProcessInstance) {
		deployment, err := client.ProcessDeployment.Create().
			SetDeploymentID("deployment-" + taskNamespace).
			SetDeploymentName("PostgreSQL claim " + taskNamespace).
			SetTenantID(fixtureTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		definition, err := client.ProcessDefinition.Create().
			SetKey("process-" + taskNamespace).
			SetName("PostgreSQL claim " + taskNamespace).
			SetBpmnXML([]byte("<definitions/>")).
			SetDeploymentID(deployment.ID).
			SetTenantID(fixtureTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		instance, err := client.ProcessInstance.Create().
			SetProcessInstanceID("instance-" + taskNamespace).
			SetProcessDefinitionKey(definition.Key).
			SetProcessDefinitionID(definition.ID).
			SetTenantID(fixtureTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		task, err := client.ProcessTask.Create().
			SetTaskID("task-" + taskNamespace).
			SetProcessInstanceID(instance.ID).
			SetProcessDefinitionKey(definition.Key).
			SetTaskDefinitionKey("approval-" + taskNamespace).
			SetTaskName("PostgreSQL claim task").
			SetTaskVariables(map[string]interface{}{"fixture": taskNamespace}).
			SetTenantID(fixtureTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		return task, instance
	}

	task, instance := createTask(namespace, tenant)
	otherTask, otherInstance := createTask(namespace+"-other", otherTenant)
	return postgresClaimFixture{
		tenantID:      tenant.ID,
		otherTenantID: otherTenant.ID,
		actorIDs:      [2]int{actors[0].ID, actors[1].ID},
		taskID:        task.ID,
		taskKey:       task.TaskID,
		instanceID:    instance.ID,
		otherTaskID:   otherTask.ID,
		otherInstance: otherInstance.ID,
	}
}

func (b *postgresClaimLoadBarrier) interceptor(worker string) ent.Interceptor {
	var waitOnce sync.Once
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err != nil {
				return value, err
			}
			queryContext := entgo.QueryFromContext(ctx)
			if queryContext == nil || queryContext.Type != ent.TypeProcessTask || queryContext.Op != entgo.OpQueryOnly {
				return value, nil
			}
			rows, ok := value.([]*ent.ProcessTask)
			if !ok {
				return value, nil
			}
			for _, row := range rows {
				if row.ID != b.taskID {
					continue
				}
				waitOnce.Do(func() {
					b.arrived <- postgresClaimLoad{
						worker: worker, taskID: row.ID, tenantID: row.TenantID,
						assignee: row.Assignee, status: row.Status,
					}
					select {
					case <-b.release:
					case <-ctx.Done():
					}
				})
				break
			}
			return value, nil
		})
	})
}

func TestClaimTaskConcurrentCASPostgres(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	migrateBPMNPostgresIntegrationTables(t, setupClient,
		migrate.ProcessDeploymentsTable,
		migrate.ProcessDefinitionsTable,
		migrate.ProcessInstancesTable,
		migrate.ProcessTasksTable,
	)
	fixture := seedPostgresClaimFixture(t, setupClient, setupDB)
	targetBefore, err := setupClient.ProcessTask.Query().Where(
		processtask.ID(fixture.taskID), processtask.TenantID(fixture.tenantID),
	).Only(context.Background())
	require.NoError(t, err)
	controlBefore, err := setupClient.ProcessTask.Query().Where(
		processtask.ID(fixture.otherTaskID), processtask.TenantID(fixture.otherTenantID),
	).Only(context.Background())
	require.NoError(t, err)
	targetBeforeSnapshot := snapshotPostgresProcessTask(targetBefore)
	controlBeforeSnapshot := snapshotPostgresProcessTask(controlBefore)
	clientA, _ := openBPMNPostgresIntegrationClient(t)
	clientB, _ := openBPMNPostgresIntegrationClient(t)
	barrier := &postgresClaimLoadBarrier{
		taskID: fixture.taskID, arrived: make(chan postgresClaimLoad, 2), release: make(chan struct{}),
	}
	clientA.ProcessTask.Intercept(barrier.interceptor("claim-worker-a"))
	clientB.ProcessTask.Intercept(barrier.interceptor("claim-worker-b"))
	engines := [2]*CustomProcessEngine{
		NewCustomProcessEngine(clientA, zap.NewNop().Sugar()).(*CustomProcessEngine),
		NewCustomProcessEngine(clientB, zap.NewNop().Sugar()).(*CustomProcessEngine),
	}

	results := make(chan postgresClaimResult, 2)
	for i, actorID := range fixture.actorIDs {
		go func(engine *CustomProcessEngine, claimantID int) {
			ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: claimantID, TenantID: fixture.tenantID, CanUpdateAllTasks: true,
			})
			results <- postgresClaimResult{
				actorID: claimantID,
				err:     engine.TaskService().ClaimTask(ctx, fixture.taskKey, strconv.Itoa(claimantID)),
			}
		}(engines[i], actorID)
	}

	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	loads := make([]postgresClaimLoad, 0, 2)
	for len(loads) < 2 {
		select {
		case load := <-barrier.arrived:
			loads = append(loads, load)
		case result := <-results:
			require.NoError(t, result.err, "claim returned before both transactions observed the task")
			require.FailNow(t, "claim returned before PostgreSQL CAS barrier")
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for both PostgreSQL claim transactions")
		}
	}
	for _, load := range loads {
		require.Equal(t, fixture.taskID, load.taskID)
		require.Equal(t, fixture.tenantID, load.tenantID)
		require.Empty(t, load.assignee, "worker %s did not observe an unassigned task", load.worker)
		require.Equal(t, common.ProcessTaskStatusCreated, load.status)
	}
	require.ElementsMatch(t, []string{"claim-worker-a", "claim-worker-b"}, []string{loads[0].worker, loads[1].worker})
	close(barrier.release)
	released = true

	claimResults := make([]postgresClaimResult, 0, 2)
	resultTimeout := time.NewTimer(postgresIntegrationTimeout)
	defer resultTimeout.Stop()
	for len(claimResults) < 2 {
		select {
		case result := <-results:
			claimResults = append(claimResults, result)
		case <-resultTimeout.C:
			require.FailNow(t, "timed out waiting for PostgreSQL claim results")
		}
	}
	successes, conflicts := 0, 0
	winnerID := 0
	for _, result := range claimResults {
		if result.err == nil {
			successes++
			winnerID = result.actorID
			continue
		}
		var appErr *common.AppError
		if errors.As(result.err, &appErr) && appErr.Code == common.ErrCodeConflict {
			conflicts++
		}
	}
	require.Equal(t, 1, successes, "claim results: %v", claimResults)
	require.Equal(t, 1, conflicts, "claim results: %v", claimResults)

	persistedTask, err := setupClient.ProcessTask.Query().Where(
		processtask.ID(fixture.taskID), processtask.TenantID(fixture.tenantID),
	).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(winnerID), persistedTask.Assignee)
	require.Equal(t, common.ProcessTaskStatusAssigned, persistedTask.Status)
	require.False(t, persistedTask.AssignedTime.IsZero())
	require.True(t, persistedTask.StartedTime.IsZero())
	require.True(t, persistedTask.CompletedTime.IsZero())
	require.True(t, persistedTask.UpdatedAt.After(targetBeforeSnapshot.UpdatedAt))
	expectedTarget := targetBeforeSnapshot
	expectedTarget.Assignee = strconv.Itoa(winnerID)
	expectedTarget.Status = common.ProcessTaskStatusAssigned
	expectedTarget.AggregationVersion++
	expectedTarget.AssignedTime = persistedTask.AssignedTime
	expectedTarget.UpdatedAt = persistedTask.UpdatedAt
	require.Equal(t, expectedTarget, snapshotPostgresProcessTask(persistedTask))
	audits, err := setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.tenantID),
		processauditlog.ProcessInstanceID(fixture.instanceID),
		processauditlog.Action(AuditActionTaskClaimed),
	).All(context.Background())
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, fixture.tenantID, audits[0].TenantID)
	require.Equal(t, fixture.instanceID, audits[0].ProcessInstanceID)
	require.Equal(t, AuditActionTaskClaimed, audits[0].Action)
	require.Equal(t, winnerID, audits[0].AssigneeID)

	otherTask, err := setupClient.ProcessTask.Query().Where(
		processtask.ID(fixture.otherTaskID), processtask.TenantID(fixture.otherTenantID),
	).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, controlBeforeSnapshot, snapshotPostgresProcessTask(otherTask))
	otherAudits, err := setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.otherTenantID),
		processauditlog.ProcessInstanceID(fixture.otherInstance),
		processauditlog.Action(AuditActionTaskClaimed),
	).Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, otherAudits)
}

func TestBPMNTaskTerminalCommandsRaceWithCompletionPostgres(t *testing.T) {
	tests := []struct {
		command       BPMNTaskCommand
		successAction string
		mutate        func(*CustomProcessEngine, context.Context, postgresClaimFixture) error
	}{
		{BPMNTaskCommandAssign, AuditActionTaskAssigned, func(engine *CustomProcessEngine, ctx context.Context, fixture postgresClaimFixture) error {
			return engine.TaskService().AssignTask(ctx, fixture.taskKey, strconv.Itoa(fixture.actorIDs[1]))
		}},
		{BPMNTaskCommandCancel, AuditActionTaskCancelled, func(engine *CustomProcessEngine, ctx context.Context, fixture postgresClaimFixture) error {
			return engine.TaskService().CancelTask(ctx, fixture.taskKey, "race")
		}},
		{BPMNTaskCommandSetVariables, AuditActionTaskVariablesChanged, func(engine *CustomProcessEngine, ctx context.Context, fixture postgresClaimFixture) error {
			return engine.TaskService().SetTaskVariables(ctx, fixture.taskKey, map[string]interface{}{"race_mutation": true})
		}},
	}

	for _, tt := range tests {
		t.Run(string(tt.command), func(t *testing.T) {
			setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
			require.NoError(t, setupClient.Schema.Create(context.Background()))
			fixture := seedPostgresClaimFixture(t, setupClient, setupDB)
			task := setupClient.ProcessTask.GetX(context.Background(), fixture.taskID)
			instance := setupClient.ProcessInstance.GetX(context.Background(), fixture.instanceID)
			definition := setupClient.ProcessDefinition.GetX(context.Background(), instance.ProcessDefinitionID)
			xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="task-race" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="` + task.TaskDefinitionKey + `" name="Race task" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-task" sourceRef="start" targetRef="` + task.TaskDefinitionKey + `" />
    <bpmn:sequenceFlow id="to-end" sourceRef="` + task.TaskDefinitionKey + `" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
			_, err := setupClient.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(context.Background())
			require.NoError(t, err)

			clientA, _ := openBPMNPostgresIntegrationClient(t)
			clientB, _ := openBPMNPostgresIntegrationClient(t)
			barrier := &postgresClaimLoadBarrier{
				taskID: fixture.taskID, arrived: make(chan postgresClaimLoad, 2), release: make(chan struct{}),
			}
			clientA.ProcessTask.Intercept(barrier.interceptor("complete"))
			clientB.ProcessTask.Intercept(barrier.interceptor(string(tt.command)))
			completeEngine := NewCustomProcessEngine(clientA, zap.NewNop().Sugar()).(*CustomProcessEngine)
			mutationEngine := NewCustomProcessEngine(clientB, zap.NewNop().Sugar()).(*CustomProcessEngine)
			completeCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: fixture.actorIDs[0], TenantID: fixture.tenantID, CanUpdateAllTasks: true,
			})
			mutationCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: fixture.actorIDs[1], TenantID: fixture.tenantID, CanUpdateAllTasks: true,
			})

			results := make(chan postgresTaskRaceResult, 2)
			go func() {
				results <- postgresTaskRaceResult{command: BPMNTaskCommandComplete, err: completeEngine.CompleteTask(completeCtx, fixture.taskKey, map[string]interface{}{"completion": true})}
			}()
			go func() {
				results <- postgresTaskRaceResult{command: tt.command, err: tt.mutate(mutationEngine, mutationCtx, fixture)}
			}()

			for i := 0; i < 2; i++ {
				select {
				case <-barrier.arrived:
				case <-time.After(postgresIntegrationTimeout):
					require.FailNow(t, "timed out waiting for both task race loads")
				}
			}
			close(barrier.release)

			raceResults := []postgresTaskRaceResult{<-results, <-results}
			successes, conflicts := 0, 0
			var winner BPMNTaskCommand
			for _, result := range raceResults {
				if result.err == nil {
					successes++
					winner = result.command
					continue
				}
				var appErr *common.AppError
				if errors.As(result.err, &appErr) && appErr.Code == common.ErrCodeConflict {
					conflicts++
				}
			}
			require.Equal(t, 1, successes, "race results: %v", raceResults)
			require.Equal(t, 1, conflicts, "race results: %v", raceResults)

			persisted := setupClient.ProcessTask.GetX(context.Background(), fixture.taskID)
			require.Equal(t, 1, persisted.AggregationVersion)
			if winner == BPMNTaskCommandComplete {
				require.Equal(t, common.ProcessTaskStatusCompleted, persisted.Status)
				require.NotEqual(t, true, persisted.TaskVariables["race_mutation"])
			} else {
				switch tt.command {
				case BPMNTaskCommandAssign:
					require.Equal(t, common.ProcessTaskStatusAssigned, persisted.Status)
				case BPMNTaskCommandCancel:
					require.Equal(t, common.ProcessTaskStatusCancelled, persisted.Status)
				case BPMNTaskCommandSetVariables:
					require.Equal(t, true, persisted.TaskVariables["race_mutation"])
				}
			}
			require.Equal(t, 1, setupClient.ProcessAuditLog.Query().Where(
				processauditlog.TenantID(fixture.tenantID),
				processauditlog.ProcessInstanceID(fixture.instanceID),
				processauditlog.Action(AuditActionTaskMutationRejected),
			).CountX(context.Background()))
			successActions := []string{AuditActionTaskCompleted, tt.successAction}
			require.Equal(t, 1, setupClient.ProcessAuditLog.Query().Where(
				processauditlog.TenantID(fixture.tenantID),
				processauditlog.ProcessInstanceID(fixture.instanceID),
				processauditlog.ActionIn(successActions...),
			).CountX(context.Background()))
		})
	}
}

func TestBPMNParticipantCompletionCASLoserAuditsAfterReassignmentPostgres(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	require.NoError(t, setupClient.Schema.Create(context.Background()))
	fixture := seedPostgresClaimFixture(t, setupClient, setupDB)
	task := setupClient.ProcessTask.GetX(context.Background(), fixture.taskID)
	task = setupClient.ProcessTask.UpdateOne(task).
		SetAssignee(strconv.Itoa(fixture.actorIDs[0])).
		SetStatus(common.ProcessTaskStatusAssigned).
		SaveX(context.Background())
	instance := setupClient.ProcessInstance.GetX(context.Background(), fixture.instanceID)
	definition := setupClient.ProcessDefinition.GetX(context.Background(), instance.ProcessDefinitionID)
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="participant-reassignment-race" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="` + task.TaskDefinitionKey + `" name="Participant task" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-task" sourceRef="start" targetRef="` + task.TaskDefinitionKey + `" />
    <bpmn:sequenceFlow id="to-end" sourceRef="` + task.TaskDefinitionKey + `" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
	setupClient.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).SaveX(context.Background())

	completionClient, _ := openBPMNPostgresIntegrationClient(t)
	assignmentClient, _ := openBPMNPostgresIntegrationClient(t)
	barrier := &postgresTaskMutationBarrier{arrived: make(chan struct{}), release: make(chan struct{})}
	completionClient.ProcessTask.Use(barrier.hook())
	completionEngine := NewCustomProcessEngine(completionClient, zap.NewNop().Sugar()).(*CustomProcessEngine)
	assignmentEngine := NewCustomProcessEngine(assignmentClient, zap.NewNop().Sugar()).(*CustomProcessEngine)
	participantCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: fixture.actorIDs[0], TenantID: fixture.tenantID,
	})
	elevatedCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: fixture.actorIDs[1], TenantID: fixture.tenantID, CanUpdateAllTasks: true,
	})

	completionResult := make(chan error, 1)
	go func() {
		completionResult <- completionEngine.CompleteTask(
			participantCtx, fixture.taskKey, map[string]interface{}{"participant_completion": true},
		)
	}()
	select {
	case <-barrier.arrived:
	case <-time.After(postgresIntegrationTimeout):
		require.FailNow(t, "timed out waiting for participant completion task CAS")
	}
	require.NoError(t, assignmentEngine.TaskService().AssignTask(
		elevatedCtx, fixture.taskKey, strconv.Itoa(fixture.actorIDs[1]),
	))
	close(barrier.release)

	var completionErr error
	select {
	case completionErr = <-completionResult:
	case <-time.After(postgresIntegrationTimeout):
		require.FailNow(t, "timed out waiting for participant completion CAS loser")
	}
	var appErr *common.AppError
	require.ErrorAs(t, completionErr, &appErr)
	require.Equal(t, common.ErrCodeConflict, appErr.Code)

	persisted := setupClient.ProcessTask.GetX(context.Background(), fixture.taskID)
	require.Equal(t, strconv.Itoa(fixture.actorIDs[1]), persisted.Assignee)
	require.Equal(t, common.ProcessTaskStatusAssigned, persisted.Status)
	require.Equal(t, task.AggregationVersion+1, persisted.AggregationVersion)
	require.NotEqual(t, true, persisted.TaskVariables["participant_completion"])

	rejectionAudits := setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.tenantID),
		processauditlog.ProcessInstanceID(fixture.instanceID),
		processauditlog.Action(AuditActionTaskMutationRejected),
	).AllX(context.Background())
	require.Len(t, rejectionAudits, 1)
	require.Equal(t, fixture.actorIDs[0], rejectionAudits[0].UserID)
	require.Equal(t, "PostgreSQL claimant 1", rejectionAudits[0].UserName)
	require.Equal(t, string(BPMNTaskCommandComplete), rejectionAudits[0].Metadata["command"])
	require.Equal(t, 1, setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.tenantID),
		processauditlog.ProcessInstanceID(fixture.instanceID),
		processauditlog.Action(AuditActionTaskAssigned),
	).CountX(context.Background()))
}

func TestBPMNProcessInstanceVariablesConcurrentCASPostgres(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	require.NoError(t, setupClient.Schema.Create(context.Background()))
	fixture := seedPostgresClaimFixture(t, setupClient, setupDB)
	before := setupClient.ProcessInstance.GetX(context.Background(), fixture.instanceID)

	clients := [2]*ent.Client{}
	engines := [2]*CustomProcessEngine{}
	arrived := make(chan string, 2)
	release := make(chan struct{})
	for i := range clients {
		clients[i], _ = openBPMNPostgresIntegrationClient(t)
		worker := "variables-worker-" + strconv.Itoa(i+1)
		clients[i].ProcessInstance.Intercept((&postgresProcessInstanceLoadBarrier{
			instanceID: fixture.instanceID, worker: worker, arrived: arrived, release: release,
		}).interceptor())
		engines[i] = NewCustomProcessEngine(clients[i], zap.NewNop().Sugar()).(*CustomProcessEngine)
	}

	results := make(chan postgresProcessVariableRaceResult, 2)
	for i := range engines {
		go func(index int) {
			value := "value-" + strconv.Itoa(index+1)
			ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: fixture.actorIDs[index], TenantID: fixture.tenantID, CanUpdateAllInstances: true,
			})
			results <- postgresProcessVariableRaceResult{
				value: value,
				err: engines[index].ProcessInstanceService().SetProcessInstanceVariables(
					ctx, before.ProcessInstanceID, map[string]interface{}{"race": value},
				),
			}
		}(i)
	}

	seen := make([]string, 0, 2)
	for len(seen) < 2 {
		select {
		case worker := <-arrived:
			seen = append(seen, worker)
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for process variable load barrier")
		}
	}
	require.ElementsMatch(t, []string{"variables-worker-1", "variables-worker-2"}, seen)
	close(release)

	raceResults := make([]postgresProcessVariableRaceResult, 0, 2)
	for len(raceResults) < 2 {
		select {
		case result := <-results:
			raceResults = append(raceResults, result)
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for process variable race results")
		}
	}
	successes, conflicts := 0, 0
	winner := ""
	for _, result := range raceResults {
		if result.err == nil {
			successes++
			winner = result.value
			continue
		}
		var appErr *common.AppError
		if errors.As(result.err, &appErr) && appErr.Code == common.ErrCodeConflict {
			conflicts++
		}
	}
	require.Equal(t, 1, successes, "race results: %v", raceResults)
	require.Equal(t, 1, conflicts, "race results: %v", raceResults)
	after := setupClient.ProcessInstance.GetX(context.Background(), fixture.instanceID)
	require.Equal(t, before.Version+1, after.Version)
	require.Equal(t, map[string]interface{}{"race": winner}, after.Variables)
	require.Equal(t, 1, setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.tenantID),
		processauditlog.ProcessInstanceID(fixture.instanceID),
		processauditlog.Action(AuditActionVariableChanged),
	).CountX(context.Background()))
}
