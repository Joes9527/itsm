package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func TestIncidentAssignmentHTTPUsesProfessionalIDAndObservedVersion(t *testing.T) {
	f := newConversionControllerFixture(t, false, true)
	ctx := context.Background()
	f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("unrelated").SetTicketNumber("UNRELATED").SaveX(ctx)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetAssigneeID(f.actor.ID).SetRecordClass("incident").SetTitle("handover").SetTicketNumber("INC-HANDOVER").SetStatus("in_progress").SaveX(ctx)
	inc := f.client.Incident.Create().SetWorkItemID(item.ID).SaveX(ctx)
	require.NotEqual(t, item.ID, inc.ID)
	next := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("next-owner").SetName("Next").SetEmail("next@example.test").SetPasswordHash("test").SetRole("service_agent").SetActive(true).SaveX(ctx)
	logger := zap.NewNop().Sugar()
	handler := NewIncidentController(service.NewIncidentService(f.client, logger, executionfixture.Standard()), nil, nil, nil, nil, logger)
	f.router.POST("/api/v1/incidents/:id/assign", handler.AssignIncident)
	send := func(id int, key, reason string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"assigneeId":%d,"version":%d,"operationId":%q,"reason":%q}`, next.ID, item.Version, key, reason)
		request := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/incidents/%d/assign", id), strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		return response
	}

	invalidRequest := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/incidents/%d/assign", inc.ID), strings.NewReader(fmt.Sprintf(`{"assigneeId":0,"version":%d,"operationId":"invalid-target","reason":"handover"}`, item.Version)))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	f.router.ServeHTTP(invalidResponse, invalidRequest)
	require.Equal(t, 400, invalidResponse.Code, invalidResponse.Body.String())
	response := send(inc.ID, "http-handover", "continue investigation")
	require.Equal(t, 200, response.Code, response.Body.String())
	var payload struct {
		Data workitemmutation.Result `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, item.ID, payload.Data.WorkItemID)
	require.Equal(t, item.Version+1, payload.Data.Version)
	require.Equal(t, "in_progress", payload.Data.Status)
	response = send(inc.ID, "http-handover", "continue investigation")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Data.Replayed)
	require.Equal(t, 409, send(inc.ID, "different-attempt", "continue investigation").Code)
	require.Equal(t, 409, send(inc.ID, "http-handover", "changed reason").Code)
	require.Equal(t, 404, send(item.ID, "wrong-identity", "continue investigation").Code)
	require.Equal(t, next.ID, f.client.Ticket.GetX(ctx, item.ID).AssigneeID)
	require.Equal(t, item.Version+1, f.client.Ticket.GetX(ctx, item.ID).Version)
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(ctx))
}
