package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap/zaptest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== BPMNProcessEngine 辅助方法测试 ====================

func TestBPMNProcessEngine_NewCustomProcessEngine(t *testing.T) {
	// 使用 nil client 创建引擎（用于测试辅助方法）
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		logger:     logger,
		parser:     NewBPMNParser(),
		exprEngine: NewExpressionEngine(),
	}

	require.NotNil(t, engine)
	require.NotNil(t, engine.parser)
	require.NotNil(t, engine.exprEngine)
}

// ==================== 流程查找方法测试 ====================

func TestBPMNProcessEngine_FindOutgoingFlows(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "flow1", SourceRef: "start", TargetRef: "task1"},
			{ID: "flow2", SourceRef: "start", TargetRef: "task2"},
			{ID: "flow3", SourceRef: "task1", TargetRef: "end"},
		},
	}

	// 查找从 start 开始的流向
	flows := engine.findOutgoingFlows(process, "start")
	assert.Len(t, flows, 2)
	assert.Equal(t, "flow1", flows[0].ID)
	assert.Equal(t, "flow2", flows[1].ID)

	// 查找从 task1 开始的流向
	flows = engine.findOutgoingFlows(process, "task1")
	assert.Len(t, flows, 1)
	assert.Equal(t, "flow3", flows[0].ID)

	// 查找不存在的节点
	flows = engine.findOutgoingFlows(process, "nonexistent")
	assert.Len(t, flows, 0)
}

func TestBPMNProcessEngine_IsEndEvent(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		EndEvents: []*BPMNEndEvent{
			{ID: "end1", Name: "结束事件"},
			{ID: "end2", Name: "取消结束"},
		},
	}

	assert.True(t, engine.isEndEvent(process, "end1"))
	assert.True(t, engine.isEndEvent(process, "end2"))
	assert.False(t, engine.isEndEvent(process, "start"))
	assert.False(t, engine.isEndEvent(process, "nonexistent"))
}

func TestBPMNProcessEngine_FindUserTask(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		UserTasks: []*BPMNUserTask{
			{ID: "task1", Name: "审批任务"},
			{ID: "task2", Name: "处理任务"},
		},
	}

	task := engine.findUserTask(process, "task1")
	require.NotNil(t, task)
	assert.Equal(t, "审批任务", task.Name)

	task = engine.findUserTask(process, "task2")
	require.NotNil(t, task)
	assert.Equal(t, "处理任务", task.Name)

	task = engine.findUserTask(process, "nonexistent")
	assert.Nil(t, task)
}

func TestBPMNProcessEngine_FindEndEvent(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		EndEvents: []*BPMNEndEvent{
			{ID: "end1", Name: "正常结束"},
		},
	}

	endEvent := engine.findEndEvent(process, "end1")
	require.NotNil(t, endEvent)
	assert.Equal(t, "正常结束", endEvent.Name)

	endEvent = engine.findEndEvent(process, "nonexistent")
	assert.Nil(t, endEvent)
}

func TestBPMNProcessEngine_FindExclusiveGateway(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		ExclusiveGateways: []*BPMNExclusiveGateway{
			{ID: "gw1", Name: "排他网关"},
		},
	}

	gateway := engine.findExclusiveGateway(process, "gw1")
	require.NotNil(t, gateway)
	assert.Equal(t, "排他网关", gateway.Name)

	gateway = engine.findExclusiveGateway(process, "nonexistent")
	assert.Nil(t, gateway)
}

func TestBPMNProcessEngine_FindServiceTask(t *testing.T) {
	engine := &CustomProcessEngine{}
	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{ID: "svc1", Name: "发送通知", Type: "notification"},
		},
	}

	task := engine.findServiceTask(process, "svc1")
	require.NotNil(t, task)
	assert.Equal(t, "发送通知", task.Name)

	task = engine.findServiceTask(process, "nonexistent")
	assert.Nil(t, task)
}

// ==================== 条件评估测试 ====================

