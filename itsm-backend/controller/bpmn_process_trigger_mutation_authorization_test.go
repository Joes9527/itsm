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
	client      *ent.Client
	controller  *BPMNProcessTriggerController
	tenant      *ent.Tenant
	otherTenant *ent.Tenant
	admin       *ent.User
	endUser     *ent.User
	participant *ent.User
	crossTenant *ent.User
	definition  *ent.ProcessDefinition
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
	otherTenant, err := client.Tenant.Create().
		SetName("Other process trigger mutation tenant").
		SetCode(fmt.Sprintf("other-trigger-mutation-%d", time.Now().UnixNano())).
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
	participant, err := client.User.Create().
		SetUsername(fmt.Sprintf("trigger.participant.%d", time.Now().UnixNano())).
		SetEmail(fmt.Sprintf("trigger.participant.%d@example.test", time.Now().UnixNano())).
		SetName("Trigger Participant").
		SetPasswordHash("test").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	crossTenant, err := client.User.Create().
		SetUsername(fmt.Sprintf("trigger.cross.%d", time.Now().UnixNano())).
		SetEmail(fmt.Sprintf("trigger.cross.%d@example.test", time.Now().UnixNano())).
		SetName("Trigger Cross Tenant").
		SetPasswordHash("test").
		SetRole("super_admin").
		SetActive(true).
		SetTenantID(otherTenant.ID).
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
		client:      client,
		controller:  NewBPMNProcessTriggerController(triggerService, nil, nil),
		tenant:      tenant,
		otherTenant: otherTenant,
		admin:       admin,
		endUser:     endUser,
		participant: participant,
		crossTenant: crossTenant,
		definition:  definition,
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
	return f.routerForTenant(user, role, f.tenant)
}

func (f *processTriggerMutationHTTPFixture) routerForTenant(user *ent.User, role string, tenant *ent.Tenant) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		requestCtx := middleware.WithAuthenticatedTenantID(ctx.Request.Context(), tenant.ID)
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Set("tenant_id", tenant.ID)
		ctx.Set("user_id", user.ID)
		ctx.Set("role", role)
		ctx.Set("client", f.client)
		ctx.Next()
	})
	f.controller.RegisterRoutes(group)
	return router
}

func (f *processTriggerMutationHTTPFixture) do(actor, method, path, body string) *httptest.ResponseRecorder {
	var user *ent.User
	var tenant *ent.Tenant
	switch actor {
	case "participant":
		user, tenant = f.participant, f.tenant
	case "outsider":
		user, tenant = f.endUser, f.tenant
	case "elevated":
		user, tenant = f.admin, f.tenant
	case "cross_tenant":
		user, tenant = f.crossTenant, f.otherTenant
	default:
		panic("unknown process trigger actor: " + actor)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	f.routerForTenant(user, user.Role, tenant).ServeHTTP(recorder, request)
	return recorder
}

func (f *processTriggerMutationHTTPFixture) seedParticipantTask(t *testing.T, instance *ent.ProcessInstance) {
	t.Helper()
	_, err := f.client.ProcessTask.Create().
		SetTaskID(fmt.Sprintf("trigger-participant-task-%d", time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("participant-task").
		SetTaskName("Participant task").
		SetAssignee(f.participant.Username).
		SetTenantID(f.tenant.ID).
		Save(context.Background())
	require.NoError(t, err)
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

func TestBPMNProcessTriggerHTTPAuthorizationStatus(t *testing.T) {
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
			recorder, response := performProcessTriggerMutationRequest(t, f.router(f.endUser, "end_user"), fmt.Sprintf("/api/v1/process-trigger/%s/%d", tt.route, instance.ID))
			assert.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			assert.Equal(t, common.ForbiddenCode, response.Code)
			assert.NotEqual(t, common.SuccessCode, response.Code)
			assert.Contains(t, response.Message, "无权修改流程实例")
			assert.NotContains(t, recorder.Body.String(), f.tenant.Code)
			assert.NotContains(t, recorder.Body.String(), "tenant_id")
			assert.NotContains(t, recorder.Body.String(), "candidate")
			assert.NotContains(t, recorder.Body.String(), "SELECT")
			assert.NotContains(t, recorder.Body.String(), "variables")

			after, err := f.client.ProcessInstance.Get(context.Background(), instance.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.initialStatus, after.Status)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(context.Background()))
		})
	}
}

func TestBPMNProcessTriggerStatusAuthorizationMatrix(t *testing.T) {
	f := newProcessTriggerMutationHTTPFixture(t)
	instance := f.seedInstance(t, "status-matrix", "running")
	f.seedParticipantTask(t, instance)

	for _, tc := range []struct {
		actor string
		want  int
	}{
		{actor: "participant", want: http.StatusOK},
		{actor: "outsider", want: http.StatusForbidden},
		{actor: "elevated", want: http.StatusOK},
		{actor: "cross_tenant", want: http.StatusNotFound},
	} {
		t.Run(tc.actor, func(t *testing.T) {
			response := f.do(tc.actor, http.MethodGet, fmt.Sprintf("/api/v1/process-trigger/status/%d", instance.ID), "")
			require.Equal(t, tc.want, response.Code, response.Body.String())
			if tc.want != http.StatusOK {
				assertBPMNDenialBodyIsSafe(t, response, "privateVariable", f.tenant.Code)
			}
		})
	}
}

func TestBPMNProcessTriggerMutationAuthorizationMatrix(t *testing.T) {
	f := newProcessTriggerMutationHTTPFixture(t)
	for _, action := range []struct {
		name, route, initialStatus, expectedStatus, auditAction string
	}{
		{name: "cancel", route: "cancel", initialStatus: "running", expectedStatus: "terminated", auditAction: service.AuditActionProcessTerminated},
		{name: "suspend", route: "suspend", initialStatus: "running", expectedStatus: "suspended", auditAction: service.AuditActionProcessSuspended},
		{name: "resume", route: "resume", initialStatus: "suspended", expectedStatus: "running", auditAction: service.AuditActionProcessResumed},
	} {
		for _, actor := range []struct {
			name, fixtureActor string
			want               int
		}{
			{name: "participant", fixtureActor: "participant", want: http.StatusForbidden},
			{name: "outsider", fixtureActor: "outsider", want: http.StatusForbidden},
			{name: "elevated", fixtureActor: "elevated", want: http.StatusOK},
			{name: "cross-tenant", fixtureActor: "cross_tenant", want: http.StatusNotFound},
		} {
			t.Run(action.name+"-"+actor.name, func(t *testing.T) {
				instance := f.seedInstance(t, action.name+"-"+actor.name, action.initialStatus)
				f.seedParticipantTask(t, instance)
				response := f.do(actor.fixtureActor, http.MethodPost, fmt.Sprintf("/api/v1/process-trigger/%s/%d", action.route, instance.ID), `{"reason":"maintenance"}`)
				require.Equal(t, actor.want, response.Code, response.Body.String())

				after, err := f.client.ProcessInstance.Get(context.Background(), instance.ID)
				require.NoError(t, err)
				if actor.want != http.StatusOK {
					assert.Equal(t, action.initialStatus, after.Status)
					assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(context.Background()))
					assertBPMNDenialBodyIsSafe(t, response, "privateVariable", f.tenant.Code)
					return
				}

				assert.Equal(t, action.expectedStatus, after.Status)
				audit, err := f.client.ProcessAuditLog.Query().
					Where(processauditlog.ProcessInstanceID(instance.ID)).
					Only(context.Background())
				require.NoError(t, err)
				assert.Equal(t, action.auditAction, audit.Action)
			})
		}
	}
}
