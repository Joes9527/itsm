package router

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/controller"
	"testing"
)

func TestProblemRCARoutesMatchInvestigationClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, &RouterConfig{Logger: zap.NewNop().Sugar(), ProblemInvestigationController: &controller.ProblemInvestigationController{}})
	routes := map[string]bool{}
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, path := range []string{
		"POST /api/v1/problem-investigation/root-cause-analysis",
		"PUT /api/v1/problem-investigation/root-cause-analysis/:id",
		"GET /api/v1/problem-investigation/problems/:id/summary",
	} {
		require.True(t, routes[path], path)
	}
}
