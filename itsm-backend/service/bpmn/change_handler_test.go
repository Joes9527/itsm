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

	changeEntity, err := client.Change.Create().
		SetTitle("租户过滤测试变更").
		SetCreatedBy(creator.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewChangeServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, changeEntity
}

// TestChangeServiceTaskHandler_TenantScopedActions 覆盖九个带读写动作的租户隔离三件套：
// 同租户 Valid 生效、跨租户拒绝且零写入、无租户上下文 fail-closed。
func TestChangeServiceTaskHandler_TenantScopedActions(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		// setupStatus 是 Valid 用例的前置状态：状态类动作必须从白名单允许的
		// 前置状态出发（fixture 默认为 draft）
		setupStatus string
		extraVars   map[string]interface{}
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
				assert.Equal(t, "改过的变更标题", after.Title)
				assert.Equal(t, "draft", after.Status, "update 只提交 title 时不得改状态")
			},
		},
		{
			name:   "approve",
			action: "approve_change",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "pending_approval", after.Status)
			},
		},
		{
			name:        "reject",
			action:      "reject_change",
			setupStatus: "pending_approval",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "rejected", after.Status)
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
				assert.Equal(t, "scheduled", after.Status)
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
				assert.Equal(t, "in_progress", after.Status)
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
				assert.Equal(t, "completed", after.Status, "验证通过应对齐域状态机 completed")
			},
		},
		{
			name:        "close",
			action:      "close_change",
			setupStatus: "in_progress",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "completed", after.Status, "关闭应对齐域状态机 completed")
				assert.False(t, after.ActualEndDate.IsZero())
			},
		},
		{
			name:   "assess_risk",
			action: "assess_risk",
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
			name:   "notify_stakeholders",
			action: "notify_stakeholders",
			assertValid: func(t *testing.T, client *ent.Client, changeID int) {
				after, err := client.Change.Get(context.Background(), changeID)
				require.NoError(t, err)
				assert.Equal(t, "draft", after.Status, "通知动作不应改状态")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("Valid", func(t *testing.T) {
				client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
				ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

				if tc.setupStatus != "" {
					_, err := client.Change.UpdateOne(changeEntity).SetStatus(tc.setupStatus).Save(ctx)
					require.NoError(t, err)
				}

				vars := map[string]interface{}{"action": tc.action, "change_id": changeEntity.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				result, err := handler.Execute(ctx, nil, vars)
				require.NoError(t, err)
				assert.True(t, result.Success)
				tc.assertValid(t, client, changeEntity.ID)
			})

			t.Run("CrossTenant", func(t *testing.T) {
				client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
				otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

				before, err := client.Change.Get(context.Background(), changeEntity.ID)
				require.NoError(t, err)

				vars := map[string]interface{}{"action": tc.action, "change_id": changeEntity.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err = handler.Execute(otherCtx, nil, vars)
				assert.Error(t, err, "跨租户写入必须失败")

				after, err := client.Change.Get(context.Background(), changeEntity.ID)
				require.NoError(t, err)
				assert.Equal(t, before.Status, after.Status, "跨租户请求不得改状态")
				assert.Equal(t, before.Title, after.Title, "跨租户请求不得改标题")
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
	assert.True(t, result.Success)
	assert.Equal(t, "high", result.OutputVars["risk_level"], "emergency 变更应评估为 high")

	after, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "high", after.RiskLevel)
}

// TestChangeServiceTaskHandler_ScheduleChange_DateParsing 锁定 scheduleChange 的
// RFC3339 日期解析行为不因租户改造而变。
func TestChangeServiceTaskHandler_ScheduleChange_DateParsing(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	// 排期动作要求前置状态 approved（白名单 approved → scheduled）
	_, err := client.Change.UpdateOne(changeEntity).SetStatus("approved").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":            "schedule_change",
		"change_id":         changeEntity.ID,
		"planned_start_date": "2026-09-01T08:00:00Z",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	after, err := client.Change.Get(ctx, changeEntity.ID)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", after.Status)
	want := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	assert.Equal(t, want, after.PlannedStartDate)
	assert.True(t, after.PlannedEndDate.IsZero(), "未提交结束日期时不应写入")
}
