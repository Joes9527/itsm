//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	problem "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
	"itsm-backend/migration"
	"itsm-backend/service"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkItemProblemLifecycleAllocatedMSP(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint("bound=", bound), func(t *testing.T) {
			f := newProblemLifecycleFixture(t)
			f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
			provider := f.client.Tenant.Create().SetCode("lifecycle-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
			actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider-operator").SetName("MSP operator").SetEmail("provider-operator@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
			allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
			for _, resource := range []string{"problem", "incident"} {
				permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":write").SetName(resource + " write").SetResource(resource).SetAction("write").SaveX(f.ctx)
				f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
			}
			authorization.InvalidateAllPermissionCaches()
			t.Cleanup(authorization.InvalidateAllPermissionCaches)
			nativeInvestigator := f.actor
			clients, cfg := runtimeClients(t, f.incidentEffectsFixture)
			for _, table := range []string{"problems", "problem_investigations"} {
				_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
				require.NoError(t, err)
				_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+table+"_id_seq TO "+cfg.User)
				require.NoError(t, err)
			}
			f.ctx = tenantctx.WithTenantID(f.ctx, f.tenant.ID)
			f.owner = problem.NewService(problem.NewEntRepository(clients.Tenant), zap.NewNop().Sugar())
			f.owner.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
			_, err := clients.Tenant.User.Get(f.ctx, actor.ID)
			require.True(t, ent.IsNotFound(err), "provider must be hidden in customer RLS")
			f.actor = actor
			if bound {
				policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("MSP SLA").SetResponseTime(30).SetResolutionTime(120).SaveX(f.ctx)
				tx, err := f.client.Tx(f.ctx)
				require.NoError(t, err)
				require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID), &policy.ID))
				require.NoError(t, tx.Commit())
			}
			for _, investigator := range []int{0, actor.ID} {
				cmd := f.command("investigate", fmt.Sprint("invalid-investigator-", investigator))
				cmd.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: investigator}
				_, err := f.owner.ApplyCommand(f.ctx, cmd)
				require.ErrorContains(t, err, "select an active investigator in the customer tenant")
			}
			start := f.command("investigate", "allocated-start")
			start.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: nativeInvestigator.ID}
			_, err = f.owner.ApplyCommand(f.ctx, start)
			require.NoError(t, err)
			var assigned int
			require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT investigator_id FROM problem_investigations WHERE problem_id=$1", f.p.ID).Scan(&assigned))
			require.Equal(t, nativeInvestigator.ID, assigned)
			_, readErr := f.owner.Get(f.ctx, f.p.ID, workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, Source: "http"})
			require.Error(t, readErr, "MSP allocation alone does not grant current shared row visibility")
			f.evidence(t)
			f.apply(t, "verify_resolution", "allocated-verify")
			require.Equal(t, actor.ID, f.client.Problem.GetX(f.ctx, f.p.ID).VerifiedBy)
			f.apply(t, "resolve", "allocated-resolve")
			f.apply(t, "close", "allocated-close")
			f.apply(t, "reopen", "allocated-reopen")
			if bound {
				require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).SLACycleNumber)
			}
			allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			_, err = f.owner.ApplyCommand(f.ctx, f.command("verify_resolution", "revoked"))
			require.Error(t, err)
			allocation.Update().ClearDeassignedAt().ExecX(f.ctx)
			incOwner := service.NewIncidentService(clients.Tenant, zap.NewNop().Sugar())
			incOwner.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
			incidentCommand := func(action, key string) dto.IncidentCommand {
				return dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version, OperationID: key, Source: "http"}, IncidentID: f.inc.ID, Action: action, Resolution: "service restored", Reason: "confirmed"}
			}
			for _, action := range []string{"acknowledge", "start", "resolve", "close", "reopen"} {
				_, err := incOwner.ApplyIncidentCommand(f.ctx, incidentCommand(action, "msp-"+action))
				require.NoError(t, err)
			}
			assertDenied := func(label string) {
				t.Helper()
				_, err := f.owner.ApplyCommand(f.ctx, f.command("verify_resolution", label))
				require.Error(t, err, label)
				appErr, ok := common.AsAppError(err)
				require.True(t, ok, label)
				require.Equal(t, common.ErrCodeForbidden, appErr.Code, label)
				_, err = incOwner.ApplyIncidentCommand(f.ctx, incidentCommand("resolve", label))
				require.Error(t, err, label)
				appErr, ok = common.AsAppError(err)
				require.True(t, ok, label)
				require.Equal(t, common.ErrCodeForbidden, appErr.Code, label)
			}
			allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			assertDenied("revoked-allocation")
			outsider := f.client.User.Create().SetTenantID(provider.ID).SetUsername("unallocated").SetName("unallocated").SetEmail("unallocated@example.test").SetPasswordHash("test").SetRole("super_admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
			outsider.Update().SetRole("admin").ExecX(f.ctx)
			f.actor = outsider
			assertDenied("unallocated-provider")
			outsider.Update().ClearMspRole().ExecX(f.ctx)
			assertDenied("native-cross-tenant")
			f.actor = &ent.User{ID: 999999}
			assertDenied("forged-actor")
			allocation.Update().ClearDeassignedAt().ExecX(f.ctx)
			f.actor = actor
			rule := f.client.IncidentRule.Create().SetTenantID(f.tenant.ID).SetName("Provider close").SetRuleType("automation").SetIsActive(true).SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "change_status", "status": "resolved", "resolution": "restored by provider"}}).SaveX(f.ctx)
			ruleIncident := f.client.Incident.GetX(f.ctx, f.inc.ID)
			ruleIncident.Edges.WorkItem = f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
			ruleCtx := service.WithIncidentAlertActor(f.ctx, actor.ID, "incident_rule", "allocated-rule")
			require.NoError(t, incOwner.RuleEngine().ExecuteRule(ruleCtx, rule, ruleIncident, f.tenant.ID))
			rule.Actions = []map[string]interface{}{{"type": "change_status", "status": "closed", "reason": "confirmed"}}
			allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			deniedRuleCtx := service.WithIncidentAlertActor(f.ctx, actor.ID, "incident_rule", "revoked-rule")
			require.Error(t, incOwner.RuleEngine().ExecuteRule(deniedRuleCtx, rule, ruleIncident, f.tenant.ID))
			allocation.Update().ClearDeassignedAt().ExecX(f.ctx)

			actor.Update().SetActive(false).ExecX(f.ctx)
			assertDenied("inactive-actor")
			actor.Update().SetActive(true).ExecX(f.ctx)
			role.Update().SetIsActive(false).ExecX(f.ctx)
			assertDenied("revoked-permission")
			role.Update().SetIsActive(true).ExecX(f.ctx)
			outsider.Update().SetRole("super_admin").ExecX(f.ctx)
			f.actor = outsider
			f.apply(t, "verify_resolution", "native-super-admin")
			_, err = incOwner.ApplyIncidentCommand(f.ctx, incidentCommand("reopen", "incident-native-super-admin"))
			require.NoError(t, err)
			require.Equal(t, outsider.ID, f.client.Problem.GetX(f.ctx, f.p.ID).VerifiedBy)
			var wrongActors int
			require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM audit_logs WHERE path=$1 AND resource='work_item' AND user_id NOT IN ($2,$3)", fmt.Sprint(f.p.WorkItemID), actor.ID, outsider.ID).Scan(&wrongActors))
			require.Zero(t, wrongActors)
			var providerReceipts int
			require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM audit_logs WHERE path=$1 AND resource='work_item' AND user_id=$2", fmt.Sprint(f.p.WorkItemID), actor.ID).Scan(&providerReceipts))
			require.Equal(t, 5, providerReceipts)
		})
	}
}

