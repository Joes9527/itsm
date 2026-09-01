package schema

import (
	"testing"

	"entgo.io/ent"
	"github.com/stretchr/testify/require"
)

func TestProfessionalExtensionsDoNotOwnWorkItemSharedFields(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		fields []ent.Field
	}{
		{name: "incident", fields: (Incident{}).Fields()},
		{name: "problem", fields: (Problem{}).Fields()},
		{name: "change", fields: (Change{}).Fields()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fieldNames := make([]string, 0, len(testCase.fields))
			for _, schemaField := range testCase.fields {
				fieldNames = append(fieldNames, schemaField.Descriptor().Name)
			}
			for _, sharedField := range []string{"title", "description", "status", "priority"} {
				require.NotContains(t, fieldNames, sharedField,
					"%s is WorkItem-owned and must not be stored on the %s extension", sharedField, testCase.name)
			}
		})
	}
}
