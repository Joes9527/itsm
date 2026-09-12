// Package executionscope defines deployment execution restrictions, never domain authorization.
package executionscope

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

var ErrDenied = errors.New("execution scope is not permitted")

type Ref struct {
	DeploymentID string
	ScopeID      string
	TenantID     int
}

type CapabilityMode string

const (
	Disabled CapabilityMode = "disabled"
	Scoped   CapabilityMode = "scoped"
)

var deploymentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func ValidateRef(ref Ref) error {
	id, err := uuid.Parse(ref.ScopeID)
	if err != nil || id == uuid.Nil || id.String() != ref.ScopeID || ref.TenantID <= 0 || !deploymentPattern.MatchString(ref.DeploymentID) {
		return fmt.Errorf("%w: invalid deployment, scope or tenant identity", ErrDenied)
	}
	return nil
}

// ValidateCapability rejects unsupported scoped execution even for known capabilities.
// Callers must separately prove required journey capabilities are enabled.
func ValidateCapability(name string, mode CapabilityMode) error {
	if mode != Disabled && mode != Scoped {
		return fmt.Errorf("%w: unknown capability mode", ErrDenied)
	}
	switch name {
	case "outbox", "callback", "notification", "kaf_worker", "sla", "escalation", "event_audit", "webhook", "tool_queue", "request_async":
		return nil
	case "embedding", "connector_poll", "cloud_discovery", "cmdb_import_export":
		if mode == Disabled {
			return nil
		}
	}
	return fmt.Errorf("%w: unsupported capability", ErrDenied)
}
