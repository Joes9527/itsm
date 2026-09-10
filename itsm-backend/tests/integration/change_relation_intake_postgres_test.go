//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	changeDomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalogDomain "itsm-backend/handlers/service_catalog"
	standardDomain "itsm-backend/handlers/standard_change"
	"itsm-backend/middleware"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

func changeRelationIntakeHTTP(t *testing.T, standard bool) (*relationFixture, *gin.Engine, string, string) {
	t.Helper()
	f, _, _, _ := newIntakeRelationFixture(t)
	for _, table := range []string{"changes", "standard_changes"} {
		_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT,INSERT,UPDATE ON %s TO %q", table, f.runtimeRole))
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT USAGE ON SEQUENCE %s_id_seq TO %q", table, f.runtimeRole))
		require.NoError(t, err)
	}
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("change").SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(f.ctx)
	logger := zap.NewNop().Sugar()
	owner := changeDomain.NewService(changeDomain.NewEntRepository(f.runtime.Tenant, nil), f.runtime.Tenant, logger)
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(owner))
	resolver := intake.NewResolver(catalogDomain.NewService(nil, f.runtime.Tenant, logger, nil), service.NewProcessBindingService(f.runtime.Tenant), service.NewConfigurationItemService(f.runtime.Tenant, logger, nil, nil), service.NewTicketCategoryService(f.runtime.Tenant))
	app := intake.NewService(f.runtime.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), f.runtime.IntakeDirectorySnapshot())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set("role", authorization.EffectiveSessionRole(f.actor))
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.tenant.ID})
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	})
	source := fmt.Sprintf(`"sourceRelations":[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"metadata":{"required":true}}]`, f.problem.ID)
	path := "/api/v1/changes"
	body := `{"title":"new change","justification":"fix root cause","type":"normal","impactScope":"low","riskLevel":"low","implementationPlan":"deploy","rollbackPlan":"restore",` + source + `}`
	if standard {
		template := f.client.StandardChange.Create().SetTenantID(f.tenant.ID).SetCreatedBy(f.actor.ID).SetTitle("approved standard").SetDescription("standard description").SetJustification("routine").SetCategory("routine").SetImplementationPlan("deploy template").SetRollbackPlan("restore template").SetRiskLevel("low").SetImpactScope("low").SetIsActive(true).SaveX(f.ctx)
		h := standardDomain.NewHandler(f.runtime.Tenant, logger)
		h.SetCreationApplication(app)
		r.POST("/api/v1/standard-changes/:id/instantiate", h.InstantiateStandardChange)
		path = fmt.Sprintf("/api/v1/standard-changes/%d/instantiate", template.ID)
		body = `{` + source + `}`
	} else {
		h := changeDomain.NewHandler(owner)
		h.SetCreationApplication(app)
		r.POST(path, h.CreateChange)
	}
	return f, r, path, body
}

func createChangeRelationHTTP(r *gin.Engine, path, body, key string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(w, req)
	return w
}

func TestChangeSourceRelationsHTTPNormalAndStandard(t *testing.T) {
	for _, standard := range []bool{false, true} {
		t.Run(fmt.Sprintf("standard_%v", standard), func(t *testing.T) {
			f, r, path, body := changeRelationIntakeHTTP(t, standard)
			w := createChangeRelationHTTP(r, path, body, "change-linked")
			require.Equal(t, 201, w.Code, w.Body.String())
			var response struct {
				Code int                           `json:"code"`
				Data creation.CreateWorkItemResult `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Zero(t, response.Code)
			require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
			require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, response.Data.WorkItemID).Version)
			relation := f.client.WorkItemRelation.Query().OnlyX(f.ctx)
			require.Equal(t, f.problem.ID, relation.SourceWorkItemID)
			require.Equal(t, response.Data.WorkItemID, relation.TargetWorkItemID)
			require.Equal(t, "resolved_by_change", relation.RelationType)
			require.True(t, relation.Metadata.Required)
			require.Equal(t, 1, f.client.Change.Query().CountX(f.ctx))
			w = createChangeRelationHTTP(r, path, body, "change-linked")
			require.Equal(t, 200, w.Code, w.Body.String())
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.True(t, response.Data.Replayed)
			require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
		})
	}
}

func TestChangeCreationRetiresVersionlessRelationInput(t *testing.T) {
	f, r, path, body := changeRelationIntakeHTTP(t, false)
	body = strings.Split(body, `,"sourceRelations"`)[0] + `,"relatedTickets":[]}`
	w := createChangeRelationHTTP(r, path, body, "legacy-related-input")
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Zero(t, f.client.Change.Query().CountX(f.ctx))
}

func TestChangeSourceRelationsStrictHTTP(t *testing.T) {
	for _, standard := range []bool{false, true} {
		t.Run(fmt.Sprintf("standard_%v", standard), func(t *testing.T) {
			f, r, path, body := changeRelationIntakeHTTP(t, standard)
			start := strings.Index(body, `"sourceRelations":`)
			prefix := body[:start]
			for name, source := range map[string]string{
				"required_wrong_type": fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"related_to","expectedVersion":1,"metadata":{"required":true}}]`, f.problem.ID),
				"unknown_member":      fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"version":1,"metadata":{}}]`, f.problem.ID),
				"duplicate_version":   fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"expectedVersion":1,"metadata":{}}]`, f.problem.ID),
				"missing_version":     fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","metadata":{}}]`, f.problem.ID),
				"unknown_relation":    fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"future_relation","expectedVersion":1,"metadata":{}}]`, f.problem.ID),
				"null_array":          `null`, "null_item": `[null]`,
				"null_metadata": fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"metadata":null}]`, f.problem.ID),
				"null_required": fmt.Sprintf(`[{"sourceWorkItemId":%d,"relationType":"resolved_by_change","expectedVersion":1,"metadata":{"required":null}}]`, f.problem.ID),
			} {
				t.Run(name, func(t *testing.T) {
					w := createChangeRelationHTTP(r, path, prefix+`"sourceRelations":`+source+`}`, "strict-"+name)
					require.Equal(t, 400, w.Code, w.Body.String())
				})
			}
			require.Zero(t, f.client.Change.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
		})
	}
}

