//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/middleware"
	"itsm-backend/service"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkItemChangePIROwner(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	req := &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful", ObjectivesAchieved: true}
	meta := f.command("pir", "pir-create").Meta
	result, err := f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.NoError(t, err)
	require.Equal(t, meta.ExpectedVersion+1, result.Version)
	require.Positive(t, result.PIRID)
	pir := f.client.ChangePIR.GetX(f.ctx, result.PIRID)
	require.Equal(t, f.actor.ID, pir.ReviewerID)
	replay, err := f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.PIRID, replay.PIRID)
	summary := "Reviewed deployment"
	update := &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, SuccessSummary: &summary}
	meta = f.command("pir", "pir-update").Meta
	updated, err := f.pirOwner.UpdatePIR(f.ctx, pir.ID, update, meta)
	require.NoError(t, err)
	require.Equal(t, result.Version+1, updated.Version)
	meta = f.command("pir", "pir-delete").Meta
	deleted, err := f.pirOwner.DeletePIR(f.ctx, pir.ID, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, meta)
	require.NoError(t, err)
	require.Equal(t, updated.Version+1, deleted.Version)
	replay, err = f.pirOwner.DeletePIR(f.ctx, pir.ID, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, meta)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Zero(t, f.client.ChangePIR.Query().CountX(f.ctx))
	require.Equal(t, 3, f.client.AuditLog.Query().Where(auditlog.ActionIn("change.pir_create", "change.pir_update", "change.pir_delete")).CountX(f.ctx))
}

func TestWorkItemChangePIRGuards(t *testing.T) {
	for _, mode := range []string{"stale", "missing_version", "missing_key", "foreign_tenant", "foreign_change", "forged_actor", "revoked", "audit_rollback", "terminal_completed", "terminal_cancelled", "terminal_rejected", "invalid_result", "invalid_time", "empty_update"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			req := &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}
			created, err := f.pirOwner.CreatePIR(f.ctx, req, f.command("pir", "create").Meta)
			require.NoError(t, err)
			summary := "New lessons"
			patch := &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, LessonsLearned: &summary}
			meta := f.command("pir", "update").Meta
			switch mode {
			case "stale":
				meta.ExpectedVersion--
			case "missing_version":
				meta.ExpectedVersion = 0
			case "missing_key":
				meta.OperationID = ""
			case "foreign_tenant":
				meta.TenantID++
			case "foreign_change":
				patch.ChangeID++
			case "forged_actor":
				meta.ActorID += 1000
			case "revoked":
				f.actor.Update().SetActive(false).ExecX(f.ctx)
			case "audit_rollback":
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.AuditLogMutation); ok {
							return nil, errors.New("PIR audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "terminal_completed", "terminal_cancelled", "terminal_rejected":
				f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus(strings.TrimPrefix(mode, "terminal_")).ExecX(f.ctx)
			case "invalid_result":
				bad := "default"
				patch.OverallResult = &bad
			case "empty_update":
				patch.LessonsLearned = nil
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			pir := f.client.ChangePIR.GetX(f.ctx, created.PIRID)
			if mode == "invalid_time" {
				start, end := time.Now(), time.Now().Add(-time.Hour)
				req.ActualStartTime = &start
				req.ActualEndTime = &end
				_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
			} else {
				_, err = f.pirOwner.UpdatePIR(f.ctx, created.PIRID, patch, meta)
			}
			require.Error(t, err)
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, before.Version, after.Version)
			require.True(t, before.UpdatedAt.Equal(after.UpdatedAt))
			stored := f.client.ChangePIR.GetX(f.ctx, created.PIRID)
			require.Equal(t, pir.LessonsLearned, stored.LessonsLearned)
			require.True(t, pir.UpdatedAt.Equal(stored.UpdatedAt))
			if strings.HasPrefix(mode, "terminal_") || mode == "audit_rollback" {
				_, err = f.pirOwner.DeletePIR(f.ctx, created.PIRID, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, meta)
				require.Error(t, err)
				_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
				require.Error(t, err)
				require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			}
		})
	}
}
func TestWorkItemChangePIRMSPAndReplay(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	role, allocation := setChangeWriter(t, f, true)
	req := &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "rolled_back"}
	meta := f.command("pir", "create").Meta
	result, err := f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.NoError(t, err)
	require.Equal(t, f.actor.ID, f.client.ChangePIR.GetX(f.ctx, result.PIRID).ReviewerID)
	receipt := f.client.AuditLog.Query().Where(auditlog.Action("change.pir_create")).OnlyX(f.ctx)
	require.Equal(t, f.actor.ID, receipt.UserID)
	err = receipt.Update().SetRequestBody("{}").Exec(f.ctx)
	require.Error(t, err, "actual immutable receipt trigger")
	req.OverallResult = "failed"
	_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.ErrorContains(t, err, "operationId")
	req.OverallResult = "rolled_back"
	role.Update().SetIsActive(false).ExecX(f.ctx)
	_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.Error(t, err)
	role.Update().SetIsActive(true).ExecX(f.ctx)
	f.client.MSPAllocation.DeleteOne(allocation).ExecX(f.ctx)
	_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
	require.Error(t, err)
}
func TestWorkItemChangePIRRaces(t *testing.T) {
	for _, mode := range []string{"metadata", "task", "same_key", "different_key", "close_update", "close_delete"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			var task *ent.ProcessTask
			if mode == "task" {
				task = prepareChangeDefaultTask(t, f)
			}
			var pirID int
			if strings.HasPrefix(mode, "close_") {
				f.authorize(t)
				outcome := f.command("record_outcome", "outcome")
				outcome.Outcome = "successful"
				end := time.Now()
				outcome.ActualEnd = &end
				f.apply(t, outcome)
				summary := "Validated deployment"
				result, err := f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful", SuccessSummary: &summary}, f.command("pir", "pir").Meta)
				require.NoError(t, err)
				pirID = result.PIRID
				review := f.command("review", "review")
				review.PIRID = pirID
				f.apply(t, review)
			}
			meta := f.command("pir", "race").Meta
			req := &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}
			results := make(chan error, 2)
			synchronizeChangeOwnerReads(t, f)
			go func() {
				var err error
				if mode == "close_update" {
					summary := "New review facts"
					_, err = f.pirOwner.UpdatePIR(f.ctx, pirID, &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, SuccessSummary: &summary}, meta)
				} else if mode == "close_delete" {
					_, err = f.pirOwner.DeletePIR(f.ctx, pirID, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, meta)
				} else {
					_, err = f.pirOwner.CreatePIR(f.ctx, req, meta)
				}
				results <- err
			}()
			go func() {
				var err error
				switch mode {
				case "metadata":
					title := "Concurrent title"
					_, err = f.owner.ApplyMetadata(f.ctx, changedomain.MetadataCommand{Meta: meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}})
				case "task":
					_, err = f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: changedomain.Command{Meta: meta, ChangeID: f.c.ID, Action: "assess", Evidence: "Assessed"}, TaskID: task.TaskID})
				case "same_key", "different_key":
					copy := meta
					if mode == "different_key" {
						copy.OperationID += "-other"
					}
					_, err = f.pirOwner.CreatePIR(f.ctx, req, copy)
				default:
					close := changedomain.Command{Meta: meta, ChangeID: f.c.ID, Action: "close", Evidence: "Closure checked", PIRID: pirID}
					_, err = f.owner.ApplyCommand(f.ctx, close)
				}
				results <- err
			}()
			a, b := <-results, <-results
			if mode == "same_key" {
				require.NoError(t, a)
				require.NoError(t, b)
			} else {
				require.NotEqual(t, a == nil, b == nil, "one owner must win: %v / %v", a, b)
			}
			item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, meta.ExpectedVersion+1, item.Version)
			if item.Status == "completed" {
				require.Equal(t, "Validated deployment", f.client.ChangePIR.GetX(f.ctx, pirID).SuccessSummary)
			}
			if mode == "task" && f.client.ChangePIR.Query().CountX(f.ctx) > 0 {
				// The loser can proceed with a new explicit current version; no process variable write in PIR.
				cmd := changedomain.TaskCommand{Command: f.command("assess", "after-pir"), TaskID: task.TaskID}
				result, err := f.owner.CompleteChangeTask(f.ctx, cmd)
				require.NoError(t, err)
				require.NotNil(t, result.Result)
				require.Equal(t, item.Version+1, result.Result.Version)
			}
		})
	}
}

