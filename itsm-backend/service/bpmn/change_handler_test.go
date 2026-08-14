package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

func newChangeHandlerTestClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:change_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	return client
}

func createTestChangeForHandler(t *testing.T, client *ent.Client, tenantID int, status string) *ent.Change {
	t.Helper()
	c, err := client.Change.Create().
		SetTitle("测试变更").
		SetType("normal").
		SetStatus(status).
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetTenantID(tenantID).
		SetCreatedBy(1).
		Save(context.Background())
	require.NoError(t, err)
	return c
}

func TestChangeServiceTaskHandler_ApproveChangeAction_DoesNotWriteInvalidStatus(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "approve_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status,
		"approve_change 这个 action 在 CAB 审批节点和排期节点都会触发，本身不代表审批结果，不应该改变 Change.Status")
}

func TestChangeServiceTaskHandler_ScheduleChangeAction_WritesApproved(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "schedule_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status)
}

func TestChangeServiceTaskHandler_RejectChangeAction_WritesRejected(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	c := createTestChangeForHandler(t, client, 1, "pending")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "reject_change",
		"business_id": float64(c.ID),
	})
	require.NoError(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status)
}

func TestChangeServiceTaskHandler_ScheduleChangeAction_InvalidTransitionRejected(t *testing.T) {
	client := newChangeHandlerTestClient(t)
	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)

	// rejected 是终态，不允许再转成 approved —— IsValidChangeStatusTransition 必须真的被遵守。
	c := createTestChangeForHandler(t, client, 1, "rejected")

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "schedule_change",
		"business_id": float64(c.ID),
	})
	require.Error(t, err)

	updated, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status, "非法转换必须被拒绝，不能静默写入")
}
