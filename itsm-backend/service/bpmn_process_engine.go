package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processdeployment"
	"itsm-backend/ent/processexecutionhistory"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/user"
	"itsm-backend/ent/workflowtask"
	"itsm-backend/service/approver"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

// ProcessEngine BPMN流程引擎核心接口
type ProcessEngine interface {
	// 流程定义管理
	ProcessDefinitionService() ProcessDefinitionService
	// 流程实例管理
	ProcessInstanceService() ProcessInstanceService
	// 任务管理
	TaskService() TaskService
	// 流程执行
	StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, variables map[string]interface{}) (*ent.ProcessInstance, error)
	CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error
	SuspendProcess(ctx context.Context, processInstanceID string, reason string) error
	ResumeProcess(ctx context.Context, processInstanceID string) error
	TerminateProcess(ctx context.Context, processInstanceID string, reason string) error
}

// ProcessDefinitionService 流程定义服务接口
type ProcessDefinitionService interface {
	CreateProcessDefinition(ctx context.Context, req *CreateProcessDefinitionRequest) (*ent.ProcessDefinition, error)
	GetProcessDefinition(ctx context.Context, key string, version string) (*ent.ProcessDefinition, error)
	GetProcessDefinitionByID(ctx context.Context, id int) (*ent.ProcessDefinition, error)
	GetLatestProcessDefinition(ctx context.Context, key string) (*ent.ProcessDefinition, error)
	UpdateProcessDefinition(ctx context.Context, key string, version string, req *UpdateProcessDefinitionRequest) (*ent.ProcessDefinition, error)
	DeleteProcessDefinition(ctx context.Context, key string, version string) error
	ListProcessDefinitions(ctx context.Context, req *ListProcessDefinitionsRequest) ([]*ent.ProcessDefinition, int, error)
	SetProcessDefinitionActive(ctx context.Context, key string, version string, active bool) error
}

// ProcessInstanceService 流程实例服务接口
type ProcessInstanceService interface {
	GetProcessInstance(ctx context.Context, processInstanceID string) (*ent.ProcessInstance, error)
	ListProcessInstances(ctx context.Context, req *ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error)
	GetProcessInstanceVariables(ctx context.Context, processInstanceID string) (map[string]interface{}, error)
	SetProcessInstanceVariables(ctx context.Context, processInstanceID string, variables map[string]interface{}) error
	GetProcessInstanceHistory(ctx context.Context, processInstanceID string) ([]*ent.ProcessExecutionHistory, error)
	GetInstanceStatistics(ctx context.Context, req *InstanceStatisticsRequest) (*InstanceStatistics, error)
}

// TaskService 任务管理服务接口
type TaskService interface {
	GetTask(ctx context.Context, taskID string) (*ent.ProcessTask, error)
	GetTaskByID(ctx context.Context, id int) (*ent.ProcessTask, error)
	CompleteTaskByID(ctx context.Context, id int, variables map[string]interface{}) error
	ClaimTask(ctx context.Context, taskID string, userID string) error
	ClaimTaskByID(ctx context.Context, id int, userID int) error
	ListUserTasks(ctx context.Context, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error)
	ListUserTaskViews(ctx context.Context, req *ListUserTasksRequest) ([]*dto.BPMNTaskResponse, int, error)
	AssignTask(ctx context.Context, taskID string, assignee string) error
	CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error
	CancelTask(ctx context.Context, taskID string, reason string) error
	GetTaskVariables(ctx context.Context, taskID string) (map[string]interface{}, error)
	SetTaskVariables(ctx context.Context, taskID string, variables map[string]interface{}) error
	HandleTaskTimeout(ctx context.Context, taskID string) error
	RetryTask(ctx context.Context, taskID string, maxRetries int) error
	DelegateTask(ctx context.Context, taskID string, newAssignee string) error
	EscalateTask(ctx context.Context, taskID string, reason string) error
	BatchAssignTasks(ctx context.Context, taskIDs []string, assignee string) error
	GetTaskStatistics(ctx context.Context, req *TaskStatisticsRequest) (*TaskStatistics, error)
	ListApprovalDecisions(ctx context.Context, processInstanceKey string) ([]*ent.ProcessApprovalDecision, error)
	// 会签相关
	CreateCounterSignTasks(ctx context.Context, parentTaskID string, req *CounterSignRequest) ([]*ent.ProcessTask, error)
	GetCounterSignStatus(ctx context.Context, parentTaskID string) (*CounterSignStatus, error)
	Vote(ctx context.Context, taskID string, req *VoteRequest) error
}

// CustomProcessEngine 是ProcessEngine接口的实现
// 充当领域服务(Domain Service)，协调流程定义、实例和任务实体的生命周期
type CustomProcessEngine struct {
	client           *ent.Client
	logger           *zap.SugaredLogger
	parser           *BPMNParser            // 使用自定义的BPMN解析器
	exprEngine       *ExpressionEngine      // 表达式引擎
	expressionVars   map[string]interface{} // 表达式变量
	callbackRegistry *bpmn.CallbackRegistry // 服务任务回调注册中心
	groupResolver    *bpmn.GroupResolver    // 审批组解析器：candidateGroups → 候选用户
	// 内部服务
	processDefinitionService *bpmnProcessDefinitionService
	processInstanceService   *bpmnProcessInstanceService
	taskService              *bpmnTaskService
	// 审计服务
	auditService *BPMNAuditService
}

// NewCustomProcessEngine 创建自定义流程引擎实例
func NewCustomProcessEngine(client *ent.Client, logger *zap.SugaredLogger) ProcessEngine {
	engine := &CustomProcessEngine{
		client:           client,
		logger:           logger,
		parser:           NewBPMNParser(),
		exprEngine:       NewExpressionEngine(),
		expressionVars:   make(map[string]interface{}),
		callbackRegistry: bpmn.NewCallbackRegistry(client, logger),
		groupResolver:    bpmn.NewGroupResolver(client),
	}
	engine.processDefinitionService = &bpmnProcessDefinitionService{client: client, logger: logger}
	engine.processInstanceService = &bpmnProcessInstanceService{client: client, logger: logger}
	engine.auditService = NewBPMNAuditService(client, logger)
	engine.taskService = &bpmnTaskService{client: client, logger: logger, groupResolver: engine.groupResolver, auditService: engine.auditService}

	// 注册流程相关的内置函数
	engine.registerProcessFunctions()

	return engine
}

// registerProcessFunctions 注册流程相关的内置函数
func (e *CustomProcessEngine) registerProcessFunctions() {
	// 获取任务列表
	e.exprEngine.RegisterFunction("getTasks", func(ctx context.Context, assignee string) []interface{} {
		// 从数据库查询任务
		tasks, err := e.client.WorkflowTask.Query().
			Where(workflowtask.Assignee(assignee)).
			Where(workflowtask.CompletedAtIsNil()).
			All(ctx)
		if err != nil {
			e.logger.Warnw("Failed to query tasks", "error", err)
			return []interface{}{}
		}
		result := make([]interface{}, len(tasks))
		for i, task := range tasks {
			result[i] = map[string]interface{}{
				"id":          task.TaskID,
				"name":        task.Name,
				"instance_id": task.InstanceID,
			}
		}
		return result
	})

	// 获取用户信息
	e.exprEngine.RegisterFunction("getUser", func(userID interface{}) interface{} {
		return map[string]interface{}{
			"id":   userID,
			"name": "User " + fmt.Sprintf("%v", userID),
		}
	})

	// 获取当前时间
	e.exprEngine.RegisterFunction("currentTime", func() int64 {
		return time.Now().Unix()
	})

	// 日期计算
	e.exprEngine.RegisterFunction("addDays", func(timestamp int64, days int) int64 {
		return timestamp + int64(days*86400)
	})

	// 数组长度
	e.exprEngine.RegisterFunction("size", func(arr []interface{}) int {
		return len(arr)
	})

	// 随机数
	e.exprEngine.RegisterFunction("random", func(min, max float64) float64 {
		return min + (max-min)*float64(time.Now().UnixNano()%10000000)/10000000
	})
}

// ProcessDefinitionService 返回流程定义服务
func (e *CustomProcessEngine) ProcessDefinitionService() ProcessDefinitionService {
	return &bpmnProcessDefinitionService{client: e.client}
}

// ProcessInstanceService 返回流程实例服务
func (e *CustomProcessEngine) ProcessInstanceService() ProcessInstanceService {
	return &bpmnProcessInstanceService{client: e.client}
}

// TaskService 返回任务服务
func (e *CustomProcessEngine) TaskService() TaskService {
	return &bpmnTaskService{client: e.client, logger: e.logger, groupResolver: e.groupResolver, auditService: e.auditService}
}

// StartProcess 启动流程实例
func (e *CustomProcessEngine) StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, variables map[string]interface{}) (*ent.ProcessInstance, error) {
	// 1. 获取租户ID
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)

	// 2. 获取流程定义
	query := e.client.ProcessDefinition.Query().
		Where(processdefinition.Key(processDefinitionKey)).
		Where(processdefinition.IsActive(true)).
		Where(processdefinition.IsLatest(true))
	if tenantID > 0 {
		query = query.Where(processdefinition.TenantID(tenantID))
	}
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	// 3. 解析BPMN
	bpmnDefinitions, err := e.parser.ParseXML(definition.BpmnXML)
	if err != nil {
		return nil, fmt.Errorf("解析BPMN失败: %w", err)
	}

	if len(bpmnDefinitions.Processes) == 0 {
		return nil, fmt.Errorf("BPMN中未找到流程定义")
	}
	process := bpmnDefinitions.Processes[0]

	// 3. 找到开始事件
	if len(process.StartEvents) == 0 {
		return nil, fmt.Errorf("流程缺少开始事件")
	}
	startEvent := process.StartEvents[0]

	// 4. 创建流程实例
	// initiator 取自当前认证用户（controller 的 getBPMNTenantContext 注入的
	// BPMNUserIDContextKey）；系统触发（无认证用户上下文，例如工单创建自动触发
	// 流程）时，沿用现有的 requester_id 变量约定作为兜底。空字符串（两者都没有）
	// 是允许的——这类实例只能靠任务参与关系被参与者看到，不会出现在任何人的
	// "我发起的" 视图里。
	initiator := ""
	if callerID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); callerID > 0 {
		initiator = strconv.Itoa(callerID)
	} else if reqID, ok := variables["requester_id"]; ok {
		switch v := reqID.(type) {
		case float64:
			initiator = strconv.Itoa(int(v))
		case int:
			initiator = strconv.Itoa(v)
		case string:
			initiator = v
		}
	}

	instance, err := e.client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("PI-%s-%d", processDefinitionKey, time.Now().UnixNano())).
		SetBusinessKey(businessKey).
		SetProcessDefinitionKey(processDefinitionKey).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
		SetStartTime(time.Now()).
		SetTenantID(definition.TenantID).
		SetCurrentActivityID(startEvent.ID).
		SetCurrentActivityName(startEvent.Name).
		SetInitiator(initiator).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建流程实例失败: %w", err)
	}

	// 5. 执行流程推进（从StartEvent开始）
	// 平台级操作（ctx 无租户键：controller 的 getBPMNTenantContext 对 tenant_id=0
	// 不注入）此前会在带 RequireTenantID 的 ServiceTask 上硬失败。实例租户跟随流程
	// 定义（definition.TenantID 是 Positive 校验过的权威值），把它注入 ctx 后 handler
	// 的写侧 Where(TenantID) 仍然生效，不放松任何约束——伪造的 variables["tenant_id"]
	// 只会导致写不中行。
	if tenantID <= 0 {
		ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, definition.TenantID)
	}
	if err := e.executeStep(ctx, instance, process, startEvent.ID, variables); err != nil {
		return nil, err
	}

	// 6. 记录审计日志 - 流程启动
	// 从context中获取用户信息
	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	if err := e.auditService.RecordProcessStarted(ctx, instance, userID, userName, variables); err != nil {
		e.logger.Warnw("audit record failed", "error", err)
	}

	return instance, nil
}

// CompleteTask 完成任务（使用乐观锁保护变量合并，防止并发覆写）
func (e *CustomProcessEngine) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	// 1. 获取任务
	taskQuery := e.client.ProcessTask.Query().
		Where(processtask.TaskID(taskID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		taskQuery = taskQuery.Where(processtask.TenantID(tenantID))
	}
	task, err := taskQuery.First(ctx)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	if err := e.authorizeTaskActor(ctx, task); err != nil {
		return err
	}

	// 2. 获取流程实例 - 使用任务中存储的ProcessInstanceID (ent自动生成的ID)
	instance, err := e.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	if err != nil {
		return fmt.Errorf("获取流程实例失败: %w", err)
	}

	// 2.5 平台级操作（ctx 无租户键：controller 的 getBPMNTenantContext 对 tenant_id=0
	// 不注入）恢复可用：注入实例租户作为 handler 执行租户。此前 dispatchUserTaskCallback
	// 里的 RequireTenantID 会失败，业务副作用被静默跳过。实例租户由启动时的
	// definition.TenantID 而来（Positive 校验过的权威值），注入它只补全租户上下文，
	// 写侧 Where(TenantID) 仍是安全边界。任务查询（第 1 步）在注入前执行，
	// 平台视角的跨租户查询行为与之前一致。
	if ctxTenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); ctxTenantID <= 0 {
		ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, instance.TenantID)
	}

	// 3. 获取流程定义并解析
	// A running instance is immutable with respect to its deployed definition.
	// Looking up the latest definition here can silently move an old instance
	// onto a newly published graph halfway through execution.
	definition, err := e.client.ProcessDefinition.Query().
		Where(
			processdefinition.ID(instance.ProcessDefinitionID),
			processdefinition.TenantID(instance.TenantID),
		).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("获取流程定义失败: %w", err)
	}

	bpmnDefinitions, err := e.parser.ParseXML(definition.BpmnXML)
	if err != nil {
		return fmt.Errorf("解析BPMN失败: %w", err)
	}
	process := bpmnDefinitions.Processes[0]

	// 4. 更新当前任务状态
	if task.Status == "completed" || task.Status == "cancelled" {
		return fmt.Errorf("任务已结束，不能重复完成")
	}

	updated, err := e.client.ProcessTask.Update().
		Where(
			processtask.ID(task.ID),
			processtask.TenantID(instance.TenantID),
			processtask.StatusNEQ("completed"),
			processtask.StatusNEQ("cancelled"),
		).
		SetStatus("completed").
		SetCompletedTime(time.Now()).
		SetTaskVariables(variables).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("任务已被处理，请刷新后重试")
	}

	// 5. 使用乐观锁合并变量（最多重试3次）
	instance, err = e.mergeVariablesWithOptimisticLock(ctx, instance.ID, variables)
	if err != nil {
		return fmt.Errorf("合并实例变量失败: %w", err)
	}

	// 6. 执行流程推进（从当前UserTask继续）
	if err := e.executeStep(ctx, instance, process, task.TaskDefinitionKey, instance.Variables); err != nil {
		return err
	}
	if err := e.recordApprovalDecision(ctx, instance, task, variables); err != nil {
		return err
	}

	// 6.5 UserTask 声明了 service_task_type/action metaData 时（比如变更流程的
	// Activity_CABApproval、Activity_Schedule 等节点），完成后要走跟 ServiceTask
	// 一样的 callback registry 分发——这些节点在 BPMN 图上是 UserTask（需要人工操作
	// 触发完成），但完成后的业务副作用（更新 Change.Status 等）复用同一套
	// ChangeServiceTaskHandler/TicketServiceTaskHandler 实现，不新写一套分发机制。
	//
	// 注意 task 是步骤 1 读出来的快照，它的 TaskVariables 仍是 createUserTask 写入的
	// taskConfig（含 metaData）；步骤 4 的 SetTaskVariables(variables) 已经把库里那行
	// 覆盖成完成时提交的变量了，所以这里必须用快照读，不能重新查库。
	//
	// 传 instance.Variables（步骤 5 合并落库后的最新值）而不是本次调用的原始 variables：
	// 启动流程时注入的 change_id/business_id 等字段存在实例变量里，不会随每次任务完成
	// 请求重复携带。只在调用方显式传参时才够用的话，走"审批中心"通用决策接口（不知道
	// 也不该关心具体业务字段名）完成这类 UserTask 时，handler 拿到的 change_id 就是 0，
	// 报"无效的变更ID"——业务副作用被吞掉但流程 token 已经往前走了，状态跟着悬空。
	// action 仍然优先取 task 自身 metaData（dispatchUserTaskCallback 内部处理），不受此影响。
	e.dispatchUserTaskCallback(ctx, task, instance.Variables)

	// 7. 记录审计日志 - 任务完成
	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	variablesBefore := task.TaskVariables
	if err := e.auditService.RecordTaskCompleted(ctx, task, userID, userName, variablesBefore, variables); err != nil {
		e.logger.Warnw("audit record failed", "error", err)
	}

	return nil
}

