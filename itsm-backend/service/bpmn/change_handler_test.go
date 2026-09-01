package bpmn

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

// setupChangeHandlerFixture 建一条 draft 状态的变更。change_normal_flow 的 7 个 userTask
// 都声明 service_task_type=change_task，动作的 change_id 在 UserTask 回调路径上来自
// 客户端提交的变量，可被伪造，因此所有动作都必须带租户约束。
func setupChangeHandlerFixture(t *testing.T) (*ent.Client, *ChangeServiceTaskHandler, int, *ent.Change) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:change_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("ch-1").SetDomain("ch-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("creator-ch").SetEmail("creator-ch@test.com").SetPasswordHash("x").
		SetName("发起人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().SetTitle("测试变更").SetStatus("draft").SetPriority("medium").SetType("change").SetRecordClass("change_request").SetTicketNumber("TKT-CHANGE-HANDLER").SetRequesterID(creator.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	changeEntity, err := client.Change.Create().
		SetCreatedBy(creator.ID).
		SetTenantID(tenant.ID).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewChangeServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, changeEntity
}

func requireHandlerChangeWorkItem(t *testing.T, client *ent.Client, entity *ent.Change) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Get(context.Background(), entity.WorkItemID)
	require.NoError(t, err)
	return workItem
}

// fakeChangeDomainService 是 ChangeDomainServiceInterface 的轻量测试替身，只用来验证
// createChange 的委托逻辑本身（参数传递是否正确、错误是否透传、OutputVars 是否用领域服务
// 返回的 ID）——不依赖 handlers/change 包（会话到 service/bpmn 反向 import handlers/change
// 造成的编译期依赖复杂度，跟生产代码是否真的绕过 WorkItem 创建这个问题正交，那部分由
// handlers/change 包内的集成测试
// TestChangeServiceTaskHandler_CreateChange_DelegatesToRealServiceAndCreatesWorkItem 覆盖，
// 那里用真实的 handlers/change.Service 实现，能验证到真实的 WorkItem 创建）。
type fakeChangeDomainService struct {
	calls []struct {
		tenantID, createdBy                      int
		title, description, changeType, priority string
	}
	returnID  int
	returnErr error
}

func (f *fakeChangeDomainService) CreateChangeForWorkflow(ctx context.Context, tenantID, createdBy int, title, description, changeType, priority string) (int, error) {
	f.calls = append(f.calls, struct {
		tenantID, createdBy                      int
		title, description, changeType, priority string
	}{tenantID, createdBy, title, description, changeType, priority})
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return f.returnID, nil
}

// TestChangeServiceTaskHandler_CreateChange_DelegatesToInjectedService 锁定 Wave 2 迁移：
// createChange 不再直接 h.client.Change.Create()（那样会绕过 WorkItem 事务化创建），必须
// 委托给注入的 ChangeDomainServiceInterface，并且用它返回的 ID 组装 OutputVars。
func TestChangeServiceTaskHandler_CreateChange_DelegatesToInjectedService(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_handler_create_delegate?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	handler := NewChangeServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	fake := &fakeChangeDomainService{returnID: 4242}
	handler.SetChangeService(fake)

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 7)
	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "create_change",
		"title":       "委托测试变更",
		"description": "验证委托而不是直接写 Ent",
		"type":        "normal",
		"priority":    "high",
		"created_by":  float64(99),
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectApplied, result.Status)
	assert.Equal(t, 4242, result.OutputVars["change_id"], "OutputVars 应该使用领域服务返回的 ID")

	require.Len(t, fake.calls, 1)
	call := fake.calls[0]
	assert.Equal(t, 7, call.tenantID)
	assert.Equal(t, 99, call.createdBy)
	assert.Equal(t, "委托测试变更", call.title)
	assert.Equal(t, "normal", call.changeType)
	assert.Equal(t, "high", call.priority)

	// 绕开委托、直接查 Ent：确认没有任何一条 changes 行是这次调用意外直接建出来的
	// （Wave 2 之前的旧实现会在这里留下一条 h.client.Change.Create() 建的行）。
	count, err := client.Change.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "createChange 不应该再直接对 Ent 写 Change 行，全部应该经过注入的领域服务")
}

// TestChangeServiceTaskHandler_CreateChange_FailsClosedWithoutService 锁定：没有注入
// ChangeDomainServiceInterface 时必须显式报错，不能静默 no-op 成功——那样会让 BPMN
// 流程以为变更已经创建，但实际上什么都没发生。
func TestChangeServiceTaskHandler_CreateChange_FailsClosedWithoutService(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_handler_create_no_service?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	handler := NewChangeServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	// 故意不调用 SetChangeService。

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 7)
	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "create_change",
		"title":  "未注入领域服务的变更",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "change service 未注入")
}

