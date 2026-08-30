package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type processTriggerMutationHTTPFixture struct {
	client     *ent.Client
	controller *BPMNProcessTriggerController
	tenant     *ent.Tenant
	admin      *ent.User
	endUser    *ent.User
	definition *ent.ProcessDefinition
}

func newProcessTriggerMutationHTTPFixture(t *testing.T) *processTriggerMutationHTTPFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:bpmn_trigger_mutation_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Process trigger mutation tenant").
		SetCode(fmt.Sprintf("trigger-mutation-%d", time.Now().UnixNano())).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	admin, err := client.User.Create().
		SetUsername(fmt.Sprintf("trigger.admin.%d", time.Now().UnixNano())).
		SetEmail(fmt.Sprintf("trigger.admin.%d@example.test", time.Now().UnixNano())).
		SetName("Trigger Admin").
		SetPasswordHash("test").
		SetRole("super_admin").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	endUser, err := client.User.Create().
		SetUsername(fmt.Sprintf("trigger.user.%d", time.Now().UnixNano())).
		SetEmail(fmt.Sprintf("trigger.user.%d@example.test", time.Now().UnixNano())).
		SetName("Trigger End User").
		SetPasswordHash("test").
		SetRole("end_user").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID(fmt.Sprintf("trigger-deployment-%d", time.Now().UnixNano())).
		SetDeploymentName("Process trigger mutation deployment").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey(fmt.Sprintf("trigger_mutation_%d", time.Now().UnixNano())).
		SetName("Process trigger mutation").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar())
	triggerService := service.NewProcessTriggerService(client, engine)
	return &processTriggerMutationHTTPFixture{
		client:     client,
		controller: NewBPMNProcessTriggerController(triggerService, nil, nil),
		tenant:     tenant,
		admin:      admin,
		endUser:    endUser,
		definition: definition,
	}
}

func (f *processTriggerMutationHTTPFixture) seedInstance(t *testing.T, suffix, status string) *ent.ProcessInstance {
	t.Helper()
	instance, err := f.client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("trigger-instance-%s-%d", suffix, time.Now().UnixNano())).
		SetProcessDefinitionKey(f.definition.Key).
		SetProcessDefinitionID(f.definition.ID).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetStatus(status).
		SetTenantID(f.tenant.ID).
		Save(context.Background())
	require.NoError(t, err)
	return instance
}

func (f *processTriggerMutationHTTPFixture) router(user *ent.User, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		requestCtx := middleware.WithAuthenticatedTenantID(ctx.Request.Context(), f.tenant.ID)
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Set("tenant_id", f.tenant.ID)
		ctx.Set("user_id", user.ID)
		ctx.Set("role", role)
		ctx.Set("client", f.client)
		ctx.Next()
	})
	f.controller.RegisterRoutes(group)
	return router
}

func performProcessTriggerMutationRequest(t *testing.T, router *gin.Engine, path string) (*httptest.ResponseRecorder, common.Response) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"reason":"maintenance"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	var response common.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestBPMNProcessTriggerMutationsUseTrustedUpdateScope(t *testing.T) {
	f := newProcessTriggerMutationHTTPFixture(t)
	tests := []struct {
		name, route, initialStatus, expectedStatus, action string
	}{
		{name: "suspend", route: "suspend", initialStatus: "running", expectedStatus: "suspended", action: service.AuditActionProcessSuspended},
		{name: "resume", route: "resume", initialStatus: "suspended", expectedStatus: "running", action: service.AuditActionProcessResumed},
		{name: "terminate", route: "cancel", initialStatus: "running", expectedStatus: "terminated", action: service.AuditActionProcessTerminated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := f.seedInstance(t, "allowed-"+tt.name, tt.initialStatus)
			recorder, response := performProcessTriggerMutationRequest(t, f.router(f.admin, "super_admin"), fmt.Sprintf("/api/v1/process-trigger/%s/%d", tt.route, instance.ID))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, common.SuccessCode, response.Code)

			after, err := f.client.ProcessInstance.Get(context.Background(), instance.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, after.Status)
			audit, err := f.client.ProcessAuditLog.Query().
				Where(processauditlog.ProcessInstanceID(instance.ID)).
				Only(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.action, audit.Action)
			assert.Equal(t, f.admin.ID, audit.UserID)
			assert.Equal(t, f.tenant.ID, audit.TenantID)
		})
	}
}

func TestBPMNProcessTriggerMutationsRequireUpdatePermission(t *testing.T) {
	f := newProcessTriggerMutationHTTPFixture(t)
	tests := []struct {
		name, route, initialStatus string
	}{
		{name: "suspend", route: "suspend", initialStatus: "running"},
		{name: "resume", route: "resume", initialStatus: "suspended"},
		{name: "terminate", route: "cancel", initialStatus: "running"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := f.seedInstance(t, "denied-"+tt.name, tt.initialStatus)
			_, response := performProcessTriggerMutationRequest(t, f.router(f.endUser, "end_user"), fmt.Sprintf("/api/v1/process-trigger/%s/%d", tt.route, instance.ID))
			assert.NotEqual(t, common.SuccessCode, response.Code)
			assert.Contains(t, response.Message, "无权修改流程实例")

			after, err := f.client.ProcessInstance.Get(context.Background(), instance.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.initialStatus, after.Status)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(context.Background()))
		})
	}
}
