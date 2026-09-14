//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkItemRelationsGenericDeletionRejectsActiveReference(t *testing.T) {
	f := newRelationFixture(t)
	_, err := f.owner.Apply(f.ctx, f.command("generic-delete-link"), false)
	require.NoError(t, err)
	owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	err = owner.DeleteTicket(f.ctx, f.problem.ID, f.command("delete").Meta)
	require.Error(t, err, "generic deletion must preserve active professional references")
	require.Nil(t, f.client.Ticket.GetX(f.ctx, f.problem.ID).DeletedAt)
}

func TestWorkItemRelationsBatchDeletionRejectsMissingAtomically(t *testing.T) {
	f := newRelationFixture(t)
	owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	err := owner.BatchDeleteTickets(f.ctx, []int{f.problem.ID, 999999999}, f.command("delete").Meta)
	require.Error(t, err, "one missing target must reject the entire batch")
	require.Nil(t, f.client.Ticket.GetX(f.ctx, f.problem.ID).DeletedAt)
}

func TestWorkItemRelationsBatchDeletionValidationAndRollback(t *testing.T) {
	for _, scenario := range []string{"clean", "empty", "duplicate", "missing", "foreign", "linked", "hidden", "revoked_delete", "revoked_read", "inactive", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRelationFixture(t)
			owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
			owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
			ids := []int{f.problem.ID, f.inc.WorkItemID}
			if scenario == "empty" {
				ids = nil
			}
			if scenario == "duplicate" {
				ids = []int{f.problem.ID, f.problem.ID}
			}
			if scenario == "missing" {
				ids = append(ids, 999999999)
			}
			if scenario == "foreign" {
				f.client.Ticket.UpdateOneID(f.problem.ID).SetTenantID(f.tenant.ID + 1).ExecX(f.ctx)
			}
			if scenario == "linked" {
				_, err := f.owner.Apply(f.ctx, f.command("batch-linked"), false)
				require.NoError(t, err)
			}
			if scenario == "inactive" {
				f.actor.Update().SetActive(false).ExecX(f.ctx)
			}
			if scenario == "hidden" || scenario == "revoked_delete" || scenario == "revoked_read" {
				f.actor.Update().SetRole("batch_operator").ExecX(f.ctx)
				role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("batch_operator").SetName("operator").SaveX(f.ctx)
				for _, resource := range []string{"incident", "problem"} {
					for _, verb := range []string{"read", "delete"} {
						if resource == "problem" && (scenario == "revoked_delete" && verb == "delete" || scenario == "revoked_read" && verb == "read") {
							continue
						}
						p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + verb).SetName(verb).SetResource(resource).SetAction(verb).SaveX(f.ctx)
						f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).ExecX(f.ctx)
					}
				}
				if scenario == "hidden" {
					other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("hidden-owner").SetName("other").SetRole("agent").SetPasswordHash("test").SetEmail("hidden@example.test").SetActive(true).SaveX(f.ctx)
					f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(other.ID).ClearAssigneeID().ExecX(f.ctx)
				}
			}
			before := map[int]int{}
			for _, id := range []int{f.problem.ID, f.inc.WorkItemID} {
				before[id] = f.client.Ticket.GetX(f.ctx, id).Version
			}
			if scenario == "write_failure" {
				f.runtime.Tenant.Ticket.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
						return nil, errors.New("injected delete failure")
					})
				})
			}
			err := owner.BatchDeleteTickets(f.ctx, ids, f.command("batch-delete").Meta)
			if scenario == "clean" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			for id, version := range before {
				after := f.client.Ticket.GetX(f.ctx, id)
				if scenario == "clean" {
					require.NotNil(t, after.DeletedAt)
					require.Equal(t, version+1, after.Version)
				} else {
					require.Nil(t, after.DeletedAt)
					require.Equal(t, version, after.Version)
				}
			}
		})
	}
}

