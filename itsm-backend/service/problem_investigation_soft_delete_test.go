package service

import (
	"context"
	"database/sql"
	"testing"

	"itsm-backend/dto"

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

	svc := NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar())
	_, err = svc.GetProblemInvestigation(context.Background(), 40, 101)
	require.ErrorContains(t, err, "问题调查不存在")

	status := dto.InvestigationStatusCompleted
	_, err = svc.UpdateProblemInvestigation(context.Background(), 40, &dto.UpdateProblemInvestigationRequest{Status: &status}, 101)
	require.ErrorContains(t, err, "问题调查不存在")

	var storedStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM problem_investigations WHERE id = 40`).Scan(&storedStatus))
	require.Equal(t, "in_progress", storedStatus)

	require.ErrorContains(t, svc.DeleteRootCauseAnalysis(context.Background(), 50, 101), "根因分析不存在")
	require.ErrorContains(t, svc.DeleteProblemSolution(context.Background(), 60, 101), "解决方案不存在")
	var retainedCount int
	require.NoError(t, db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM problem_root_cause_analyses WHERE id = 50) +
		(SELECT COUNT(*) FROM problem_solutions WHERE id = 60)`).Scan(&retainedCount))
	require.Equal(t, 2, retainedCount, "soft-deleted WorkItem must prevent both deletes")
}

func TestCreateProblemInvestigationRollsBackWhenWorkItemIsSoftDeletedDuringMutation(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:problem-investigation-create-race?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
		CREATE TABLE tickets (
			id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, title TEXT NOT NULL,
			status TEXT NOT NULL, updated_at DATETIME, deleted_at DATETIME
		);
		CREATE TABLE problems (id INTEGER PRIMARY KEY, work_item_id INTEGER NOT NULL);
		CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, name TEXT NOT NULL);
		CREATE TABLE problem_investigations (
			id INTEGER PRIMARY KEY, problem_id INTEGER NOT NULL, investigator_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'in_progress', start_date DATETIME DEFAULT CURRENT_TIMESTAMP,
			estimated_completion_date DATETIME, actual_completion_date DATETIME,
			investigation_summary TEXT, created_at DATETIME, updated_at DATETIME
		);
		INSERT INTO tickets (id, tenant_id, title, status) VALUES (10, 101, 'problem', 'open');
		INSERT INTO problems (id, work_item_id) VALUES (20, 10);
		INSERT INTO users (id, tenant_id, name) VALUES (30, 101, 'investigator');
		CREATE TRIGGER soft_delete_work_item_after_investigation
		AFTER INSERT ON problem_investigations
		BEGIN
			UPDATE tickets SET deleted_at = CURRENT_TIMESTAMP WHERE id = 10;
		END;
	`)
	require.NoError(t, err)

	svc := NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar())
	response, err := svc.CreateProblemInvestigation(context.Background(), &dto.CreateProblemInvestigationRequest{
		ProblemID:            20,
		InvestigatorID:       30,
		InvestigationSummary: "race probe",
	}, 101)
	require.Error(t, err)
	require.Nil(t, response)

	var investigationCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM problem_investigations`).Scan(&investigationCount))
	require.Zero(t, investigationCount, "failed mutation must not leave an investigation row")
}
