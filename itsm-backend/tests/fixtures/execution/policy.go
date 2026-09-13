// Package execution supplies explicit deployment settings to test fixtures.
package execution

import (
	"itsm-backend/config"
	"itsm-backend/database"
)

func Standard() *database.ExecutionPolicy {
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "test-standard"})
	if err != nil {
		panic(err)
	}
	return policy
}