func TestBPMNProcessEngine_EvaluateCondition_ComplexExpressions(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		logger:     logger,
		exprEngine: NewExpressionEngine(),
	}

	tests := []struct {
		name       string
		expression string
		variables  map[string]interface{}
		expected   bool
	}{
		{
			name:       "等于比较 - true",
			expression: "status == 'approved'",
			variables:  map[string]interface{}{"status": "approved"},
			expected:   true,
		},
		{
			name:       "等于比较 - false",
			expression: "status == 'approved'",
			variables:  map[string]interface{}{"status": "rejected"},
			expected:   false,
		},
		{
			name:       "数字大于比较",
			expression: "priority > 5",
			variables:  map[string]interface{}{"priority": 10},
			expected:   true,
		},
		{
			name:       "数字大于比较 - 不满足",
			expression: "priority > 5",
			variables:  map[string]interface{}{"priority": 3},
			expected:   false,
		},
		{
			name:       "布尔 true",
			expression: "isUrgent == true",
			variables:  map[string]interface{}{"isUrgent": true},
			expected:   true,
		},
		{
			name:       "布尔 false",
			expression: "isUrgent == false",
			variables:  map[string]interface{}{"isUrgent": true},
			expected:   false,
		},
		{
			name:       "AND 条件",
			expression: "status == 'approved' && priority > 3",
			variables:  map[string]interface{}{"status": "approved", "priority": 5},
			expected:   true,
		},
		{
			name:       "OR 条件",
			expression: "status == 'approved' || status == 'pending'",
			variables:  map[string]interface{}{"status": "pending"},
			expected:   true,
		},
		{
			name:       "混合条件",
			expression: "(status == 'approved') && (priority > 3 || isValid == true)",
			variables:  map[string]interface{}{"status": "approved", "priority": 1, "isValid": true},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.evaluateCondition(&BPMNSequenceFlow{
				ConditionExpression: &BPMNConditionExpression{
					Expression: tt.expression,
				},
			}, tt.variables)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBPMNProcessEngine_EvaluateCondition_InvalidExpressions(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		logger:     logger,
		exprEngine: NewExpressionEngine(),
	}

	invalidExpressions := []string{
		"invalid {{{{ expression",
		"unbalanced (parentheses",
		"undefined_var == 'test'",
		"status === 'test'", // 三等号无效
		"status = 'test'",   // 赋值不是比较
	}

	for _, expr := range invalidExpressions {
		result := engine.evaluateCondition(&BPMNSequenceFlow{
			ConditionExpression: &BPMNConditionExpression{
				Expression: expr,
			},
		}, map[string]interface{}{"status": "test"})
		assert.False(t, result, "Invalid expression '%s' should return false", expr)
	}
}

// ==================== 规则匹配测试 ====================

func TestMatchRuleConditions(t *testing.T) {
	tests := []struct {
		name       string
		conditions []map[string]interface{}
		taskName   string
		expected   bool
	}{
		{
			name:       "空条件列表",
			conditions: []map[string]interface{}{},
			taskName:   "审批任务",
			expected:   false,
		},
		{
			name: "equals 匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "equals", "value": "审批任务"},
			},
			taskName: "审批任务",
			expected: true,
		},
		{
			name: "equals 不匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "equals", "value": "处理任务"},
			},
			taskName: "审批任务",
			expected: false,
		},
		{
			name: "contains 匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "contains", "value": "审批"},
			},
			taskName: "经理审批任务",
			expected: true,
		},
		{
			name: "contains 不匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "contains", "value": "紧急"},
			},
			taskName: "审批任务",
			expected: false,
		},
		{
			name: "prefix 匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "prefix", "value": "经理"},
			},
			taskName: "经理审批",
			expected: true,
		},
		{
			name: "prefix 不匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "prefix", "value": "经理"},
			},
			taskName: "审批经理",
			expected: false,
		},
		{
			name: "suffix 匹配",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "suffix", "value": "审批"},
			},
			taskName: "经理审批",
			expected: true,
		},
		{
			name: "非 task_name 字段被忽略",
			conditions: []map[string]interface{}{
				{"field": "priority", "operator": "equals", "value": "high"},
				{"field": "task_name", "operator": "contains", "value": "审批"},
			},
			taskName: "审批任务",
			expected: true,
		},
		{
			name: "未知操作符",
			conditions: []map[string]interface{}{
				{"field": "task_name", "operator": "unknown", "value": "审批"},
			},
			taskName: "审批任务",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchRuleConditions(tt.conditions, tt.taskName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==================== Service Task 变量合并测试 ====================

func TestMergeServiceTaskVariables(t *testing.T) {
	instanceVars := map[string]interface{}{
		"ticket_id":    123,
		"requester_id": 456,
		"priority":     "high",
	}

	tests := []struct {
		name        string
		task        *BPMNServiceTask
		expectedLen int
	}{
		{
			name:        "nil task",
			task:        nil,
			expectedLen: 3, // 只有 instance vars
		},
		{
			name: "带所有属性的 task",
			task: &BPMNServiceTask{
				Type:           "notification",
				OperationRef:   "send_email",
				CCType:         "role",
				CCUserIDs:      "1,2,3",
				CCGroupIDs:     "g1,g2",
				CCRoleIDs:      "r1",
				CCVariable:     "cc_list",
				CCNotify:       "true",
				NotifyChannels: "email,wechat",
			},
			expectedLen: 12, // 3 instance + 9 task
		},
		{
			name: "带部分属性的 task",
			task: &BPMNServiceTask{
				Type: "notification",
			},
			expectedLen: 4, // 3 instance + 1 type
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeServiceTaskVariables(instanceVars, tt.task)
			assert.Equal(t, tt.expectedLen, len(result))

			// 验证实例变量被正确复制
			assert.Equal(t, 123, result["ticket_id"])
			assert.Equal(t, 456, result["requester_id"])
			assert.Equal(t, "high", result["priority"])
		})
	}
}

// ==================== 表达式函数注册测试 ====================

func TestCustomProcessEngine_RegisterProcessFunctions(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	engine := &CustomProcessEngine{
		logger:         logger,
		exprEngine:     NewExpressionEngine(),
		expressionVars: make(map[string]interface{}),
	}

	// 注册流程函数
	engine.registerProcessFunctions()

	// 验证表达式引擎已注册
	assert.NotNil(t, engine.expressionVars)
	assert.NotNil(t, engine.exprEngine)
}

// ==================== 流程定义服务接口测试 ====================

func TestBPMNProcessDefinitionService_CreateProcessDefinition(t *testing.T) {
	// 验证请求结构
	req := &CreateProcessDefinitionRequest{
		Key:         "test-process",
		Name:        "测试流程",
		Description: "这是一个测试流程",
		Category:    "test",
		BPMNXML:     "<bpmn>...</bpmn>",
		ProcessVariables: map[string]interface{}{
			"var1": "value1",
		},
		TenantID: 1,
	}

	require.NotEmpty(t, req.Key)
	require.NotEmpty(t, req.Name)
	require.NotEmpty(t, req.BPMNXML)
	require.Greater(t, req.TenantID, 0)
}

func TestBPMNProcessDefinitionService_ListProcessDefinitions(t *testing.T) {
	req := &ListProcessDefinitionsRequest{
		Key:      "test-process",
		Category: "incident",
		IsActive: boolPtr(true),
		TenantID: 1,
		Page:     1,
		PageSize: 10,
	}

	assert.Equal(t, "test-process", req.Key)
	assert.Equal(t, 1, req.Page)
	assert.Equal(t, 10, req.PageSize)
	assert.NotNil(t, req.IsActive)
	assert.True(t, *req.IsActive)
}

// ==================== 流程实例服务接口测试 ====================

func TestBPMNProcessInstanceService_GetProcessInstance(t *testing.T) {
	req := &ListProcessInstancesRequest{
		ProcessDefinitionKey: "test-process",
		Status:               "running",
		BusinessKey:          "ticket-123",
		TenantID:             1,
		Page:                 1,
		PageSize:             20,
	}

	assert.Equal(t, "test-process", req.ProcessDefinitionKey)
	assert.Equal(t, "running", req.Status)
}

func TestInstanceStatistics(t *testing.T) {
	stats := &InstanceStatistics{
		Total:      100,
		Running:    50,
		Completed:  40,
		Suspended:  5,
		Terminated: 5,
	}

	assert.Equal(t, 100, stats.Total)
	assert.Equal(t, 50, stats.Running)
	assert.Equal(t, 40, stats.Completed)
	assert.Equal(t, 5, stats.Suspended)
	assert.Equal(t, 5, stats.Terminated)
}

// ==================== 任务服务接口测试 ====================

func TestBPMNTaskService_ListUserTasks(t *testing.T) {
	req := &ListUserTasksRequest{
		Assignee:             "user1",
		CandidateUsers:       "user2,user3",
		CandidateGroups:      "managers",
		UserID:               1,
		Status:               "created",
		ProcessDefinitionKey: "test-process",
		ProcessInstanceID:    123,
		TenantID:             1,
		Page:                 1,
		PageSize:             10,
	}

	assert.Equal(t, "user1", req.Assignee)
	assert.Equal(t, 1, req.UserID)
}

func TestTaskStatistics(t *testing.T) {
	stats := &TaskStatistics{
		TotalTasks:        100,
		CompletedTasks:    60,
		PendingTasks:      30,
		OverdueTasks:      10,
		AverageCompletion: 3600000.0, // 1小时（毫秒）
		StatusBreakdown: map[string]int{
			"completed": 60,
			"pending":   30,
			"assigned":  10,
		},
		AssigneeBreakdown: map[string]int{
			"user1": 50,
			"user2": 50,
		},
	}

	assert.Equal(t, 100, stats.TotalTasks)
	assert.Equal(t, 60, stats.CompletedTasks)
	assert.Equal(t, 30, stats.PendingTasks)
	assert.Equal(t, 10, stats.OverdueTasks)
	assert.Len(t, stats.StatusBreakdown, 3)
	assert.Len(t, stats.AssigneeBreakdown, 2)
}

// ==================== 会签测试 ====================

func TestCounterSignStatus(t *testing.T) {
	status := &CounterSignStatus{
		ParentTaskID: "parent-1",
		Total:        5,
		Completed:    3,
		Approved:     2,
		Rejected:     1,
		Pending:      2,
		Status:       "pending",
	}

	assert.Equal(t, "parent-1", status.ParentTaskID)
	assert.Equal(t, 5, status.Total)
	assert.Equal(t, 2, status.Pending)
}

func TestCounterSignRequest(t *testing.T) {
	req := &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{"user1", "user2", "user3"},
		Threshold:    2,
	}

	assert.Equal(t, "parallel", req.ApprovalType)
	assert.Len(t, req.Approvers, 3)
	assert.Equal(t, 2, req.Threshold)
}

