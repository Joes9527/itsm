package controller

import (
	"bytes"
	"context"
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

func TestIncidentCreateUsesUnifiedIntakeAdapterAndPreservesResponse(t *testing.T) {
	creator := &recordingIncidentIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 101, RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 202},
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, nil, zaptest.NewLogger(t).Sugar())
	controller.intakeService = creator
	controller.incidentCreateReader = stubIncidentCreateReader{response: &dto.IncidentResponse{ID: 202, Title: "VPN unavailable", IncidentNumber: "INC-202"}}
	router := incidentCreateRouter(controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"VPN unavailable","description":"cannot connect","severity":"high","impact":"medium","urgency":"critical","configurationItemIds":[9,3]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "incident-key-202")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 1, creator.calls)
	assert.Equal(t, 19, creator.identity.TenantID)
	assert.Equal(t, 73, creator.identity.ActorID)
	assert.Equal(t, "incident-key-202", creator.command.IdempotencyKey)
	assert.Equal(t, intake.IntakeKindIncident, creator.command.IntakeKind)
	assert.Equal(t, []int{9, 3}, creator.command.CIIDs)
	require.NotNil(t, creator.command.Incident)
	assert.Equal(t, "critical", creator.command.Incident.Urgency)
	assert.Contains(t, response.Body.String(), `"id":202`)
	assert.Contains(t, response.Body.String(), `"incidentNumber":"INC-202"`)
}
