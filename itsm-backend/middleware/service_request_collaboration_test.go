package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/ent/enttest"
)

func TestRequestedItemCollaborationScope(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:sr-collaboration-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	item := setupWorkItemRBACTestTicket(t, client, 1, "service_request_item")
	assignee := client.User.Create().SetUsername("helpdesk").SetName("Helpdesk").SetEmail("helpdesk@example.test").SetPasswordHash("unused").SetRole("l1_support").SetTenantID(item.TenantID).SetActive(true).SaveX(ctx)
	outsider := client.User.Create().SetUsername("outsider").SetName("Other Helpdesk").SetEmail("outsider@example.test").SetPasswordHash("unused").SetRole("l1_support").SetTenantID(item.TenantID).SetActive(true).SaveX(ctx)
	client.Ticket.UpdateOneID(item.ID).SetAssigneeID(assignee.ID).ExecX(ctx)
	for _, tc := range []struct {
		name          string
		actor, tenant int
		actions       []string
		want          int
	}{
		{"assigned_helpdesk", assignee.ID, 1, []string{"read", "provision"}, 200},
		{"unassigned_helpdesk", outsider.ID, 1, []string{"read", "provision"}, 403},
		{"requester", item.RequesterID, 1, []string{"read", "write"}, 200},
		{"another_requester", assignee.ID, 1, []string{"read", "write"}, 403},
		{"read_only_assignee", assignee.ID, 1, []string{"read"}, 403},
		{"legacy_granular_actions", assignee.ID, 1, []string{"read", "create", "update"}, 403},
		{"missing_read", assignee.ID, 1, []string{"provision"}, 403},
		{"missing_actor", 0, 1, []string{"read", "provision"}, 403},
		{"other_tenant", assignee.ID, 99, []string{"read", "provision"}, 404},
		{"resource_admin", outsider.ID, 1, []string{"*"}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			perms := []authorization.Permission{}
			for _, a := range tc.actions {
				perms = append(perms, authorization.Permission{Resource: "service_request", Action: a})
			}
			withHardcodedPermissions(t, "sr_collaborator", perms)
			for _, action := range []string{"create", "update"} {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(item.ID)}}
				c.Set("client", client)
				c.Set("tenant_id", tc.tenant)
				c.Set("role", "sr_collaborator")
				c.Set("user_id", tc.actor)
				RequireWorkItemCollaborationPermission(action)(c)
				require.Equal(t, tc.want, w.Code, w.Body.String())
				require.Equal(t, tc.want != 200, c.IsAborted())
			}
		})
	}
	t.Run("generic_ticket_grant_is_not_service_request_collaboration", func(t *testing.T) {
		withHardcodedPermissions(t, "sr_collaborator", []authorization.Permission{{Resource: "ticket", Action: "*"}})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(item.ID)}}
		c.Set("client", client)
		c.Set("tenant_id", 1)
		c.Set("role", "sr_collaborator")
		c.Set("user_id", assignee.ID)
		RequireWorkItemCollaborationPermission("create")(c)
		require.Equal(t, 403, w.Code)
	})

	t.Run("reassignment_revokes_collaboration", func(t *testing.T) {
		withHardcodedPermissions(t, "sr_collaborator", []authorization.Permission{{Resource: "service_request", Action: "read"}, {Resource: "service_request", Action: "provision"}})
		client.Ticket.UpdateOneID(item.ID).ClearAssigneeID().ExecX(ctx)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(item.ID)}}
		c.Set("client", client)
		c.Set("tenant_id", 1)
		c.Set("role", "sr_collaborator")
		c.Set("user_id", assignee.ID)
		RequireWorkItemCollaborationPermission("create")(c)
		require.Equal(t, 403, w.Code)
	})
}

// Relation commands use their owning service's update policy, not collaboration row scope.
func TestRequestedItemRelationPermissionDoesNotRequireCollaboration(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:sr-relation-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	item := setupWorkItemRBACTestTicket(t, client, 1, "service_request_item")
	withHardcodedPermissions(t, "relation_operator", []authorization.Permission{{Resource: "service_request", Action: "read"}, {Resource: "service_request", Action: "update"}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/work-items/1/relations", nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(item.ID)}}
	c.Set("client", client)
	c.Set("tenant_id", 1)
	c.Set("role", "relation_operator")
	c.Set("user_id", item.RequesterID+1000)
	RequireWorkItemRecordClassPermission("update")(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.False(t, c.IsAborted())
}
