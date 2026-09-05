package bpmn

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestGetIntFromVarsRejectsLossyIntegerValues(t *testing.T) {
	for _, value := range []any{1.5, float32(16777216), math.Inf(1), json.Number("1.5"), json.Number("1e40"), "bad", float64(9007199254740992)} {
		require.Zero(t, GetIntFromVars(map[string]any{"id": value}, "id"), "value %v", value)
	}
	for _, value := range []any{json.Number("2"), json.Number("2.0"), "2", 2, int64(2), float64(2)} {
		require.Equal(t, 2, GetIntFromVars(map[string]any{"id": value}, "id"), "value %v", value)
	}
}
