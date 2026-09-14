package config

import "testing"

func TestExecutionConfigRequiresExplicitModeAndScope(t *testing.T) {
	for _, cfg := range []ExecutionConfig{
		{}, {Mode: "candidate", DeploymentID: "candidate"},
		{Mode: "standard", DeploymentID: "candidate", Capabilities: map[string]string{"unknown": "disabled"}},
		{Mode: "candidate", DeploymentID: "candidate", Scopes: []ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"embedding": "scoped"}},
		{Mode: "candidate", DeploymentID: "candidate", Scopes: []ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"connector_diagnostics": "scoped"}},
	} {
		if cfg.Validate() == nil {
			t.Fatalf("accepted invalid execution config: %#v", cfg)
		}
	}
	cfg := ExecutionConfig{Mode: "standard", DeploymentID: "candidate", Capabilities: map[string]string{"outbox": "enabled"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled("outbox") || cfg.Enabled("embedding") {
		t.Fatal("unspecified capability must remain disabled")
	}
}