func TestWorkItemChangePIRHTTPBinding(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := changedomain.NewHandler(f.owner)
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Next()
	})
	router.POST("/changes/:id/pir", handler.CreatePIR)
	invoke := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", fmt.Sprintf("/changes/%d/pir", f.c.ID), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	missing := invoke(`{"overallResult":"successful","operationId":"missing"}`)
	require.Equal(t, 400, missing.Code)
	body := fmt.Sprintf(`{"overallResult":"successful","expectedVersion":1,"operationId":"http-create","actorId":99999,"tenantId":99999}`)
	forged := invoke(body)
	require.Equal(t, 400, forged.Code)
	require.Zero(t, f.client.ChangePIR.Query().CountX(f.ctx))
	body = `{"overallResult":"successful","expectedVersion":1,"operationId":"http-create"}`
	success := invoke(body)
	require.Equal(t, 200, success.Code, success.Body.String())
	var decoded struct {
		Data struct {
			PIRID   int `json:"pirId"`
			Version int `json:"version"`
		}
	}
	require.NoError(t, json.Unmarshal(success.Body.Bytes(), &decoded))
	require.Positive(t, decoded.Data.PIRID)
	require.Equal(t, 2, decoded.Data.Version)
	require.Equal(t, f.actor.ID, f.client.ChangePIR.GetX(f.ctx, decoded.Data.PIRID).ReviewerID)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	denied := invoke(body)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}
