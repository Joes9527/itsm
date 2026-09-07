package service_request

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/common/accessgrant"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"testing"
)

func TestAccessSnapshotTrustedRequesterAndFrozenTerms(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	tenant := c.Tenant.Create().SetName("Native").SetCode("native").SaveX(ctx)
	user := c.User.Create().SetTenantID(tenant.ID).SetName("Requester").SetUsername("requester").SetEmail("requester@example.test").SetPasswordHash("unused").SaveX(ctx)
	mapping := c.ExternalIdentity.Create().SetTenantID(tenant.ID).SetUserID(user.ID).SetProvider("graph").SetWorkspace("directory").SetSubject("trusted-subject").SaveX(ctx)
	catalog := c.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("Access").SetTargetClass("service_request_item").SaveX(ctx)
	policy := c.CatalogAccessPolicy.Create().SetCatalogID(catalog.ID).SetProvider("graph").SetExternalSystem("directory").SetGroupID("approved-group").SetDurationField("duration").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 2592000}}).SaveX(ctx)
	fields := []creation.ResolvedFieldDefinition{{Key: "duration", DataType: "select", Required: true, Options: []any{map[string]any{"value": "month", "label": "一个月"}}}}
	projected, err := service.ProjectCatalogOptions(fields[0].Options)
	require.NoError(t, err)
	values, err := service.ResolveCatalogOptionKeys(fields, map[string]any{"duration": projected[0].Key, "applicant_upn": "attacker"})
	require.NoError(t, err)
	p, err := service_catalog.ReadAccessPolicy(ctx, c, tenant.ID, catalog.ID)
	require.NoError(t, err)
	in := creation.ResolvedIntake{Identity: creation.Identity{TenantID: tenant.ID, ActorID: user.ID, RequesterID: user.ID}, Catalog: &creation.ResolvedCatalog{ID: catalog.ID, AccessPolicy: p}, FieldDefinitions: fields, Command: creation.CreateWorkItemCommand{FormValues: values}}
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	snapshot, err := prepareAccessSnapshot(ctx, tx, in)
	require.NoError(t, err)
	require.Equal(t, "trusted-subject", snapshot.SubjectID)
	require.Equal(t, "month", snapshot.DurationKey)
	require.Equal(t, int64(2592000), snapshot.DurationSeconds)
	item := tx.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(user.ID).SetTitle("Access").SetDescription("Access").SetTicketNumber("SR1").SetRecordClass("service_request_item").SaveX(ctx)
	tx.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(catalog.ID).SaveX(ctx)
	require.NoError(t, saveAccessSnapshot(ctx, tx, item.ID, snapshot))
	require.NoError(t, tx.Commit())
	c.CatalogAccessPolicy.UpdateOne(policy).SetGroupID("changed-group").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 3600}}).AddVersion(1).SaveX(ctx)
	owner := NewService(NewEntRepository(c), c, zap.NewNop().Sugar(), nil)
	frozen, err := owner.ReadAccessSnapshot(ctx, c, tenant.ID, item.ID)
	require.NoError(t, err)
	require.Equal(t, snapshot, frozen)
	foreign, err := owner.ReadAccessSnapshot(ctx, c, tenant.ID+1, item.ID)
	require.NoError(t, err)
	require.Nil(t, foreign)
	dep := c.ProcessDeployment.Create().SetTenantID(tenant.ID).SetDeploymentID("approved-dep").SetDeploymentName("Access").SaveX(ctx)
	def := c.ProcessDefinition.Create().SetTenantID(tenant.ID).SetDeploymentID(dep.ID).SetKey("access").SetName("Access").SetBpmnXML([]byte(`<definitions/>`)).SaveX(ctx)
	inst := c.ProcessInstance.Create().SetTenantID(tenant.ID).SetProcessDefinitionID(def.ID).SetProcessDefinitionKey("access").SetProcessInstanceID("approved-inst").SetBusinessType("service_request").SetBusinessID(item.ID).SaveX(ctx)
	task := c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("grant").SetTaskName("Grant").SetTaskID("approved-task").SetTaskType("kaf_delegate").SetStatus("delegated").SetCallbackAction(accessgrant.Capability).SetCallbackConfigRef(fmt.Sprint(policy.ID)).SaveX(ctx)
	kaf := c.User.Create().SetTenantID(tenant.ID).SetName("KAF").SetUsername("kaf").SetEmail("kaf@example.test").SetPasswordHash("unused").SetRole("kaf_automation").SaveX(ctx)
	delegate := service.NewKafDelegationService(c)
	delegate.SetApprovedAccessReader(owner)
	kafctx := context.WithValue(context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID), bpmn.BPMNUserIDContextKey, kaf.ID)
	_, err = delegate.GetTaskContext(kafctx, task.TaskID)
	require.Error(t, err, "submission confirmation is not approval")
	approvalTask := c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("approval").SetTaskName("Approval").SetTaskID("business-approval").SetStatus("completed").SetTaskVariables(map[string]any{"taskPurpose": "approval"}).SaveX(ctx)
	c.ProcessApprovalDecision.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessTaskID(approvalTask.ID).SetProcessInstanceKey(inst.ProcessInstanceID).SetTaskID(approvalTask.TaskID).SetProcessDefinitionKey("access").SetNodeKey("approval").SetActorID(user.ID).SetAction("approve").SetDecision("approved").SaveX(ctx)
	approved, err := delegate.GetTaskContext(kafctx, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, *snapshot, approved.ApprovedAccess.ApprovalSnapshot)
	require.Len(t, approved.ApprovedAccess.Approvals, 1)
	c.ExternalIdentity.UpdateOne(mapping).SetActive(false).SaveX(ctx)
	_, err = delegate.GetTaskContext(kafctx, task.TaskID)
	require.Error(t, err, "execution rechecks trusted mapping")

	// The actual domain reader blocks one task without starving same-tenant work.
	healthy := c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("ordinary").SetTaskName("Ordinary").SetTaskID("healthy-task").SetTaskType("kaf_delegate").SetStatus("delegated").SaveX(ctx)
	later := c.ProcessTask.Create().SetTenantID(tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("later").SetTaskName("Later").SetTaskID("later-blocked-task").SetTaskType("kaf_delegate").SetStatus("delegated").SetCallbackAction(accessgrant.Capability).SetCallbackConfigRef(fmt.Sprint(policy.ID)).SaveX(ctx)
	cursor := ""
	for index, expected := range []string{task.TaskID, healthy.TaskID, later.TaskID} {
		page, err := delegate.ListDelegatedTaskPage(kafctx, 1, cursor)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, expected, page.Items[0].TaskID)
		if index != 1 {
			require.Equal(t, "domain_blocked", page.Items[0].Status)
			require.Equal(t, "requester_identity_inactive", page.Items[0].BlockReason)
			require.Empty(t, page.Items[0].AllowedActions)
			require.Nil(t, page.Items[0].ApprovedAccess)
		} else {
			require.Equal(t, "delegated", page.Items[0].Status)
		}
		cursor = page.NextCursor
	}
	require.Empty(t, cursor)
	_, err = delegate.ListDelegatedTaskPage(ctx, 1, "")
	require.Error(t, err, "missing tenant remains fatal")
	unauthorized := context.WithValue(kafctx, bpmn.BPMNUserIDContextKey, user.ID)
	_, err = delegate.ListDelegatedTaskPage(unauthorized, 1, "")
	require.Error(t, err, "nontechnical actor remains forbidden")

	// Other domain-owner decisions are equally visible and remain non-executable.
	c.ExternalIdentity.UpdateOne(mapping).SetActive(true).SaveX(ctx)
	c.ProcessTask.UpdateOne(later).SetStatus("completed").SaveX(ctx)
	c.ProcessInstance.UpdateOne(inst).SetStatus("suspended").SaveX(ctx)
	page, err := delegate.ListDelegatedTaskPage(kafctx, 10, "")
	require.NoError(t, err)
	require.Equal(t, "domain_blocked", page.Items[0].Status)
	require.Equal(t, "approval_not_executable", page.Items[0].BlockReason)
	require.Equal(t, "delegated", page.Items[1].Status)
	_, err = delegate.GetTaskContext(kafctx, task.TaskID)
	require.Error(t, err)
	c.ProcessInstance.UpdateOne(inst).SetStatus("running").SaveX(ctx)
	c.KafTaskActionLedger.Create().SetTenantID(tenant.ID).SetTaskID(task.TaskID).SetRunID("run").SetStepID("step").SetAction("complete_bpmn_task").SetIdempotencyKey("key").SetCorrelationID("corr").SetProcedureRef("proc").SetProcedureVersion("1").SetResultStatus("failed_terminal").SaveX(ctx)
	page, err = delegate.ListDelegatedTaskPage(kafctx, 10, "")
	require.NoError(t, err)
	require.Equal(t, "domain_blocked", page.Items[0].Status)
	require.Equal(t, "delegated", page.Items[1].Status)
	c.ExternalIdentity.UpdateOne(mapping).SetActive(false).SaveX(ctx)
	tx, err = c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = prepareAccessSnapshot(ctx, tx, in)
	require.Error(t, err, "inactive mapping fails closed")
	require.NoError(t, tx.Rollback())
	raw, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec("DROP TABLE external_identities")
	require.NoError(t, err)
	_, err = delegate.ListDelegatedTaskPage(kafctx, 10, "")
	require.Error(t, err, "domain-reader database failure must abort the page")
	var blocked *accessgrant.BlockedError
	require.NotErrorAs(t, err, &blocked)

}
