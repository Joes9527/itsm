package service

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"
	"time"
)

func TestIncidentEscalationCommandsAtomicReplay(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "success_replay", true: "invalid_target_rollback"}[invalid], func(t *testing.T) {
			client, _, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "escalation-command")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "actor")
			require.NoError(t, err)
			actor.Update().SetRole("super_admin").ExecX(ctx)
			target, err := createIncidentTestUser(ctx, client, tenant.ID, "target")
			require.NoError(t, err)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "INC-ESC-COMMAND")
			inc.Update().SetDetectedAt(time.Now().Add(-time.Hour)).ExecX(ctx)
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
			targetID := target.ID
			if invalid {
				targetID = 999999
			}
			svc := NewIncidentEscalationService(client, executionfixture.Standard())
			_, err = svc.CreateEscalationRule(ctx, dto.CreateIncidentEscalationRuleRequest{Name: "timeout L1", TriggerType: "time_based", TriggerMinutes: 1, EscalationLevel: 1, TargetAssigneeType: "user", TargetAssigneeID: &targetID, AutoEscalate: true, IsActive: true, TenantID: tenant.ID})
			require.NoError(t, err)
			meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "scheduler", OperationID: "escalation-attempt"}
			_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
			if invalid {
				require.Error(t, err)
				after := client.Ticket.GetX(ctx, before.ID)
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.Status, after.Status)
				require.Zero(t, client.Incident.GetX(ctx, inc.ID).EscalationLevel)
				require.Zero(t, client.AuditLog.Query().CountX(ctx))
				return
			}
			require.NoError(t, err)
			after := client.Ticket.GetX(ctx, before.ID)
			require.Equal(t, target.ID, after.AssigneeID)
			require.Equal(t, "escalated", after.Status)
			require.Equal(t, before.Version+2, after.Version)
			audits := client.AuditLog.Query().CountX(ctx)
			require.Equal(t, 2, audits)
			_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
			require.NoError(t, err)
			require.Equal(t, after.Version, client.Ticket.GetX(ctx, before.ID).Version)
			require.Equal(t, audits, client.AuditLog.Query().CountX(ctx))
		})
	}
}

func TestIncidentEscalationAlertAcceptanceRollsBackAndReplays(t *testing.T) {
	client, owner, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "escalation-alert")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "actor")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "INC-ESC-ALERT")
	inc.Update().SetDetectedAt(time.Now().Add(-time.Hour)).ExecX(ctx)
	before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	svc := NewIncidentEscalationService(client, executionfixture.Standard())
	_, err = svc.CreateEscalationRule(ctx, dto.CreateIncidentEscalationRuleRequest{Name: "timeout L1", TriggerType: "time_based", TriggerMinutes: 1, EscalationLevel: 1, TargetAssigneeType: "user", AutoEscalate: true, IsActive: true, TenantID: tenant.ID, NotificationConfig: map[string]interface{}{"email": true, "recipients": []string{"operator@example.com"}}})
	require.NoError(t, err)
	meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "scheduler", OperationID: "escalation-alert"}
	_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
	require.Error(t, err)
	require.Equal(t, before.Version, client.Ticket.GetX(ctx, before.ID).Version)
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	alertCreator := NewIncidentAlertingService(client, owner.logger, executionfixture.Standard())
	ctx = configureIncidentAlertProducerTarget(t, ctx, alertCreator, tenant.ID)
	svc.SetAlertCreator(alertCreator)
	_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
	require.NoError(t, err)
	require.Equal(t, 1, client.IncidentAlert.Query().CountX(ctx))
	outbox := client.OutboxEvent.Query().CountX(ctx)
	require.GreaterOrEqual(t, outbox, 2)
	_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
	require.NoError(t, err)
	require.Equal(t, 1, client.IncidentAlert.Query().CountX(ctx))
	require.Equal(t, outbox, client.OutboxEvent.Query().CountX(ctx))
	actor.Update().SetActive(false).ExecX(ctx)
	_, err = svc.CheckAndEscalate(ctx, inc.ID, meta)
	require.Error(t, err)
}

func TestIncidentEscalationBatchUsesCanonicalCandidates(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "escalation-batch")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "actor")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "INC-BATCH")
	inc.Update().SetDetectedAt(time.Now().Add(-time.Hour)).ExecX(ctx)
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	svc := NewIncidentEscalationService(client, executionfixture.Standard())
	_, err = svc.CreateEscalationRule(ctx, dto.CreateIncidentEscalationRuleRequest{Name: "batch L1", TriggerType: "time_based", TriggerMinutes: 1, EscalationLevel: 1, TargetAssigneeType: "user", AutoEscalate: true, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	require.Error(t, svc.ProcessEscalations(ctx, tenant.ID))
	ctx = WithIncidentAlertActor(ctx, actor.ID, "scheduler", "batch-run")
	require.NoError(t, svc.ProcessEscalations(ctx, tenant.ID))
	require.Equal(t, item.Version+1, client.Ticket.GetX(ctx, item.ID).Version)
	require.NoError(t, svc.ProcessEscalations(ctx, tenant.ID))
	require.Equal(t, item.Version+1, client.Ticket.GetX(ctx, item.ID).Version)
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
}
