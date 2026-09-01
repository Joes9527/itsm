package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBPMNTemplateService_ListTemplates_IncludesTicketUrgentFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "ticket_urgent_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "ticket_urgent_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急工单流程", found.Name)
	assert.Equal(t, "ticket", found.Category)
	assert.Equal(t, "ticket_urgent_flow.bpmn", found.Filename)
}

func TestBPMNTemplateService_ListTemplates_IncludesSSLVPNApprovalFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "sslvpn_approval_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "sslvpn_approval_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "SSL-VPN 申请与双级审批流", found.Name)
	assert.Equal(t, "service_request", found.Category)
	assert.Equal(t, "sslvpn_approval_flow.bpmn", found.Filename)
}

func TestBPMNTemplateService_TicketGeneralAndUrgentFlow_ApprovalNodeMarked(t *testing.T) {
	parser := NewBPMNParser()

	for _, file := range []string{"ticket_general_flow.bpmn", "ticket_urgent_flow.bpmn"} {
		data, err := bpmnTemplates.ReadFile("bpmn/" + file)
		require.NoError(t, err, file)

		defs, err := parser.ParseXML(data)
		require.NoError(t, err, file)
		require.Len(t, defs.Processes, 1, file)

		var approval *BPMNUserTask
		for _, ut := range defs.Processes[0].UserTasks {
			if ut.ID == "Activity_Approval" {
				approval = ut
				break
			}
		}
		require.NotNil(t, approval, "%s 应该有 Activity_Approval 节点", file)
		assert.Equal(t, "approval", approval.TaskPurpose, "%s 的 Activity_Approval 应该打上 taskPurpose=approval", file)
	}
}

func TestBPMNTemplateService_DeployAndStartTicketUrgentFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_urgent_flow_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Urgent Flow Tenant").
		SetCode("urgent-flow").
		SetDomain("urgent.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	templateSvc := NewBPMNTemplateService(client)
	err = templateSvc.DeployTemplateByName(ctx, "ticket_urgent_flow", tenant.ID)
	require.NoError(t, err, "ticket_urgent_flow 模板应该能正常部署")

	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok)

	runCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	runCtx = WithTrustedBPMNTenantContext(runCtx, tenant.ID)
	instance, err := engine.StartProcess(runCtx, "ticket_urgent_flow", "TICKET-URGENT-1", "", 0, map[string]interface{}{
		"requester_id": float64(1),
		"triggered_by": "system",
	})
	require.NoError(t, err, "ticket_urgent_flow 应该能成功启动流程实例，不再报'获取流程定义失败'")
	assert.NotNil(t, instance)
	assert.Equal(t, "ticket_urgent_flow", instance.ProcessDefinitionKey)
}

func TestBPMNTemplateService_ListTemplates_IncludesChangeEmergencyFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "change_emergency_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "change_emergency_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急变更流程", found.Name)
	assert.Equal(t, "change", found.Category)
	assert.Equal(t, "emergency", found.SubCategory)
}

func TestBPMNTemplateService_DeployAndStartChangeEmergencyFlow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:change_emergency_flow_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Change Emergency Flow Tenant").
		SetCode("change-emergency-flow").
		SetDomain("change-emergency.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	templateSvc := NewBPMNTemplateService(client)
	err = templateSvc.DeployTemplateByName(ctx, "change_emergency_flow", tenant.ID)
	require.NoError(t, err, "change_emergency_flow 模板应该能正常部署")

	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok)

	runCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	runCtx = WithTrustedBPMNTenantContext(runCtx, tenant.ID)
	instance, err := engine.StartProcess(runCtx, "change_emergency_flow", "CHANGE-EMERGENCY-1", "", 0, map[string]interface{}{
		"requester_id": float64(1),
		"triggered_by": "system",
	})
	require.NoError(t, err, "change_emergency_flow 应该能成功启动流程实例，不再报'流程定义不存在'")
	assert.NotNil(t, instance)
	assert.Equal(t, "change_emergency_flow", instance.ProcessDefinitionKey)
}

func TestBPMNTemplateService_ListTemplates_IncludesServiceRequestUrgentFlow(t *testing.T) {
	svc := &BPMNTemplateService{}
	templates, err := svc.listTemplates()
	require.NoError(t, err)

	var found *TemplateInfo
	for _, tmpl := range templates {
		if tmpl.ID == "service_request_urgent_flow" {
			found = tmpl
			break
		}
	}
	require.NotNil(t, found, "service_request_urgent_flow.bpmn 应该被发现并纳入模板清单")
	assert.Equal(t, "紧急服务请求流程", found.Name)
	assert.Equal(t, "service_request", found.Category)
}

