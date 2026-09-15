package service_catalog

import (
	"context"
	"testing"

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

func TestEntRepository_PreservesExplicitTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)
	created, err := repo.Create(ctx, &ServiceCatalog{Name: "Consultation", Category: "IT", TenantID: 1, TargetClass: "generic", Status: "disabled", DeliveryTime: 1})
	require.NoError(t, err)
	require.Equal(t, "generic", created.TargetClass)
	created.TargetClass = "problem"
	updated, err := repo.Update(ctx, 1, created)
	require.NoError(t, err)
	require.Equal(t, "problem", updated.TargetClass)
	require.Equal(t, "problem", client.ServiceCatalog.GetX(ctx, created.ID).TargetClass)
}
