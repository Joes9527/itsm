//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	requestDomain "itsm-backend/handlers/service_request"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestWorkItemRelationsServiceRequestDeletionRejectsReference(t *testing.T) {
	f := newRelationFixture(t)
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON service_requests TO %q", f.runtimeRole))
	require.NoError(t, err)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTicketNumber("REQ-DELETE").SetTitle("request deletion").SetRecordClass("service_request_item").SetStatus("submitted").SaveX(f.ctx)
	sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(f.ctx)
	cmd := f.command("request-reference")
	cmd.Type = "related_to"
	cmd.TargetID = item.ID
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	owner := requestDomain.NewService(requestDomain.NewEntRepository(f.runtime.Tenant, executionfixture.Standard()), f.runtime.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	err = owner.Delete(f.ctx, sr.ID, cmd.Meta)
	require.Error(t, err, "RequestedItem deletion must reject an active reference")
	require.Nil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
	require.Equal(t, item.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
}

func TestWorkItemRelationsRequestedItemPolicyAcrossDeletionEntrypoints(t *testing.T) {
	for _, entry := range []string{"professional", "generic", "batch", "subtask"} {
		t.Run(entry, func(t *testing.T) {
			f := newRelationFixture(t)
			_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON service_requests TO %q", f.runtimeRole))
			require.NoError(t, err)
			other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("requester").SetName("requester").SetRole("end_user").SetPasswordHash("test").SetEmail("requester@example.test").SetActive(true).SaveX(f.ctx)
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(other.ID).SetAssigneeID(f.actor.ID).SetOpenedByID(other.ID).SetTicketNumber("REQ-POLICY").SetTitle("request deletion").SetRecordClass("service_request_item").SetStatus("submitted").SetParentTicketID(f.problem.ID).SaveX(f.ctx)
			sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(f.ctx)
			f.actor.Update().SetRole("request_deleter").ExecX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("request_deleter").SetName("request deleter").SaveX(f.ctx)
			for _, resource := range []string{"service_request", "problem"} {
				for _, action := range []string{"read", "delete"} {
					p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + "_" + action).SetName(action).SetResource(resource).SetAction(action).SaveX(f.ctx)
					f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).ExecX(f.ctx)
				}
			}
			owner := requestDomain.NewService(requestDomain.NewEntRepository(f.runtime.Tenant, executionfixture.Standard()), f.runtime.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
			owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
			generic := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
			generic.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
			meta := f.command("delete-policy").Meta
			switch entry {
			case "professional":
				err = owner.Delete(f.ctx, sr.ID, meta)
			case "generic":
				err = generic.DeleteTicket(f.ctx, item.ID, meta)
			case "batch":
				err = generic.BatchDeleteTickets(f.ctx, []int{f.problem.ID, item.ID}, meta)
			case "subtask":
				err = generic.DeleteSubtask(f.ctx, f.problem.ID, item.ID, meta)
			}
			require.Error(t, err, "visible assignee with delete but without manage must not delete another requester's RequestedItem")
			require.Nil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
			require.Nil(t, f.client.Ticket.GetX(f.ctx, f.problem.ID).DeletedAt)
			item.Update().SetRequesterID(f.actor.ID).ExecX(f.ctx)
			switch entry {
			case "professional":
				err = owner.Delete(f.ctx, sr.ID, meta)
			case "generic":
				err = generic.DeleteTicket(f.ctx, item.ID, meta)
			case "batch":
				err = generic.BatchDeleteTickets(f.ctx, []int{f.problem.ID, item.ID}, meta)
			case "subtask":
				err = generic.DeleteSubtask(f.ctx, f.problem.ID, item.ID, meta)
			}
			require.NoError(t, err, "current requester with read/delete must retain access through every entrypoint")
			require.NotNil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
			require.Equal(t, item.Version+1, f.client.Ticket.GetX(f.ctx, item.ID).Version)
		})
	}
}

