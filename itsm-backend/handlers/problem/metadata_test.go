package problem_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func TestProblemMetadataRequiresActor(t *testing.T) {
	client, svc, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "metadata-actor")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "metadata-actor")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	title := "Changed without actor"
	_, err := svc.ApplyMetadata(ctx, problemDomain.MetadataCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ExpectedVersion: p.Version, Source: "http", OperationID: "no-actor"}, ProblemID: p.ID, Patch: dto.UpdateProblemRequest{Title: &title}})
	require.Error(t, err, "metadata without trusted actor must be rejected")
}

func TestProblemMetadataPreservesVerifiedHandover(t *testing.T) {
	client, svc, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "handover")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "handover")
	next := createProblemHandlerUser(t, ctx, client, tenant.ID, "next")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	item := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("investigating").SetAssigneeID(actor.ID).SaveX(ctx)
	client.Problem.UpdateOneID(p.ID).SetRootCause("leak").SetResolution("patch").ExecX(ctx)
	meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "verify"}
	verified, err := svc.ApplyCommand(ctx, problemDomain.Command{Meta: meta, ProblemID: p.ID, Action: "verify_resolution", VerificationNote: "regression passed"})
	require.NoError(t, err)
	evidence := client.Problem.GetX(ctx, p.ID)
	meta.ExpectedVersion = verified.Version
	meta.OperationID = "handover"
	cmd := problemDomain.MetadataCommand{Meta: meta, ProblemID: p.ID, Patch: dto.UpdateProblemRequest{AssigneeID: &next.ID}}
	_, err = svc.ApplyMetadata(ctx, cmd)
	require.Error(t, err)
	cmd.Patch.AssignmentReason = "application team ownership"
	assigned, err := svc.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	replay, err := svc.ApplyMetadata(ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	after := client.Problem.GetX(ctx, p.ID)
	require.Equal(t, evidence.VerifiedVersion, after.VerifiedVersion)
	require.Equal(t, evidence.VerifiedAt, after.VerifiedAt)
	require.Equal(t, evidence.VerifiedBy, after.VerifiedBy)
	projection, err := svc.Get(ctx, p.ID, meta)
	require.NoError(t, err)
	require.True(t, problemDomain.BuildProblemActions(service.ActionActor{Client: client, Role: actor.Role, TenantID: tenant.ID}, projection)["resolve"].Allowed)
	meta.ExpectedVersion = assigned.Version
	meta.OperationID = "resolve"
	resolved, err := svc.ApplyCommand(ctx, problemDomain.Command{Meta: meta, ProblemID: p.ID, Action: "resolve"})
	require.NoError(t, err)
	projection, err = svc.Get(ctx, p.ID, meta)
	require.NoError(t, err)
	require.True(t, problemDomain.BuildProblemActions(service.ActionActor{Client: client, Role: actor.Role, TenantID: tenant.ID}, projection)["close"].Allowed)
	meta.ExpectedVersion = resolved.Version
	meta.OperationID = "close"
	_, err = svc.ApplyCommand(ctx, problemDomain.Command{Meta: meta, ProblemID: p.ID, Action: "close"})
	require.NoError(t, err)
}

func TestProblemMetadataChangedContentCannotResurrectVerification(t *testing.T) {
	client, svc, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "content")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "content")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	item := client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("investigating").SaveX(ctx)
	client.Problem.UpdateOneID(p.ID).SetRootCause("A").SetResolution("patch").ExecX(ctx)
	meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "verify"}
	result, err := svc.ApplyCommand(ctx, problemDomain.Command{Meta: meta, ProblemID: p.ID, Action: "verify_resolution", VerificationNote: "passed"})
	require.NoError(t, err)
	for _, root := range []string{"B", "A"} {
		meta.ExpectedVersion = result.Version
		meta.OperationID = "root-" + root
		result, err = svc.ApplyMetadata(ctx, problemDomain.MetadataCommand{Meta: meta, ProblemID: p.ID, Patch: dto.UpdateProblemRequest{RootCause: &root}})
		require.NoError(t, err)
	}
	meta.ExpectedVersion = result.Version
	meta.OperationID = "resolve"
	_, err = svc.ApplyCommand(ctx, problemDomain.Command{Meta: meta, ProblemID: p.ID, Action: "resolve"})
	require.Error(t, err)
	require.Zero(t, client.Problem.GetX(ctx, p.ID).VerifiedVersion)
}

func TestProblemMetadataSolutionHTTPPresence(t *testing.T) {
	router, _, svc, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, "solution-presence")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "solution-presence")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	client.Problem.UpdateOneID(p.ID).SetRootCause("root").SetResolution("original").SetVerifiedVersion(p.Version).ExecX(ctx)
	path := fmt.Sprintf("/api/v1/problems/%d/solution", p.ID)
	for i, body := range []map[string]any{{"workaround": "restart"}, {"resolution": "", "solution": "must not fallback"}, {"solution": "legacy permanent"}, {}} {
		body["version"] = client.Ticket.GetX(ctx, *p.WorkItemID).Version
		body["operationId"] = fmt.Sprintf("presence-%d", i)
		response := performProblemRequest(router, "PUT", path, body, tenant.ID, actor.ID)
		if i == 3 {
			require.Equal(t, 400, response.Code)
			continue
		}
		require.Equal(t, 200, response.Code, response.Body.String())
		stored := client.Problem.GetX(ctx, p.ID)
		switch i {
		case 0:
			require.Equal(t, "original", stored.Resolution)
			require.Equal(t, p.Version, stored.VerifiedVersion)
		case 1:
			require.Empty(t, stored.Resolution)
			require.Zero(t, stored.VerifiedVersion)
		case 2:
			require.Equal(t, "legacy permanent", stored.Resolution)
		}
	}
}

