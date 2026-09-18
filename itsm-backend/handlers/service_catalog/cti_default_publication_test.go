package service_catalog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
)

// ctiPublicationFixture 准备一个租户、一棵三级分类树与目录服务。
func ctiPublicationFixture(t *testing.T) (context.Context, *ent.Client, *Service, int, [3]int) {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	tenant := client.Tenant.Create().SetName("CTI publication").SetCode("cti-publication").SaveX(ctx)
	categories := service.NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{"network", "remote", "vpn"} {
		record, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant.ID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	// 复用既有发布 fixture：注册了真实 creator registry，发布校验才能通过。
	// 无流程的默认绑定让 generic 目录具备可发布的履约路径。
	client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType("generic").
		SetProcessDefinitionKey("none").SetIsDefault(true).
		SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	owner := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), sameTransactionDirectory{})
	return ctx, client, owner, tenant.ID, ids
}

func enforceCatalogCTI(t *testing.T, ctx context.Context, client *ent.Client, tenantID int) {
	t.Helper()
	client.SystemConfig.Create().SetTenantID(tenantID).SetKey(service.CTIGovernanceConfigKey).
		SetValue(`{"catalogEnforced":true}`).SetValueType("json").SaveX(ctx)
}

func TestCatalogDraftMayOmitDefaultClassification(t *testing.T) {
	ctx, _, owner, tenantID, _ := ctiPublicationFixture(t)
	draft, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Draft", Category: "IT", TargetClass: "generic", Status: "disabled"})
	require.NoError(t, err, "目录草稿可以不完整")
	require.Zero(t, draft.DefaultTicketCategoryID)

	read, err := owner.Get(ctx, tenantID, draft.ID)
	require.NoError(t, err)
	require.Nil(t, defaultTicketCategoryID(read.DefaultTicketCategoryID))
	require.Empty(t, read.DefaultCTIPath)
}

// 未启用门禁的既有目录保持可发布，不会被新代码阻断；启用门禁后必须配置完整三级。
func TestCatalogPublicationRequiresCompleteDefaultOnlyWhenEnforced(t *testing.T) {
	ctx, client, owner, tenantID, ids := ctiPublicationFixture(t)
	legacy, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Legacy", Category: "IT", TargetClass: "generic", Status: "enabled"})
	require.NoError(t, err, "未启用门禁时，既有形态的目录仍可发布")
	require.Zero(t, legacy.DefaultTicketCategoryID)

	enforceCatalogCTI(t, ctx, client, tenantID)
	_, err = owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Enforced", Category: "IT", TargetClass: "generic", Status: "enabled"})
	require.ErrorContains(t, err, "default classification")

	published, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Enforced ok", Category: "IT", TargetClass: "generic", Status: "enabled", DefaultTicketCategoryID: &ids[2]})
	require.NoError(t, err)
	require.Equal(t, ids[2], published.DefaultTicketCategoryID)
	require.Len(t, published.DefaultCTIPath, 3)
	require.Equal(t, ids[0], published.DefaultCTIPath[0].ID)
	require.Equal(t, "network / remote / vpn", joinPathNames(published.DefaultCTIPath))
}

func TestCatalogPublicationRejectsPartialOrInactiveDefault(t *testing.T) {
	ctx, client, owner, tenantID, ids := ctiPublicationFixture(t)
	categories := service.NewTicketCategoryService(client)

	// 二级节点不是完整三级路径。
	_, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Partial", Category: "IT", TargetClass: "generic", Status: "enabled", DefaultTicketCategoryID: &ids[1]})
	require.ErrorContains(t, err, "three-level")
	require.Zero(t, client.ServiceCatalog.Query().CountX(ctx), "失败的发布必须整笔回滚")

	// 停用祖先同样不构成有效路径。
	disabled := false
	_, err = categories.UpdateCategory(ctx, ids[0], &service.UpdateCategoryRequest{IsActive: &disabled}, tenantID)
	require.NoError(t, err)
	_, err = owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Inactive", Category: "IT", TargetClass: "generic", Status: "enabled", DefaultTicketCategoryID: &ids[2]})
	require.ErrorContains(t, err, "three-level")
	require.Zero(t, client.ServiceCatalog.Query().CountX(ctx), "失败的发布必须整笔回滚")
}

// 默认分类（含派生路径）属于公开确认契约：变更必须改变 CatalogVersion。
func TestCatalogVersionTracksDefaultClassification(t *testing.T) {
	ctx, client, owner, tenantID, ids := ctiPublicationFixture(t)
	categories := service.NewTicketCategoryService(client)
	other, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: "vpn-alt", Code: "vpn-alt", ParentID: ids[1], IsActive: true, TenantID: tenantID})
	require.NoError(t, err)

	published, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "VPN", Category: "IT", TargetClass: "generic", Status: "enabled", DefaultTicketCategoryID: &ids[2]})
	require.NoError(t, err)
	require.NotEmpty(t, published.CatalogVersion)

	changed, err := owner.Update(ctx, tenantID, published.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: published.CatalogVersion, DefaultTicketCategoryID: &other.ID})
	require.NoError(t, err)
	require.NotEqual(t, published.CatalogVersion, changed.CatalogVersion, "默认分类变化必须让旧确认失效")
	require.Equal(t, other.ID, changed.DefaultTicketCategoryID)

	// 改名同样改变派生路径指纹。
	renamed, err := owner.Update(ctx, tenantID, changed.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: changed.CatalogVersion, Name: strPtr("VPN renamed")})
	require.NoError(t, err)
	require.NotEqual(t, changed.CatalogVersion, renamed.CatalogVersion)

	// 分类改名也会改变目录的公开路径指纹。
	client.TicketCategory.UpdateOneID(ids[0]).SetName("network-renamed").ExecX(ctx)
	reread, err := owner.Get(ctx, tenantID, renamed.ID)
	require.NoError(t, err)
	require.NotEqual(t, renamed.CatalogVersion, reread.CatalogVersion)
}

// 草稿允许不完整，但结构引用必须属于本租户。
func TestCatalogDefaultClassificationMustBelongToTenant(t *testing.T) {
	ctx, client, owner, tenantID, _ := ctiPublicationFixture(t)
	otherTenant := client.Tenant.Create().SetName("Other").SetCode("other-tenant").SaveX(ctx)
	foreign := client.TicketCategory.Create().SetName("foreign").SetCode("foreign").SetTenantID(otherTenant.ID).SaveX(ctx)

	_, err := owner.Create(ctx, tenantID, dto.CreateServiceCatalogRequest{Name: "Foreign", Category: "IT", TargetClass: "generic", Status: "disabled", DefaultTicketCategoryID: &foreign.ID})
	require.ErrorContains(t, err, "outside the tenant")
	require.Zero(t, client.ServiceCatalog.Query().CountX(ctx))
}

func defaultTicketCategoryID(id int) *int {
	if id <= 0 {
		return nil
	}
	value := id
	return &value
}

func joinPathNames(path []CTIPathNode) string {
	names := ""
	for index, node := range path {
		if index > 0 {
			names += " / "
		}
		names += node.Name
	}
	return names
}

func strPtr(value string) *string { return &value }
