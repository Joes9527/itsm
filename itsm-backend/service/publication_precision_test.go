package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	"testing"
)

func TestPublicationPersistedNumericOptionsAndRouting(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	field := client.FieldDefinition.Create().SetTenantID(1).SetEntityType("service_catalog").SetEntityID(1).SetName("quota").SetLabel("Quota").SetFieldType("select").SetRequired(true).SetOptions([]any{map[string]any{"label": "Exact", "value": json.Number("9007199254740993")}}).SaveX(ctx)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	validator := NewFieldDefinitionService(tx.Client())
	require.NoError(t, validator.ValidateCreationValues(ctx, tx, 1, "service_catalog", field.EntityID, map[string]any{"quota": json.Number("9007199254740993")}))
	require.Error(t, validator.ValidateCreationValues(ctx, tx, 1, "service_catalog", field.EntityID, map[string]any{"quota": json.Number("9007199254740992")}))
	require.NoError(t, tx.Rollback())
	client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetProcessDefinitionKey("exact").SetPriority(10).SetConditions(map[string]any{"amount": map[string]any{"gte": json.Number("9007199254740993.125")}}).SaveX(ctx)
	client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetProcessDefinitionKey("fallback").SetIsDefault(true).SaveX(ctx)
	router := NewProcessRoutingService(client, zap.NewNop().Sugar())
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	for _, tc := range []struct{ amount, key string }{{"9007199254740993.125", "exact"}, {"9007199254740993.124", "fallback"}} {
		route, err := router.FindBestRouteTx(ctx, tx, &RoutingContext{TenantID: 1, BusinessType: "ticket", Variables: map[string]any{"amount": json.Number(tc.amount)}})
		require.NoError(t, err)
		require.NotNil(t, route)
		require.Equal(t, tc.key, route.ProcessDefinitionKey)
	}
}
