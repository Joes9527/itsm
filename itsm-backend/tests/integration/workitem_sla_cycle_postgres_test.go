//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	ticketrepo "itsm-backend/repository/ticket"
	"itsm-backend/service"
	"testing"
	"time"
)

// The test command models the contract consumed by subsequent professional commands:
// receipt replay, CAS, SLA side effect and command receipt share one transaction.
func cycleCommand(ctx context.Context, client *ent.Client, id int, at time.Time, meta workitemmutation.Meta) (workitemmutation.Result, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	defer tx.Rollback()
	digest := fmt.Sprintf("reset:%d:%s", id, at.Format(time.RFC3339Nano))
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantID(meta.TenantID), auditlog.UserID(meta.ActorID), auditlog.OperationID(meta.OperationID)).Only(ctx)
	if err == nil {
		if receipt.RequestDigest == nil || *receipt.RequestDigest != digest {
			return workitemmutation.Result{}, errors.New("operation conflict")
		}
		return workitemmutation.Result{WorkItemID: id, Version: *receipt.ResultVersion, Status: *receipt.ResultStatus, Replayed: true}, nil
	}
	if !ent.IsNotFound(err) {
		return workitemmutation.Result{}, err
	}
	item, err := tx.Ticket.Query().Where(ticket.ID(id), ticket.TenantID(meta.TenantID)).Only(ctx)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	n, err := tx.Ticket.Update().Where(ticket.ID(id), ticket.TenantID(meta.TenantID), ticket.Version(meta.ExpectedVersion)).AddVersion(1).Save(ctx)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	if n != 1 {
		return workitemmutation.Result{}, errors.New("version conflict")
	}
	if err = service.NewTicketSLAService(client, zap.NewNop().Sugar()).ResetCycleTx(ctx, tx, item, at, meta); err != nil {
		return workitemmutation.Result{}, err
	}
	result := workitemmutation.Result{WorkItemID: id, Version: meta.ExpectedVersion + 1, Status: item.Status}
	_, err = tx.AuditLog.Create().SetTenantID(meta.TenantID).SetUserID(meta.ActorID).SetOperationID(meta.OperationID).SetRequestDigest(digest).SetResultVersion(result.Version).SetResultStatus(result.Status).SetPath(fmt.Sprint(id)).SetMethod("COMMAND").SetAction("test.reopen").Save(ctx)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func cycleFixture(t *testing.T) (*incidentEffectsFixture, *ent.Ticket, *ent.SLADefinition, time.Time, workitemmutation.Meta) {
	f := newIncidentEffectsFixture(t)
	at := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	item = item.Update().SetCreatedAt(at).SaveX(f.ctx)
	policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("frozen").SetResponseTime(60).SetResolutionTime(60).SaveX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, item, &policy.ID))
	require.NoError(t, tx.Commit())
	item = f.client.Ticket.UpdateOneID(item.ID).SetFirstResponseAt(at.Add(30 * time.Minute)).SetResolvedAt(at.Add(time.Hour)).SetSLAPausedMinutes(30).SaveX(f.ctx)
	return f, item, policy, at, workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, OperationID: "reopen-1", Source: "api"}
}
func TestWorkItemSLACycleFrozenReopenAndReceipt(t *testing.T) {
	f, item, policy, at, meta := cycleFixture(t)
	policy.Update().SetResponseTime(900).SetResolutionTime(900).SetBusinessHours(map[string]interface{}{"work_days": []interface{}{0}}).SaveX(f.ctx)
	result, err := cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
	require.NoError(t, err)
	got := f.client.Ticket.GetX(f.ctx, item.ID)
	require.Equal(t, 2, got.SLACycleNumber)
	require.Equal(t, 0, got.SLAPausedMinutes)
	require.True(t, got.FirstResponseAt.IsZero())
	require.True(t, got.ResolvedAt.IsZero())
	require.True(t, got.SLAResponseDeadline.Equal(at.Add(5*time.Hour)), "new response deadline must be 13:00")
	require.Equal(t, 60, got.AppliedSLAPolicy.ResponseMinutes)
	replay, err := cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.Version, replay.Version)
	_, err = cycleCommand(f.ctx, f.client, item.ID, at.Add(5*time.Hour), meta)
	require.ErrorContains(t, err, "operation conflict")
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, item.ID).SLACycleNumber)
	history := f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).AllX(f.ctx)
	require.Len(t, history, 1)
	var facts map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(*history[0].RequestBody), &facts))
	require.NotNil(t, facts["policy"])
	_, err = service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).GetTicketSLAInfo(f.ctx, item.ID, f.tenant.ID+1)
	require.Error(t, err)
}
func TestWorkItemSLACycleAuditFailureRollsBackCAS(t *testing.T) {
	for _, stage := range []string{"before_fact", "after_receipt"} {
		t.Run(stage, func(t *testing.T) {
			f, item, _, at, meta := cycleFixture(t)
			f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if stage == "before_fact" {
						return nil, errors.New("audit fault")
					}
					value, err := next.Mutate(ctx, m)
					if err != nil {
						return nil, err
					}
					action, _ := m.(*ent.AuditLogMutation).Action()
					if action == "test.reopen" {
						return nil, errors.New("audit fault after receipt")
					}
					return value, nil
				})
			})
			_, err := cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
			require.ErrorContains(t, err, "audit fault")
			got := f.client.Ticket.GetX(f.ctx, item.ID)
			require.Equal(t, item.Version, got.Version)
			require.Equal(t, 1, got.SLACycleNumber)
			require.False(t, got.ResolvedAt.IsZero())
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
		})
	}
}
func TestWorkItemSLACyclePreservesBreachAndFailsClosed(t *testing.T) {
	f, item, _, at, meta := cycleFixture(t)
	f.client.Ticket.UpdateOneID(item.ID).SetResolvedAt(at.Add(2 * time.Hour)).SaveX(f.ctx)
	_, err := cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
	require.NoError(t, err)
	history := f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).OnlyX(f.ctx)
	require.Contains(t, *history.RequestBody, `"resolutionBreached":true`)
	meta.OperationID = "reopen-2"
	meta.ExpectedVersion++
	f.client.Ticket.UpdateOneID(item.ID).ClearAppliedSLAPolicy().SaveX(f.ctx)
	_, err = cycleCommand(f.ctx, f.client, item.ID, at.Add(8*time.Hour), meta)
	require.ErrorContains(t, err, "applied SLA policy")
	require.Equal(t, meta.ExpectedVersion, f.client.Ticket.GetX(f.ctx, item.ID).Version)
}

