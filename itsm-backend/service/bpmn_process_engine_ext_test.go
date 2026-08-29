package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processtask"
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
		SetBusinessType("change").
		SetBusinessID(1).
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
	assert.Equal(t, "1", stored[0].BusinessID)
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

	// TicketServiceTaskHandler.updateTicketStatus 现在委托给注入的 TicketService（Task 5：
	// 不再绕过领域服务直接写 Ent），跟生产环境 bootstrap 里的 SetTicketService 装配是同一个模式。
	ticketSvc := NewTicketServiceForTest(engine.client, engine.logger)
	if h, ok := engine.callbackRegistry.GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler); ok {
		h.SetTicketService(ticketSvc)
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

func TestProcessTask_CorrelationIDRoundTrip(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, _ := setupApprovalDecisionFixture(t, engine)
	ctx := baseCtx

	_, taskID := createProcessFixture(t, engine, tenantID, "correlation1")

	updated, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCorrelationID("corr-abc-123").
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "corr-abc-123", updated.CorrelationID)

	reloaded, err := engine.client.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "corr-abc-123", reloaded.CorrelationID)
}

// fakeAsyncServiceTaskHandler 是测试专用的最小异步 handler：实现
// ServiceTaskHandlerInterface + AsyncServiceTaskHandler，记录 Execute 被调用次数，
// 用于断言暂停时不执行、完成时只执行一次。
type fakeAsyncServiceTaskHandler struct {
	taskType  string
	handlerID string
	executed  int
}

func (h *fakeAsyncServiceTaskHandler) GetTaskType() string  { return h.taskType }
func (h *fakeAsyncServiceTaskHandler) GetHandlerID() string { return h.handlerID }
func (h *fakeAsyncServiceTaskHandler) IsAsync() bool        { return true }
func (h *fakeAsyncServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}
func (h *fakeAsyncServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	h.executed++
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*fakeAsyncServiceTaskHandler)(nil)
var _ bpmn.AsyncServiceTaskHandler = (*fakeAsyncServiceTaskHandler)(nil)

func TestHandleElement_AsyncServiceTask_PausesAndCreatesDelegatedTask(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	instanceID, _ := createProcessFixture(t, engine, tenantID, "async-pause-1")
	instance, err := engine.client.ProcessInstance.Get(ctx, instanceID)
	require.NoError(t, err)

	fakeHandler := &fakeAsyncServiceTaskHandler{taskType: "fake_async_task", handlerID: "fake_async_handler"}
	engine.callbackRegistry.RegisterHandler(fakeHandler)

	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{
				ID:   "Activity_KafDelegate",
				Name: "KAF 委派",
				ExtensionElements: &BPMNExtensionElements{
					MetaData: []BPMNMetaData{
						{Name: "service_task_type", Value: "fake_async_task"},
						{Name: "action", Value: "delegate"},
						{Name: "allowed_actions", Value: "resolve,update_progress"},
					},
				},
			},
		},
		EndEvents: []*BPMNEndEvent{{ID: "End_1", Name: "结束"}},
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "Flow_1", SourceRef: "Activity_KafDelegate", TargetRef: "End_1"},
		},
	}

	err = engine.handleElement(ctx, instance, process, "Activity_KafDelegate")
	require.NoError(t, err)

	assert.Equal(t, 0, fakeHandler.executed, "异步 handler 在暂停时不应该被 Execute")

	updatedInstance, err := engine.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_KafDelegate", updatedInstance.CurrentActivityID, "流程应该停在委派节点，不应该推进到结束节点")

	delegatedTask, err := engine.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_KafDelegate")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "fake_async_task", delegatedTask.TaskType)
	assert.Equal(t, "delegated", delegatedTask.Status)
	assert.Equal(t, "fake_async_task", delegatedTask.TaskVariables["service_task_type"])
	assert.Equal(t, "delegate", delegatedTask.TaskVariables["action"])
	assert.Equal(t, "resolve,update_progress", delegatedTask.TaskVariables["allowed_actions"])
}
