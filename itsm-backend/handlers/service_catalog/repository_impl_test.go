package service_catalog

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestEntRepository_Search_IncludesLegacyActiveStatus(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	repo := NewEntRepository(client)

	_, err := client.ServiceCatalog.Create().
		SetName("VM Service").
		SetCategory("it_service").
		SetDescription("virtual machine").
		SetDeliveryTime(1).
		SetStatus("active").
		SetTenantID(1).
		Save(ctx)
	require.NoError(t, err)

	list, total, err := repo.Search(ctx, 1, "vm", ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	require.Equal(t, "active", list[0].Status)
}

func TestEntRepository_ToDomain_CarriesServiceType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_type?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := client.ServiceCatalog.Create().
		SetName("云服务器申请").
		SetCategory("云资源").
		SetDescription("desc").
		SetDeliveryTime(1).
		SetStatus("active").
		SetTenantID(1).
		SetServiceType("vm").
		Save(ctx)
	require.NoError(t, err)

	got, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, "vm", got.ServiceType)
}

func TestTargetClassAuthority_CreateRequiresExplicitValidTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_create?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "云主机申请", Category: "云资源", DeliveryTime: 1, Status: "enabled", TenantID: 1,
	})
	require.Error(t, err)
	require.Nil(t, created)
}

func TestTargetClassAuthority_UpdatePersistsExplicitTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_update?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := client.ServiceCatalog.Create().
		SetName("事件上报目录").SetCategory("运维").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(1).SetTargetClass(TargetClassServiceRequestItem).
		Save(ctx)
	require.NoError(t, err)

	current, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	current.Name = "事件上报目录（已编辑）"
	current.TargetClass = TargetClassIncident
	updated, err := repo.Update(ctx, 1, current)
	require.NoError(t, err)
	require.Equal(t, TargetClassIncident, updated.TargetClass)

	fetched, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, TargetClassIncident, fetched.TargetClass)
}

func TestEntRepository_GetActiveForIntakeUsesPersistedTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_intake_target?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := client.ServiceCatalog.Create().
		SetName("事件目录").SetCategory("运维").SetStatus("enabled").SetIsActive(true).
		SetTenantID(1).SetTargetClass(TargetClassIncident).
		Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	got, err := repo.GetActiveForIntake(ctx, tx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, TargetClassIncident, got.TargetClass)
	require.Equal(t, created.UpdatedAt.UTC().Format(time.RFC3339Nano), got.Version)
	require.NoError(t, tx.Rollback())
}

func TestEntRepository_GetActiveForIntakeHidesDisabledAndCrossTenantCatalogs(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_intake_hidden?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	disabled, err := client.ServiceCatalog.Create().
		SetName("停用目录").SetStatus("disabled").SetIsActive(false).SetTenantID(1).
		SetTargetClass(TargetClassServiceRequestItem).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = repo.GetActiveForIntake(ctx, tx, 1, disabled.ID)
	require.True(t, ent.IsNotFound(err))
	_, err = repo.GetActiveForIntake(ctx, tx, 2, disabled.ID)
	require.True(t, ent.IsNotFound(err))
	require.NoError(t, tx.Rollback())
}