// dispatchUserTaskCallback 在用户任务完成后，按其 service_task_type metaData 找到对应的
// ServiceTaskHandler 并执行，把"人工完成节点"的业务副作用交给已有的 handler 实现。
//
// 只有模板显式声明了 service_task_type 的 UserTask 才会走到这里，未声明的普通用户任务
// （绝大多数流程）完全不受影响。
//
// 传给 handler 的变量刻意只取"完成任务时提交的 variables" + metaData 里的 action，
// 不把 instance.Variables 整个合进去。原因是 ProcessTriggerService 会把 business_id
// 写进实例变量，而 TicketServiceTaskHandler 正是按 business_id 取工单、按 action
// 改状态的——一旦合并实例变量，像 ticket_general_flow 的 Activity_Handle/Activity_Resolve
// （action=update_status）这类节点会在任何一次人工完成时把工单状态强制改成默认的
// in_progress，等于凭空回退业务状态。让调用方显式传业务 ID 才触发副作用，
// 是这里唯一安全的默认值。
//
// 失败只告警不阻断：走到这一步时任务已置为 completed、流程也已推进，返回错误既回滚不了
// 也会诱导调用方重试（重试会撞上"任务已结束，不能重复完成"）。副作用失败留待告警与审计追踪。
func (e *CustomProcessEngine) dispatchUserTaskCallback(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) {
	if e.callbackRegistry == nil || task == nil {
		return
	}
	serviceTaskType, _ := task.TaskVariables[bpmnMetaDataServiceTaskType].(string)
	if serviceTaskType == "" {
		return
	}

	handler := e.findHandlerByTaskType(serviceTaskType)
	if handler == nil {
		// 模板声明了类型但没有注册对应 handler（例如 release_task），按 ServiceTask
		// 分支的既有约定视为 NoOp，仅告警不阻断流程。
		e.logger.Warnw("UserTask 声明的 service_task_type 没有注册处理器，跳过回调",
			"taskID", task.TaskID, "serviceTaskType", serviceTaskType)
		return
	}

	callbackVars := make(map[string]interface{}, len(variables)+1)
	for k, v := range variables {
		callbackVars[k] = v
	}
	// metaData 的 action 是节点固有语义，优先级高于调用方传入的同名变量，
	// 避免外部请求体伪造 action 让节点执行别的业务动作。
	if action, ok := task.TaskVariables[bpmnMetaDataAction].(string); ok && action != "" {
		callbackVars[bpmnMetaDataAction] = action
	}

	if _, err := handler.Execute(ctx, task, callbackVars); err != nil {
		e.logger.Warnw("UserTask 完成后回调执行失败",
			"taskID", task.TaskID, "taskDefinitionKey", task.TaskDefinitionKey,
			"serviceTaskType", serviceTaskType, "error", err)
		return
	}
	e.logger.Infow("UserTask 完成后回调执行成功",
		"taskID", task.TaskID, "taskDefinitionKey", task.TaskDefinitionKey,
		"serviceTaskType", serviceTaskType)
}

// findHandlerByTaskType 按 handler 的 GetTaskType() 查找处理器。
//
// 不能直接用 CallbackRegistry.GetHandler(serviceTaskType)：registry 的 map 是以
// handler.GetHandlerID()（如 "change_service_handler"）为键注册的，而 BPMN metaData 里写的是
// 任务类型（如 "change_task"），按 ID 查必然返回 nil。registry 内部的 getHandler 做了
// 类型兜底匹配但未导出，这里用导出的 ListHandlers 做等价匹配。
func (e *CustomProcessEngine) findHandlerByTaskType(taskType string) bpmn.ServiceTaskHandlerInterface {
	if e.callbackRegistry == nil || taskType == "" {
		return nil
	}
	// 先按 handler ID 精确匹配，兼容模板直接写 handler ID 的情况。
	if handler := e.callbackRegistry.GetHandler(taskType); handler != nil {
		return handler
	}
	for _, handler := range e.callbackRegistry.ListHandlers() {
		if handler.GetTaskType() == taskType {
			return handler
		}
	}
	return nil
}

func (e *CustomProcessEngine) recordApprovalDecision(ctx context.Context, instance *ent.ProcessInstance, task *ent.ProcessTask, variables map[string]interface{}) error {
	action, _ := variables["approvalAction"].(string)
	if action == "" {
		return nil
	}
	decision, _ := variables["approvalResult"].(string)
	comment, _ := variables["approvalComment"].(string)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if actorID <= 0 {
		return fmt.Errorf("审批决策缺少认证操作人")
	}
	actorName := ""
	if actor, err := e.client.User.Get(ctx, actorID); err == nil {
		actorName = actor.Name
	}
	businessType := fmt.Sprint(instance.Variables["business_type"])
	businessID := fmt.Sprint(instance.Variables["business_id"])
	_, err := e.client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID(task.TaskID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetNodeKey(task.TaskDefinitionKey).
		SetBusinessType(businessType).SetBusinessID(businessID).
		SetActorID(actorID).SetActorName(actorName).SetAction(action).SetDecision(decision).
		SetComment(comment).SetVariablesSnapshot(variables).SetTenantID(instance.TenantID).Save(ctx)
	if err != nil {
		return fmt.Errorf("记录审批决策失败: %w", err)
	}
	return nil
}

// authorizeTaskActor ensures that task actions are performed by the assigned
// user, an explicitly resolved candidate (by ID, username, email, or
// candidate_groups membership), or an explicitly declared system caller
// (bpmn.BPMNSystemCallerContextKey). candidate_groups matches must
// additionally pass isProcessInstanceRequester exclusion, since (unlike
// candidate_users) candidate_groups isn't pre-filtered for self-approval at
// task-creation time — see bpmn.CallerIdentity.MatchesCandidateGroup's doc
// comment. A call with no authenticated actor and no system-caller
// declaration is denied — replaces the previous implicit permissive default.
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, e.client, e.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("审批用户不存在: %w", err)
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, rErr := isProcessInstanceRequester(ctx, e.client, task.ProcessInstanceID, tenantID, userID)
		if rErr != nil {
			return fmt.Errorf("校验申请人身份失败: %w", rErr)
		}
		if !isRequester {
			return nil
		}
	}
	return fmt.Errorf("当前用户不是该任务的审批人或候选人")
}

// isProcessInstanceRequester reports whether userID is the requester_id
// recorded on the process instance owning this task (instance.Variables,
// the same source loadApprovalRequester already reads). Used to exclude the
// requester from candidate_groups-based action authority — unlike
// candidate_users, candidate_groups is not pre-filtered at task-creation
// time (see bpmn.CallerIdentity.MatchesCandidateGroup's doc comment), so
// without this check a requester who happens to belong to the configured
// approval group could bypass the self-approval prevention that
// candidate_users filtering already enforces. Tenant-scoped per the plan's
// Global Constraints for ProcessInstance/ProcessTask queries.
func isProcessInstanceRequester(ctx context.Context, client *ent.Client, processInstanceID, tenantID, userID int) (bool, error) {
	instance, err := client.ProcessInstance.Query().
		Where(processinstance.ID(processInstanceID), processinstance.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return false, fmt.Errorf("查询流程实例失败: %w", err)
	}
	reqIDRaw, ok := instance.Variables["requester_id"]
	if !ok {
		return false, nil
	}
	var reqID int
	switch v := reqIDRaw.(type) {
	case float64:
		reqID = int(v)
	case int:
		reqID = v
	case string:
		reqID, _ = strconv.Atoi(v)
	}
	return reqID > 0 && reqID == userID, nil
}

// mergeVariablesWithOptimisticLock 使用乐观锁合并流程实例变量，防止并发覆写
func (e *CustomProcessEngine) mergeVariablesWithOptimisticLock(ctx context.Context, instanceID int, newVars map[string]interface{}) (*ent.ProcessInstance, error) {
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		tx, err := e.client.Tx(ctx)
		if err != nil {
			return nil, fmt.Errorf("开启事务失败: %w", err)
		}

		// 在事务内读取最新实例
		inst, err := tx.Client().ProcessInstance.Get(ctx, instanceID)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("查询流程实例失败: %w", err)
		}

		// 合并变量
		merged := make(map[string]interface{})
		if inst.Variables != nil {
			for k, v := range inst.Variables {
				merged[k] = v
			}
		}
		for k, v := range newVars {
			merged[k] = v
		}

		// 带版本号条件更新（乐观锁）
		count, err := tx.Client().ProcessInstance.Update().
			Where(
				processinstance.ID(instanceID),
				processinstance.Version(inst.Version), // 乐观锁条件
			).
			SetVariables(merged).
			SetVersion(inst.Version + 1).
			Save(ctx)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("更新实例变量失败: %w", err)
		}

		if count > 0 {
			// 更新成功，提交事务
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("提交事务失败: %w", err)
			}
			// 返回更新后的实例（包含新变量）
			inst.Variables = merged
			inst.Version = inst.Version + 1
			return inst, nil
		}

		// count == 0，版本冲突，回滚并重试
		tx.Rollback()
		e.logger.Infow("变量更新版本冲突，重试", "attempt", attempt+1, "instance_id", instanceID)
	}

	return nil, fmt.Errorf("变量更新冲突，已重试%d次", maxRetries)
}

// executeStep 执行流程步骤
func (e *CustomProcessEngine) executeStep(ctx context.Context, instance *ent.ProcessInstance, process *BPMNProcess, currentElementID string, variables map[string]interface{}) error {
	outgoingFlows := e.findOutgoingFlows(process, currentElementID)

	if len(outgoingFlows) == 0 {
		if e.isEndEvent(process, currentElementID) {
			return e.completeProcess(ctx, instance)
		}
		return nil
	}

	var targetRef string
	unconditionalCount := 0
	for _, flow := range outgoingFlows {
		if e.evaluateCondition(flow, variables) {
			if targetRef == "" {
				targetRef = flow.TargetRef
			}
			if flow.ConditionExpression == nil || flow.ConditionExpression.Expression == "" {
				unconditionalCount++
			}
		}
	}

	if targetRef == "" {
		return fmt.Errorf("没有符合条件的路径")
	}

	// 多个无条件分支同时命中 → BPMN 建模警告，取第一条
	if unconditionalCount > 1 {
		e.logger.Warnw("多个无条件分支同时匹配，已取第一条（请检查BPMN流程：应为排他网关添加条件表达式）",
			"element_id", currentElementID,
			"unconditional_count", unconditionalCount,
			"selected_target", targetRef,
			"instance_id", instance.ProcessInstanceID,
		)
	}

	return e.handleElement(ctx, instance, process, targetRef)
}

