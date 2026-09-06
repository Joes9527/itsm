package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"itsm-backend/common"
	"os"
	"strings"
	"time"
)

type IntakeIdentityProviderConfig struct {
	Secret   string   `json:"secret"`
	Channels []string `json:"channels"`
	Purposes []string `json:"purposes"`
}
type IntakeIdentityConfig struct {
	Providers                    map[string]IntakeIdentityProviderConfig
	MaxAge, FutureSkew, TokenTTL time.Duration
}

// Only a role-restricted server file may supply exchange provider secrets.
// This file is independent from JWT, webhook and automation credentials.
func loadIntakeIdentityConfig(getenv func(string) string) (IntakeIdentityConfig, error) {
	result := IntakeIdentityConfig{Providers: map[string]IntakeIdentityProviderConfig{}, MaxAge: time.Minute, FutureSkew: 5 * time.Second, TokenTTL: 5 * time.Minute}
	path := getenv("INTAKE_IDENTITY_CONFIG_FILE")
	if path == "" {
		return result, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("identity config file unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return result, fmt.Errorf("identity config requires a regular owner-only file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return result, fmt.Errorf("identity config exceeds limit")
	}
	if _, err = common.DecodeJSONObject(raw); err != nil {
		return result, fmt.Errorf("invalid identity config object")
	}
	var wire struct {
		Providers  map[string]IntakeIdentityProviderConfig `json:"providers"`
		MaxAge     string                                  `json:"maxAge"`
		FutureSkew string                                  `json:"futureSkew"`
		TokenTTL   string                                  `json:"tokenTTL"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil {
		return result, fmt.Errorf("invalid identity config fields")
	}
	for _, entry := range []struct {
		raw      string
		value    *time.Duration
		min, max time.Duration
	}{{wire.MaxAge, &result.MaxAge, time.Second, 5 * time.Minute}, {wire.FutureSkew, &result.FutureSkew, 0, 30 * time.Second}, {wire.TokenTTL, &result.TokenTTL, time.Second, 15 * time.Minute}} {
		if entry.raw != "" {
			v, err := time.ParseDuration(entry.raw)
			if err != nil || v < entry.min || v > entry.max || v%time.Second != 0 {
				return result, fmt.Errorf("invalid identity time window")
			}
			*entry.value = v
		}
	}
	valid := func(value string) bool {
		return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n")
	}
	for provider, p := range wire.Providers {
		if !valid(provider) || !valid(p.Secret) || len(p.Channels) == 0 || len(p.Purposes) == 0 {
			return result, fmt.Errorf("invalid identity provider configuration")
		}
		seen := map[string]bool{}
		for _, channel := range p.Channels {
			if !valid(channel) || seen[channel] {
				return result, fmt.Errorf("invalid identity channel configuration")
			}
			seen[channel] = true
		}
		seen = map[string]bool{}
		for _, purpose := range p.Purposes {
			if (purpose != "create" && purpose != "read") || seen[purpose] {
				return result, fmt.Errorf("invalid identity purpose configuration")
			}
			seen[purpose] = true
		}
		result.Providers[provider] = p
	}
	return result, nil
}
func validateIdentitySecretSeparation(cfg IntakeIdentityConfig, secrets ...string) error {
	for _, p := range cfg.Providers {
		for _, secret := range secrets {
			if secret != "" && p.Secret == secret {
				return fmt.Errorf("identity exchange secrets must be separate from other service credentials")
			}
		}
	}
	return nil
}
