package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestA2UITicketController_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &A2UITicketController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "end_user") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/a2ui/tickets", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
