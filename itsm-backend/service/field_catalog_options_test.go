package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestCatalogOptionKeysPreserveTypesAndDetachNestedInput(t *testing.T) {
	options := []any{map[string]any{"label": "String", "value": "9007199254740993"}, map[string]any{"label": "Number", "value": json.Number("9007199254740993")}, map[string]any{"label": "Adjacent", "value": json.Number("9007199254740992")}}
	keys, err := ProjectCatalogOptions(options)
	require.NoError(t, err)
	require.Equal(t, "option:v1:IjkwMDcxOTkyNTQ3NDA5OTMi", keys[0].Key)
	require.Equal(t, "option:v1:OTAwNzE5OTI1NDc0MDk5Mw", keys[1].Key)
	require.Equal(t, "option:v1:OTAwNzE5OTI1NDc0MDk5Mg", keys[2].Key)
	fields := []creation.ResolvedFieldDefinition{{Key: "one", DataType: "select", Options: options}, {Key: "many", DataType: "multiselect", Options: options}, {Key: "notes", DataType: "text"}}
	many := make([]any, 3, 8)
	many[0], many[1], many[2] = keys[0].Key, keys[1].Key, keys[2].Key
	values := map[string]any{"one": keys[1].Key, "many": many, "notes": map[string]any{"nested": []any{map[string]any{"number": json.Number("9007199254740993")}}}}
	id := 1
	command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "catalog_item", Confirmation: "confirmed", IdempotencyKey: "immutable", Title: "Original", CatalogItemID: &id, CatalogVersion: "catalog-v1:original", FormSchemaVersion: "form-v1:original", FormValues: values}
	before, err := json.Marshal(command)
	require.NoError(t, err)
	_, beforeDigest, err := creation.CanonicalizeCommand(command)
	require.NoError(t, err)
	resolved, err := ResolveCatalogOptionKeys(fields, values)
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), resolved["one"])
	require.Equal(t, []any{"9007199254740993", json.Number("9007199254740993"), json.Number("9007199254740992")}, resolved["many"])
	resolved["one"] = "changed"
	resolved["many"].([]any)[0] = "changed"
	resolved["notes"].(map[string]any)["nested"].([]any)[0].(map[string]any)["number"] = "changed"
	after, err := json.Marshal(command)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, afterDigest, err := creation.CanonicalizeCommand(command)
	require.NoError(t, err)
	require.Equal(t, beforeDigest, afterDigest)
	for _, invalid := range []any{"option:v1:OTk5", "9007199254740993", json.Number("9007199254740993")} {
		_, err = ResolveCatalogOptionKeys(fields, map[string]any{"one": invalid})
		require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
	}
	_, err = ResolveCatalogOptionKeys(fields, map[string]any{"many": []any{keys[1].Key, "option:v1:OTk5"}})
	require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
}
