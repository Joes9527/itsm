package service

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

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
