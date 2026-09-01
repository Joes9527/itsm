package bpmn_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	servicerequesthandler "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	. "itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func failResolvedTicketMutation(client *ent.Client) {
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if ticketMutation, ok := mutation.(*ent.TicketMutation); ok {
				if status, exists := ticketMutation.Status(); exists && status == "resolved" {
					return nil, errors.New("injected linked work item failure")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func setupServiceRequestHandlerFixture(t *testing.T) (*ent.Client, *ServiceRequestServiceTaskHandler, int, *ent.Ticket, *ent.ServiceRequest) {
	client := enttest.Open(t, "sqlite3", "file:service_request_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("srh-1").SetDomain("srh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-srh").SetEmail("requester-srh@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTitle("服务请求关联工单").SetTicketNumber("T-SRH-1").SetStatus("open").
		SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	sr, err := client.ServiceRequest.Create().
		SetTenantID(tenant.ID).SetTicketID(tkt.ID).SetCatalogID(1).SetRequesterID(requester.ID).
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	handler := NewServiceRequestServiceTaskHandler(client, logger)
	handler.SetServiceRequestService(servicerequesthandler.NewService(nil, nil, nil, client, workitemnumber.NewPostgreSQLAllocator(), logger, nil, nil, nil))
	return client, handler, tenant.ID, tkt, sr
}

func TestServiceRequestHandler_AssignRequest_SetsAuthoritativeWorkItemAssignee(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	assignee := client.User.Create().SetUsername("assignee-srh").SetEmail("assignee-srh@test.com").SetPasswordHash("x").SetName("处理人").SetTenantID(tenantID).SetActive(true).SaveX(ctx)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_request",
		"request_id":  float64(sr.ID),
		"assignee_id": float64(assignee.ID),
	})
	require.NoError(t, err)
	assert.True(t, result.Status == CallbackEffectApplied)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.Zero(t, updated.ProcessorID, "professional extension must not shadow WorkItem assignment")
	updatedWorkItem := client.Ticket.GetX(ctx, tkt.ID)
	require.Equal(t, assignee.ID, updatedWorkItem.AssigneeID)
}

func TestServiceRequestHandler_ProvisionCASLoserReturnsIdempotent(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	startedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var injected atomic.Bool
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if srMutation, ok := mutation.(*ent.ServiceRequestMutation); ok {
				if _, exists := srMutation.StartedAt(); exists && injected.CompareAndSwap(false, true) {
					_, injectErr := client.ServiceRequest.UpdateOneID(sr.ID).SetStartedAt(startedAt).AddVersion(1).Save(mutationCtx)
					require.NoError(t, injectErr)
				}
			}
			return next.Mutate(mutationCtx, mutation)
		})
	})

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "provision_resource", "request_id": sr.ID, "resource_type": "vm",
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, effect.Status)
	require.Equal(t, startedAt, client.ServiceRequest.GetX(ctx, sr.ID).StartedAt)
}

func TestServiceRequestHandler_UpdateRequestCASLoserClassifiesPayload(t *testing.T) {
	tests := []struct {
		name       string
		winnerCost string
		wantStatus CallbackEffectStatus
	}{
		{name: "same payload is idempotent", winnerCost: "CC-CAS", wantStatus: CallbackEffectIdempotent},
		{name: "different payload is blocked", winnerCost: "CC-OTHER", wantStatus: CallbackEffectBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			var injected atomic.Bool
			client.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
					if srMutation, ok := mutation.(*ent.ServiceRequestMutation); ok {
						if _, exists := srMutation.CostCenter(); exists && injected.CompareAndSwap(false, true) {
							_, injectErr := client.ServiceRequest.UpdateOneID(sr.ID).SetCostCenter(tc.winnerCost).AddVersion(1).Save(mutationCtx)
							require.NoError(t, injectErr)
						}
					}
					return next.Mutate(mutationCtx, mutation)
				})
			})

			effect, err := handler.Execute(ctx, nil, map[string]interface{}{
				"action": "update_request", "request_id": sr.ID, "cost_center": "CC-CAS",
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, effect.Status)
		})
	}
}

func TestServiceRequestHandler_CompleteRequest_UpdatesRequestAndLinkedTicket(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":          "complete_request",
		"request_id":      float64(sr.ID),
		"completion_note": "已开通",
	})
	require.NoError(t, err)
	assert.True(t, result.Status == CallbackEffectApplied)

	updatedSR, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.False(t, updatedSR.CompletedAt.IsZero())
	assert.Equal(t, "已开通", updatedSR.CompletionNote)

	updatedTicket, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", updatedTicket.Status)
}

