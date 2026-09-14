//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/middleware"
)

func changeHTTPFixture(f *changeLifecycleFixture) (*gin.Engine, *changedomain.Handler) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set("role", f.actor.Role)
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	})
	return r, changedomain.NewHandler(f.owner)
}

func TestWorkItemChangeHTTPMetadataRiskAssignment(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	r, h := changeHTTPFixture(f)
	r.PUT("/changes/:id", h.UpdateChange)
	r.PUT("/changes/:id/risk", h.UpdateRisk)
	r.POST("/changes/:id/assign", h.AssignChange)
	base := fmt.Sprintf("/changes/%d", f.c.ID)
	for _, body := range []string{`{"expectedVersion":1,"operationId":"bad","status":"completed"}`, `{"expectedVersion":1,"operationId":"bad","actorId":999,"title":"x"}`, `{"expectedVersion":1,"operationId":"bad","tenantId":999,"title":"x"}`, `{"expectedVersion":0,"operationId":"bad","title":"x"}`} {
		w := changeHTTPCall(r, "PUT", base, body)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	w := changeHTTPCall(r, "PUT", base+"/risk", `{"expectedVersion":1,"operationId":"risk","riskLevel":"high","riskDescription":"observed risk","impactAnalysis":"impact","mitigationMeasures":"mitigate","contingencyPlan":"restore","riskOwner":"operations"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	risk, err := f.owner.GetRisk(f.ctx, f.c.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "high", risk.RiskLevel)
	require.Equal(t, "observed risk", risk.RiskDescription)
	require.Equal(t, "high", f.client.Change.GetX(f.ctx, f.c.ID).RiskLevel)
	w = changeHTTPCall(r, "POST", base+"/assign", fmt.Sprintf(`{"expectedVersion":2,"operationId":"assign","assigneeId":%d}`, f.actor.ID))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, f.actor.ID, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).AssigneeID)
	w = changeHTTPCall(r, "POST", base+"/assign", `{"expectedVersion":3,"operationId":"foreign","assigneeId":999999}`)
	require.Equal(t, 400, w.Code, w.Body.String())
	f.runtime.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if a, ok := m.(*ent.AuditLogMutation); ok {
				if action, _ := a.Action(); action == "change.metadata" {
					return nil, errors.New("receipt unavailable")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	w = changeHTTPCall(r, "PUT", base+"/risk", `{"expectedVersion":3,"operationId":"rollback","riskDescription":"changed"}`)
	require.Equal(t, 500, w.Code, w.Body.String())
	risk, err = f.owner.GetRisk(f.ctx, f.c.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "observed risk", risk.RiskDescription)
	require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
}

func TestWorkItemChangeHTTPTaskProgress(t *testing.T) {
	for _, mode := range []string{"complete", "msp", "pending", "acceptance_rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			if mode == "msp" {
				f.actor, _, _ = seedChangeHTTPMSP(t, f)
			}
			xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
			require.NoError(t, err)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			r, h := changeHTTPFixture(f)
			r.POST("/changes/:id/submit", h.ExecuteAction)
			r.POST("/changes/:id/assess", h.ExecuteAction)
			r.GET("/changes/:id/task-progress", h.GetTaskProgress)
			r.GET("/changes/:id", h.GetChange)
			base := fmt.Sprintf("/changes/%d", f.c.ID)
			w := changeHTTPCall(r, "POST", base+"/submit", `{"expectedVersion":1,"operationId":"http-submit"}`)
			require.Equal(t, 200, w.Code, w.Body.String())
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
			view := changeHTTPCall(r, "GET", base, "")
			require.Equal(t, 200, view.Code, view.Body.String())
			var projection struct {
				Data struct {
					Version      int               `json:"version"`
					CurrentTasks map[string]string `json:"currentTasks"`
				}
			}
			require.NoError(t, json.Unmarshal(view.Body.Bytes(), &projection))
			require.Equal(t, 2, projection.Data.Version)
			require.Equal(t, task.TaskID, projection.Data.CurrentTasks["assess"])
			f.runtime.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if a, ok := m.(*ent.AuditLogMutation); ok {
						action, _ := a.Action()
						if mode == "pending" && action == "change.assess" || mode == "acceptance_rollback" && action == "change.task_completion" {
							return nil, errors.New("receipt unavailable")
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			body := fmt.Sprintf(`{"expectedVersion":2,"operationId":"http-assess","taskId":%q,"evidence":"verified assessment"}`, task.TaskID)
			w = changeHTTPCall(r, "POST", base+"/assess", body)
			if mode == "acceptance_rollback" {
				require.Equal(t, 500, w.Code, w.Body.String())
				require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				return
			}
			status := 200
			if mode == "pending" {
				status = 202
			}
			require.Equal(t, status, w.Code, w.Body.String())
			var response struct {
				Code int                       `json:"code"`
				Data changedomain.TaskProgress `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Zero(t, response.Code)
			if mode == "pending" {
				require.Nil(t, response.Data.Result)
			} else {
				require.Equal(t, 3, response.Data.Result.Version)
			}
			replay := changeHTTPCall(r, "POST", base+"/assess", body)
			require.Equal(t, status, replay.Code, replay.Body.String())
			progress := changeHTTPCall(r, "GET", base+"/task-progress?operationId=http-assess&action=assess", "")
			require.Equal(t, status, progress.Code, progress.Body.String())
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.task_completion")).CountX(f.ctx))
			if mode == "pending" {
				row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
				f.client.ProcessCallbackOutbox.UpdateOneID(row.ID).SetStatus("blocked").SetLastErrorClass("handler_contract").ExecX(f.ctx)
				blocked := changeHTTPCall(r, "GET", base+"/task-progress?operationId=http-assess&action=assess", "")
				require.Equal(t, 409, blocked.Code, blocked.Body.String())
				require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.Status("blocked")).CountX(f.ctx))
			}
			f.actor.Update().SetActive(false).ExecX(f.ctx)
			denied := changeHTTPCall(r, "POST", base+"/assess", body)
			require.Equal(t, 403, denied.Code, denied.Body.String())
		})
	}
}

