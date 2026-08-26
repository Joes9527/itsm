package controller

import (
	"strings"
	"testing"

	"itsm-backend/service/bpmn"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBPMNAuthorizationRegistryMatchesRegisteredRoutes guards against the
// exact class of gap this plan exists to close: GetCounterSignStatus shipped
// with zero authorization because no structured list of "every route needs
// an entry" existed. This test walks the actual gin routes RegisterRoutes
// produces and cross-checks them against bpmn.BPMNTaskInstanceAuthRegistry
// in both directions — a route with no registry entry, or a registry entry
// for a route that no longer exists, both fail the test.
func TestBPMNAuthorizationRegistryMatchesRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	controller := &BPMNWorkflowController{}
	controller.RegisterRoutes(group)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		path := strings.TrimPrefix(route.Path, "/api/v1")
		if !strings.HasPrefix(path, "/bpmn/tasks") && !strings.HasPrefix(path, "/bpmn/process-instances") {
			continue
		}
		registered[route.Method+" "+path] = true
	}

	registryKeys := make(map[string]bool, len(bpmn.BPMNTaskInstanceAuthRegistry))
	for _, entry := range bpmn.BPMNTaskInstanceAuthRegistry {
		key := entry.Method + " " + entry.Path
		registryKeys[key] = true
		assert.True(t, registered[key], "registry entry %s has no matching registered route —登记表和实际路由已经不一致", key)
	}
	for key := range registered {
		assert.True(t, registryKeys[key], "registered route %s has no authorization_registry.go entry — 新接口忘了登记", key)
	}
	require.NotEmpty(t, registered, "sanity check: RegisterRoutes must have produced at least one /bpmn/tasks or /bpmn/process-instances route")
}