// TestChangeServiceTaskHandler_CreateChange_PropagatesServiceError 锁定领域服务返回的
// 错误（比如 creator 不存在/不活跃）会原样透传，不被吞掉或替换成一个误导性的成功结果。
func TestChangeServiceTaskHandler_CreateChange_PropagatesServiceError(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_handler_create_error?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	handler := NewChangeServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	fake := &fakeChangeDomainService{returnErr: assert.AnError}
	handler.SetChangeService(fake)

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 7)
	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "create_change",
		"title":  "会失败的变更",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "创建变更失败")
}

// TestChangeServiceTaskHandler_TenantScopedActions 覆盖九个带读写动作的租户隔离三件套：
// 同租户 Valid 生效、跨租户拒绝且零写入、无租户上下文 fail-closed。
func TestChangeServiceTaskHandler_TenantScopedActions(t *testing.T) {
	tests := []struct {
		name   string
		action string
		// setupStatus 是 Valid 用例的前置状态：状态类动作必须从白名单允许的
		// 前置状态出发（fixture 默认为 draft）
		setupStatus string
		extraVars   map[string]interface{}
		expected    CallbackEffectStatus
		assertValid func(t *testing.T, client *ent.Client, changeID int)
	}{
		{
			name:   "update",
			action: "update_change",
			extraVars: map[string]interface{}{
				"title": "改过的变更标题",
			},
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "改过的变更标题", requireHandlerChangeWorkItem(t, client, after).Title)
				assert.Equal(t, "draft", requireHandlerChangeWorkItem(t, client, after).Status, "update 只提交 title 时不得改状态")
			},
		},
		{
			name:     "approve",
			action:   "approve_change",
			expected: CallbackEffectBlocked,
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				// approve_change 这个 action 在 CAB 审批节点本身触发，不管审批结果是
				// approve 还是 reject 都会走到这里（节点自己的 action 是固定的，不代表
				// 审批结果）——真正的终态判定在 schedule_change/reject_change。这里只做
				// 存在性确认，不写状态，避免引入一个 canonical 状态机不认识的
				// "pending_approval" 中间态。
				assert.Equal(t, "draft", requireHandlerChangeWorkItem(t, client, after).Status, "approve_change 本身不应该改变 Change.Status")
			},
		},
		{
			name:        "reject",
			action:      "reject_change",
			setupStatus: "submitted",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "rejected", requireHandlerChangeWorkItem(t, client, after).Status)
			},
		},
		{
			name:        "schedule",
			action:      "schedule_change",
			setupStatus: "approved",
			extraVars: map[string]interface{}{
				"planned_start_date": "2026-09-01T00:00:00Z",
				"planned_end_date":   "2026-09-02T00:00:00Z",
			},
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "scheduled", requireHandlerChangeWorkItem(t, client, after).Status)
				assert.False(t, after.PlannedStartDate.IsZero(), "排期日期应写入")
				assert.False(t, after.PlannedEndDate.IsZero())
			},
		},
		{
			name:        "implement",
			action:      "implement_change",
			setupStatus: "scheduled",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "in_progress", requireHandlerChangeWorkItem(t, client, after).Status)
				assert.False(t, after.ActualStartDate.IsZero())
			},
		},
		{
			name:        "verify",
			action:      "verify_change",
			setupStatus: "in_progress",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "completed", requireHandlerChangeWorkItem(t, client, after).Status, "验证通过应对齐域状态机 completed")
			},
		},
		{
			name:        "close",
			action:      "close_change",
			setupStatus: "in_progress",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "completed", requireHandlerChangeWorkItem(t, client, after).Status, "关闭应对齐域状态机 completed")
				assert.False(t, after.ActualEndDate.IsZero())
			},
		},
		{
			name:      "assess_risk",
			action:    "assess_risk",
			extraVars: map[string]interface{}{
				// type 缺省为 normal → medium；用 emergency 验证分支
			},
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "medium", after.RiskLevel)
			},
		},
		{
			name:     "notify_stakeholders",
			action:   "notify_stakeholders",
			expected: CallbackEffectBlocked,
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "draft", requireHandlerChangeWorkItem(t, client, after).Status, "通知动作不应改状态")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("Valid", func(t *testing.T) {
				client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
				ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

				if tc.setupStatus != "" {
					_, err := client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetStatus(tc.setupStatus).Save(ctx)
					require.NoError(t, err)
				}

				vars := map[string]interface{}{"action": tc.action, "change_id": changeEntity.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				result, err := handler.Execute(ctx, nil, vars)
				require.NoError(t, err)
				expected := tc.expected
				if expected == "" {
					expected = CallbackEffectApplied
				}
				assert.Equal(t, expected, result.Status)
				tc.assertValid(t, client, changeEntity.ID)
			})

			t.Run("CrossTenant", func(t *testing.T) {
				client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
				otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

				before, err := client.Change.Get(context.Background(), changeEntity.ID)
				require.NoError(t, err)
				beforeWorkItem := requireHandlerChangeWorkItem(t, client, before)

				vars := map[string]interface{}{"action": tc.action, "change_id": changeEntity.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err = handler.Execute(otherCtx, nil, vars)
				assert.Error(t, err, "跨租户写入必须失败")

				after, err := client.Change.Get(context.Background(), changeEntity.ID)
				require.NoError(t, err)
				assert.Equal(t, beforeWorkItem.Status, requireHandlerChangeWorkItem(t, client, after).Status, "跨租户请求不得改状态")
				assert.Equal(t, beforeWorkItem.Title, requireHandlerChangeWorkItem(t, client, after).Title, "跨租户请求不得改标题")
				assert.Equal(t, before.RiskLevel, after.RiskLevel)
			})

			t.Run("NoTenant_FailClosed", func(t *testing.T) {
				_, handler, _, changeEntity := setupChangeHandlerFixture(t)

				vars := map[string]interface{}{"action": tc.action, "change_id": changeEntity.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err := handler.Execute(context.Background(), nil, vars)
				require.Error(t, err, "租户未知时必须拒绝执行")
				assert.Contains(t, err.Error(), "租户")
			})
		})
	}
}

