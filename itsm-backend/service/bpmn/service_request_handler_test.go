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

	handler := NewServiceRequestServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, tkt, sr
}

func TestServiceRequestHandler_AssignRequest_SetsProcessor(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_request",
		"request_id":  float64(sr.ID),
		"assignee_id": float64(42),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.Equal(t, 42, updated.ProcessorID)
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
	assert.True(t, result.Success)

	updatedSR, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.False(t, updatedSR.CompletedAt.IsZero())
	assert.Equal(t, "已开通", updatedSR.CompletionNote)

	updatedTicket, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", updatedTicket.Status)
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
	_, err := handler.Execute(ctx, nil, provision)
	require.NoError(t, err)
	firstStarted := client.ServiceRequest.GetX(ctx, sr.ID).StartedAt
	_, err = handler.Execute(ctx, nil, provision)
	require.NoError(t, err)
	assert.Equal(t, firstStarted, client.ServiceRequest.GetX(ctx, sr.ID).StartedAt)

	complete := map[string]interface{}{
		"action": "complete_request", "request_id": sr.ID, "completion_note": "ready",
	}
	_, err = handler.Execute(ctx, nil, complete)
	require.NoError(t, err)
	firstRequest := client.ServiceRequest.GetX(ctx, sr.ID)
	firstTicket := client.Ticket.GetX(ctx, tkt.ID)
	_, err = handler.Execute(ctx, nil, complete)
	require.NoError(t, err)
	afterRequest := client.ServiceRequest.GetX(ctx, sr.ID)
	afterTicket := client.Ticket.GetX(ctx, tkt.ID)
	assert.Equal(t, firstRequest.CompletedAt, afterRequest.CompletedAt)
	assert.Equal(t, firstTicket.ResolvedAt, afterTicket.ResolvedAt)
}

func TestServiceRequestHandler_CreateRequest_ReturnsExplicitUnsupportedError(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "create_request",
		"title":  "新请求",
	})
	require.Error(t, err, "服务请求在流程启动前就已经存在（先创建 ServiceRequest 才会触发 BPMN），"+
		"从流程内部再\"创建\"一个于架构不符——这里应该是明确报错而不是假装成功")
}

func TestServiceRequestHandler_InvalidRequestID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "assign_request"})
	assert.Error(t, err)
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
	assert.True(t, result.Success)

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

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":     "reject_request",
		"request_id": float64(sr.ID),
	})
	require.Error(t, err, "resolved 工单不允许再被驳回")
	assert.Contains(t, err.Error(), "非法的关联工单状态转换")

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
	assert.True(t, result.Success)

	after, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", after.Status, "幂等完成不得改变状态")
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
