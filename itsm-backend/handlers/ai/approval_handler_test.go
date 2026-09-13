package ai_test

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/ent"
	"itsm-backend/handlers/ai"
	creation "itsm-backend/handlers/common/workitemcreation"
	"net/http/httptest"
	"strings"
	"testing"
)

type approvalResponseRepo struct {
	rbacMockRepo
	err error
}

func (r *approvalResponseRepo) DecideToolInvocation(_ context.Context, id, tenant, actor int, _ bool, _ string) (*ai.ToolInvocation, error) {
	return &ai.ToolInvocation{ID: id, TenantID: tenant, ApprovedBy: actor}, r.err
}
func TestToolApprovalHTTPFailureClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"conflict", ai.ErrToolApprovalConflict, 409}, {"scope", executionscope.ErrDenied, 403},
		{"permission", creation.NewPermissionDenied("private-detail", nil), 403}, {"missing", &ent.NotFoundError{}, 404},
		{"database", errors.New("private-detail"), 500}, {"committed queue unavailable", nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := ai.NewService(&approvalResponseRepo{err: tc.err}, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil, nil, nil, nil)
			h := ai.NewHandler(svc)
			router := gin.New()
			router.POST("/tools/:id/approve", func(c *gin.Context) { c.Set("tenant_id", 1); c.Set("user_id", 2); h.ApproveTool(c) })
			request := httptest.NewRequest("POST", "/tools/1/approve", strings.NewReader(`{"approve":true,"reason":"reviewed"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			require.NotContains(t, response.Body.String(), "private-detail")
		})
	}
}
