package authorization

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"testing"
	"time"
)

func TestLifecycleActorPreservesInfrastructureErrors(t *testing.T) {
	for _, source := range []string{"actor", "tenant"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:lifecycle-error-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
			defer client.Close()
			tenant := client.Tenant.Create().SetName("live").SetCode("live").SetStatus("active").SaveX(ctx)
			actor := createTenantSessionUser(t, client, tenant.ID, "actor", "super_admin", "")
			failure := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) { return nil, context.DeadlineExceeded })
			})
			if source == "actor" {
				client.User.Intercept(failure)
			} else {
				client.Tenant.Intercept(failure)
			}
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = ResolveLifecycleActor(ctx, tx, nil, actor.ID, tenant.ID)
			require.ErrorIs(t, err, context.DeadlineExceeded, "temporary lookup failure must retain retryable cause")
		})
	}
}
