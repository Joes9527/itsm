package service_test

import (
	"context"
	"database/sql"
	"fmt"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"time"
)

type rcaCommandFixture struct {
	*service.ProblemInvestigationService
	owner                                                                           *problemDomain.Service
	client                                                                          *ent.Client
	db                                                                              *sql.DB
	tenant, foreignTenant, actor, foreignActor, pid, foreignPID, deletedPID, itemID int
}

func rcaAuthorityFixture(t *testing.T) (*sql.DB, *rcaCommandFixture) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { client.Close() })
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("owner").SetName("owner").SaveX(ctx)
	foreign := client.Tenant.Create().SetCode("foreign").SetName("foreign").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("actor").SetEmail("actor@test.local").SetPasswordHash("test").SetName("analyst").SetActive(true).SetRole("super_admin").SaveX(ctx)
	outsider := client.User.Create().SetTenantID(foreign.ID).SetUsername("outsider").SetEmail("outsider@test.local").SetPasswordHash("test").SetName("outsider").SetActive(true).SetRole("super_admin").SaveX(ctx)
	client.Ticket.Create().SetTicketNumber("dummy").SetTitle("dummy").SetStatus("open").SetPriority("high").SetTenantID(tenant.ID).SetRequesterID(actor.ID).SaveX(ctx)
	records := []*ent.Problem{}
	for i, tid := range []int{tenant.ID, foreign.ID, tenant.ID} {
		requester := actor.ID
		if tid == foreign.ID {
			requester = outsider.ID
		}
		w := client.Ticket.Create().SetTicketNumber(fmt.Sprint(i)).SetTitle("source").SetStatus("investigating").SetPriority("high").SetRecordClass("problem").SetTenantID(tid).SetRequesterID(requester).SetOpenedByID(requester).SaveX(ctx)
		records = append(records, client.Problem.Create().SetWorkItemID(w.ID).SetRootCause("original").SaveX(ctx))
	}
	client.Ticket.UpdateOneID(records[2].WorkItemID).SetDeletedAt(time.Now()).ExecX(ctx)

	_, err = db.Exec(`CREATE TABLE problem_root_cause_analyses (id INTEGER PRIMARY KEY,problem_id INTEGER NOT NULL UNIQUE,analyst_id INTEGER NOT NULL,analysis_method TEXT,contributing_factors TEXT,evidence TEXT,confidence_level TEXT,analysis_date DATETIME,reviewed_by INTEGER,review_date DATETIME,created_at DATETIME,updated_at DATETIME)`)
	require.NoError(t, err)
	return db, &rcaCommandFixture{ProblemInvestigationService: service.NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar()), owner: problemDomain.NewService(problemDomain.NewEntRepository(client), zaptest.NewLogger(t).Sugar(), executionfixture.Standard()), client: client, db: db, tenant: tenant.ID, foreignTenant: foreign.ID, actor: actor.ID, foreignActor: outsider.ID, pid: records[0].ID, foreignPID: records[1].ID, deletedPID: records[2].ID, itemID: records[0].WorkItemID}
}
func (s *rcaCommandFixture) mutate(ctx context.Context, pid, tenant int, rca *problemDomain.RootCauseMetadata) error {
	p, err := s.client.Problem.Get(ctx, pid)
	if err != nil {
		return err
	}
	item, err := s.client.Ticket.Get(ctx, p.WorkItemID)
	if err != nil {
		return err
	}
	_, err = s.owner.ApplyMetadata(ctx, problemDomain.MetadataCommand{Meta: workitemmutation.Meta{TenantID: tenant, ActorID: s.actor, ExpectedVersion: item.Version, Source: "http", OperationID: uuid.NewString()}, ProblemID: pid, RootCauseAnalysis: rca})
	return err
}
func (s *rcaCommandFixture) CreateRootCauseAnalysis(ctx context.Context, req *dto.CreateRootCauseAnalysisRequest, tenant int) (*dto.RootCauseAnalysisResponse, error) {
	if err := s.mutate(ctx, req.ProblemID, tenant, &problemDomain.RootCauseMetadata{Create: req}); err != nil {
		return nil, err
	}
	var id int
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM problem_root_cause_analyses WHERE problem_id=$1", req.ProblemID).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetRootCauseAnalysis(ctx, id, tenant)
}
func (s *rcaCommandFixture) UpdateRootCauseAnalysis(ctx context.Context, id int, req *dto.UpdateRootCauseAnalysisRequest, tenant int) (*dto.RootCauseAnalysisResponse, error) {
	old, err := s.GetRootCauseAnalysis(ctx, id, tenant)
	if err != nil {
		return nil, err
	}
	if err = s.mutate(ctx, old.ProblemID, tenant, &problemDomain.RootCauseMetadata{ID: id, Update: req}); err != nil {
		return nil, err
	}
	return s.GetRootCauseAnalysis(ctx, id, tenant)
}
func (s *rcaCommandFixture) DeleteRootCauseAnalysis(ctx context.Context, id, tenant int) error {
	old, err := s.GetRootCauseAnalysis(ctx, id, tenant)
	if err != nil {
		return err
	}
	return s.mutate(ctx, old.ProblemID, tenant, &problemDomain.RootCauseMetadata{ID: id, Delete: true})
}