func TestChangeSourceRelationsHTTPAtomicRollback(t *testing.T) {
	for _, standard := range []bool{false, true} {
		for _, stage := range []string{"relation", "relation_audit"} {
			t.Run(fmt.Sprintf("standard_%v_%s", standard, stage), func(t *testing.T) {
				f, r, path, body := changeRelationIntakeHTTP(t, standard)
				tickets, receipts, audits := f.client.Ticket.Query().CountX(f.ctx), f.client.IntakeRequest.Query().CountX(f.ctx), f.client.AuditLog.Query().CountX(f.ctx)
				reached := false
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if stage == "relation_audit" {
							action, _ := m.Field("action")
							if action != "work_item.relation_added" {
								return next.Mutate(ctx, m)
							}
						}
						value, err := next.Mutate(ctx, m)
						if err != nil {
							return value, err
						}
						reached = true
						return nil, errors.New("private injected Change post-extension failure")
					})
				}
				if stage == "relation" {
					f.runtime.Tenant.WorkItemRelation.Use(hook)
				} else {
					f.runtime.Tenant.AuditLog.Use(hook)
				}
				w := createChangeRelationHTTP(r, path, body, "change-rollback")
				require.Equal(t, 503, w.Code, w.Body.String())
				require.True(t, reached, w.Body.String())
				require.NotContains(t, w.Body.String(), "private injected")
				require.Equal(t, tickets, f.client.Ticket.Query().CountX(f.ctx))
				require.Equal(t, receipts, f.client.IntakeRequest.Query().CountX(f.ctx))
				require.Equal(t, audits, f.client.AuditLog.Query().CountX(f.ctx))
				require.Zero(t, f.client.Change.Query().CountX(f.ctx))
				require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
				require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
			})
		}
	}
}

func TestChangeSourceRelationsHTTPConflictAndCurrentReplay(t *testing.T) {
	for _, standard := range []bool{false, true} {
		t.Run(fmt.Sprintf("standard_%v", standard), func(t *testing.T) {
			f, r, path, body := changeRelationIntakeHTTP(t, standard)
			w := createChangeRelationHTTP(r, path, strings.Replace(body, `"expectedVersion":1`, `"expectedVersion":99`, 1), "stale-source")
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Zero(t, f.client.Change.Query().CountX(f.ctx))
			w = createChangeRelationHTTP(r, path, body, "")
			require.Equal(t, 400, w.Code, w.Body.String())
			w = createChangeRelationHTTP(r, path, body, "valid-current")
			require.Equal(t, 201, w.Code, w.Body.String())
			// The HTTP middleware continues claiming super_admin; only persisted authority matters on replay.
			f.client.User.UpdateOneID(f.actor.ID).SetRole("viewer").ExecX(f.ctx)
			w = createChangeRelationHTTP(r, path, body, "valid-current")
			require.Equal(t, 401, w.Code, w.Body.String())
			require.Equal(t, 1, f.client.Change.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
			require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
		})
	}
}

func TestChangeSourceRelationsHTTPSelectedTenant(t *testing.T) {
	for _, standard := range []bool{false, true} {
		for _, restricted := range []bool{false, true} {
			t.Run(fmt.Sprintf("standard_%v_restricted_%v", standard, restricted), func(t *testing.T) {
				f, r, path, body := changeRelationIntakeHTTP(t, standard)
				requester := f.actor.ID
				f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
				provider := f.client.Tenant.Create().SetCode("change-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
				actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("change-operator").SetName("Operator").SetEmail("change-operator@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
				if restricted {
					actor = actor.Update().SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
					f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").ExecX(f.ctx)
					role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
					for _, resource := range []string{"problem", "change", "standard_change"} {
						actions := []string{"read"}
						if resource != "standard_change" {
							actions = append(actions, "write")
						}
						if resource == "change" {
							actions = append(actions, "create_on_behalf")
						}
						for _, action := range actions {
							permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":" + action).SetResource(resource).SetAction(action).SetName(action).SaveX(f.ctx)
							f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(f.ctx)
						}
					}
				}
				f.actor = actor
				body = strings.TrimSuffix(body, "}") + fmt.Sprintf(`,"requesterId":%d}`, requester)
				w := createChangeRelationHTTP(r, path, body, "selected-change")
				if restricted {
					require.Equal(t, 404, w.Code, w.Body.String())
					require.Zero(t, f.client.Change.Query().CountX(f.ctx))
					require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
					return
				}
				require.Equal(t, 201, w.Code, w.Body.String())
				var response struct {
					Data creation.CreateWorkItemResult `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				target := f.client.Ticket.GetX(f.ctx, response.Data.WorkItemID)
				require.Equal(t, requester, target.RequesterID)
				require.Equal(t, actor.ID, target.OpenedByID)
				require.Zero(t, target.AssigneeID)
				require.Equal(t, f.tenant.ID, target.TenantID)
				relation := f.client.WorkItemRelation.Query().OnlyX(f.ctx)
				require.Equal(t, actor.ID, relation.CreatedByID)
				require.Equal(t, requester, f.client.Ticket.GetX(f.ctx, f.problem.ID).RequesterID)
				w = createChangeRelationHTTP(r, path, body, "selected-change")
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), `"replayed":true`)
			})
		}
	}
}
