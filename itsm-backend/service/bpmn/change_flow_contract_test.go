package bpmn

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangeDefaultFlowsRequireProfessionalEvidence(t *testing.T) {
	for _, name := range []string{"change_normal_flow", "change_emergency_flow"} {
		body, err := os.ReadFile(name + ".bpmn")
		require.NoError(t, err)
		s := string(body)
		require.Contains(t, s, ">assess_risk<")
		require.Contains(t, s, ">review_change<")
		require.NotContains(t, s, "verify_passed")
		require.NotContains(t, s, ">update_change<")
		require.NotContains(t, s, ">reject_change<")
		if name == "change_emergency_flow" {
			require.NotContains(t, s, ">schedule_change<")
		} else {
			require.Contains(t, s, ">authorize_change<")
		}
	}
}
