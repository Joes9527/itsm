package service_request

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/handlers/intake"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingServiceRequestIntake struct {
	calls    int
	identity intake.Identity
	command  intake.CreateWorkItemCommand
	result   *intake.CreateWorkItemResult
}

func (f *recordingServiceRequestIntake) Create(_ context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	f.calls++
	f.identity = identity
	f.command = command
	return f.result, nil
}

type stubServiceRequestCreateReader struct{ request *ServiceRequest }

func (f stubServiceRequestCreateReader) Get(context.Context, int, int) (*ServiceRequest, error) {
	return f.request, nil
}

func serviceRequestCreateRouter(handler *Handler) *gin.Engine {
	router := gin.New()
	router.Use(srAuth(19, 73))
	router.POST("/api/v1/service-requests", handler.Create)
	return router
}

func TestServiceRequestCreateRequiresIdempotencyKey(t *testing.T) {
	creator := &recordingServiceRequestIntake{}
	handler := NewHandler(nil, creator)
	router := serviceRequestCreateRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/service-requests", bytes.NewBufferString(`{"catalogId":5,"title":"VPN access"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, creator.calls)
}

func TestServiceRequestCreateUsesUnifiedIntakeAdapterAndPreservesResponse(t *testing.T) {
	expires := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	creator := &recordingServiceRequestIntake{result: &intake.CreateWorkItemResult{
		WorkItemID: 301, RecordClass: intake.RecordClassServiceRequestItem,
		ProfessionalReference: intake.ProfessionalReference{Type: "service_request", ID: 302},
	}}
	handler := NewHandler(nil, creator)
	handler.serviceRequestCreateReader = stubServiceRequestCreateReader{request: &ServiceRequest{
		ID: 302, TicketID: 301, CatalogID: 5, RequesterID: 73, TicketTitle: "VPN access",
	}}
	router := serviceRequestCreateRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/service-requests", bytes.NewBufferString(`{"catalogId":5,"title":"VPN access","reason":"remote work","costCenter":"CC-1","expireAt":"`+expires.Format(time.RFC3339)+`","formData":{"environment":"prod"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "request-key-302")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 1, creator.calls)
	assert.Equal(t, 19, creator.identity.TenantID)
	assert.Equal(t, 73, creator.identity.ActorID)
	assert.Equal(t, "request-key-302", creator.command.IdempotencyKey)
	assert.Equal(t, intake.IntakeKindCatalogItem, creator.command.IntakeKind)
	require.NotNil(t, creator.command.CatalogItemID)
	assert.Equal(t, 5, *creator.command.CatalogItemID)
	assert.Equal(t, "prod", creator.command.FormValues["environment"])
	assert.Equal(t, "CC-1", creator.command.FormValues["cost_center"])
	assert.Equal(t, expires.UTC().Format(time.RFC3339Nano), creator.command.FormValues["expire_at"])
	assert.Contains(t, response.Body.String(), `"id":302`)
	assert.Contains(t, response.Body.String(), `"ticketId":301`)
}