func TestVoteRequest(t *testing.T) {
	tests := []struct {
		name     string
		approved bool
		comment  string
	}{
		{"批准", true, "同意"},
		{"拒绝", false, "需要修改"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &VoteRequest{
				Approved: tt.approved,
				Comment:  tt.comment,
			}
			assert.Equal(t, tt.approved, req.Approved)
			assert.Equal(t, tt.comment, req.Comment)
		})
	}
}

// ==================== 版本号解析测试 ====================

func TestGetNextVersion(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		expected string
	}{
		{"初始版本", "1.0.0", "1.0.1"},
		{"递增 patch", "1.0.8", "1.0.9"},
		{"递增 minor", "1.0.9", "1.1.0"},
		{"递增 major", "1.9.9", "2.0.0"},
		{"带 v 前缀", "v1.0.0", "2.0.0"}, // 注意：当前实现会解析为 1.0.0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 由于 getNextVersion 是私有方法且需要数据库连接，
			// 这里我们只验证版本号格式的预期行为
			// 实际测试需要通过集成测试
			assert.NotEmpty(t, tt.existing)
		})
	}
}

// ==================== BPMN 结构体测试 ====================

func TestBPMNProcess_Structure(t *testing.T) {
	process := &BPMNProcess{
		ID:                "Process_1",
		Name:              "测试流程",
		StartEvents:       []*BPMNStartEvent{{ID: "StartEvent_1", Name: "开始"}},
		EndEvents:         []*BPMNEndEvent{{ID: "EndEvent_1", Name: "结束"}},
		UserTasks:         []*BPMNUserTask{{ID: "Task_1", Name: "用户任务"}},
		ServiceTasks:      []*BPMNServiceTask{{ID: "ServiceTask_1", Name: "服务任务"}},
		ExclusiveGateways: []*BPMNExclusiveGateway{{ID: "Gateway_1", Name: "网关"}},
		SequenceFlows:     []*BPMNSequenceFlow{{ID: "Flow_1", SourceRef: "StartEvent_1", TargetRef: "Task_1"}},
	}

	assert.NotEmpty(t, process.ID)
	assert.NotEmpty(t, process.StartEvents)
	assert.NotEmpty(t, process.EndEvents)
	assert.Len(t, process.UserTasks, 1)
	assert.Len(t, process.ServiceTasks, 1)
	assert.Len(t, process.ExclusiveGateways, 1)
	assert.Len(t, process.SequenceFlows, 1)
}

func TestBPMNUserTask_AssigneeExtraction(t *testing.T) {
	task := &BPMNUserTask{
		ID:              "Task_1",
		Name:            "审批任务",
		Assignee:        "",
		CandidateUsers:  "user1,user2",
		CandidateGroups: "managers",
	}

	// 验证候选用户和候选组
	assert.Empty(t, task.Assignee)
	assert.NotEmpty(t, task.CandidateUsers)
	assert.NotEmpty(t, task.CandidateGroups)
}

func TestBPMNServiceTask_ImplementationTypes(t *testing.T) {
	tasks := []*BPMNServiceTask{
		{ID: "svc1", Name: "task1", Implementation: "delegateExpression"},
		{ID: "svc2", Name: "task2", Class: "com.example.Delegate"},
		{ID: "svc3", Name: "task3", OperationRef: "operation1"},
	}

	for _, task := range tasks {
		// 至少有一个实现类型
		hasImplementation := task.Implementation != "" || task.Class != "" || task.DelegateExpression != "" || task.OperationRef != ""
		assert.True(t, hasImplementation, "Task %s should have an implementation type", task.ID)
	}
}

// ==================== 时间戳处理测试 ====================

func TestProcessInstance_Timestamps(t *testing.T) {
	now := time.Now()

	// 模拟流程实例的时间戳
	instance := struct {
		StartTime time.Time
		EndTime   *time.Time
	}{
		StartTime: now,
		EndTime:   nil,
	}

	assert.False(t, instance.EndTime != nil, "EndTime should be nil for running instance")

	// 完成后设置 EndTime
	endTime := now.Add(time.Hour)
	instance.EndTime = &endTime

	assert.NotNil(t, instance.EndTime)
	assert.True(t, instance.EndTime.After(instance.StartTime))
}

// ==================== Approval decision audit tests ====================

func newApprovalDecisionTestEngine(t *testing.T) (*CustomProcessEngine, context.Context) {
	client := enttest.Open(t, "sqlite3", "file:approval_decisions_engine?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok, "expected ProcessEngine to be *CustomProcessEngine")
	return engine, context.Background()
}

func setupApprovalDecisionFixture(t *testing.T, engine *CustomProcessEngine) (tenantID, actorID int) {
	t.Helper()
	ctx := context.Background()
	tenant, err := engine.client.Tenant.Create().
		SetName("Approval Tenant").
		SetCode("approval").
		SetDomain("approval.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	user, err := engine.client.User.Create().
		SetUsername("approver1").
		SetEmail("approver1@example.com").
		SetName("Approver One").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant.ID, user.ID
}

// createProcessFixture inserts a running process definition + instance + task.
// Returns the process instance ID (int PK) and the task ID (int PK).
func createProcessFixture(t *testing.T, engine *CustomProcessEngine, tenantID int, keySuffix string) (instanceID, taskID int) {
	t.Helper()
	ctx := context.Background()
	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-" + keySuffix).
		SetDeploymentName("Deployment " + keySuffix).
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	def, err := engine.client.ProcessDefinition.Create().
		SetKey("approval_test_" + keySuffix).
		SetName("Approval Test " + keySuffix).
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := engine.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-" + keySuffix).
		SetProcessDefinitionKey(def.Key).
		SetProcessDefinitionID(def.ID).
		SetStatus("running").
		SetVariables(map[string]interface{}{"business_type": "change", "business_id": "CHG-" + keySuffix}).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	task, err := engine.client.ProcessTask.Create().
		SetTaskID("TASK-" + keySuffix).
		SetTaskDefinitionKey("node_" + keySuffix).
		SetTaskName("Approval Task " + keySuffix).
		SetProcessDefinitionKey(def.Key).
		SetProcessInstanceID(instance.ID).
		SetStatus("running").
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return instance.ID, task.ID
}

func TestRecordApprovalDecision_PersistsApproveReject(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	instanceID, taskID := createProcessFixture(t, engine, tenantID, "approval1")
	instance, err := engine.client.ProcessInstance.Get(ctx, instanceID)
	require.NoError(t, err)
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)

	variables := map[string]interface{}{
		"approvalAction":  "approve",
		"approvalResult":  "approved",
		"approvalComment": "lgtm",
	}
	require.NoError(t, engine.recordApprovalDecision(ctx, instance, task, variables))

	stored, err := engine.client.ProcessApprovalDecision.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "approve", stored[0].Action)
	assert.Equal(t, "approved", stored[0].Decision)
	assert.Equal(t, "lgtm", stored[0].Comment)
	assert.Equal(t, "change", stored[0].BusinessType)
	assert.Equal(t, "CHG-approval1", stored[0].BusinessID)
	assert.Equal(t, actorID, stored[0].ActorID)
}

