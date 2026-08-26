package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
)

func TestApprovalDecisionHistoryTenantIsolationAndUniqueness(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:approval_decisions?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	// ListApprovalDecisions now resolves the owning ProcessInstance first (to run
	// authorizeProcessInstanceViewer against it), so this fixture needs a real
	// process_instance row for tenant 1 keyed "PI-1" — not just the
	// ProcessApprovalDecision rows the tenant-isolation/uniqueness assertions
	// actually exercise.
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-approval-history").
		SetDeploymentName("approval-history").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := client.ProcessDefinition.Create().
		SetKey("change").
		SetName("Change").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-1").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetTenantID(1).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	create := func(tenantID, taskID int, key string) error {
		_, err := client.ProcessApprovalDecision.Create().
			SetProcessInstanceID(10).SetProcessTaskID(taskID).SetProcessInstanceKey(key).
			SetTaskID("TASK").SetProcessDefinitionKey("change").SetNodeKey("manager").
			SetActorID(1).SetAction("approve").SetDecision("approved").SetTenantID(tenantID).Save(ctx)
		return err
	}
	if err := create(1, 100, "PI-1"); err != nil {
		t.Fatal(err)
	}
	if err := create(2, 200, "PI-1"); err != nil {
		t.Fatal(err)
	}
	if err := create(1, 100, "PI-1"); err == nil {
		t.Fatal("expected duplicate task decision to fail")
	}

	svc := &bpmnTaskService{client: client}
	// Elevated: this test asserts tenant isolation/uniqueness of decision rows,
	// not participant-level viewer authorization, so it bypasses that check the
	// same way an ops-console caller with process_instance:read would.
	tenantOne := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, 1)
	tenantOne = context.WithValue(tenantOne, bpmn.BPMNElevatedContextKey, true)
	history, err := svc.ListApprovalDecisions(tenantOne, "PI-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].TenantID != 1 {
		t.Fatalf("unexpected tenant history: %#v", history)
	}
}