func TestProblemMetadataAssignmentGuardAndRollback(t *testing.T) {
	client, svc, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "guards")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "guards")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	client.Ticket.UpdateOneID(*p.WorkItemID).SetAssigneeID(actor.ID).ExecX(ctx)
	projection, err := svc.Get(ctx, p.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID})
	require.NoError(t, err)
	action := problemDomain.CanAssignProblem(service.ActionActor{Client: client, Role: actor.Role, TenantID: tenant.ID}, projection)
	require.False(t, action.Allowed)
	next := createProblemHandlerUser(t, ctx, client, tenant.ID, "guard-next")
	meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: p.Version, Source: "http", OperationID: "guards"}
	cmd := problemDomain.MetadataCommand{Meta: meta, ProblemID: p.ID, Patch: dto.UpdateProblemRequest{AssigneeID: &next.ID, AssignmentReason: "handover"}}
	client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("closed").ExecX(ctx)
	_, err = svc.ApplyMetadata(ctx, cmd)
	require.Error(t, err)
	client.Ticket.UpdateOneID(*p.WorkItemID).SetStatus("open").ExecX(ctx)
	client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) { return nil, errors.New("audit refused") })
	})
	_, err = svc.ApplyMetadata(ctx, cmd)
	require.ErrorContains(t, err, "audit refused")
	after := client.Ticket.GetX(ctx, *p.WorkItemID)
	require.Equal(t, p.Version, after.Version)
	require.Equal(t, actor.ID, after.AssigneeID)
}

func TestProblemRootCauseHTTPRejectsWhitespace(t *testing.T) {
	router, _, svc, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, "root-space")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "root-space")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	before := client.Problem.UpdateOneID(p.ID).SetRootCause("established cause").SetVerificationNote("evidence").SaveX(ctx)
	w := performProblemRequest(router, "PUT", fmt.Sprintf("/api/v1/problems/%d/root-cause", p.ID), map[string]any{"rootCause": "   ", "version": p.Version, "operationId": "space-root"}, tenant.ID, actor.ID)
	require.Equal(t, 400, w.Code, w.Body.String())
	after := client.Problem.GetX(ctx, p.ID)
	require.Equal(t, before.RootCause, after.RootCause)
	require.Equal(t, before.VerificationNote, after.VerificationNote)
	require.Equal(t, p.Version, client.Ticket.GetX(ctx, *p.WorkItemID).Version)
}

func TestProblemEvidenceHTTPMutationContract(t *testing.T) {
	router, _, svc, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, "evidence-http")
	actor := createProblemHandlerUser(t, ctx, client, tenant.ID, "evidence-http")
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, actor.ID)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:problem-http-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE problem_solutions(id INTEGER PRIMARY KEY,problem_id INTEGER,solution_type TEXT,solution_description TEXT,proposed_by INTEGER,proposed_date DATETIME,status TEXT,priority TEXT,estimated_effort_hours INTEGER,estimated_cost REAL,risk_assessment TEXT,approval_status TEXT,created_at DATETIME,updated_at DATETIME)`)
	require.NoError(t, err)
	c := controller.NewProblemInvestigationController(zaptest.NewLogger(t).Sugar(), service.NewProblemInvestigationService(db, zaptest.NewLogger(t).Sugar()))
	c.SetProblemDomain(svc.Service)
	router.POST("/evidence/solutions", c.CreateProblemSolution)
	router.DELETE("/evidence/solutions/:id", c.DeleteProblemSolution)
	payload := map[string]any{"problemId": p.ID, "solutionType": "fix", "solutionDescription": "candidate body", "priority": "high"}
	w := performProblemRequest(router, "POST", "/evidence/solutions", payload, tenant.ID, actor.ID)
	require.Equal(t, 400, w.Code)
	payload["version"] = p.Version
	payload["operationId"] = "http-candidate"
	for i := 0; i < 2; i++ {
		w = performProblemRequest(router, "POST", "/evidence/solutions", payload, tenant.ID, actor.ID)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	var count, id int
	require.NoError(t, db.QueryRow("SELECT count(*),max(id) FROM problem_solutions").Scan(&count, &id))
	require.Equal(t, 1, count)
	payload = map[string]any{"problemId": p.ID, "version": p.Version + 1, "operationId": "http-delete"}
	for i := 0; i < 2; i++ {
		w = performProblemRequest(router, "DELETE", fmt.Sprintf("/evidence/solutions/%d", id), payload, tenant.ID, actor.ID)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.Equal(t, p.Version+2, client.Ticket.GetX(ctx, *p.WorkItemID).Version)
}
