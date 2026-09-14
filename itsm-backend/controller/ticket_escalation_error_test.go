package controller

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	creation "itsm-backend/handlers/common/workitemcreation"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTicketEscalationErrorClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"permission", creation.NewPermissionDenied("missing", nil), 403},
		{"scope", executionscope.ErrDenied, 403},
		{"version", common.NewVersionConflictError("ticket", 1, 1, 2), 409},
		{"infrastructure", creation.NewInfrastructureUnavailable("directory", errors.New("secret database detail")), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			respondTicketEscalationError(ctx, tc.err)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "secret database detail")
		})
	}
}
func TestTicketEscalationRequiresCommandMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"reason":"help"}`, `{"reason":"help","version":1}`, `{"reason":"help","operationId":"once"}`} {
		router := gin.New()
		owner := &TicketController{}
		router.POST("/tickets/:id/escalate", owner.EscalateTicket)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/tickets/1/escalate", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code)
	}
}