func TestWorkItemRelationsIncidentAndSubtaskDeletion(t *testing.T) {
	for _, scenario := range []string{"incident_clean", "incident_linked", "subtask_clean", "subtask_wrong_parent", "subtask_linked", "subtask_hidden_parent"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRelationFixture(t)
			_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON incidents TO %q", f.runtimeRole))
			require.NoError(t, err)
			childID := f.inc.WorkItemID
			parent := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("generic").SetTitle("Parent").SetTicketNumber("PARENT-DELETE").SaveX(f.ctx)
			if strings.HasPrefix(scenario, "subtask") {
				f.client.Ticket.UpdateOneID(childID).SetParentTicketID(parent.ID).ExecX(f.ctx)
			}
			if strings.HasSuffix(scenario, "linked") {
				_, err = f.owner.Apply(f.ctx, f.command("delete-link"), false)
				require.NoError(t, err)
			}
			if scenario == "subtask_hidden_parent" {
				f.actor.Update().SetRole("scoped").ExecX(f.ctx)
				role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("scoped").SetName("scoped").SaveX(f.ctx)
				for _, resource := range []string{"incident", "ticket"} {
					for _, verb := range []string{"read", "delete"} {
						p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + verb).SetName(verb).SetResource(resource).SetAction(verb).SaveX(f.ctx)
						f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).ExecX(f.ctx)
					}
				}
				other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("parent-owner").SetName("other").SetRole("agent").SetPasswordHash("test").SetEmail("parent@example.test").SetActive(true).SaveX(f.ctx)
				parent.Update().SetRequesterID(other.ID).ExecX(f.ctx)
			}
			before := f.client.Ticket.GetX(f.ctx, childID).Version
			if strings.HasPrefix(scenario, "incident") {
				owner := service.NewIncidentService(f.runtime.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
				owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
				err = owner.DeleteIncident(f.ctx, f.inc.ID, f.command("delete").Meta)
			} else {
				owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
				owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
				parentID := parent.ID
				if scenario == "subtask_wrong_parent" {
					parentID = f.problem.ID
				}
				err = owner.DeleteSubtask(f.ctx, parentID, childID, f.command("delete").Meta)
			}
			after := f.client.Ticket.GetX(f.ctx, childID)
			if strings.HasSuffix(scenario, "clean") {
				require.NoError(t, err)
				require.NotNil(t, after.DeletedAt)
				require.Equal(t, before+1, after.Version)
			} else {
				require.Error(t, err)
				require.Nil(t, after.DeletedAt)
				require.Equal(t, before, after.Version)
			}
			require.Nil(t, f.client.Ticket.GetX(f.ctx, parent.ID).DeletedAt)
		})
	}
}

func TestWorkItemRelationsGenericDeletionPreservesChangeCallbackGuard(t *testing.T) {
	for _, mode := range []string{"single", "batch", "subtask"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			f.apply(t, f.command("submit", "submit"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			f.client.ProcessCallbackOutbox.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetExecutionKey("unresolved-delete").SetCallbackKind("service_task").SetHandlerID("change_service_handler").SetTaskType("change_task").SetElementID("assess").SetAction("assess_risk").SetStatus("blocked").SaveX(f.ctx)
			owner := service.NewTicketServiceForTest(f.runtime, zap.NewNop().Sugar())
			meta := f.command("delete", "cancel").Meta
			parentID := 0
			if mode == "subtask" {
				parent := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("generic").SetTitle("Change parent").SetTicketNumber("CHANGE-PARENT").SaveX(f.ctx)
				parentID = parent.ID
				f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetParentTicketID(parentID).ExecX(f.ctx)
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			var err error
			if mode == "single" {
				err = owner.DeleteTicket(f.ctx, f.c.WorkItemID, meta)
			} else if mode == "batch" {
				err = owner.BatchDeleteTickets(f.ctx, []int{f.c.WorkItemID}, meta)
			} else {
				err = owner.DeleteSubtask(f.ctx, parentID, f.c.WorkItemID, meta)
			}
			var blocked *workitemmutation.UnresolvedChangeCallbackError
			require.ErrorAs(t, err, &blocked)
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Nil(t, after.DeletedAt)
			require.Equal(t, before.Version, after.Version)
		})
	}
}

func TestWorkItemRelationsBatchDeleteOldSnapshotCannotMissReference(t *testing.T) {
	f := newRelationFixture(t)
	owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	other := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("generic").SetTitle("other deletion").SetTicketNumber("BATCH-OTHER").SaveX(f.ctx)
	// Pause only the first batch identity read; it has established its RR snapshot.
	ready, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	f.runtime.Tenant.Ticket.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if first.CompareAndSwap(false, true) {
				close(ready)
				select {
				case <-release:
				case <-time.After(10 * time.Second):
					return nil, errors.New("barrier timeout")
				}
			}
			return v, err
		})
	}))
	done := make(chan error, 1)
	go func() {
		done <- owner.BatchDeleteTickets(f.ctx, []int{other.ID, f.problem.ID}, f.command("batch-race").Meta)
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("batch snapshot timeout")
	}
	cmd := f.command("reference-race")
	_, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	close(release)
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("batch completion timeout")
	}
	require.Error(t, err)
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		require.NotEqual(t, "40P01", state.SQLState(), "sorted locks must not deadlock")
	}
	for _, id := range []int{other.ID, f.problem.ID} {
		require.Nil(t, f.client.Ticket.GetX(f.ctx, id).DeletedAt)
	}
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
}