// TestBPMNTemplateService_ChangeNormalFlow_ApprovalGatewayConditionCompiles 锁定 Fix 2：
// change_normal_flow.bpmn 里 Gateway_Approval 的出边条件表达式必须是这个引擎（expr-lang/expr）
// 能编译的裸表达式，不能带 "${...}" 包装——那种写法之前会导致 EvaluateCondition 编译失败
// （"unexpected token Bracket"），evaluateCondition 编译出错时按 false 处理，两条出边都算不满足，
// 网关直接报"没有符合条件的路径"硬错误。这条测试直接解析 .bpmn 文件、取出 Gateway_Approval
// 出边的真实条件表达式字符串，喂给 ExpressionEngine.EvaluateCondition 求值，如果把 XML 里的
// "${}" 包装加回去重新跑这条测试，应该会失败。
func TestBPMNTemplateService_ChangeNormalFlow_ApprovalGatewayConditionCompiles(t *testing.T) {
	parser := NewBPMNParser()

	data, err := bpmnTemplates.ReadFile("bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)

	defs, err := parser.ParseXML(data)
	require.NoError(t, err)
	require.Len(t, defs.Processes, 1)

	var approvalFlow, scheduleFlow *BPMNSequenceFlow
	for _, flow := range defs.Processes[0].SequenceFlows {
		if flow.SourceRef != "Gateway_Approval" {
			continue
		}
		switch flow.TargetRef {
		case "Activity_CABApproval":
			approvalFlow = flow
		case "Activity_Schedule":
			scheduleFlow = flow
		}
	}
	require.NotNil(t, approvalFlow, "Gateway_Approval 应该有一条指向 Activity_CABApproval 的出边（approval_required==true）")
	require.NotNil(t, scheduleFlow, "Gateway_Approval 应该有一条指向 Activity_Schedule 的出边（approval_required!=true）")
	require.NotNil(t, approvalFlow.ConditionExpression)
	require.NotNil(t, scheduleFlow.ConditionExpression)

	engine := NewExpressionEngine()

	approvalRequiredTrue := map[string]interface{}{
		"variables": map[string]interface{}{"approval_required": true},
	}
	result, err := engine.EvaluateCondition(approvalFlow.ConditionExpression.Expression, approvalRequiredTrue)
	require.NoError(t, err, "approval_required==true 分支的条件表达式应该能正常编译求值，而不是解析失败")
	assert.True(t, result, "approval_required=true 时，指向 Activity_CABApproval 的分支应该判定为满足")

	result, err = engine.EvaluateCondition(scheduleFlow.ConditionExpression.Expression, approvalRequiredTrue)
	require.NoError(t, err, "approval_required!=true 分支的条件表达式应该能正常编译求值，而不是解析失败")
	assert.False(t, result, "approval_required=true 时，指向 Activity_Schedule 的分支应该判定为不满足")
}

func TestBPMNTemplateService_ServiceRequestFlows_ApprovalNodeMarked(t *testing.T) {
	parser := NewBPMNParser()

	for _, file := range []string{"service_request_flow.bpmn", "service_request_urgent_flow.bpmn"} {
		data, err := bpmnTemplates.ReadFile("bpmn/" + file)
		require.NoError(t, err, file)

		defs, err := parser.ParseXML(data)
		require.NoError(t, err, file)
		require.Len(t, defs.Processes, 1, file)

		var approval *BPMNUserTask
		for _, ut := range defs.Processes[0].UserTasks {
			if ut.ID == "Activity_Approval" {
				approval = ut
				break
			}
		}
		require.NotNil(t, approval, "%s 应该有 Activity_Approval 节点", file)
		assert.Equal(t, "approval", approval.TaskPurpose, "%s 的 Activity_Approval 应该打上 taskPurpose=approval", file)
	}
}

// TestBPMNTemplateService_LoadAndDeployTemplates_DriftPublishesNewVersion 是模板漂移
// 同步的回归：存量租户的旧模板内容与嵌入模板不一致时，再次同步必须自动发布新版本
// （事务化降级 is_latest），旧版本停用、新版本激活，且同一 key 恰好一行
// is_latest=true + is_active=true。再同步一次应幂等（不产生第三个版本）。
func TestBPMNTemplateService_LoadAndDeployTemplates_DriftPublishesNewVersion(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:template_drift_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("drift-1").SetDomain("drift-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNTemplateService(client)

	// 首次部署：v1
	_, err = svc.LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	defs, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("release_approval_flow"), processdefinition.TenantID(tenant.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, defs, 1, "首次部署应只有一个版本")

	// 模拟线上旧模板（内容与嵌入版本不一致）
	_, err = client.ProcessDefinition.UpdateOne(defs[0]).
		SetBpmnXML([]byte("<definitions/>")).
		Save(ctx)
	require.NoError(t, err)

	// 再次同步：内容漂移 → 自动发布新版本
	_, err = svc.LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	defs, err = client.ProcessDefinition.Query().
		Where(processdefinition.Key("release_approval_flow"), processdefinition.TenantID(tenant.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, defs, 2, "漂移同步应发布第二个版本")

	latestCount := 0
	activeCount := 0
	for _, def := range defs {
		if def.IsLatest {
			latestCount++
			assert.True(t, def.IsActive, "最新版本必须是激活的")
		} else {
			assert.False(t, def.IsActive, "旧版本必须被停用")
		}
		if def.IsActive {
			activeCount++
		}
	}
	assert.Equal(t, 1, latestCount, "同一 key 应恰好一行 is_latest=true")
	assert.Equal(t, 1, activeCount, "同一 key 应恰好一行 is_active=true")

	// 新版本内容必须与嵌入模板一致
	latest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("release_approval_flow"), processdefinition.TenantID(tenant.ID),
			processdefinition.IsLatest(true)).
		Only(ctx)
	require.NoError(t, err)
	embedded, err := bpmnTemplates.ReadFile("bpmn/release_approval_flow.bpmn")
	require.NoError(t, err)
	assert.Equal(t, embedded, latest.BpmnXML, "新版本应写入嵌入模板的最新内容")

	// 幂等：第三次同步不产生新版本
	_, err = svc.LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)
	defs, err = client.ProcessDefinition.Query().
		Where(processdefinition.Key("release_approval_flow"), processdefinition.TenantID(tenant.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, defs, 2, "内容一致时同步应幂等，不再产生新版本")
}
