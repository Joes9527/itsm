//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"itsm-backend/migration"
	"strings"
	"testing"
	"time"
)

func TestAuthTokenStateSchemaIsolation(t *testing.T) {
	_, dsn := candidatePrivatePostgresDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "auth_schema_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	role := name + "_run"
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	defer func() { _, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); require.NoError(t, err) }()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT")
	require.NoError(t, err)
	t.Cleanup(func() {
		db, e := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
		require.NoError(t, e)
		defer db.Close()
		_, e = db.Exec("DROP ROLE " + role)
		require.NoError(t, e)
	})
	owner, err := sql.Open("postgres", dsn(name, "candidate_test_owner"))
	require.NoError(t, err)
	defer owner.Close()
	_, err = owner.ExecContext(ctx, "CREATE TABLE legacy_business (id integer PRIMARY KEY, value text); INSERT INTO legacy_business VALUES (1,'preserved')")
	require.NoError(t, err)
	migrationSQL := migration.GetMigrationSQL("046_auth_token_state")
	require.NotEmpty(t, migrationSQL)
	_, err = owner.ExecContext(ctx, migrationSQL)
	require.NoError(t, err)
	var count int
	require.NoError(t, owner.QueryRowContext(ctx, "SELECT count(*) FROM public.auth_state_authorities").Scan(&count))
	require.Zero(t, count, "application authority must be explicitly provisioned")
	authority := uuid.NewString()
	_, err = owner.ExecContext(ctx, "INSERT INTO public.auth_state_authorities(authority_id,deployment_id,schema_version) VALUES ($1,'private-auth-test',1)", authority)
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, "GRANT SELECT ON public.auth_state_authorities TO "+role+"; GRANT SELECT,INSERT ON public.auth_token_states TO "+role)
	require.NoError(t, err)
	runtime, err := sql.Open("postgres", dsn(name, role))
	require.NoError(t, err)
	defer runtime.Close()
	digest := strings.Repeat("a", 64)
	insert := "INSERT INTO public.auth_token_states(authority_id,purpose,token_digest,tenant_id,actor_id,expires_at) VALUES ($1,'access_revocation',$2,3,7,$3)"
	_, err = runtime.ExecContext(ctx, insert, authority, digest, time.Now().Add(time.Hour))
	require.Error(t, err, "missing tenant cannot insert")
	tx, err := runtime.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_tenant','3',true)")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, insert, authority, digest, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	for _, tenant := range []string{"3", "4"} {
		tx, err := runtime.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_tenant',$1,true)", tenant)
		require.NoError(t, err)
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM public.auth_token_states").Scan(&count))
		if tenant == "3" {
			require.Equal(t, 1, count)
		} else {
			require.Zero(t, count)
		}
		require.NoError(t, tx.Rollback())
	}
	for _, statement := range []string{"DELETE FROM public.auth_token_states", "UPDATE public.auth_token_states SET actor_id=99", "TRUNCATE public.auth_token_states", "DELETE FROM public.auth_state_authorities", "ALTER TABLE public.auth_token_states DISABLE ROW LEVEL SECURITY"} {
		_, err := runtime.ExecContext(ctx, statement)
		require.Error(t, err)
	}
	var value string
	require.NoError(t, owner.QueryRowContext(ctx, "SELECT value FROM legacy_business WHERE id=1").Scan(&value))
	require.Equal(t, "preserved", value)
}
