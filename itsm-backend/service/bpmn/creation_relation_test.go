package bpmn

import (
	"testing"

	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestCreationCallbackSourceRelations(t *testing.T) {
	values := map[string]any{"title": "Change", "source_relations": []any{map[string]any{"sourceWorkItemId": 7, "expectedVersion": 3, "relationType": "resolved_by_change", "metadata": map[string]any{"required": true}}}}
	cmd, _, err := creationCommandFromCallback(values, "callback", creation.RecordClassChangeRequest, 1)
	require.NoError(t, err)
	require.Len(t, cmd.SourceRelations, 1)
	require.Equal(t, 3, cmd.SourceRelations[0].ExpectedVersion)
	require.True(t, cmd.SourceRelations[0].Metadata.Required)
	for _, field := range []string{"related_tickets", "related_ticket_numbers"} {
		_, _, err = creationCommandFromCallback(map[string]any{"title": "Change", field: []any{7}}, "old", creation.RecordClassChangeRequest, 1)
		require.Error(t, err)
	}
}
