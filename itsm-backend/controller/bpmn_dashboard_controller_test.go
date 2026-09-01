package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newBPMNReadAuthorizationRouter(t *testing.T, f *bpmnHTTPAuthorizationFixture, register func(*gin.RouterGroup)) *gin.Engine {
	t.Helper()
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(func(ctx *gin.Context) {
		actorName := ctx.GetHeader("X-Test-Actor")
		actor := f.actors[actorName]
		require.NotNil(t, actor, "unknown test actor %q", actorName)
		actorTenantID := f.tenant.ID
		if actorName == "cross_tenant" {
			actorTenantID = f.otherTenant.ID
		}
		requestCtx := middleware.WithAuthenticatedTenantID(ctx.Request.Context(), actorTenantID)
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Set("tenant_id", actorTenantID)
		ctx.Set("user_id", actor.ID)
		ctx.Set("role", actor.Role)
		ctx.Set("client", f.client)
		ctx.Next()
	})
	register(api)
	return router
}

func doBPMNReadAsActor(t *testing.T, router *gin.Engine, actor, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-Test-Actor", actor)
	router.ServeHTTP(recorder, request)
	return recorder
}

func seedBPMNAuthorizationAuditLog(t *testing.T, f *bpmnHTTPAuthorizationFixture, auditService *service.BPMNAuditService) {
	t.Helper()
	err := auditService.RecordAudit(context.Background(), &service.AuditContext{
		ProcessInstanceID:    f.instance.ID,
		ProcessInstanceKey:   f.instance.ProcessInstanceID,
		ProcessDefinitionKey: f.instance.ProcessDefinitionKey,
		ProcessDefinitionID:  f.instance.ProcessDefinitionID,
		ActivityID:           "participant-task",
		ActivityName:         "Participant task",
		ActivityType:         service.ActivityTypeUserTask,
		Action:               service.AuditActionTaskAssigned,
		UserID:               f.actors["participant"].ID,
		UserName:             f.actors["participant"].Username,
		VariablesBefore:      map[string]interface{}{"privateVariable": "audit-variable-secret"},
		VariablesAfter:       map[string]interface{}{"privateVariable": "audit-variable-secret"},
		TenantID:             f.tenant.ID,
	})
	require.NoError(t, err)
}

func TestBPMNDashboardAuthorizationMatrix(t *testing.T) {
	f := newBPMNHTTPAuthorizationFixture(t)
	logger := zap.NewNop().Sugar()
	auditService := service.NewBPMNAuditService(f.client, logger)
	seedBPMNAuthorizationAuditLog(t, f, auditService)
	controller := NewBPMNDashboardController(
		service.NewBPMNMetricsService(f.client, logger),
		auditService,
		service.NewBPMNTenantService(f.client, logger),
		service.NewBPMNSLAService(f.client, logger),
	)
	router := newBPMNReadAuthorizationRouter(t, f, controller.RegisterRoutes)

	objectRoutes := []string{
		"/api/v1/bpmn/dashboard/audit-logs?process_instance_id=" + strconv.Itoa(f.instance.ID),
		"/api/v1/bpmn/dashboard/audit-logs/timeline/" + f.instance.ProcessInstanceID,
	}
	for _, path := range objectRoutes {
		for actor, want := range map[string]int{
			"participant":  http.StatusOK,
			"outsider":     http.StatusForbidden,
			"elevated":     http.StatusOK,
			"cross_tenant": http.StatusNotFound,
		} {
			t.Run(actor+" "+path, func(t *testing.T) {
				response := doBPMNReadAsActor(t, router, actor, path)
				assert.Equal(t, want, response.Code, response.Body.String())
				if want != http.StatusOK {
					assertBPMNDenialBodyIsSafe(t, response, "audit-variable-secret", "sensitive-candidate-expression", f.tenant.Code)
				}
			})
		}
	}

	aggregateRoutes := []string{
		"/api/v1/bpmn/dashboard/metrics",
		"/api/v1/bpmn/dashboard/process/" + f.instance.ProcessDefinitionKey + "/metrics",
		"/api/v1/bpmn/dashboard/audit-logs",
		"/api/v1/bpmn/dashboard/audit-logs/user/" + strconv.Itoa(f.actors["participant"].ID),
		"/api/v1/bpmn/dashboard/sla/violations",
		"/api/v1/bpmn/dashboard/sla/compliance?key=" + f.instance.ProcessDefinitionKey,
		"/api/v1/bpmn/dashboard/tenant/stats",
		"/api/v1/bpmn/dashboard/bottlenecks?key=" + f.instance.ProcessDefinitionKey,
	}
	for _, path := range aggregateRoutes {
		for actor, want := range map[string]int{"participant": http.StatusForbidden, "elevated": http.StatusOK} {
			t.Run(actor+" "+path, func(t *testing.T) {
				response := doBPMNReadAsActor(t, router, actor, path)
				assert.Equal(t, want, response.Code, response.Body.String())
				if want != http.StatusOK {
					assert.False(t, strings.Contains(response.Body.String(), "audit-variable-secret"), response.Body.String())
				}
			})
		}
	}
}
