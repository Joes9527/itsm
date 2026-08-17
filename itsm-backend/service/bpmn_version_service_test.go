package service

import (
	"context"
	"testing"

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
	_, err = svc.CreateVersion(ctx, &CreateVersionRequest{
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
	assert.Equal(t, 1, latestCount)
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

func newTenantCtx(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
}
