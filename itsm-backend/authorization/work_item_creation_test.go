package authorization

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestCreationUsesDomainPermissionAndCurrentGrant(t *testing.T) {
	for _, tc := range []struct {
		name, class, resource string
		actions               []string
		allowed               bool
	}{
		{"requester_create", "generic", "ticket", []string{"read", "create", "update"}, true},
		{"read_only", "generic", "ticket", []string{"read"}, false},
		{"update_is_not_create", "generic", "ticket", []string{"read", "update"}, false},
		{"legacy_write_is_not_create", "generic", "ticket", []string{"read", "write"}, false},
		{"create_still_needs_read", "generic", "ticket", []string{"create"}, false},
		{"incident_write", "incident", "incident", []string{"read", "write"}, true},
		{"incident_create_is_not_write", "incident", "incident", []string{"read", "create"}, false},
		{"problem_write", "problem", "problem", []string{"read", "write"}, true},
		{"change_write", "change_request", "change", []string{"read", "write"}, true},
		{"requested_item_write", "service_request_item", "service_request", []string{"read", "write"}, true},
		{"unsupported", "catalog_task", "service_request", []string{"read", "write", "create"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:creation-permission-%s-%d?mode=memory&cache=shared&_fk=1", tc.name, time.Now().UnixNano()))
			t.Cleanup(func() { _ = client.Close() })
			tenant := client.Tenant.Create().SetName("creation").SetCode("creation").SetStatus("active").SaveX(ctx)
			actor := createTenantSessionUser(t, client, tenant.ID, "requester", "end_user", "")
			role := client.Role.Create().SetName("requester").SetCode("end_user").SetTenantID(tenant.ID).SetIsActive(true).SaveX(ctx)
			for _, action := range tc.actions {
				p := client.Permission.Create().SetName(action).SetCode(tc.resource + ":" + action).SetResource(tc.resource).SetAction(action).SetTenantID(tenant.ID).SaveX(ctx)
				client.RolePermission.Create().SetRoleID(role.ID).SetPermissionID(p.ID).SetTenantID(tenant.ID).SaveX(ctx)
			}
			identity := creation.Identity{ActorID: actor.ID, RequesterID: actor.ID, TenantID: tenant.ID, Role: "end_user", Channel: "web"}
			command := creation.CreateWorkItemCommand{RecordClass: tc.class, Title: "requester help"}
			check := func(allowed bool) {
				tx, err := client.Tx(ctx)
				require.NoError(t, err)
				defer tx.Rollback()
				_, err = AuthorizeWorkItemCreation(ctx, tx, tx.Client(), identity, command)
				if allowed {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
			check(tc.allowed)
			if tc.allowed {
				// A later request/replay must read grants again, rather than trust prior authorization.
				_, err := client.RolePermission.Delete().Where(rolepermission.RoleIDEQ(role.ID)).Exec(ctx)
				require.NoError(t, err)
				check(false)
			}
		})
	}
}