func (e *CustomProcessEngine) handleElement(ctx context.Context, instance *ent.ProcessInstance, process *BPMNProcess, elementID string) error {
	// Find the element name for logging
	elementName := elementID
	if task := e.findUserTask(process, elementID); task != nil {
		elementName = task.Name
	} else if endEvent := e.findEndEvent(process, elementID); endEvent != nil {
		elementName = endEvent.Name
	}

	_, err := e.client.ProcessInstance.UpdateOne(instance).
		SetCurrentActivityID(elementID).
		SetCurrentActivityName(elementName).
		Save(ctx)
	if err != nil {
		return err
	}

	// Debug: log element info
	e.logger.Debugw("handleElement called", "elementID", elementID, "elementName", elementName, "userTasksCount", len(process.UserTasks))

	if task := e.findUserTask(process, elementID); task != nil {
		e.logger.Infow("Found user task, creating task", "taskID", task.ID, "taskName", task.Name)
		return e.createUserTask(ctx, instance, task)
	} else if endEvent := e.findEndEvent(process, elementID); endEvent != nil {
		return e.completeProcess(ctx, instance)
	} else if gateway := e.findExclusiveGateway(process, elementID); gateway != nil {
		return e.executeStep(ctx, instance, process, elementID, instance.Variables)
	} else if serviceTask := e.findServiceTask(process, elementID); serviceTask != nil {
		// 优先按 metaData 里的 service_task_type/action 分发——跟 UserTask 走
		// dispatchUserTaskCallback 时用的是同一套 findHandlerByTaskType 查找口径，
		// 保证"模板声明了 service_task_type 就一定能找到对应 handler"这条规则
		// 在 UserTask 和 ServiceTask 两种节点类型上表现一致。
		if serviceTaskType := serviceTask.ServiceTaskType(); serviceTaskType != "" {
			if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
				callbackVars := mergeServiceTaskVariables(instance.Variables, serviceTask)
				if action := serviceTask.ServiceTaskAction(); action != "" {
					callbackVars[bpmnMetaDataAction] = action
				}
				e.logger.Infow("执行 ServiceTask 回调（metaData 分发）", "serviceTaskType", serviceTaskType, "elementID", elementID)
				if _, err := handler.Execute(ctx, nil, callbackVars); err != nil {
					return fmt.Errorf("ServiceTask %s 执行失败: %w", elementID, err)
				}
				return e.executeStep(ctx, instance, process, elementID, instance.Variables)
			}
			// 声明了类型但没有注册对应 handler（比如未来新增了类型但忘了注册）：
			// 按既有约定视为 NoOp，只告警不阻断流程，跟 dispatchUserTaskCallback
			// 遇到同样情况时的处理方式保持一致。
			e.logger.Warnw("ServiceTask 声明的 service_task_type 没有注册处理器，跳过执行", "elementID", elementID, "serviceTaskType", serviceTaskType)
			return e.executeStep(ctx, instance, process, elementID, instance.Variables)
		}

		// 没有声明 metaData 时，保留原有按 implementation/class/expression/operationRef
		// 属性猜 handler ID 的兜底逻辑——这是历史行为，目前没有任何内置模板会走到这里
		// （全部改成了 metaData 声明），但不删除它，避免破坏可能存在的自定义模板。
		serviceRef := serviceTask.ID
		if serviceTask.Name != "" {
			serviceRef = serviceTask.Name
		}
		if serviceTask.Implementation != "" {
			serviceRef = serviceTask.Implementation
		} else if serviceTask.Class != "" {
			serviceRef = serviceTask.Class
		} else if serviceTask.DelegateExpression != "" {
			serviceRef = serviceTask.DelegateExpression
		} else if serviceTask.OperationRef != "" {
			serviceRef = serviceTask.OperationRef
		}

		if e.callbackRegistry != nil {
			handler := e.callbackRegistry.GetHandler(serviceRef)
			if handler == nil {
				handler = e.callbackRegistry.GetHandler(serviceTask.GetType())
			}
			if handler != nil {
				e.logger.Infow("执行 ServiceTask 回调", "serviceRef", serviceRef, "elementID", elementID)
				taskVariables := mergeServiceTaskVariables(instance.Variables, serviceTask)
				if _, err := handler.Execute(ctx, nil, taskVariables); err != nil {
					return fmt.Errorf("ServiceTask %s 执行失败: %w", serviceRef, err)
				}
			} else {
				e.logger.Warnw("未注册的 ServiceTask，跳过执行", "serviceRef", serviceRef, "elementID", elementID)
			}
		}
		return e.executeStep(ctx, instance, process, elementID, instance.Variables)
	}

	return e.executeStep(ctx, instance, process, elementID, instance.Variables)
}

func mergeServiceTaskVariables(instanceVariables map[string]interface{}, task *BPMNServiceTask) map[string]interface{} {
	variables := make(map[string]interface{}, len(instanceVariables)+12)
	for key, value := range instanceVariables {
		variables[key] = value
	}
	if task == nil {
		return variables
	}
	if task.Type != "" {
		variables["type"] = task.Type
	}
	if task.OperationRef != "" {
		variables["operationRef"] = task.OperationRef
	}
	if task.CCType != "" {
		variables["ccType"] = task.CCType
	}
	if task.CCUserIDs != "" {
		variables["ccUserIds"] = task.CCUserIDs
	}
	if task.CCGroupIDs != "" {
		variables["ccGroupIds"] = task.CCGroupIDs
	}
	if task.CCRoleIDs != "" {
		variables["ccRoleIds"] = task.CCRoleIDs
	}
	if task.CCVariable != "" {
		variables["ccVariable"] = task.CCVariable
	}
	if task.CCNotify != "" {
		variables["ccNotify"] = task.CCNotify
	}
	if task.NotifyChannels != "" {
		variables["notifyChannels"] = task.NotifyChannels
	}
	return variables
}

// approvalFallbackCandidateGroup 是 taskPurpose="approval" 任务在没有解析出部门负责人、
// 且 BPMN 也没有显式声明 candidateGroups 时使用的默认候选组名。租户需要在 /admin/groups
// 里创建这个组并配置至少 2 名成员，否则单人部门 + 单人组的组合会出现审批任务无人可领。
const approvalFallbackCandidateGroup = "ticket-approvers"

func (e *CustomProcessEngine) createUserTask(ctx context.Context, instance *ent.ProcessInstance, task *BPMNUserTask) error {
	// 自动分配逻辑：优先级 BPMN定义 > 流程变量(request/assignee) > 默认分配
	assignee := task.Assignee

	// 辅助函数：从变量中提取用户ID
	getUserID := func(key string) string {
		if v, ok := instance.Variables[key]; ok {
			switch val := v.(type) {
			case float64:
				// JSON numbers are float64
				if val > 0 {
					return strconv.FormatFloat(val, 'f', 0, 64)
				}
			case int:
				if val > 0 {
					return strconv.Itoa(val)
				}
			case string:
				if val != "" && val != "0" {
					return val
				}
			}
		}
		return ""
	}

	// taskPurpose="approval" 的任务需要申请人身份，用来：
	// 1) 解析申请人所在部门的负责人作为 assignee；
	// 2) 把申请人自己从 candidateGroups 展开出的候选人里剔除。
	// 非 approval 任务不需要，不做这次额外查询。
	var approvalRequester *ent.User
	if task.TaskPurpose == "approval" {
		approvalRequester = e.loadApprovalRequester(ctx, instance, getUserID)
	}

	// 角色查询解析出的候选人列表——跟 candidateGroups 展开不是同一条路径（角色不等于组），
	// 但最终都要合并进 candidate_users、排除申请人自己，所以先收集，稍后统一处理。
	var roleCandidates []string

	// 如果BPMN没有定义分配人，从流程变量中获取
	if assignee == "" {
		if task.TaskPurpose == "approval" {
			switch {
			case strings.TrimSpace(task.CandidateGroups) != "" || strings.TrimSpace(task.CandidateUsers) != "":
				// BPMN 已经显式声明了 candidateGroups/candidateUsers（比如 legacy 审批链迁移出来的
				// 按角色/组路由节点，见 legacy_approval_migration_service.go，或者流程设计器直接
				// 指定了候选人），说明这个节点的路由方式是配置驱动的，不触发下面任何自动解析——
				// 避免用一个跟节点配置无关的语义覆盖它。
			case strings.TrimSpace(task.AssigneeRole) != "":
				// 按角色查这个租户里所有 active 且该角色的用户，全部作为候选人（不是挑一个人
				// 直接指派）——同一个角色可能有多人，候选人列表让谁先领谁审批，不会因为引擎
				// 武断选中的那个人离职/请假就卡死。查询语义复用 approval_service.go
				// resolveApprover "role" 分支的过滤条件（RoleEQ + TenantID + Active）。
				candidates, err := e.resolveRoleCandidates(ctx, instance.TenantID, task.AssigneeRole)
				if err != nil {
					e.logger.Warnw("按角色解析候选审批人失败，转候选组兜底", "role", task.AssigneeRole, "error", err)
				} else {
					// 排除申请人自己必须在这里做（存入 roleCandidates 之前），而不是等到下面
					// 合并进 expandedCandidateUsers 的时候才做——下面的候选组兜底判断
					// （len(roleCandidates) == 0）要看到排除申请人之后的真实候选人数，否则
					// "角色唯一匹配到的人正好是申请人自己"这种情况会被误判为"有候选人"，
					// 不会触发 candidateGroups 兜底，最终任务 assignee/candidate_users/
					// candidate_groups 三者皆空，变成没人能处理的孤儿任务。
					if approvalRequester != nil {
						candidates = excludeUserFromCandidates(candidates, approvalRequester)
					}
					if len(candidates) == 0 {
						e.logger.Infow(
							"按角色未解析到候选审批人（该角色下无匹配用户，或唯一匹配是申请人本人已被排除），转候选组兜底",
							"role", task.AssigneeRole, "tenantID", instance.TenantID,
						)
					}
					roleCandidates = candidates
				}
			case task.AssigneeDeptId != 0 || task.AssigneeTeamId != 0 || task.AssigneeProjectId != 0 || task.AssigneeTempTeamId != 0:
				// BPMN 声明了固定范围的组织路由（部门/团队/项目/临时团队负责人，范围钉死在配置的
				// 具体 ID 上，不取申请人的）——四个 resolver 都是"至多解析出一个人"的形状，
				// 跟下面 default 分支的"申请人自己部门"解析方式一样，只是 appCtx 的范围来源不同。
				assignee = e.resolveFixedScopeAssignee(ctx, instance, approvalRequester, task)
				if assignee == "" {
					// 固定范围没配置/解析失败/解析出来是申请人自己——退到"申请人自己部门"这一级，
					// 而不是直接跳到候选组兜底，保持跟原有优先级链兼容。
					assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
				}
			case task.AssigneeGmChain:
				// 沿申请人自己的真实汇报链找总经理，矩阵组织下天然按人（业务线）区分，
				// 跟上面的固定组织范围路由是两种不同语义，互斥（BPMN 设计器保证不会
				// 同时声明 assigneeDeptId 和 assigneeGmChain）。
				assignee = e.resolveGmChainAssignee(ctx, instance, approvalRequester)
				if assignee == "" {
					assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
				}
			default:
				// 都没声明：解析申请人自己所在部门的负责人（这次会话早前已经做的部分）
				assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
			}
		} else {
			// 优先使用 requester_id（工单申请人）
			assignee = getUserID("requester_id")
			// 其次使用 triggered_by（触发者）
			if assignee == "" {
				assignee = getUserID("triggered_by")
			}
			// 再其次使用 assignee_id
			if assignee == "" {
				assignee = getUserID("assignee_id")
			}
			// 如果还是没有，根据任务名称自动分配
			if assignee == "" {
				assignee = e.getDefaultAssigntee(ctx, instance, task)
			}
		}
	}

	// 审批任务如果自动解析都没有产出结果（部门负责人解析失败/角色查询没找到候选人/BPMN
	// 也没有声明 candidateGroups），兜底用固定候选组，保证任务始终有机会被领取。
	candidateGroupsToExpand := task.CandidateGroups
	if task.TaskPurpose == "approval" && assignee == "" && len(roleCandidates) == 0 && strings.TrimSpace(candidateGroupsToExpand) == "" {
		candidateGroupsToExpand = approvalFallbackCandidateGroup
	}

	// 展开 candidateGroups 为具体用户，合并到 candidate_users。
	// 这样「我的待办」接口才有可能查到分配给我的任务。
	expandedCandidateUsers := task.CandidateUsers
	if e.groupResolver != nil && strings.TrimSpace(candidateGroupsToExpand) != "" {
		_, groupUsernames, err := e.groupResolver.ExpandGroupsToUsers(ctx, instance.TenantID, candidateGroupsToExpand)
		if err != nil {
			// 解析失败：记录警告但不阻塞流程，以免审批组配置漂移导致整个流程中断
			e.logger.Warnw(
				"审批组展开失败，继续仅使用 BPMN candidateUsers",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"error", err,
			)
		} else {
			if task.TaskPurpose == "approval" && approvalRequester != nil {
				groupUsernames = excludeUserFromCandidates(groupUsernames, approvalRequester)
			}
			expandedCandidateUsers = e.groupResolver.MergeCandidateUsers(task.CandidateUsers, groupUsernames)
			e.logger.Infow(
				"审批组已展开",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"expandedUsers", groupUsernames,
			)
		}
	}
	if task.TaskPurpose == "approval" && len(roleCandidates) > 0 {
		// 按角色查出来的候选人，合并进 candidate_users——跟 candidateGroups 展开是互斥的两条
		// 路径（见上面的 switch），这里不会重复合并同一批人。申请人自己已经在上面的 switch
		// case 里排除过了，这里不需要再排除一次。
		expandedCandidateUsers = e.groupResolver.MergeCandidateUsers(expandedCandidateUsers, roleCandidates)
		e.logger.Infow(
			"按角色的候选审批人已展开",
			"taskID", task.ID,
			"role", task.AssigneeRole,
			"expandedUsers", roleCandidates,
		)
	}
	if task.TaskPurpose == "approval" && assignee == "" && strings.TrimSpace(expandedCandidateUsers) == "" {
		e.logger.Warnw(
			"审批任务没有解析到任何审批人（自动分配全部失败，候选组/候选角色展开后也为空），任务将无人可领",
			"taskID", task.ID,
			"taskName", task.Name,
			"candidateGroups", candidateGroupsToExpand,
		)
	}

	// Use instance.ID (auto-generated integer) for the relationship
	taskConfig := map[string]interface{}{
		"taskPurpose": task.TaskPurpose, "approvalMode": task.ApprovalMode,
		"approvalThreshold": task.ApprovalThreshold, "rejectStrategy": task.RejectStrategy,
		"timeoutAction": task.TimeoutAction, "allowDelegate": task.AllowDelegate,
		"allowAddApprover":        task.AllowAddApprover,
		"commentRequiredOnReject": task.CommentRequiredOnReject,
	}
	// service_task_type/action 来自 <bpmn:extensionElements> 的 metaData，只有模板真的
	// 声明了才写入——CompleteTask 靠这两个 key 是否存在来决定要不要走回调分发，
	// 无条件写空串会让"未声明"和"声明为空"无法区分。
	if serviceTaskType := task.ServiceTaskType(); serviceTaskType != "" {
		taskConfig[bpmnMetaDataServiceTaskType] = serviceTaskType
		if action := task.ServiceTaskAction(); action != "" {
			taskConfig[bpmnMetaDataAction] = action
		}
	}
	createdTask, err := e.client.ProcessTask.Create().
		SetTaskID(fmt.Sprintf("TASK-%s-%d", task.ID, time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey(task.ID).
		SetTaskName(task.Name).
		SetTaskType("user_task").
		SetStatus("created").
		SetAssignee(assignee).
		SetCandidateUsers(expandedCandidateUsers).
		SetCandidateGroups(candidateGroupsToExpand).
		SetFormKey(task.FormKey).
		SetTaskVariables(taskConfig).
		SetTenantID(instance.TenantID).
		SetCreatedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("创建用户任务失败: %w", err)
	}
	if task.TaskPurpose == "approval" && task.ApprovalMode != "" && task.ApprovalMode != "single" {
		approvers := splitNonEmptyCSV(expandedCandidateUsers)
		if len(approvers) > 1 {
			threshold := task.ApprovalThreshold
			switch task.ApprovalMode {
			case "any":
				threshold = 1
			case "all", "sequential":
				threshold = len(approvers)
			}
			approvalType := "parallel"
			if task.ApprovalMode == "sequential" {
				approvalType = "serial"
			}
			if _, err := e.taskService.CreateCounterSignTasks(ctx, createdTask.TaskID, &CounterSignRequest{ApprovalType: approvalType, Approvers: approvers, Threshold: threshold}); err != nil {
				return fmt.Errorf("创建会签任务失败: %w", err)
			}
		}
	}
	e.logger.Infow("User task created with auto-assignment", "taskID", task.ID, "taskName", task.Name, "assignee", assignee)
	return nil
}

// loadApprovalRequester 加载 taskPurpose="approval" 任务对应流程实例的申请人（requester_id
// 流程变量指向的 User），找不到时返回 nil（调用方会退化到候选组兜底路径，不会报错阻塞流程）。
func (e *CustomProcessEngine) loadApprovalRequester(ctx context.Context, instance *ent.ProcessInstance, getUserID func(string) string) *ent.User {
	idStr := getUserID("requester_id")
	if idStr == "" {
		return nil
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return nil
	}
	requester, err := e.client.User.Query().
		Where(user.IDEQ(id), user.TenantIDEQ(instance.TenantID)).
		Only(ctx)
	if err != nil {
		e.logger.Warnw("解析审批任务申请人失败", "requesterID", id, "tenantID", instance.TenantID, "error", err)
		return nil
	}
	return requester
}

// resolveApprovalAssignee 把申请人所在部门（含祖先部门递归）的负责人解析为审批任务的
// assignee。复用 service/approver.DeptManagerResolver（已有、已测试、已被 legacy 审批链
// approval_service.go:940 使用的部门->负责人查询），不重新实现部门递归逻辑。
// 解析失败，或者解析出的负责人正好是申请人自己（避免部门负责人审批自己提交的工单），
// 都返回空字符串——调用方会转入 candidateGroups 兜底路径。
func (e *CustomProcessEngine) resolveApprovalAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string {
	if requester == nil || requester.DepartmentID == 0 {
		return ""
	}
	approvers, err := approver.NewDeptManagerResolver().Resolve(ctx, e.client, &approver.ApproverContext{
		TenantID:     instance.TenantID,
		DepartmentID: requester.DepartmentID,
	})
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"审批任务未解析到部门负责人，转候选组兜底",
			"requesterID", requester.ID, "departmentID", requester.DepartmentID, "error", err,
		)
		return ""
	}
	manager := approvers[0]
	if manager.UserID == requester.ID {
		e.logger.Infow(
			"部门负责人是申请人本人，转候选组兜底，避免自己审批自己",
			"requesterID", requester.ID, "departmentID", requester.DepartmentID,
		)
		return ""
	}
	return strconv.Itoa(manager.UserID)
}

