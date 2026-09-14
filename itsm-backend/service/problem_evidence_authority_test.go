package service_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	problem "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestProblemEvidenceMutationAuthorityReplayAndRollback(t *testing.T) {
	db, f := rcaAuthorityFixture(t)
	_, err := db.Exec(`CREATE TABLE problem_investigations(id INTEGER PRIMARY KEY,problem_id INTEGER NOT NULL,status TEXT,investigation_summary TEXT,updated_at DATETIME);
 CREATE TABLE problem_investigation_steps(id INTEGER PRIMARY KEY,investigation_id INTEGER NOT NULL,step_number INTEGER,step_title TEXT,step_description TEXT,status TEXT,assigned_to INTEGER,notes TEXT,start_date DATETIME,completion_date DATETIME,created_at DATETIME,updated_at DATETIME);
 CREATE TABLE problem_solutions(id INTEGER PRIMARY KEY,problem_id INTEGER NOT NULL,solution_type TEXT,solution_description TEXT,proposed_by INTEGER,proposed_date DATETIME,status TEXT,priority TEXT,estimated_effort_hours INTEGER,estimated_cost REAL,risk_assessment TEXT,approval_status TEXT,created_at DATETIME,updated_at DATETIME);`)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO problem_investigations(id,problem_id,status) VALUES(40,?,'in_progress')", f.pid)
	require.NoError(t, err)
	ctx := context.Background()
	f.client.Problem.UpdateOneID(f.pid).SetResolution("selected fix").SetVerificationDigest("original digest").SetVerificationNote("preserved").ExecX(ctx)
	meta := workitemmutation.Meta{TenantID: f.tenant, ActorID: f.actor, ExpectedVersion: 1, Source: "http", OperationID: "step-create"}
	cmd := problem.MetadataCommand{Meta: meta, ProblemID: f.pid, Evidence: &problem.EvidenceMetadata{CreateStep: &dto.CreateInvestigationStepRequest{ProblemID: f.pid, InvestigationID: 40, StepNumber: 1, StepTitle: "Inspect", StepDescription: "Inspect pool", AssignedTo: &f.actor}}}
	missing := cmd
	missing.Meta.ActorID = 0
	_, err = f.owner.ApplyMetadata(ctx, missing)
	require.Error(t, err)
	result, err := f.owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, 2, result.Version)
	replay, err := f.owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM problem_investigation_steps").Scan(&count))
	require.Equal(t, 1, count)
	cmd.Meta.ExpectedVersion = result.Version
	cmd.Meta.OperationID = "foreign-step"
	cmd.Evidence.CreateStep.AssignedTo = &f.foreignActor
	_, err = f.owner.ApplyMetadata(ctx, cmd)
	require.Error(t, err)
	require.Equal(t, result.Version, f.client.Ticket.GetX(ctx, f.itemID).Version)
	cmd.Evidence = &problem.EvidenceMetadata{CreateSolution: &dto.CreateProblemSolutionRequest{ProblemID: f.pid, ProposedBy: f.actor, SolutionType: dto.SolutionTypeFix, SolutionDescription: "candidate", Priority: "high"}}
	cmd.Meta.OperationID = "candidate"
	result, err = f.owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	after := f.client.Problem.GetX(ctx, f.pid)
	require.Equal(t, "selected fix", after.Resolution)
	require.Equal(t, "original digest", after.VerificationDigest)
	require.Equal(t, "preserved", after.VerificationNote)
	var solutionID int
	require.NoError(t, db.QueryRow("SELECT id FROM problem_solutions WHERE problem_id=?", f.pid).Scan(&solutionID))
	cmd.Meta.ExpectedVersion = result.Version
	cmd.Meta.OperationID = "delete-candidate"
	cmd.Evidence = &problem.EvidenceMetadata{ID: solutionID, DeleteSolution: true}
	result, err = f.owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	replay, err = f.owner.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.NoError(t, db.QueryRow("SELECT count(*) FROM problem_solutions").Scan(&count))
	require.Zero(t, count)
	cmd.Meta.ExpectedVersion = result.Version
	cmd.Meta.OperationID = "rollback-evidence"
	cmd.Evidence = &problem.EvidenceMetadata{ID: 40, Investigation: &dto.UpdateProblemInvestigationRequest{InvestigationSummary: ptrEvidence("must rollback")}}
	_, err = db.Exec(`CREATE TRIGGER evidence_rollback BEFORE UPDATE ON problem_investigations BEGIN SELECT RAISE(ABORT,'forced evidence failure'); END`)
	require.NoError(t, err)
	_, err = f.owner.ApplyMetadata(ctx, cmd)
	require.Error(t, err)
	require.Equal(t, result.Version, f.client.Ticket.GetX(ctx, f.itemID).Version)
}
func ptrEvidence(s string) *string { return &s }
