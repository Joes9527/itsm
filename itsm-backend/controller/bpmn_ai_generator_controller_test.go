package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBPMNAIGeneratorController_RoutesRequireBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &BPMNAIGeneratorController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "l1_support")
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/bpmn/ai/templates/suggestions", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
