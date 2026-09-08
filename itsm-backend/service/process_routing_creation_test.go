package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRoutingExactDecimalConditions(t *testing.T) {
	service := &ProcessRoutingService{}
	values := map[string]any{"amount": json.Number("9007199254740993.125")}
	require.True(t, service.evaluateConditions(map[string]any{"min_amount": json.Number("9007199254740993.124")}, values))
	require.False(t, service.evaluateConditions(map[string]any{"amount": map[string]any{"gte": json.Number("9007199254740993.126")}}, values))
	require.False(t, routingEqual(int64(9007199254740993), int64(9007199254740992)))
}
