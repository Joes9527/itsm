package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestStartProcessByDefinitionIDUsesFrozenDefinitionAndReplays(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:frozen_process_start?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant, err := client.Tenant.Create().SetName("Frozen process tenant").SetCode("frozen-process").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().SetDeploymentID("frozen-deployment").SetDeploymentName("Frozen").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	oldDefinition, err := client.ProcessDefinition.Create().SetKey("frozen-flow").SetName("Frozen v1").SetVersion("1").
		SetBpmnXML([]byte(minimalStartEndBPMN("frozen-v1"))).SetIsActive(true).SetIsLatest(false).
		SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessDefinition.Create().SetKey("frozen-flow").SetName("Frozen v2").SetVersion("2").
		SetBpmnXML([]byte(minimalStartEndBPMN("frozen-v2"))).SetIsActive(true).SetIsLatest(true).
		SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()).(*CustomProcessEngine)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	first, err := engine.StartProcessByDefinitionID(tenantCtx, oldDefinition.ID, "workflow-start:501:1", "work_item", 501, map[string]any{"workItemId": 501})
	require.NoError(t, err)
	require.Equal(t, oldDefinition.ID, first.ProcessDefinitionID)

	replayed, err := engine.StartProcessByDefinitionID(tenantCtx, oldDefinition.ID, "workflow-start:501:1", "work_item", 501, map[string]any{"workItemId": 501})
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.ID)
	count, err := client.ProcessInstance.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func minimalStartEndBPMN(processID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="start"><outgoing>flow-1</outgoing></startEvent>
    <endEvent id="end"><incoming>flow-1</incoming></endEvent>
    <sequenceFlow id="flow-1" sourceRef="start" targetRef="end" />
  </process>
</definitions>`
}

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

func TestCreateUserTask_AssigneeGmChain_ResolvesSubmitterOwnChain(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:gm_chain_task?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("GM Chain Tenant").
		SetCode("gm-chain").
		SetDomain("gm-chain.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	gm, err := client.User.Create().
		SetUsername("branch_gm").
		SetEmail("gm@gm-chain.test").
		SetName("Branch GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetJobTitle("综合物流总经理").
		Save(ctx)
	require.NoError(t, err)

	submitter, err := client.User.Create().
		SetUsername("gm_chain_submitter").
		SetEmail("submitter@gm-chain.test").
		SetName("Submitter").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetManagerID(gm.ID).
		Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)

	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	assignee := engine.resolveGmChainAssignee(ctx, instance, submitter)
	assert.Equal(t, strconv.Itoa(gm.ID), assignee)
}

func TestCreateUserTask_AssigneeGmChain_SelfApprovalFallsBackEmpty(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:gm_chain_self?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("GM Chain Self Tenant").
		SetCode("gm-chain-self").
		SetDomain("gm-chain-self.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	gmSubmitter, err := client.User.Create().
		SetUsername("self_gm").
		SetEmail("self-gm@gm-chain-self.test").
		SetName("Self GM").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenant.ID).
		SetJobTitle("综合物流总经理").
		Save(ctx)
	require.NoError(t, err)

	// 提交人自己没有更上级的总经理（manager_id=0），resolveGmChainAssignee 应该返回空串，
	// 而不是报错或者把提交人自己当成审批人。
	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)
	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	assignee := engine.resolveGmChainAssignee(ctx, instance, gmSubmitter)
	assert.Equal(t, "", assignee)
}