// TestChangeServiceTaskHandler_AssessRisk_EmergencyType 锁定 assessRisk 的原有分支逻辑：
// emergency 类型变更评估为 high 风险。
func TestChangeServiceTaskHandler_AssessRisk_EmergencyType(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Change.UpdateOne(changeEntity).SetType("emergency").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":    "assess_risk",
		"change_id": changeEntity.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectApplied, result.Status)
	assert.Equal(t, "high", result.OutputVars["risk_level"], "emergency 变更应评估为 high")

	after, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "high", after.RiskLevel)
}

func TestChangeServiceTaskHandler_NotifyStakeholdersWithoutDurableDeliveryBlocks(t *testing.T) {
	_, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "notify_stakeholders", "change_id": changeEntity.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectBlocked, result.Status)
	assert.Equal(t, CallbackBlockHandlerContract, result.BlockCode)
}

func TestChangeServiceTaskHandler_ApproveRequiresMatchingPersistedDecision(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	actor := client.User.Query().OnlyX(ctx)
	deployment := client.ProcessDeployment.Create().
		SetDeploymentID("change-cab-deployment").SetDeploymentName("change CAB approval").
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).SetTenantID(tenantID).SaveX(ctx)
	definition := client.ProcessDefinition.Create().
		SetKey("change-cab-approval").SetName("change CAB approval").SetVersion("1").SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/> ")).SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).SaveX(ctx)
	instance := client.ProcessInstance.Create().
		SetProcessInstanceID("change-cab-instance").SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).SetStatus("running").SetVariables(map[string]interface{}{}).SetTenantID(tenantID).SaveX(ctx)
	task := client.ProcessTask.Create().
		SetTaskID("change-cab-task").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("Activity_CABApproval").SetTaskName("CAB 审批").SetTaskType("user_task").
		SetStatus("completed").SetTaskVariables(map[string]interface{}{"approvalAction": "approve"}).SetTenantID(tenantID).SaveX(ctx)
	variables := map[string]interface{}{"action": "approve_change", "change_id": changeEntity.ID}

	missing, err := handler.Execute(ctx, task, variables)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, missing.Status)
	require.Equal(t, CallbackBlockTargetMissing, missing.BlockCode)

	client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID(task.TaskID).
		SetProcessDefinitionKey(definition.Key).SetNodeKey(task.TaskDefinitionKey).
		SetActorID(actor.ID).SetAction("approve").SetDecision("approved").SetTenantID(tenantID).SaveX(ctx)

	task.TaskVariables["approvalAction"] = "reject"
	mismatched, err := handler.Execute(ctx, task, variables)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, mismatched.Status)

	task.TaskVariables["approvalAction"] = "approve"
	matched, err := handler.Execute(ctx, task, variables)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, matched.Status)

	crossTenantCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+1)
	_, err = handler.Execute(crossTenantCtx, task, variables)
	require.Error(t, err)
}

