//go:build integration_postgres

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/controller"
	"itsm-backend/middleware"
)

// Exercise the actual public handler, which intentionally supplies empty
// businessType and zero businessID to the engine for standalone processes.
func TestWorkItemBPMNHTTPStartIdentityBoundary(t *testing.T) {
	cases := []struct {
		name, identityKey  string
		canonical, allowed bool
	}{
		{name: "canonical_workitem_key", canonical: true},
		{name: "work_item_id", identityKey: "work_item_id"},
		{name: "ticket_id", identityKey: "ticket_id"},
		{name: "change_id", identityKey: "change_id"},
		{name: "record_class", identityKey: "record_class"},
		{name: "business_type", identityKey: "business_type"},
		{name: "business_id", identityKey: "business_id"},
		{name: "business_key", identityKey: "business_key"},
		{name: "standalone", allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			businessKey := "standalone:review-test"
			canonicalKey := fmt.Sprintf("change_request:%d", f.c.WorkItemID)
			if tc.canonical {
				businessKey = canonicalKey
			}
			variables := map[string]interface{}{"requester_id": f.actor.ID, "triggered_by": fmt.Sprint(f.actor.ID)}
			if tc.identityKey != "" {
				value := interface{}(f.c.WorkItemID)
				switch tc.identityKey {
				case "change_id":
					value = f.c.ID
				case "record_class", "business_type":
					value = "change_request"
				case "business_key":
					value = canonicalKey
				}
				variables[tc.identityKey] = value
			}
			body, err := json.Marshal(map[string]interface{}{"processDefinitionKey": "change_normal_flow", "businessKey": businessKey, "variables": variables})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/bpmn/process-instances", bytes.NewReader(body)).WithContext(middleware.WithAuthenticatedTenantID(f.ctx, f.tenant.ID))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("tenant_id", f.tenant.ID)
			ctx.Set("user_id", f.actor.ID)
			ctx.Set("role", "super_admin")
			ctx.Set("client", f.runtime)
			controller.NewBPMNWorkflowController(f.engine, nil, executionfixture.Standard()).StartProcess(ctx)
			if tc.allowed {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx), recorder.Body.String())
				instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
				require.Equal(t, businessKey, instance.BusinessKey)
				require.Empty(t, instance.BusinessType)
				require.Zero(t, instance.BusinessID)
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessTask.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.ctx))
				// Rejecting the generic entry must leave the owning command free to start.
				f.apply(t, f.command("submit", "after-rejected-http"))
				require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
			}
		})
	}
}
