package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompleteTaskRollsBackDatabaseStateWhenAuditFails(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "complete-audit-rollback")
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskVariables(map[string]interface{}{"before": "kept"}).
		Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	forcedErr := errors.New("forced complete audit failure")
	failProcessAuditCreation(f.client, forcedErr)

	err = f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{
		"approvalAction": "approve",
		"approved":       true,
	})
	require.ErrorIs(t, err, forcedErr)

	afterTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	afterInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, common.ProcessTaskStatusCreated, afterTask.Status)
	assert.Equal(t, map[string]interface{}{"before": "kept"}, afterTask.TaskVariables)
	assert.Equal(t, instance.CurrentActivityID, afterInstance.CurrentActivityID)
	assert.Equal(t, instance.Status, afterInstance.Status)
	assert.Equal(t, instance.Variables, afterInstance.Variables)
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
}

func TestCompleteTaskAuditUsesTypedScopeActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "complete-audit-actor")
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	audit := f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(task.ProcessInstanceID),
		processauditlog.Action(AuditActionTaskCompleted),
	).OnlyX(f.userCtx)
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.actor.Name, audit.UserName)
}

func TestClaimTaskAuditFailureRollsBackBothClaimVariants(t *testing.T) {
	variants := map[string]func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error{
		"task key": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID))
		},
		"database id": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID)
		},
	}
	for name, claim := range variants {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, "claim-audit-rollback-"+strings.ReplaceAll(name, " ", "-"))
			task := f.createProcessTask(t, instance, f.tenant.ID, "claim-audit-rollback-"+strings.ReplaceAll(name, " ", "-"), "", "", f.actor.Role)
			forcedErr := errors.New("forced claim audit failure")
			failProcessAuditCreation(f.client, forcedErr)

			err := claim(f, f.typedTaskScopeOnlyCtx(f.actor, false), task)
			require.ErrorIs(t, err, forcedErr)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Empty(t, after.Assignee)
			assert.Equal(t, common.ProcessTaskStatusCreated, after.Status)
			assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
		})
	}
}

func TestClaimTaskConcurrentClaimersUseCAS(t *testing.T) {
	rawDB, err := sql.Open("sqlite3", testDSN())
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, rawDB)))
	require.NoError(t, client.Schema.Create(context.Background()))
	f := newBPMNAuthorizationFixtureWithClient(t, client)
	instance := f.createProcessInstance(t, f.tenant, "claim-concurrent")
	task := f.createProcessTask(t, instance, f.tenant.ID, "claim-concurrent", "", "", "service_agent")

	start := make(chan struct{})
	results := make(chan error, 2)
	claim := func(actor *ent.User) {
		<-start
		results <- f.engine.TaskService().ClaimTask(f.typedTaskScopeOnlyCtx(actor, false), task.TaskID, strconv.Itoa(actor.ID))
	}
	go claim(f.actor)
	go claim(f.outsider)
	close(start)

	errs := []error{<-results, <-results}
	successes, conflicts := 0, 0
	for _, claimErr := range errs {
		if claimErr == nil {
			successes++
			continue
		}
		var appErr *common.AppError
		if errors.As(claimErr, &appErr) && appErr.Code == common.ErrCodeConflict {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes, "claim errors: %v", errs)
	assert.Equal(t, 1, conflicts, "claim errors: %v", errs)
	after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	assert.Contains(t, []string{strconv.Itoa(f.actor.ID), strconv.Itoa(f.outsider.ID)}, after.Assignee)
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(AuditActionTaskClaimed)).CountX(f.userCtx))
}

func TestTaskMutationsUseAuthoritativeParticipantTokens(t *testing.T) {
	tokens := map[string]func(*bpmnAuthorizationFixture) (string, string, string){
		"email":           func(f *bpmnAuthorizationFixture) (string, string, string) { return "", f.actor.Email, "" },
		"primary-role":    func(f *bpmnAuthorizationFixture) (string, string, string) { return "", "", f.actor.Role },
		"additional-role": func(*bpmnAuthorizationFixture) (string, string, string) { return "", "", "network_eng" },
		"group":           func(*bpmnAuthorizationFixture) (string, string, string) { return "", "", "vpn-operators" },
	}
	mutations := map[string]func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error{
		"claim": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID))
		},
		"claim-by-id": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID)
		},
		"complete": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{})
		},
		"vote": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			_, err := f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
			if err != nil {
				return err
			}
			return f.engine.TaskService().Vote(ctx, task.TaskID, &VoteRequest{Approved: true})
		},
	}

	for mutationName, mutate := range mutations {
		for tokenName, candidates := range tokens {
			t.Run(mutationName+" by "+tokenName, func(t *testing.T) {
				f := newBPMNAuthorizationFixture(t)
				task := f.seedNonParticipantApprovalTask(t, strings.ReplaceAll(mutationName+"-"+tokenName, " ", "-"))
				assignee, users, groups := candidates(f)
				task, err := f.client.ProcessTask.UpdateOne(task).
					SetAssignee(assignee).SetCandidateUsers(users).SetCandidateGroups(groups).Save(f.userCtx)
				require.NoError(t, err)
				require.NoError(t, mutate(f, f.typedTaskScopeOnlyCtx(f.actor, false), task))
			})
		}
	}
}

