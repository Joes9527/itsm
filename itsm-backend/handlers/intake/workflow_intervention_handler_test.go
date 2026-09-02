package intake

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	itsmservice "itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubWorkflowRetryRepository struct {
	err        error
	tenantID   int
	workItemID int
	userID     int
	requestID  string
}

func (r *stubWorkflowRetryRepository) RetryDeadWorkflowStart(ctx context.Context, tenantID, workItemID int) error {
	r.tenantID, r.workItemID = tenantID, workItemID
	r.userID, _ = ctx.Value("user_id").(int)
	r.requestID, _ = ctx.Value("request_id").(string)
	return r.err
}

func TestWorkflowManualInterventionHandlerUsesAuthenticatedScope(t *testing.T) {
	repository := &stubWorkflowRetryRepository{}
	router := newWorkflowInterventionRouter(repository)
	response := performWorkflowRetry(router, "/api/v1/intake/work-items/501/workflow-start/retry")
	require.Equal(t, http.StatusAccepted, response.Code)
	require.Equal(t, 7, repository.tenantID)
	require.Equal(t, 501, repository.workItemID)
	require.Equal(t, 11, repository.userID)
	require.Equal(t, "manual-request-1", repository.requestID)
}

func TestWorkflowManualInterventionHandlerFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		err    error
		status int
		code   string
	}{
		{name: "invalid work item", path: "/api/v1/intake/work-items/not-an-id/workflow-start/retry", status: 400, code: "InvalidCommand"},
		{name: "not dead or foreign tenant", path: "/api/v1/intake/work-items/501/workflow-start/retry", err: itsmservice.ErrWorkflowStartNotDead, status: 404, code: "ReferenceNotFound"},
		{name: "repository unavailable", path: "/api/v1/intake/work-items/501/workflow-start/retry", err: errors.New("sql password=secret"), status: 503, code: "InfrastructureUnavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &stubWorkflowRetryRepository{err: test.err}
			response := performWorkflowRetry(newWorkflowInterventionRouter(repository), test.path)
			require.Equal(t, test.status, response.Code)
			require.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
			require.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func newWorkflowInterventionRouter(repository *stubWorkflowRetryRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 7)
		c.Set("user_id", 11)
		c.Set("role", "ops_manager")
		c.Set("request_id", "manual-request-1")
		c.Next()
	})
	NewWorkflowInterventionHandler(repository).RegisterRoutes(router.Group("/api/v1"))
	return router
}

func performWorkflowRetry(router http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, nil)
	router.ServeHTTP(response, request)
	return response
}