func TestWorkItemSLACycleExplicitPolicyApplication(t *testing.T) {
	f, item, policy, at, meta := cycleFixture(t)
	f.client.Ticket.UpdateOneID(item.ID).ClearAppliedSLAPolicy().SaveX(f.ctx)
	policy.Update().SetResponseTime(120).SaveX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	tx.Ticket.UpdateOneID(item.ID).AddVersion(1).SaveX(f.ctx)
	require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyPolicyTx(f.ctx, tx, item, policy.ID, at.Add(4*time.Hour), meta))
	require.NoError(t, tx.Commit())
	got := f.client.Ticket.GetX(f.ctx, item.ID)
	require.Equal(t, 120, got.AppliedSLAPolicy.ResponseMinutes)
	require.True(t, got.SLAResponseDeadline.Equal(at.Add(6*time.Hour)))
	require.Equal(t, 2, got.SLACycleNumber)
}
func TestWorkItemSLACycleMetadataAndNoSLA(t *testing.T) {
	f, item, _, at, meta := cycleFixture(t)
	for _, test := range []struct {
		name   string
		change func(*workitemmutation.Meta)
	}{
		{"missing actor", func(m *workitemmutation.Meta) { m.ActorID = 0 }},
		{"other tenant", func(m *workitemmutation.Meta) { m.TenantID++ }},
		{"unknown actor", func(m *workitemmutation.Meta) { m.ActorID += 10000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := meta
			test.change(&bad)
			tx, err := f.client.Tx(f.ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			tx.Ticket.UpdateOneID(item.ID).AddVersion(1).SaveX(f.ctx)
			require.Error(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ResetCycleTx(f.ctx, tx, item, at.Add(4*time.Hour), bad))
		})
	}
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.ErrorContains(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ResetCycleTx(f.ctx, tx, item, at.Add(4*time.Hour), meta), "caller CAS")
	require.NoError(t, tx.Rollback())
	f.client.Ticket.UpdateOneID(item.ID).ClearSLADefinitionID().ClearAppliedSLAPolicy().ClearSLAResponseDeadline().ClearSLAResolutionDeadline().SetSLACycleNumber(0).ClearSLACycleStartedAt().SaveX(f.ctx)
	_, err = cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
	require.NoError(t, err)
	require.Equal(t, 0, f.client.Ticket.GetX(f.ctx, item.ID).SLACycleNumber)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).CountX(f.ctx))
}
func TestWorkItemSLACycleCompletionProjection(t *testing.T) {
	f, item, _, at, _ := cycleFixture(t)
	svc := service.NewTicketSLAService(f.client, zap.NewNop().Sugar())
	got, err := svc.GetTicketSLAInfo(f.ctx, item.ID, f.tenant.ID)
	require.NoError(t, err)
	require.False(t, got.ResponseBreached)
	require.False(t, got.ResolutionBreached)
	require.True(t, got.ResponseDeadline.Equal(at.Add(time.Hour)))
	require.Equal(t, 0, got.ResponseTimeUsed)
	require.Equal(t, 30, got.ResolutionTimeUsed)
	metrics, err := service.NewSLAMonitorService(f.client, zap.NewNop().Sugar()).CalculateSLAMetrics(f.ctx, f.tenant.ID, at.Add(-time.Hour), at.Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, metrics.ViolatedTickets)
	require.Equal(t, 0.5, metrics.AvgResolutionHours)
	apiSvc := service.NewTicketService(&service.TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
	apiInfo, err := apiSvc.GetTicketSLAInfo(f.ctx, item.ID, f.tenant.ID)
	require.NoError(t, err)
	require.False(t, apiInfo.IsBreached)
	require.Equal(t, 1, apiInfo.CycleNumber)
	require.Equal(t, 60, apiInfo.ResponseTime)
	require.Equal(t, 30, *apiInfo.ResponseTimeRemaining)
	require.Equal(t, 0, *apiInfo.ResolutionTimeRemaining)
	_, err = apiSvc.GetTicketSLAInfo(f.ctx, item.ID, f.tenant.ID+1)
	require.Error(t, err)

}

func TestWorkItemSLACycleMigrationAndImmutableAudit(t *testing.T) {
	f, item, _, at, meta := cycleFixture(t)
	sql := migration.GetMigrationSQL("032_workitem_sla_cycle")
	require.NotEmpty(t, sql)
	_, err := f.db.ExecContext(f.ctx, sql)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, sql)
	require.NoError(t, err, "migration is idempotent")
	_, err = cycleCommand(f.ctx, f.client, item.ID, at.Add(4*time.Hour), meta)
	require.NoError(t, err)
	row := f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).OnlyX(f.ctx)
	_, err = row.Update().SetRequestBody("{}").Save(f.ctx)
	require.ErrorContains(t, err, "immutable")
	err = f.client.AuditLog.DeleteOneID(row.ID).Exec(f.ctx)
	require.ErrorContains(t, err, "immutable")
	receipt := f.client.AuditLog.Query().Where(auditlog.OperationID(meta.OperationID)).OnlyX(f.ctx)
	_, err = receipt.Update().SetResultVersion(999).Save(f.ctx)
	require.ErrorContains(t, err, "immutable")
	err = f.client.AuditLog.DeleteOneID(receipt.ID).Exec(f.ctx)
	require.ErrorContains(t, err, "immutable")
	_, err = f.client.AuditLog.Create().SetTenantID(meta.TenantID).SetUserID(meta.ActorID).SetOperationID(meta.OperationID).SetPath("test").SetMethod("COMMAND").Save(f.ctx)
	require.Error(t, err, "durable unique operation key")
	normal := f.client.AuditLog.Create().SetTenantID(meta.TenantID).SetUserID(meta.ActorID).SetAction("http.request").SetPath("test").SetMethod("GET").SaveX(f.ctx)
	_, err = normal.Update().SetStatusCode(200).Save(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.client.AuditLog.DeleteOneID(normal.ID).Exec(f.ctx))

}

