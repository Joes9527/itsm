package approver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolutionMetadataCarriesWhyThisPerson(t *testing.T) {
	r := Resolution{
		ApproverID:   42,
		Reason:       ReasonManagerChainLevel,
		Level:        2,
		FallbackUsed: false,
		Detail:       "climbed 2 hops from requester 7",
	}
	meta := r.Metadata()
	require.Equal(t, 42, meta["approverId"])
	require.Equal(t, "manager_chain_level", meta["reason"])
	require.Equal(t, 2, meta["level"])
	require.Equal(t, false, meta["fallbackUsed"])
	require.Equal(t, "climbed 2 hops from requester 7", meta["detail"])
}

// 未知取值必须被拒绝——引擎不得解释一个它不认识的来源。
func TestResolutionReasonVocabularyIsClosed(t *testing.T) {
	require.Error(t, validateResolution(Resolution{ApproverID: 1, Reason: "made_up"}))
	require.NoError(t, validateResolution(Resolution{ApproverID: 1, Reason: ReasonDirectManager}))
	require.Error(t, validateResolution(Resolution{ApproverID: 0, Reason: ReasonDirectManager}),
		"approver id 0 is not a resolution")
	require.Error(t, validateResolution(Resolution{Reason: ReasonFallbackGroup, FallbackUsed: false}),
		"fallback_group must be marked as fallback")
}