func TestServiceRequestHandler_CompleteRequestRollsBackExtensionWhenWorkItemWriteFails(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	failResolvedTicketMutation(client)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":          "complete_request",
		"request_id":      sr.ID,
		"completion_note": "must rollback",
	})
	require.ErrorContains(t, err, "injected linked work item failure")

	afterRequest := client.ServiceRequest.GetX(context.Background(), sr.ID)
	require.True(t, afterRequest.CompletedAt.IsZero())
	require.Empty(t, afterRequest.CompletionNote)
	afterWorkItem := client.Ticket.GetX(context.Background(), tkt.ID)
	require.Equal(t, "open", afterWorkItem.Status)
}

func TestServiceRequestHandler_CompleteRequestCASConflictBlocksWithoutPartialWrite(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	var injected atomic.Bool
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if srMutation, ok := mutation.(*ent.ServiceRequestMutation); ok {
				if _, exists := srMutation.CompletedAt(); exists && injected.CompareAndSwap(false, true) {
					return 0, nil
				}
			}
			return next.Mutate(mutationCtx, mutation)
		})
	})

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "complete_request", "request_id": sr.ID, "completion_note": "ready",
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.True(t, client.ServiceRequest.GetX(ctx, sr.ID).CompletedAt.IsZero())
	require.Equal(t, "open", client.Ticket.GetX(ctx, tkt.ID).Status)
}

func TestServiceRequestHandler_RejectRequest_UpdatesLinkedTicketStatus(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":        "reject_request",
		"request_id":    float64(sr.ID),
		"reject_reason": "预算不足",
	})
	require.NoError(t, err)

	updatedTicket, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "closed", updatedTicket.Status)

	updatedSR, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.Contains(t, updatedSR.CompletionNote, "预算不足")
	_ = tkt
}

func TestServiceRequestHandler_ProvisionResource_SetsStartedAt(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":        "provision_resource",
		"request_id":    float64(sr.ID),
		"resource_type": "vm",
	})
	require.NoError(t, err)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.False(t, updated.StartedAt.IsZero())
}

func TestServiceRequestHandler_RetryPreservesFirstEffectTimestamps(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	provision := map[string]interface{}{
		"action": "provision_resource", "request_id": sr.ID, "resource_type": "vm",
	}
	firstProvision, err := handler.Execute(ctx, nil, provision)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectApplied, firstProvision.Status)
	firstStarted := client.ServiceRequest.GetX(ctx, sr.ID).StartedAt
	retriedProvision, err := handler.Execute(ctx, nil, provision)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, retriedProvision.Status)
	assert.Equal(t, firstStarted, client.ServiceRequest.GetX(ctx, sr.ID).StartedAt)

	complete := map[string]interface{}{
		"action": "complete_request", "request_id": sr.ID, "completion_note": "ready",
	}
	firstComplete, err := handler.Execute(ctx, nil, complete)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectApplied, firstComplete.Status)
	firstRequest := client.ServiceRequest.GetX(ctx, sr.ID)
	firstTicket := client.Ticket.GetX(ctx, tkt.ID)
	retriedComplete, err := handler.Execute(ctx, nil, complete)
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, retriedComplete.Status)
	afterRequest := client.ServiceRequest.GetX(ctx, sr.ID)
	afterTicket := client.Ticket.GetX(ctx, tkt.ID)
	assert.Equal(t, firstRequest.CompletedAt, afterRequest.CompletedAt)
	assert.Equal(t, firstTicket.ResolvedAt, afterTicket.ResolvedAt)
}

func TestServiceRequestHandler_CreateRequestBlocksBeforeEffect(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "create_request",
		"title":  "新请求",
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)
}

func TestServiceRequestHandler_InvalidRequestID_Blocks(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "assign_request"})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)
}

// TestServiceRequestHandler_UpdateRequest_WritesFormFields 锁定 P2.1 的 update_request
// 真实实现：纯表单元数据字段（无状态语义）按提交变量写入。
func TestServiceRequestHandler_UpdateRequest_WritesFormFields(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":              "update_request",
		"request_id":          float64(sr.ID),
		"cost_center":         "CC-001",
		"data_classification": "confidential",
		"needs_public_ip":     true,
		"compliance_ack":      true,
	})
	require.NoError(t, err)
	assert.True(t, result.Status == CallbackEffectApplied)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.Equal(t, "CC-001", updated.CostCenter)
	assert.Equal(t, "confidential", updated.DataClassification)
	assert.True(t, updated.NeedsPublicIP)
	assert.True(t, updated.ComplianceAck)
}

