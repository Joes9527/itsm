package workerhealth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadinessFailsWhenDependencyIsUnavailable(t *testing.T) {
	server := New("127.0.0.1:0", func(context.Context) error { return errors.New("database unavailable") }, nil)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "not_ready\n", response.Body.String())
}

func TestReadinessAndLivenessAreHealthyWhenDependenciesAreReady(t *testing.T) {
	server := New("127.0.0.1:0", func(context.Context) error { return nil }, nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, response.Code, path)
	}
}