func TestWorkItemRelationsServiceRequestDeletionCurrentAuthority(t *testing.T) {
	for _, scenario := range []string{"requester", "manager", "no_delete", "no_read", "revoked_write", "reassigned_requester", "inactive", "hidden", "foreign", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRelationFixture(t)
			_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON service_requests TO %q", f.runtimeRole))
			require.NoError(t, err)
			other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("other").SetName("other").SetRole("end_user").SetPasswordHash("test").SetEmail("other@example.test").SetActive(true).SaveX(f.ctx)
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTicketNumber("REQ-AUTH").SetTitle("request deletion").SetRecordClass("service_request_item").SetStatus("submitted").SaveX(f.ctx)
			sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(f.ctx)
			f.actor.Update().SetRole("request_operator").ExecX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("request_operator").SetName("operator").SaveX(f.ctx)
			for _, action := range []string{"read", "delete", "write"} {
				if scenario == "no_delete" && action == "delete" || scenario == "no_read" && action == "read" || (scenario == "requester" || scenario == "reassigned_requester" || scenario == "revoked_write") && action == "write" {
					continue
				}
				p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("request_" + action).SetName(action).SetResource("service_request").SetAction(action).SaveX(f.ctx)
				f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).ExecX(f.ctx)
			}
			if scenario == "manager" || scenario == "revoked_write" || scenario == "reassigned_requester" {
				item.Update().SetRequesterID(other.ID).SetAssigneeID(f.actor.ID).ExecX(f.ctx)
			}
			if scenario == "hidden" {
				item.Update().SetRequesterID(other.ID).ClearAssigneeID().ExecX(f.ctx)
			}
			if scenario == "inactive" {
				f.actor.Update().SetActive(false).ExecX(f.ctx)
			}
			if scenario == "write_failure" {
				f.runtime.Tenant.Ticket.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
						return nil, errors.New("private injected delete failure")
					})
				})
			}
			owner := requestDomain.NewService(requestDomain.NewEntRepository(f.runtime.Tenant, executionfixture.Standard()), f.runtime.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
			owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
			meta := f.command("delete").Meta
			if scenario == "foreign" {
				meta.TenantID++
			}
			err = owner.Delete(f.ctx, sr.ID, meta)
			after := f.client.Ticket.GetX(f.ctx, item.ID)
			if scenario == "requester" || scenario == "manager" {
				require.NoError(t, err)
				require.NotNil(t, after.DeletedAt)
				require.Equal(t, item.Version+1, after.Version)
				_, err = owner.Get(f.ctx, sr.ID, f.tenant.ID)
				require.Error(t, err)
			} else {
				require.Error(t, err)
				require.Nil(t, after.DeletedAt)
				require.Equal(t, item.Version, after.Version)
			}
			require.Equal(t, 1, f.client.ServiceRequest.Query().CountX(f.ctx), "professional history is retained")
		})
	}
}

func TestWorkItemRelationsServiceRequestOldSnapshotCannotMissReference(t *testing.T) {
	f := newRelationFixture(t)
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON service_requests TO %q", f.runtimeRole))
	require.NoError(t, err)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTicketNumber("REQ-RACE").SetTitle("request race").SetRecordClass("service_request_item").SetStatus("submitted").SaveX(f.ctx)
	sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(f.ctx)
	owner := requestDomain.NewService(requestDomain.NewEntRepository(f.runtime.Tenant, executionfixture.Standard()), f.runtime.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	ready, release := make(chan struct{}), make(chan struct{})
	f.runtime.Tenant.ServiceRequest.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			close(ready)
			select {
			case <-release:
			case <-time.After(10 * time.Second):
				return nil, errors.New("release timeout")
			}
			return v, err
		})
	}))
	done := make(chan error, 1)
	cmd := f.command("request-old-snapshot")
	cmd.TargetID, cmd.Type = item.ID, "related_to"
	go func() { done <- owner.Delete(f.ctx, sr.ID, cmd.Meta) }()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("delete snapshot barrier timeout")
	}
	_, err = f.owner.Apply(f.ctx, cmd, false)
	close(release)
	require.NoError(t, err)
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("delete contender timeout")
	}
	require.Error(t, err)
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		require.NotEqual(t, "40P01", state.SQLState())
	}
	require.Nil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
	require.Equal(t, item.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	require.Nil(t, f.client.WorkItemRelation.Query().OnlyX(f.ctx).DeletedAt)
}

func TestWorkItemServiceRequestDeletionHTTPCurrentIdentity(t *testing.T) {
	for _, scenario := range []string{"linked", "forged_role", "missing_actor", "write_failure", "clean"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRelationFixture(t)
			_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON service_requests TO %q", f.runtimeRole))
			require.NoError(t, err)
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTicketNumber("REQ-HTTP").SetTitle("request http").SetRecordClass("service_request_item").SetStatus("submitted").SaveX(f.ctx)
			sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(f.ctx)
			owner := requestDomain.NewService(requestDomain.NewEntRepository(f.runtime.Tenant, executionfixture.Standard()), f.runtime.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
			owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
			status := 200
			if scenario == "linked" {
				cmd := f.command("http-link")
				cmd.TargetID, cmd.Type = item.ID, "related_to"
				_, err = f.owner.Apply(f.ctx, cmd, false)
				require.NoError(t, err)
				status = 409
			}
			if scenario == "forged_role" {
				f.actor.Update().SetRole("viewer").ExecX(f.ctx)
				status = 403
			}
			if scenario == "missing_actor" {
				status = 401
			}
			if scenario == "write_failure" {
				status = 500
				f.runtime.Tenant.Ticket.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
						return nil, errors.New("private-storage-secret")
					})
				})
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("tenant_id", f.tenant.ID)
				c.Set("role", "super_admin")
				if scenario != "missing_actor" {
					c.Set("user_id", f.actor.ID)
				}
			})
			router.DELETE("/api/v1/service-requests/:id", requestDomain.NewHandler(owner).Delete)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/service-requests/%d", sr.ID), nil).WithContext(f.ctx))
			require.Equal(t, status, recorder.Code, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), "private-storage-secret")
			if status == 200 {
				require.NotNil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
			} else {
				require.Nil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
			}
		})
	}
}
