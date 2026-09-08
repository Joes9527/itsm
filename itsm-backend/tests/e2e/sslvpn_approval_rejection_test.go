package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	creation "itsm-backend/handlers/common/workitemcreation"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/service"
)

// SQLite compares timestamp text; use one UTC clock representation for the
// real dispatcher and ORM defaults without editing any persisted event/state.
func sslvpnApprovalUTC(t *testing.T) {
	t.Helper()
	original := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = original })
}

// setupSSLVPNTestHarness deploys service/bpmn/sslvpn_approval_flow.bpmn
// through BPMNTemplateService and configures distinct real candidate-group actors.
func startSSLVPNApprovalRequest(t *testing.T, h *sslvpnTestHarness) (creation.CreateWorkItemResult, *ent.ProcessInstance) {
	t.Helper()
	ctx := context.Background()
	catalog, code := doRequest(t, h.router, h.userSession, http.MethodGet, fmt.Sprintf("/api/v1/service-catalogs/%d", h.fixture.CatalogItem.ID), nil)
	require.Equal(t, http.StatusOK, code)
	var revision dto.ServiceCatalogResponse
	require.NoError(t, json.Unmarshal(catalog.Data, &revision))
	result, code := doRequest(t, h.router, h.userSession, http.MethodPost, "/api/v1/service-requests", map[string]interface{}{
		"catalogId": h.fixture.CatalogItem.ID, "recordClass": "service_request_item", "catalogVersion": revision.CatalogVersion, "formSchemaVersion": revision.FormSchemaVersion,
		"title": "Synthetic access approval regression", "reason": "Isolated approval boundary verification",
		"formData": map[string]interface{}{"customFieldValues": []map[string]interface{}{
			{"name": "target_systems", "value": "synthetic-system"}, {"name": "access_duration", "value": "days_30"}, {"name": "access_reason", "value": "Approval boundary verification"},
		}},
	})
	require.Equal(t, http.StatusCreated, code, result.Message)
	var created creation.CreateWorkItemResult
	require.NoError(t, json.Unmarshal(result.Data, &created))
	event := h.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested"), outboxevent.AggregateIDEQ(fmt.Sprint(created.WorkItemID))).OnlyX(ctx)
	require.NoError(t, service.NewWorkflowStartOutboxHandler(h.client, h.engine.(*service.CustomProcessEngine), h.client).Deliver(ctx, event))
	instance := h.client.ProcessInstance.Query().Where(processinstance.TenantIDEQ(h.tenant.ID), processinstance.BusinessTypeEQ("service_request"), processinstance.BusinessIDEQ(created.WorkItemID)).OnlyX(ctx)
	require.NotEqual(t, h.fixture.Users.Supervisor.ID, h.fixture.Users.Lixin.ID)
	require.Equal(t, "dept_manager", h.fixture.Users.Supervisor.Role)
	require.Equal(t, "network_eng", h.fixture.Users.Lixin.Role)
	return created, instance
}

func sslvpnApprovalTask(t *testing.T, h *sslvpnTestHarness, instance *ent.ProcessInstance, node string) *ent.ProcessTask {
	t.Helper()
	return h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ(node)).OnlyX(context.Background())
}
func submitSSLVPNDecision(t *testing.T, h *sslvpnTestHarness, session, taskID, action string) (apiEnvelope, int) {
	t.Helper()
	return doRequest(t, h.router, session, http.MethodPost, "/api/v1/bpmn/tasks/"+taskID+"/decisions", map[string]interface{}{"action": action, "comment": "Synthetic regression decision"})
}
func assertSSLVPNDispatchCount(t *testing.T, h *sslvpnTestHarness, expected int32) {
	t.Helper()
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusAccepted) }))
	defer receiver.Close()
	dispatcher, err := service.NewKafOutboxDispatcher(service.NewOutboxEventRepository(h.client), service.KafOutboxConfig{WebhookURL: receiver.URL, WebhookSecret: "isolated-rejection-test", BatchSize: 10, PollInterval: time.Second})
	require.NoError(t, err)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	assert.Equal(t, expected, calls.Load(), "mock KAF HTTP dispatches; no real KAF or provider is connected")
}
func assertSSLVPNNoDelegation(t *testing.T, h *sslvpnTestHarness, instance *ent.ProcessInstance) {
	t.Helper()
	ctx := context.Background()
	assert.Zero(t, h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskTypeEQ("kaf_delegate")).CountX(ctx))
	assert.Zero(t, h.client.OutboxEvent.Query().Where(outboxevent.TenantIDEQ(h.tenant.ID), outboxevent.EventTypeEQ("kaf_delegate_requested")).CountX(ctx))
	assert.Zero(t, h.client.ServiceRequestAccessResult.Query().CountX(ctx))
	assert.Zero(t, h.client.KafTaskActionLedger.Query().CountX(ctx))
	assertSSLVPNDispatchCount(t, h, 0)
}

