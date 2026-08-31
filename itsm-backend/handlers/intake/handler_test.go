package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubCreateService struct {
	result   *CreateWorkItemResult
	err      error
	identity Identity
	command  CreateWorkItemCommand
	calls    int
}

func (s *stubCreateService) Create(_ context.Context, identity Identity, command CreateWorkItemCommand) (*CreateWorkItemResult, error) {
	s.calls++
	s.identity = identity
	s.command = command
	return s.result, s.err
}

func newIntakeHandlerRouter(service *stubCreateService, authenticated bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "request-test-1")
		if authenticated {
			c.Set("tenant_id", 7)
			c.Set("user_id", 11)
			c.Set("role", "end_user")
		}
		c.Next()
	})
	NewHandler(service).RegisterRoutes(router.Group("/api/v1"))
	return router
}

func performIntakeRequest(router http.Handler, body string, bearer bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/intake/work-items", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer {
		request.Header.Set("Authorization", "Bearer access-token")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestIntakeHandlerReturns201Then200AndDerivesIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		replayed bool
		status   int
		bearer   bool
		channel  string
	}{
		{name: "first create from cookie", status: http.StatusCreated, channel: "itsm_web"},
		{name: "replay from bearer", replayed: true, status: http.StatusOK, bearer: true, channel: "itsm_api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &stubCreateService{result: &CreateWorkItemResult{
				WorkItemID: 51, Number: "TKT-51", RecordClass: RecordClassIncident,
				ProfessionalReference: ProfessionalReference{Type: "incident", ID: 81},
				WorkflowStartStatus:   "pending", Replayed: test.replayed,
			}}
			response := performIntakeRequest(newIntakeHandlerRouter(service, true), `{"idempotencyKey":"key-1","intakeKind":"incident","title":"Outage"}`, test.bearer)
			require.Equal(t, test.status, response.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, "request-test-1", body["requestId"])
			require.Equal(t, float64(7), float64(service.identity.TenantID))
			require.Equal(t, service.identity.ActorID, service.identity.RequesterID)
			require.Equal(t, test.channel, service.identity.Channel)
		})
	}
}

func TestIntakeHandlerRejectsMissingIdentityAndStrictBody(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		service := &stubCreateService{}
		response := performIntakeRequest(newIntakeHandlerRouter(service, false), `{"idempotencyKey":"key-1","intakeKind":"incident","title":"Outage"}`, true)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Contains(t, response.Body.String(), `"code":"AuthenticationRequired"`)
		require.Zero(t, service.calls)
	})

	for name, body := range map[string]string{
		"unknown field":   `{"idempotencyKey":"key-1","intakeKind":"incident","title":"Outage","tenantId":99}`,
		"multiple values": `{"idempotencyKey":"key-1","intakeKind":"incident","title":"Outage"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			service := &stubCreateService{}
			response := performIntakeRequest(newIntakeHandlerRouter(service, true), body, true)
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Contains(t, response.Body.String(), `"code":"InvalidCommand"`)
			require.Zero(t, service.calls)
		})
	}

	t.Run("missing idempotency key", func(t *testing.T) {
		service := &stubCreateService{err: NewInvalidCommand("invalid intake command", FieldError{Field: "idempotencyKey", Message: "is required"}, nil)}
		response := performIntakeRequest(newIntakeHandlerRouter(service, true), `{"intakeKind":"incident","title":"Outage"}`, true)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), `"field":"idempotencyKey"`)
	})

	t.Run("body size limit", func(t *testing.T) {
		service := &stubCreateService{}
		oversized := `{"idempotencyKey":"key-1","intakeKind":"incident","title":"` + string(bytes.Repeat([]byte("x"), int(maxCreateWorkItemBodyBytes))) + `"}`
		response := performIntakeRequest(newIntakeHandlerRouter(service, true), oversized, true)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Zero(t, service.calls)
	})
}

func TestIntakeHandlerMapsStableSafeErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		forbidden string
	}{
		{name: "permission", err: NewPermissionDenied("target create permission is required", nil), status: 403, code: "PermissionDenied"},
		{name: "reference", err: NewReferenceNotFound("catalog item was not found", nil), status: 404, code: "ReferenceNotFound"},
		{name: "conflict", err: NewIdempotencyConflict("idempotency key conflict", nil), status: 409, code: "IdempotencyConflict"},
		{name: "validation", err: NewDomainValidationFailed("field validation failed", nil, FieldError{Field: "title", Message: "required"}), status: 422, code: "DomainValidationFailed"},
		{name: "infrastructure", err: NewInfrastructureUnavailable("database password leaked", errors.New("pq: secret table")), status: 503, code: "InfrastructureUnavailable", retryable: true, forbidden: "password"},
		{name: "untyped", err: errors.New("sql: raw secret"), status: 500, code: "InternalFailure", forbidden: "secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubCreateService{err: test.err}
			response := performIntakeRequest(newIntakeHandlerRouter(service, true), `{"idempotencyKey":"key-1","intakeKind":"incident","title":"Outage"}`, true)
			require.Equal(t, test.status, response.Code)
			var body struct {
				Code      string `json:"code"`
				RequestID string `json:"requestId"`
				Retryable bool   `json:"retryable"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, test.code, body.Code)
			require.Equal(t, "request-test-1", body.RequestID)
			require.Equal(t, test.retryable, body.Retryable)
			if test.forbidden != "" {
				require.NotContains(t, response.Body.String(), test.forbidden)
			}
		})
	}
}