// TestChangeServiceTaskHandler_ScheduleChange_DateParsing 锁定 scheduleChange 的
// RFC3339 日期解析行为不因租户改造而变。
func TestChangeServiceTaskHandler_ScheduleChange_DateParsing(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	// 排期动作要求前置状态 approved（白名单 approved → scheduled）
	_, err := client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetStatus("approved").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":             "schedule_change",
		"change_id":          changeEntity.ID,
		"planned_start_date": "2026-09-01T08:00:00Z",
	})
	require.NoError(t, err)
	assert.True(t, result.Status == CallbackEffectApplied)

	after, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", requireHandlerChangeWorkItem(t, client, after).Status)
	want := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	assert.Equal(t, want, after.PlannedStartDate)
	assert.True(t, after.PlannedEndDate.IsZero(), "未提交结束日期时不应写入")
}

func TestChangeServiceTaskHandler_CloseCompletedBackfillsEndDateOnce(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetStatus("completed").Save(ctx)
	require.NoError(t, err)
	_, err = client.Change.UpdateOne(changeEntity).ClearActualEndDate().Save(ctx)
	require.NoError(t, err)

	variables := map[string]interface{}{"action": "close_change", "change_id": changeEntity.ID}
	_, err = handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	closed, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	require.False(t, closed.ActualEndDate.IsZero())
	closedAt := closed.ActualEndDate

	time.Sleep(time.Millisecond)
	_, err = handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	retried, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, closedAt, retried.ActualEndDate, "retry must preserve the original closure timestamp")
}

// A redelivered callback that observes its complete intended domain state must
// report idempotent.  Returning applied here would let the outbox record a
// durable effect that this delivery did not actually perform.
func TestChangeServiceTaskHandler_RetryWithoutWriteIsIdempotent(t *testing.T) {

	tests := []struct {
		name   string
		action string
		status string
		setup  func(*ent.Change)
	}{
		{
			name:   "reject",
			action: "reject_change",
			status: "rejected",
		},
		{
			name:   "implement",
			action: "implement_change",
			status: "in_progress",
		},
		{
			name:   "verify",
			action: "verify_change",
			status: "completed",
		},
		{
			name:   "close",
			action: "close_change",
			status: "completed",
			setup: func(entity *ent.Change) {
				entity.ActualEndDate = time.Now()
			},
		},
		{
			name:   "schedule",
			action: "schedule_change",
			status: "scheduled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			update := client.Change.UpdateOne(changeEntity).SetStatus(tt.status)
			if tt.setup != nil {
				entity := *changeEntity
				tt.setup(&entity)
				update.SetActualEndDate(entity.ActualEndDate)
			}
			require.NoError(t, update.Exec(ctx))

			result, err := handler.Execute(ctx, nil, map[string]interface{}{
				"action": tt.action, "change_id": changeEntity.ID,
			})
			require.NoError(t, err)
			assert.Equal(t, CallbackEffectIdempotent, result.Status)
		})
	}
}

// TestChangeServiceTaskHandler_ScheduleChangeAction_EmergencyStopsAtApproved covers Finding 4 of
// the Track4 final review: emergency-type changes have no "scheduled" state in their state
// machine (approved -> in_progress is the only legal next hop, a fast-track). scheduleChange must
// not blindly force a second hop to "scheduled" for emergency changes — it must detect that
// approved -> scheduled is not a legal transition for this type and stop at "approved", leaving
// Activity_Implement to take it directly to in_progress.
func TestChangeServiceTaskHandler_ScheduleChangeAction_EmergencyStopsAtApproved(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Change.UpdateOne(changeEntity).SetType("emergency").Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetStatus("approved").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":    "schedule_change",
		"change_id": changeEntity.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectIdempotent, result.Status)

	updated, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", requireHandlerChangeWorkItem(t, client, updated).Status)
}

// TestChangeServiceTaskHandler_ScheduleChangeAction_InvalidTransitionRejected 锁定
// transitionChangeStatus 真的在遵守 IsValidChangeStatusTransition，不会静默写入非法状态。
func TestChangeServiceTaskHandler_ScheduleChangeAction_InvalidTransitionRejected(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	// rejected 是终态，不允许再转成 approved —— IsValidChangeStatusTransition 必须真的被遵守。
	_, err := client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetStatus("rejected").Save(ctx)
	require.NoError(t, err)

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":    "schedule_change",
		"change_id": changeEntity.ID,
	})
	require.Error(t, err)

	updated, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", requireHandlerChangeWorkItem(t, client, updated).Status, "非法转换必须被拒绝，不能静默写入")
}