func changeHTTPCall(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestWorkItemChangeHTTPRequiresExplicitIdentity(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	r, h := changeHTTPFixture(f)
	r.PUT("/changes/:id", h.UpdateChange)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	w := changeHTTPCall(r, "PUT", fmt.Sprintf("/changes/%d", f.c.ID), `{"title":"Missing version"}`)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
}

func TestWorkItemChangeHTTPRiskAssignmentBoundaries(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	native := f.actor
	msp, _, role := seedChangeHTTPMSP(t, f)
	inactive := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("inactive-assignee").SetName("Inactive").SetEmail("inactive@example.test").SetPasswordHash("test").SetActive(false).SaveX(f.ctx)
	r, h := changeHTTPFixture(f)
	r.PUT("/changes/:id", h.UpdateChange)
	r.PUT("/changes/:id/risk", h.UpdateRisk)
	r.POST("/changes/:id/assign", h.AssignChange)
	base := fmt.Sprintf("/changes/%d", f.c.ID)
	for _, id := range []int{inactive.ID, msp.ID} {
		for _, endpoint := range []string{"metadata", "assign"} {
			method, path := "PUT", base
			if endpoint == "assign" {
				method, path = "POST", base+"/assign"
			}
			w := changeHTTPCall(r, method, path, fmt.Sprintf(`{"expectedVersion":1,"operationId":%q,"assigneeId":%d}`, fmt.Sprintf("invalid-%s-%d", endpoint, id), id))
			require.Equal(t, 400, w.Code, w.Body.String())
		}
	}
	f.actor = msp
	w := changeHTTPCall(r, "PUT", base, fmt.Sprintf(`{"expectedVersion":1,"operationId":"msp-assign","assigneeId":%d}`, native.ID))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, native.ID, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).AssigneeID)
	role.Update().SetIsActive(false).ExecX(f.ctx)
	for _, path := range []string{base, base + "/risk"} {
		w := changeHTTPCall(r, "PUT", path, `{"expectedVersion":2,"operationId":"no-write","riskLevel":"high"}`)
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	role.Update().SetIsActive(true).ExecX(f.ctx)
	f.actor = native
	w = changeHTTPCall(r, "PUT", base+"/risk", `{"expectedVersion":2,"operationId":"risk-facts","riskLevel":"high","riskDescription":"canonical observed risk"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	task := prepareChangeDefaultTask(t, f)
	before := f.client.Change.GetX(f.ctx, f.c.ID)
	result, err := f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("assess", "risk-assess"), TaskID: task.TaskID})
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	assessed := f.client.Change.GetX(f.ctx, f.c.ID)
	require.NotEmpty(t, assessed.AssessmentDigest)
	require.Equal(t, before.RiskLevel, assessed.RiskLevel)
	for _, patch := range []string{`"riskLevel":"low"`, `"riskDescription":"rewritten"`, `"impactAnalysis":"rewritten"`, `"mitigationMeasures":"rewritten"`, `"contingencyPlan":"rewritten"`, `"riskOwner":"rewritten"`, `"riskReviewDate":"2026-09-09T12:00:00Z"`} {
		body := fmt.Sprintf(`{"expectedVersion":%d,"operationId":"frozen",%s}`, result.Result.Version, patch)
		w := changeHTTPCall(r, "PUT", base+"/risk", body)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	require.Equal(t, assessed.AssessmentDigest, f.client.Change.GetX(f.ctx, f.c.ID).AssessmentDigest)
	risk, err := f.owner.GetRisk(f.ctx, f.c.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "high", risk.RiskLevel)
	require.Equal(t, "canonical observed risk", risk.RiskDescription)
}

func TestWorkItemChangeHTTPPIRMutations(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	r, h := changeHTTPFixture(f)
	r.POST("/changes/:id/pir", h.CreatePIR)
	r.PUT("/changes/pir/:id", h.UpdatePIR)
	r.DELETE("/changes/pir/:id", h.DeletePIR)
	create := fmt.Sprintf("/changes/%d/pir", f.c.ID)
	for _, bad := range []string{`{"overallResult":"successful","expectedVersion":1,"operationId":"forged","actorId":999}`, `{"overallResult":"successful","expectedVersion":1,"operationId":"foreign","changeId":999}`, `{"overallResult":"successful","ExpectedVersion":1,"operationId":"wrong-case"}`, `{"overallResult":"successful","expectedVersion":1,"operationId":"null","successSummary":null}`} {
		w := changeHTTPCall(r, "POST", create, bad)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	body := `{"overallResult":"successful","expectedVersion":1,"operationId":"pir-create"}`
	w := changeHTTPCall(r, "POST", create, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	var reply struct {
		Data struct {
			PIRID   int `json:"pirId"`
			Version int `json:"version"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reply))
	require.Positive(t, reply.Data.PIRID)
	require.Equal(t, 2, reply.Data.Version)
	pirID := reply.Data.PIRID
	path := fmt.Sprintf("/changes/pir/%d", pirID)
	w = changeHTTPCall(r, "PUT", path, fmt.Sprintf(`{"changeId":%d,"expectedVersion":2,"operationId":"pir-edit","successSummary":"verified"}`, f.c.ID))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "verified", f.client.ChangePIR.GetX(f.ctx, pirID).SuccessSummary)
	w = changeHTTPCall(r, "DELETE", path, fmt.Sprintf(`{"changeId":%d,"expectedVersion":2,"operationId":"stale-delete"}`, f.c.ID))
	require.Equal(t, 409, w.Code, w.Body.String())
	body = fmt.Sprintf(`{"changeId":%d,"expectedVersion":3,"operationId":"pir-delete"}`, f.c.ID)
	w = changeHTTPCall(r, "DELETE", path, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Zero(t, f.client.ChangePIR.Query().CountX(f.ctx))
	w = changeHTTPCall(r, "DELETE", path, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 4, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
}

func TestWorkItemChangeHTTPPIRAuditRollback(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			r, h := changeHTTPFixture(f)
			r.POST("/changes/:id/pir", h.CreatePIR)
			r.PUT("/changes/pir/:id", h.UpdatePIR)
			r.DELETE("/changes/pir/:id", h.DeletePIR)
			path := fmt.Sprintf("/changes/%d/pir", f.c.ID)
			method := "POST"
			if action != "create" {
				w := changeHTTPCall(r, "POST", path, `{"expectedVersion":1,"operationId":"seed","overallResult":"successful"}`)
				require.Equal(t, 200, w.Code, w.Body.String())
				pir := f.client.ChangePIR.Query().OnlyX(f.ctx)
				path = fmt.Sprintf("/changes/pir/%d", pir.ID)
				method = "PUT"
				if action == "delete" {
					method = "DELETE"
				}
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			count := f.client.ChangePIR.Query().CountX(f.ctx)
			f.runtime.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if _, ok := m.(*ent.AuditLogMutation); ok {
						return nil, errors.New("PIR audit unavailable")
					}
					return next.Mutate(ctx, m)
				})
			})
			extra := `,"overallResult":"successful"`
			if action == "update" {
				extra = `,"successSummary":"changed"`
			}
			if action == "delete" {
				extra = ""
			}
			body := fmt.Sprintf(`{"changeId":%d,"expectedVersion":%d,"operationId":"rollback"%s}`, f.c.ID, before.Version, extra)
			w := changeHTTPCall(r, method, path, body)
			require.Equal(t, 500, w.Code, w.Body.String())
			require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			require.Equal(t, count, f.client.ChangePIR.Query().CountX(f.ctx))
			if action != "create" {
				require.Empty(t, f.client.ChangePIR.Query().OnlyX(f.ctx).SuccessSummary)
			}
		})
	}
}

