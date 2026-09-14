package service_test

import (
	"context"
	"database/sql"
	"testing"

	"itsm-backend/dto"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestProblemInvestigationRejectsSoftDeletedWorkItemReadsAndMutations(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:problem-investigation-soft-delete?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
		CREATE TABLE tickets (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME);
		CREATE TABLE problems (id INTEGER PRIMARY KEY, work_item_id INTEGER NOT NULL);
		CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, name TEXT NOT NULL);
		CREATE TABLE problem_investigations (
			id INTEGER PRIMARY KEY, problem_id INTEGER NOT NULL, investigator_id INTEGER NOT NULL,
			status TEXT NOT NULL, start_date DATETIME, estimated_completion_date DATETIME,
			actual_completion_date DATETIME, investigation_summary TEXT, created_at DATETIME, updated_at DATETIME
		);
		CREATE TABLE problem_root_cause_analyses (id INTEGER PRIMARY KEY, problem_id INTEGER NOT NULL);
		CREATE TABLE problem_solutions (id INTEGER PRIMARY KEY, problem_id INTEGER NOT NULL);
		INSERT INTO tickets (id, tenant_id, deleted_at) VALUES (10, 101, CURRENT_TIMESTAMP);
		INSERT INTO problems (id, work_item_id) VALUES (20, 10);
		INSERT INTO users (id, tenant_id, name) VALUES (30, 101, 'investigator');
		INSERT INTO problem_investigations
			(id, problem_id, investigator_id, status, start_date, created_at, updated_at)
		VALUES (40, 20, 30, 'in_progress', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		INSERT INTO problem_root_cause_analyses (id, problem_id) VALUES (50, 20);
		INSERT INTO problem_solutions (id, problem_id) VALUES (60, 20);
	`)
	require.NoError(t, err)

	svc := service.NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar())
	_, err = svc.GetProblemInvestigation(context.Background(), 40, 101)
	require.ErrorContains(t, err, "问题调查不存在")

	rcaDB, rcaOwner := rcaAuthorityFixture(t)
	_, err = rcaDB.Exec(`INSERT INTO problem_root_cause_analyses(id,problem_id,analyst_id) VALUES(50,?,?)`, rcaOwner.deletedPID, rcaOwner.actor)
	require.NoError(t, err)
	require.Error(t, rcaOwner.mutate(context.Background(), rcaOwner.deletedPID, rcaOwner.tenant, &problemDomain.RootCauseMetadata{ID: 50, Delete: true}))
	var rcaRetained int
	require.NoError(t, rcaDB.QueryRow("SELECT count(*) FROM problem_root_cause_analyses WHERE id=50").Scan(&rcaRetained))
	require.Equal(t, 1, rcaRetained)
	_, err = rcaDB.Exec(`CREATE TABLE problem_investigations(id INTEGER PRIMARY KEY,problem_id INTEGER NOT NULL,status TEXT); CREATE TABLE problem_solutions(id INTEGER PRIMARY KEY,problem_id INTEGER NOT NULL);`)
	require.NoError(t, err)
	_, err = rcaDB.Exec("INSERT INTO problem_investigations(id,problem_id,status) VALUES(40,?,'in_progress')", rcaOwner.deletedPID)
	require.NoError(t, err)
	_, err = rcaDB.Exec("INSERT INTO problem_solutions(id,problem_id) VALUES(60,?)", rcaOwner.deletedPID)
	require.NoError(t, err)
	status := dto.InvestigationStatusCompleted
	for _, e := range []*problemDomain.EvidenceMetadata{{ID: 40, Investigation: &dto.UpdateProblemInvestigationRequest{Status: &status}}, {ID: 60, DeleteSolution: true}} {
		_, err = rcaOwner.owner.ApplyMetadata(context.Background(), problemDomain.MetadataCommand{Meta: workitemmutation.Meta{TenantID: rcaOwner.tenant, ActorID: rcaOwner.actor, ExpectedVersion: 1, Source: "test", OperationID: "deleted-evidence"}, ProblemID: rcaOwner.deletedPID, Evidence: e})
		require.Error(t, err)
	}
	var storedStatus string
	require.NoError(t, rcaDB.QueryRow("SELECT status FROM problem_investigations WHERE id=40").Scan(&storedStatus))
	require.Equal(t, "in_progress", storedStatus)
	var retainedCount int
	require.NoError(t, rcaDB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM problem_root_cause_analyses WHERE id = 50) +
		(SELECT COUNT(*) FROM problem_solutions WHERE id = 60)`).Scan(&retainedCount))
	require.Equal(t, 2, retainedCount, "soft-deleted WorkItem must prevent both deletes")
}
