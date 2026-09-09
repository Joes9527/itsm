package change

import "testing"

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