type problemLifecycleFixture struct {
	*incidentEffectsFixture
	owner *problem.Service
	p     *ent.Problem
}

func newProblemLifecycleFixture(t *testing.T) *problemLifecycleFixture {
	t.Helper()
	f := newIncidentEffectsFixture(t)
	for _, name := range []string{"032_workitem_sla_cycle", "034_problem_investigation_completion"} {
		sql := migration.GetMigrationSQL(name)
		require.NotEmpty(t, sql)
		_, err := f.db.ExecContext(f.ctx, sql)
		require.NoError(t, err)
	}
	rca, err := os.ReadFile("../../migrations/20260909_problem_rca_authority.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(rca))
	require.NoError(t, err)
	f.actor.Update().SetRole("super_admin").ExecX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("Recurring pool leak").SetTicketNumber("PRB-LIFECYCLE").SetRecordClass("problem").SetStatus("open").SetPriority("high").SaveX(f.ctx)
	p := f.client.Problem.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	return &problemLifecycleFixture{f, problem.NewService(problem.NewEntRepository(f.client), zap.NewNop().Sugar()), p}
}
func (f *problemLifecycleFixture) command(action, key string) problem.Command {
	item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	return problem.Command{Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "test", OperationID: key}, ProblemID: f.p.ID, Action: action}
}
func (f *problemLifecycleFixture) apply(t *testing.T, action, key string) workitemmutation.Result {
	t.Helper()
	cmd := f.command(action, key)
	cmd.VerificationNote = "Regression and load checks passed"
	result, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.NoError(t, err)
	return result
}
func (f *problemLifecycleFixture) evidence(t *testing.T) {
	t.Helper()
	// Persistence probe supplies the observed version for this metadata effect fixture; it is not a public relation read.
	p := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	_, err := f.owner.Update(f.ctx, f.tenant.ID, f.p.ID, &problem.Problem{Version: p.Version, RootCause: "Connection leak", Resolution: "Close connections on cancellation"})
	require.NoError(t, err)
}

