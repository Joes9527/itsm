package workitemcreation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputMeaningChangeCategoryWireAndDigest(t *testing.T) {
	raw := `{"idempotencyKey":"one","intakeKind":"change_request","recordClass":"change_request","confirmation":"confirmed","title":"Change","change":{"category":" Network "}}`
	digests := []string{}
	for _, category := range []string{" Network ", "Network", "Security"} {
		command, err := DecodeCreateWorkItemCommand(strings.NewReader(strings.Replace(raw, " Network ", category, 1)))
		require.NoError(t, err)
		_, digest, err := CanonicalizeCommand(command)
		require.NoError(t, err)
		digests = append(digests, digest)
	}
	require.Equal(t, digests[0], digests[1])
	require.NotEqual(t, digests[0], digests[2])
}
func TestInputMeaningRejectsRemovedPresetInstruction(t *testing.T) {
	_, err := DecodeCreateWorkItemCommand(strings.NewReader(`{"formPresetId":"static-vpn"}`))
	require.Error(t, err)
}

// Fixed digests captured from source 9f12521c before this input-contract slice.
func TestInputMeaningPreviousV4Digest(t *testing.T) {
	expected := []string{"2ec76db736b1fd9435526ee7bc7da176e37d1b83d7ac522cd997273b9dd9e08d", "203d4c44f2e3e535ad9805703c5e7acab9490299ac86c115674931ec950cc086"}
	for index, raw := range []string{
		`{"idempotencyKey":"one","intakeKind":"generic","recordClass":"generic","confirmation":"confirmed","title":"VPN access"}`,
		`{"idempotencyKey":"one","intakeKind":"change_request","recordClass":"change_request","confirmation":"confirmed","title":"Change","change":{"justification":"Security","impactScope":"low","riskLevel":"low","implementationPlan":"Deploy","rollbackPlan":"Restore"}}`,
	} {
		command, err := DecodeCreateWorkItemCommand(strings.NewReader(raw))
		require.NoError(t, err)
		_, digest, err := CanonicalizeCommand(command)
		require.NoError(t, err)
		require.Equal(t, expected[index], digest)
	}
}