// resolveGmChainAssignee 把申请人自己的个人汇报链（user.manager_id，非部门维度）向上爬到第一个
// job_title 命中"总经理"关键字的人，解析为审批任务的 assignee。矩阵组织下同一个部门节点可能
// 有多个平级总经理（不同业务线各自的负责人），PersonalManagerResolver 按人（顺着申请人自己的
// 真实汇报链）解析，天然避开这种部门维度无法区分的歧义——设计详见
// docs/superpowers/specs/2026-08-20-personal-manager-chain-approval-design.md。
// 解析失败，或者解析出的总经理正好是申请人自己，都返回空字符串——注意这不会直接落到候选组
// 兜底：调用方（createUserTask 的 switch 分支）在这个函数返回空串后，会先退到
// resolveApprovalAssignee（申请人自己部门的负责人）再试一次，只有那一步也失败才会最终落到
// 候选组兜底。
func (e *CustomProcessEngine) resolveGmChainAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User) string {
	if requester == nil {
		return ""
	}
	approvers, err := approver.NewPersonalManagerResolver().Resolve(ctx, e.client, &approver.ApproverContext{
		TenantID:    instance.TenantID,
		RequesterID: requester.ID,
	})
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"审批任务未在申请人汇报链上解析到总经理，转候选组兜底",
			"requesterID", requester.ID, "error", err,
		)
		return ""
	}
	gm := approvers[0]
	if gm.UserID == requester.ID {
		e.logger.Infow(
			"汇报链解析出的总经理是申请人本人，转候选组兜底，避免自己审批自己",
			"requesterID", requester.ID,
		)
		return ""
	}
	return strconv.Itoa(gm.UserID)
}

// resolveRoleCandidates 查询该租户下所有 active 且（主角色等于 roleCode，或通过
// user_roles 多对多边额外拥有 roleCode 这个角色）的用户，返回候选人展开形态的字符串
// 列表（跟 GroupResolver.ExpandGroupsToUsers 的 usernames 返回值同样的
// username→email→ID 兜底规则），供 excludeUserFromCandidates/MergeCandidateUsers 直接复用。
// roleCode 应为 roles 表中存在的 code 值——不存在的角色查询返回空列表而非报错，
// 调用方按"没查到候选人"处理，转候选组兜底。
func (e *CustomProcessEngine) resolveRoleCandidates(ctx context.Context, tenantID int, roleCode string) ([]string, error) {
	// 候选人 = 主角色字段等于 roleCode 的用户，UNION 通过 user_roles 多对多边额外拥有
	// roleCode 这个角色的用户（一人多角色，仅影响这里的 BPMN 候选资格，不影响 RBAC 权限判定，
	// 见 dto.UpdateUserRequest.AdditionalRoleIds 的注释）。
	users, err := e.client.User.Query().
		Where(
			user.TenantIDEQ(tenantID),
			user.Active(true),
			user.Or(
				user.RoleEQ(roleCode),
				user.HasRolesWith(role.CodeEQ(roleCode), role.TenantIDEQ(tenantID)),
			),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询角色候选审批人失败: %w", err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		display := strings.TrimSpace(u.Username)
		if display == "" {
			display = strings.TrimSpace(u.Email)
		}
		if display == "" {
			display = strconv.Itoa(u.ID)
		}
		names = append(names, display)
	}
	return names, nil
}

// resolveRolesByPermission 从 DB 查询租户下拥有指定 resource:action 权限的所有角色 code。
func (e *CustomProcessEngine) resolveRolesByPermission(ctx context.Context, tenantID int, resource, action string) []string {
	perms, err := e.client.Permission.Query().
		Where(permission.ResourceEQ(resource), permission.ActionEQ(action), permission.TenantIDEQ(tenantID)).
		All(ctx)
	if err != nil || len(perms) == 0 {
		return nil
	}
	permIDs := make([]int, len(perms))
	for i, p := range perms {
		permIDs[i] = p.ID
	}
	rps, err := e.client.RolePermission.Query().
		Where(rolepermission.PermissionIDIn(permIDs...), rolepermission.TenantID(tenantID)).
		All(ctx)
	if err != nil || len(rps) == 0 {
		return nil
	}
	roleIDs := make([]int, len(rps))
	for i, rp := range rps {
		roleIDs[i] = rp.RoleID
	}
	roles, err := e.client.Role.Query().
		Where(role.IDIn(roleIDs...), role.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		return nil
	}
	codes := make([]string, 0, len(roles))
	for _, r := range roles {
		codes = append(codes, r.Code)
	}
	return codes
}

// resolveFixedScopeAssignee 处理固定范围组织路由（BPMN 声明 assigneeDeptId/assigneeTeamId/
// assigneeProjectId/assigneeTempTeamId 中的一个，按这个顺序取第一个非零的）。四个 resolver
// （service/approver/*.go，已有、已测试）都是"至多解析出一个人"的形状，返回值/自我审批
// 排除规则完全比照 resolveApprovalAssignee（申请人自己部门）——用 approvers[0].UserID 而
// 不是 approvers[0].UserName，因为 ApproverInfo.UserName 实际填的是 User.Name（显示名），
// 不是 authorizeTaskActor 用来比对的 User.Username（登录名），用 UserName 会导致候选人
// 字符串永远匹配不上真实登录用户。
func (e *CustomProcessEngine) resolveFixedScopeAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, task *BPMNUserTask) string {
	var resolver approver.ApproverResolver
	appCtx := &approver.ApproverContext{TenantID: instance.TenantID}
	switch {
	case task.AssigneeDeptId != 0:
		resolver = approver.NewDeptManagerResolver()
		appCtx.DepartmentID = task.AssigneeDeptId
	case task.AssigneeTeamId != 0:
		resolver = approver.NewTeamLeaderResolver()
		appCtx.TeamID = task.AssigneeTeamId
	case task.AssigneeProjectId != 0:
		resolver = approver.NewProjectMgrResolver()
		appCtx.ProjectID = task.AssigneeProjectId
	case task.AssigneeTempTeamId != 0:
		resolver = approver.NewTempTeamResolver()
		appCtx.TeamID = task.AssigneeTempTeamId
	default:
		return ""
	}
	approvers, err := resolver.Resolve(ctx, e.client, appCtx)
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"固定范围审批人解析失败，转候选组兜底",
			"resolverType", resolver.GetType(), "error", err,
		)
		return ""
	}
	if requester != nil && approvers[0].UserID == requester.ID {
		e.logger.Infow(
			"固定范围解析出的审批人是申请人本人，转候选组兜底，避免自己审批自己",
			"resolverType", resolver.GetType(), "requesterID", requester.ID,
		)
		return ""
	}
	return strconv.Itoa(approvers[0].UserID)
}

// excludeUserFromCandidates 从 candidateGroups 展开出来的候选人显示名列表里剔除某个用户。
// 匹配语义跟 authorizeTaskActor.allowed（bpmn_process_engine.go:417）保持一致：用户 ID
// 字符串或用户名；额外多判断一次 Email，覆盖 GroupResolver.ExpandGroupsToUsers 在用户名
// 为空时退化用 Email 做候选人显示名的情况。
func excludeUserFromCandidates(usernames []string, u *ent.User) []string {
	if u == nil || len(usernames) == 0 {
		return usernames
	}
	idStr := strconv.Itoa(u.ID)
	filtered := make([]string, 0, len(usernames))
	for _, name := range usernames {
		if name == idStr {
			continue
		}
		if u.Username != "" && name == u.Username {
			continue
		}
		if u.Email != "" && name == u.Email {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func splitNonEmptyCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

// getDefaultAssigntee 根据任务类型和业务逻辑获取默认分配人
// 优先级：1.流程变量显式指定 > 2.数据库规则匹配 > 3.中文关键词兜底（deprecated）
func (e *CustomProcessEngine) getDefaultAssigntee(ctx context.Context, instance *ent.ProcessInstance, task *BPMNUserTask) string {
	taskName := task.Name

	// 第一优先：流程变量中显式指定 assignee
	if instance.Variables != nil {
		if assignee, ok := instance.Variables["assignee"]; ok {
			switch val := assignee.(type) {
			case float64:
				if val > 0 {
					return strconv.FormatFloat(val, 'f', 0, 64)
				}
			case int:
				if val > 0 {
					return strconv.Itoa(val)
				}
			case string:
				if val != "" && val != "0" {
					return val
				}
			}
		}
	}

	// 第二优先：数据库 ticket_assignment_rules 规则匹配
	if assigneeFromRule := e.getAssigneeFromDBRules(ctx, instance, taskName); assigneeFromRule != "" {
		return assigneeFromRule
	}

	// 第三优先：中文关键词兜底（deprecated，未来版本将移除）
	// 审批类任务 - 从 DB 查询有审批权限的角色，再分配用户
	if strings.Contains(taskName, "审批") || strings.Contains(taskName, "审核") || strings.Contains(taskName, "批准") {
		if approverRoles := e.resolveRolesByPermission(ctx, instance.TenantID, "approval", "write"); len(approverRoles) > 0 {
			users, err := e.client.User.Query().
				Where(user.RoleIn(approverRoles...)).
				Where(user.TenantID(instance.TenantID)).
				Where(user.Active(true)).
				Limit(1).
				All(ctx)
			if err == nil && len(users) > 0 {
				return strconv.Itoa(users[0].ID)
			}
		}
	}

	// 处理类任务 - 从 DB 查询有工单处理权限的角色，再分配用户
	if strings.Contains(taskName, "处理") || strings.Contains(taskName, "执行") {
		if handlerRoles := e.resolveRolesByPermission(ctx, instance.TenantID, "ticket", "write"); len(handlerRoles) > 0 {
			users, err := e.client.User.Query().
				Where(user.RoleIn(handlerRoles...)).
				Where(user.TenantID(instance.TenantID)).
				Where(user.Active(true)).
				Limit(1).
				All(ctx)
			if err == nil && len(users) > 0 {
				return strconv.Itoa(users[0].ID)
			}
		}
	}

	// 默认分配 - 返回第一个活跃用户
	users, err := e.client.User.Query().
		Where(user.TenantID(instance.TenantID)).
		Where(user.Active(true)).
		Limit(1).
		All(ctx)
	if err == nil && len(users) > 0 {
		return strconv.Itoa(users[0].ID)
	}

	return ""
}

// getAssigneeFromDBRules 从数据库 ticket_assignment_rules 表查询匹配的分配规则
func (e *CustomProcessEngine) getAssigneeFromDBRules(ctx context.Context, instance *ent.ProcessInstance, taskName string) string {
	// 查询当前租户下所有激活的分配规则，按优先级降序排列
	rules, err := e.client.TicketAssignmentRule.Query().
		Where(
			ticketassignmentrule.TenantID(instance.TenantID),
			ticketassignmentrule.IsActive(true),
		).
		Order(ent.Desc(ticketassignmentrule.FieldPriority)).
		All(ctx)
	if err != nil {
		e.logger.Warnw("查询分配规则失败", "error", err)
		return ""
	}

	// 在内存中匹配规则条件
	for _, rule := range rules {
		if !rule.IsActive {
			continue
		}
		// 检查条件是否匹配任务名称
		if matchRuleConditions(rule.Conditions, taskName) {
			// 从 actions 中提取 assignee
			if assigneeVal, ok := rule.Actions["assignee_id"]; ok {
				switch v := assigneeVal.(type) {
				case float64:
					if v > 0 {
						return strconv.FormatFloat(v, 'f', 0, 64)
					}
				case int:
					if v > 0 {
						return strconv.Itoa(v)
					}
				case string:
					if v != "" && v != "0" {
						return v
					}
				}
			}
		}
	}

	return ""
}

// matchRuleConditions 检查规则条件是否与任务名称匹配
func matchRuleConditions(conditions []map[string]interface{}, taskName string) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, cond := range conditions {
		field, _ := cond["field"].(string)
		operator, _ := cond["operator"].(string)
		value, _ := cond["value"].(string)

		if field != "task_name" {
			continue
		}

		switch operator {
		case "equals":
			if taskName == value {
				return true
			}
		case "contains":
			if strings.Contains(taskName, value) {
				return true
			}
		case "prefix":
			if strings.HasPrefix(taskName, value) {
				return true
			}
		case "suffix":
			if strings.HasSuffix(taskName, value) {
				return true
			}
		}
	}
	return false
}

func (e *CustomProcessEngine) completeProcess(ctx context.Context, instance *ent.ProcessInstance) error {
	_, err := e.client.ProcessInstance.UpdateOne(instance).
		SetStatus("completed").
		SetEndTime(time.Now()).
		Save(ctx)
	return err
}

func (e *CustomProcessEngine) findOutgoingFlows(process *BPMNProcess, sourceRef string) []*BPMNSequenceFlow {
	var flows []*BPMNSequenceFlow
	for _, flow := range process.SequenceFlows {
		if flow.SourceRef == sourceRef {
			flows = append(flows, flow)
		}
	}
	return flows
}

// evaluateCondition 评估流转条件 (Domain Logic)
// 使用表达式引擎评估条件
func (e *CustomProcessEngine) evaluateCondition(flow *BPMNSequenceFlow, variables map[string]interface{}) bool {
	if flow.ConditionExpression == nil || flow.ConditionExpression.Expression == "" {
		return true // 无条件则默认通过
	}

	// 合并变量
	evalVars := make(map[string]interface{})
	for k, v := range e.expressionVars {
		evalVars[k] = v
	}
	for k, v := range variables {
		evalVars[k] = v
	}

	// 将 variables 包装在 "variables" 键中，以便 BPMN 表达式可以使用 variables['key'] 语法
	evalVars["variables"] = variables

	// 使用表达式引擎评估条件
	result, err := e.exprEngine.EvaluateCondition(flow.ConditionExpression.Expression, evalVars)
	if err != nil {
		// SEC-002 修复：评估失败时默认拒绝（return false），而非放行
		e.logger.Errorw(
			"条件评估失败，默认拒绝流转",
			"expression", flow.ConditionExpression.Expression,
			"error", err,
		)
		return false
	}

	return result
}

func (e *CustomProcessEngine) isEndEvent(process *BPMNProcess, id string) bool {
	for _, event := range process.EndEvents {
		if event.ID == id {
			return true
		}
	}
	return false
}

func (e *CustomProcessEngine) findUserTask(process *BPMNProcess, id string) *BPMNUserTask {
	for _, task := range process.UserTasks {
		if task.ID == id {
			return task
		}
	}
	return nil
}

func (e *CustomProcessEngine) findEndEvent(process *BPMNProcess, id string) *BPMNEndEvent {
	for _, event := range process.EndEvents {
		if event.ID == id {
			return event
		}
	}
	return nil
}

func (e *CustomProcessEngine) findExclusiveGateway(process *BPMNProcess, id string) *BPMNExclusiveGateway {
	for _, gateway := range process.ExclusiveGateways {
		if gateway.ID == id {
			return gateway
		}
	}
	return nil
}

func (e *CustomProcessEngine) findServiceTask(process *BPMNProcess, id string) *BPMNServiceTask {
	for _, task := range process.ServiceTasks {
		if task.ID == id {
			return task
		}
	}
	return nil
}

func (e *CustomProcessEngine) SuspendProcess(ctx context.Context, processInstanceID string, reason string) error {
	// 1. 获取流程实例
	query := e.client.ProcessInstance.Query().
		Where(processinstance.ProcessInstanceID(processInstanceID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instance, err := query.First(ctx)
	if err != nil {
		return fmt.Errorf("获取流程实例失败: %w", err)
	}

	// 2. 更新实例状态
	_, err = e.client.ProcessInstance.UpdateOne(instance).
		SetStatus("suspended").
		SetSuspendedTime(time.Now()).
		SetSuspendedReason(reason).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("暂停流程实例失败: %w", err)
	}

	// 3. 记录审计日志
	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	if err := e.auditService.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeUserTask,
		Action:               AuditActionProcessSuspended,
		UserID:               userID,
		UserName:             userName,
		Comment:              reason,
		TenantID:             instance.TenantID,
	}); err != nil {
		e.logger.Warnw("audit record failed", "error", err)
	}

	return nil
}

func (e *CustomProcessEngine) ResumeProcess(ctx context.Context, processInstanceID string) error {
	// 1. 获取流程实例
	query := e.client.ProcessInstance.Query().
		Where(processinstance.ProcessInstanceID(processInstanceID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instance, err := query.First(ctx)
	if err != nil {
		return fmt.Errorf("获取流程实例失败: %w", err)
	}

	// 2. 更新实例状态
	_, err = e.client.ProcessInstance.UpdateOne(instance).
		SetStatus("running").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("恢复流程实例失败: %w", err)
	}

	// 3. 记录审计日志
	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	if err := e.auditService.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeUserTask,
		Action:               AuditActionProcessResumed,
		UserID:               userID,
		UserName:             userName,
		TenantID:             instance.TenantID,
	}); err != nil {
		e.logger.Warnw("audit record failed", "error", err)
	}

	return nil
}

