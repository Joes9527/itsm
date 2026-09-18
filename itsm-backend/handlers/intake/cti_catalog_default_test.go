package intake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
)

// ctiCatalogFixture 准备一棵三级分类树+一个已发布目录，并安装真实的统一创建解析器。
type ctiCatalogFixture struct {
	client   *ent.Client
	app      *Service
	identity creation.Identity
	owner    *catalog.Service
	ids      [3]int
	altLeaf  int
}

func newCTICatalogFixture(t *testing.T) *ctiCatalogFixture {
	t.Helper()
	client, app, identity, _, _, _ := intakeFixture(t)
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	app.registry = NewCreatorRegistry()
	require.NoError(t, app.registry.Register(service.NewTicketServiceForTest(client, logger)))
	client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType("generic").
		SetProcessDefinitionKey("none").SetIsDefault(true).
		SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	owner := catalog.NewService(catalog.NewEntRepository(client), client, logger, sameTransactionDirectory{})
	owner.SetCreatorRegistry(app.registry)
	categories := service.NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{"network", "remote", "vpn"} {
		record, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: identity.TenantID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	alt, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: "vpn-alt", Code: "vpn-alt", ParentID: ids[1], IsActive: true, TenantID: identity.TenantID})
	require.NoError(t, err)
	app.resolver = NewResolver(owner, service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), categories)
	return &ctiCatalogFixture{client: client, app: app, identity: identity, owner: owner, ids: ids, altLeaf: alt.ID}
}

func (f *ctiCatalogFixture) publishCatalog(t *testing.T, name string, defaultLeaf *int) *catalog.ServiceCatalog {
	t.Helper()
	published, err := f.owner.Create(context.Background(), f.identity.TenantID, dto.CreateServiceCatalogRequest{
		Name: name, Category: "IT", TargetClass: "generic", Status: "enabled", DefaultTicketCategoryID: defaultLeaf,
	})
	require.NoError(t, err)
	return published
}

func (f *ctiCatalogFixture) command(catalogRecord *catalog.ServiceCatalog, key string) creation.CreateWorkItemCommand {
	return creation.CreateWorkItemCommand{
		RecordClass: "generic", IntakeKind: "catalog_item", Confirmation: "confirmed",
		Title: "VPN access", CatalogItemID: &catalogRecord.ID,
		CatalogVersion: catalogRecord.CatalogVersion, FormSchemaVersion: catalogRecord.FormSchemaVersion,
		IdempotencyKey: key,
	}
}

func TestCatalogDefaultClassificationDrivesCreation(t *testing.T) {
	f := newCTICatalogFixture(t)
	ctx := context.Background()
	published := f.publishCatalog(t, "VPN access", &f.ids[2])

	// 用户不提交 CTI：目录默认分类成为权威，工单只保存最深节点。
	result, err := f.app.Create(ctx, f.identity, f.command(published, "cti-1"))
	require.NoError(t, err)
	item := f.client.Ticket.GetX(ctx, result.WorkItemID)
	require.Equal(t, f.ids[2], item.CategoryID)

	// 客户端提交相同最深节点可兼容。
	same := f.command(published, "cti-2")
	same.CTI = &creation.CTIInput{CategoryID: &f.ids[0], TypeID: &f.ids[1], ItemID: &f.ids[2]}
	_, err = f.app.Create(ctx, f.identity, same)
	require.NoError(t, err)

	// 不同路径必须冲突，且不得产生半写入。
	conflict := f.command(published, "cti-3")
	conflict.CTI = &creation.CTIInput{CategoryID: &f.ids[0], TypeID: &f.ids[1], ItemID: &f.altLeaf}
	_, err = f.app.Create(ctx, f.identity, conflict)
	require.ErrorContains(t, err, "conflicts with the catalog default")
	require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
	require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))

	// 客户端不能自报目录默认值：该字段不参与 JSON 绑定。
	require.Contains(t, string(creationMustJSON(t, conflict)), "\"idempotencyKey\"")
	require.NotContains(t, string(creationMustJSON(t, conflict)), "catalogDefaultCategoryID")
}

func TestCatalogDefaultClassificationChangeInvalidatesConfirmation(t *testing.T) {
	f := newCTICatalogFixture(t)
	ctx := context.Background()
	published := f.publishCatalog(t, "VPN change", &f.ids[2])
	command := f.command(published, "cti-version-1")
	_, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)

	// 目录默认分类变化 → 旧确认版本必须被拒绝，且无新工单。
	updated, err := f.owner.Update(ctx, f.identity.TenantID, published.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: published.CatalogVersion, DefaultTicketCategoryID: &f.altLeaf})
	require.NoError(t, err)
	require.NotEqual(t, published.CatalogVersion, updated.CatalogVersion)

	stale := f.command(published, "cti-version-2")
	_, err = f.app.Create(ctx, f.identity, stale)
	require.ErrorIs(t, err, creation.ErrCatalogVersionConflict)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))

	// 使用新版本提交，落入新的最深节点。
	refreshed := f.command(updated, "cti-version-3")
	result, err := f.app.Create(ctx, f.identity, refreshed)
	require.NoError(t, err)
	item := f.client.Ticket.GetX(ctx, result.WorkItemID)
	require.Equal(t, f.altLeaf, item.CategoryID)
}

// 强制门禁只影响启用之后的判断：旧目录（无默认分类）在未启用时仍可申请，
// 启用后必须缺配失败，而不是被静默放过。
func TestCatalogEnforcementRequiresConfiguredDefault(t *testing.T) {
	f := newCTICatalogFixture(t)
	ctx := context.Background()
	legacy := f.publishCatalog(t, "Legacy catalog", nil)

	result, err := f.app.Create(ctx, f.identity, f.command(legacy, "cti-legacy-1"))
	require.NoError(t, err)
	item := f.client.Ticket.GetX(ctx, result.WorkItemID)
	require.Zero(t, item.CategoryID, "未启用门禁的旧目录保持既有行为：允许未分类")

	f.client.SystemConfig.Create().SetTenantID(f.identity.TenantID).
		SetKey(service.CTIGovernanceConfigKey).SetValue(`{"catalogEnforced":true}`).SetValueType("json").SaveX(ctx)
	_, err = f.app.Create(ctx, f.identity, f.command(legacy, "cti-legacy-2"))
	require.ErrorContains(t, err, "does not declare a complete default classification")
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))

	// 目录补配默认分类后即可继续申请。
	configured := f.publishCatalog(t, "Configured catalog", &f.ids[2])
	result, err = f.app.Create(ctx, f.identity, f.command(configured, "cti-legacy-3"))
	require.NoError(t, err)
	item = f.client.Ticket.GetX(ctx, result.WorkItemID)
	require.Equal(t, f.ids[2], item.CategoryID)
}

// 独立报障不受目录门禁影响：未分类仍然合法。
func TestOrdinaryReportStaysUnclassifiedWhenCatalogEnforced(t *testing.T) {
	f := newCTICatalogFixture(t)
	ctx := context.Background()
	f.client.SystemConfig.Create().SetTenantID(f.identity.TenantID).
		SetKey(service.CTIGovernanceConfigKey).SetValue(`{"catalogEnforced":true}`).SetValueType("json").SaveX(ctx)
	command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", Title: "VPN 无法连接", IdempotencyKey: "cti-report-1"}
	result, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	item := f.client.Ticket.GetX(ctx, result.WorkItemID)
	require.Zero(t, item.CategoryID)
}

func creationMustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