func TestWorkItemProblemLifecycleEvidenceReplayAndReopen(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	_, err := f.owner.ApplyCommand(f.ctx, f.command("resolve", "missing"))
	require.ErrorContains(t, err, "verified permanent")
	f.apply(t, "investigate", "start")
	_, err = f.owner.ApplyCommand(f.ctx, f.command("investigate", "same-state"))
	require.Error(t, err)
	f.evidence(t)
	policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("Problem SLA").SetResponseTime(30).SetResolutionTime(120).SaveX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID), &policy.ID))
	require.NoError(t, tx.Commit())
	_, err = f.owner.ApplyCommand(f.ctx, f.command("resolve", "unverified"))
	require.Error(t, err)
	verify := f.command("verify_resolution", "verify")
	verify.VerificationNote = "Regression passed"
	verified, err := f.owner.ApplyCommand(f.ctx, verify)
	require.NoError(t, err)
	p := f.client.Problem.GetX(f.ctx, f.p.ID)
	require.Equal(t, verified.Version, p.VerifiedVersion)
	require.Equal(t, f.actor.ID, p.VerifiedBy)
	require.False(t, p.VerifiedAt.IsZero())
	resolved := f.apply(t, "resolve", "resolve")
	require.Equal(t, "resolved", resolved.Status)
	f.apply(t, "close", "close")
	item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	require.False(t, item.ResolvedAt.IsZero())
	require.False(t, item.ClosedAt.IsZero())
	replay, err := f.owner.ApplyCommand(f.ctx, verify)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, verified.Version, replay.Version)
	verify.VerificationNote = "changed"
	_, err = f.owner.ApplyCommand(f.ctx, verify)
	var conflict *workitemmutation.OperationConflictError
	require.ErrorAs(t, err, &conflict)
	f.apply(t, "reopen", "reopen")
	item = f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	require.Equal(t, "investigating", item.Status)
	require.True(t, item.ResolvedAt.IsZero())
	require.Nil(t, item.ClosedAt)
	require.Zero(t, f.client.Problem.GetX(f.ctx, f.p.ID).VerifiedVersion)
	require.Equal(t, 2, item.SLACycleNumber)
	_, err = f.owner.ApplyCommand(f.ctx, f.command("resolve", "old-evidence"))
	require.Error(t, err)
}

