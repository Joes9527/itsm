package service_request

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/servicerequestaccessresult"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"testing"
	"time"
)

func TestAccessResultFulfillmentUsesProfessionalAndWorkflowOwners(t *testing.T) {
	for _, state := range []string{"unknown", "awaiting_approval", "fulfilling", "rejected", "cancelled", "completed", "already_present", "transport_completed", "suspended", "failed_delegation", "multiple_delegations_failed"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			c := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
			defer c.Close()
			tenant := c.Tenant.Create().SetName("Native").SetCode("native").SaveX(ctx)
			u := c.User.Create().SetTenantID(tenant.ID).SetName("Requester").SetUsername("requester").SetEmail("requester@example.test").SetPasswordHash("unused").SetRole("super_admin").SaveX(ctx)
			item := c.Ticket.Create().SetTenantID(tenant.ID).SetTicketNumber("SR1").SetTitle("Access").SetDescription("Access").SetRequesterID(u.ID).SetRecordClass("service_request_item").SaveX(ctx)
			cat := c.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("Access").SetTargetClass("service_request_item").SaveX(ctx)
			c.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(cat.ID).SaveX(ctx)
			dep := c.ProcessDeployment.Create().SetTenantID(tenant.ID).SetDeploymentID("dep").SetDeploymentName("D").SaveX(ctx)
			def := c.ProcessDefinition.Create().SetTenantID(tenant.ID).SetDeploymentID(dep.ID).SetKey("access").SetName("Access").SetBpmnXML([]byte(`<definitions><process id="access"><userTask id="approval" taskPurpose="approval"/></process></definitions>`)).SaveX(ctx)
			inst := c.ProcessInstance.Create().SetTenantID(tenant.ID).SetProcessDefinitionID(def.ID).SetProcessDefinitionKey("access").SetProcessInstanceID("inst").SetBusinessID(item.ID).SetBusinessType("service_request").SaveX(ctx)
			task := c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("approval").SetTaskName("Approval").SetTaskID("task").SetTaskVariables(map[string]any{"taskPurpose": "approval"}).SaveX(ctx)
			expected := state
			switch state {
			case "unknown":
				c.ProcessTask.DeleteOne(task).ExecX(ctx)
			case "awaiting_approval":
			case "fulfilling":
				c.ProcessTask.UpdateOne(task).SetStatus("delegated").SetTaskType("kaf_delegate").SetCallbackTaskType("kaf_delegate").SaveX(ctx)
			case "failed_delegation", "multiple_delegations_failed":
				c.ProcessTask.UpdateOne(task).SetStatus("delegated").SetTaskType("kaf_delegate").SaveX(ctx)
				failedTask := task
				if state == "multiple_delegations_failed" {
					failedTask = c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("second").SetTaskName("Second").SetTaskID("second").SetStatus("delegated").SetTaskType("kaf_delegate").SaveX(ctx)
				}
				c.KafTaskActionLedger.Create().SetTenantID(tenant.ID).SetTaskID(failedTask.TaskID).SetRunID("run").SetStepID("step").SetAction("complete_bpmn_task").SetIdempotencyKey("idempotency").SetCorrelationID("correlation").SetProcedureRef("procedure").SetProcedureVersion("1").SetResultStatus("failed_retryable").SaveX(ctx)
				expected = "unknown"
			case "rejected":
				c.ProcessApprovalDecision.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessTaskID(task.ID).SetProcessInstanceKey("inst").SetTaskID("task").SetProcessDefinitionKey("access").SetNodeKey("approval").SetActorID(u.ID).SetAction("reject").SetDecision("rejected").SaveX(ctx)
			case "cancelled":
				item = c.Ticket.UpdateOne(item).SetStatus("cancelled").SaveX(ctx)
			case "completed", "already_present":
				outcome, baseline := "granted", "not_member"
				var expiry *time.Time
				verified := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
				if state == "already_present" {
					outcome, baseline = "already_present", "member"
				} else {
					v := verified.Add(24 * time.Hour)
					expiry = &v
				}
				c.ServiceRequestAccessResult.Create().SetWorkItemID(item.ID).SetProcessTaskID(task.ID).SetOutcome(servicerequestaccessresult.Outcome(outcome)).SetProvider("graph").SetSubjectID("subject").SetGroupID("group").SetBaseline(servicerequestaccessresult.Baseline(baseline)).SetVerifiedAt(verified).SetNillableExpiresAt(expiry).SetEvidenceRef("evidence").SaveX(ctx)
				_, duplicateErr := c.ServiceRequestAccessResult.Create().SetWorkItemID(item.ID).SetProcessTaskID(task.ID).SetOutcome(servicerequestaccessresult.Outcome(outcome)).SetProvider("graph").SetSubjectID("subject").SetGroupID("group").SetBaseline(servicerequestaccessresult.Baseline(baseline)).SetVerifiedAt(verified).SetNillableExpiresAt(expiry).SetEvidenceRef("replay").Save(ctx)
				require.Error(t, duplicateErr, "one professional result per WorkItem")
				expected = "completed"
			case "transport_completed":
				c.ProcessTask.UpdateOne(task).SetStatus("completed").SaveX(ctx)
				c.ProcessInstance.UpdateOne(inst).SetStatus("completed").SaveX(ctx)
				expected = "unknown"
			case "suspended":
				c.ProcessInstance.UpdateOne(inst).SetStatus("suspended").SaveX(ctx)
				expected = "unknown"
			}
			owner := NewService(NewEntRepository(c), c, zap.NewNop().Sugar(), nil)
			got, err := owner.ReadFulfillment(ctx, c, item)
			require.NoError(t, err)
			require.Equal(t, expected, got.State)
			reader := intake.NewReadService(authorization.NewSessionReader(c, accessTestDirectory{}), nil, "fixture-only-cursor")
			reader.SetFulfillmentReader(owner)
			view, err := reader.WorkItem(tenantctx.WithTenantID(ctx, tenant.ID), creation.Identity{TenantID: tenant.ID, ActorID: u.ID, Role: u.Role}, item.ID)
			require.NoError(t, err)
			require.Equal(t, expected, view.FulfillmentState)
			require.Equal(t, got.AccessResult, view.AccessResult)

			if expected == "completed" {
				require.NotNil(t, got.AccessResult)
				if state == "already_present" {
					require.Nil(t, got.AccessResult.ExpiresAt)
					require.False(t, got.AccessResult.Managed)
				} else {
					require.NotNil(t, got.AccessResult.ExpiresAt)
					require.True(t, got.AccessResult.Managed)
				}
			}
		})
	}
}

type accessTestDirectory struct{}

func (accessTestDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return nil }, nil
}
