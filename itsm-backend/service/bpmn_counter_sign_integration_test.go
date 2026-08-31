//go:build integration

package service

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/migrate"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type postgresVoteLoadBarrier struct {
	childID int
	actorID int
	arrived chan int
	release chan struct{}
}

func (b *postgresVoteLoadBarrier) interceptor() ent.Interceptor {
	var waitOnce sync.Once
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err != nil {
				return value, err
			}
			rows, ok := value.([]*ent.ProcessTask)
			if !ok {
				return value, nil
			}
			for _, row := range rows {
				if row.ID != b.childID {
					continue
				}
				waitOnce.Do(func() {
					b.arrived <- b.actorID
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

func TestCounterSignDistinctChildVotesConvergePostgres(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	migrateBPMNPostgresIntegrationTables(t, setupClient,
		migrate.TenantsTable,
		migrate.UsersTable,
		migrate.ProcessDeploymentsTable,
		migrate.ProcessDefinitionsTable,
		migrate.ProcessInstancesTable,
		migrate.ProcessTasksTable,
		migrate.ProcessAuditLogsTable,
		migrate.ProcessApprovalDecisionsTable,
		migrate.ProcessCallbackOutboxesTable,
	)

	ctx := context.Background()
	namespace := uuid.NewString()
	tenant := createPostgresIntegrationTenant(t, setupClient, namespace+"-votes")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cancel()
		for _, statement := range []string{
			"DELETE FROM process_callback_outboxes WHERE tenant_id = $1",
			"DELETE FROM process_approval_decisions WHERE tenant_id = $1",
			"DELETE FROM process_audit_logs WHERE tenant_id = $1",
			"DELETE FROM process_tasks WHERE tenant_id = $1",
			"DELETE FROM process_instances WHERE tenant_id = $1",
			"DELETE FROM process_definitions WHERE tenant_id = $1",
			"DELETE FROM process_deployments WHERE tenant_id = $1",
			"DELETE FROM users WHERE tenant_id = $1",
			"DELETE FROM tenants WHERE id = $1",
		} {
			_, err := setupDB.ExecContext(cleanupCtx, statement, tenant.ID)
			require.NoError(t, err)
		}
	})

	actors := [2]*ent.User{}
	for i := range actors {
		identity := namespace + "-vote-actor-" + strconv.Itoa(i+1)
		actors[i] = setupClient.User.Create().
			SetUsername(identity).
			SetEmail(identity + "@example.test").
			SetName("Concurrent voter " + strconv.Itoa(i+1)).
			SetPasswordHash("integration-test-only").
			SetRole("service_agent").
			SetActive(true).
			SetTenantID(tenant.ID).
			SaveX(ctx)
	}
	deployment := setupClient.ProcessDeployment.Create().
		SetDeploymentID("deployment-" + namespace).
		SetDeploymentName("Concurrent counter-sign").
		SetTenantID(tenant.ID).
		SaveX(ctx)
	definition := setupClient.ProcessDefinition.Create().
		SetKey("counter-sign-" + namespace).
		SetName("Concurrent counter-sign").
		SetBpmnXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="concurrent-counter-sign" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-end" sourceRef="approval" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`)).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		SetIsActive(true).
		SetIsLatest(true).
		SaveX(ctx)
	instance := setupClient.ProcessInstance.Create().
		SetProcessInstanceID("instance-" + namespace).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetTenantID(tenant.ID).
		SaveX(ctx)
	parent := setupClient.ProcessTask.Create().
		SetTaskID("parent-" + namespace).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("approval").
		SetTaskName("Approval").
		SetTaskType("user_task").
		SetStatus(common.ProcessTaskStatusCreated).
		SetCallbackHandlerID(bpmnNoUserTaskCallbackHandlerID).
		SetTaskVariables(map[string]interface{}{
			"approval_type": "parallel",
			"threshold":     2,
			"total":         2,
			"completed":     0,
			"approved":      0,
			"rejected":      0,
			"preserved":     "parent-summary",
		}).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	children := [2]*ent.ProcessTask{}
	for i := range children {
		children[i] = setupClient.ProcessTask.Create().
			SetTaskID("child-" + namespace + "-" + strconv.Itoa(i+1)).
			SetProcessInstanceID(instance.ID).
			SetProcessDefinitionKey(definition.Key).
			SetTaskDefinitionKey("approval_counter").
			SetTaskName("Approval child").
			SetTaskType("user_task").
			SetAssignee(strconv.Itoa(actors[i].ID)).
			SetStatus(common.ProcessTaskStatusAssigned).
			SetParentTaskID(parent.TaskID).
			SetRootTaskID(parent.TaskID).
			SetTenantID(tenant.ID).
			SaveX(ctx)
	}

	clients := [2]*ent.Client{}
	engines := [2]*CustomProcessEngine{}
	barriers := [2]*postgresVoteLoadBarrier{}
	arrived := make(chan int, 2)
	release := make(chan struct{})
	for i := range clients {
		clients[i], _ = openBPMNPostgresIntegrationClient(t)
		barriers[i] = &postgresVoteLoadBarrier{
			childID: children[i].ID,
			actorID: actors[i].ID,
			arrived: arrived,
			release: release,
		}
		clients[i].ProcessTask.Intercept(barriers[i].interceptor())
		engines[i] = NewCustomProcessEngine(clients[i], zap.NewNop().Sugar()).(*CustomProcessEngine)
	}

	results := make(chan error, 2)
	for i := range engines {
		go func(index int) {
			voteCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: actors[index].ID, TenantID: tenant.ID,
			})
			results <- engines[index].TaskService().Vote(voteCtx, children[index].TaskID, &VoteRequest{
				Approved: true,
				Comment:  "concurrent approval " + strconv.Itoa(index+1),
			})
		}(i)
	}

	seenActors := make([]int, 0, 2)
	for len(seenActors) < 2 {
		select {
		case actorID := <-arrived:
			seenActors = append(seenActors, actorID)
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for distinct child vote barrier")
		}
	}
	require.ElementsMatch(t, []int{actors[0].ID, actors[1].ID}, seenActors)
	close(release)
	for range engines {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for concurrent child votes")
		}
	}

	persistedParent := setupClient.ProcessTask.GetX(ctx, parent.ID)
	require.Equal(t, common.ProcessTaskStatusCompleted, persistedParent.Status)
	require.Equal(t, 2, persistedParent.AggregationVersion)
	require.Equal(t, "parent-summary", persistedParent.TaskVariables["preserved"])
	for key, expected := range map[string]int{
		"threshold": 2,
		"total":     2,
		"completed": 2,
		"approved":  2,
		"rejected":  0,
	} {
		actual, ok := numericInt(persistedParent.TaskVariables[key])
		require.True(t, ok, "summary field %s is not numeric", key)
		require.Equal(t, expected, actual, "summary field %s", key)
	}
	require.Equal(t, "approved", persistedParent.TaskVariables["final_status"])
	require.Equal(t, "approved", persistedParent.TaskVariables["approvalResult"])

	for _, child := range children {
		require.Equal(t, common.ProcessTaskStatusCompleted, setupClient.ProcessTask.GetX(ctx, child.ID).Status)
	}
	persistedInstance := setupClient.ProcessInstance.GetX(ctx, instance.ID)
	require.Equal(t, "completed", persistedInstance.Status)
	require.Equal(t, "end", persistedInstance.CurrentActivityID)
	require.Equal(t, true, persistedInstance.Variables["approved"])
	require.Equal(t, 2, setupClient.ProcessApprovalDecision.Query().Where(
		processapprovaldecision.TenantID(tenant.ID),
		processapprovaldecision.ProcessInstanceKey(instance.ProcessInstanceID),
	).CountX(ctx))
	require.Equal(t, 1, setupClient.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(tenant.ID),
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.ActivityID(parent.TaskDefinitionKey),
		processauditlog.Action(AuditActionTaskCompleted),
	).CountX(ctx))
}
