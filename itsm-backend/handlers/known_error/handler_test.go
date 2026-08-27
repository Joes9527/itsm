package known_error

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestKnownErrorHandler_RoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l3_expert") })
	h.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/known-errors", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
