package config

import (
	"fmt"
	"itsm-backend/common/executionscope"
)

type ExecutionScopeConfig struct {
	TenantID int    `mapstructure:"tenant_id"`
	ScopeID  string `mapstructure:"scope_id"`
}
type ExecutionConfig struct {
	Mode         string                 `mapstructure:"mode"`
	DeploymentID string                 `mapstructure:"deployment_id"`
	Scopes       []ExecutionScopeConfig `mapstructure:"scopes"`
	Capabilities map[string]string      `mapstructure:"capabilities"`
}

func (c ExecutionConfig) Validate() error {
	if c.Mode != "standard" && c.Mode != "candidate" {
		return fmt.Errorf("explicit standard or candidate execution mode required")
	}
	if err := executionscope.ValidateDeploymentID(c.DeploymentID); err != nil {
		return err
	}
	if c.Mode == "candidate" && len(c.Scopes) == 0 {
		return fmt.Errorf("candidate execution requires scopes")
	}
	if c.Mode == "standard" && len(c.Scopes) != 0 {
		return fmt.Errorf("standard execution cannot carry candidate scopes")
	}
	seen := map[int]bool{}
	for _, s := range c.Scopes {
		if seen[s.TenantID] {
			return fmt.Errorf("duplicate execution tenant")
		}
		seen[s.TenantID] = true
		if err := executionscope.ValidateRef(executionscope.Ref{DeploymentID: c.DeploymentID, ScopeID: s.ScopeID, TenantID: s.TenantID}); err != nil {
			return err
		}
	}
	for name, mode := range c.Capabilities {
		if c.Mode == "candidate" {
			if err := executionscope.ValidateCapability(name, executionscope.CapabilityMode(mode)); err != nil {
				return err
			}
		} else {
			if err := executionscope.ValidateCapability(name, executionscope.Disabled); err != nil {
				return err
			}
			if mode != "enabled" && mode != "disabled" {
				return fmt.Errorf("standard capabilities require enabled or disabled mode")
			}
		}
	}
	return nil
}

// Enabled is only valid after Validate and runtime role/scope admission.
func (c ExecutionConfig) Enabled(name string) bool {
	return c.Mode == "standard" && c.Capabilities[name] == "enabled" || c.Mode == "candidate" && c.Capabilities[name] == "scoped"
}
