package service

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestWorkItemRelationRejectsReverseExistingTuple(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "relation")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "relation")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	a := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "relation-a")
	b := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "relation-b")
	client.WorkItemRelation.Create().SetTenantID(tenant.ID).SetSourceWorkItemID(b.WorkItemID).SetTargetWorkItemID(a.WorkItemID).SetRelationType("related_to").SetCreatedByID(actor.ID).SaveX(ctx)
	before := client.Ticket.GetX(ctx, a.WorkItemID)
	owner := NewWorkItemRelationService(client, nil)
	_, err = owner.Apply(ctx, RelationCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "duplicate"}, SourceID: a.WorkItemID, TargetID: b.WorkItemID, Type: "related_to"}, false)
	require.Error(t, err, "undirected duplicate must reject regardless of stored orientation")
	require.Equal(t, before.Version, client.Ticket.GetX(ctx, a.WorkItemID).Version)
	require.Equal(t, 1, client.WorkItemRelation.Query().CountX(ctx))
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	rows, err := owner.ListTx(ctx, tx, workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID}, a.WorkItemID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, b.WorkItemID, rows[0].Source.WorkItemID)
	require.Equal(t, before.TicketNumber, rows[0].Target.Number)
}

func TestInvestigatedByDirection(t *testing.T) {
	if err := ValidateRelationClasses("investigated_by", "incident", "problem"); err != nil {
		t.Fatal(err)
	}
	if ValidateRelationClasses("investigated_by", "problem", "incident") == nil {
		t.Fatal("reversed relation accepted")
	}
	if ValidateRelationClasses("unknown", "incident", "problem") == nil {
		t.Fatal("unknown relation accepted")
	}
	if ValidateRelationClasses("related_to", "unknown", "problem") == nil {
		t.Fatal("unknown source class accepted")
	}
	if ValidateRelationClasses("related_to", "problem", "unknown") == nil {
		t.Fatal("unknown target class accepted")
	}
}

func TestWorkItemRelationClassMatrix(t *testing.T) {
	for _, tc := range []struct {
		kind, source, target string
		allowed              bool
	}{
		{"caused_by", "incident", "problem", true}, {"caused_by", "problem", "incident", false},
		{"resolved_by_change", "incident", "change_request", true}, {"resolved_by_change", "problem", "change_request", true}, {"resolved_by_change", "change_request", "problem", false},
		{"requested_change", "service_request_item", "change_request", true}, {"requested_change", "generic", "change_request", false},
		{"fulfilled_by", "service_request_item", "catalog_task", true}, {"fulfilled_by", "catalog_task", "service_request_item", false},
		{"duplicate_of", "problem", "problem", true}, {"duplicate_of", "problem", "incident", false},
		{"parent_child", "catalog_task", "generic", true}, {"parent_child", "unknown", "generic", false},
		{"related_to", "generic", "catalog_task", true}, {"related_to", "catalog_task", "generic", true},
		{"unknown", "generic", "generic", false}, {"duplicate_of", "unknown", "unknown", false},
	} {
		t.Run(tc.kind+"/"+tc.source+"/"+tc.target, func(t *testing.T) {
			require.Equal(t, tc.allowed, ValidateRelationClasses(tc.kind, tc.source, tc.target) == nil)
		})
	}
}
