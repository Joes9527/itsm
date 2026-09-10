//go:build integration_postgres

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
	p := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
	s := problemDomain.NewService(problemDomain.NewEntRepository(f.runtime.Tenant), zap.NewNop().Sugar())
	s.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	h := problemDomain.NewHandler(s, f.runtime.Tenant)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set("role", "super_admin")
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	})
	r.POST("/api/v1/problems/:id/associations", h.AddAssociation)
	r.DELETE("/api/v1/problems/:id/associations", h.RemoveAssociation)
	r.DELETE("/api/v1/problems/:id", h.Delete)
	return f, r, fmt.Sprintf("/api/v1/problems/%d/associations", p.ID)
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
	w = relationHTTP(r, "DELETE", strings.TrimSuffix(path, "/associations"), "")
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