func TestWorkItemProblemLifecycleLegacyAndVersionChanges(t *testing.T) {
	for _, state := range []string{"resolved", "closed"} {
		t.Run(state, func(t *testing.T) {
			f := newProblemLifecycleFixture(t)
			f.client.Ticket.UpdateOneID(f.p.WorkItemID).SetStatus(state).ExecX(f.ctx)
			_, err := f.owner.ApplyCommand(f.ctx, f.command("close", "legacy-close"))
			require.Error(t, err)
			f.apply(t, "reopen", "legacy-reopen")
		})
	}
	f := newProblemLifecycleFixture(t)
	f.apply(t, "investigate", "start")
	f.evidence(t)
	f.apply(t, "verify_resolution", "verify")
	stale := f.command("resolve", "resolve-stale")
	before := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	_, err := f.owner.Update(f.ctx, f.tenant.ID, f.p.ID, &problem.Problem{Version: before.Version, RootCause: "Corrected cause"})
	require.NoError(t, err)
	_, err = f.owner.ApplyCommand(f.ctx, stale)
	require.True(t, common.IsVersionConflictError(err))
	_, err = f.owner.ApplyCommand(f.ctx, f.command("resolve", "new-version-old-verification"))
	require.Error(t, err)
	_, err = f.owner.Update(f.ctx, f.tenant.ID, f.p.ID, &problem.Problem{Status: "resolved", Version: before.Version})
	require.ErrorContains(t, err, "lifecycle command")
	_, err = f.owner.Update(f.ctx, f.tenant.ID, f.p.ID, &problem.Problem{Title: "missing version"})
	require.ErrorContains(t, err, "version required")
	f.apply(t, "verify_resolution", "reverify")
	f.apply(t, "resolve", "resolve")
	before = f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	_, err = f.owner.Update(f.ctx, f.tenant.ID, f.p.ID, &problem.Problem{Version: before.Version, Resolution: "Revised solution"})
	require.NoError(t, err)
	_, err = f.owner.ApplyCommand(f.ctx, f.command("close", "edited-after-resolve"))
	require.Error(t, err)
}

func TestWorkItemProblemLifecycleInvestigationAtomicityAndCandidateAuthority(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	cmd := f.command("investigate", "create-investigation")
	cmd.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: f.actor.ID, InvestigationSummary: "pool investigation"}
	_, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.NoError(t, err)
	var investigationID int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT id FROM problem_investigations WHERE problem_id=$1", f.p.ID).Scan(&investigationID))
	reader := service.NewProblemInvestigationService(f.db, zap.NewNop().Sugar())
	complete := dto.InvestigationStatusCompleted
	_, err = reader.UpdateProblemInvestigation(f.ctx, investigationID, &dto.UpdateProblemInvestigationRequest{Status: &complete}, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "investigating", f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Status)
	candidate, err := reader.CreateProblemSolution(f.ctx, &dto.CreateProblemSolutionRequest{ProblemID: f.p.ID, SolutionType: dto.SolutionTypeWorkaround, SolutionDescription: "Restart process", ProposedBy: f.actor.ID, Priority: "high"}, f.tenant.ID)
	require.NoError(t, err)
	selectCmd := f.command("select_resolution", "select-workaround")
	selectCmd.SolutionID = candidate.ID
	_, err = f.owner.ApplyCommand(f.ctx, selectCmd)
	require.ErrorContains(t, err, "permanent solution")
	require.Empty(t, f.client.Problem.GetX(f.ctx, f.p.ID).Resolution)
	candidate, err = reader.CreateProblemSolution(f.ctx, &dto.CreateProblemSolutionRequest{ProblemID: f.p.ID, SolutionType: dto.SolutionTypeFix, SolutionDescription: "Fix pool release", ProposedBy: f.actor.ID, Priority: "high"}, f.tenant.ID)
	require.NoError(t, err)
	selectCmd = f.command("select_resolution", "select-fix")
	selectCmd.SolutionID = candidate.ID
	_, err = f.owner.ApplyCommand(f.ctx, selectCmd)
	require.NoError(t, err)
	edited := "Another candidate body"
	_, err = reader.UpdateProblemSolution(f.ctx, candidate.ID, &dto.UpdateProblemSolutionRequest{SolutionDescription: &edited}, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "Fix pool release", f.client.Problem.GetX(f.ctx, f.p.ID).Resolution, "selection is a final decision, not a synchronized copy")
}

