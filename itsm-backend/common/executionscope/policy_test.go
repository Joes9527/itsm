package executionscope

import "testing"

func TestValidateRefRejectsIncompleteIdentity(t *testing.T) {
	valid := Ref{DeploymentID: "candidate-20260912", ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9", TenantID: 1}
	if err := ValidateRef(valid); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []Ref{
		{}, {DeploymentID: valid.DeploymentID, TenantID: 1},
		{DeploymentID: valid.DeploymentID, ScopeID: "bad", TenantID: 1},
		{DeploymentID: valid.DeploymentID, ScopeID: valid.ScopeID, TenantID: 0},
		{DeploymentID: "candidate:other", ScopeID: valid.ScopeID, TenantID: 1},
		{DeploymentID: valid.DeploymentID, ScopeID: "00000000-0000-0000-0000-000000000000", TenantID: 1},
	} {
		if ValidateRef(ref) == nil {
			t.Fatalf("accepted invalid ref: %#v", ref)
		}
	}
}

func TestCapabilityConfigurationFailsClosed(t *testing.T) {
	for _, mode := range []CapabilityMode{Disabled, Scoped} {
		if err := ValidateCapability("outbox", mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		mode CapabilityMode
	}{
		{"unknown", Disabled}, {"outbox", "enabled"}, {"outbox", ""}, {"embedding", Scoped},
	} {
		if ValidateCapability(tc.name, tc.mode) == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
}