func TestWorkItemSLACycleEscalationAfterRealReopen(t *testing.T) {
	f := incidentLifecycleFixture(t)
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-2 * time.Hour)
	policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("cycle escalation").SetResponseTime(30).SetResolutionTime(60).SaveX(f.ctx)
	item = item.Update().SetCreatedAt(at).SaveX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, item, &policy.ID))
	require.NoError(t, tx.Commit())
	old := f.client.SLAViolation.Create().SetTenantID(f.tenant.ID).SetTicketID(item.ID).SetSLADefinitionID(policy.ID).SetViolationType("resolution_time").SetViolationTime(at.Add(time.Hour)).SetViolationOccurredAt(at.Add(time.Hour)).SaveX(f.ctx)
	old = f.client.SLAViolation.GetX(f.ctx, old.ID)
	cmd := incidentPGCommand(f, "cycle-resolve")
	_, err = f.svc.ApplyIncidentCommand(f.ctx, cmd)
	require.NoError(t, err)
	cmd = incidentPGCommand(f, "cycle-reopen")
	cmd.Action = "reopen"
	cmd.Reason = "service interrupted again"
	_, err = f.svc.ApplyIncidentCommand(f.ctx, cmd)
	require.NoError(t, err)
	before := f.client.Ticket.GetX(f.ctx, item.ID)
	svc := service.NewIncidentEscalationService(f.client)
	_, err = svc.CreateEscalationRule(f.ctx, dto.CreateIncidentEscalationRuleRequest{Name: "cycle L1", TriggerType: "sla_breach", TriggerMinutes: 1, EscalationLevel: 1, TargetAssigneeType: "user", AutoEscalate: true, IsActive: true, TenantID: f.tenant.ID})
	require.NoError(t, err)
	check := func(key string) {
		_, err := svc.CheckAndEscalate(f.ctx, f.inc.ID, workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: before.Version, Source: "scheduler", OperationID: key})
		require.NoError(t, err)
	}
	check("old-cycle")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version, "old cycle cannot escalate reopened work")
	// A stale monitor snapshot may be inserted after reopen; current breach facts must still gate it.
	delayed := f.client.SLAViolation.Create().SetTenantID(f.tenant.ID).SetTicketID(item.ID).SetSLADefinitionID(policy.ID).SetViolationType("resolution_time").SetViolationTime(time.Now()).SetViolationOccurredAt(time.Now()).SaveX(f.ctx)
	delayed = f.client.SLAViolation.GetX(f.ctx, delayed.ID)
	check("delayed-old-cycle")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	// New response breach must not match a resolution-only old report.
	f.client.Ticket.UpdateOneID(item.ID).SetSLAResponseDeadline(time.Now().Add(-time.Minute)).ExecX(f.ctx)
	check("wrong-type")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	f.client.SLAViolation.Create().SetTenantID(f.tenant.ID).SetTicketID(item.ID).SetSLADefinitionID(policy.ID).SetViolationType("response_time").SetViolationTime(time.Now()).SetViolationOccurredAt(at.Add(30 * time.Minute)).SaveX(f.ctx)
	check("old-occurrence")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)

	f.client.SLAViolation.Create().SetTenantID(f.tenant.ID).SetTicketID(item.ID).SetSLADefinitionID(policy.ID).SetViolationType("response_time").SetViolationTime(time.Now()).SetViolationOccurredAt(time.Now()).SaveX(f.ctx)
	check("current-cycle")
	require.Equal(t, "escalated", f.client.Ticket.GetX(f.ctx, item.ID).Status)
	oldJSON, err := json.Marshal(old)
	require.NoError(t, err)
	oldAfterJSON, err := json.Marshal(f.client.SLAViolation.GetX(f.ctx, old.ID))
	require.NoError(t, err)
	require.JSONEq(t, string(oldJSON), string(oldAfterJSON))
	delayedJSON, err := json.Marshal(delayed)
	require.NoError(t, err)
	delayedAfterJSON, err := json.Marshal(f.client.SLAViolation.GetX(f.ctx, delayed.ID))
	require.NoError(t, err)
	require.JSONEq(t, string(delayedJSON), string(delayedAfterJSON))
	detail, err := service.NewTicketServiceForTest(f.client, zap.NewNop().Sugar()).GetTicketSLAInfo(f.ctx, item.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, 2, detail.CycleNumber)
	require.Len(t, detail.History, 1)
	require.True(t, detail.History[0].ResolutionBreached)
	require.True(t, f.client.Ticket.GetX(f.ctx, item.ID).CreatedAt.Equal(at))
}