func TestWorkItemProblemLifecycleAuditRollbackAndTenantDenials(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	foreign := f.client.Tenant.Create().SetCode("foreign-problem").SetName("foreign").SaveX(f.ctx)
	analyst := f.client.User.Create().SetTenantID(foreign.ID).SetUsername("foreign-analyst").SetName("foreign").SetEmail("foreign@example.test").SetPasswordHash("test").SetActive(true).SaveX(f.ctx)
	cmd := f.command("investigate", "foreign-analyst")
	cmd.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: analyst.ID}
	before := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	_, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Version)
	// Preserve the replaced SQL service's soft-delete-during-creation regression
	// at the new owning transaction boundary.
	_, err = f.db.ExecContext(f.ctx, `CREATE FUNCTION problem_creation_delete_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE tickets SET deleted_at=now() WHERE id=(SELECT work_item_id FROM problems WHERE id=NEW.problem_id); RETURN NEW; END $$; CREATE TRIGGER problem_creation_delete_probe AFTER INSERT ON problem_investigations FOR EACH ROW EXECUTE FUNCTION problem_creation_delete_probe()`)
	require.NoError(t, err)
	deletedDuringCreate := f.command("investigate", "deleted-during-create")
	deletedDuringCreate.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: f.actor.ID}
	_, err = f.owner.ApplyCommand(f.ctx, deletedDuringCreate)
	require.Error(t, err)
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Version)
	require.Nil(t, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).DeletedAt)
	_, err = f.db.ExecContext(f.ctx, `DROP TRIGGER problem_creation_delete_probe ON problem_investigations; DROP FUNCTION problem_creation_delete_probe()`)
	require.NoError(t, err)
	f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			return nil, errors.New("injected audit failure")
		})
	})
	cmd = f.command("investigate", "audit-failure")
	cmd.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: f.actor.ID}
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.ErrorContains(t, err, "injected audit failure")
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Version)
	var count int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM problem_investigations").Scan(&count))
	require.Zero(t, count)
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
	cmd = f.command("investigate", "foreign-actor")
	cmd.Meta.ActorID = analyst.ID
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
}

func TestWorkItemProblemLifecycleConcurrentRCAAndHTTP(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	f.apply(t, "investigate", "start")
	f.evidence(t)
	reader := service.NewProblemInvestigationService(f.db, zap.NewNop().Sugar())
	rca, err := reader.CreateRootCauseAnalysis(f.ctx, &dto.CreateRootCauseAnalysisRequest{ProblemID: f.p.ID, AnalystID: f.actor.ID, AnalysisMethod: "5_whys", RootCauseDescription: "Connection leak", ConfidenceLevel: dto.ConfidenceHigh}, f.tenant.ID)
	require.NoError(t, err)
	f.apply(t, "verify_resolution", "verify")
	cmd := f.command("resolve", "concurrent")
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	var commandErr, rcaErr error
	go func() { defer wg.Done(); <-start; _, commandErr = f.owner.ApplyCommand(f.ctx, cmd) }()
	go func() {
		defer wg.Done()
		<-start
		body := "RCA corrected concurrently"
		_, rcaErr = reader.UpdateRootCauseAnalysis(f.ctx, rca.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &body}, f.tenant.ID)
	}()
	close(start)
	wg.Wait()
	require.NoError(t, rcaErr)
	item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	if commandErr == nil {
		require.Equal(t, "resolved", item.Status)
	} else {
		require.Equal(t, "investigating", item.Status)
	}
	_, err = f.owner.ApplyCommand(f.ctx, f.command("close", "cannot-close-changed-evidence"))
	require.Error(t, err)
	// HTTP metadata is server-owned and version/operationId are mandatory.
	h := problem.NewHandler(f.owner, f.client)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Set("user_id", f.actor.ID)
	})
	router.POST("/problems/:id/verify-resolution", h.VerifyResolution)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", fmt.Sprintf("/problems/%d/verify-resolution", f.p.ID), strings.NewReader(`{"verified":true}`)))
	require.Equal(t, 400, w.Code)
	unauth := gin.New()
	unauth.Use(func(c *gin.Context) {
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
	})
	unauth.POST("/problems/:id/verify-resolution", h.VerifyResolution)
	w = httptest.NewRecorder()
	unauth.ServeHTTP(w, httptest.NewRequest("POST", fmt.Sprintf("/problems/%d/verify-resolution", f.p.ID), strings.NewReader(`{"version":1,"operationId":"no-actor","verified":true,"actorId":1}`)))
	require.Equal(t, 401, w.Code)
}

