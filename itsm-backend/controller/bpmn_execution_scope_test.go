package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"itsm-backend/common/executionscope"
)

func TestBPMNExecutionScopeDenialReturnsForbidden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"scope", executionscope.ErrDenied, http.StatusForbidden},
		{"wrapped scope", fmt.Errorf("start: %w", executionscope.ErrDenied), http.StatusForbidden},
		{"infrastructure", errors.New("private database read failed"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			respondBPMNError(ctx, tc.err, "流程操作失败")
			assert.Equal(t, tc.status, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "private database")
		})
	}
}
