package workitemcreation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceRelationsStrictWireAndCanonicalIdentity(t *testing.T) {
	base := `{"recordClass":"problem","intakeKind":"problem","confirmation":"confirmed","title":"investigation","idempotencyKey":"source-test","sourceRelations":`
	for _, raw := range []string{
		`[{"sourceWorkItemId":1,"relationType":"investigated_by","expected_version":1}]`,
		`[{"sourceWorkItemId":1,"relationType":"investigated_by","expectedVersion":1,"metadata":{"unknown":true}}]`,
		`[{"sourceWorkItemId":1,"relationType":"investigated_by","expectedVersion":1,"expectedVersion":2}]`,
		`[{"sourceWorkItemId":1,"relationType":"investigated_by","expectedVersion":null}]`,
	} {
		_, err := DecodeCreateWorkItemCommand(strings.NewReader(base + raw + `}`))
		require.Error(t, err, raw)
	}
	first, err := DecodeCreateWorkItemCommand(strings.NewReader(base + `[{"sourceWorkItemId":2,"relationType":"related_to","expectedVersion":3},{"sourceWorkItemId":1,"relationType":"investigated_by","expectedVersion":1}]}`))
	require.NoError(t, err)
	normalized, digest, err := CanonicalizeCommand(first)
	require.NoError(t, err)
	require.Equal(t, 1, normalized.SourceRelations[0].SourceWorkItemID)
	first.SourceRelations[0], first.SourceRelations[1] = first.SourceRelations[1], first.SourceRelations[0]
	_, reordered, err := CanonicalizeCommand(first)
	require.NoError(t, err)
	require.Equal(t, digest, reordered)
	for _, invalid := range []SourceRelationInput{{SourceWorkItemID: 1, ExpectedVersion: 0, RelationType: "related_to"}, {SourceWorkItemID: 0, ExpectedVersion: 1, RelationType: "related_to"}, {SourceWorkItemID: 1, ExpectedVersion: 1}} {
		first.SourceRelations = []SourceRelationInput{invalid}
		_, _, err = CanonicalizeCommand(first)
		require.ErrorIs(t, err, ErrInvalidCommand)
	}
}
