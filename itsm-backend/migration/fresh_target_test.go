package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyFreshTargetAcceptsOnlyExactCommittedPhases(t *testing.T) {
	prepared := freshPrepareRelationNames()
	ent := append(append([]string(nil), prepared...), freshEntRelationNames()...)
	current := append(append([]string(nil), ent...), freshBaselineRelationNames()...)

	phase, err := classifyFreshTargetRelations(nil)
	require.NoError(t, err)
	require.Equal(t, freshTargetEmpty, phase)

	phase, err = classifyFreshTargetRelations(prepared)
	require.NoError(t, err)
	require.Equal(t, freshTargetPrepared, phase)

	phase, err = classifyFreshTargetRelations(ent)
	require.NoError(t, err)
	require.Equal(t, freshTargetEntSchema, phase)

	phase, err = classifyFreshTargetRelations(current)
	require.NoError(t, err)
	require.Equal(t, freshTargetCurrentRelease, phase)
}

func TestClassifyFreshTargetRejectsMixedLegacyOrExtraRelations(t *testing.T) {
	for name, relations := range map[string][]string{
		"legacy": {"tickets"},
		"mixed":  append(freshPrepareRelationNames(), "tickets"),
		"extra":  append(append(freshPrepareRelationNames(), freshEntRelationNames()...), "operator_scratch"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := classifyFreshTargetRelations(relations)
			require.ErrorContains(t, err, "fresh target is neither empty nor a verified current-release phase")
		})
	}
}
