package controller

import (
	"net/http"
	"strconv"
	"testing"

	"itsm-backend/service"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestBPMNMonitoringObjectAndAggregateAuthorization(t *testing.T) {
	f := newBPMNHTTPAuthorizationFixture(t)
	logger := zap.NewNop().Sugar()
	auditService := service.NewBPMNAuditService(f.client, logger)
	seedBPMNAuthorizationAuditLog(t, f, auditService)
	controller := NewBPMNMonitoringController(service.NewBPMNMonitoringService(f.client, auditService, logger))
	router := newBPMNReadAuthorizationRouter(t, f, controller.RegisterRoutes)

	objectRoutes := []string{
		"/api/v1/bpmn/monitoring/instances/" + strconv.Itoa(f.instance.ID) + "/status",
		"/api/v1/bpmn/monitoring/instances/" + f.instance.ProcessInstanceID + "/timeline",
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
		"/api/v1/bpmn/monitoring/metrics",
		"/api/v1/bpmn/monitoring/metrics/" + f.instance.ProcessDefinitionKey,
		"/api/v1/bpmn/monitoring/instances/status",
		"/api/v1/bpmn/monitoring/performance",
		"/api/v1/bpmn/monitoring/performance/alerts",
		"/api/v1/bpmn/monitoring/health",
		"/api/v1/bpmn/monitoring/audit-logs",
	}
	for _, path := range aggregateRoutes {
		for actor, want := range map[string]int{"participant": http.StatusForbidden, "elevated": http.StatusOK} {
			t.Run(actor+" "+path, func(t *testing.T) {
				response := doBPMNReadAsActor(t, router, actor, path)
				assert.Equal(t, want, response.Code, response.Body.String())
				if want != http.StatusOK {
					assertBPMNDenialBodyIsSafe(t, response, "audit-variable-secret", "sensitive-candidate-expression", f.tenant.Code)
				}
			})
		}
	}
}
