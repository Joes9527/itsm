package service_catalog

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
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
		SetTargetClass(TargetClassServiceRequestItem).
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
		SetTargetClass(TargetClassServiceRequestItem).
		Save(ctx)
	require.NoError(t, err)

	got, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, "vm", got.ServiceType)
}

// TestEntRepository_Create_WritesTargetClassDirectlyNoITSMTypeDerivation 覆盖 Task 14 的核心
// 契约：target_class 是调用方显式提供的值，repository 直接落库，不再从 itsm_type（已随
// migration 024 一起删除）派生。
func TestEntRepository_Create_WritesTargetClassDirectlyNoITSMTypeDerivation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_direct?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "变更申请", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1,
		TargetClass: TargetClassChangeRequest,
	})
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, created.TargetClass, "target_class must come from the caller-supplied value, not a derivation from ITSMType")

	fetched, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, fetched.TargetClass)
}

// TestEntRepository_Create_RejectsMissingTargetClass 覆盖 Create 不再有 fail-safe 默认值：
// 空 target_class 必须报错，而不是静默落到 service_request_item。
func TestEntRepository_Create_RejectsMissingTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_required?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	_, err := repo.Create(ctx, &ServiceCatalog{Name: "变更申请", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1})
	require.Error(t, err)
}

// TestEntRepository_Create_RejectsInvalidTargetClass 覆盖非法取值（不在三个受约束枚举内）
// 同样必须被拒绝。
func TestEntRepository_Create_RejectsInvalidTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_invalid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	_, err := repo.Create(ctx, &ServiceCatalog{
		Name: "变更申请", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1,
		TargetClass: "bogus_class",
	})
	require.Error(t, err)
}

// TestEntRepository_Update_WritesTargetClassDirectlyNoITSMTypeDerivation 覆盖 Update 侧同样
// 的直写契约：传入的 catalog.TargetClass 原样落库。
func TestEntRepository_Update_WritesTargetClassDirectlyNoITSMTypeDerivation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_update_direct?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "事件上报目录", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1,
		TargetClass: TargetClassServiceRequestItem,
	})
	require.NoError(t, err)

	created.Name = "事件上报目录（已编辑）"
	created.TargetClass = TargetClassIncident
	updated, err := repo.Update(ctx, 1, created)
	require.NoError(t, err)
	assert.Equal(t, TargetClassIncident, updated.TargetClass)

	fetched, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassIncident, fetched.TargetClass)
}

// TestEntRepository_Update_RejectsInvalidTargetClass 覆盖 Update 同样拒绝非法/空 target_class
// ——防御性校验跟 Create 保持一致，即便 Service 层已经做过一次校验。
func TestEntRepository_Update_RejectsInvalidTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_target_class_update_invalid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := repo.Create(ctx, &ServiceCatalog{
		Name: "事件上报目录", Category: "运维", DeliveryTime: 1, Status: "enabled", TenantID: 1,
		TargetClass: TargetClassServiceRequestItem,
	})
	require.NoError(t, err)

	created.TargetClass = "bogus_class"
	_, err = repo.Update(ctx, 1, created)
	require.Error(t, err)
}
