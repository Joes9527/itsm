package ai_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/handlers/ai"
	"itsm-backend/service"
)

type failingToolAuditRepo struct {
	rbacMockRepo
	failure error
}

func (r *failingToolAuditRepo) CreateToolInvocation(context.Context, *ai.ToolInvocation) (*ai.ToolInvocation, error) {
	return nil, r.failure
}

func TestUnknownToolRetainsAuditFailure(t *testing.T) {
	failure := errors.New("private audit failure")
	svc := ai.NewService(&failingToolAuditRepo{failure: failure}, zap.NewNop().Sugar(), nil, service.NewToolRegistry(nil, nil, nil, nil), nil, nil, nil, nil, nil, nil, nil)
	_, _, err := svc.ExecuteTool(context.Background(), 1, 1, "requester", "missing-tool", map[string]interface{}{})
	require.ErrorIs(t, err, ai.ErrUnknownTool)
	require.ErrorIs(t, err, failure)
}

func TestUnknownToolAuditFailureHTTPIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := ai.NewService(&failingToolAuditRepo{failure: errors.New("private audit failure")}, zap.NewNop().Sugar(), nil, service.NewToolRegistry(nil, nil, nil, nil), nil, nil, nil, nil, nil, nil, nil)
	h := ai.NewHandler(svc)
	router := gin.New()
	router.POST("/tools", func(c *gin.Context) { c.Set("tenant_id", 1); c.Set("user_id", 1); h.ExecuteTool(c) })
	request := httptest.NewRequest("POST", "/tools", strings.NewReader(`{"name":"missing-tool","args":{}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, 503, response.Code)
	require.NotContains(t, response.Body.String(), "private audit failure")
}
