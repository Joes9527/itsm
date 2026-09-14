//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/common/tenantctx"
	"itsm-backend/migration"
)

func TestPostgresTokenStateAuthorityAndConcurrency(t *testing.T) {
	_, dsn := candidatePrivatePostgresDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	name := "auth_state_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	role := name + "_run"
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	defer func() {
		_, e := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, e)
		_, e = admin.Exec("DROP ROLE " + role)
		require.NoError(t, e)
	}()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT")
	require.NoError(t, err)
	owner, err := sql.Open("postgres", dsn(name, "candidate_test_owner"))
	require.NoError(t, err)
	defer owner.Close()
	_, err = owner.ExecContext(ctx, migration.GetMigrationSQL("046_auth_token_state"))
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, "GRANT SELECT ON public.auth_state_authorities TO "+role+"; GRANT SELECT,INSERT ON public.auth_token_states TO "+role)
	require.NoError(t, err)
	authority := uuid.NewString()
	connect := func() (*sql.DB, authentication.TokenStateStore) {
		db, err := sql.Open("postgres", dsn(name, role))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		store, err := authentication.NewPostgresTokenStateStore(db, authority)
		require.NoError(t, err)
		return db, store
	}
	_, first := connect()
	require.Error(t, first.Ready(ctx), "missing authority cannot mean an empty safe store")
	_, err = owner.ExecContext(ctx, "INSERT INTO public.auth_state_authorities(authority_id,deployment_id,schema_version) VALUES($1,'private-state-test',1)", authority)
	require.NoError(t, err)
	require.NoError(t, first.Ready(ctx))
	_, second := connect()
	token, err := authentication.GenerateAccessToken(7, "operator", "agent", 3, "private-pg-state", time.Hour)
	require.NoError(t, err)
	claims, err := authentication.ValidateAccessToken(ctx, token, "private-pg-state")
	require.NoError(t, err)
	credential := claims.Credential()
	revoked, err := second.IsRevoked(ctx, credential)
	require.NoError(t, err)
	require.False(t, revoked)
	require.NoError(t, first.Revoke(ctx, credential))
	require.NoError(t, first.Revoke(ctx, credential))
	revoked, err = second.IsRevoked(ctx, credential)
	require.NoError(t, err)
	require.True(t, revoked)
	_, err = second.IsRevoked(tenantctx.WithTenantID(ctx, 4), credential)
	require.Error(t, err)
	require.Error(t, first.ConsumeRefresh(ctx, credential))
	require.Error(t, first.Revoke(ctx, &authentication.VerifiedCredential{}))

	refresh, err := authentication.GenerateRefreshToken(7, "operator", "agent", 3, "private-pg-state", time.Hour)
	require.NoError(t, err)
	consumer := authentication.NewRefreshTokenConsumer("private-pg-state", nil)
	verified, err := consumer.Validate(refresh)
	require.NoError(t, err)
	refreshCredential := verified.Credential()
	var wg sync.WaitGroup
	outcomes := make(chan error, 32)
	for i := 0; i < 32; i++ {
		_, store := connect()
		wg.Add(1)
		go func() { defer wg.Done(); outcomes <- store.ConsumeRefresh(ctx, refreshCredential) }()
	}
	wg.Wait()
	close(outcomes)
	succeeded, consumed := 0, 0
	for err := range outcomes {
		if err == nil {
			succeeded++
		} else if errors.Is(err, authentication.ErrRefreshTokenConsumed) {
			consumed++
		} else {
			t.Errorf("unexpected token state error: %v", err)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 31, consumed)
	require.ErrorIs(t, second.ConsumeRefresh(ctx, refreshCredential), authentication.ErrRefreshTokenConsumed)

	connector, err := pq.NewConnector(dsn(name, role))
	require.NoError(t, err)
	var loseCommit atomic.Bool
	faultDB := sql.OpenDB(&authCommitFaultConnector{inner: connector, fail: &loseCommit})
	defer faultDB.Close()
	faultStore, err := authentication.NewPostgresTokenStateStore(faultDB, authority)
	require.NoError(t, err)
	require.NoError(t, faultStore.Ready(ctx))
	newAccess, err := authentication.GenerateAccessToken(8, "other-operator", "agent", 3, "private-pg-state", time.Hour)
	require.NoError(t, err)
	accessClaims, err := authentication.ValidateAccessToken(ctx, newAccess, "private-pg-state")
	require.NoError(t, err)
	loseCommit.Store(true)
	require.ErrorIs(t, faultStore.Revoke(ctx, accessClaims.Credential()), authentication.ErrTokenStateUnavailable)
	persisted, err := second.IsRevoked(ctx, accessClaims.Credential())
	require.NoError(t, err)
	require.True(t, persisted, "actual PG commit succeeded despite lost acknowledgement")
	newRefresh, err := authentication.GenerateRefreshToken(8, "other-operator", "agent", 3, "private-pg-state", time.Hour)
	require.NoError(t, err)
	refreshClaims, err := consumer.Validate(newRefresh)
	require.NoError(t, err)
	loseCommit.Store(true)
	require.ErrorIs(t, faultStore.ConsumeRefresh(ctx, refreshClaims.Credential()), authentication.ErrTokenStateUnavailable)
	require.ErrorIs(t, second.ConsumeRefresh(ctx, refreshClaims.Credential()), authentication.ErrRefreshTokenConsumed)

	var policyChangeErr error
	var policyProbe atomic.Bool
	policyProbe.Store(true)
	guardedDB := sql.OpenDB(&authCommitFaultConnector{inner: connector, fail: &loseCommit, beforeRead: func(readCtx context.Context) {
		if !policyProbe.CompareAndSwap(true, false) {
			return
		}
		ddlCtx, cancelDDL := context.WithTimeout(readCtx, 150*time.Millisecond)
		defer cancelDDL()
		_, policyChangeErr = owner.ExecContext(ddlCtx, "ALTER POLICY auth_token_state_tenant ON public.auth_token_states USING (false)")
	}})
	defer guardedDB.Close()
	guardedStore, err := authentication.NewPostgresTokenStateStore(guardedDB, authority)
	require.NoError(t, err)
	stillRevoked, readErr := guardedStore.IsRevoked(ctx, credential)
	_, err = owner.ExecContext(ctx, "ALTER POLICY auth_token_state_tenant ON public.auth_token_states USING (tenant_id = NULLIF(current_setting('app.current_tenant',true),'')::bigint)")
	require.NoError(t, err)
	require.Error(t, policyChangeErr, "policy DDL must not change the meaning of a state read after readiness validation")
	require.NoError(t, readErr)
	require.True(t, stillRevoked)

	ownerStore, err := authentication.NewPostgresTokenStateStore(owner, authority)
	require.NoError(t, err)
	require.Error(t, ownerStore.Ready(ctx), "owner is never the runtime auth role")
	_, err = owner.ExecContext(ctx, "GRANT UPDATE ON public.auth_token_states TO "+role)
	require.NoError(t, err)
	require.Error(t, first.Ready(ctx))
	_, err = owner.ExecContext(ctx, "REVOKE UPDATE ON public.auth_token_states FROM "+role)
	require.NoError(t, err)
	require.NoError(t, first.Ready(ctx))
	_, err = owner.ExecContext(ctx, "GRANT UPDATE(actor_id) ON public.auth_token_states TO "+role)
	require.NoError(t, err)
	require.Error(t, first.Ready(ctx), "column-level writes also violate append-only runtime permissions")
	_, err = owner.ExecContext(ctx, "REVOKE UPDATE(actor_id) ON public.auth_token_states FROM "+role)
	require.NoError(t, err)
	require.NoError(t, first.Ready(ctx))
	_, err = owner.ExecContext(ctx, "ALTER POLICY auth_token_state_tenant ON public.auth_token_states USING (false)")
	require.NoError(t, err)
	_, err = first.IsRevoked(ctx, credential)
	require.ErrorIs(t, err, authentication.ErrTokenStateUnavailable, "a wrong policy must not hide revocation and allow a token")
	_, err = owner.ExecContext(ctx, "ALTER POLICY auth_token_state_tenant ON public.auth_token_states USING (tenant_id = NULLIF(current_setting('app.current_tenant',true),'')::bigint)")
	require.NoError(t, err)
	require.NoError(t, first.Ready(ctx))
	closedDB, closedStore := connect()
	require.NoError(t, closedDB.Close())
	require.ErrorIs(t, closedStore.Ready(ctx), authentication.ErrTokenStateUnavailable)
	_, err = owner.ExecContext(ctx, "ALTER TABLE public.auth_token_states RENAME TO auth_token_states_unavailable")
	require.NoError(t, err)
	_, err = first.IsRevoked(ctx, credential)
	require.ErrorIs(t, err, authentication.ErrTokenStateUnavailable)
	require.ErrorIs(t, first.Revoke(ctx, credential), authentication.ErrTokenStateUnavailable)
}