func TestRecordApprovalDecision_SkippedWhenActionMissing(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	instanceID, taskID := createProcessFixture(t, engine, tenantID, "skip1")
	instance, err := engine.client.ProcessInstance.Get(ctx, instanceID)
	require.NoError(t, err)
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)

	require.NoError(t, engine.recordApprovalDecision(ctx, instance, task, map[string]interface{}{"unrelated": "x"}))
	count, err := engine.client.ProcessApprovalDecision.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestRecordApprovalDecision_RejectsMissingActor(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	instanceID, taskID := createProcessFixture(t, engine, tenantID, "noactor1")
	instance, err := engine.client.ProcessInstance.Get(ctx, instanceID)
	require.NoError(t, err)
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)

	err = engine.recordApprovalDecision(ctx, instance, task, map[string]interface{}{"approvalAction": "approve"})
	assert.Error(t, err)
}

func TestAuthorizeTaskActor_AllowsAssigneeAndCandidate(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, taskID1 := createProcessFixture(t, engine, tenantID, "authz1")
	_, taskID2 := createProcessFixture(t, engine, tenantID, "authz2")
	_, taskID3 := createProcessFixture(t, engine, tenantID, "authz3")

	// 1. Assignee match by id
	assignee := fmt.Sprintf("%d", actorID)
	task1, err := engine.client.ProcessTask.UpdateOneID(taskID1).SetAssignee(assignee).Save(ctx)
	require.NoError(t, err)
	assert.NoError(t, engine.authorizeTaskActor(ctx, task1))

	// 2. Candidate user match by username
	task2, err := engine.client.ProcessTask.UpdateOneID(taskID2).SetCandidateUsers("approver1").Save(ctx)
	require.NoError(t, err)
	assert.NoError(t, engine.authorizeTaskActor(ctx, task2))

	// 3. No match
	task3, err := engine.client.ProcessTask.UpdateOneID(taskID3).SetAssignee("someone-else").SetCandidateUsers("other-user").Save(ctx)
	require.NoError(t, err)
	assert.Error(t, engine.authorizeTaskActor(ctx, task3))
}

func TestAuthorizeTaskActor_NoActorContextIsPermissive(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, taskID := createProcessFixture(t, engine, tenantID, "noctx1")
	task, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	// No actor in context should not error (system/internal calls stay working)
	assert.NoError(t, engine.authorizeTaskActor(ctx, task))
}

func TestAuthorizeTaskActor_AllowsCandidateGroupMatch(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).
		AddMemberIDs(actorID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "groupauthz1")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCandidateGroups("network_eng").
		Save(ctx)
	require.NoError(t, err)

	assert.NoError(t, engine.authorizeTaskActor(ctx, task),
		"a caller who is only a candidate via candidate_groups must be allowed to act on the task")
}

func TestIsTaskCandidate_AllowsCandidateGroupMatch(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).
		AddMemberIDs(actorID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "groupcandidate1")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCandidateGroups("network_eng").
		Save(ctx)
	require.NoError(t, err)

	ok, err := isTaskCandidate(ctx, engine.client, actorID, task)
	require.NoError(t, err)
	assert.True(t, ok, "a caller who is only a candidate via candidate_groups must be claimable")
}

func TestBPMNServiceTask_ServiceTaskType_ReadsExtensionElementsMetaData(t *testing.T) {
	task := &BPMNServiceTask{
		ID: "svc1",
		ExtensionElements: &BPMNExtensionElements{
			MetaData: []BPMNMetaData{
				{Name: "service_task_type", Value: "generic_task"},
				{Name: "action", Value: "notify"},
			},
		},
	}
	assert.Equal(t, "generic_task", task.ServiceTaskType())
	assert.Equal(t, "notify", task.ServiceTaskAction())
}

func TestBPMNServiceTask_ServiceTaskType_NilExtensionElementsReturnsEmpty(t *testing.T) {
	task := &BPMNServiceTask{ID: "svc2"}
	assert.Equal(t, "", task.ServiceTaskType())
	assert.Equal(t, "", task.ServiceTaskAction())
}

func TestHandleElement_ServiceTask_DispatchesByMetaDataOverAttributeGuessing(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	tkt, err := engine.client.Ticket.Create().
		SetTitle("svc-task-dispatch-test").SetTicketNumber("T-SVC-1").SetStatus("open").
		SetRequesterID(actorID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	// ProcessInstance.process_definition_id 是 schema 里的必填正整数外键（非
	// Optional），跟 createProcessFixture（本文件上方）一样先落一条最小
	// ProcessDeployment + ProcessDefinition，拿到真实 ID 再建 ProcessInstance。
	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-svc-dispatch-test").
		SetDeploymentName("Deployment svc-dispatch-test").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	def, err := engine.client.ProcessDefinition.Create().
		SetKey("svc_dispatch_test_flow").
		SetName("Svc Dispatch Test Flow").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := engine.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-svc-dispatch-test").
		SetProcessDefinitionKey(def.Key).
		SetProcessDefinitionID(def.ID).
		SetBusinessKey(fmt.Sprintf("ticket:%d", tkt.ID)).
		SetStatus("running").SetTenantID(tenantID).
		SetVariables(map[string]interface{}{"business_type": "ticket", "business_id": tkt.ID}).
		Save(ctx)
	require.NoError(t, err)

	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{
				ID:             "Activity_UpdateStatus",
				Name:           "更新状态",
				Implementation: "##WebService", // 内置模板里的占位符属性，不应该被用来查 handler
				ExtensionElements: &BPMNExtensionElements{
					MetaData: []BPMNMetaData{
						{Name: "service_task_type", Value: "ticket_task"},
						{Name: "action", Value: "update_status"},
					},
				},
			},
		},
		EndEvents: []*BPMNEndEvent{{ID: "End_1", Name: "结束"}},
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "Flow_1", SourceRef: "Activity_UpdateStatus", TargetRef: "End_1"},
		},
	}

	err = engine.handleElement(ctx, instance, process, "Activity_UpdateStatus")
	require.NoError(t, err)

	updated, err := engine.client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", updated.Status, "ticket_task 的 update_status（默认目标状态）应该真实生效")
}

