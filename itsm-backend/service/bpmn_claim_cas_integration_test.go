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

type postgresClaimLoadBarrier struct {
	taskID  int
	arrived chan postgresClaimLoad
	release chan struct{}
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
	audits, err := setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.tenantID),
		processauditlog.ProcessInstanceID(fixture.instanceID),
		processauditlog.Action(AuditActionTaskClaimed),
	).All(context.Background())
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, winnerID, audits[0].AssigneeID)

	otherTask, err := setupClient.ProcessTask.Query().Where(
		processtask.ID(fixture.otherTaskID), processtask.TenantID(fixture.otherTenantID),
	).Only(context.Background())
	require.NoError(t, err)
	require.Empty(t, otherTask.Assignee)
	require.Equal(t, common.ProcessTaskStatusCreated, otherTask.Status)
	otherAudits, err := setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(fixture.otherTenantID),
		processauditlog.ProcessInstanceID(fixture.otherInstance),
		processauditlog.Action(AuditActionTaskClaimed),
	).Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, otherAudits)
}
