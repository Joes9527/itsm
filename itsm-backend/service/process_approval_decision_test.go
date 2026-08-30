package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
)

func TestApprovalDecisionHistoryTenantIsolationAndUniqueness(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:approval_decisions?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
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
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("approval-decisions").
		SetDeploymentName("Approval Decisions").
		SetTenantID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := client.ProcessDefinition.Create().
		SetKey("approval-decisions").
		SetName("Approval Decisions").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ProcessInstance.Create().
		SetProcessInstanceID("PI-1").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetTenantID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	svc := &bpmnTaskService{client: client}
	tenantOne := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, 1)
	tenantOne = WithBPMNAccessScope(tenantOne, BPMNAccessScope{UserID: 1, TenantID: 1, CanReadAllInstances: true})
	history, err := svc.ListApprovalDecisions(tenantOne, "PI-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].TenantID != 1 {
		t.Fatalf("unexpected tenant history: %#v", history)
	}
}