// The real PostgreSQL driver commits; this wrapper drops only its success
// acknowledgement. It models an uncertain response without undoing the write.
type authCommitFaultConnector struct {
	inner      driver.Connector
	fail       *atomic.Bool
	beforeRead func(context.Context)
}

func (c *authCommitFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &authCommitFaultConn{Conn: conn, fail: c.fail, beforeRead: c.beforeRead}, nil
}
func (c *authCommitFaultConnector) Driver() driver.Driver { return c.inner.Driver() }

type authCommitFaultConn struct {
	driver.Conn
	fail       *atomic.Bool
	beforeRead func(context.Context)
}

func (c *authCommitFaultConn) Begin() (driver.Tx, error) {
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &authCommitFaultTx{Tx: tx, fail: c.fail}, nil
}

func (c *authCommitFaultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &authCommitFaultTx{Tx: tx, fail: c.fail}, nil
}

func (c *authCommitFaultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func (c *authCommitFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.beforeRead != nil && strings.HasPrefix(q, "SELECT tenant_id,actor_id,expires_at FROM public.auth_token_states") {
		c.beforeRead(ctx)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

type authCommitFaultTx struct {
	driver.Tx
	fail *atomic.Bool
}

func (t *authCommitFaultTx) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.fail.CompareAndSwap(true, false) {
		return errors.New("private test lost PostgreSQL commit acknowledgement")
	}
	return nil
}
