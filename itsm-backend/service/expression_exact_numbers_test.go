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
	for _, value := range []json.Number{"NaN", "1/2", "", "1e999999999", ".5", "-.5", "1.", "01.25", "1_0.5"} {
		_, err := NewExpressionEngine().EvaluateCondition("amount > 0", map[string]any{"amount": value})
		require.Error(t, err)
	}
	for _, expression := range []string{"amount / 0 > 1", "amount % 0.1 == 0"} {
		_, err := NewExpressionEngine().EvaluateCondition(expression, map[string]any{"amount": json.Number("1.2")})
		require.Error(t, err)
	}
}

func TestExpressionEnginePreservesExprNumericLiteralSyntax(t *testing.T) {
	for _, expression := range []string{
		"quantity > .5", "-quantity < -.5", "quantity > +.5", "quantity > 1.",
		"quantity < .5e+1", "quantity > 01.25", "quantity < 1_0.5_0",
		"quantity == 0x2", "quantity == 0b10", "quantity == 0o2",
	} {
		t.Run(expression, func(t *testing.T) {
			native, err := NewExpressionEngine().EvaluateCondition(expression, map[string]any{"quantity": 2})
			require.NoError(t, err, "fixture must use syntax the existing expr engine accepts")
			require.True(t, native)
			frozen, err := NewExpressionEngine().EvaluateCondition(expression, map[string]any{"quantity": json.Number("2")})
			require.NoError(t, err)
			require.Equal(t, native, frozen)
		})
	}
	precise, err := NewExpressionEngine().EvaluateCondition(".1000000000000000000000000000000001 > .1", map[string]any{"quantity": json.Number("2")})
	require.NoError(t, err)
	require.True(t, precise, "literal conversion must retain original digits")
}
