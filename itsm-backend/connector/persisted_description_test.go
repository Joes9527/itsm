package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
)

func TestManagerDescribesPersistedGraphInOwningTransaction(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:persisted-graph-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "persisted-description"})
	require.NoError(t, err)
	registry := connector.NewRegistry()
	created := 0
	registry.Register(func() connector.Connector { created++; return msgraph.New() })
	manager := connector.NewManager(registry, nil, policy)
	defer manager.CloseAll()
	describe, ok := any(manager).(interface {
		DescribePersistedDeliveryTarget(context.Context, executionscope.Ref, string, *ent.Client, string, string) (string, error)
	})
	require.True(t, ok, "persisted description must use the original configuration store")
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	ref, err := policy.EventRef(1)
	require.NoError(t, err)
	settings := map[string]interface{}{"azure_tenant_id": "test", "mailbox": "original@example.invalid"}
	credentials := map[string]string{"azure_client_id": "app"}
	rawSettings, _ := json.Marshal(settings)
	rawCredentials, _ := json.Marshal(credentials)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	row, err := tx.ConnectorConfig.Create().SetTenantID(1).SetName("msgraph-email").SetProvider("microsoft").SetEnabled(true).SetSettings(string(rawSettings)).SetCredentials(string(rawCredentials)).Save(ctx)
	require.NoError(t, err)
	_, err = tx.ConnectorConfig.Create().SetTenantID(2).SetName("msgraph-email").SetProvider("microsoft").SetEnabled(true).SetSettings("foreign malformed settings").Save(ctx)
	require.NoError(t, err)
	expected, err := msgraph.New().DescribeDeliveryDestination(connector.Config{Settings: settings, Credentials: credentials})
	require.NoError(t, err)
	actual, err := describe.DescribePersistedDeliveryTarget(ctx, ref, "notification", tx.Client(), "msgraph-email", "microsoft")
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, 1, created)
	require.Empty(t, manager.ListByTenant(1))
	require.ErrorIs(t, policy.RequireConnectorDelivery(ctx, ref, "notification"), executionscope.ErrDenied)
	for _, scenario := range []string{"foreign tenant", "wrong provider", "invalid JSON", "disabled", "duplicate", "missing", "invalid credentials", "canceled", "nil client"} {
		t.Run(scenario, func(t *testing.T) {
			targetRef := ref
			provider := "microsoft"
			name := "msgraph-email"
			readCtx, readClient := ctx, tx.Client()
			switch scenario {
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				readCtx = canceled
			case "nil client":
				readClient = nil
			case "invalid credentials":
				require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetCredentials("secret malformed JSON").Exec(ctx))
				defer func() {
					require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetCredentials(string(rawCredentials)).Exec(ctx))
				}()
			case "foreign tenant":
				targetRef.TenantID = 2
			case "wrong provider":
				provider = "other"
			case "invalid JSON":
				require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetSettings("secret malformed JSON").Exec(ctx))
				defer func() {
					require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetSettings(string(rawSettings)).Exec(ctx))
				}()
			case "disabled":
				require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetEnabled(false).Exec(ctx))
				defer func() { require.NoError(t, tx.ConnectorConfig.UpdateOne(row).SetEnabled(true).Exec(ctx)) }()
			case "duplicate":
				other, err := tx.ConnectorConfig.Create().SetTenantID(1).SetName(name).SetProvider("other").SetEnabled(true).Save(ctx)
				require.NoError(t, err)
				defer func() { require.NoError(t, tx.ConnectorConfig.DeleteOne(other).Exec(ctx)) }()
			case "missing":
				name = "missing"
			}
			digest, err := describe.DescribePersistedDeliveryTarget(readCtx, targetRef, "notification", readClient, name, provider)
			if scenario == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, executionscope.ErrDenied)
			}
			require.Empty(t, digest)
			require.NotContains(t, err.Error(), "secret malformed JSON")
		})
	}
	require.Equal(t, 1, created)
	require.NoError(t, tx.Rollback())
	count, err := client.ConnectorConfig.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	queryFailure := errors.New("injected configuration query failure")
	queries := 0
	client.Intercept(ent.InterceptFunc(func(ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) {
			queries++
			return nil, queryFailure
		})
	}))
	actual, err = describe.DescribePersistedDeliveryTarget(ctx, ref, "notification", client, "msgraph-email", "microsoft")
	require.ErrorIs(t, err, queryFailure)
	require.Empty(t, actual)
	require.Equal(t, 1, queries)
	manager.CloseAll()
	actual, err = describe.DescribePersistedDeliveryTarget(ctx, ref, "notification", client, "msgraph-email", "microsoft")
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.Empty(t, actual)
	require.Equal(t, 1, queries, "closed manager must not query the store")
}