func TestWorkItemChangePIRAuditRollback(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			var id int
			if action != "create" {
				r, err := f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}, f.command("pir", "create").Meta)
				require.NoError(t, err)
				id = r.PIRID
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			f.runtime.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if _, ok := m.(*ent.AuditLogMutation); ok {
						return nil, errors.New("PIR receipt unavailable")
					}
					return next.Mutate(ctx, m)
				})
			})
			meta := f.command("pir", "rollback").Meta
			var err error
			switch action {
			case "create":
				_, err = f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}, meta)
			case "update":
				value := "Changed"
				_, err = f.pirOwner.UpdatePIR(f.ctx, id, &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, SuccessSummary: &value}, meta)
			case "delete":
				_, err = f.pirOwner.DeletePIR(f.ctx, id, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, meta)
			}
			require.ErrorContains(t, err, "PIR receipt unavailable")
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, before.Version, after.Version)
			require.True(t, before.UpdatedAt.Equal(after.UpdatedAt))
			if action == "create" {
				require.Zero(t, f.client.ChangePIR.Query().CountX(f.ctx))
			} else {
				require.Empty(t, f.client.ChangePIR.GetX(f.ctx, id).SuccessSummary)
			}
		})
	}
}

func TestWorkItemChangePIRDeletePermission(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	role, _ := setChangeWriter(t, f, false)
	created, err := f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}, f.command("pir", "create").Meta)
	require.NoError(t, err)
	req := &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}
	meta := f.command("pir", "delete").Meta
	_, err = f.pirOwner.DeletePIR(f.ctx, created.PIRID, req, meta)
	require.Error(t, err, "write permission does not authorize PIR deletion")
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:delete").SetName("Delete").SetResource("change").SetAction("delete").SaveX(f.ctx)
	grant := f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	result, err := f.pirOwner.DeletePIR(f.ctx, created.PIRID, req, meta)
	require.NoError(t, err)
	require.Equal(t, created.Version+1, result.Version)
	f.client.RolePermission.DeleteOne(grant).ExecX(f.ctx)
	_, err = f.pirOwner.DeletePIR(f.ctx, created.PIRID, req, meta)
	require.Error(t, err, "delete replay must recheck current permission")
}

func TestWorkItemChangePIRTaskVersionRefresh(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeDefaultTask(t, f)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	stale := changedomain.TaskCommand{Command: f.command("assess", "stale-task"), TaskID: task.TaskID}
	created, err := f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "failed"}, f.command("pir", "create").Meta)
	require.NoError(t, err)
	require.Equal(t, instance.Variables, f.client.ProcessInstance.GetX(f.ctx, instance.ID).Variables, "PIR never writes process variables")
	_, err = f.owner.CompleteChangeTask(f.ctx, stale)
	require.Error(t, err)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
	fresh := changedomain.TaskCommand{Command: f.command("assess", "fresh-task"), TaskID: task.TaskID}
	result, err := f.owner.CompleteChangeTask(f.ctx, fresh)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, created.Version+1, result.Result.Version)
}

func TestWorkItemChangePIRExistingRowConcurrentReplay(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			created, err := f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}, f.command("pir", "create").Meta)
			require.NoError(t, err)
			meta := f.command("pir", "contended-"+action).Meta
			summary := "Concurrent review"
			invoke := func(version int) (service.PIRMutationResult, error) {
				copy := meta
				copy.ExpectedVersion = version
				if action == "update" {
					return f.pirOwner.UpdatePIR(f.ctx, created.PIRID, &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, SuccessSummary: &summary}, copy)
				}
				return f.pirOwner.DeletePIR(f.ctx, created.PIRID, &dto.DeleteChangePIRRequest{ChangeID: f.c.ID}, copy)
			}
			synchronizeChangeOwnerReads(t, f)
			type attempt struct {
				result service.PIRMutationResult
				err    error
			}
			results := make(chan attempt, 2)
			for range 2 {
				go func() { result, err := invoke(meta.ExpectedVersion); results <- attempt{result, err} }()
			}
			a, b := <-results, <-results
			require.NoError(t, a.err)
			require.NoError(t, b.err)
			require.Equal(t, created.PIRID, a.result.PIRID)
			require.Equal(t, a.result.PIRID, b.result.PIRID)
			require.Equal(t, meta.ExpectedVersion+1, a.result.Version)
			require.Equal(t, a.result.Version, b.result.Version)
			require.Equal(t, a.result.Status, b.result.Status)
			require.NotEqual(t, a.result.Replayed, b.result.Replayed, "exactly the losing request replays")
			require.Equal(t, meta.ExpectedVersion+1, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.pir_"+action)).CountX(f.ctx))
			if action == "delete" {
				require.Zero(t, f.client.ChangePIR.Query().CountX(f.ctx))
			} else {
				require.Equal(t, summary, f.client.ChangePIR.GetX(f.ctx, created.PIRID).SuccessSummary)
			}
			_, err = invoke(meta.ExpectedVersion + 1)
			require.ErrorContains(t, err, "operationId", "same key cannot describe a different expected version")
			f.actor.Update().SetActive(false).ExecX(f.ctx)
			_, err = invoke(meta.ExpectedVersion)
			require.Error(t, err, "current authorization precedes immutable replay")
		})
	}
}
