package service

import "testing"

// B2 step 1: the pure decision function that gates the Change -> Problem
// verification prompt. Only an explicit successful outcome requests verification;
// every other professional outcome must not be interpreted as a completed repair.
func TestChangeOutcomeRequestsVerification(t *testing.T) {
	if !RequiresProblemVerification("successful") {
		t.Fatal("successful change outcome must request problem verification")
	}
	for _, outcome := range []string{"failed", "rolled_back", "closed", "", "unknown"} {
		if RequiresProblemVerification(outcome) {
			t.Fatalf("outcome %q must not be treated as a verified repair", outcome)
		}
	}
}