// TestHandleElement_ServiceTask_IncidentAutoAssign_NoAssignee_ContinuesFlow 是 Finding 1
// 在"bug 真正显形的那一层"的回归：incident_emergency_flow 的 Activity_AutoAssign 是起始
// 事件后的第一个 serviceTask（service_task_type=incident_task, action=assign_incident），
// 而新建事件的 assignee_id 天生是 0（Optional 字段）。handler 一旦对空处理人返回 error，
// handleElement 会把错误往上抛、StartProcess 整体失败，而调用方
// （incident_service.go 的 fire-and-forget goroutine）只 Warnw 一句——流程实例就永久卡在
// 起始事件上，对任何用户都不可见。这里断言的是：handleElement 成功返回，且流程能推进到下一步。
func TestHandleElement_ServiceTask_IncidentAutoAssign_NoAssignee_ContinuesFlow(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	inc, err := engine.client.Incident.Create().
		SetTitle("自动分配空态回归").
		SetIncidentNumber("INC-AUTOASSIGN-1").
		SetStatus("new").
		SetReporterID(actorID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	require.Zero(t, inc.AssigneeID, "新建事件默认没有处理人，这正是生产里的常态")

	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-incident-autoassign").
		SetDeploymentName("Deployment incident-autoassign").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	def, err := engine.client.ProcessDefinition.Create().
		SetKey("incident_autoassign_test_flow").
		SetName("Incident AutoAssign Test Flow").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := engine.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-incident-autoassign").
		SetProcessDefinitionKey(def.Key).
		SetProcessDefinitionID(def.ID).
		SetBusinessKey(fmt.Sprintf("incident:%d", inc.ID)).
		SetStatus("running").SetTenantID(tenantID).
		SetVariables(map[string]interface{}{
			"business_type": "incident",
			"business_id":   inc.ID,
			"incident_id":   inc.ID,
			"assignee_id":   inc.AssigneeID, // 0：没有可用处理人
			"tenant_id":     tenantID,
		}).
		Save(ctx)
	require.NoError(t, err)

	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{
				ID:             "Activity_AutoAssign",
				Name:           "自动分配",
				Implementation: "##WebService",
				ExtensionElements: &BPMNExtensionElements{
					MetaData: []BPMNMetaData{
						{Name: "service_task_type", Value: "incident_task"},
						{Name: "action", Value: "assign_incident"},
					},
				},
			},
		},
		EndEvents: []*BPMNEndEvent{{ID: "End_1", Name: "结束"}},
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "Flow_1", SourceRef: "Activity_AutoAssign", TargetRef: "End_1"},
		},
	}

	err = engine.handleElement(ctx, instance, process, "Activity_AutoAssign")
	require.NoError(t, err, "无处理人是正常空态，不应该让 handleElement 失败、把流程卡死在起始节点")

	// 流程确实推进到了下一步（End_1 -> completeProcess）
	updatedInstance, err := engine.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedInstance.Status, "空态跳过后流程应该继续走到结束事件")

	updatedIncident, err := engine.client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	assert.Zero(t, updatedIncident.AssigneeID, "空态跳过时不得写入处理人")
	assert.Equal(t, "new", updatedIncident.Status, "空态跳过时不得改状态")
}

// ==================== StartProcess initiator population tests ====================

