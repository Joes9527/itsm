package change

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
)

func TestChangeCommandRejectsMissingIdentity(t *testing.T) {
	_, err := (&Service{}).ApplyCommand(context.Background(), Command{Action: "implement", ChangeID: 1})
	require.ErrorContains(t, err, "trusted")
}

func TestStandardPolicyRequiresTimestampAndUnchangedScope(t *testing.T) {
	c := &ent.Change{Type: "standard", StandardTemplateID: 3, ImplementationPlan: "deploy", RollbackPlan: "restore", RiskLevel: "low", ImpactScope: "low", AffectedCis: []string{"7"}}
	c.StandardPolicy = map[string]any{"templateId": 3, "tenantId": 1, "active": true, "approvalRequired": false, "implementationPlan": "deploy", "rollbackPlan": "restore", "riskLevel": "low", "impactScope": "low", "affectedCis": []string{"7"}}
	require.False(t, qualifyingStandardPolicy(c, 1), "missing policy time cannot authorize")
	c.StandardPolicy["updatedAt"] = "2026-09-01T12:00:00Z"
	require.True(t, qualifyingStandardPolicy(c, 1))
	c.AffectedCis = []string{"8"}
	require.False(t, qualifyingStandardPolicy(c, 1), "changed applicability needs CAB")
}

func TestAssessmentBindsCurrentProfessionalFacts(t *testing.T) {
	c := &ent.Change{Type: "normal", ImplementationPlan: "v1", RollbackPlan: "restore", AffectedCis: []string{"7"}}
	first, err := assessmentDigest(c)
	require.NoError(t, err)
	c.ImplementationPlan = "v2"
	second, err := assessmentDigest(c)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestChangeClosureIsNotSuccess(t *testing.T) {
	for _, outcome := range []string{"closed", "failed", "rolled_back", ""} {
		if IsSuccessfulOutcome(outcome) {
			t.Fatalf("%q counted as success", outcome)
		}
	}
	if !IsSuccessfulOutcome("successful") {
		t.Fatal("success missing")
	}
}