func TestWorkItemSLACycleRealReopenUnavailableFrozenContract(t *testing.T) {
	for _, failure := range []string{"unsupported_snapshot", "invalid_calendar"} {
		t.Run(failure, func(t *testing.T) {
			f := incidentLifecycleFixture(t)
			item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
			policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("frozen calendar").SetResponseTime(30).SetResolutionTime(60).SaveX(f.ctx)
			tx, err := f.client.Tx(f.ctx)
			require.NoError(t, err)
			require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, item, &policy.ID))
			require.NoError(t, tx.Commit())
			_, err = f.svc.ApplyIncidentCommand(f.ctx, incidentPGCommand(f, "calendar-resolve"))
			require.NoError(t, err)
			item = f.client.Ticket.GetX(f.ctx, item.ID)
			snapshot := *item.AppliedSLAPolicy
			if failure == "unsupported_snapshot" {
				snapshot.SchemaVersion = 999
			} else {
				snapshot.BusinessHours = map[string]interface{}{"work_days": []interface{}{0}}
			}
			before := item.Update().SetAppliedSLAPolicy(&snapshot).SaveX(f.ctx)
			cmd := incidentPGCommand(f, "calendar-reopen")
			cmd.Action = "reopen"
			cmd.Reason = "service interrupted again"
			_, err = f.svc.ApplyIncidentCommand(f.ctx, cmd)
			require.Error(t, err)
			after := f.client.Ticket.GetX(f.ctx, item.ID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, before.SLACycleNumber, after.SLACycleNumber)
			require.Equal(t, before.AppliedSLAPolicy, after.AppliedSLAPolicy)
			require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("sla.cycle.completed")).CountX(f.ctx))
		})
	}
}
