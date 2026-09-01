package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupUserTaskCallbackEnv 部署模板并返回可用的引擎/租户上下文。
func setupUserTaskCallbackEnv(t *testing.T) (*ent.Client, ProcessEngine, context.Context, int) {
	t.Helper()

	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("UserTask Callback Tenant").
		SetCode("usertask-callback-tenant").
		SetDomain("usertask-callback.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	reader, err := client.User.Create().
		SetUsername("usertask-callback-reader").
		SetEmail("usertask-callback-reader@example.test").
		SetName("UserTask Callback Reader").
		SetPasswordHash("test").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger)
	injectEngineChangeCallbackTestService(t, engine, client)

	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	ctx = WithBPMNAccessScope(ctx, BPMNAccessScope{
		UserID:            reader.ID,
		TenantID:          tenant.ID,
		CanReadAllTasks:   true,
		CanUpdateAllTasks: true,
	})
	return client, engine, ctx, tenant.ID
}

func findTaskByDefinitionKey(t *testing.T, client *ent.Client, ctx context.Context, instanceID int, key string) *ent.ProcessTask {
	t.Helper()
	task, err := client.ProcessTask.Query().
		Where(
			processtask.ProcessInstanceID(instanceID),
			processtask.TaskDefinitionKey(key),
		).
		Only(ctx)
	require.NoError(t, err, "应能找到节点 %s 对应的流程任务", key)
	return task
}

func createUserTaskCallbackChange(t *testing.T, client *ent.Client, ctx context.Context, tenantID, requesterID int, number string) (*ent.Ticket, *ent.Change) {
	t.Helper()
	workItem := client.Ticket.Create().
		SetTitle("User task callback change").
		SetStatus("draft").
		SetTicketNumber(number).
		SetType("change").
		SetRecordClass("change_request").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		SaveX(ctx)
	changeEntity := client.Change.Create().
		SetWorkItemID(workItem.ID).
		SaveX(ctx)
	return workItem, changeEntity
}

// TestUserTaskWithServiceTaskTypeMetadataTriggersCallback 是回归测试：
// change_normal_flow.bpmn 的 Activity_CABApproval 是 UserTask，但带了
// service_task_type=change_task / action=approve_change 的 extensionElements metaData，
// 对应的 ChangeServiceTaskHandler 已完整实现并注册。完成这个任务时必须真的调用
// handler，把 Change.Status 更新成 pending_approval——这在此前从未发生过，因为：
//  1. BPMNUserTask 结构体根本没有解析 extensionElements，metaData 在 xml.Unmarshal 时被丢弃；
//  2. CompleteTask 只在 ServiceTask 分支查 callback registry。
func TestUserTaskWithServiceTaskTypeMetadataTriggersCallback(t *testing.T) {
	client, engine, ctx, tenantID := setupUserTaskCallbackEnv(t)

	scope, err := BPMNAccessScopeFromContext(ctx)
	require.NoError(t, err)
	workItem, ch := createUserTaskCallbackChange(t, client, ctx, tenantID, scope.UserID, "T-USER-CALLBACK-1")

	instance, err := engine.StartProcess(ctx, "change_normal_flow", "change:callback-1", "change", workItem.ID, map[string]interface{}{
		"approval_required": true,
	})
	require.NoError(t, err)

	// StartProcess 返回的是创建时的实例快照，推进结果要重新读库确认。
	started, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "Activity_Assessment", started.CurrentActivityID,
		"流程启动后应停在第一个用户任务 Activity_Assessment")

	// 完成变更评估节点，让审批网关把流程路由到 CAB 审批节点。
	assessment := findTaskByDefinitionKey(t, client, ctx, instance.ID, "Activity_Assessment")
	require.NoError(t, engine.CompleteTask(ctx, assessment.TaskID, map[string]interface{}{}))

	advanced, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "Activity_CABApproval", advanced.CurrentActivityID,
		"approval_required=true 时网关应路由到 CAB 审批节点")

	// Empty completion variables contain no approval action/fact. The callback
	// row must keep the diagram-derived non-optional snapshot and block instead
	// of advancing. Only the outbox may turn an already persisted optional
	// snapshot into a skip.
	cabTask := findTaskByDefinitionKey(t, client, ctx, instance.ID, "Activity_CABApproval")
	require.NoError(t, engine.CompleteTask(ctx, cabTask.TaskID, map[string]interface{}{}))
	row := client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ProcessInstanceID(instance.ID),
		processcallbackoutbox.ProcessTaskID(cabTask.ID),
	).OnlyX(ctx)
	require.False(t, row.OptionalDeclared)
	processed, err := engine.(*CustomProcessEngine).ProcessPendingCallbacks(ctx, "cab-empty-approval-worker", 10)
	require.NoError(t, err)
	require.Zero(t, processed)
	row = client.ProcessCallbackOutbox.GetX(ctx, row.ID)
	require.Equal(t, "blocked", row.Status)

	afterCallback, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "Activity_CABApproval", afterCallback.CurrentActivityID)
	require.Equal(t, "running", afterCallback.Status)

	updated, err := client.Change.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "draft", requireChangeWorkItem(t, client, updated).Status,
		"完成 Activity_CABApproval 触发 ChangeServiceTaskHandler.approveChange，但该回调不改变状态（approve_change 是节点本身的固定 action，不代表审批结果），真正的状态转换发生在后续的 schedule_change/reject_change")
}

