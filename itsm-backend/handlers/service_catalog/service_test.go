package service_catalog

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestService_Create_SavesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	fields := []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text", Required: true, SortOrder: 0},
		{Name: "device_count", Label: "设备数量", FieldType: "number", Required: false, SortOrder: 1},
	}

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, fields, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.NotNil(t, created)

	// 返回对象应该携带刚保存的字段定义
	require.Len(t, created.Fields, 2)
	assert.Equal(t, "office_location", created.Fields[0].Name)
	assert.Equal(t, "device_count", created.Fields[1].Name)

	// 底层 field_definitions 表应该真正落库，entity_type 为 "service_catalog"
	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "office_location", listed[0].Name)
	assert.Equal(t, "办公地点", listed[0].Label)
	assert.Equal(t, "device_count", listed[1].Name)
}

func TestService_Create_NoClient_SkipsFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_noclient?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	// client == nil：不接入字段定义（沿用现有可选 client 的兼容路径）
	svc := NewService(repo, nil, zaptest.NewLogger(t).Sugar())

	fields := []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	}

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, fields, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.NotNil(t, created)
	// Create 仍然把入参 fields 回填到返回对象上
	require.Len(t, created.Fields, 1)
}

func TestService_Update_NilFields_LeavesExistingDefinitionsUntouched(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_nil?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	// Update 时不传 fields（nil）：既有字段定义应保持不变
	updated, err := svc.Update(ctx, 1, created.ID, "VM Service Updated", "", "", 0, "", 0, 0, nil, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.NotNil(t, updated)

	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "office_location", listed[0].Name)
}

func TestService_Update_NonNilFields_ReplacesDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_replace?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	newFields := []service.FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number"},
	}
	updated, err := svc.Update(ctx, 1, created.ID, "", "", "", 0, "", 0, 0, newFields, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.Len(t, updated.Fields, 1)
	assert.Equal(t, "device_count", updated.Fields[0].Name)

	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "device_count", listed[0].Name)
}

// TestService_Create_FieldDefinitions_CrossTenantIsolation 验证 Create 写入的字段定义
// 严格按 tenant_id 隔离：租户 B 即便拿到租户 A 目录的真实 entity_id，也读不到租户 A 的字段定义，
// 且租户 B 也不能通过 Service.Get 拿到租户 A 的目录本身（repo.Get 已按 tenantID 过滤）。
func TestService_Create_FieldDefinitions_CrossTenantIsolation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_tenant_isolation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().
		SetName("SC Tenant A").SetCode("SCA-" + scUID()).SetDomain("sc-a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().
		SetName("SC Tenant B").SetCode("SCB-" + scUID()).SetDomain("sc-b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	createdA, err := svc.Create(ctx, "VM Service A", "it_service", "tenant A catalog", 1, tenantA.ID, "enabled", 0, 0, []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text", Required: true},
	}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.Len(t, createdA.Fields, 1)

	// 租户 B 用租户 A 目录的真实 entity_id 去查字段定义，必须查不到任何东西
	listedForB, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenantB.ID, "service_catalog", createdA.ID)
	require.NoError(t, err)
	assert.Empty(t, listedForB, "tenant B must not see tenant A's field definitions even with a valid entity_id")

	// 租户 A 自己查得到
	listedForA, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenantA.ID, "service_catalog", createdA.ID)
	require.NoError(t, err)
	require.Len(t, listedForA, 1)
	assert.Equal(t, "office_location", listedForA[0].Name)

	// 租户 B 也不能通过 Service.Get 拿到租户 A 的目录本身
	_, err = svc.Get(ctx, tenantB.ID, createdA.ID)
	assert.Error(t, err, "tenant B must not be able to fetch tenant A's service catalog")
}

func TestService_CreateAndGet_PersistsFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text", Required: true}}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	require.Len(t, created.Fields, 1)
	assert.Equal(t, "environment", created.Fields[0].Name)

	fetched, err := svc.Get(ctx, tenant.ID, created.ID)
	require.NoError(t, err)
	require.Len(t, fetched.Fields, 1)
	assert.Equal(t, "环境", fetched.Fields[0].Label)
}

func TestService_List_BatchLoadsFieldDefinitionsPerCatalog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_list_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-list-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	c1, err := svc.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)
	c2, err := svc.Create(ctx, "VPN权限", "网络", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	list, _, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	byID := map[int]*ServiceCatalog{}
	for _, c := range list {
		byID[c.ID] = c
	}
	require.Len(t, byID[c1.ID].Fields, 1)
	assert.Equal(t, "environment", byID[c1.ID].Fields[0].Name)
	assert.Empty(t, byID[c2.ID].Fields)
}

// TestService_Delete_DisablesFieldDefinitions 证明删除 service catalog 时同步禁用其字段定义
// （ListDefinitions 不再返回），但不会物理删除行——因为 repo.Delete 本身是软删除
// （status=disabled），目录随时可能被重新启用，字段定义配置必须能一起恢复
// （最终整分支评审 Fix 2 + 重新审查发现的回归修正：硬删除会导致目录恢复后字段配置永久丢失）。
func TestService_Delete_DisablesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_delete_disables_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-delete-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	listedBefore, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenant.ID, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listedBefore, 1)

	require.NoError(t, svc.Delete(ctx, tenant.ID, created.ID))

	listedAfter, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenant.ID, "service_catalog", created.ID)
	require.NoError(t, err)
	assert.Empty(t, listedAfter, "ListDefinitions should not return definitions for a disabled catalog")

	// 数据本身还在（is_active=false），不是被物理删除——这是跟旧的硬删除实现的关键区别：
	// 目录被恢复（status 改回 enabled）后，字段配置应该能一起恢复，而不是永久丢失。
	raw, err := client.FieldDefinition.Query().
		Where(fielddefinition.TenantID(tenant.ID), fielddefinition.EntityType("service_catalog"), fielddefinition.EntityID(created.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, raw, 1, "field_definitions row must survive a soft-deleted catalog, not be physically removed")
	assert.False(t, raw[0].IsActive)
	assert.Equal(t, "environment", raw[0].Name)
}