func (e *CustomProcessEngine) TerminateProcess(ctx context.Context, processInstanceID string, reason string) error {
	// 1. 获取流程实例
	query := e.client.ProcessInstance.Query().
		Where(processinstance.ProcessInstanceID(processInstanceID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instance, err := query.First(ctx)
	if err != nil {
		return fmt.Errorf("获取流程实例失败: %w", err)
	}

	// 2. 更新实例状态
	_, err = e.client.ProcessInstance.UpdateOne(instance).
		SetStatus("terminated").
		SetEndTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("终止流程实例失败: %w", err)
	}

	// 3. 取消所有进行中的任务
	_, err = e.client.ProcessTask.Update().
		Where(processtask.ProcessInstanceID(instance.ID)).
		Where(processtask.StatusNEQ("completed")).
		Where(processtask.StatusNEQ("cancelled")).
		SetStatus("cancelled").
		SetCompletedTime(time.Now()).
		Save(ctx)
	if err != nil {
		e.logger.Warnw("取消流程任务失败", "error", err)
	}

	// 4. 记录审计日志
	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	if err := e.auditService.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeEndEvent,
		Action:               AuditActionProcessTerminated,
		UserID:               userID,
		UserName:             userName,
		Comment:              reason,
		TenantID:             instance.TenantID,
	}); err != nil {
		e.logger.Warnw("audit record failed", "error", err)
	}

	return nil
}

// Request/Response structs
type CreateProcessDefinitionRequest struct {
	Key              string                 `json:"key" binding:"required"`
	Name             string                 `json:"name" binding:"required"`
	Description      string                 `json:"description"`
	Category         string                 `json:"category"`
	BPMNXML          string                 `json:"bpmnXml" binding:"required"`
	ProcessVariables map[string]interface{} `json:"processVariables"`
	TenantID         int                    `json:"tenantId" binding:"required"`
}

type UpdateProcessDefinitionRequest struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	Category         string                 `json:"category"`
	BPMNXML          string                 `json:"bpmnXml"`
	ProcessVariables map[string]interface{} `json:"processVariables"`
	IsActive         *bool                  `json:"isActive"`
}

