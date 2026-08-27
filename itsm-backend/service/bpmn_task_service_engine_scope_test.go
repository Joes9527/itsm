package service

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 本文件锁定 Wave 1 全分支评审发现的 Critical：CallbackRegistry 的依赖注入到不了生产路径。
//
// 症状（修复前）：bootstrap 在 internal/bootstrap/app.go 里只往那一个
// processEngine 的 CallbackRegistry 注入了 TicketService/IncidentService，但
// PUT /workflow/tasks/:id/complete 走的是
// processEngine.TaskService().CompleteTaskByID(...)，而 bpmnTaskService 的
// CompleteTask/CompleteTaskByID 每次都 NewCustomProcessEngine(...) 现造一个引擎——
// 新引擎带的是一个全新的、从没被注入过的 CallbackRegistry。于是
// dispatchUserTaskCallback 找到的 TicketServiceTaskHandler 的 statusService 是 nil，
// 回调返回 "ticket status service 未注入"，而该错误在 dispatchUserTaskCallback 里
// 只被 Warnw 吞掉（设计如此：任务已完成、流程已推进，不能回滚），所以工单状态
// 在生产环境里静默地永远不会被 BPMN 改动，且没有任何请求会失败。
//
// 因此这些测试必须断言"数据库里的业务状态真的变了"，只断言 CompleteTask 不返回
// error 是抓不到这个 bug 的。

// engineScopeFixtureProcessKey / engineScopeFixtureBPMN 是本文件专用的流程夹具。
//
// 为什么不用内置的 ticket_general_flow.bpmn：它（以及 ticket_urgent_flow /
// ticket_assignment_flow）把网关条件写成
//
//	<bpmn:conditionExpression ...><bpmn:body>${variables['x'] == true}</bpmn:body></...>
//
// 而 BPMNConditionExpression.Expression 的 tag 是 `xml:",chardata"`——它取不到子元素
// <bpmn:body> 的正文，解析出来的表达式只有空白字符；即使取到了，`${...}` 这层包装
// 在 expr-lang 里也编译不过。两条叠加导致这三个模板的第一个排他网关
// （Activity_Assign → Gateway_Approval）永远无路可走，报"没有符合条件的路径"。
// 这是先于本分支就存在的产品缺陷（详见本次评审修复报告），修它会改变多个线上模板的
// 路由行为，不属于本次"评审修复波"的范围，因此这里不去依赖那些坏掉的模板。
//
// 能正常工作的内置模板（change_normal_flow / service_request_flow 等）用的是
// <![CDATA[variables['x'] == true]]> 这种写法，但它们没有挂 ticket_task 的节点，
// 覆盖不到本文件要锁定的 TicketService 注入链路。
//
// 所以这里部署一个只为本回归存在的最小流程：没有网关，两个 UserTask，
// 第二个节点带 service_task_type=ticket_task / action=update_status 的 metaData——
// 它和生产模板里 Activity_Handle 的声明完全一致，走的也是同一条分发链路
// （TaskService().CompleteTask → engine.CompleteTask → dispatchUserTaskCallback →
// findHandlerByTaskType → 被注入的 TicketServiceTaskHandler → TicketService）。
const engineScopeFixtureProcessKey = "ticket_engine_scope_fixture_flow"

const engineScopeFixtureBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="ticket_engine_scope_fixture_flow" name="任务服务引擎作用域回归夹具" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="工单创建">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>

    <!-- 无 metaData 的普通人工节点：只用来证明"中间节点也经由 TaskService 完成"，
         本身不产生工单副作用，避免干扰后面对状态的断言。 -->
    <bpmn:userTask id="Activity_Assign" name="任务分配">
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>

    <!-- 与 ticket_general_flow.bpmn 的 Activity_Handle 同构：UserTask + ticket_task/update_status -->
    <bpmn:userTask id="Activity_Handle" name="工单处理">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">update_status</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:userTask>

    <bpmn:endEvent id="EndEvent_1" name="工单关闭">
      <bpmn:incoming>Flow_3</bpmn:incoming>
    </bpmn:endEvent>

    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Assign" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_Assign" targetRef="Activity_Handle" />
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Activity_Handle" targetRef="EndEvent_1" />
  </bpmn:process>