// TestService_Search_PopulatesFieldDefinitions 证明 Search 的结果也批量回填字段定义，
// 和 List/Get 保持一致（最终整分支评审 cleanup #3）。
func TestService_Search_PopulatesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_search_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-search-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "云主机申请搜索测试", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	results, total, err := svc.Search(ctx, tenant.ID, "云主机申请搜索", ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, results, 1)
	assert.Equal(t, created.ID, results[0].ID)
	require.Len(t, results[0].Fields, 1)
	assert.Equal(t, "environment", results[0].Fields[0].Name)
}

func TestService_Get_TenantIsolation_NoCrossTenantFieldLeak(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_tenant_isolation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenantA, err := client.Tenant.Create().SetName("A").SetCode("sc-tenant-a").SetDomain("a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("B").SetCode("sc-tenant-b").SetDomain("b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	catalogA, err := svc.Create(ctx, "服务A", "分类", "desc", 1, tenantA.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "secretField", Label: "租户A专属字段", FieldType: "text"}}, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	// 租户 B 用同样的 entity_id（碰巧撞上租户 A 的 catalog.ID）查询，不应该看到租户 A 的字段定义。
	// 用直接调 FieldDefinitionService 而非 svc.Get 来模拟"entity_id 相同、tenant_id 不同"这种最容易漏查 tenantID 的场景。
	defs, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenantB.ID, "service_catalog", catalogA.ID)
	require.NoError(t, err)
	assert.Empty(t, defs, "租户 B 不应该查到租户 A 的字段定义，即使 entity_id 相同")
}

// TestService_Create_RejectsMissingTargetClass 覆盖 Task 14 的核心契约：Create 不再从
// itsm_type 派生 target_class，调用方必须显式提供，否则必须报错，而不是静默落一个 fail-safe
// 默认值。
func TestService_Create_RejectsMissingTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_missing_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	_, err := svc.Create(ctx, "VM Service", "it_service", "desc", 1, 1, "enabled", 0, 0, nil, "", "", "")
	require.Error(t, err)
}

// TestService_Create_RejectsInvalidTargetClass 覆盖非法取值（不在三个受约束枚举内）同样必须
// 被拒绝，不能悄悄落库。
func TestService_Create_RejectsInvalidTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_invalid_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	_, err := svc.Create(ctx, "VM Service", "it_service", "desc", 1, 1, "enabled", 0, 0, nil, "", "", "bogus_class")
	require.Error(t, err)
}

// TestService_Create_PersistsSuppliedTargetClass 证明调用方提供的 targetClass 原样传到
// repository 写入的 ServiceCatalog 领域对象上（不是被某种计算覆盖）。
func TestService_Create_PersistsSuppliedTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_persists_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "变更申请目录", "运维", "desc", 1, 1, "enabled", 0, 0, nil, "", "", TargetClassChangeRequest)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, created.TargetClass)

	fetched, err := svc.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, fetched.TargetClass, "落库的值必须是调用方提供的值")
}

// TestService_Update_PreservesTargetClassWhenOmitted 覆盖"更新时省略 targetClass 保留当前
// 值"的语义——这与其它可选字段（name/category/...）的"空值即保留"约定一致。
func TestService_Update_PreservesTargetClassWhenOmitted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_preserve_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "事件上报目录", "运维", "desc", 1, 1, "enabled", 0, 0, nil, "", "", TargetClassIncident)
	require.NoError(t, err)

	// 无关字段的更新：targetClass 传空字符串（省略），必须保留原来的 incident。
	updated, err := svc.Update(ctx, 1, created.ID, "事件上报目录（已编辑）", "", "", 0, "", 0, 0, nil, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, TargetClassIncident, updated.TargetClass, "省略 targetClass 时必须保留当前值，不能被清空或改写")

	fetched, err := svc.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassIncident, fetched.TargetClass)
}

// TestService_Update_ChangesTargetClassWhenSupplied 覆盖显式提供新值时必须替换当前值。
func TestService_Update_ChangesTargetClassWhenSupplied(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_changes_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "服务目录", "运维", "desc", 1, 1, "enabled", 0, 0, nil, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	updated, err := svc.Update(ctx, 1, created.ID, "", "", "", 0, "", 0, 0, nil, "", "", TargetClassChangeRequest)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, updated.TargetClass)

	fetched, err := svc.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassChangeRequest, fetched.TargetClass)
}

// TestService_Update_RejectsInvalidTargetClass 覆盖更新时提供了值但值不合法的拒绝路径——
// 跟 Create 的校验规则完全一致，只是"提供了才校验"。
func TestService_Update_RejectsInvalidTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_invalid_target_class?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "服务目录", "运维", "desc", 1, 1, "enabled", 0, 0, nil, "", "", TargetClassServiceRequestItem)
	require.NoError(t, err)

	_, err = svc.Update(ctx, 1, created.ID, "", "", "", 0, "", 0, 0, nil, "", "", "bogus_class")
	require.Error(t, err)

	// 拒绝之后不能把已经落库的合法值改坏。
	fetched, err := svc.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, TargetClassServiceRequestItem, fetched.TargetClass)
}
