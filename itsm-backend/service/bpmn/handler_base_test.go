package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/release"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestGetTenantIDFromVars 覆盖 Finding 3 要修的安全属性：
//  1. 认证上下文注入的租户 ID 是唯一可信来源，优先级高于流程变量；
//  2. 两者都没有时 fail closed（返回 0），绝不回退到硬编码的租户 1。
//
// 参与者变量是不可信输入，tenant_id 不能成为 handler 的租户权威来源；
// 默认到 1 会让越权写入正好落在租户 1 的业务数据上。
func TestGetTenantIDFromVars(t *testing.T) {
	cases := []struct {
		name      string
		ctx       context.Context
		variables map[string]interface{}
		want      int
	}{
		{
			name:      "ctx 与 variables 都缺失时 fail closed 返回 0",
			ctx:       context.Background(),
			variables: map[string]interface{}{},
			want:      0,
		},
		{
			name:      "variables 为 nil 时同样 fail closed",
			ctx:       context.Background(),
			variables: nil,
			want:      0,
		},
		{
			name:      "只有 ctx 时取 ctx",
			ctx:       context.WithValue(context.Background(), BPMNTenantIDContextKey, 7),
			variables: map[string]interface{}{},
			want:      7,
		},
		{
			name:      "只有 variables 时拒绝不可信租户",
			ctx:       context.Background(),
			variables: map[string]interface{}{"tenant_id": 5},
			want:      0,
		},
		{
			name:      "variables 里的 JSON 数字也不授予租户范围",
			ctx:       context.Background(),
			variables: map[string]interface{}{"tenant_id": float64(5)},
			want:      0,
		},
		{
			name:      "两者冲突时以 ctx（认证态）为准，忽略可伪造的 variables",
			ctx:       context.WithValue(context.Background(), BPMNTenantIDContextKey, 5),
			variables: map[string]interface{}{"tenant_id": 1},
			want:      5,
		},
		{
			name:      "ctx 里的非法租户不回退 variables",
			ctx:       context.WithValue(context.Background(), BPMNTenantIDContextKey, 0),
			variables: map[string]interface{}{"tenant_id": 3},
			want:      0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetTenantIDFromVars(tc.ctx, tc.variables))
		})
	}
}

// TestReleaseHandler_NoTenantContext_FailsClosed 把上面的单元属性钉到一个真实写入路径上：
// 没有任何可信租户来源时，release_task 的状态变更不能"默默按租户 1 执行"。
func TestReleaseHandler_NoTenantContext_FailsClosed(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:handler_base_failclosed?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("hb-1").SetDomain("hb-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tenant.ID, "本用例依赖第一个租户的 ID 恰好是旧代码硬编码的默认值 1")

	creator, err := client.User.Create().
		SetUsername("creator-hb").SetEmail("creator-hb@test.com").SetPasswordHash("x").
		SetName("创建人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	rel, err := client.Release.Create().
		SetReleaseNumber("REL-HB-1").SetTitle("失败关闭测试发布").SetStatus("draft").
		SetCreatedBy(creator.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewReleaseServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())

	// 关键：ctx 无 BPMNTenantIDContextKey，variables 无 tenant_id
	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "schedule",
		"business_id": rel.ID,
	})
	assert.Error(t, err, "没有可信租户来源时必须 fail closed，不能回落到硬编码租户 1")

	after, err := client.Release.Query().Where(release.ID(rel.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "draft", after.Status, "fail closed 时不得写入任何状态")
}

func TestStatefulBuiltInHandlersRejectUnknownActions(t *testing.T) {
	handlers := []ServiceTaskHandlerInterface{
		NewTicketServiceTaskHandler(nil, nil),
		NewChangeServiceTaskHandler(nil, nil),
		NewIncidentServiceTaskHandler(nil, nil),
		NewGenericServiceTaskHandler(nil, nil),
		NewServiceRequestServiceTaskHandler(nil, nil),
		NewReleaseServiceTaskHandler(nil, nil),
	}

	for _, handler := range handlers {
		t.Run(handler.GetHandlerID(), func(t *testing.T) {
			_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
				"action":      "unsupported_action",
				"business_id": 1,
			})
			require.Error(t, err)
		})
	}
}
