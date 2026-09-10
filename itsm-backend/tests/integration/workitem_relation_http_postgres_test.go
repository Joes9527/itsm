//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/ent"
	changeDomain "itsm-backend/handlers/change"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/controller"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/rolepermission"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
)

func problemRelationHTTP(t *testing.T) (*relationFixture, *gin.Engine, string) {
	t.Helper()
	f := newRelationFixture(t)
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON problems,ticket_categories TO %q", f.runtimeRole))
	require.NoError(t, err)
	_ = f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
	s := problemDomain.NewService(problemDomain.NewEntRepository(f.runtime.Tenant), zap.NewNop().Sugar())
	s.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	h := problemDomain.NewHandler(s, f.runtime.Tenant)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set("role", "super_admin")
		c.Set("client", f.runtime.Tenant)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	})
	shared := controller.NewWorkItemRelationController(f.owner)
	r.POST("/api/v1/work-items/:id/relations", middleware.RequireWorkItemRecordClassPermission("update"), shared.Add)
	r.DELETE("/api/v1/work-items/:id/relations", middleware.RequireWorkItemRecordClassPermission("update"), shared.Remove)
	r.GET("/api/v1/work-items/:id/relations", middleware.RequireWorkItemRecordClassPermission("read"), shared.List)
	r.GET("/api/v1/problems/:id", h.Get)
	r.DELETE("/api/v1/problems/:id", h.Delete)
	return f, r, fmt.Sprintf("/api/v1/work-items/%d/relations", f.inc.WorkItemID)
}

func relationHTTP(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, request)
	return w
}

func TestProblemRelationHTTPStrictNestedMetadata(t *testing.T) {
	f, r, path := problemRelationHTTP(t)
	body := fmt.Sprintf(`{"sourceWorkItemId":%d,"targetWorkItemId":%d,"relationType":"investigated_by","expectedVersion":1,"operationId":"strict-body","metadata":{"required":false}}`, f.inc.WorkItemID, f.problem.ID)
	for _, invalid := range []string{
		strings.Replace(body, `"required":false`, `"required":null`, 1),
		strings.Replace(body, `{"required":false}`, `null`, 1),
		strings.Replace(body, `"required":false`, `"required":false,"required":true`, 1),
		strings.Replace(body, `"required":false`, `"Required":false`, 1),
		strings.Replace(body, `"expectedVersion":1,`, ``, 1),
		strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":0`, 1),
		strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":1,"expectedVersion":1`, 1),
		strings.Replace(body, `"operationId":"strict-body",`, ``, 1),
		strings.Replace(body, `"operationId":"strict-body"`, `"operationId":" "`, 1),
		strings.Replace(body, `"required":false`, `"required":true`, 1),
		strings.Replace(body, `"relationType":"investigated_by"`, `"relationType":"unknown"`, 1),
		strings.Replace(body, `"sourceWorkItemId"`, `"SourceWorkItemId"`, 1),
		`{"relatedType":"incident","relatedIds":[1]}`,
	} {
		w := relationHTTP(r, "POST", path, invalid)
		require.Equal(t, 400, w.Code, w.Body.String())
		require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
	}
}

