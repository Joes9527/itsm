package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
)

// The regression catches identity rewriting, lost configuration, duplicate audit,
// absent CAS, tenant/role bypass, and non-atomic audit failures at the real route.
func TestBindingDeactivationPreservesLegacyConfiguration(t *testing.T) {
	for _, identity := range []string{"ticket", "service_request", "cloud_public_ops", "generic"} {
		t.Run(identity, func(t *testing.T) {
			client, binding, router := bindingDeactivationFixture(t, "super_admin", 1, 7)
			binding = binding.Update().SetBusinessType(identity).SaveX(context.Background())
			before := *binding
			body := fmt.Sprintf(`{"reason":"Retire reviewed legacy routing","expectedUpdatedAt":%q}`, binding.UpdatedAt.Format(time.RFC3339Nano))
			response := performBindingDeactivation(router, binding.ID, body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var envelope struct {
				Code int
				Data struct {
					IsActive     bool
					BusinessType string
				}
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Zero(t, envelope.Code)
			require.False(t, envelope.Data.IsActive)
			require.Equal(t, identity, envelope.Data.BusinessType)
			after := client.ProcessBinding.GetX(context.Background(), binding.ID)
			require.False(t, after.IsActive)
			require.True(t, after.UpdatedAt.After(before.UpdatedAt))
			after.IsActive, after.UpdatedAt = before.IsActive, before.UpdatedAt
			beforeJSON, err := json.Marshal(before)
			require.NoError(t, err)
			afterJSON, err := json.Marshal(after)
			require.NoError(t, err)
			require.JSONEq(t, string(beforeJSON), string(afterJSON), "deactivation must preserve persisted configuration")
			audit := client.AuditLog.Query().OnlyX(context.Background())
			require.Equal(t, 1, audit.TenantID)
			require.Equal(t, 7, audit.UserID)
			require.Equal(t, "deactivate", audit.Action)
			require.Contains(t, *audit.RequestBody, "Retire reviewed legacy routing")
			response = performBindingDeactivation(router, binding.ID, body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, 1, client.AuditLog.Query().CountX(context.Background()))
		})
	}
}

func TestBindingDeactivationRejectsStaleAndUnauthorizedCommands(t *testing.T) {
	for _, tc := range []struct {
		name, role            string
		tenant, actor, status int
		body                  string
	}{
		{"role", "change_manager", 1, 7, 403, "valid"},
		{"tenant", "super_admin", 2, 7, 404, "valid"},
		{"missing_actor", "super_admin", 1, 0, 401, "valid"},
		{"stale", "super_admin", 1, 7, 409, `{"reason":"reviewed","expectedUpdatedAt":"2000-01-01T00:00:00Z"}`},
		{"no_version", "super_admin", 1, 7, 400, `{"reason":"reviewed"}`},
		{"no_reason", "super_admin", 1, 7, 400, `{"expectedUpdatedAt":"2000-01-01T00:00:00Z"}`},
		{"blank_reason", "super_admin", 1, 7, 400, `{"reason":"   ","expectedUpdatedAt":"2000-01-01T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, binding, router := bindingDeactivationFixture(t, tc.role, tc.tenant, tc.actor)
			body := tc.body
			if body == "valid" {
				body = fmt.Sprintf(`{"reason":"reviewed","expectedUpdatedAt":%q}`, binding.UpdatedAt.Format(time.RFC3339Nano))
			}
			response := performBindingDeactivation(router, binding.ID, body)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.True(t, client.ProcessBinding.GetX(context.Background(), binding.ID).IsActive)
			require.Zero(t, client.AuditLog.Query().CountX(context.Background()))
		})
	}
}

func TestBindingDeactivationAuditFailureRollsBack(t *testing.T) {
	client, binding, router := bindingDeactivationFixture(t, "super_admin", 1, 7)
	client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("audit storage unavailable")
		})
	})
	body := fmt.Sprintf(`{"reason":"reviewed","expectedUpdatedAt":%q}`, binding.UpdatedAt.Format(time.RFC3339Nano))
	response := performBindingDeactivation(router, binding.ID, body)
	require.Equal(t, 500, response.Code, response.Body.String())
	persisted := client.ProcessBinding.GetX(context.Background(), binding.ID)
	require.True(t, persisted.IsActive)
	require.Equal(t, binding.UpdatedAt, persisted.UpdatedAt)
	require.Zero(t, client.AuditLog.Query().CountX(context.Background()))
}

func bindingDeactivationFixture(t *testing.T, role string, tenant, actor int) (*ent.Client, *ent.ProcessBinding, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	binding := client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetBusinessSubType("service_request").SetProcessDefinitionKey("existing-flow").SetProcessVersion(1).SetIsDefault(true).SetPriority(15).SetIsActive(true).SetDepartmentID(3).SetTeamID(4).SetCategoryID(5).SetScenario("existing").SetCategory("operations").SetConditions(map[string]interface{}{"priority": "high"}).SetOverrides(map[string]interface{}{"review": true}).SetApprovalChainID("existing-chain").SetSLAPolicyID("6").SetUpdatedAt(time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)).SaveX(context.Background())
	router := gin.New()
	group := router.Group("/api/v1")
	group.Use(func(c *gin.Context) { c.Set("role", role); c.Set("tenant_id", tenant); c.Set("user_id", actor) })
	NewBPMNProcessTriggerController(nil, service.NewProcessBindingService(client), nil).RegisterRoutes(group)
	return client, binding, router
}

func performBindingDeactivation(router *gin.Engine, id int, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/process-bindings/%d/deactivate", id), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}
