//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	problem "itsm-backend/handlers/problem"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"sync"
	"testing"
)

func TestWorkItemAssignmentProblemAtomicReceipt(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	next := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("problem-next").SetEmail("problem-next@test.local").SetName("next").SetPasswordHash("test").SetActive(true).SaveX(f.ctx)
	before := f.client.Ticket.UpdateOneID(f.p.WorkItemID).SetAssigneeID(f.actor.ID).SaveX(f.ctx)
	cmd := problem.MetadataCommand{Meta: f.command("metadata", "assignment-once").Meta, ProblemID: f.p.ID, Patch: dto.UpdateProblemRequest{AssigneeID: &next.ID, AssignmentReason: "application ownership"}}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; _, errs[i] = f.owner.ApplyMetadata(f.ctx, cmd) }(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, before.Version+1, f.client.Ticket.GetX(f.ctx, before.ID).Version)
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
	cmd.Patch.AssignmentReason = "different intent"
	_, err := f.owner.ApplyMetadata(f.ctx, cmd)
	require.Error(t, err)
	cmd.Patch.AssignmentReason = "application ownership"
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	_, err = f.owner.ApplyMetadata(f.ctx, cmd)
	require.Error(t, err)
}

func TestWorkItemAssignmentProblemAuditRollback(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	next := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("rollback-next").SetEmail("rollback-next@test.local").SetName("next").SetPasswordHash("test").SetActive(true).SaveX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
	f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			return nil, errors.New("injected Problem metadata audit failure")
		})
	})
	_, err := f.owner.ApplyMetadata(f.ctx, problem.MetadataCommand{Meta: f.command("metadata", "audit-failure").Meta, ProblemID: f.p.ID, Patch: dto.UpdateProblemRequest{AssigneeID: &next.ID}})
	require.ErrorContains(t, err, "injected Problem metadata audit failure")
	after := f.client.Ticket.GetX(f.ctx, before.ID)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.AssigneeID, after.AssigneeID)
}

func TestWorkItemAssignmentProblemRuntimeRLS(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	target := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("rls-problem-target").SetEmail("rls-problem-target@test.local").SetName("target").SetPasswordHash("test").SetActive(true).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f.incidentEffectsFixture)
	for _, table := range []string{"problems", "work_item_relations"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
	}
	owner := problem.NewService(problem.NewEntRepository(clients.Tenant), zap.NewNop().Sugar(), executionfixture.Standard())
	owner.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	cmd := problem.MetadataCommand{Meta: f.command("metadata", "runtime-assignment").Meta, ProblemID: f.p.ID, Patch: dto.UpdateProblemRequest{AssigneeID: &target.ID}}
	_, err := owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, target.ID, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).AssigneeID)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	_, err = owner.ApplyMetadata(ctx, cmd)
	require.Error(t, err)
}

func TestWorkItemAssignmentProblemEvidenceAtomicityAndReplay(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	cmd := problem.MetadataCommand{Meta: f.command("metadata", "candidate-create").Meta, ProblemID: f.p.ID, Evidence: &problem.EvidenceMetadata{CreateSolution: &dto.CreateProblemSolutionRequest{ProblemID: f.p.ID, ProposedBy: f.actor.ID, SolutionType: dto.SolutionTypeFix, SolutionDescription: "candidate only", Priority: "high"}}}
	result, err := f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	replay, err := f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	var count, id int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*),max(id) FROM problem_solutions WHERE problem_id=$1", f.p.ID).Scan(&count, &id))
	require.Equal(t, 1, count)
	cmd.Meta.ExpectedVersion = result.Version
	cmd.Meta.OperationID = "candidate-delete"
	cmd.Evidence = &problem.EvidenceMetadata{ID: id, DeleteSolution: true}
	result, err = f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	replay, err = f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	cmd.Meta.ExpectedVersion = result.Version
	cmd.Meta.OperationID = "candidate-audit-failure"
	cmd.Evidence = &problem.EvidenceMetadata{CreateSolution: &dto.CreateProblemSolutionRequest{ProblemID: f.p.ID, ProposedBy: f.actor.ID, SolutionType: dto.SolutionTypeFix, SolutionDescription: "rollback", Priority: "high"}}
	_, err = f.db.ExecContext(f.ctx, `CREATE FUNCTION problem_candidate_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced audit failure'; END $$; CREATE TRIGGER problem_candidate_audit_failure BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION problem_candidate_audit_failure()`)
	require.NoError(t, err)
	_, err = f.owner.ApplyMetadata(f.ctx, cmd)
	require.Error(t, err)
	require.Equal(t, result.Version, f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Version)
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM problem_solutions WHERE problem_id=$1", f.p.ID).Scan(&count))
	require.Zero(t, count)
}
