package service

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/systemconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupSystemConfigTest(t *testing.T) (*ent.Client, *SystemConfigService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewSystemConfigService(client, logger)
	ctx := context.Background()
	return client, svc, ctx
}

func createSystemConfigTestTenant(ctx context.Context, client *ent.Client, suffix string) (*ent.Tenant, error) {
	return client.Tenant.Create().
		SetName("Test Tenant " + suffix).
		SetCode("test" + suffix).
		SetDomain("test" + suffix + ".com").
		SetStatus("active").
		Save(ctx)
}

// TestSystemConfigService_UpdateSystemConfig_ProtectedKeyRejected 验证受保护的
// legacyApprovalWriteLocked key 不能通过通用的 UpdateSystemConfig 接口修改。
func TestSystemConfigService_UpdateSystemConfig_ProtectedKeyRejected(t *testing.T) {
	client, svc, ctx := setupSystemConfigTest(t)
	defer client.Close()

	tenant, err := createSystemConfigTestTenant(ctx, client, "protected-update")
	require.NoError(t, err)

	cfg, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenant.ID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	updateReq := &dto.UpdateSystemConfigRequest{Value: "false"}
	updated, err := svc.UpdateSystemConfig(ctx, cfg.ID, updateReq, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, updated)
	assert.Contains(t, err.Error(), "受保护")

	// 确认值真的没被改动。
	stillLocked, err := client.SystemConfig.Get(ctx, cfg.ID)
	require.NoError(t, err)
	assert.Equal(t, "true", stillLocked.Value, "受保护 key 的值不应该被通用更新接口改动")
}

// TestSystemConfigService_UpdateSystemConfig_NormalKeyAllowed 验证普通 key 依然可以通过
// UpdateSystemConfig 正常修改（回归校验，避免保护逻辑误伤非受保护的 key）。
func TestSystemConfigService_UpdateSystemConfig_NormalKeyAllowed(t *testing.T) {
	client, svc, ctx := setupSystemConfigTest(t)
	defer client.Close()

	tenant, err := createSystemConfigTestTenant(ctx, client, "normal-update")
	require.NoError(t, err)

	cfg, err := client.SystemConfig.Create().
		SetKey("systemName").
		SetValue("旧名字").
		SetValueType("string").
		SetCategory("general").
		SetTenantID(tenant.ID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	updateReq := &dto.UpdateSystemConfigRequest{Value: "新名字"}
	updated, err := svc.UpdateSystemConfig(ctx, cfg.ID, updateReq, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "新名字", updated.Value)
}

// TestSystemConfigService_BatchUpdateSystemConfigs_ProtectedKeySkipped 验证批量更新接口
// 遇到受保护 key 时跳过它，但仍然正常处理其它普通 key（不中断整个批次）。
func TestSystemConfigService_BatchUpdateSystemConfigs_ProtectedKeySkipped(t *testing.T) {
	client, svc, ctx := setupSystemConfigTest(t)
	defer client.Close()

	tenant, err := createSystemConfigTestTenant(ctx, client, "protected-batch")
	require.NoError(t, err)

	lockCfg, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenant.ID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	batch := []dto.UpdateSystemConfigRequest{
		{Key: "legacyApprovalWriteLocked", Value: "false", ValueType: "boolean"},
		{Key: "systemName", Value: "批量更新后的名字", ValueType: "string"},
	}

	results, err := svc.BatchUpdateSystemConfigs(ctx, batch, tenant.ID)
	require.NoError(t, err)

	// 只有普通 key 应该出现在结果里。
	require.Len(t, results, 1)
	assert.Equal(t, "systemName", results[0].Key)
	assert.Equal(t, "批量更新后的名字", results[0].Value)

	// 受保护 key 对应的行没有被改动。
	stillLocked, err := client.SystemConfig.Get(ctx, lockCfg.ID)
	require.NoError(t, err)
	assert.Equal(t, "true", stillLocked.Value, "受保护 key 不应该被批量更新接口改动")

	// 普通 key 确实持久化到了数据库。
	persisted, err := client.SystemConfig.Query().
		Where(systemconfig.KeyEQ("systemName"), systemconfig.TenantIDEQ(tenant.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "批量更新后的名字", persisted.Value)
}

// TestSystemConfigService_BatchUpdateSystemConfigs_ProtectedKeyNotCreated 验证当受保护 key
// 在数据库里还不存在时，批量更新接口也不会顺手把它创建出来。
func TestSystemConfigService_BatchUpdateSystemConfigs_ProtectedKeyNotCreated(t *testing.T) {
	client, svc, ctx := setupSystemConfigTest(t)
	defer client.Close()

	tenant, err := createSystemConfigTestTenant(ctx, client, "protected-batch-create")
	require.NoError(t, err)

	batch := []dto.UpdateSystemConfigRequest{
		{Key: "legacyApprovalWriteLocked", Value: "true", ValueType: "boolean"},
	}

	results, err := svc.BatchUpdateSystemConfigs(ctx, batch, tenant.ID)
	require.NoError(t, err)
	assert.Empty(t, results)

	exists, err := client.SystemConfig.Query().
		Where(systemconfig.KeyEQ("legacyApprovalWriteLocked"), systemconfig.TenantIDEQ(tenant.ID)).
		Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists, "受保护 key 不应该被批量更新接口意外创建")
}