// TestServiceRequestHandler_UpdateRequest_CrossTenant 更新动作同样受租户约束。
func TestServiceRequestHandler_UpdateRequest_CrossTenant(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

	_, err := handler.Execute(otherCtx, nil, map[string]interface{}{
		"action":      "update_request",
		"request_id":  float64(sr.ID),
		"cost_center": "CC-EVIL",
	})
	assert.Error(t, err, "跨租户更新必须失败")

	after, err := client.ServiceRequest.Get(context.Background(), sr.ID)
	require.NoError(t, err)
	assert.Empty(t, after.CostCenter, "跨租户请求不得写入字段")
}

// TestServiceRequestHandler_RejectRequest_IllegalTransition_Rejected 锁定关联工单的
// 状态机校验：resolved 工单不能再被驳回/取消（resolved→closed 不在白名单内）。
func TestServiceRequestHandler_RejectRequest_IllegalTransition_Rejected(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Ticket.UpdateOne(tkt).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":     "reject_request",
		"request_id": float64(sr.ID),
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status, "resolved 工单不允许再被驳回")
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)

	after, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", after.Status, "非法转换不得改写工单")
}

// TestServiceRequestHandler_CompleteRequest_Idempotent 同状态幂等放行：
// 已 resolved 的工单重复 complete 不报错（服务请求补记完成时间）。
func TestServiceRequestHandler_CompleteRequest_Idempotent(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Ticket.UpdateOne(tkt).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":     "complete_request",
		"request_id": float64(sr.ID),
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectApplied, result.Status, "补记 completed_at 是真实首次写入")

	retried, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":     "complete_request",
		"request_id": float64(sr.ID),
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectIdempotent, retried.Status)

	after, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", after.Status, "幂等完成不得改变状态")
}

func TestServiceRequestHandler_NoWriteActionsReturnIdempotent(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.ServiceRequest.UpdateOne(sr).
		SetCostCenter("CC-001").
		SetStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOne(tkt).SetStatus("in_progress").Save(ctx)
	require.NoError(t, err)
	assignee := client.User.Create().SetUsername("same-assignee-srh").SetEmail("same-assignee-srh@test.com").SetPasswordHash("x").SetName("处理人").SetTenantID(tenantID).SetActive(true).SaveX(ctx)
	client.Ticket.UpdateOneID(tkt.ID).SetAssigneeID(assignee.ID).ExecX(ctx)

	for _, tc := range []struct {
		name string
		vars map[string]interface{}
	}{
		{name: "unchanged update", vars: map[string]interface{}{"action": "update_request", "request_id": sr.ID, "cost_center": "CC-001"}},
		{name: "same assignee", vars: map[string]interface{}{"action": "assign_request", "request_id": sr.ID, "assignee_id": assignee.ID}},
		{name: "already provisioning", vars: map[string]interface{}{"action": "provision_resource", "request_id": sr.ID, "resource_type": "vm"}},
		{name: "same linked status", vars: map[string]interface{}{"action": "approve_request", "request_id": sr.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			effect, execErr := handler.Execute(ctx, nil, tc.vars)
			require.NoError(t, execErr)
			require.Equal(t, CallbackEffectIdempotent, effect.Status)
		})
	}
}

func TestServiceRequestHandler_UnknownActionBlocks(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "invented_action"})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)
}

func TestServiceRequestHandler_DeterministicBindingFailuresBlock(t *testing.T) {
	_, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	tests := []struct {
		name      string
		variables map[string]interface{}
	}{
		{name: "missing request identity", variables: map[string]interface{}{"action": "complete_request"}},
		{name: "malformed expiry", variables: map[string]interface{}{"action": "update_request", "request_id": sr.ID, "expire_at": "tomorrow"}},
		{name: "invalid whitelist item", variables: map[string]interface{}{"action": "update_request", "request_id": sr.ID, "source_ip_whitelist": []interface{}{42}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			effect, err := handler.Execute(ctx, nil, tc.variables)
			require.NoError(t, err)
			require.Equal(t, CallbackEffectBlocked, effect.Status)
			require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)
		})
	}
}

// TestServiceRequestHandler_SetLinkedTicketStatus_AlwaysTenantScoped 锁定删除
// `if tenantID > 0` 死代码后的行为：关联工单写入恒带租户约束，跨租户零写入。
func TestServiceRequestHandler_SetLinkedTicketStatus_AlwaysTenantScoped(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

	_, err := handler.Execute(otherCtx, nil, map[string]interface{}{
		"action":     "approve_request",
		"request_id": float64(sr.ID),
	})
	assert.Error(t, err, "跨租户的关联工单写入必须失败")

	after, err := client.Ticket.Get(context.Background(), tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "open", after.Status, "跨租户请求不得改写关联工单状态")
}