func TestWorkItemProblemLifecycleRLSAndReviewerScope(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	foreign := f.client.Tenant.Create().SetCode("rls-other").SetName("other").SaveX(f.ctx)
	reviewer := f.client.User.Create().SetTenantID(foreign.ID).SetUsername("other-reviewer").SetEmail("other-reviewer@example.test").SetName("reviewer").SetPasswordHash("test").SetActive(true).SaveX(f.ctx)
	reader := service.NewProblemInvestigationService(f.db, zap.NewNop().Sugar())
	rca, err := reader.CreateRootCauseAnalysis(f.ctx, &dto.CreateRootCauseAnalysisRequest{ProblemID: f.p.ID, AnalystID: f.actor.ID, AnalysisMethod: "5_whys", RootCauseDescription: "pool", ConfidenceLevel: dto.ConfidenceHigh}, f.tenant.ID)
	require.NoError(t, err)
	before := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	_, err = reader.UpdateRootCauseAnalysis(f.ctx, rca.ID, &dto.UpdateRootCauseAnalysisRequest{ReviewedBy: &reviewer.ID}, f.tenant.ID)
	require.Error(t, err)
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Version)
	_, err = reader.UpdateRootCauseAnalysis(f.ctx, rca.ID, &dto.UpdateRootCauseAnalysisRequest{ReviewedBy: &f.actor.ID}, f.tenant.ID)
	require.NoError(t, err)
	driver, db := runtimeRLSDriver(t, f.incidentEffectsFixture)
	var role, schema string
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT current_user,current_schema()").Scan(&role, &schema))
	require.True(t, strings.HasPrefix(role, "entry_rls_"))
	require.True(t, strings.HasPrefix(schema, "a4_incident_effects_"))
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	scoped := ent.NewClient(ent.Driver(driver))
	owner := problem.NewService(problem.NewEntRepository(scoped), zap.NewNop().Sugar())
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	cmd := f.command("investigate", "rls-create")
	cmd.Investigation = &dto.CreateProblemInvestigationRequest{ProblemID: f.p.ID, InvestigatorID: f.actor.ID}
	_, err = owner.ApplyCommand(ctx, cmd)
	require.NoError(t, err)
	var id int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT id FROM problem_investigations WHERE problem_id=$1", f.p.ID).Scan(&id))
	step, err := reader.CreateInvestigationStep(f.ctx, &dto.CreateInvestigationStepRequest{InvestigationID: id, StepNumber: 1, StepTitle: "Inspect", StepDescription: "Inspect pool", AssignedTo: &f.actor.ID}, f.tenant.ID)
	require.NoError(t, err)
	require.Positive(t, step.ID)
	_, err = reader.CreateInvestigationStep(f.ctx, &dto.CreateInvestigationStepRequest{InvestigationID: id, StepNumber: 2, StepTitle: "Denied", StepDescription: "Foreign assignee", AssignedTo: &reviewer.ID}, f.tenant.ID)
	require.Error(t, err)
	tx, err := db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	for _, table := range []string{"problem_investigations", "problem_investigation_steps", "problem_solutions"} {
		var enabled, forced bool
		require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid=$1::regclass", table).Scan(&enabled, &forced))
		require.True(t, enabled)
		require.True(t, forced)
	}
	_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", fmt.Sprint(f.tenant.ID))
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM problem_investigations").Scan(&count))
	require.Equal(t, 1, count)
	_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", fmt.Sprint(foreign.ID))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM problem_investigations").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM problem_investigation_steps").Scan(&count))
	require.Zero(t, count)
	_, err = tx.ExecContext(f.ctx, "INSERT INTO problem_investigations(problem_id,investigator_id) VALUES($1,$2)", f.p.ID, reviewer.ID)
	require.Error(t, err)
}