func TestProblemRelationHTTPCommandsAndCurrentAuthority(t *testing.T) {
	f, r, path := problemRelationHTTP(t)
	body := fmt.Sprintf(`{"sourceWorkItemId":%d,"targetWorkItemId":%d,"relationType":"investigated_by","expectedVersion":1,"operationId":"http-add"}`, f.inc.WorkItemID, f.problem.ID)
	w := relationHTTP(r, "POST", path, strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":999`, 1))
	require.Equal(t, 409, w.Code, w.Body.String())
	w = relationHTTP(r, "POST", path, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		Data workitemmutation.Result `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, f.inc.WorkItemID, result.Data.WorkItemID)
	require.Equal(t, 2, result.Data.Version)
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
	w = relationHTTP(r, "POST", path, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.True(t, result.Data.Replayed)
	w = relationHTTP(r, "DELETE", fmt.Sprintf("/api/v1/problems/%d", f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx).ID), "")
	require.Equal(t, 409, w.Code, w.Body.String())
	w = relationHTTP(r, "POST", path, strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":2`, 1))
	require.Equal(t, 409, w.Code, w.Body.String())
	remove := strings.Replace(strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":2`, 1), `http-add`, `http-remove`, 1)
	w = relationHTTP(r, "DELETE", path, remove)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = relationHTTP(r, "DELETE", path, remove)
	require.Equal(t, 200, w.Code, w.Body.String())
	relink := strings.Replace(strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":3`, 1), `http-add`, `http-relink`, 1)
	w = relationHTTP(r, "POST", path, relink)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 4, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	// The request context still says super_admin; current persisted role/grants
	// and both endpoint row scopes must decide replay, never that stale label.
	f.actor.Update().SetRole("relation_operator").ExecX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("relation_operator").SetName("relation operator").SaveX(f.ctx)
	for _, pair := range [][2]string{{"incident", "read"}, {"incident", "write"}, {"problem", "read"}} {
		grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetResource(pair[0]).SetAction(pair[1]).SetCode("relation_" + pair[0] + pair[1]).SetName(pair[1]).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
	}
	grants := f.client.RolePermission.Query().Where(rolepermission.RoleID(role.ID)).AllX(f.ctx)
	for _, grant := range grants {
		f.client.RolePermission.Delete().Where(rolepermission.RoleID(role.ID), rolepermission.PermissionID(grant.PermissionID)).ExecX(f.ctx)
		w = relationHTTP(r, "POST", path, body)
		require.Equal(t, 403, w.Code, w.Body.String())
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.PermissionID).ExecX(f.ctx)
	}
	other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("hidden-requester").SetName("other").SetRole("agent").SetPasswordHash("test").SetEmail("hidden@example.test").SetActive(true).SaveX(f.ctx)
	f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(other.ID).ExecX(f.ctx)
	w = relationHTTP(r, "POST", path, body)
	require.Equal(t, 404, w.Code, w.Body.String())
	f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(f.actor.ID).ExecX(f.ctx)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	w = relationHTTP(r, "POST", path, body)
	require.Equal(t, 403, w.Code, w.Body.String())
	actorID := f.actor.ID
	f.actor.ID = 0 // missing authenticated identity at the HTTP boundary
	w = relationHTTP(r, "POST", path, body)
	f.actor.ID = actorID
	require.Equal(t, 401, w.Code, w.Body.String())
	require.Equal(t, 4, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	require.Equal(t, 3, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestProblemRelationHTTPRequiredChangeMetadataAndTenant(t *testing.T) {
	f, r, path := problemRelationHTTP(t)
	path = fmt.Sprintf("/api/v1/work-items/%d/relations", f.problem.ID)
	item, _ := deletionOwner(t, f, "change")
	body := fmt.Sprintf(`{"sourceWorkItemId":%d,"targetWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"operationId":"required-change","metadata":{"required":true}}`, f.problem.ID, item.ID)
	w := relationHTTP(r, "POST", path, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	row := f.client.WorkItemRelation.Query().OnlyX(f.ctx)
	require.True(t, row.Metadata.Required)
	require.Equal(t, f.problem.ID, row.SourceWorkItemID)
	require.Equal(t, item.ID, row.TargetWorkItemID)
	foreign := f.client.Tenant.Create().SetCode("foreign-target").SetName("foreign").SaveX(f.ctx)
	f.client.Ticket.UpdateOneID(item.ID).SetTenantID(foreign.ID).ExecX(f.ctx)
	w = relationHTTP(r, "POST", path, body)
	require.Equal(t, 404, w.Code, w.Body.String())
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
}

func TestProblemRelationReadRequiresCurrentActor(t *testing.T) {
	f, r, _ := problemRelationHTTP(t)
	id := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx).ID
	f.actor.ID = 0
	w := relationHTTP(r, "GET", fmt.Sprintf("/api/v1/problems/%d", id), "")
	require.Equal(t, 401, w.Code, w.Body.String())
}

func TestWorkItemRelationHTTPExistingIncidentChangeAndRelatedTo(t *testing.T) {
	for _, kind := range []string{"resolved_by_change", "related_to"} {
		t.Run(kind, func(t *testing.T) {
			f, r, path := problemRelationHTTP(t)
			target, _ := deletionOwner(t, f, "change")
			body := fmt.Sprintf(`{"sourceWorkItemId":%d,"targetWorkItemId":%d,"relationType":"%s","expectedVersion":1,"operationId":"existing-target","metadata":{"required":%t}}`, f.inc.WorkItemID, target.ID, kind, kind == "resolved_by_change")
			w := relationHTTP(r, "POST", fmt.Sprintf("/api/v1/work-items/%d/relations", target.ID), body)
			require.Equal(t, 400, w.Code, w.Body.String())
			w = relationHTTP(r, "POST", path, body)
			require.Equal(t, 200, w.Code, w.Body.String())
			w = relationHTTP(r, "GET", path, "")
			require.Equal(t, 200, w.Code, w.Body.String())
			var response struct {
				Data []relationmeta.View `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Len(t, response.Data, 1)
			require.Equal(t, kind, response.Data[0].Type)
			require.Equal(t, f.inc.WorkItemID, response.Data[0].Source.WorkItemID)
			require.Equal(t, target.ID, response.Data[0].Target.WorkItemID)
			require.NotEmpty(t, response.Data[0].Source.Number)
			require.Equal(t, 2, response.Data[0].Source.Version)
			remove := strings.Replace(strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":2`, 1), "existing-target", "remove-target", 1)
			w = relationHTTP(r, "DELETE", path, remove)
			require.Equal(t, 200, w.Code, w.Body.String())
			w = relationHTTP(r, "GET", path, "")
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), `"data":[]`)
			require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
			require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, target.ID).Version)
			// Removed professional alias is deliberately unavailable.
			w = relationHTTP(r, "POST", "/api/v1/problems/1/associations", body)
			require.Equal(t, 404, w.Code)
		})
	}
}

