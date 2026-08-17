package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupReleaseHandlerFixture(t *testing.T) (*ent.Client, *ReleaseServiceTaskHandler, int, *ent.Release) {
	client := enttest.Open(t, "sqlite3", "file:release_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("rh-1").SetDomain("rh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("creator-rh").SetEmail("creator-rh@test.com").SetPasswordHash("x").
		SetName("发布负责人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	release, err := client.Release.Create().
		SetReleaseNumber("REL-RH-1").SetTitle("测试发布").SetStatus("draft").
		SetCreatedBy(creator.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewReleaseServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, release
}

func TestReleaseHandler_TechReview_AppendsReleaseNotes(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "tech_review",
		"business_id": float64(release.ID),
		"comment":     "技术评审通过，无阻塞项",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Contains(t, updated.ReleaseNotes, "技术评审通过，无阻塞项")
	assert.Equal(t, "draft", updated.Status, "技术评审不改变发布状态")
}

func TestReleaseHandler_Approval_IsDocumentedNoop(t *testing.T) {
	_, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "approval",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err, "approval 动作是有意的空操作，权威状态转换在 ReleaseService.ApplyReleaseApproval 里")
	assert.True(t, result.Success)
}

func TestReleaseHandler_Execute_AdvancesThroughStatuses(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	// 手工把状态先推到 scheduled，模拟 approval 动作已经在 ApplyReleaseApproval 里
	// 真正生效之后的状态——execute/verify 动作要在这个前提下工作。
	_, err := client.Release.UpdateOneID(release.ID).SetStatus("scheduled").Save(ctx)
	require.NoError(t, err)

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "execute",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err)
	afterExecute, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Equal(t, "in-progress", afterExecute.Status)

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "verify",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err)
	afterVerify, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", afterVerify.Status)
	assert.False(t, afterVerify.ActualReleaseDate.IsZero())
}

func TestReleaseHandler_Schedule_IsIdempotentOnAlreadyScheduled(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Release.UpdateOneID(release.ID).SetStatus("scheduled").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "schedule",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err, "已经是 scheduled 时重复调用应该是幂等成功，不是状态机错误")
	assert.True(t, result.Success)
}

func TestReleaseHandler_InvalidBusinessID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _ := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "execute"})
	assert.Error(t, err)
}