func TestVoteRollsBackFinalVoteWhenParentAdvancementFailsAndCanRetry(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	parent := f.seedNonParticipantApprovalTask(t, "vote-parent-retry")
	parent, err := f.client.ProcessTask.UpdateOne(parent).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	children, err := f.engine.TaskService().CreateCounterSignTasks(
		f.typedTaskScopeOnlyCtx(f.actor, false), parent.TaskID,
		&CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1},
	)
	require.NoError(t, err)
	require.Len(t, children, 1)

	forcedErr := errors.New("forced parent advancement failure")
	var failOnce atomic.Bool
	failOnce.Store(true)
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.ProcessInstanceMutation); ok && failOnce.CompareAndSwap(true, false) {
				return nil, forcedErr
			}
			return next.Mutate(ctx, mutation)
		})
	})
	voteCtx := f.typedTaskScopeOnlyCtx(f.actor, false)

	err = f.engine.TaskService().Vote(voteCtx, children[0].TaskID, &VoteRequest{Approved: true})
	require.ErrorIs(t, err, forcedErr)
	assert.Equal(t, common.ProcessTaskStatusAssigned, f.client.ProcessTask.GetX(f.userCtx, children[0].ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCreated, f.client.ProcessTask.GetX(f.userCtx, parent.ID).Status)
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessTaskID(children[0].ID)).CountX(f.userCtx))

	require.NoError(t, f.engine.TaskService().Vote(voteCtx, children[0].TaskID, &VoteRequest{Approved: true}))
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, children[0].ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, parent.ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessInstance.GetX(f.userCtx, parent.ProcessInstanceID).Status)
}

func TestInternalCABCascadeIsNarrowTenantBoundAndAudited(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := seedInternalCascadeTask(t, f, "Activity_Schedule")
	req := BPMNInternalCascadeRequest{
		TenantID: f.tenant.ID, InstanceID: task.ProcessInstanceID, TaskID: task.TaskID,
		NodeKey: task.TaskDefinitionKey, Source: BPMNInternalSourceChangeCABCascade,
		Variables: map[string]interface{}{"change_id": 42},
	}

	badNode := req
	badNode.NodeKey = "Activity_Implement"
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badNode))
	badTenant := req
	badTenant.TenantID = f.otherTenant.ID
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badTenant))
	badSource := req
	badSource.Source = BPMNInternalSource("untrusted_caller")
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badSource))
	require.NoError(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, req))

	audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).OnlyX(f.userCtx)
	assert.Equal(t, "system", audit.UserName)
	assert.Equal(t, string(BPMNInternalSourceChangeCABCascade), audit.Metadata["source"])
	assert.Equal(t, task.TaskDefinitionKey, audit.Metadata["node_key"])
}

func TestCompleteTaskRunsServiceHandlerOnlyAfterTaskAndAuditCommit(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "post-commit-handler")
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="post-commit" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:serviceTask id="probe" name="Probe">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">post_commit_probe</bpmn:metaData></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-probe" sourceRef="approval" targetRef="probe" />
    <bpmn:sequenceFlow id="to-end" sourceRef="probe" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
	_, err = f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
	probe := &postCommitProbeHandler{client: f.client, taskID: task.TaskID}
	f.engine.callbackRegistry.RegisterHandler(probe)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	assert.True(t, probe.observedCommittedState)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID).Status)
}

type postCommitProbeHandler struct {
	client                 *ent.Client
	taskID                 string
	observedCommittedState bool
}

func (h *postCommitProbeHandler) GetTaskType() string  { return "post_commit_probe" }
func (h *postCommitProbeHandler) GetHandlerID() string { return "post_commit_probe" }
func (h *postCommitProbeHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *postCommitProbeHandler) Execute(ctx context.Context, _ *ent.ProcessTask, _ map[string]interface{}) (*dto.ServiceTaskResult, error) {
	task, err := h.client.ProcessTask.Query().Where(processtask.TaskID(h.taskID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := h.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(task.ProcessInstanceID),
		processauditlog.Action(AuditActionTaskCompleted),
	).Count(ctx)
	if err != nil {
		return nil, err
	}
	h.observedCommittedState = task.Status == common.ProcessTaskStatusCompleted && audits == 1
	if !h.observedCommittedState {
		return nil, fmt.Errorf("service handler observed uncommitted task state")
	}
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*postCommitProbeHandler)(nil)

func seedInternalCascadeTask(t *testing.T, f *bpmnAuthorizationFixture, nodeKey string) *ent.ProcessTask {
	t.Helper()
	instance := f.createProcessInstance(t, f.tenant, "internal-cascade-"+nodeKey)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="internal-cascade" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="%s" name="Cascade" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-cascade" sourceRef="start" targetRef="%s" />
    <bpmn:sequenceFlow id="to-end" sourceRef="%s" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, nodeKey, nodeKey, nodeKey)
	_, err := f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
	_, err = f.client.ProcessInstance.UpdateOne(instance).SetCurrentActivityID(nodeKey).SetCurrentActivityName("Cascade").Save(f.userCtx)
	require.NoError(t, err)
	task := f.createProcessTask(t, instance, f.tenant.ID, "internal-cascade-task", "", "", "")
	task, err = f.client.ProcessTask.UpdateOne(task).SetTaskDefinitionKey(nodeKey).SetTaskName("Cascade").Save(f.userCtx)
	require.NoError(t, err)
	return task
}