func TestChangeListHTTPProjectionFailureContract(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, code int
	}{
		{"missing_actor", 401, common.AuthFailedCode},
		{"revoked_actor", 403, common.ForbiddenCode},
		{"revoked_change_read", 403, common.ForbiddenCode},
		{"revoked_endpoint_read", 403, common.ForbiddenCode},
		{"hidden_endpoint", 404, common.NotFoundCode},
		{"projection_storage", 500, common.InternalErrorCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelationFixture(t)
			_, owner, change := projectionOwners(t, f)
			cmd := f.command("list-error-link")
			cmd.SourceID = f.problem.ID
			cmd.TargetID = change.WorkItemID
			cmd.Type = "resolved_by_change"
			cmd.Required = true
			_, err := f.owner.Apply(f.ctx, cmd, false)
			require.NoError(t, err)
			f.client.User.UpdateOneID(f.actor.ID).SetRole("list_reader").ExecX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("list_reader").SetName("List reader").SetIsActive(true).SaveX(f.ctx)
			grants := map[string]int{}
			for _, resource := range []string{"change", "problem"} {
				grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":list-read").SetName("Read").SetResource(resource).SetAction("read").SaveX(f.ctx)
				f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
				grants[resource] = grant.ID
			}
			actorID := f.actor.ID
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set("tenant_id", f.tenant.ID)
				c.Set("user_id", actorID)
				c.Set("role", "super_admin")
				c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
				c.Request = c.Request.WithContext(f.ctx)
				c.Next()
			})
			r.GET("/api/v1/changes", changeDomain.NewHandler(owner).ListChanges)
			w := relationHTTP(r, "GET", "/api/v1/changes", "")
			require.Equal(t, 200, w.Code, w.Body.String())
			marker := "private-list-projection-driver-detail"
			reached := false
			switch tc.name {
			case "missing_actor":
				actorID = 0
			case "revoked_actor":
				f.client.User.UpdateOneID(f.actor.ID).SetActive(false).ExecX(f.ctx)
			case "revoked_change_read":
				f.client.RolePermission.Delete().Where(rolepermission.PermissionID(grants["change"])).ExecX(f.ctx)
			case "revoked_endpoint_read":
				f.client.RolePermission.Delete().Where(rolepermission.PermissionID(grants["problem"])).ExecX(f.ctx)
			case "hidden_endpoint":
				other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("other-list-reader").SetName("Other").SetEmail("other-list@example.test").SetPasswordHash("test").SetRole("end_user").SetActive(true).SaveX(f.ctx)
				f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(other.ID).ClearAssigneeID().ExecX(f.ctx)
			case "projection_storage":
				f.runtime.Tenant.WorkItemRelation.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
					return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { reached = true; return nil, errors.New(marker) })
				}))
			}
			w = relationHTTP(r, "GET", "/api/v1/changes", "")
			// Nonfatal assertions expose both wrong statuses and raw error leakage in RED.
			if w.Code != tc.status {
				t.Errorf("HTTP status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			var response common.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			if response.Code != tc.code {
				t.Errorf("envelope code=%d want=%d", response.Code, tc.code)
			}
			require.Nil(t, response.Data, "failure must not return authoritative empty page")
			require.NotContains(t, w.Body.String(), marker)
			if tc.name == "projection_storage" {
				require.True(t, reached)
			}
			require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, change.WorkItemID).Version)
		})
	}
}
