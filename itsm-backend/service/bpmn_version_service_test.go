package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBPMNVersionService_CreateVersion_DemotesOldLatest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_create_demotes?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-1").SetDomain("bvc-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)

	v1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow",
		Name:                 "测试流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenant.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)

	v2, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow",
		Name:                 "测试流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenant.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)
	require.NotEqual(t, v1.Version, v2.Version)

	latestCount, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("test_flow"),
			processdefinition.TenantID(tenant.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, latestCount, "同一 key 任何时刻只应该有一行 is_latest=true")

	stillLatest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("test_flow"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, v2.Version, stillLatest.Version, "最新的应该是最后一次创建的版本")
}

func TestBPMNVersionService_ActivateVersion_DoesNotBreakIsLatest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_activate_islatest?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-2").SetDomain("bvc-2.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)
	v1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow2", Name: "流程2", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)
	v2, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow2", Name: "流程2", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)

	// 激活回第一个版本，is_latest 不应该跟着 is_active 一起被搞乱——
	// 激活的是旧版本，但"最新版本"这个概念不应该因为激活操作而改变。
	require.NoError(t, svc.ActivateVersion(ctx, "test_flow2", v1.Version, tenant.ID))

	latestCount, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("test_flow2"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, latestCount, "激活旧版本后仍应只有一行 is_latest=true")

	stillLatest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("test_flow2"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, v2.Version, stillLatest.Version, "激活操作不应改变 is_latest 的指向，应保持为 v2（最新创建的版本）")
}

func TestBPMNVersionService_GetLatestProcessDefinition_ReturnsMostRecentlyCreated(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_get_latest?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-3").SetDomain("bvc-3.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	versionSvc := NewBPMNVersionService(client, logger)
	_, err = versionSvc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow3", Name: "流程3", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)
	v2, err := versionSvc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow3", Name: "流程3", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)

	defSvc := bpmnProcessDefinitionService{client: client}
	latest, err := defSvc.GetLatestProcessDefinition(newTenantCtx(ctx, tenant.ID), "test_flow3")
	require.NoError(t, err)
	assert.Equal(t, v2.Version, latest.Version)
}

func TestBPMNVersionService_CreateVersion_TenantIsolation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_tenant_isolation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	// 创建两个不同的租户
	tenantA, err := client.Tenant.Create().
		SetName("Tenant A").SetCode("tenant-a").SetDomain("tenant-a.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	tenantB, err := client.Tenant.Create().
		SetName("Tenant B").SetCode("tenant-b").SetDomain("tenant-b.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)

	// 在两个租户下为同一个 ProcessDefinitionKey 创建版本
	// 租户A的第一个版本
	aV1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "shared_key",
		Name:                 "共享流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenantA.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)

	// 租户B的第一个版本（同一个 key）
	bV1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "shared_key",
		Name:                 "共享流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenantB.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)

	// 现在每个租户都有一行 is_latest=true 的行
	tenantALatestCount, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantA.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tenantALatestCount, "租户A应该有一行 is_latest=true")

	tenantBLatestCount, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantB.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tenantBLatestCount, "租户B应该有一行 is_latest=true")

	// 只为租户A创建第二个版本，这应该只会降级租户A的旧版本，不应该影响租户B
	aV2, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "shared_key",
		Name:                 "共享流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenantA.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)
	require.NotEqual(t, aV1.Version, aV2.Version)

	// 验证租户A：只有一行 is_latest=true，且是新版本
	tenantALatestCountAfter, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantA.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tenantALatestCountAfter, "租户A降级后应仍只有一行 is_latest=true")

	tenantAStillLatest, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantA.ID),
			processdefinition.IsLatest(true),
		).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, aV2.Version, tenantAStillLatest.Version, "租户A的最新版本应为新创建的版本")

	// 验证租户B：仍有一行 is_latest=true，且未改变（仍是 bV1）
	tenantBLatestCountAfter, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantB.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tenantBLatestCountAfter, "租户B应仍有一行 is_latest=true（降级操作应仅限租户A）")

	tenantBStillLatest, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("shared_key"),
			processdefinition.TenantID(tenantB.ID),
			processdefinition.IsLatest(true),
		).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, bV1.Version, tenantBStillLatest.Version, "租户B的最新版本应保持不变（为 bV1），证明降级操作已正确隔离租户")
}

func newTenantCtx(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
}

// TestBPMNVersionService_CreateVersion_ConvergesMultiplePreExistingLatest 复现原始审计
// 发现的生产数据形态：同一个 (tenant, key) 已经有多行 is_latest=true（新行靠 schema 默认值
// 天生是 true，历史上从来没人主动降级过旧行）。旧实现用 Query().First() + UpdateOne 只降级
// 一行，跑完 CreateVersion 还剩 N-1 行旧的 + 1 行新的，根本没收敛。
func TestBPMNVersionService_CreateVersion_ConvergesMultiplePreExistingLatest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_converge_multi?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-multi").SetDomain("bvc-multi.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-converge-multi").
		SetDeploymentName("Deployment converge-multi").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("seed").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 种下 3 行同时 is_latest=true 的脏数据
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		_, err := client.ProcessDefinition.Create().
			SetKey("dirty_flow").
			SetName("脏数据流程").
			SetVersion(v).
			SetBpmnXML([]byte("<x/>")).
			SetIsLatest(true).
			SetIsActive(false).
			SetDeploymentID(deployment.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	preCount, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("dirty_flow"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, preCount, "前置条件：确实存在多行 is_latest=true 的脏数据")

	svc := NewBPMNVersionService(client, logger)
	newVersion, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "dirty_flow",
		Name:                 "脏数据流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenant.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)

	latest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("dirty_flow"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 1, "CreateVersion 必须把所有旧的 is_latest=true 一次性降级，收敛到恰好 1 行")
	assert.Equal(t, newVersion.Version, latest[0].Version)
}

// TestBPMNVersionService_CreateVersion_RollsBackDemoteOnCreateFailure 证明降级和创建
// 处在同一个事务里：中途 Create 失败时，旧的 is_latest 必须被回滚回来。否则该 key 会变成
// 0 行 is_latest=true，GetLatestProcessDefinition 直接查不到，StartProcess 全线挂掉——
// 比原来的"N 行并列最新"更糟。
//
// 故障注入手段：ProcessDeployment.deployment_id 在 schema 里是 Unique，
// 预先占用下一次 CreateVersion 会生成的那个 deployment_id，即可让 Create 撞唯一约束失败。
func TestBPMNVersionService_CreateVersion_RollsBackDemoteOnCreateFailure(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_rollback?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-rb").SetDomain("bvc-rb.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)
	v1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "rollback_flow", Name: "回滚流程", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)

	// 抢占下一版本将要使用的 deployment_id，制造唯一约束冲突
	nextDeploymentID := fmt.Sprintf("tenant-%d-%s-v%s", tenant.ID, "rollback_flow", incrementSemver(v1.Version))
	_, err = client.ProcessDeployment.Create().
		SetDeploymentID(nextDeploymentID).
		SetDeploymentName("占位，制造唯一约束冲突").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("seed").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "rollback_flow", Name: "回滚流程", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.Error(t, err, "部署记录唯一约束冲突，CreateVersion 应该失败")

	latest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("rollback_flow"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 1, "创建失败必须连同降级一起回滚，绝不能留下 0 行 is_latest=true")
	assert.Equal(t, v1.Version, latest[0].Version, "回滚后仍应是原来的最新版本")
}
