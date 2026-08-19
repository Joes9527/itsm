package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestEvaluateCondition_FailureReturnsFalse 测试条件评估失败时返回 false
func TestEvaluateCondition_FailureReturnsFalse(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		client:     nil,
		logger:     logger,
		parser:     NewBPMNParser(),
		exprEngine: NewExpressionEngine(),
	}

	variables := map[string]interface{}{
		"status": "open",
	}

	// 使用一个无效的表达式，评估应该失败并返回 false
	result := engine.evaluateCondition(&BPMNSequenceFlow{
		ConditionExpression: &BPMNConditionExpression{
			Expression: "invalid {{{{ expression",
		},
	}, variables)

	if result {
		t.Error("无效表达式评估应返回 false，但返回了 true")
	}
}

// TestEvaluateCondition_NoConditionReturnsTrue 测试无条件表达式时返回 true
func TestEvaluateCondition_NoConditionReturnsTrue(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		client:     nil,
		logger:     logger,
		parser:     NewBPMNParser(),
		exprEngine: NewExpressionEngine(),
	}

	variables := map[string]interface{}{
		"status": "open",
	}

	// 无条件表达式，应该默认通过
	result := engine.evaluateCondition(&BPMNSequenceFlow{
		ConditionExpression: nil,
	}, variables)

	if !result {
		t.Error("无条件表达式应返回 true")
	}
}

// TestEvaluateCondition_EmptyExpressionReturnsTrue 测试空条件表达式时返回 true
func TestEvaluateCondition_EmptyExpressionReturnsTrue(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		client:     nil,
		logger:     logger,
		parser:     NewBPMNParser(),
		exprEngine: NewExpressionEngine(),
	}

	variables := map[string]interface{}{
		"status": "open",
	}

	// 空条件表达式
	result := engine.evaluateCondition(&BPMNSequenceFlow{
		ConditionExpression: &BPMNConditionExpression{
			Expression: "",
		},
	}, variables)

	if !result {
		t.Error("空条件表达式应返回 true")
	}
}

// TestEvaluateCondition_ValidExpression 测试有效表达式正常评估
func TestEvaluateCondition_ValidExpression(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		client:     nil,
		logger:     logger,
		parser:     NewBPMNParser(),
		exprEngine: NewExpressionEngine(),
	}

	variables := map[string]interface{}{
		"priority": 1,
	}

	// 有效表达式：priority == 1
	result := engine.evaluateCondition(&BPMNSequenceFlow{
		ConditionExpression: &BPMNConditionExpression{
			Expression: "priority == 1",
		},
	}, variables)

	if !result {
		t.Error("priority == 1 评估应返回 true")
	}
}

func TestResolveRoleCandidates_MatchesPrimaryAndAdditionalRole(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:resolve_role_candidates?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("Role Candidates Tenant").
		SetCode("role-candidates").
		SetDomain("role-candidates.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	itDirectorRole, err := client.Role.Create().
		SetName("IT总监").
		SetCode("it_director").
		SetDescription("IT部门总监").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// primaryUser: 主角色字段直接就是 it_director（老路径，应该继续命中）
	primaryUser, err := client.User.Create().
		SetUsername("primary_it_director").
		SetEmail("primary@role-candidates.test").
		SetName("Primary IT Director").
		SetPasswordHash("hash").
		SetRole("it_director").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// secondaryUser: 主角色是 dept_manager，但通过 user_roles 边额外挂了 it_director——
	// 这是本次新加的路径，应该也能被 resolveRoleCandidates("it_director") 命中。
	secondaryUser, err := client.User.Create().
		SetUsername("secondary_it_director").
		SetEmail("secondary@role-candidates.test").
		SetName("Secondary IT Director").
		SetPasswordHash("hash").
		SetRole("dept_manager").
		SetActive(true).
		SetTenantID(tenant.ID).
		AddRoleIDs(itDirectorRole.ID).
		Save(ctx)
	require.NoError(t, err)

	// unrelatedUser: 既不是主角色也没有附加角色，不应该出现在结果里。
	_, err = client.User.Create().
		SetUsername("unrelated_user").
		SetEmail("unrelated@role-candidates.test").
		SetName("Unrelated User").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)
	names, err := engine.resolveRoleCandidates(ctx, tenant.ID, "it_director")
	require.NoError(t, err)

	assert.Len(t, names, 2)
	assert.Contains(t, names, primaryUser.Username)
	assert.Contains(t, names, secondaryUser.Username)
}
