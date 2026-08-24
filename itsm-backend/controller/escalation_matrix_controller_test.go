package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestEscalationMatrixController_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &EscalationMatrixController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "ops_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/escalation-matrices", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
