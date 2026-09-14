package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/dto"
)

func rcaAuthorityFixture(t *testing.T) (*sql.DB, *ProblemInvestigationService) {
	t.Helper()
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
 CREATE TABLE tickets (id INTEGER PRIMARY KEY, tenant_id INTEGER, title TEXT, deleted_at DATETIME, version INTEGER DEFAULT 1, updated_at DATETIME);
 CREATE TABLE problems (id INTEGER PRIMARY KEY, work_item_id INTEGER, root_cause TEXT);
 CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, name TEXT);
 CREATE TABLE problem_root_cause_analyses (
 id INTEGER PRIMARY KEY, problem_id INTEGER NOT NULL UNIQUE, analyst_id INTEGER NOT NULL,
 analysis_method TEXT, contributing_factors TEXT, evidence TEXT, confidence_level TEXT,
 analysis_date DATETIME, reviewed_by INTEGER, review_date DATETIME, created_at DATETIME, updated_at DATETIME);
 INSERT INTO tickets(id, tenant_id, title, deleted_at) VALUES (1, 10, 'source', NULL), (2, 20, 'foreign', NULL), (3, 10, 'deleted', CURRENT_TIMESTAMP);
 INSERT INTO problems VALUES (1, 1, 'original'), (2, 2, 'foreign original'), (3, 3, 'deleted original');
 INSERT INTO users VALUES (1, 10, 'analyst'), (2, 20, 'foreign analyst');
 `)
	require.NoError(t, err)
	return db, NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar())
}

func rcaRequest(problemID, analystID int) *dto.CreateRootCauseAnalysisRequest {
	return &dto.CreateRootCauseAnalysisRequest{ProblemID: problemID, AnalystID: analystID, AnalysisMethod: "5_whys", RootCauseDescription: "new cause", ConfidenceLevel: dto.ConfidenceHigh}
}

func TestRCACreateRollsBackMetadataFailure(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	_, err := db.Exec(`CREATE TRIGGER reject_metadata BEFORE INSERT ON problem_root_cause_analyses BEGIN SELECT RAISE(ABORT, 'metadata failed'); END`)
	require.NoError(t, err)
	result, err := svc.CreateRootCauseAnalysis(context.Background(), rcaRequest(1, 1), 10)
	require.Error(t, err)
	require.Nil(t, result)
	var root string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&root))
	require.Equal(t, "original", root)
}

func TestRCAUpdateRollsBackMetadataAndRootTogether(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	created, err := svc.CreateRootCauseAnalysis(context.Background(), rcaRequest(1, 1), 10)
	require.NoError(t, err)
	// A deletion during metadata mutation makes the subsequent authority update fail closed.
	_, err = db.Exec(`CREATE TRIGGER delete_during_update AFTER UPDATE ON problem_root_cause_analyses BEGIN UPDATE tickets SET deleted_at=CURRENT_TIMESTAMP WHERE id=1; END`)
	require.NoError(t, err)
	root, evidence := "revised", "new evidence"
	result, err := svc.UpdateRootCauseAnalysis(context.Background(), created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root, Evidence: &evidence}, 10)
	require.Error(t, err)
	require.Nil(t, result)
	var storedRoot, storedEvidence string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&storedRoot))
	require.Equal(t, "new cause", storedRoot)
	require.NoError(t, db.QueryRow(`SELECT evidence FROM problem_root_cause_analyses WHERE id=?`, created.ID).Scan(&storedEvidence))
	require.Empty(t, storedEvidence)
	var deleted sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT deleted_at FROM tickets WHERE id=1`).Scan(&deleted))
	require.False(t, deleted.Valid)
}

func TestRCAAuthorityTenantAndMetadataIsolation(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	ctx := context.Background()
	for _, req := range []*dto.CreateRootCauseAnalysisRequest{rcaRequest(2, 1), rcaRequest(3, 1), rcaRequest(1, 2)} {
		result, err := svc.CreateRootCauseAnalysis(ctx, req, 10)
		require.Error(t, err)
		require.Nil(t, result)
	}
	created, err := svc.CreateRootCauseAnalysis(ctx, rcaRequest(1, 1), 10)
	require.NoError(t, err)
	root := "unauthorized overwrite"
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}, 20)
	require.Error(t, err)
	reviewer := 2
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{ReviewedBy: &reviewer, RootCauseDescription: &root}, 10)
	require.Error(t, err)
	_, err = svc.GetRootCauseAnalysis(ctx, created.ID, 20)
	require.Error(t, err)
	evidence := "metadata only"
	updated, err := svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{Evidence: &evidence}, 10)
	require.NoError(t, err)
	require.Equal(t, "new cause", updated.RootCauseDescription)
	require.Equal(t, evidence, *updated.Evidence)
	_, err = svc.CreateRootCauseAnalysis(ctx, rcaRequest(1, 1), 10)
	require.Error(t, err)
	require.NoError(t, svc.DeleteRootCauseAnalysis(ctx, created.ID, 10))
	var rootAfterDelete string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&rootAfterDelete))
	require.Equal(t, "new cause", rootAfterDelete, "deleting analysis metadata must not erase the Problem root cause")
}

func TestRCAMutationsAdvanceWorkItemVersion(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	ctx := context.Background()
	created, err := svc.CreateRootCauseAnalysis(ctx, rcaRequest(1, 1), 10)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.QueryRow(`SELECT version FROM tickets WHERE id=1`).Scan(&version))
	require.Equal(t, 2, version)
	evidence := "verified evidence"
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{Evidence: &evidence}, 10)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT version FROM tickets WHERE id=1`).Scan(&version))
	require.Equal(t, 3, version)
	var updated sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM tickets WHERE id=1`).Scan(&updated))
	require.True(t, updated.Valid)
}
