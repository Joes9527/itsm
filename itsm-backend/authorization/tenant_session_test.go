package authorization

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	entuser "itsm-backend/ent/user"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func createTenantSessionUser(t *testing.T, client *ent.Client, tenantID int, username, role, mspRole string) *ent.User {
	t.Helper()
	builder := client.User.Create().
		SetUsername(username).
		SetEmail(username + "@example.test").
		SetName(username).
		SetPasswordHash("hash").
		SetRole(role).
		SetTenantID(tenantID).
		SetActive(true)
	if mspRole != "" {
		builder.SetMspRole(entuser.MspRole(mspRole))
	}
	return builder.SaveX(context.Background())
}

func TestAuthorizeTenantSessionSupportsOnlyNativeSuperAdminAndAllocatedMSP(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:tenant-session-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	now := time.Now()
	nativeTenant := client.Tenant.Create().SetName("native").SetCode("native-session").SetType("standard").SetStatus("active").SaveX(ctx)
	mspTenant := client.Tenant.Create().SetName("msp").SetCode("msp-session").SetType("msp_provider").SetStatus("active").SaveX(ctx)
	customerTenant := client.Tenant.Create().SetName("customer").SetCode("customer-session").SetType("msp_customer").SetStatus("active").SaveX(ctx)
	otherTenant := client.Tenant.Create().SetName("other").SetCode("other-session").SetType("standard").SetStatus("active").SaveX(ctx)

	native := createTenantSessionUser(t, client, nativeTenant.ID, "native-session-user", "end_user", "")
	superAdmin := createTenantSessionUser(t, client, nativeTenant.ID, "super-session-user", "super_admin", "")
	mspAgent := createTenantSessionUser(t, client, mspTenant.ID, "msp-session-user", "admin", "provider_agent")
	client.MSPAllocation.Create().SetMspUserID(mspAgent.ID).SetCustomerTenantID(customerTenant.ID).SetRole("primary").SaveX(ctx)

	for name, tc := range map[string]struct {
		actor  *ent.User
		target int
	}{
		"native":        {actor: native, target: nativeTenant.ID},
		"super_admin":   {actor: superAdmin, target: otherTenant.ID},
		"allocated_msp": {actor: mspAgent, target: customerTenant.ID},
	} {
		t.Run(name, func(t *testing.T) {
			tenant, err := AuthorizeTenantSession(ctx, client, tc.actor, tc.target, now)
			require.NoError(t, err)
			require.Equal(t, tc.target, tenant.ID)
		})
	}

	_, err := AuthorizeTenantSession(ctx, client, native, otherTenant.ID, now)
	require.ErrorIs(t, err, ErrTenantAccessDenied)
	_, err = AuthorizeTenantSession(ctx, client, mspAgent, otherTenant.ID, now)
	require.ErrorIs(t, err, ErrTenantAccessDenied)
}

func TestAuthorizeTenantSessionRejectsInactiveAndExpiredTenant(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:tenant-session-state-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	now := time.Now()
	inactive := client.Tenant.Create().SetName("inactive").SetCode("inactive-session").SetStatus("suspended").SaveX(ctx)
	expired := client.Tenant.Create().SetName("expired").SetCode("expired-session").SetStatus("active").SetExpiresAt(now.Add(-time.Minute)).SaveX(ctx)
	actor := createTenantSessionUser(t, client, inactive.ID, "inactive-session-user", "super_admin", "")

	_, err := AuthorizeTenantSession(ctx, client, actor, inactive.ID, now)
	require.True(t, errors.Is(err, ErrTenantInactive))
	_, err = AuthorizeTenantSession(ctx, client, actor, expired.ID, now)
	require.True(t, errors.Is(err, ErrTenantExpired))
}
