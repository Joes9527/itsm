package problem

import "testing"

func TestProblemRequiresPermanentVerification(t *testing.T) {
	if ValidateResolution(ResolutionEvidence{RootCause: "known"}) == nil {
		t.Fatal("workaround-only accepted")
	}
	e := ResolutionEvidence{RootCause: "bad lock", PermanentSolution: "fixed lock", Verified: true, VerificationNote: "WMS regression passed"}
	if err := ValidateResolution(e); err != nil {
		t.Fatal(err)
	}
	e.Verified = false
	if ValidateResolution(e) == nil {
		t.Fatal("unverified fix accepted")
	}
}