func TestSSLVPNApprovalRejectionNeverDelegates(t *testing.T) {
	for _, level := range []string{"manager", "security"} {
		t.Run(level, func(t *testing.T) {
			sslvpnApprovalUTC(t)
			h := setupSSLVPNTestHarness(t)
			defer h.client.Close()
			defer h.rawDB.Close()
			created, instance := startSSLVPNApprovalRequest(t, h)
			manager := sslvpnApprovalTask(t, h, instance, "UserTask_DeptManagerApproval")
			rejected := manager
			actorSession := h.superSession
			actorID := h.fixture.Users.Supervisor.ID
			if level == "security" {
				result, code := submitSSLVPNDecision(t, h, h.superSession, manager.TaskID, "approve")
				require.Equal(t, http.StatusOK, code, result.Message)
				assertSSLVPNNoDelegation(t, h, instance)
				rejected = sslvpnApprovalTask(t, h, instance, "UserTask_L2NetworkOpsApproval")
				actorSession = h.lixinSession
				actorID = h.fixture.Users.Lixin.ID
			}
			result, code := submitSSLVPNDecision(t, h, actorSession, rejected.TaskID, "reject")
			require.Equal(t, http.StatusOK, code, result.Message)
			ctx := context.Background()
			decision := h.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessTaskIDEQ(rejected.ID)).OnlyX(ctx)
			require.Equal(t, "rejected", decision.Decision)
			require.Equal(t, actorID, decision.ActorID)
			assertSSLVPNNoDelegation(t, h, instance)
			finished := h.client.ProcessInstance.GetX(ctx, instance.ID)
			assert.Equal(t, "completed", finished.Status, "rejection terminates this process")
			assert.Equal(t, "EndEvent_Reject", finished.CurrentActivityID)
			if level == "manager" {
				assert.Zero(t, h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ("UserTask_L2NetworkOpsApproval")).CountX(ctx))
			}
			owner := sr.NewService(sr.NewEntRepository(h.client), h.client, zap.NewNop().Sugar(), nil)
			item := h.client.Ticket.GetX(ctx, created.WorkItemID)
			fulfillment, err := owner.ReadFulfillment(ctx, h.client, item)
			require.NoError(t, err)
			assert.Equal(t, "rejected", fulfillment.State)
			assert.Nil(t, fulfillment.AccessResult)
			assert.NotEqual(t, "resolved", item.Status)
			assert.True(t, h.client.ServiceRequest.Query().OnlyX(ctx).CompletedAt.IsZero(), "rejected request is not fulfilled")
			// A new approval submitted to the rejected/completed task cannot reopen it.
			before := h.client.ProcessApprovalDecision.Query().CountX(ctx)
			_, code = submitSSLVPNDecision(t, h, actorSession, rejected.TaskID, "approve")
			assert.NotEqual(t, http.StatusOK, code)
			assert.Equal(t, before, h.client.ProcessApprovalDecision.Query().CountX(ctx))
			assertSSLVPNNoDelegation(t, h, instance)
		})
	}
}

func TestSSLVPNApprovalOrderAndActorCannotBeBypassed(t *testing.T) {
	sslvpnApprovalUTC(t)
	h := setupSSLVPNTestHarness(t)
	defer h.client.Close()
	defer h.rawDB.Close()
	_, instance := startSSLVPNApprovalRequest(t, h)
	ctx := context.Background()
	manager := sslvpnApprovalTask(t, h, instance, "UserTask_DeptManagerApproval")
	assert.Zero(t, h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ("UserTask_L2NetworkOpsApproval")).CountX(ctx))
	// A definition node is not a task identity; the second level does not yet exist.
	_, code := submitSSLVPNDecision(t, h, h.lixinSession, "UserTask_L2NetworkOpsApproval", "approve")
	assert.NotEqual(t, http.StatusOK, code)
	_, code = submitSSLVPNDecision(t, h, h.lixinSession, manager.TaskID, "approve")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Zero(t, h.client.ProcessApprovalDecision.Query().CountX(ctx))
	assertSSLVPNNoDelegation(t, h, instance)
	result, code := submitSSLVPNDecision(t, h, h.superSession, manager.TaskID, "approve")
	require.Equal(t, http.StatusOK, code, result.Message)
	security := sslvpnApprovalTask(t, h, instance, "UserTask_L2NetworkOpsApproval")
	version := h.client.ProcessInstance.GetX(ctx, instance.ID).Version
	_, code = submitSSLVPNDecision(t, h, h.superSession, manager.TaskID, "approve")
	assert.NotEqual(t, http.StatusOK, code)
	assert.Equal(t, version, h.client.ProcessInstance.GetX(ctx, instance.ID).Version)
	assert.Equal(t, 1, h.client.ProcessApprovalDecision.Query().CountX(ctx))
	_, code = submitSSLVPNDecision(t, h, h.superSession, security.TaskID, "approve")
	assert.Equal(t, http.StatusForbidden, code)
	assertSSLVPNNoDelegation(t, h, instance)
	result, code = submitSSLVPNDecision(t, h, h.lixinSession, security.TaskID, "approve")
	require.Equal(t, http.StatusOK, code, result.Message)
	version = h.client.ProcessInstance.GetX(ctx, instance.ID).Version
	_, code = submitSSLVPNDecision(t, h, h.lixinSession, security.TaskID, "approve")
	assert.NotEqual(t, http.StatusOK, code)
	assert.Equal(t, version, h.client.ProcessInstance.GetX(ctx, instance.ID).Version)
	decisions := h.client.ProcessApprovalDecision.Query().Order(ent.Asc(processapprovaldecision.FieldID)).AllX(ctx)
	require.Len(t, decisions, 2)
	assert.Equal(t, h.fixture.Users.Supervisor.ID, decisions[0].ActorID)
	assert.Equal(t, h.fixture.Users.Lixin.ID, decisions[1].ActorID)
	assert.Equal(t, "UserTask_DeptManagerApproval", decisions[0].NodeKey)
	assert.Equal(t, "UserTask_L2NetworkOpsApproval", decisions[1].NodeKey)
	assert.Equal(t, 1, h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskTypeEQ("kaf_delegate")).CountX(ctx))
	assert.Equal(t, 1, h.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("kaf_delegate_requested")).CountX(ctx))
	assertSSLVPNDispatchCount(t, h, 1)
}
