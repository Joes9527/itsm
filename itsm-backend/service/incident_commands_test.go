package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestIncidentRecoveryEvidence(t *testing.T) {
	if ValidateIncidentRecovery(" ") == nil {
		t.Fatal("empty recovery accepted")
	}
	if err := ValidateIncidentRecovery("workaround restored WMS; opening verified"); err != nil {
		t.Fatal(err)
	}
}

func TestIncidentCommandRejectsSameState(t *testing.T) {
	for action, status := range map[string]string{"acknowledge": "acknowledged", "start": "in_progress", "resolve": "resolved", "close": "closed"} {
		t.Run(action, func(t *testing.T) {
			client, svc, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "same")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "same")
			require.NoError(t, err)
			actor.Update().SetRole("super_admin").ExecX(ctx)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "same")
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus(status).SaveX(ctx)
			if action == "start" {
				inc.Edges.WorkItem = before
				require.False(t, CanStartIncident(ActionActor{}, inc).Allowed)
			}
			_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "fresh"}, IncidentID: inc.ID, Action: action, Reason: "confirmed", Resolution: "restored"})
			require.Error(t, err)
			require.Equal(t, before.Version, client.Ticket.GetX(ctx, before.ID).Version)
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
		})
	}
}

func TestIncidentRuleStatusActionUsesCommand(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "rule-command")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "rule-command")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "rule-command")
	inc.Edges.WorkItem = client.Ticket.GetX(ctx, inc.WorkItemID)
	ctx = WithIncidentAlertActor(ctx, actor.ID, "incident_rule", "rule-action")
	action := &StatusChangeAction{Status: "in_progress", client: client, logger: svc.logger}
	require.NoError(t, action.Execute(ctx, inc, tenant.ID))
	require.Equal(t, "in_progress", client.Ticket.GetX(ctx, inc.WorkItemID).Status)
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
	require.NoError(t, action.Execute(ctx, inc, tenant.ID))
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
}

func TestIncidentCommandAcknowledgedStartResolve(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "start")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "start")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "start")
	version := client.Ticket.GetX(ctx, inc.WorkItemID).Version
	for _, action := range []string{"acknowledge", "start", "resolve"} {
		result, err := svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: version, Source: "http", OperationID: action}, IncidentID: inc.ID, Action: action, Resolution: "service restored"})
		require.NoError(t, err)
		require.Equal(t, version+1, result.Version)
		version = result.Version
	}
	require.Equal(t, "resolved", client.Ticket.GetX(ctx, inc.WorkItemID).Status)
}

func TestIncidentGenericEditRequiresVersionAndRejectsStatus(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "command-edit")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "command-edit")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "command-edit")
	item := client.Ticket.GetX(ctx, inc.WorkItemID)
	title := "changed"
	status := "in_progress"
	_, err = svc.UpdateIncident(ctx, inc.ID, &dto.UpdateIncidentRequest{Title: &title}, tenant.ID)
	require.Error(t, err, "missing expected version must fail")
	_, err = svc.UpdateIncident(ctx, inc.ID, &dto.UpdateIncidentRequest{Status: &status, Version: item.Version}, tenant.ID)
	require.Error(t, err, "generic status mutation must fail")
	_, err = svc.UpdateIncident(ctx, inc.ID, &dto.UpdateIncidentRequest{Title: &title, Version: item.Version, Force: true}, tenant.ID)
	require.Error(t, err, "force cannot bypass version contract")
	require.Equal(t, item.Version, client.Ticket.GetX(ctx, item.ID).Version)
}

func TestIncidentCommandAtomicReplayAndRecovery(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "command")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "command")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "command")
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	cmd := dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "resolve-one"}, IncidentID: inc.ID, Action: "resolve", Resolution: "workaround restored service"}
	result, err := svc.ApplyIncidentCommand(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, item.Version+1, result.Version)
	require.Equal(t, "resolved", result.Status)
	replay, err := svc.ApplyIncidentCommand(ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.Version, replay.Version)
	cmd.Resolution = "different evidence"
	_, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.Error(t, err)
	cmd.Meta.OperationID = "stale"
	_, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.Error(t, err)
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
	require.Equal(t, 1, client.IncidentEvent.Query().CountX(ctx))
	cmd.Action = "reopen"
	cmd.Meta.OperationID = "reopen-one"
	cmd.Meta.ExpectedVersion = result.Version
	result, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "in_progress", result.Status)
	require.True(t, client.Ticket.GetX(ctx, item.ID).ResolvedAt.IsZero())
}

func TestIncidentCommandAuditFailureRollsBack(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "command-rollback")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "command-rollback")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "rollback")
	item := client.Ticket.GetX(ctx, inc.WorkItemID)
	client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("audit unavailable") })
	})
	_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "ack"}, IncidentID: inc.ID, Action: "acknowledge"})
	require.ErrorContains(t, err, "audit unavailable")
	actual := client.Ticket.GetX(ctx, item.ID)
	require.Equal(t, item.Status, actual.Status)
	require.Equal(t, item.Version, actual.Version)
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	require.Zero(t, client.IncidentEvent.Query().CountX(ctx))
}

func TestIncidentCommandCloseReopenAuthorizationAndTenant(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "close-reopen")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "close-reopen")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "close-reopen")
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("resolved").SaveX(ctx)
	cmd := dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "close"}, IncidentID: inc.ID, Action: "close", Reason: "requester confirmed stable service"}
	closed, err := svc.ApplyIncidentCommand(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "closed", closed.Status)
	require.NotNil(t, client.Ticket.GetX(ctx, item.ID).ClosedAt)
	cmd.Action = "reopen"
	cmd.Meta.OperationID = "reopen"
	cmd.Meta.ExpectedVersion = closed.Version
	actor.Update().SetRole("unknown-no-permissions").ExecX(ctx)
	_, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.Error(t, err)
	require.Equal(t, "closed", client.Ticket.GetX(ctx, item.ID).Status)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	cmd.Meta.TenantID = tenant.ID + 1000
	_, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.Error(t, err)
	cmd.Meta.TenantID = tenant.ID
	reopened, err := svc.ApplyIncidentCommand(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "in_progress", reopened.Status)
	require.Nil(t, client.Ticket.GetX(ctx, item.ID).ClosedAt)
	actor.Update().SetActive(false).ExecX(ctx)
	_, err = svc.ApplyIncidentCommand(ctx, cmd)
	require.Error(t, err, "replay must reauthorize revoked actor")
}