func TestWorkItemChangeHTTPCancelAtomic(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			task := prepareChangeDefaultTask(t, f)
			r, h := changeHTTPFixture(f)
			r.POST("/changes/:id/cancel", h.ExecuteAction)
			if fail {
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if a, ok := m.(*ent.AuditLogMutation); ok {
							if action, _ := a.Action(); action == "change.cancel" {
								return nil, errors.New("cancel audit unavailable")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			path := fmt.Sprintf("/changes/%d/cancel", f.c.ID)
			body := fmt.Sprintf(`{"expectedVersion":%d,"operationId":"cancel","evidence":"operator cancellation"}`, before.Version)
			w := changeHTTPCall(r, "POST", path, body)
			if fail {
				require.Equal(t, 500, w.Code, w.Body.String())
				require.Equal(t, "running", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
				require.Equal(t, task.Status, f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			} else {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, "cancelled", f.client.Ticket.GetX(f.ctx, before.ID).Status)
				require.Equal(t, "terminated", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
				again := changeHTTPCall(r, "POST", path, body)
				require.Equal(t, 200, again.Code, again.Body.String())
				require.Equal(t, before.Version+1, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			}
		})
	}
}

func TestWorkItemChangeHTTPAssessmentBindsRiskDetails(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			cab := seedChangeCABActor(t, f)
			r, h := changeHTTPFixture(f)
			r.PUT("/changes/:id/risk", h.UpdateRisk)
			r.POST("/changes/:id/approve", h.ExecuteAction)
			w := changeHTTPCall(r, "PUT", fmt.Sprintf("/changes/%d/risk", f.c.ID), `{"expectedVersion":1,"operationId":"risk","riskDescription":"reviewed narrative"}`)
			require.Equal(t, 200, w.Code, w.Body.String())
			prepareChangeDefaultTask(t, f)
			completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
			if changed {
				_, err := f.client.ExecContext(f.ctx, `UPDATE change_risk_assessments SET risk_description='changed outside owner' WHERE change_id=$1 AND tenant_id=$2`, f.c.ID, f.tenant.ID)
				require.NoError(t, err)
			}
			f.actor = cab
			_, actions, _, viewErr := f.owner.GetChangeActionView(f.ctx, f.c.ID, f.command("read", "risk-view").Meta)
			require.NoError(t, viewErr)
			require.Equal(t, !changed, actions["approve"].Allowed)
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			w = changeHTTPCall(r, "POST", fmt.Sprintf("/changes/%d/approve", f.c.ID), fmt.Sprintf(`{"expectedVersion":%d,"operationId":"cab","taskId":%q,"evidence":"reviewed"}`, before.Version, task.TaskID))
			if changed {
				require.Equal(t, 409, w.Code, w.Body.String())
				require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, before.ID).Status)
				require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("change.authorize")).CountX(f.ctx))
				row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx)
				require.Equal(t, "blocked", row.Status)
				require.Equal(t, "handler_contract", row.LastErrorClass)
				count, err := f.engine.ProcessPendingCallbacks(f.ctx, "invalid-assessment-worker", 10)
				require.NoError(t, err)
				require.Zero(t, count)
				require.Equal(t, row.AttemptCount, f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).AttemptCount)

			} else {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, "approved", f.client.Ticket.GetX(f.ctx, before.ID).Status)
			}
		})
	}
}

