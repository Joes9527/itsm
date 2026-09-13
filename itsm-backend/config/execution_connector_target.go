package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// ConnectorTargetConfig is trusted startup input, never request enrollment.
// Credentials are resolved by the existing protected configuration loader.
// A declaration permits target activation only, not a business delivery.
type ConnectorTargetConfig struct {
	TenantID          int                    `mapstructure:"tenant_id"`
	ScopeID           string                 `mapstructure:"scope_id"`
	Name              string                 `mapstructure:"name"`
	Provider          string                 `mapstructure:"provider"`
	DestinationDigest string                 `mapstructure:"destination_digest"`
	Capabilities      []string               `mapstructure:"capabilities"`
	Credentials       map[string]string      `mapstructure:"credentials"`
	Settings          map[string]interface{} `mapstructure:"settings"`
}

func (c ExecutionConfig) validateConnectorTargets() error {
	if len(c.ConnectorTargets) == 0 {
		return nil
	}
	if c.Mode != "candidate" {
		return fmt.Errorf("connector targets require candidate execution")
	}
	scopes := make(map[int]string, len(c.Scopes))
	for _, scope := range c.Scopes {
		scopes[scope.TenantID] = scope.ScopeID
	}
	seen := make(map[string]bool, len(c.ConnectorTargets))
	for i, target := range c.ConnectorTargets {
		// Errors identify the declaration position, never credentials/settings.
		invalid := func() error { return fmt.Errorf("invalid connector target declaration at index %d", i) }
		if target.TenantID <= 0 || scopes[target.TenantID] == "" || scopes[target.TenantID] != target.ScopeID {
			return invalid()
		}
		for _, key := range []string{target.Name, target.Provider} {
			if key == "" || strings.TrimSpace(key) != key || strings.Contains(key, "/") || strings.IndexFunc(key, unicode.IsControl) >= 0 {
				return invalid()
			}
		}
		key := fmt.Sprintf("%d/%s/%s", target.TenantID, target.Name, target.Provider)
		if seen[key] {
			return invalid()
		}
		seen[key] = true
		digest, err := hex.DecodeString(target.DestinationDigest)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != target.DestinationDigest {
			return invalid()
		}
		if len(target.Capabilities) == 0 {
			return invalid()
		}
		capabilities := make(map[string]bool)
		for _, capability := range target.Capabilities {
			// These are existing delivery owners, not connector atomic methods.
			switch capability {
			case "notification", "webhook", "outbox":
			default:
				return invalid()
			}
			if capabilities[capability] || !c.Enabled(capability) {
				return invalid()
			}
			capabilities[capability] = true
		}
		raw, err := json.Marshal(target.Settings)
		if err != nil {
			return invalid()
		}
		// Runtime settings use JSON values. Reject lossy numeric normalization
		// rather than silently changing a configured destination/identity.
		var normalized map[string]interface{}
		if err := json.Unmarshal(raw, &normalized); err != nil {
			return invalid()
		}
		roundTrip, err := json.Marshal(normalized)
		if err != nil || !bytes.Equal(raw, roundTrip) {
			return invalid()
		}
	}
	return nil
}
