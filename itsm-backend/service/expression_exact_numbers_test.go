package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExpressionEngineExactJSONNumbers(t *testing.T) {
	vars := map[string]any{"amount": json.Number("9007199254740993.125"), "values": []any{json.Number("2"), json.Number("0.005")}}
	for _, expression := range []string{`amount > 9007199254740993.12`, `amount + values[1] == 9007199254740993.13`, `values[0] == 2`, `amount != nil && amount > 0`, `amount * (values[0] / 3) == amount * 2 / 3`, `-amount < -9007199254740993.12`} {
		t.Run(expression, func(t *testing.T) {
			result, err := NewExpressionEngine().EvaluateCondition(expression, vars)
			require.NoError(t, err)
			require.True(t, result)
		})
	}
	require.Equal(t, json.Number("9007199254740993.125"), vars["amount"], "evaluation must not rewrite frozen input")
}

func TestExpressionEngineRejectsInvalidExactNumbersAndOperations(t *testing.T) {
	for _, value := range []json.Number{"NaN", "1/2", "", "1e999999999"} {
		_, err := NewExpressionEngine().EvaluateCondition("amount > 0", map[string]any{"amount": value})
		require.Error(t, err)
	}
	for _, expression := range []string{"amount / 0 > 1", "amount % 0.1 == 0"} {
		_, err := NewExpressionEngine().EvaluateCondition(expression, map[string]any{"amount": json.Number("1.2")})
		require.Error(t, err)
	}
}