func TestWorkItemChangeHTTPAssessedEmptyRiskPreservesAbsence(t *testing.T) {
	for _, value := range []string{"", " \t\n "} {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			cab := seedChangeCABActor(t, f)
			prepareChangeDefaultTask(t, f)
			completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
			assessed := f.client.Change.GetX(f.ctx, f.c.ID)
			require.NotEmpty(t, assessed.AssessmentDigest)
			r, h := changeHTTPFixture(f)
			r.PUT("/changes/:id", h.UpdateChange)
			r.POST("/changes/:id/approve", h.ExecuteAction)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			body, err := json.Marshal(map[string]any{"expectedVersion": before.Version, "operationId": "title-empty-risk", "title": "Reviewed change title", "riskDescription": value, "impactAnalysis": value, "mitigationMeasures": value, "contingencyPlan": value, "riskOwner": value})
			require.NoError(t, err)
			w := changeHTTPCall(r, "PUT", fmt.Sprintf("/changes/%d", f.c.ID), string(body))
			require.Equal(t, 200, w.Code, w.Body.String())
			var count int
			require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM change_risk_assessments WHERE change_id=$1 AND tenant_id=$2`, f.c.ID, f.tenant.ID).Scan(&count))
			require.Zero(t, count, "unchanged empty risk patch must not create a detail record")
			require.Equal(t, assessed.AssessmentDigest, f.client.Change.GetX(f.ctx, f.c.ID).AssessmentDigest)
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, "Reviewed change title", after.Title)
			require.Equal(t, before.Version+1, after.Version)
			f.actor = cab
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
			w = changeHTTPCall(r, "POST", fmt.Sprintf("/changes/%d/approve", f.c.ID), fmt.Sprintf(`{"expectedVersion":%d,"operationId":"cab-after-title","taskId":%q,"evidence":"reviewed"}`, after.Version, task.TaskID))
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, "approved", f.client.Ticket.GetX(f.ctx, before.ID).Status)
		})
	}
}

func TestWorkItemChangeHTTPReceiptWithoutCallbackProgress(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	applied := completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	require.NotNil(t, applied.Result)
	row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKey(applied.ExecutionKey)).OnlyX(f.ctx)
	// Simulate lost continuation tracking only after the actual owner committed its receipt.
	f.client.ProcessCallbackOutbox.DeleteOneID(row.ID).ExecX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	auditCount := f.client.AuditLog.Query().CountX(f.ctx)
	taskCount := f.client.ProcessTask.Query().CountX(f.ctx)
	r, h := changeHTTPFixture(f)
	r.GET("/changes/:id/task-progress", h.GetTaskProgress)
	w := changeHTTPCall(r, "GET", fmt.Sprintf("/changes/%d/task-progress?operationId=consumer-assess&action=assess", f.c.ID), "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		Data changedomain.TaskProgress `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "effect_applied", response.Data.Progress)
	require.Equal(t, "continuation_progress_unavailable", response.Data.Reason)
	require.Equal(t, applied.Result, response.Data.Result)
	require.Equal(t, applied.TaskID, response.Data.TaskID)
	require.Equal(t, applied.ExecutionKey, response.Data.ExecutionKey)
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
	require.Equal(t, auditCount, f.client.AuditLog.Query().CountX(f.ctx))
	require.Equal(t, taskCount, f.client.ProcessTask.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKey(applied.ExecutionKey)).CountX(f.ctx))
}