</bpmn:definitions>`

// setupTicketCallbackEngine 构造一个和生产 bootstrap 同构的引擎：
// 一个 CustomProcessEngine 实例 + 往它的 CallbackRegistry 注入 TicketService，
// 之后所有任务完成都必须走这同一个实例。
func setupTicketCallbackEngine(t *testing.T) (*ent.Client, *CustomProcessEngine, context.Context, int) {
	t.Helper()

	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("TaskService Engine Scope Tenant").
		SetCode("taskservice-engine-scope").
		SetDomain("taskservice-engine-scope.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)

	// 用引擎自身的流程定义服务部署夹具（和管理端 /bpmn/definitions 走同一条路径），
	// 而不是手写 ent 插入，保证 definition/deployment 行的形状与生产一致。
	_, err = engine.ProcessDefinitionService().CreateProcessDefinition(ctx, &CreateProcessDefinitionRequest{
		Key:      engineScopeFixtureProcessKey,
		Name:     "任务服务引擎作用域回归夹具",
		Category: "ticket",
		BPMNXML:  engineScopeFixtureBPMN,
		TenantID: tenant.ID,
	})
	require.NoError(t, err)

	// 与 internal/bootstrap/app.go 的装配完全一致：只往这一个引擎的 registry 注入。
	handler, ok := engine.CallbackRegistry().GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler)
	require.True(t, ok, "ticket_service_handler 必须已注册")
	handler.SetTicketService(NewTicketServiceForTest(client, logger))

	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	return client, engine, ctx, tenant.ID
}

// createRequester 建一个租户内的真实用户：tickets.requester_id 是外键，直接写 1 会撞
// FOREIGN KEY 约束。
func createRequester(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, username string) int {
	t.Helper()
	u, err := client.User.Create().
		SetUsername(username).
		SetEmail(username + "@example.com").
		SetPasswordHash("x").
		SetName(username).
		SetTenantID(tenantID).
		SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	return u.ID
}

// driveTicketFlowToHandleTask 启动夹具流程并把它推进到 Activity_Handle
//（action=update_status，正是委托给注入的 TicketService 的那个节点），返回该待办任务。
// 中间的 Activity_Assign 一并通过 TaskService 完成，保证整条链路都是"生产路径"
// 而不是直接调 engine.CompleteTask。
func driveTicketFlowToHandleTask(t *testing.T, client *ent.Client, engine *CustomProcessEngine, ctx context.Context, ticketID int) (*ent.ProcessInstance, *ent.ProcessTask) {
	t.Helper()

	instance, err := engine.StartProcess(ctx, engineScopeFixtureProcessKey, fmt.Sprintf("ticket:%d", ticketID),
		"ticket", ticketID, map[string]interface{}{
			"business_id": ticketID,
		})
	require.NoError(t, err)

	assign := findTaskByDefinitionKey(t, client, ctx, instance.ID, "Activity_Assign")
	require.NoError(t, engine.TaskService().CompleteTask(ctx, assign.TaskID, map[string]interface{}{
		"business_id": ticketID,
	}))

	handle := findTaskByDefinitionKey(t, client, ctx, instance.ID, "Activity_Handle")
	return instance, handle
}

// TestTaskServiceCompleteTask_DispatchesToInjectedTicketService 是 Critical 的主回归：
// 沿生产链路 processEngine.TaskService().CompleteTask(...) 完成一个
// ticket_task/update_status 的 UserTask，工单状态必须在数据库里真的改变。
func TestTaskServiceCompleteTask_DispatchesToInjectedTicketService(t *testing.T) {
	client, engine, ctx, tenantID := setupTicketCallbackEngine(t)
	requesterID := createRequester(t, client, ctx, tenantID, "ts-scope-1")

	tkt, err := client.Ticket.Create().
		SetTitle("TaskService 注入回归").
		SetTicketNumber("T-TS-SCOPE-1").
		SetStatus("open").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	_, handle := driveTicketFlowToHandleTask(t, client, engine, ctx, tkt.ID)

	require.NoError(t, engine.TaskService().CompleteTask(ctx, handle.TaskID, map[string]interface{}{
		"business_id": tkt.ID,
		"new_status":  "in_progress",
	}))

	updated, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status,
		"通过 TaskService().CompleteTask 完成 update_status 节点必须真的更新工单状态——"+
			"若 bpmnTaskService 又开始自己 NewCustomProcessEngine，注入过的 CallbackRegistry 就丢了，"+
			"回调会被 dispatchUserTaskCallback 静默 Warn 掉，这里会看到状态没变")
}

// TestTaskServiceCompleteTaskByID_DispatchesToInjectedTicketService 覆盖
// PUT /workflow/tasks/:id/complete 实际调用的那个方法（按数据库自增 ID 完成）。
// controller/bpmn_workflow_controller.go 的 SubmitTaskDecision 对数字任务 ID
// 走的就是 CompleteTaskByID。
func TestTaskServiceCompleteTaskByID_DispatchesToInjectedTicketService(t *testing.T) {
	client, engine, ctx, tenantID := setupTicketCallbackEngine(t)
	requesterID := createRequester(t, client, ctx, tenantID, "ts-scope-2")

	tkt, err := client.Ticket.Create().
		SetTitle("TaskService ByID 注入回归").
		SetTicketNumber("T-TS-SCOPE-2").
		SetStatus("open").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	_, handle := driveTicketFlowToHandleTask(t, client, engine, ctx, tkt.ID)

	require.NoError(t, engine.TaskService().CompleteTaskByID(ctx, handle.ID, map[string]interface{}{
		"business_id": tkt.ID,
		"new_status":  "in_progress",
	}))

	updated, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status,
		"CompleteTaskByID（HTTP 完成任务接口的实际入口）同样必须复用注入过的引擎")
}

// TestBPMNApprovalBridge_DispatchesToInjectedTicketService 覆盖 Critical 的另一半：
// BPMNApprovalBridge 此前也在三个方法里各自 NewCustomProcessEngine，业务侧审批/阶段
// 桥接完成 UserTask 时同样拿不到注入过的 registry。
func TestBPMNApprovalBridge_DispatchesToInjectedTicketService(t *testing.T) {
	client, engine, ctx, tenantID := setupTicketCallbackEngine(t)
	requesterID := createRequester(t, client, ctx, tenantID, "ts-scope-3")

	tkt, err := client.Ticket.Create().
		SetTitle("审批桥接注入回归").
		SetTicketNumber("T-TS-SCOPE-3").
		SetStatus("open").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	driveTicketFlowToHandleTask(t, client, engine, ctx, tkt.ID)

	// 与 bootstrap 一致：桥接拿到的是同一个已装配的引擎。
	bridge := NewBPMNApprovalBridge(client, zap.NewNop().Sugar(), engine)
	handled, err := bridge.CompleteBusinessStageTask(context.Background(), tenantID, 0, "ticket", tkt.ID,
		"Activity_Handle", map[string]interface{}{"new_status": "in_progress"})
	require.NoError(t, err)
	require.True(t, handled, "存在待办的 Activity_Handle 任务时桥接必须接管")

	updated, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status,
		"审批/阶段桥接完成 UserTask 时必须复用注入过的引擎，否则业务副作用被静默丢弃")
}

// TestTaskService_ReusesEngineInstanceAndRegistry 是结构性回归：TaskService() 必须
// 返回引擎自身持有的那个任务服务实例，并且它推进流程时用的就是这个引擎的
// CallbackRegistry。这条断言同时覆盖 Incident 侧的注入——
// IncidentServiceTaskHandler 唯一委托给注入服务的动作（assign_incident）在现有模板里
// 只挂在 ServiceTask 上（incident_emergency_flow 的 Activity_AutoAssign，由
// StartProcess 直接推进，已由 bpmn_platform_tenant_test.go 覆盖），没有可从任务完成
// 路径到达的 UserTask，所以这里锁定的是"registry 同源"这个共同前提，而不是再造一个
// 只为测试存在的 BPMN 模板。
func TestTaskService_ReusesEngineInstanceAndRegistry(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*CustomProcessEngine)

	first := engine.TaskService()
	second := engine.TaskService()
	require.Same(t, first, second, "TaskService() 必须返回同一个实例，不能每次现造")

	internal, ok := first.(*bpmnTaskService)
	require.True(t, ok)
	require.Same(t, engine, internal.engine,
		"bpmnTaskService 必须持有创建它的引擎，任务完成才会命中被 bootstrap 注入过的 CallbackRegistry")

	// 注入发生在拿到 TaskService 之后也必须可见（bootstrap 就是这个顺序）。
	incidentHandler, ok := engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler)
	require.True(t, ok, "incident_service_handler 必须已注册")
	incidentHandler.SetIncidentService(NewIncidentService(client, zap.NewNop().Sugar()))

	reachable, ok := internal.engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler)
	require.True(t, ok)
	require.Same(t, incidentHandler, reachable,
		"任务完成路径可达的 handler 必须就是被注入的那一个")
}
