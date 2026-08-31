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

// TestEntRepository_Create_SyncsTargetClassFromITSMType 覆盖任务包验收标准："新建
// ServiceCatalog 时 target_class 被正确同步设置（不再是空值）"。Service.Create 目前不
// 暴露设置 itsm_type 的参数（domain 字面量里 ITSMType 是零值 ""），这条测试直接验证
// repository 层：即便传入的 catalog.ITSMType 是 ""，Create() 也必须落一个非空、且与
// ComputeTargetClass("") 一致的 target_class，不能让新记录继续是空值。
func TestEntRepository_Create_SyncsTargetClassFromITSMType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_create?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "云主机申请", Category: "云资源", DeliveryTime: 1, Status: "enabled", TenantID: 1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.TargetClass, "新建 ServiceCatalog 的 target_class 不应该是空值")
	require.Equal(t, TargetClassServiceRequestItem, created.TargetClass)

	// 从数据库重新查询，确认真的落库了（不是只在内存里的返回值上有值）。
	fetched, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, TargetClassServiceRequestItem, fetched.TargetClass)
}

// TestEntRepository_Update_SelfHealsTargetClassFromCurrentITSMType 覆盖存量数据的自愈路径：
// 一条 itsm_type=Incident 但 target_class 还是空值的存量行（模拟还没跑
// cmd/backfill_servicecatalog_target_class 的情形），只要被编辑保存一次（走
// handlers/service_catalog.Service.Update → repository Update），target_class 就应该被
// 按当前 itsm_type 重新计算、纠正为非空值，不需要额外单独跑回填脚本。
func TestEntRepository_Update_SelfHealsTargetClassFromCurrentITSMType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_update?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := client.ServiceCatalog.Create().
		SetName("事件上报目录").SetCategory("运维").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(1).SetItsmType("Incident"). // target_class 留空，模拟未回填的存量行
		Save(ctx)
	require.NoError(t, err)
	require.Empty(t, created.TargetClass)

	current, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Incident", current.ITSMType)

	current.Name = "事件上报目录（已编辑）"
	updated, err := repo.Update(ctx, 1, current)
	require.NoError(t, err)
	require.Equal(t, TargetClassIncident, updated.TargetClass, "Update 时应该按当前 itsm_type 自愈同步 target_class")

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
		SetTenantID(1).SetItsmType("Request").SetTargetClass(TargetClassIncident).
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
