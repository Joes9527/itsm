package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCORSMiddlewareAllowsWorkItemIdempotencyPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ITSM_CORS_ALLOWED_ORIGINS", "http://localhost:3001")
	router := gin.New()
	router.Use(CORSMiddleware())
	router.POST("/api/v1/tickets", func(c *gin.Context) { c.Status(http.StatusCreated) })
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/tickets", nil)
	request.Header.Set("Origin", "http://localhost:3001")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "content-type,authorization,x-csrf-token,idempotency-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "http://localhost:3001", response.Header().Get("Access-Control-Allow-Origin"))
	allowed := strings.Split(strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), ",")
	for i := range allowed {
		allowed[i] = strings.TrimSpace(allowed[i])
	}
	for _, header := range []string{"content-type", "authorization", "x-csrf-token", "idempotency-key"} {
		assert.Contains(t, allowed, header, "browser must be able to send the creation idempotency key")
	}
}

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Save original env
	originalEnv := os.Getenv("ITSM_CORS_ALLOWED_ORIGINS")
	defer os.Setenv("ITSM_CORS_ALLOWED_ORIGINS", originalEnv)

	// Clear the env to use default behavior (echo origin)
	os.Unsetenv("ITSM_CORS_ALLOWED_ORIGINS")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("OPTIONS", "/", nil)
	c.Request.Header.Set("Origin", "http://localhost:3000")

	CORSMiddleware()(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
	// In development mode (no ITSM_CORS_ALLOWED_ORIGINS set), it echoes the origin
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, PUT, PATCH, DELETE, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
}

func TestCORSMiddleware_WithAllowedOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Set allowed origins
	os.Setenv("ITSM_CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8080")
	defer os.Unsetenv("ITSM_CORS_ALLOWED_ORIGINS")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("OPTIONS", "/", nil)
	c.Request.Header.Set("Origin", "http://localhost:3000")

	CORSMiddleware()(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_WithoutOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Clear the env
	os.Unsetenv("ITSM_CORS_ALLOWED_ORIGINS")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("OPTIONS", "/", nil)
	// No Origin header

	CORSMiddleware()(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
	// Without origin header, defaults to *
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}
