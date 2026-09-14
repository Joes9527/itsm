// Package execution supplies explicit deployment settings to test fixtures.
package execution

import (
	"itsm-backend/config"
	"itsm-backend/database"
)

func Standard(enabledCapabilities ...string) *database.ExecutionPolicy {
	capabilities := make(map[string]string, len(enabledCapabilities))
	for _, capability := range enabledCapabilities {
		capabilities[capability] = "enabled"
	}
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "test-standard", Capabilities: capabilities})
	if err != nil {
		panic(err)
	}
	return policy
}