// TestUserTaskMetadataPersistsOnlyInImmutableDescriptor verifies that routing
// metadata never enters participant-editable form variables.
func TestUserTaskMetadataPersistsOnlyInImmutableDescriptor(t *testing.T) {
	client, engine, ctx, tenantID := setupUserTaskCallbackEnv(t)

	scope, err := BPMNAccessScopeFromContext(ctx)
	require.NoError(t, err)
	workItem, _ := createUserTaskCallbackChange(t, client, ctx, tenantID, scope.UserID, "T-USER-CALLBACK-2")

	instance, err := engine.StartProcess(ctx, "change_normal_flow", "change:callback-2", "change", workItem.ID, map[string]interface{}{
		"approval_required": true,
	})
	require.NoError(t, err)

	assessment := findTaskByDefinitionKey(t, client, ctx, instance.ID, "Activity_Assessment")
	require.NotContains(t, assessment.TaskVariables, "service_task_type")
	require.NotContains(t, assessment.TaskVariables, "action")
	require.Equal(t, "change_service_handler", assessment.CallbackHandlerID)
	require.Equal(t, "change_task", assessment.CallbackTaskType)
	require.Equal(t, "update_change", assessment.CallbackAction)

	// 反面用例：service_request_flow 的用户任务没有声明 service_task_type metadata，
	// 不应该出现这两个 key（否则回调会对无关流程无条件触发）。
	requestWorkItem := client.Ticket.Create().
		SetTitle("Service request callback").SetTicketNumber("T-USER-CALLBACK-SR-1").
		SetType("service_request").SetRecordClass("service_request_item").
		SetRequesterID(scope.UserID).SetTenantID(tenantID).SaveX(ctx)
	client.ServiceRequest.Create().SetTenantID(tenantID).SetTicketID(requestWorkItem.ID).
		SetCatalogID(1).SetRequesterID(scope.UserID).SaveX(ctx)
	srInstance, err := engine.StartProcess(ctx, "service_request_flow", "service_request:callback-3", "service_request", requestWorkItem.ID, map[string]interface{}{
		"approval_required": true,
	})
	require.NoError(t, err)

	srTasks, _, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		ProcessInstanceID: srInstance.ID,
		PageSize:          10,
	})
	require.NoError(t, err)
	require.Len(t, srTasks, 1)
	require.NotContains(t, srTasks[0].TaskVariables, "service_task_type",
		"未声明 service_task_type 的 UserTask 不应被写入该变量")
	require.NotContains(t, srTasks[0].TaskVariables, "action",
		"未声明 action 的 UserTask 不应被写入该变量")
	require.Equal(t, bpmnNoUserTaskCallbackHandlerID, srTasks[0].CallbackHandlerID)

	// 完成这个无 metadata 的用户任务不应报错（回调分发必须整体跳过）。
	require.NoError(t, engine.CompleteTask(ctx, srTasks[0].TaskID, map[string]interface{}{}))
}
