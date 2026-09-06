package common

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestDecodeJSONObjectPreservesNumbersAndBounds(t *testing.T) {
	raw := []byte(`{"number":9007199254740993,"nested":[{"number":1e1000}],"extension":true}`)
	original := string(raw)
	object, err := DecodeJSONObject(raw)
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), object["number"])
	require.Equal(t, json.Number("1e1000"), object["nested"].([]any)[0].(map[string]any)["number"])
	require.Equal(t, original, string(raw))
	// Root plus 63 arrays is accepted; another container is rejected.
	_, err = DecodeJSONObject([]byte(`{"x":` + strings.Repeat("[", 63) + `0` + strings.Repeat("]", 63) + `}`))
	require.NoError(t, err)
	_, err = DecodeJSONObject([]byte(`{"x":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`))
	require.Error(t, err)
	prefix, suffix := `{"x":"`, `"}`
	_, err = DecodeJSONObject([]byte(prefix + strings.Repeat("a", (1<<20)-len(prefix)-len(suffix)) + suffix))
	require.NoError(t, err)
	_, err = DecodeJSONObject([]byte(prefix + strings.Repeat("a", (1<<20)-len(prefix)-len(suffix)+1) + suffix))
	require.Error(t, err)
}