func TestWorkItemDeletionHTTPStrictBatchAndCounts(t *testing.T) {
	f := newRelationFixture(t)
	owner := service.NewTicketServiceForTest(f.runtime.Tenant, zap.NewNop().Sugar())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	h := controller.NewTicketController(owner, nil, nil, f.runtime.Tenant, zap.NewNop().Sugar())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	actorID := f.actor.ID
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Set("user_id", actorID)
		c.Set("role", "stale-untrusted-role")
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	})
	incOwner := service.NewIncidentService(f.runtime.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	incOwner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	incHandler := controller.NewIncidentController(incOwner, nil, nil, nil, nil, zap.NewNop().Sugar())
	r.DELETE("/api/v1/incidents/:id", incHandler.DeleteIncident)
	_, grantErr := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON incidents TO %q", f.runtimeRole))
	require.NoError(t, grantErr)
	r.POST("/api/v1/tickets/batch-delete", h.BatchDeleteTickets)
	r.DELETE("/api/v1/tickets/:id", h.DeleteTicket)
	r.DELETE("/api/v1/tickets/:id/subtasks/:subtask_id", h.DeleteSubtask)
	request := func(method, path, body string, status int) common.Response {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		var envelope common.Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		if status == 200 {
			require.Equal(t, 0, envelope.Code)
		} else {
			require.NotEqual(t, 0, envelope.Code)
		}
		return envelope
	}
	id := f.problem.ID
	for _, body := range []string{fmt.Sprintf(`{"ticketIds":[%d],"unknown":true}`, id), fmt.Sprintf(`{"ticketIds":[%d],"ticketIds":[%d]}`, id, id), fmt.Sprintf(`{"TicketIds":[%d]}`, id), `{"ticketIds":null}`, `{"ticketIds":[]}`, `{"ticketIds":[null]}`, fmt.Sprintf(`{"ticketIds":[%d,%d]}`, id, id), `{"ticketIds":[0]}`} {
		request("POST", "/api/v1/tickets/batch-delete", body, 400)
		require.Nil(t, f.client.Ticket.GetX(f.ctx, id).DeletedAt)
	}
	request("POST", "/api/v1/tickets/batch-delete", fmt.Sprintf(`{"ticketIds":[%d,999999999]}`, id), 404)
	actorID = 0
	request("DELETE", fmt.Sprintf("/api/v1/tickets/%d", id), "", 401)
	actorID = f.actor.ID
	_, err := f.owner.Apply(f.ctx, f.command("http-linked"), false)
	require.NoError(t, err)
	request("DELETE", fmt.Sprintf("/api/v1/tickets/%d", id), "", 409)
	request("DELETE", fmt.Sprintf("/api/v1/incidents/%d", f.inc.ID), "", 409)
	request("DELETE", fmt.Sprintf("/api/v1/tickets/%d/subtasks/%d", id, f.inc.WorkItemID), "", 400)
	request("POST", "/api/v1/tickets/batch-delete", fmt.Sprintf(`{"ticketIds":[%d,%d]}`, id, f.inc.WorkItemID), 409)
	cmd := f.command("http-remove")
	_, err = f.owner.Apply(f.ctx, cmd, true)
	require.NoError(t, err)
	before := f.client.Ticket.GetX(f.ctx, id).Version
	response := request("POST", "/api/v1/tickets/batch-delete", fmt.Sprintf(`{"ticketIds":[%d,%d]}`, id, f.inc.WorkItemID), 200)
	require.Equal(t, float64(2), response.Data.(map[string]interface{})["deleted_count"])
	require.Equal(t, before+1, f.client.Ticket.GetX(f.ctx, id).Version)
	request("DELETE", fmt.Sprintf("/api/v1/tickets/%d", id), "", 404)
}
