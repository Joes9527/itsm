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
