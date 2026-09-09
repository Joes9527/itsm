package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIncidentCommandRequiresVersionAndOperationID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"acknowledge", "resolve", "close", "reopen"} {
		t.Run(action, func(t *testing.T) {
			controller := &IncidentController{}
			router := gin.New()
			handlers := map[string]gin.HandlerFunc{"acknowledge": controller.AcknowledgeIncident, "resolve": controller.ResolveIncident, "close": controller.CloseIncident, "reopen": controller.ReopenIncident}
			router.POST("/incidents/:id/"+action, handlers[action])
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/incidents/1/"+action, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Equal(t, 400, rec.Code)
		})
	}
}
