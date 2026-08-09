package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
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
	instance, err := engine.StartProcess(runCtx, "ticket_urgent_flow", "TICKET-URGENT-1", map[string]interface{}{
		"requester_id": float64(1),
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
	instance, err := engine.StartProcess(runCtx, "change_emergency_flow", "CHANGE-EMERGENCY-1", map[string]interface{}{
		"requester_id": float64(1),
	})
	require.NoError(t, err, "change_emergency_flow 应该能成功启动流程实例，不再报'流程定义不存在'")
	assert.NotNil(t, instance)
	assert.Equal(t, "change_emergency_flow", instance.ProcessDefinitionKey)
}
