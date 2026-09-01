package schema

import (
	"reflect"
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

func TestProfessionalExtensionsEnforceOneWorkItemPerClass(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		fields  []ent.Field
		indexes []ent.Index
	}{
		{name: "incident", fields: (Incident{}).Fields(), indexes: (Incident{}).Indexes()},
		{name: "problem", fields: (Problem{}).Fields(), indexes: (Problem{}).Indexes()},
		{name: "change", fields: (Change{}).Fields(), indexes: (Change{}).Indexes()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fieldMatches := 0
			for _, schemaField := range testCase.fields {
				descriptor := schemaField.Descriptor()
				if descriptor.Name == "work_item_id" {
					fieldMatches++
					require.False(t, descriptor.Unique,
						"%s.work_item_id uniqueness must have one explicit index authority", testCase.name)
				}
			}
			require.Equal(t, 1, fieldMatches)

			matches := 0
			for _, schemaIndex := range testCase.indexes {
				descriptor := schemaIndex.Descriptor()
				if reflect.DeepEqual(descriptor.Fields, []string{"work_item_id"}) {
					matches++
					require.True(t, descriptor.Unique,
						"%s.work_item_id must have one authoritative unique index", testCase.name)
				}
			}
			require.Equal(t, 1, matches,
				"%s.work_item_id must not have duplicate or conflicting index declarations", testCase.name)
		})
	}
}