// minimalStartToEndBPMN builds a minimal, parseable BPMN XML with just a
// start event flowing directly to an end event, for tests that need to
// exercise StartProcess end-to-end (parser + executor) rather than
// constructing ProcessInstance/ProcessTask rows directly.
func minimalStartToEndBPMN(processKey string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://itsm">
  <process id="` + processKey + `" isExecutable="true">
    <startEvent id="StartEvent_1" name="Start">
      <outgoing>Flow_1</outgoing>
    </startEvent>
    <endEvent id="EndEvent_1" name="End">
      <incoming>Flow_1</incoming>
    </endEvent>
    <sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
  </process>
</definitions>`
}

func TestStartProcess_PopulatesInitiatorFromContextUser(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)

	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-initiator1").SetDeploymentName("Deployment initiator1").
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).
		SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessDefinition.Create().
		SetKey("initiator_test_flow").SetName("Initiator Test").SetVersion("1").
		SetIsLatest(true).SetIsActive(true).
		SetBpmnXML([]byte(minimalStartToEndBPMN("initiator_test_flow"))).
		SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	instance, err := engine.StartProcess(ctx, "initiator_test_flow", "initiator-biz-1", map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", actorID), instance.Initiator)
}

func TestStartProcess_FallsBackToRequesterIDVariableWhenNoContextUser(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)

	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-initiator2").SetDeploymentName("Deployment initiator2").
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).
		SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessDefinition.Create().
		SetKey("initiator_test_flow2").SetName("Initiator Test 2").SetVersion("1").
		SetIsLatest(true).SetIsActive(true).
		SetBpmnXML([]byte(minimalStartToEndBPMN("initiator_test_flow2"))).
		SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	// System-triggered start: no authenticated user in ctx, only the
	// requester_id convention variable (see ticket_service.go's trigger path).
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	instance, err := engine.StartProcess(ctx, "initiator_test_flow2", "initiator-biz-2", map[string]interface{}{
		"requester_id": float64(actorID),
	})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", actorID), instance.Initiator)
}

// ==================== ListProcessInstances participant-scoping tests ====================

func TestListProcessInstances_NonElevatedSeesOnlyInitiatedOrParticipated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("other-user").SetEmail("other-user@example.com").SetName("Other User").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	// Instance 1: viewer is the initiator.
	initiatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list1-initiated")
	_, err = engine.client.ProcessInstance.UpdateOneID(initiatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	// Instance 2: viewer is not the initiator, but is a task assignee on it.
	participatedInstanceID, participatedTaskID := createProcessFixture(t, engine, tenantID, "list1-participated")
	_, err = engine.client.ProcessInstance.UpdateOneID(participatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(participatedTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	// Instance 3: viewer has no relation to it at all.
	unrelatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list1-unrelated")
	_, err = engine.client.ProcessInstance.UpdateOneID(unrelatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	// Not elevated.
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	svc := engine.ProcessInstanceService()
	instances, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	ids := make([]int, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	assert.ElementsMatch(t, []int{initiatedInstanceID, participatedInstanceID}, ids)
}

func TestListProcessInstances_ElevatedSeesEverythingInTenant(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("other-user2").SetEmail("other-user2@example.com").SetName("Other User 2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	unrelatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list2-unrelated")
	_, err = engine.client.ProcessInstance.UpdateOneID(unrelatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	svc := engine.ProcessInstanceService()
	_, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 1, total, "elevated caller must see the unrelated instance too")
}

func TestListProcessInstances_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("part-other").SetDomain("part-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	otherInstanceID, _ := createProcessFixture(t, engine, otherTenant.ID, "list3-other-tenant")
	_, err = engine.client.ProcessInstance.UpdateOneID(otherInstanceID).
		SetInitiator(fmt.Sprintf("%d", viewerID)). // same viewer ID, different tenant
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	svc := engine.ProcessInstanceService()
	_, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "must never return another tenant's instance regardless of elevation")
}

// TestListProcessInstances_SubstringCandidateDoesNotLeak guards against the
// coarse SQL Contains predicate (LIKE '%v%') being mistaken for an exact
// participant match. A task whose candidate_users merely CONTAINS the
// viewer's ID as a substring (e.g. viewer ID "1" inside candidate string
// "19") must NOT make that task's process instance visible to the viewer —
// only identity.IsTaskParticipant's exact, trimmed per-CSV-element
// comparison should decide that.
func TestListProcessInstances_SubstringCandidateDoesNotLeak(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("other-user3").SetEmail("other-user3@example.com").SetName("Other User 3").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	// Instance whose only task has a candidate_users value that is a
	// superstring of the viewer's ID (not an exact CSV-token match) — e.g.
	// viewer ID "1" makes the coarse Contains("1") predicate match a
	// candidate string like "19", even though "19" != "1".
	substringInstanceID, substringTaskID := createProcessFixture(t, engine, tenantID, "list4-substring")
	_, err = engine.client.ProcessInstance.UpdateOneID(substringInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	collidingCandidate := fmt.Sprintf("%d9", viewerID) // e.g. viewerID=1 -> "19"; contains "1" but != "1"
	_, err = engine.client.ProcessTask.UpdateOneID(substringTaskID).
		SetCandidateUsers(collidingCandidate).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	svc := engine.ProcessInstanceService()
	instances, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "a substring-only candidate_users match must not leak the instance")
	assert.Empty(t, instances)
}

// ==================== GetTask/GetTaskByID authorization tests ====================

func TestGetTaskByID_ParticipantCanView(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "gettask1")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", actorID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, task.ID)
}

func TestGetTaskByID_InitiatorCanViewReadOnly(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "gettask2")
	_, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).Save(context.Background())
	require.NoError(t, err)
	// Task is assigned to someone else — initiator is not a candidate on it.
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee("someone-else").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err, "the process initiator must be able to view any task in their own instance")
	assert.Equal(t, taskID, task.ID)
}

func TestGetTaskByID_NonParticipantDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("bystander").SetEmail("bystander@example.com").SetName("Bystander").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	instanceID, taskID := createProcessFixture(t, engine, tenantID, "gettask3")
	_, err = engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err = engine.TaskService().GetTaskByID(ctx, taskID)
	assert.Error(t, err, "a non-participant, non-elevated caller must not be able to view the task")
}

func TestGetTaskByID_ElevatedCanViewAnything(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("bystander2").SetEmail("bystander2@example.com").SetName("Bystander 2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "gettask4")
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, task.ID)
}

// TestGetTaskByID_CrossTenantNeverLeaks guards the explicit TenantID
// predicate in GetTaskByID's query (service/bpmn_process_engine.go): a task
// belonging to another tenant must be unreachable by numeric ID alone, even
// when the caller happens to be assigned to it (same user ID reused across
// tenants) and even when the caller is elevated — tenant isolation is a
// harder boundary than participation/elevation and must fail closed
// regardless of either.
func TestGetTaskByID_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("gettask-other").SetDomain("gettask-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	_, otherTaskID := createProcessFixture(t, engine, otherTenant.ID, "gettask-cross-tenant")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)). // same viewer ID, different tenant
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	_, err = engine.TaskService().GetTaskByID(ctx, otherTaskID)
	assert.Error(t, err, "must never return another tenant's task regardless of elevation or assignee match")
}

// ==================== ListUserTasks authorization tests ====================

func TestListUserTasks_NonElevatedIgnoresOverrideParams(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("victim").SetEmail("victim@example.com").SetName("Victim").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, viewerTaskID := createProcessFixture(t, engine, tenantID, "listtasks1-mine")
	_, err = engine.client.ProcessTask.UpdateOneID(viewerTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	_, otherTaskID := createProcessFixture(t, engine, tenantID, "listtasks1-victim")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	// Attacker-style request: explicitly ask for the OTHER user's tasks.
	req := &ListUserTasksRequest{
		Assignee: fmt.Sprintf("%d", otherUser.ID),
		TenantID: tenantID,
		Page:     1, PageSize: 50,
	}
	tasks, total, err := engine.TaskService().ListUserTasks(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, total, "must be forced back to the caller's own scope, not the requested override")
	assert.Equal(t, viewerTaskID, tasks[0].ID)
}

func TestListUserTasks_ElevatedHonorsOverrideParams(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("target-user").SetEmail("target-user@example.com").SetName("Target").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, otherTaskID := createProcessFixture(t, engine, tenantID, "listtasks2-target")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	req := &ListUserTasksRequest{
		Assignee: fmt.Sprintf("%d", otherUser.ID),
		TenantID: tenantID,
		Page:     1, PageSize: 50,
	}
	tasks, total, err := engine.TaskService().ListUserTasks(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, otherTaskID, tasks[0].ID)
}

func TestListUserTasks_MultiGroupCallerMatchesAnyOfTheirGroups(t *testing.T) {
	// The brief's original version of this test put the caller in TWO
	// groups to prove the OR-loop over identity.GroupsCSV (added in this
	// task) beats the old single CandidateGroupsContains(wholeCSV) check.
	// That fixture is impossible under this schema: Group.Edges().members
	// is backed by a single nullable "group_members" FK column on `users`
	// (see ent/migrate/schema.go), not a join table, so a user can belong
	// to at most ONE group at a time — a second
	// Group.Create()...AddMemberIDs(viewerID) errors with "already
	// connected to a different group_members" (confirmed by running this
	// fixture). This is a pre-existing, documented constraint elsewhere
	// too (service/bpmn/bpmn_group_resolver_db_test.go:
	// "user 只能属于一个 group...多组场景需要多用户").
	//
	// Given that, identity.GroupsCSV can never actually contain more than
	// one entry for a real caller, so the OR-loop's split behavior is
	// defensive/future-proofing rather than something a real single-user
	// fixture can exercise as a true regression. This test instead proves
	// the still-real, still-testable part of the same code path: a
	// caller sees a task via their (single) candidate_groups membership,
	// and does NOT see a task whose candidate_groups names a different
	// group they don't belong to.
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	_, err := engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).AddMemberIDs(viewerID).
		Save(context.Background())
	require.NoError(t, err)

	_, matchingTaskID := createProcessFixture(t, engine, tenantID, "listtasks3-group")
	_, err = engine.client.ProcessTask.UpdateOneID(matchingTaskID).
		SetCandidateGroups("network_eng").Save(context.Background())
	require.NoError(t, err)

	_, otherGroupTaskID := createProcessFixture(t, engine, tenantID, "listtasks3-othergroup")
	_, err = engine.client.ProcessTask.UpdateOneID(otherGroupTaskID).
		SetCandidateGroups("finance_team").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	tasks, total, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		UserID: viewerID, TenantID: tenantID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, matchingTaskID, tasks[0].ID)
}

// TestListUserTasks_SubstringCandidateDoesNotLeak guards against the coarse
// SQL Contains predicate (LIKE '%v%') being mistaken for an exact
// participant match in the req.UserID > 0 branch. A task whose
// candidate_users/candidate_groups merely CONTAINS the caller's ID/group as
// a substring (e.g. caller ID "1" inside candidate string "19") must NOT
// show up in that caller's task list — only identity.IsTaskParticipant's
// exact, trimmed per-CSV-element comparison should decide that. Modeled
// after TestListProcessInstances_SubstringCandidateDoesNotLeak.
func TestListUserTasks_SubstringCandidateDoesNotLeak(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	// Task whose candidate_users value is a superstring of the viewer's ID
	// (not an exact CSV-token match) — e.g. viewer ID "1" makes the coarse
	// Contains("1") predicate match a candidate string like "19", even
	// though "19" != "1".
	_, substringTaskID := createProcessFixture(t, engine, tenantID, "listtasks4-substring")
	collidingCandidate := fmt.Sprintf("%d9", viewerID) // e.g. viewerID=1 -> "19"; contains "1" but != "1"
	_, err := engine.client.ProcessTask.UpdateOneID(substringTaskID).
		SetCandidateUsers(collidingCandidate).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	tasks, total, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		UserID: viewerID, TenantID: tenantID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "a substring-only candidate_users match must not leak the task")
	assert.Empty(t, tasks)
}

func TestAssignTask_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target").SetEmail("assignee-target@example.com").SetName("Target").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "assign1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().AssignTask(notParticipantCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	assert.Error(t, err, "a non-participant, non-elevated caller must not be able to reassign the task")

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TaskService().AssignTask(elevatedCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	require.NoError(t, err, "an elevated caller must be able to reassign any task")

	auditLogs, err := engine.client.ProcessAuditLog.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
	assert.Equal(t, AuditActionTaskAssigned, auditLogs[0].Action)
	assert.Equal(t, actorID, auditLogs[0].UserID)
}

func TestCancelTask_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "cancel1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().CancelTask(notParticipantCtx, task.TaskID, "no longer needed")
	assert.Error(t, err)

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TaskService().CancelTask(elevatedCtx, task.TaskID, "no longer needed")
	require.NoError(t, err)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionTaskCancelled)).All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
}

func TestSetTaskVariables_ParticipantAllowedAndAudited(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "vars1")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", actorID)).SetTaskVariables(map[string]interface{}{"comment": "old"}).
		Save(context.Background())
	require.NoError(t, err)
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"comment": "new"})
	require.NoError(t, err)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionVariableChanged)).All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
}

func TestCreateCounterSignTasks_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	approver, err := engine.client.User.Create().
		SetUsername("countersign-approver").SetEmail("countersign-approver@example.com").SetName("Approver").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "countersign1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	_, err = engine.TaskService().CreateCounterSignTasks(notParticipantCtx, task.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	assert.Error(t, err)

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	created, err := engine.TaskService().CreateCounterSignTasks(elevatedCtx, task.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	require.NoError(t, err)
	assert.Len(t, created, 1)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionActivityStarted)).All(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(auditLogs), 1)
}

// TestCreateCounterSignTasks_CrossTenantNeverLeaks guards
// CreateCounterSignTasks's parentTask lookup, which — unlike AssignTask/
// CancelTask/SetTaskVariables, which all route through the tenant-filtered
// s.GetTask — did its own inline `ProcessTask.Query().Where(TaskID(...))`
// with no TenantID predicate at all. A caller from tenant A must not be
// able to create counter-sign tasks against a parent task belonging to
// tenant B by supplying that task's TaskID, even when elevated (elevated
// bypasses the participant check inside authorizeTaskMutation entirely, so
// tenant isolation on the initial lookup is the only remaining guard).
func TestCreateCounterSignTasks_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("countersign-other").SetDomain("countersign-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	_, otherTaskID := createProcessFixture(t, engine, otherTenant.ID, "countersign-cross-tenant")
	otherTask, err := engine.client.ProcessTask.Get(context.Background(), otherTaskID)
	require.NoError(t, err)

	approver, err := engine.client.User.Create().
		SetUsername("countersign-cross-approver").SetEmail("countersign-cross-approver@example.com").SetName("Cross Approver").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	_, err = engine.TaskService().CreateCounterSignTasks(ctx, otherTask.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	assert.Error(t, err, "must never create counter-sign tasks against another tenant's parent task, regardless of elevation")
}

// TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord verifies that
// authorizeTaskMutation's permissive "no actor" case (userID<=0, matching
// authorizeTaskActor's existing "system/internal call" convention) does NOT
// also produce an audit record with a bogus UserID:0/UserName:"" row. A
// caller lacking bpmn.BPMNUserIDContextKey entirely represents an internal/
// system call path, not an unauthenticated human — but it must still leave
// no audit trail, since there is no real actor to attribute the action to.
func TestAssignTask_SystemCallerNoUserIDProducesNoAuditRecord(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target-2").SetEmail("assignee-target-2@example.com").SetName("Target2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "assign-noactor")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	// Deliberately no bpmn.BPMNUserIDContextKey set at all - simulates a
	// system/internal call, not an authenticated human caller. AssignTask's
	// own authorizeTaskMutation check is untouched by Task 2 and still
	// treats userID<=0 as a permissive system/internal call, but AssignTask
	// first calls GetTask, which is gated by authorizeTaskViewer — that now
	// requires an explicit system-caller declaration rather than inferring
	// it from the absence of a user, so this fixture must declare it too.
	systemCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	systemCtx = context.WithValue(systemCtx, bpmn.BPMNSystemCallerContextKey, true)

	err = engine.TaskService().AssignTask(systemCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	require.NoError(t, err, "a system/internal call with no actor must still be permitted (matches authorizeTaskActor's convention)")

	auditLogs, err := engine.client.ProcessAuditLog.Query().All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, auditLogs, "a system/internal call with no actor must not produce an audit record")
}

// ==================== Final fix-wave regression tests ====================

// TestGetCounterSignStatus_CrossTenantNeverLeaks guards GetCounterSignStatus,
// which previously did zero tenant scoping AND zero authorization: any
// authenticated user in any tenant holding a task_id could read another
// tenant's counter-sign state (final whole-branch review Finding 1). Now it
// must tenant-scope both the parent-task and sub-task queries, and gate on
// the parent task via authorizeTaskViewer, before returning any status data.
func TestGetCounterSignStatus_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("countersign-status-other").SetDomain("countersign-status-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	_, otherParentTaskID := createProcessFixture(t, engine, otherTenant.ID, "countersign-status-cross-tenant")
	otherParentTask, err := engine.client.ProcessTask.Get(context.Background(), otherParentTaskID)
	require.NoError(t, err)

	approver, err := engine.client.User.Create().
		SetUsername("countersign-status-approver").SetEmail("countersign-status-approver@example.com").SetName("Approver").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(otherTenant.ID).
		Save(context.Background())
	require.NoError(t, err)

	// Create real counter-sign sub-tasks under the other tenant's parent
	// task, from a properly scoped other-tenant context, so there is real
	// data to potentially leak.
	otherTenantCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, otherTenant.ID)
	otherTenantCtx = context.WithValue(otherTenantCtx, bpmn.BPMNElevatedContextKey, true)
	_, err = engine.TaskService().CreateCounterSignTasks(otherTenantCtx, otherParentTask.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	require.NoError(t, err)

	// Caller belongs to a different tenant, has no relation whatsoever to
	// the other tenant's task, and is even elevated within their OWN
	// tenant — elevation must not cross the tenant boundary.
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	_, err = engine.TaskService().GetCounterSignStatus(ctx, otherParentTask.TaskID)
	assert.Error(t, err, "must never return another tenant's counter-sign status regardless of elevation")
}

// TestListUserTasks_NoUserIDNonElevatedReturnsEmpty guards ListUserTasks's
// fail-open bug: when a non-elevated caller has no resolvable user ID
// (bpmn.BPMNUserIDContextKey absent or 0), the old code forced
// req.UserID = 0, which then fell through to the unfiltered "else" branch
// (req.Assignee/CandidateUsers/CandidateGroups were also blanked to "") and
// returned the ENTIRE tenant's task list — the opposite of fail-closed.
// Mirrors ListProcessInstances's existing "userID <= 0 -> empty" guard
// (final whole-branch review Finding 4).
func TestListUserTasks_NoUserIDNonElevatedReturnsEmpty(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)

	// A task exists in the tenant, unrelated to any particular caller —
	// this is what the old bug would have handed back wholesale.
	_, taskID := createProcessFixture(t, engine, tenantID, "listtasks-noauth")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetStatus("assigned").Save(context.Background())
	require.NoError(t, err)

	// Deliberately no bpmn.BPMNUserIDContextKey set, and not elevated.
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	tasks, total, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		TenantID: tenantID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "a non-elevated caller with no resolvable user ID must see nothing, not the whole tenant's tasks")
	assert.Empty(t, tasks)
}

// TestListUserTasks_CrossTenantNeverLeaks closes a test-coverage gap flagged
// by the final whole-branch review (Finding 9): ListUserTasks had no
// cross-tenant regression test, unlike its sibling ListProcessInstances
// (TestListProcessInstances_CrossTenantNeverLeaks). Modeled on that test.
func TestListUserTasks_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("listtasks-other").SetDomain("listtasks-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	_, otherTaskID := createProcessFixture(t, engine, otherTenant.ID, "listtasks-cross-tenant")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)). // same viewer ID, different tenant
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	tasks, total, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		UserID: viewerID, TenantID: tenantID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "must never return another tenant's task regardless of elevation or assignee match")
	assert.Empty(t, tasks)
}

// TestSetTaskVariables_NonParticipantDeniedUnlessElevated closes a
// test-coverage gap flagged by the final whole-branch review (Finding 8):
// SetTaskVariables only had a participant-allowed-and-audited test, with no
// non-participant-denied or elevated case, unlike its siblings AssignTask/
// CancelTask. Modeled on TestAssignTask_NonParticipantDeniedUnlessElevated.
func TestSetTaskVariables_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "vars-nonparticipant")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().SetTaskVariables(notParticipantCtx, task.TaskID, map[string]interface{}{"comment": "hijacked"})
	assert.Error(t, err, "a non-participant, non-elevated caller must not be able to set task variables")

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TaskService().SetTaskVariables(elevatedCtx, task.TaskID, map[string]interface{}{"comment": "elevated-set"})
	require.NoError(t, err, "an elevated caller must be able to set variables on any task")

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionVariableChanged)).All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1, "only the elevated call should produce an audit record")
}

// TestVote_CrossTenantDenied confirms (final whole-branch review Finding 2)
// that Vote's two ProcessTask lookups, now given explicit TenantID
// predicates as defense-in-depth, actually deny a cross-tenant vote attempt
// rather than merely relying on incidental protection elsewhere. The
// assignee field is deliberately set to the SAME numeric ID as the caller's
// own user ID (just in a different tenant) — before the tenant predicate,
// authorizeTaskActor's MatchesAssigneeOrCandidateUser would have matched on
// that string equality alone; with the predicate, the initial task lookup
// itself never finds the other tenant's task.
func TestVote_CrossTenantDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("vote-other").SetDomain("vote-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	_, otherTaskID := createProcessFixture(t, engine, otherTenant.ID, "vote-cross-tenant")
	otherTask, err := engine.client.ProcessTask.Get(context.Background(), otherTaskID)
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)). // same numeric ID as the caller, different tenant
		SetStatus("assigned").
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().Vote(ctx, otherTask.TaskID, &VoteRequest{Approved: true, Comment: "should be denied"})
	assert.Error(t, err, "must never allow a vote on another tenant's task, even with a colliding assignee ID")
}

// TestVote_ByCounterSignApproverNotOriginalParentAssignee guards against a
// regression discovered while implementing Finding 1's fix: gating
// GetCounterSignStatus solely on the PARENT task's participant list (via
// authorizeTaskViewer) breaks the normal counter-sign flow. CreateCounterSignTasks
// fans a parent task out into N per-approver sub-tasks without touching the
// parent task's own assignee field, so a real counter-sign approver — who is
// only ever a participant of their OWN sub-task, gated correctly by
// authorizeTaskActor when Vote fetches it — is very often NOT a participant
// of the parent task. Vote calls GetCounterSignStatus internally after
// recording the vote; that internal call must not fail for this caller.
// See authorizeCounterSignViewer, which allows either parent-task
// participation OR any sub-task participation.
func TestVote_ByCounterSignApproverNotOriginalParentAssignee(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)

	approver, err := engine.client.User.Create().
		SetUsername("countersign-vote-approver").SetEmail("countersign-vote-approver@example.com").SetName("Vote Approver").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	approver2, err := engine.client.User.Create().
		SetUsername("countersign-vote-approver2").SetEmail("countersign-vote-approver2@example.com").SetName("Vote Approver 2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, parentTaskID := createProcessFixture(t, engine, tenantID, "vote-countersign")
	parentTask, err := engine.client.ProcessTask.Get(context.Background(), parentTaskID)
	require.NoError(t, err)
	// parentTask.Assignee is left at its zero value — CreateCounterSignTasks
	// never sets it, so it is NOT the approver below, by design.

	elevatedCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	elevatedCtx = context.WithValue(elevatedCtx, bpmn.BPMNElevatedContextKey, true)
	// Threshold 2 with only one of the two approvers voting below keeps the
	// counter-sign status "pending" rather than "approved" — this test is
	// only about the vote itself succeeding (i.e. not being wrongly denied
	// by authorizeCounterSignViewer), not about exercising the downstream
	// parent-task-completion path (which needs a real deployed BPMN
	// definition that this lightweight fixture doesn't provide).
	created, err := engine.TaskService().CreateCounterSignTasks(elevatedCtx, parentTask.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID), fmt.Sprintf("%d", approver2.ID)},
		Threshold:    2,
	})
	require.NoError(t, err)
	require.Len(t, created, 2)

	voteCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, approver.ID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().Vote(voteCtx, created[0].TaskID, &VoteRequest{Approved: true, Comment: "real vote"})
	require.NoError(t, err, "a counter-sign approver who is not the original parent task's assignee must still be able to vote")
}

func TestGetTaskByID_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "viewer-nouser1")

	// 没有 BPMNUserIDContextKey，也没有 BPMNSystemCallerContextKey：必须拒绝，
	// 不再是旧约定里的"没有用户就放行"。
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.TaskService().GetTaskByID(ctx, taskID)
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied by default")
}

func TestGetTaskByID_ExplicitSystemCallerAllowed(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "viewer-syscaller1")

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err, "an explicitly declared system caller must be permitted")
	assert.Equal(t, taskID, task.ID)
}

func TestGetCounterSignStatus_NoUserNoSystemCallerDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, parentTaskID := createProcessFixture(t, engine, tenantID, "countersign-nouser1")
	parentTask, err := engine.client.ProcessTask.Get(context.Background(), parentTaskID)
	require.NoError(t, err)

	elevatedCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	elevatedCtx = context.WithValue(elevatedCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	elevatedCtx = context.WithValue(elevatedCtx, bpmn.BPMNElevatedContextKey, true)
	_, err = engine.TaskService().CreateCounterSignTasks(elevatedCtx, parentTask.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", actorID)},
		Threshold:    1,
	})
	require.NoError(t, err)

	// 没有用户、没有系统调用声明、也没有提权：必须拒绝——这条断言同时证明
	// authorizeCounterSignViewer 继承了 authorizeTaskViewer 的新默认行为。
	noAuthCtx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	_, err = engine.TaskService().GetCounterSignStatus(noAuthCtx, parentTask.TaskID)
	assert.Error(t, err, "no user and no explicit system-caller declaration must be denied for counter-sign status too")
}
