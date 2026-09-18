package workitemassignment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
)

func TestLifecycleWriterBindsIdentityAndLifetime(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, "sqlite3", fmt.Sprintf("file:assignment-lifetime-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer c.Close()
	tenant := c.Tenant.Create().SetName("live").SetCode("live").SetStatus("active").SaveX(ctx)
	actor := c.User.Create().SetUsername("actor").SetEmail("actor@example.test").SetName("actor").SetPasswordHash("hash").SetRole("end_user").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	other := c.User.Create().SetUsername("other").SetEmail("other@example.test").SetName("other").SetPasswordHash("hash").SetRole("end_user").SetTenantID(tenant.ID).SetActive(false).SaveX(ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	cmd := Command{ActorID: actor.ID, ActorTenantID: tenant.ID, TenantID: tenant.ID}
	var escaped *Writer
	enqueue := func(context.Context, *ent.Client, Event) error { return nil }
	err = WithLifecycleWriter(ctx, tx, nil, actor.ID, tenant.ID, enqueue, func(w *Writer, projection *ent.User) error {
		escaped = w
		require.NoError(t, w.validateIdentity(ctx, tx.Client(), cmd))
		require.Error(t, w.validateIdentity(ctx, c, cmd))
		require.Error(t, w.validateIdentity(tenantctx.WithTenantID(ctx, tenant.ID+1), tx.Client(), cmd))
		forged := cmd
		forged.ActorID = other.ID
		projection.ID = other.ID
		require.Error(t, w.validateIdentity(ctx, tx.Client(), forged))
		forged = cmd
		forged.ActorTenantID++
		require.Error(t, w.validateIdentity(ctx, tx.Client(), forged))
		forged = cmd
		forged.AssigneeID = other.ID
		require.Error(t, w.validateIdentity(ctx, tx.Client(), forged))
		require.NoError(t, w.validateIdentity(ctx, tx.Client(), cmd))
		return nil
	})
	require.NoError(t, err)
	require.Error(t, escaped.validateIdentity(ctx, tx.Client(), cmd))
	require.NoError(t, tx.Rollback())
	called := false
	require.Error(t, WithLifecycleWriter(ctx, tx, nil, actor.ID, tenant.ID, enqueue, func(*Writer, *ent.User) error { called = true; return nil }))
	require.False(t, called)
}

type lifecycleCloseFailure struct{ failure error }

func (d lifecycleCloseFailure) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return d.failure }, nil
}

func TestLifecycleWriterPropagatesDirectoryCloseFailure(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, "sqlite3", fmt.Sprintf("file:assignment-close-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer c.Close()
	tenant := c.Tenant.Create().SetName("live").SetCode("live").SetStatus("active").SaveX(ctx)
	actor := c.User.Create().SetUsername("actor").SetEmail("actor@example.test").SetName("actor").SetPasswordHash("hash").SetRole("end_user").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	failure := errors.New("directory close failed")
	called := false
	err = WithLifecycleWriter(ctx, tx, lifecycleCloseFailure{failure}, actor.ID, tenant.ID, func(context.Context, *ent.Client, Event) error { return nil }, func(*Writer, *ent.User) error { called = true; return nil })
	require.ErrorIs(t, err, failure)
	require.False(t, called)
}

// SQLite verifies policy composition only; PostgreSQL RLS and snapshot export
// remain covered by the real-infrastructure integration suite.
func TestLifecycleWriterUsesCurrentMSPAllocation(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, "sqlite3", fmt.Sprintf("file:assignment-msp-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer c.Close()
	provider := c.Tenant.Create().SetName("provider").SetCode("provider").SetType("msp_provider").SetStatus("active").SaveX(ctx)
	customer := c.Tenant.Create().SetName("customer").SetCode("customer").SetType("msp_customer").SetStatus("active").SaveX(ctx)
	actor := c.User.Create().SetUsername("actor").SetEmail("actor@example.test").SetName("actor").SetPasswordHash("hash").SetRole("end_user").SetTenantID(customer.ID).SetActive(true).SaveX(ctx)
	owner := c.User.Create().SetUsername("owner").SetEmail("owner@example.test").SetName("owner").SetPasswordHash("hash").SetRole("admin").SetMspRole("provider_agent").SetTenantID(provider.ID).SetActive(true).SaveX(ctx)
	allocation := c.MSPAllocation.Create().SetMspUserID(owner.ID).SetCustomerTenantID(customer.ID).SetRole("primary").SaveX(ctx)
	ctx = tenantctx.WithTenantID(ctx, customer.ID)
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	enqueue := func(context.Context, *ent.Client, Event) error { return nil }
	sentinel := errors.New("caller denied transition")
	err = WithLifecycleWriter(ctx, tx, nil, actor.ID, customer.ID, enqueue, func(w *Writer, _ *ent.User) error {
		cmd := Command{ActorID: actor.ID, ActorTenantID: customer.ID, TenantID: customer.ID, AssigneeID: owner.ID}
		require.NoError(t, w.validateIdentity(ctx, tx.Client(), cmd))
		require.NoError(t, tx.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).Exec(ctx))
		require.Error(t, w.validateIdentity(ctx, tx.Client(), cmd))
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Error(t, WithLifecycleWriter(ctx, tx, nil, owner.ID, customer.ID, enqueue, func(*Writer, *ent.User) error { t.Fatal("revoked actor reached callback"); return nil }))
	require.NoError(t, tx.Rollback())
	require.True(t, c.MSPAllocation.GetX(context.Background(), allocation.ID).DeassignedAt.IsZero())
}
