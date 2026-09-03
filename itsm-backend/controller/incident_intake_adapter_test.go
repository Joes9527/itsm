package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/handlers/intake"
	"itsm-backend/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type recordingIncidentIntake struct {
	calls    int
	identity intake.Identity
	command  intake.CreateWorkItemCommand
	result   *intake.CreateWorkItemResult
	err      error
}

func (f *recordingIncidentIntake) Create(_ context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	f.calls++
	f.identity = identity
	f.command = command
	return f.result, f.err
}

type stubIncidentCreateReader struct {
	response *dto.IncidentResponse
	err      error
}

func (f stubIncidentCreateReader) GetIncident(context.Context, int, int) (*dto.IncidentResponse, error) {
	return f.response, f.err
}

func incidentCreateRouter(controller *IncidentController) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 19)
		c.Set("user_id", 73)
		c.Set("role", "manager")
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 19})
		c.Next()
	})
	router.POST("/api/v1/incidents", controller.CreateIncident)
	return router
}

func TestIncidentCreateRequiresIdempotencyKey(t *testing.T) {
	creator := &recordingIncidentIntake{}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	router := incidentCreateRouter(controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"VPN unavailable"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, creator.calls)
}

func TestIncidentCreateMapsFullFieldSet(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 303, RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 404},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 404}}
	router := incidentCreateRouter(controller)

	body := `{"title":"t","priority":"critical","assigneeId":9,"source":"monitoring","impactAnalysis":{"technicalImpact":"regional"},"metadata":{"k":"v"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotNil(t, creator.command.Incident)
	assert.Equal(t, "critical", creator.command.Incident.ExplicitPriority)
	assert.Equal(t, 9, *creator.command.Incident.AssigneeID)
	assert.Equal(t, "monitoring", creator.command.Incident.Source)
	assert.Nil(t, creator.command.SourceReference)
	assert.Equal(t, "regional", creator.command.Incident.ImpactAnalysis["technicalImpact"])
	assert.Nil(t, creator.command.CTI)
	assert.Equal(t, 19, creator.identity.TenantID)
	assert.Equal(t, 73, creator.identity.ActorID)
	assert.Equal(t, 73, creator.identity.RequesterID)
	assert.Equal(t, "itsm_web", creator.identity.Channel)
}

func TestIncidentCreateRetryWithSameKeyAndBodyProducesIdenticalDigest(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 404},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 404}}
	router := incidentCreateRouter(controller)

	body := `{"title":"t","source":"monitoring"}`
	post := func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "key-retry")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	post()
	firstCommand := creator.command
	post()
	secondCommand := creator.command

	firstDigest, err := json.Marshal(firstCommand)
	require.NoError(t, err)
	secondDigest, err := json.Marshal(secondCommand)
	require.NoError(t, err)
	assert.JSONEq(t, string(firstDigest), string(secondDigest), "identical body + identical key must produce an identical command on every call")
}

func TestIncidentCreateMapsCategoryTypeAndDetectedAt(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 303, RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 404},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 404}}
	router := incidentCreateRouter(controller)

	body := `{"title":"t","type":"security_event","category":"performance","subcategory":"cpu","detectedAt":"2026-09-01T10:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-2")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotNil(t, creator.command.Incident)
	assert.Equal(t, "security_event", creator.command.Incident.Type)
	assert.Equal(t, "performance", creator.command.Incident.Category)
	assert.Equal(t, "cpu", creator.command.Incident.Subcategory)
	assert.Equal(t, "2026-09-01T10:00:00Z", creator.command.Incident.DetectedAt)
}

func TestIncidentCreateMapsIntakeErrorToResponse(t *testing.T) {
	creator := &recordingIncidentIntake{err: intake.NewPermissionDenied("actor cannot create incidents", nil)}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{}
	router := incidentCreateRouter(controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"t"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-3")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "actor cannot create incidents")
}