type ListProcessDefinitionsRequest struct {
	Key      string `json:"key"`
	Category string `json:"category"`
	IsActive *bool  `json:"isActive"`
	TenantID int    `json:"tenantId"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

type ListProcessInstancesRequest struct {
	ProcessDefinitionKey string `json:"processDefinitionKey"`
	Status               string `json:"status"`
	BusinessKey          string `json:"businessKey"`
	TenantID             int    `json:"tenantId"`
	Page                 int    `json:"page"`
	PageSize             int    `json:"pageSize"`
}

type ListUserTasksRequest struct {
	Assignee        string `json:"assignee"`
	CandidateUsers  string `json:"candidateUsers"`
	CandidateGroups string `json:"candidateGroups"`
	// UserID 为「我的待办」语义：查询“分配给我 OR 我在候选人 OR 我所在组作为候选组”的任务。
	// 传入后：Assignee/CandidateUsers/CandidateGroups 会被忽略（可选透传）。
	UserID               int    `json:"userId"`
	Status               string `json:"status"`
	ProcessDefinitionKey string `json:"processDefinitionKey"`
	ProcessInstanceID    int    `json:"processInstanceId"`
	TenantID             int    `json:"tenantId"`
	Page                 int    `json:"page"`
	PageSize             int    `json:"pageSize"`
}

type TaskStatisticsRequest struct {
	ProcessDefinitionKey string     `json:"processDefinitionKey"`
	Assignee             string     `json:"assignee"`
	Status               string     `json:"status"`
	TenantID             int        `json:"tenantId"`
	StartDate            *time.Time `json:"startDate"`
	EndDate              *time.Time `json:"endDate"`
}

type TaskStatistics struct {
	TotalTasks        int                    `json:"totalTasks"`
	CompletedTasks    int                    `json:"completedTasks"`
	PendingTasks      int                    `json:"pendingTasks"`
	OverdueTasks      int                    `json:"overdueTasks"`
	AverageCompletion float64                `json:"averageCompletion"`
	StatusBreakdown   map[string]int         `json:"statusBreakdown"`
	AssigneeBreakdown map[string]int         `json:"assigneeBreakdown"`
	TimeDistribution  map[string]interface{} `json:"timeDistribution"`
}

// InstanceStatisticsRequest 实例统计请求
type InstanceStatisticsRequest struct {
	ProcessDefinitionKey string     `json:"processDefinitionKey"`
	Status               string     `json:"status"`
	TenantID             int        `json:"tenantId"`
	StartDate            *time.Time `json:"startDate"`
	EndDate              *time.Time `json:"endDate"`
}

// InstanceStatistics 实例统计
type InstanceStatistics struct {
	Total      int `json:"total"`
	Running    int `json:"running"`
	Completed  int `json:"completed"`
	Suspended  int `json:"suspended"`
	Terminated int `json:"terminated"`
}

// CounterSignStatus 会签状态
type CounterSignStatus struct {
	ParentTaskID string `json:"parentTaskId"`
	Total        int    `json:"total"`
	Completed    int    `json:"completed"`
	Approved     int    `json:"approved"`
	Rejected     int    `json:"rejected"`
	Pending      int    `json:"pending"`
	Status       string `json:"status"` // pending, approved, rejected
}

// CounterSignRequest 会签请求
type CounterSignRequest struct {
	ApprovalType string   `json:"approvalType"` // serial, parallel
	Approvers    []string `json:"approvers"`
	Threshold    int      `json:"threshold"`
}

// VoteRequest 投票请求
type VoteRequest struct {
	Approved bool   `json:"approved"`
	Comment  string `json:"comment"`
}

// Service implementations
type bpmnProcessDefinitionService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func (s *bpmnProcessDefinitionService) CreateProcessDefinition(ctx context.Context, req *CreateProcessDefinitionRequest) (*ent.ProcessDefinition, error) {
	// 首先检查或创建 ProcessDeployment
	var deployment *ent.ProcessDeployment
	existingDeployments, err := s.client.ProcessDeployment.Query().
		Where(processdeployment.TenantID(req.TenantID)).
		Order(ent.Desc("created_at")).
		Limit(1).
		All(ctx)

	if err == nil && len(existingDeployments) > 0 {
		deployment = existingDeployments[0]
	} else {
		// 创建新的部署记录
		deployment, err = s.client.ProcessDeployment.Create().
			SetDeploymentID(fmt.Sprintf("deploy-%d", time.Now().UnixNano())).
			SetDeploymentName(req.Name + "-deployment").
			SetDeploymentSource("api").
			SetTenantID(req.TenantID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建部署记录失败: %w", err)
		}
	}

	// 获取当前最高版本号
	nextVersion := s.getNextVersion(ctx, req.Key, req.TenantID)

	// 将旧版本标记为非最新
	existing, err := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(req.Key)).
		Where(processdefinition.IsLatest(true)).
		Where(processdefinition.TenantID(req.TenantID)).
		First(ctx)

	if err == nil && existing != nil {
		_, err = s.client.ProcessDefinition.UpdateOne(existing).
			SetIsLatest(false).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("更新旧版本失败: %w", err)
		}
	}

	definition, err := s.client.ProcessDefinition.Create().
		SetKey(req.Key).
		SetName(req.Name).
		SetDescription(req.Description).
		SetCategory(req.Category).
		SetBpmnXML([]byte(req.BPMNXML)).
		SetProcessVariables(req.ProcessVariables).
		SetVersion(nextVersion).
		SetIsActive(true).
		SetIsLatest(true).
		SetTenantID(req.TenantID).
		SetDeploymentID(deployment.ID).
		SetDeploymentName(deployment.DeploymentName).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建流程定义失败: %w", err)
	}

	return definition, nil
}

// getNextVersion 获取下一个版本号
func (s *bpmnProcessDefinitionService) getNextVersion(ctx context.Context, key string, tenantID int) string {
	// 查询当前最高版本
	existing, err := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(key)).
		Where(processdefinition.TenantID(tenantID)).
		Order(ent.Desc("version")).
		First(ctx)
	if err != nil {
		// 没有版本，返回初始版本
		return "1.0.0"
	}

	// 解析当前版本号并递增
	currentVersion := existing.Version
	// 支持 v1.0.0, 1.0.0, 1 等格式
	versionNum := 1
	if currentVersion != "" {
		// 尝试提取数字部分
		var major, minor, patch int
		_, err := fmt.Sscanf(currentVersion, "%d.%d.%d", &major, &minor, &patch)
		if err != nil {
			// 如果解析失败，尝试只解析主版本号
			fmt.Sscanf(currentVersion, "%d", &major)
			versionNum = major
		} else {
			versionNum = major*100 + minor*10 + patch
		}
	}

	// 递增版本号 (使用语义化版本号)
	newMajor := versionNum / 100
	newMinor := (versionNum / 10) % 10
	newPatch := versionNum % 10

	// 如果patch已达9，重置并递增minor
	if newPatch >= 9 {
		newPatch = 0
		newMinor++
		if newMinor >= 9 {
			newMinor = 0
			newMajor++
		}
	} else {
		newPatch++
	}

	return fmt.Sprintf("%d.%d.%d", newMajor, newMinor, newPatch)
}

func (s *bpmnProcessDefinitionService) GetProcessDefinition(ctx context.Context, key string, version string) (*ent.ProcessDefinition, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(key)).
		Where(processdefinition.Version(version))
	if tenantID > 0 {
		query = query.Where(processdefinition.TenantID(tenantID))
	}
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	return definition, nil
}

// GetProcessDefinitionByID 根据ID获取流程定义
func (s *bpmnProcessDefinitionService) GetProcessDefinitionByID(ctx context.Context, id int) (*ent.ProcessDefinition, error) {
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.ID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processdefinition.TenantID(tenantID))
	}
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	return definition, nil
}

func (s *bpmnProcessDefinitionService) GetLatestProcessDefinition(ctx context.Context, key string) (*ent.ProcessDefinition, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(key)).
		Where(processdefinition.IsLatest(true))
	if tenantID > 0 {
		query = query.Where(processdefinition.TenantID(tenantID))
	}
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取最新流程定义失败: %w", err)
	}

	return definition, nil
}

func (s *bpmnProcessDefinitionService) UpdateProcessDefinition(ctx context.Context, key string, version string, req *UpdateProcessDefinitionRequest) (*ent.ProcessDefinition, error) {
	definition, err := s.GetProcessDefinition(ctx, key, version)
	if err != nil {
		return nil, err
	}

	update := s.client.ProcessDefinition.UpdateOne(definition)

	if req.Name != "" {
		update.SetName(req.Name)
	}
	if req.Description != "" {
		update.SetDescription(req.Description)
	}
	if req.Category != "" {
		update.SetCategory(req.Category)
	}
	if req.BPMNXML != "" {
		update.SetBpmnXML([]byte(req.BPMNXML))
	}
	if req.ProcessVariables != nil {
		update.SetProcessVariables(req.ProcessVariables)
	}
	if req.IsActive != nil {
		update.SetIsActive(*req.IsActive)
	}

	updated, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新流程定义失败: %w", err)
	}

	return updated, nil
}

func (s *bpmnProcessDefinitionService) DeleteProcessDefinition(ctx context.Context, key string, version string) error {
	definition, err := s.GetProcessDefinition(ctx, key, version)
	if err != nil {
		return err
	}

	// 检查是否有运行中的实例
	runningCount, err := s.client.ProcessInstance.
		Query().
		Where(processinstance.ProcessDefinitionID(definition.ID)).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("检查流程实例失败: %w", err)
	}
	if runningCount > 0 {
		return fmt.Errorf("该流程定义有 %d 个运行中的实例，请先关闭后再删除", runningCount)
	}

	return s.client.ProcessDefinition.DeleteOne(definition).Exec(ctx)
}

func (s *bpmnProcessDefinitionService) ListProcessDefinitions(ctx context.Context, req *ListProcessDefinitionsRequest) ([]*ent.ProcessDefinition, int, error) {
	query := s.client.ProcessDefinition.Query()

	if req.Key != "" {
		query = query.Where(processdefinition.Key(req.Key))
	}
	if req.Category != "" {
		query = query.Where(processdefinition.Category(req.Category))
	}
	if req.IsActive != nil {
		query = query.Where(processdefinition.IsActive(*req.IsActive))
	}
	if req.TenantID > 0 {
		query = query.Where(processdefinition.TenantID(req.TenantID))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程定义总数失败: %w", err)
	}

	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	definitions, err := query.Order(ent.Desc(processdefinition.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程定义列表失败: %w", err)
	}

	return definitions, total, nil
}

func (s *bpmnProcessDefinitionService) SetProcessDefinitionActive(ctx context.Context, key string, version string, active bool) error {
	definition, err := s.GetProcessDefinition(ctx, key, version)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessDefinition.UpdateOne(definition).
		SetIsActive(active).
		Save(ctx)

	return err
}

type bpmnProcessInstanceService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func (s *bpmnProcessInstanceService) GetProcessInstance(ctx context.Context, processInstanceID string) (*ent.ProcessInstance, error) {
	id, err := strconv.Atoi(processInstanceID)
	if err != nil {
		return nil, fmt.Errorf("无效的流程实例ID: %w", err)
	}
	query := s.client.ProcessInstance.Query().
		Where(processinstance.ID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instance, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}

	return instance, nil
}

func (s *bpmnProcessInstanceService) ListProcessInstances(ctx context.Context, req *ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error) {
	query := s.client.ProcessInstance.Query()

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processinstance.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.Status != "" {
		query = query.Where(processinstance.Status(req.Status))
	}
	if req.BusinessKey != "" {
		query = query.Where(processinstance.BusinessKey(req.BusinessKey))
	}
	// 无条件带上 TenantID 谓词（Global Constraint：every query must include an
	// explicit TenantID predicate），与下方 processtask.TenantID(req.TenantID)
	// 保持一致，不再对 req.TenantID<=0 的情况放行不带租户过滤的查询。
	query = query.Where(processinstance.TenantID(req.TenantID))

	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	if !elevated {
		userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
		if userID <= 0 {
			// No authenticated actor and not elevated: fail closed, see nothing.
			return []*ent.ProcessInstance{}, 0, nil
		}
		userIDStr := strconv.Itoa(userID)

		// Two-step: first find which instances this user participates in via
		// any task (assignee/candidate_users/candidate_groups), then OR that
		// with instances they initiated. Deliberately not a single ent
		// subquery/JOIN — ent's subquery support for "EXISTS a related row
		// matching X" is awkward here and the two simple queries are easier
		// to read, test, and keep tenant-scoped independently.
		identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, bpmn.NewGroupResolver(s.client), req.TenantID, userID)
		if err != nil {
			return nil, 0, fmt.Errorf("解析调用者身份失败: %w", err)
		}
		taskOrPreds := []predicate.ProcessTask{
			processtask.Assignee(identity.IDStr),
			processtask.CandidateUsersContains(identity.IDStr),
		}
		if identity.Username != "" {
			taskOrPreds = append(taskOrPreds, processtask.Assignee(identity.Username), processtask.CandidateUsersContains(identity.Username))
		}
		if identity.Email != "" {
			taskOrPreds = append(taskOrPreds, processtask.Assignee(identity.Email), processtask.CandidateUsersContains(identity.Email))
		}
		for _, group := range strings.Split(identity.GroupsCSV, ",") {
			group = strings.TrimSpace(group)
			if group != "" {
				taskOrPreds = append(taskOrPreds, processtask.CandidateGroupsContains(group))
			}
		}
		// The Or(...) predicates above use Contains (SQL LIKE '%v%') on
		// CandidateUsers/CandidateGroups, which is a coarse superset
		// pre-filter only: it has no false negatives (an exact match is
		// always also a substring match) but can have false positives
		// (e.g. identity.IDStr "1" substring-matches a task whose
		// CandidateUsers is "21"). It must never be trusted as the
		// authorization decision by itself — identity.IsTaskParticipant
		// below re-checks each candidate task with exact, trimmed
		// per-CSV-element comparisons, which is the single source of
		// truth for participant matching (see service/bpmn/participation.go).
		taskQuery := s.client.ProcessTask.Query().
			Where(processtask.Or(taskOrPreds...)).
			Where(processtask.TenantID(req.TenantID))
		participantTasks, err := taskQuery.All(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("查询参与任务失败: %w", err)
		}
		instanceIDSet := make(map[int]struct{}, len(participantTasks))
		for _, t := range participantTasks {
			if !identity.IsTaskParticipant(t) {
				continue
			}
			instanceIDSet[t.ProcessInstanceID] = struct{}{}
		}
		instanceIDs := make([]int, 0, len(instanceIDSet))
		for id := range instanceIDSet {
			instanceIDs = append(instanceIDs, id)
		}

		if len(instanceIDs) > 0 {
			query = query.Where(processinstance.Or(
				processinstance.Initiator(userIDStr),
				processinstance.IDIn(instanceIDs...),
			))
		} else {
			query = query.Where(processinstance.Initiator(userIDStr))
		}
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程实例总数失败: %w", err)
	}

	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	instances, err := query.Order(ent.Desc(processinstance.FieldStartTime)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程实例列表失败: %w", err)
	}

	return instances, total, nil
}

func (s *bpmnProcessInstanceService) GetProcessInstanceVariables(ctx context.Context, processInstanceID string) (map[string]interface{}, error) {
	instance, err := s.GetProcessInstance(ctx, processInstanceID)
	if err != nil {
		return nil, err
	}

	return instance.Variables, nil
}

// reservedInstanceVariableKeys 是 ProcessTriggerService.buildProcessVariables 写入的
// 流程身份键。SetProcessInstanceVariables 拒绝覆盖它们：主防线是各 handler 写侧的
// Where(TenantID)（伪造业务 ID 只会写不中行），这里防止实例归属方污染自己实例的
// business_id/tenant_id 等身份键，导致后续 ServiceTask 分发（mergeServiceTaskVariables）
// 读到被篡改的身份上下文。CompleteTask 的任务变量合并路径不在此限制内——那是受
// ctx 租户边界约束的合法表单提交，且身份键同样有 handler 写侧过滤兜底。
var reservedInstanceVariableKeys = []string{"business_id", "business_type", "business_key", "tenant_id"}

func (s *bpmnProcessInstanceService) SetProcessInstanceVariables(ctx context.Context, processInstanceID string, variables map[string]interface{}) error {
	instance, err := s.GetProcessInstance(ctx, processInstanceID)
	if err != nil {
		return err
	}

	for _, reserved := range reservedInstanceVariableKeys {
		if _, exists := variables[reserved]; exists {
			return fmt.Errorf("变量 %q 由流程触发方管理，不允许经此端点覆盖", reserved)
		}
	}

	_, err = s.client.ProcessInstance.UpdateOne(instance).
		SetVariables(variables).
		Save(ctx)

	return err
}

func (s *bpmnProcessInstanceService) GetProcessInstanceHistory(ctx context.Context, processInstanceID string) ([]*ent.ProcessExecutionHistory, error) {
	id, err := strconv.Atoi(processInstanceID)
	if err != nil {
		return nil, fmt.Errorf("无效的流程实例ID: %w", err)
	}

	query := s.client.ProcessExecutionHistory.Query().
		Where(processexecutionhistory.ProcessInstanceID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processexecutionhistory.TenantID(tenantID))
	}

	history, err := query.Order(ent.Asc(processexecutionhistory.FieldTimestamp)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例历史失败: %w", err)
	}

	return history, nil
}

// GetInstanceStatistics 获取实例统计
func (s *bpmnProcessInstanceService) GetInstanceStatistics(ctx context.Context, req *InstanceStatisticsRequest) (*InstanceStatistics, error) {
	query := s.client.ProcessInstance.Query()

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processinstance.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.TenantID > 0 {
		query = query.Where(processinstance.TenantID(req.TenantID))
	}
	if req.StartDate != nil {
		query = query.Where(processinstance.StartTimeGTE(*req.StartDate))
	}
	if req.EndDate != nil {
		query = query.Where(processinstance.StartTimeLTE(*req.EndDate))
	}

	instances, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取实例统计失败: %w", err)
	}

	stats := &InstanceStatistics{
		Total:      len(instances),
		Running:    0,
		Completed:  0,
		Suspended:  0,
		Terminated: 0,
	}

	for _, inst := range instances {
		switch inst.Status {
		case "running":
			stats.Running++
		case "completed":
			stats.Completed++
		case "suspended":
			stats.Suspended++
		case "terminated":
			stats.Terminated++
		}
	}

	// 如果有状态筛选，返回筛选后的统计
	if req.Status != "" {
		stats.Total = 0
		switch req.Status {
		case "running":
			stats.Total = stats.Running
		case "completed":
			stats.Total = stats.Completed
		case "suspended":
			stats.Total = stats.Suspended
		case "terminated":
			stats.Total = stats.Terminated
		}
	}

	return stats, nil
}

type bpmnTaskService struct {
	client        *ent.Client
	logger        *zap.SugaredLogger
	groupResolver *bpmn.GroupResolver
	auditService  *BPMNAuditService
}

// GetTask 根据任务ID (BPMN标准task_id字符串)获取任务
func (s *bpmnTaskService) GetTask(ctx context.Context, taskID string) (*ent.ProcessTask, error) {
	query := s.client.ProcessTask.Query().
		Where(processtask.TaskID(taskID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processtask.TenantID(tenantID))
	}
	task, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	if err := s.authorizeTaskViewer(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// GetTaskByID 根据数据库自增ID获取任务
func (s *bpmnTaskService) GetTaskByID(ctx context.Context, id int) (*ent.ProcessTask, error) {
	query := s.client.ProcessTask.Query().
		Where(processtask.ID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processtask.TenantID(tenantID))
	}
	task, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	if err := s.authorizeTaskViewer(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// authorizeTaskViewer allows a task to be read by: an elevated caller
// (task:read permission), an explicitly declared system caller
// (bpmn.BPMNSystemCallerContextKey), the task's participant
// (assignee/candidate_users/candidate_groups — see
// bpmn.CallerIdentity.IsTaskParticipant), or the initiator of the task's
// parent process instance (read-only progress visibility — matches the
// "can I see my own submitted request's approval chain" product
// expectation, distinct from being allowed to act on it). Everyone else,
// including a call with no authenticated user and no system-caller
// declaration, is denied — this replaces the previous implicit
// "no userID = permissive" convention.
func (s *bpmnTaskService) authorizeTaskViewer(ctx context.Context, task *ent.ProcessTask) error {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return nil
	}
	if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("未认证的调用")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("查看用户不存在: %w", err)
	}
	if identity.IsTaskParticipant(task) {
		return nil
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(tenantID)).
		Only(ctx)
	if err == nil && instance.Initiator == identity.IDStr {
		return nil
	}
	return fmt.Errorf("当前用户无权查看该任务")
}

// authorizeCounterSignViewer gates GetCounterSignStatus. It allows anyone
// authorizeTaskViewer would allow on the PARENT task (elevated caller,
// parent-task participant, or the process instance's initiator) — but also,
// independently, any participant of ONE OF THE COUNTER-SIGN SUB-TASKS.
// The sub-task fallback is required, not optional: CreateCounterSignTasks
// fans a single parent task out into N per-approver sub-tasks without ever
// touching the parent task's own assignee/candidate fields, so the actual
// voters (Vote's callers, gated on their own sub-task via authorizeTaskActor)
// are frequently NOT participants of the parent task itself. Gating solely
// on the parent task here would incorrectly deny a legitimate counter-sign
// approver's own Vote-triggered status lookup.
func (s *bpmnTaskService) authorizeCounterSignViewer(ctx context.Context, parentTask *ent.ProcessTask, subTasks []*ent.ProcessTask) error {
	if err := s.authorizeTaskViewer(ctx, parentTask); err == nil {
		return nil
	}
	for _, sub := range subTasks {
		if err := s.authorizeTaskViewer(ctx, sub); err == nil {
			return nil
		}
	}
	return fmt.Errorf("当前用户无权查看该会签状态")
}

// authorizeTaskMutation gates assign/cancel/setVariables/counter-sign the
// same way authorizeTaskActor gates claim/complete: elevated callers
// (task:update permission) bypass the check entirely (managing a stuck task
// is a legitimate admin action); everyone else must be the task's
// assignee/candidate. Returns the resolved actor's ID and display name for
// the caller to pass into the corresponding BPMNAuditService.Record* call —
// every mutation this guards must be audited, elevated bypass or not.
//
// IMPORTANT (ruling recorded during Task 2's fix loop, see ledger): this must
// use the same two-tier check + requester exclusion as authorizeTaskActor/
// isTaskCandidate, NOT the combined IsTaskParticipant. candidate_groups is a
// LIVE re-evaluated check, not pre-filtered at task-creation time the way
// candidate_users is (createUserTask's excludeUserFromCandidates only
// filters candidate_users) — a requester who happens to belong to the
// configured approval group must not be able to bypass self-approval
// prevention by cancelling/reassigning/editing variables on their own
// approval task via that group membership. Reassignment/cancellation of
// one's own approval request is the same segregation-of-duties concern as
// self-approval, so this exclusion applies here too — unlike the read-only
// authorizeTaskViewer (Task 6), where viewing one's own request's progress
// is not a segregation-of-duties issue and needs no exclusion.
func (s *bpmnTaskService) authorizeTaskMutation(ctx context.Context, task *ent.ProcessTask) (actorID int, actorName string, err error) {
	if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
		return 0, "", nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return 0, "", fmt.Errorf("未认证的调用")
	}
	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	actor, actorErr := s.client.User.Query().Where(user.ID(userID), user.TenantID(tenantID)).Only(ctx)
	if actorErr != nil {
		return 0, "", fmt.Errorf("操作用户不存在: %w", actorErr)
	}
	if elevated {
		return userID, actor.Name, nil
	}
	identity, identErr := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if identErr != nil {
		return 0, "", fmt.Errorf("解析调用者身份失败: %w", identErr)
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return userID, actor.Name, nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, reqErr := isProcessInstanceRequester(ctx, s.client, task.ProcessInstanceID, tenantID, userID)
		if reqErr != nil {
			return 0, "", fmt.Errorf("校验申请人身份失败: %w", reqErr)
		}
		if !isRequester {
			return userID, actor.Name, nil
		}
	}
	return 0, "", fmt.Errorf("当前用户不是该任务的审批人或候选人")
}

// CompleteTaskByID 根据数据库自增ID完成任务
func (s *bpmnTaskService) CompleteTaskByID(ctx context.Context, id int, variables map[string]interface{}) error {
	engine := NewCustomProcessEngine(s.client, s.logger)
	// 直接使用 ent Client 获取任务，确保应用租户过滤
	task, err := s.client.ProcessTask.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	// 如果上下文中有租户 ID，验证任务属于该租户
	if tenantID := ctx.Value(bpmn.BPMNTenantIDContextKey); tenantID != nil && tenantID.(int) > 0 {
		if task.TenantID != tenantID.(int) {
			return fmt.Errorf("任务不属于当前租户")
		}
	}
	return engine.CompleteTask(ctx, task.TaskID, variables)
}

func (s *bpmnTaskService) ListUserTasks(ctx context.Context, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error) {
	s.logger.Debugw("ListUserTasks called", "assignee", req.Assignee, "userID", req.UserID, "tenantID", req.TenantID)

	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	if !elevated {
		// Non-elevated callers can never see anyone else's tasks — ignore
		// whatever Assignee/CandidateUsers/CandidateGroups/UserID they sent
		// and force the query back to their own authenticated identity.
		callerID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
		if callerID <= 0 {
			// No authenticated actor and not elevated: fail closed, see
			// nothing. Without this, req.UserID stays 0, which falls
			// through to the unfiltered "else" branch below and returns
			// the entire tenant's task list — the opposite of fail-closed.
			// Mirrors ListProcessInstances's identical guard.
			return []*ent.ProcessTask{}, 0, nil
		}
		req.UserID = callerID
		req.Assignee = ""
		req.CandidateUsers = ""
		req.CandidateGroups = ""
	}

	query := s.client.ProcessTask.Query()

	// 「我的待办」语义：UserID 透传时，查出"分配给我 OR 我是候选人 OR 我所在组是候选组"的任务。
	var filterInGo func([]*ent.ProcessTask) []*ent.ProcessTask
	if req.UserID > 0 {
		tenantID := req.TenantID
		if tenantID == 0 {
			if v, ok := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); ok {
				tenantID = v
			}
		}
		identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, req.UserID)
		if err != nil {
			return nil, 0, fmt.Errorf("解析用户身份失败: %w", err)
		}

		orPreds := []predicate.ProcessTask{
			processtask.Assignee(identity.IDStr),
			processtask.CandidateUsersContains(identity.IDStr),
		}
		if identity.Username != "" {
			orPreds = append(orPreds, processtask.Assignee(identity.Username), processtask.CandidateUsersContains(identity.Username))
		}
		if identity.Email != "" {
			orPreds = append(orPreds, processtask.Assignee(identity.Email), processtask.CandidateUsersContains(identity.Email))
		}
		// 每个组各自作为一条 OR 分支——之前把整份 GroupsCSV 当一个整体传给
		// CandidateGroupsContains，只有调用者恰好只属于一个组时才碰巧能匹配上；
		// 多组用户会被漏掉。
		for _, group := range strings.Split(identity.GroupsCSV, ",") {
			group = strings.TrimSpace(group)
			if group != "" {
				orPreds = append(orPreds, processtask.CandidateGroupsContains(group))
			}
		}
		query = query.Where(processtask.Or(orPreds...))

		// The Or(...) predicates above use Contains (SQL LIKE '%v%') on
		// CandidateUsers/CandidateGroups, which is a coarse superset
		// pre-filter only: it has no false negatives (an exact match is
		// always also a substring match) but can have false positives (e.g.
		// identity.IDStr "1" substring-matches a candidate string like
		// "21"). It must never be trusted as the final authorization
		// decision by itself — identity.IsTaskParticipant re-checks each
		// candidate task with exact, trimmed per-CSV-element comparisons,
		// which is the single source of truth for participant matching
		// (see service/bpmn/participation.go).
		filterInGo = func(tasks []*ent.ProcessTask) []*ent.ProcessTask {
			filtered := make([]*ent.ProcessTask, 0, len(tasks))
			for _, t := range tasks {
				if identity.IsTaskParticipant(t) {
					filtered = append(filtered, t)
				}
			}
			return filtered
		}
	} else {
		if req.Assignee != "" {
			query = query.Where(processtask.Assignee(req.Assignee))
		}
		if req.CandidateUsers != "" {
			query = query.Where(processtask.CandidateUsersContains(req.CandidateUsers))
		}
		if req.CandidateGroups != "" {
			query = query.Where(processtask.CandidateGroupsContains(req.CandidateGroups))
		}
	}
	if req.Status != "" {
		query = query.Where(processtask.Status(req.Status))
	}
	if req.ProcessDefinitionKey != "" {
		query = query.Where(processtask.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.ProcessInstanceID > 0 {
		query = query.Where(processtask.ProcessInstanceID(req.ProcessInstanceID))
	}
	if req.TenantID > 0 {
		query = query.Where(processtask.TenantID(req.TenantID))
	}

	if filterInGo != nil {
		// UserID branch: the DB query above is only a coarse superset
		// pre-filter, so pagination/count cannot be pushed down to SQL —
		// fetch all matching rows, apply the exact Go-side filter, then
		// paginate the filtered slice ourselves.
		allTasks, err := query.Order(ent.Desc(processtask.FieldCreatedTime)).All(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("获取任务列表失败: %w", err)
		}
		filtered := filterInGo(allTasks)
		total := len(filtered)

		if req.Page > 0 && req.PageSize > 0 {
			offset := (req.Page - 1) * req.PageSize
			if offset >= len(filtered) {
				return []*ent.ProcessTask{}, total, nil
			}
			end := offset + req.PageSize
			if end > len(filtered) {
				end = len(filtered)
			}
			return filtered[offset:end], total, nil
		}
		return filtered, total, nil
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取任务总数失败: %w", err)
	}

	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	tasks, err := query.Order(ent.Desc(processtask.FieldCreatedTime)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取任务列表失败: %w", err)
	}

	return tasks, total, nil
}

// ListUserTaskViews 「我的待办」视图：任务列表附带所属实例的 businessKey 等业务上下文，
// 供审批中心跳转业务单据使用。返回 DTO 而非 Ent 模型。
func (s *bpmnTaskService) ListUserTaskViews(ctx context.Context, req *ListUserTasksRequest) ([]*dto.BPMNTaskResponse, int, error) {
	tasks, total, err := s.ListUserTasks(ctx, req)
	if err != nil {
		return nil, 0, err
	}

	// 批量加载任务所属流程实例，避免 N+1 查询
	instanceIDs := make([]int, 0, len(tasks))
	seen := make(map[int]bool, len(tasks))
	for _, task := range tasks {
		if !seen[task.ProcessInstanceID] {
			seen[task.ProcessInstanceID] = true
			instanceIDs = append(instanceIDs, task.ProcessInstanceID)
		}
	}
	instanceMap := make(map[int]*ent.ProcessInstance, len(instanceIDs))
	if len(instanceIDs) > 0 {
		instances, err := s.client.ProcessInstance.Query().
			Where(processinstance.IDIn(instanceIDs...)).
			All(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("加载任务所属流程实例失败: %w", err)
		}
		for _, instance := range instances {
			instanceMap[instance.ID] = instance
		}
	}

	return dto.ToBPMNTaskResponseList(tasks, instanceMap), total, nil
}

func (s *bpmnTaskService) ListApprovalDecisions(ctx context.Context, processInstanceKey string) ([]*ent.ProcessApprovalDecision, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return nil, fmt.Errorf("缺少租户上下文")
	}
	return s.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.ProcessInstanceKey(processInstanceKey),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
}

func (s *bpmnTaskService) AssignTask(ctx context.Context, taskID string, assignee string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(assignee).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}

	var assigneeUser *ent.User
	if assigneeID, convErr := strconv.Atoi(assignee); convErr == nil {
		assigneeTenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
		if assigneeTenantID == 0 {
			assigneeTenantID = task.TenantID
		}
		assigneeUser, _ = s.client.User.Query().
			Where(user.ID(assigneeID), user.TenantID(assigneeTenantID)).
			Only(ctx)
	}
	if actorID > 0 {
		if auditErr := s.auditService.RecordTaskAssigned(ctx, task, assigneeUser, actorID, actorName); auditErr != nil {
			s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
		}
	}
	return nil
}

// isTaskCandidate 复用共享的 bpmn.CallerIdentity 参与者判定（用户 ID/用户名/邮箱，或
// candidate_groups 命中），用于 ClaimTask/ClaimTaskByID 校验：只有任务的
// assignee/candidate_users/candidate_groups 命中的人才能认领未分配的任务——否则任何
// 登录用户都能抢先认领任何审批任务（包括自己提交的工单）。candidate_groups 命中还要
// 额外排除申请人本人——candidate_groups 不像 candidate_users 那样在任务创建时就已经
// 过滤过申请人，见 MatchesCandidateGroup 的注释。
func isTaskCandidate(ctx context.Context, client *ent.Client, userID int, task *ent.ProcessTask) (bool, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, client, bpmn.NewGroupResolver(client), tenantID, userID)
	if err != nil {
		return false, err
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return true, nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, rErr := isProcessInstanceRequester(ctx, client, task.ProcessInstanceID, tenantID, userID)
		if rErr != nil {
			return false, rErr
		}
		return !isRequester, nil
	}
	return false, nil
}

// ClaimTask 认领任务 (根据task_id字符串)
func (s *bpmnTaskService) ClaimTask(ctx context.Context, taskID string, userID string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	// 检查任务是否已分配 (assignee不为空且不为"0")
	if task.Assignee != "" && task.Assignee != "0" {
		return fmt.Errorf("任务已被其他用户认领")
	}

	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		return fmt.Errorf("无效的用户ID")
	}
	ok, err := isTaskCandidate(ctx, s.client, uid, task)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("当前用户不是该任务的候选人，无法认领")
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(userID).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)

	return err
}

// ClaimTaskByID 认领任务 (根据数据库自增ID)
func (s *bpmnTaskService) ClaimTaskByID(ctx context.Context, id int, userID int) error {
	task, err := s.GetTaskByID(ctx, id)
	if err != nil {
		return err
	}

	// 检查任务是否已分配 (assignee不为空且不为"0")
	if task.Assignee != "" && task.Assignee != "0" {
		return fmt.Errorf("任务已被其他用户认领")
	}

	ok, err := isTaskCandidate(ctx, s.client, userID, task)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("当前用户不是该任务的候选人，无法认领")
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(fmt.Sprintf("%d", userID)).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)

	return err
}

func (s *bpmnTaskService) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	engine := NewCustomProcessEngine(s.client, s.logger)
	return engine.CompleteTask(ctx, taskID, variables)
}

func (s *bpmnTaskService) CancelTask(ctx context.Context, taskID string, reason string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetStatus("cancelled").
		Save(ctx)
	if err != nil {
		return err
	}

	if actorID > 0 {
		if auditErr := s.auditService.RecordTaskCancelled(ctx, task, actorID, actorName, reason); auditErr != nil {
			s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
		}
	}
	return nil
}

func (s *bpmnTaskService) GetTaskVariables(ctx context.Context, taskID string) (map[string]interface{}, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return task.TaskVariables, nil
}

func (s *bpmnTaskService) SetTaskVariables(ctx context.Context, taskID string, variables map[string]interface{}) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}
	before := task.TaskVariables

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetTaskVariables(variables).
		Save(ctx)
	if err != nil {
		return err
	}

	if actorID > 0 {
		if auditErr := s.auditService.RecordTaskVariablesChanged(ctx, task, actorID, actorName, before, variables); auditErr != nil {
			s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
		}
	}
	return nil
}

func (s *bpmnTaskService) HandleTaskTimeout(ctx context.Context, taskID string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	if !task.DueDate.IsZero() && time.Now().After(task.DueDate) {
		_, err = s.client.ProcessTask.UpdateOne(task).
			SetStatus("timeout").
			Save(ctx)
		return err
	}

	return fmt.Errorf("任务未超时")
}

func (s *bpmnTaskService) RetryTask(ctx context.Context, taskID string, maxRetries int) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	retryCount := 0
	if task.TaskVariables != nil {
		if count, exists := task.TaskVariables["retry_count"]; exists {
			if countInt, ok := count.(float64); ok {
				retryCount = int(countInt)
			}
		}
	}

	if retryCount >= maxRetries {
		return fmt.Errorf("任务重试次数已达上限: %d", maxRetries)
	}

	if task.TaskVariables == nil {
		task.TaskVariables = make(map[string]interface{})
	}
	task.TaskVariables["retry_count"] = retryCount + 1
	task.TaskVariables["last_retry_time"] = time.Now().Format(time.RFC3339)

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetStatus("pending").
		SetTaskVariables(task.TaskVariables).
		Save(ctx)

	return err
}

func (s *bpmnTaskService) DelegateTask(ctx context.Context, taskID string, newAssignee string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	if task.TaskVariables == nil {
		task.TaskVariables = make(map[string]interface{})
	}
	task.TaskVariables["delegated_from"] = task.Assignee
	task.TaskVariables["delegated_time"] = time.Now().Format(time.RFC3339)

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(newAssignee).
		SetStatus("delegated").
		SetTaskVariables(task.TaskVariables).
		Save(ctx)

	return err
}

func (s *bpmnTaskService) EscalateTask(ctx context.Context, taskID string, reason string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	if task.TaskVariables == nil {
		task.TaskVariables = make(map[string]interface{})
	}
	task.TaskVariables["escalation_reason"] = reason
	task.TaskVariables["escalated_time"] = time.Now().Format(time.RFC3339)

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetStatus("escalated").
		SetTaskVariables(task.TaskVariables).
		Save(ctx)

	return err
}

func (s *bpmnTaskService) BatchAssignTasks(ctx context.Context, taskIDs []string, assignee string) error {
	if len(taskIDs) == 0 {
		return fmt.Errorf("任务ID列表为空")
	}

	_, err := s.client.ProcessTask.Update().
		Where(processtask.TaskIDIn(taskIDs...)).
		SetAssignee(assignee).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)

	return err
}

func (s *bpmnTaskService) GetTaskStatistics(ctx context.Context, req *TaskStatisticsRequest) (*TaskStatistics, error) {
	query := s.client.ProcessTask.Query()

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processtask.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.Assignee != "" {
		query = query.Where(processtask.Assignee(req.Assignee))
	}
	if req.Status != "" {
		query = query.Where(processtask.Status(req.Status))
	}
	if req.TenantID > 0 {
		query = query.Where(processtask.TenantID(req.TenantID))
	}
	if req.StartDate != nil {
		query = query.Where(processtask.CreatedTimeGTE(*req.StartDate))
	}
	if req.EndDate != nil {
		query = query.Where(processtask.CreatedTimeLTE(*req.EndDate))
	}

	tasks, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务统计信息失败: %w", err)
	}

	stats := &TaskStatistics{
		TotalTasks:        len(tasks),
		StatusBreakdown:   make(map[string]int),
		AssigneeBreakdown: make(map[string]int),
		TimeDistribution:  make(map[string]interface{}),
	}

	var totalCompletionTime time.Duration
	completedCount := 0

	for _, task := range tasks {
		stats.StatusBreakdown[task.Status]++

		if task.Assignee != "" {
			stats.AssigneeBreakdown[task.Assignee]++
		}

		if task.Status == "completed" && !task.CompletedTime.IsZero() && !task.AssignedTime.IsZero() {
			completionTime := task.CompletedTime.Sub(task.AssignedTime)
			totalCompletionTime += completionTime
			completedCount++
		}

		if !task.DueDate.IsZero() && time.Now().After(task.DueDate) && task.Status != "completed" {
			stats.OverdueTasks++
		}
	}

	if completedCount > 0 {
		stats.AverageCompletion = float64(totalCompletionTime.Milliseconds()) / float64(completedCount)
	}

	stats.CompletedTasks = stats.StatusBreakdown["completed"]
	stats.PendingTasks = stats.StatusBreakdown["pending"] + stats.StatusBreakdown["assigned"]

	return stats, nil
}

// CreateCounterSignTasks 创建会签子任务
func (s *bpmnTaskService) CreateCounterSignTasks(ctx context.Context, parentTaskID string, req *CounterSignRequest) ([]*ent.ProcessTask, error) {
	// 获取父任务（必须显式带 TenantID 谓词——见 Task 9 回归发现：这里此前没有
	// 租户过滤，elevated 调用者可以对任意租户的任务发起会签，因为
	// authorizeTaskMutation 的 elevated 分支只校验调用者自身所在租户，不校验
	// 目标任务所属租户）。
	query := s.client.ProcessTask.Query().Where(processtask.TaskID(parentTaskID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processtask.TenantID(tenantID))
	}
	parentTask, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取父任务失败: %w", err)
	}

	actorID, actorName, err := s.authorizeTaskMutation(ctx, parentTask)
	if err != nil {
		return nil, err
	}

	// 生成根任务ID（如果是第一个会签任务）
	rootTaskID := parentTaskID
	if parentTask.RootTaskID != "" {
		rootTaskID = parentTask.RootTaskID
	}

	threshold := req.Threshold
	if threshold == 0 {
		threshold = len(req.Approvers)
	}

	var tasks []*ent.ProcessTask
	for i, approver := range req.Approvers {
		taskID := fmt.Sprintf("%s_countersign_%d", parentTaskID, i)
		status := common.ProcessTaskStatusAssigned
		if req.ApprovalType == "serial" && i > 0 {
			status = "created"
		}
		task, err := s.client.ProcessTask.Create().
			SetTaskID(taskID).
			SetProcessInstanceID(parentTask.ProcessInstanceID).
			SetProcessDefinitionKey(parentTask.ProcessDefinitionKey).
			SetTaskDefinitionKey(parentTask.TaskDefinitionKey + "_counter").
			SetTaskName(parentTask.TaskName + "_会签").
			SetTaskType("user_task").
			SetAssignee(approver).
			SetStatus(status).
			SetPriority(parentTask.Priority).
			SetParentTaskID(parentTaskID).
			SetRootTaskID(rootTaskID).
			SetTenantID(parentTask.TenantID).
			SetCreatedTime(time.Now()).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建会签任务失败: %w", err)
		}
		tasks = append(tasks, task)
	}

	// 更新父任务状态为会签中
	_, err = s.client.ProcessTask.UpdateOneID(parentTask.ID).
		SetTaskVariables(map[string]interface{}{
			"approval_type": req.ApprovalType,
			"threshold":     threshold,
			"total":         len(req.Approvers),
			"completed":     0,
			"approved":      0,
			"rejected":      0,
		}).
		Save(ctx)
	if err != nil {
		s.logger.Warnf("更新父任务变量失败: %v", err)
	}

	if actorID > 0 {
		if auditErr := s.auditService.RecordCounterSignCreated(ctx, parentTask, actorID, actorName, len(req.Approvers)); auditErr != nil {
			s.logger.Warnw("audit record failed", "error", auditErr, "task_id", parentTask.TaskID)
		}
	}

	return tasks, nil
}

// GetCounterSignStatus 获取会签状态
//
// 必须先按 TenantID 取到父任务和子任务，再通过 authorizeCounterSignViewer 校验——
// 此前这里既无租户过滤也无授权，任何持有 task_id 的用户都能读到别的租户的会签状态
// （见 BPMN 任务/实例授权修复计划复审 Finding 1）。
//
// 注意：门槛不能只查 authorizeTaskViewer(parentTask)。CreateCounterSignTasks 把
// 一个父任务派生成 N 个按审批人分别赋值 assignee 的子任务，但从不touch父任务自身的
// assignee/candidate 字段——真正投票的人（Vote 的调用者）是子任务的参与者，不一定是
// 父任务的参与者。曾经这样实现过，并用一个"审批人 != 父任务原 assignee"的会签场景
// 实测验证：会导致 Vote 内部调用 GetCounterSignStatus 时被误拒——审批人明明刚刚
// 通过 authorizeTaskActor 在子任务上通过了校验，却在这里因为父任务参与者检查失败
// 而报错。所以这里同时接受"父任务参与者/发起人/elevated"或"任意一个会签子任务的
// 参与者"，见 authorizeCounterSignViewer。
func (s *bpmnTaskService) GetCounterSignStatus(ctx context.Context, parentTaskID string) (*CounterSignStatus, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)

	parentQuery := s.client.ProcessTask.Query().Where(processtask.TaskID(parentTaskID))
	if tenantID > 0 {
		parentQuery = parentQuery.Where(processtask.TenantID(tenantID))
	}
	parentTask, err := parentQuery.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取父任务失败: %w", err)
	}

	// 获取所有会签子任务
	subQuery := s.client.ProcessTask.Query().Where(processtask.ParentTaskID(parentTaskID))
	if tenantID > 0 {
		subQuery = subQuery.Where(processtask.TenantID(tenantID))
	}
	subTasks, err := subQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取会签子任务失败: %w", err)
	}

	if err := s.authorizeCounterSignViewer(ctx, parentTask, subTasks); err != nil {
		return nil, err
	}

	status := &CounterSignStatus{
		ParentTaskID: parentTaskID,
		Total:        len(subTasks),
		Completed:    0,
		Approved:     0,
		Rejected:     0,
		Pending:      len(subTasks),
		Status:       "pending",
	}

	for _, task := range subTasks {
		switch task.Status {
		case "completed":
			status.Completed++
			status.Pending--
			// 检查审批结果
			if vars := task.TaskVariables; vars != nil {
				if approved, ok := vars["approved"].(bool); ok && approved {
					status.Approved++
				} else {
					status.Rejected++
				}
			}
		case "assigned", "created":
			// still pending
		}
	}

	threshold := status.Total
	if value, ok := numericInt(parentTask.TaskVariables["threshold"]); ok && value > 0 {
		threshold = value
	}
	if status.Approved >= threshold {
		status.Status = "approved"
	} else if status.Approved+status.Pending < threshold {
		status.Status = "rejected"
	} else {
		status.Status = "pending"
	}

	return status, nil
}

func numericInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// Vote 投票（完成会签任务）
//
// 两处 ProcessTask 查询（本任务 + 父任务）都显式带 TenantID 谓词，作为纵深防御：
// 目前即使不加这层过滤也不可利用（全局唯一的 username/email + 下游
// isProcessInstanceRequester 自身的租户过滤恰好会失败关闭），但不应该依赖这种
// 偶然性，凡是 ProcessTask 查询都应显式带租户谓词（见复审 Finding 2）。
func (s *bpmnTaskService) Vote(ctx context.Context, taskID string, req *VoteRequest) error {
	voteTenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	taskQuery := s.client.ProcessTask.Query().Where(processtask.TaskID(taskID))
	if voteTenantID > 0 {
		taskQuery = taskQuery.Where(processtask.TenantID(voteTenantID))
	}
	task, err := taskQuery.First(ctx)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	engineForAuth := NewCustomProcessEngine(s.client, s.logger).(*CustomProcessEngine)
	if err := engineForAuth.authorizeTaskActor(ctx, task); err != nil {
		return err
	}
	if task.Status == "completed" || task.Status == "cancelled" {
		return fmt.Errorf("会签任务已结束")
	}
	if task.ParentTaskID != "" && task.Status != common.ProcessTaskStatusAssigned {
		return fmt.Errorf("会签任务尚未轮到当前审批人")
	}

	// 更新任务状态为完成
	_, err = s.client.ProcessTask.UpdateOneID(task.ID).
		SetStatus("completed").
		SetCompletedTime(time.Now()).
		SetTaskVariables(map[string]interface{}{
			"approved": req.Approved,
			"comment":  req.Comment,
		}).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("完成任务失败: %w", err)
	}
	instance, err := s.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	if err == nil {
		action, decision := "reject", "rejected"
		if req.Approved {
			action, decision = "approve", "approved"
		}
		engine := NewCustomProcessEngine(s.client, s.logger).(*CustomProcessEngine)
		if err := engine.recordApprovalDecision(ctx, instance, task, map[string]interface{}{"approvalAction": action, "approvalResult": decision, "approvalComment": req.Comment}); err != nil {
			return err
		}
	}

	// 获取会签状态
	parentTaskID := task.ParentTaskID
	if parentTaskID == "" {
		return nil // 没有父任务，不需要检查会签状态
	}

	status, err := s.GetCounterSignStatus(ctx, parentTaskID)
	if err != nil {
		return fmt.Errorf("获取会签状态失败: %w", err)
	}

	// 根据会签类型和阈值判断是否需要终止其他任务
	parentTaskQuery := s.client.ProcessTask.Query().Where(processtask.TaskID(parentTaskID))
	if voteTenantID > 0 {
		parentTaskQuery = parentTaskQuery.Where(processtask.TenantID(voteTenantID))
	}
	parentTask, err := parentTaskQuery.First(ctx)
	if err != nil {
		return nil
	}

	vars := parentTask.TaskVariables
	if vars == nil {
		vars = make(map[string]interface{})
	}
	threshold := 1
	if t, ok := numericInt(vars["threshold"]); ok {
		threshold = t
	}
	approvalType := "parallel"
	if at, ok := vars["approval_type"].(string); ok {
		approvalType = at
	}

	if approvalType == "serial" && req.Approved && status.Status == "pending" {
		next, err := s.client.ProcessTask.Query().Where(processtask.ParentTaskID(parentTaskID), processtask.Status("created")).Order(ent.Asc(processtask.FieldID)).First(ctx)
		if err == nil {
			_ = s.client.ProcessTask.UpdateOneID(next.ID).SetStatus(common.ProcessTaskStatusAssigned).Exec(ctx)
		}
	}

	// 检查是否达到阈值
	if status.Status == "approved" || status.Status == "rejected" {
		_, _ = s.client.ProcessTask.Update().
			Where(processtask.ParentTaskID(parentTaskID), processtask.StatusNEQ("completed"), processtask.StatusNEQ("cancelled")).
			SetStatus("cancelled").SetCompletedTime(time.Now()).Save(ctx)
		// 更新父任务
		s.client.ProcessTask.UpdateOneID(parentTask.ID).
			SetTaskVariables(map[string]interface{}{
				"approval_type": approvalType,
				"threshold":     threshold,
				"total":         status.Total,
				"completed":     status.Completed,
				"approved":      status.Approved,
				"rejected":      status.Rejected,
				"final_status":  status.Status,
			}).
			Exec(ctx)
		workflowCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, parentTask.TenantID)
		engine := NewCustomProcessEngine(s.client, s.logger)
		if err := engine.CompleteTask(workflowCtx, parentTask.TaskID, map[string]interface{}{"approvalResult": status.Status, "approved": status.Status == "approved"}); err != nil {
			return fmt.Errorf("推进会签父任务失败: %w", err)
		}
	}

	return nil
}
