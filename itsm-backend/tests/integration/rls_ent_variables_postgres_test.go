//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
)

func TestPostgresRLSEntVariables(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	_, err := f.db.ExecContext(f.ctx, `CREATE TABLE variable_probe(id integer PRIMARY KEY, value text)`)
	require.NoError(t, err)
	drv, db := runtimeRLSDriver(t, f)
	tenant := tenantctx.WithTenantID(f.ctx, 1)
	variables := entsql.WithVar(tenant, "application_name", "ent-withvar")
	assertQuery := func(ctx context.Context, query interface {
		Query(context.Context, string, any, any) error
	}, want string) {
		rows := &entsql.Rows{}
		require.NotPanics(t, func() {
			err = query.Query(ctx, "SELECT current_setting('application_name'),current_setting('app.current_tenant')", []any{}, rows)
		})
		require.NoError(t, err)
		require.True(t, rows.Next())
		var name, id string
		require.NoError(t, rows.Scan(&name, &id))
		require.Equal(t, want, name)
		require.Equal(t, "1", id)
		require.NoError(t, rows.Close())
	}
	require.NotPanics(t, func() {
		err = drv.Exec(variables, "INSERT INTO variable_probe VALUES(1,current_setting('application_name'))", []any{}, nil)
	})
	require.NoError(t, err)
	var stored string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT value FROM variable_probe WHERE id=1").Scan(&stored))
	require.Equal(t, "ent-withvar", stored)
	assertQuery(variables, drv, "ent-withvar")
	tx, err := drv.BeginTx(variables, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	assertQuery(variables, tx, "ent-withvar")
	changed := entsql.WithVar(tenant, "application_name", "transaction-var")
	require.NoError(t, tx.Exec(changed, "INSERT INTO variable_probe VALUES(2,current_setting('application_name'))", []any{}, nil))
	assertQuery(changed, tx, "transaction-var")
	require.NoError(t, tx.Commit())
	require.Zero(t, db.Stats().InUse)
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT current_setting('application_name')").Scan(&stored))
	require.Empty(t, stored, "transaction variables must be cleared before pool reuse")
	for _, key := range []string{"app.current_tenant", "role", "session_authorization", "row_security"} {
		ctx := entsql.WithVar(tenant, key, "2")
		require.ErrorContains(t, drv.Exec(ctx, "SELECT 1", []any{}, nil), "reserved session variable")
		require.ErrorContains(t, drv.Query(ctx, "SELECT 1", []any{}, &entsql.Rows{}), "reserved session variable")
		_, err = drv.Tx(ctx)
		require.ErrorContains(t, err, "reserved session variable")
		require.Zero(t, db.Stats().InUse, "rejected checkout cannot leak")
	}
	// Public Ent variable keys are SQL identifiers. Even alternate casing or
	// quoted names cannot override the canonical context's tenant value.
	assertQuery(entsql.WithVar(variables, `"app.current_tenant"`, "2"), drv, "ent-withvar")
	tx, err = drv.Tx(variables)
	require.NoError(t, err)
	assertQuery(entsql.WithVar(variables, `APP.CURRENT_TENANT`, "2"), tx, "ent-withvar")
	require.NoError(t, tx.Rollback())
	invalid := entsql.WithVar(tenant, "this_variable_does_not_exist", "bad")
	require.Error(t, drv.Exec(invalid, "SELECT 1", []any{}, nil))
	require.Zero(t, db.Stats().InUse)
	_, err = drv.Tx(invalid)
	require.Error(t, err)
	require.Zero(t, db.Stats().InUse)
	tx, err = drv.Tx(tenant)
	require.NoError(t, err)
	require.Error(t, tx.Exec(invalid, "SELECT 1", []any{}, nil))
	require.NoError(t, tx.Rollback())
	require.Zero(t, db.Stats().InUse)
	cancelled, cancel := context.WithCancel(variables)
	cancel()
	require.Error(t, drv.Query(cancelled, "SELECT 1", []any{}, &entsql.Rows{}))
	require.Zero(t, db.Stats().InUse)
	reusable, cancel := context.WithTimeout(variables, time.Second)
	defer cancel()
	assertQuery(reusable, drv, "ent-withvar")
	require.Zero(t, db.Stats().InUse)
}
