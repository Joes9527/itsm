package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIncidentCommandRequiresVersionAndOperationID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"acknowledge", "resolve", "close", "reopen", "assign"} {
		t.Run(action, func(t *testing.T) {
			controller := &IncidentController{}
			router := gin.New()
			handlers := map[string]gin.HandlerFunc{"acknowledge": controller.AcknowledgeIncident, "resolve": controller.ResolveIncident, "close": controller.CloseIncident, "reopen": controller.ReopenIncident, "assign": controller.AssignIncident}
			router.POST("/incidents/:id/"+action, handlers[action])
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/incidents/1/"+action, strings.NewReader(`{"assigneeId":42}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Equal(t, 400, rec.Code)
		})
	}
}