func rcaRequest(problemID, analystID int) *dto.CreateRootCauseAnalysisRequest {
	return &dto.CreateRootCauseAnalysisRequest{ProblemID: problemID, AnalystID: analystID, AnalysisMethod: "5_whys", RootCauseDescription: "new cause", ConfidenceLevel: dto.ConfidenceHigh}
}

func TestRCACreateRollsBackMetadataFailure(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	_, err := db.Exec(`CREATE TRIGGER reject_metadata BEFORE INSERT ON problem_root_cause_analyses BEGIN SELECT RAISE(ABORT, 'metadata failed'); END`)
	require.NoError(t, err)
	result, err := svc.CreateRootCauseAnalysis(context.Background(), rcaRequest(svc.pid, svc.actor), svc.tenant)
	require.Error(t, err)
	require.Nil(t, result)
	var root string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=?`, svc.pid).Scan(&root))
	require.Equal(t, "original", root)
}

func TestRCAUpdateRollsBackMetadataAndRootTogether(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	created, err := svc.CreateRootCauseAnalysis(context.Background(), rcaRequest(svc.pid, svc.actor), svc.tenant)
	require.NoError(t, err)
	// A deletion during metadata mutation makes the subsequent authority update fail closed.
	_, err = db.Exec(fmt.Sprintf(`CREATE TRIGGER delete_during_update AFTER UPDATE ON problem_root_cause_analyses BEGIN UPDATE tickets SET deleted_at=CURRENT_TIMESTAMP WHERE id=%d; END`, svc.itemID))
	require.NoError(t, err)
	root, evidence := "revised", "new evidence"
	result, err := svc.UpdateRootCauseAnalysis(context.Background(), created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root, Evidence: &evidence}, svc.tenant)
	require.Error(t, err)
	require.Nil(t, result)
	var storedRoot, storedEvidence string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=?`, svc.pid).Scan(&storedRoot))
	require.Equal(t, "new cause", storedRoot)
	require.NoError(t, db.QueryRow(`SELECT evidence FROM problem_root_cause_analyses WHERE id=?`, created.ID).Scan(&storedEvidence))
	require.Empty(t, storedEvidence)
	var deleted sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT deleted_at FROM tickets WHERE id=?`, svc.itemID).Scan(&deleted))
	require.False(t, deleted.Valid)
}

func TestRCAAuthorityTenantAndMetadataIsolation(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	ctx := context.Background()
	for _, req := range []*dto.CreateRootCauseAnalysisRequest{rcaRequest(svc.foreignPID, svc.actor), rcaRequest(svc.deletedPID, svc.actor), rcaRequest(svc.pid, svc.foreignActor)} {
		result, err := svc.CreateRootCauseAnalysis(ctx, req, svc.tenant)
		require.Error(t, err)
		require.Nil(t, result)
	}
	created, err := svc.CreateRootCauseAnalysis(ctx, rcaRequest(svc.pid, svc.actor), svc.tenant)
	require.NoError(t, err)
	root := "unauthorized overwrite"
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}, svc.foreignTenant)
	require.Error(t, err)
	reviewer := svc.foreignActor
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{ReviewedBy: &reviewer, RootCauseDescription: &root}, svc.tenant)
	require.Error(t, err)
	_, err = svc.GetRootCauseAnalysis(ctx, created.ID, svc.foreignTenant)
	require.Error(t, err)
	evidence := "metadata only"
	updated, err := svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{Evidence: &evidence}, svc.tenant)
	require.NoError(t, err)
	require.Equal(t, "new cause", updated.RootCauseDescription)
	require.Equal(t, evidence, *updated.Evidence)
	_, err = svc.CreateRootCauseAnalysis(ctx, rcaRequest(svc.pid, svc.actor), svc.tenant)
	require.Error(t, err)
	require.NoError(t, svc.DeleteRootCauseAnalysis(ctx, created.ID, svc.tenant))
	var rootAfterDelete string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=?`, svc.pid).Scan(&rootAfterDelete))
	require.Equal(t, "new cause", rootAfterDelete, "deleting analysis metadata must not erase the Problem root cause")
}

func TestRCAMutationsAdvanceWorkItemVersion(t *testing.T) {
	db, svc := rcaAuthorityFixture(t)
	ctx := context.Background()
	created, err := svc.CreateRootCauseAnalysis(ctx, rcaRequest(svc.pid, svc.actor), svc.tenant)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.QueryRow(`SELECT version FROM tickets WHERE id=?`, svc.itemID).Scan(&version))
	require.Equal(t, 2, version)
	evidence := "verified evidence"
	_, err = svc.UpdateRootCauseAnalysis(ctx, created.ID, &dto.UpdateRootCauseAnalysisRequest{Evidence: &evidence}, svc.tenant)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT version FROM tickets WHERE id=?`, svc.itemID).Scan(&version))
	require.Equal(t, 3, version)
	var updated sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM tickets WHERE id=?`, svc.itemID).Scan(&updated))
	require.True(t, updated.Valid)
}
