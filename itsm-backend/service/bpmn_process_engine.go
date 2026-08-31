package service

import (
	"context"
	"errors"
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
	"itsm-backend/ent/processcallbackoutbox"
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

	"github.com/google/uuid"
	"github.com/lib/pq"
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
	StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error)
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
	DelegateTask(ctx context.Context, taskID string, newAssignee string) error
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
	client                *ent.Client
	logger                *zap.SugaredLogger
	parser                *BPMNParser            // 使用自定义的BPMN解析器
	exprEngine            *ExpressionEngine      // 表达式引擎
	expressionVars        map[string]interface{} // 表达式变量
	callbackRegistry      *bpmn.CallbackRegistry // 服务任务回调注册中心
	groupResolver         *bpmn.GroupResolver    // 审批组解析器：candidateGroups → 候选用户
	participationResolver *bpmnParticipationResolver
	callbackOutbox        *bpmnCallbackOutbox
	callbackExecutionKeys *[]string
	// 内部服务
	processDefinitionService *bpmnProcessDefinitionService
	processInstanceService   *bpmnProcessInstanceService
	taskService              *bpmnTaskService
	// 审计服务
	auditService *BPMNAuditService
}

// NewCustomProcessEngine 创建自定义流程引擎实例
func NewCustomProcessEngine(client *ent.Client, logger *zap.SugaredLogger) ProcessEngine {
	groupResolver := bpmn.NewGroupResolver(client)
	participationResolver := newBPMNParticipationResolver(client, groupResolver)
	engine := &CustomProcessEngine{
		client:                client,
		logger:                logger,
		parser:                NewBPMNParser(),
		exprEngine:            NewExpressionEngine(),
		expressionVars:        make(map[string]interface{}),
		callbackRegistry:      bpmn.NewCallbackRegistry(client, logger),
		groupResolver:         groupResolver,
		participationResolver: participationResolver,
	}
	engine.auditService = NewBPMNAuditService(client, logger)
	engine.callbackOutbox = &bpmnCallbackOutbox{client: client, executor: engine}
	engine.processDefinitionService = &bpmnProcessDefinitionService{client: client, logger: logger}
	engine.processInstanceService = &bpmnProcessInstanceService{client: client, logger: logger, participationResolver: participationResolver, auditService: engine.auditService}
	// taskService 持有 engine 自身的引用（而不是每次调用再 NewCustomProcessEngine 造一个新的）：
	// callbackRegistry 是 engine 级别的状态，bootstrap 在各领域 service 构造完成后往
	// 这一个 engine 的 registry 里注入 TicketService/IncidentService。任务完成路径若临时
	// 新建 engine，持久化执行和 worker 恢复都会拿到未注入的空 registry。
	engine.taskService = &bpmnTaskService{client: client, logger: logger, groupResolver: engine.groupResolver, participationResolver: participationResolver, engine: engine}
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
	return &bpmnProcessInstanceService{
		client:                e.client,
		logger:                e.logger,
		participationResolver: e.participationResolver,
		auditService:          e.auditService,
	}
}

// TaskService 返回任务服务
//
// 必须返回构造时创建的那一个实例（它持有 engine 自身的引用），不能每次现造一个：
// 任务完成会经由 bpmnTaskService.CompleteTask/CompleteTaskByID 回到 engine.CompleteTask，
// 而 engine 的 callbackRegistry 是被 bootstrap 注入过领域服务的实例级状态。
func (e *CustomProcessEngine) TaskService() TaskService {
	return e.taskService
}

// CallbackRegistry 暴露内部的 ServiceTask 回调注册中心，供 bootstrap 在各领域 service
// 构造完成后做延迟依赖注入（跟 TicketService.SetNotificationService 是同一个模式）。
func (e *CustomProcessEngine) CallbackRegistry() *bpmn.CallbackRegistry {
	return e.callbackRegistry
}

func resolveProcessInitiator(ctx context.Context, variables map[string]interface{}) string {
	if userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); userID > 0 {
		return strconv.Itoa(userID)
	}
	for _, key := range []string{"requester_id", "requesterId"} {
		if requesterID := bpmn.GetIntFromVars(variables, key); requesterID > 0 {
			return strconv.Itoa(requesterID)
		}
	}
	return "system"
}

// StartProcess 启动流程实例
func (e *CustomProcessEngine) StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error) {
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if legacyTenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); legacyTenantID > 0 && legacyTenantID != tenantID {
		return nil, common.NewForbiddenError("BPMN 启动租户上下文不一致")
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启流程启动事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	executionKeys := make([]string, 0)
	txEngine := e.forClient(tx.Client(), &executionKeys)

	query := tx.Client().ProcessDefinition.Query().
		Where(processdefinition.Key(processDefinitionKey)).
		Where(processdefinition.IsActive(true)).
		Where(processdefinition.IsLatest(true))
	query = query.Where(processdefinition.TenantID(tenantID))
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	bpmnDefinitions, err := e.parser.ParseXML(definition.BpmnXML)
	if err != nil {
		return nil, fmt.Errorf("解析BPMN失败: %w", err)
	}

	if len(bpmnDefinitions.Processes) == 0 {
		return nil, fmt.Errorf("BPMN中未找到流程定义")
	}
	process := bpmnDefinitions.Processes[0]

	if len(process.StartEvents) == 0 {
		return nil, fmt.Errorf("流程缺少开始事件")
	}
	startEvent := process.StartEvents[0]

	createInstance := tx.Client().ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("PI-%s-%d", processDefinitionKey, time.Now().UnixNano())).
		SetBusinessKey(businessKey).
		SetProcessDefinitionKey(processDefinitionKey).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
		SetInitiator(resolveProcessInitiator(ctx, variables)).
		SetStartTime(time.Now()).
		SetTenantID(definition.TenantID).
		SetCurrentActivityID(startEvent.ID).
		SetCurrentActivityName(startEvent.Name)
	if businessType != "" {
		createInstance = createInstance.SetBusinessType(businessType)
	}
	if businessID > 0 {
		createInstance = createInstance.SetBusinessID(businessID)
	}
	instance, err := createInstance.Save(ctx)
	if err != nil {
		// idx_process_instances_running_unique（migration 015）是这条并发竞态的最终防线：
		// 各业务域（如 handlers/change 的 SubmitChange）在调用这里之前都做了一次
		// "同 businessKey 是否已有运行中实例"的应用层检查，但那是 check-then-act，
		// 两个几乎同时的请求可能都通过检查、都走到这里——DB 唯一约束会让后到的这次
		// INSERT 失败，转成友好错误而不是让原始 SQL 报错裸露给调用方。
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "idx_process_instances_running_unique" {
			return nil, fmt.Errorf("该业务实体已存在一个运行中的流程实例，不能重复触发")
		}
		return nil, fmt.Errorf("创建流程实例失败: %w", err)
	}

	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, definition.TenantID)
	if err := txEngine.executeStep(ctx, instance, process, startEvent.ID, variables); err != nil {
		return nil, err
	}

	userID := 0
	userName := ""
	if u, ok := ctx.Value("user").(*ent.User); ok {
		userID = u.ID
		userName = u.Name
	}
	if err := txEngine.auditService.RecordProcessStarted(ctx, instance, userID, userName, variables); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交流程启动事务失败: %w", err)
	}

	instance.Unwrap()
	e.processCommittedCallbackKeys(ctx, definition.TenantID, executionKeys)
	return instance, nil
}

type completedTaskEffect struct {
	task         *ent.ProcessTask
	variables    map[string]interface{}
	asyncHandler bpmn.ServiceTaskHandlerInterface
}

// CompleteTask commits task state, audit, and callback intent atomically. The
// post-commit attempt is only a latency optimization; durable recovery owns any
// callback failure after the task transaction commits.
func (e *CustomProcessEngine) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	participantVariables, err := validateAndCloneBPMNParticipantVariables(variables, false)
	if err != nil {
		return err
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务完成事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	executionKeys := make([]string, 0)
	effect, err := e.completeTaskWithClient(ctx, tx.Client(), taskID, participantVariables, &executionKeys)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务完成事务失败: %w", err)
	}
	effect.task.Unwrap()
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID <= 0 {
		ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, effect.task.TenantID)
	}
	e.processCommittedCallbackKeys(ctx, effect.task.TenantID, executionKeys)
	e.executeAsyncUserTaskCompletion(ctx, effect)
	return nil
}

func (e *CustomProcessEngine) completeTaskWithClient(ctx context.Context, client *ent.Client, taskID string, variables map[string]interface{}, executionKeys *[]string) (*completedTaskEffect, error) {
	tenantID, err := bpmnTaskMutationTenant(ctx)
	if err != nil {
		return nil, err
	}
	taskQuery := client.ProcessTask.Query().Where(processtask.TaskID(taskID), processtask.TenantID(tenantID))
	task, err := taskQuery.Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	if err := e.authorizeTaskActorWithClient(ctx, client, task); err != nil {
		return nil, err
	}
	return e.completeAuthorizedTaskWithClient(ctx, client, task, variables, executionKeys)
}

// completeAuthorizedTaskWithClient mutates a task only after the caller has
// authorized that exact task. Vote uses it for the synthetic parent after it
// authorizes the child and locks/validates the parent in the same transaction.
func (e *CustomProcessEngine) completeAuthorizedTaskWithClient(ctx context.Context, client *ent.Client, task *ent.ProcessTask, variables map[string]interface{}, executionKeys *[]string) (*completedTaskEffect, error) {
	if task.Status == common.ProcessTaskStatusCompleted || task.Status == common.ProcessTaskStatusCancelled {
		return nil, common.NewConflictError("process task completion", "task is no longer active")
	}
	instance, err := client.ProcessInstance.Query().Where(
		processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}
	if ctxTenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); ctxTenantID <= 0 {
		ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, instance.TenantID)
	}
	definition, err := client.ProcessDefinition.Query().Where(
		processdefinition.ID(instance.ProcessDefinitionID),
		processdefinition.TenantID(instance.TenantID),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}
	definitions, err := e.parser.ParseXML(definition.BpmnXML)
	if err != nil {
		return nil, fmt.Errorf("解析BPMN失败: %w", err)
	}
	if len(definitions.Processes) == 0 {
		return nil, fmt.Errorf("解析BPMN失败: 流程定义不包含可执行流程")
	}
	process := definitions.Processes[0]
	descriptor, err := e.descriptorForProcessTask(ctx, client, task, process)
	if err != nil {
		return nil, err
	}
	if descriptor.HandlerID == bpmnUnresolvedUserTaskCallbackHandlerID {
		return nil, fmt.Errorf("回调描述符无法解析: %s", task.TaskDefinitionKey)
	}

	var handler bpmn.ServiceTaskHandlerInterface
	callbackVariables := map[string]interface{}{}
	if descriptor.HandlerID != bpmnNoUserTaskCallbackHandlerID {
		handler = e.resolveCallbackDescriptorHandler(descriptor)
		if handler == nil {
			return nil, fmt.Errorf("回调处理器不可用: %s", task.TaskDefinitionKey)
		}
		callbackVariables, err = filterBPMNCallbackPayload(handler, descriptor.Action, variables)
		if err != nil {
			return nil, err
		}
	}

	merged := make(map[string]interface{}, len(instance.Variables)+len(variables))
	for key, value := range instance.Variables {
		merged[key] = value
	}
	for key, value := range variables {
		merged[key] = value
	}
	updatedInstance, err := client.ProcessInstance.Update().Where(
		processinstance.ID(instance.ID),
		processinstance.TenantID(instance.TenantID),
		processinstance.Version(instance.Version),
	).SetVariables(merged).SetVersion(instance.Version + 1).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("合并实例变量失败: %w", err)
	}
	if updatedInstance != 1 {
		return nil, common.NewConflictError("process instance variables", "instance was concurrently updated")
	}
	taskVariablesBefore := task.TaskVariables
	mergedTaskVariables := mergeBPMNTaskCompletionVariables(taskVariablesBefore, variables)
	updatedTask, err := client.ProcessTask.Update().Where(
		processtask.ID(task.ID),
		processtask.TenantID(instance.TenantID),
		processtask.StatusNEQ(common.ProcessTaskStatusCompleted),
		processtask.StatusNEQ(common.ProcessTaskStatusCancelled),
	).SetStatus(common.ProcessTaskStatusCompleted).
		SetCompletedTime(time.Now()).SetTaskVariables(mergedTaskVariables).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新任务状态失败: %w", err)
	}
	if updatedTask != 1 {
		return nil, common.NewConflictError("process task completion", "task was concurrently completed")
	}
	instance.Variables = merged
	instance.Version++
	task.Status = common.ProcessTaskStatusCompleted
	task.TaskVariables = mergedTaskVariables
	txEngine := e.forClient(client, executionKeys)
	if err := txEngine.executeStep(ctx, instance, process, task.TaskDefinitionKey, merged); err != nil {
		return nil, err
	}
	if err := txEngine.recordApprovalDecisionWithClient(ctx, client, instance, task, variables); err != nil {
		return nil, err
	}
	actorID, actorName, metadata, err := e.completionAuditActor(ctx, client, task)
	if err != nil {
		return nil, err
	}
	if err := e.auditService.ForClient(client).RecordTaskCompletedWithMetadata(
		ctx, task, actorID, actorName, taskVariablesBefore, mergedTaskVariables, metadata,
	); err != nil {
		return nil, err
	}
	effect := &completedTaskEffect{task: task}
	if descriptor.HandlerID == bpmnNoUserTaskCallbackHandlerID {
		return effect, nil
	}
	if isAsyncHandler(handler) {
		callbackVariables[bpmnMetaDataAction] = descriptor.Action
		effect.variables = callbackVariables
		effect.asyncHandler = handler
		return effect, nil
	}
	if err := txEngine.enqueueUserTaskCallback(ctx, task, descriptor, callbackVariables); err != nil {
		return nil, err
	}
	return effect, nil
}

func (e *CustomProcessEngine) forClient(client *ent.Client, executionKeys *[]string) *CustomProcessEngine {
	clone := *e
	clone.client = client
	clone.groupResolver = bpmn.NewGroupResolver(client)
	clone.participationResolver = newBPMNParticipationResolver(client, clone.groupResolver)
	clone.auditService = e.auditService.ForClient(client)
	clone.callbackExecutionKeys = executionKeys
	clone.taskService = &bpmnTaskService{
		client:                client,
		logger:                clone.logger,
		groupResolver:         clone.groupResolver,
		participationResolver: clone.participationResolver,
		engine:                &clone,
	}
	return &clone
}

func (e *CustomProcessEngine) completionAuditActor(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (int, string, map[string]interface{}, error) {
	if metadata, ok := internalCascadeAuditMetadata(ctx); ok {
		return 0, "system", metadata, nil
	}
	if _, present := bpmnAccessScopeValue(ctx); present {
		scope, err := BPMNAccessScopeFromContext(ctx)
		if err != nil {
			return 0, "", nil, err
		}
		actor, err := loadTaskMutationActor(ctx, client, scope)
		if err != nil {
			return 0, "", nil, err
		}
		return actor.ID, actor.Name, nil, nil
	}
	if actor, ok := ctx.Value("user").(*ent.User); ok && actor.ID > 0 {
		return actor.ID, actor.Name, nil, nil
	}
	if userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); userID > 0 {
		actor, err := client.User.Query().Where(user.ID(userID), user.TenantID(task.TenantID)).Only(ctx)
		if err != nil {
			return 0, "", nil, fmt.Errorf("获取任务完成用户失败: %w", err)
		}
		return actor.ID, actor.Name, nil, nil
	}
	return 0, "", nil, nil
}

func (e *CustomProcessEngine) enqueueUserTaskCallback(ctx context.Context, task *ent.ProcessTask, descriptor bpmnCallbackDescriptor, variables map[string]interface{}) error {
	row, err := e.callbackOutbox.enqueue(ctx, e.client, bpmnCallbackEnqueueRequest{
		TenantID: task.TenantID, ProcessInstanceID: task.ProcessInstanceID,
		ProcessTaskID: task.ID, TaskID: task.TaskID,
		CallbackKind: "user_task_callback", HandlerID: descriptor.HandlerID,
		TaskType: descriptor.TaskType, ElementID: task.TaskDefinitionKey,
		Action: descriptor.Action, ConfigRef: descriptor.ConfigRef, Variables: variables,
	})
	if err != nil {
		return fmt.Errorf("enqueue user task callback failed")
	}
	e.collectCallbackExecutionKey(row.ExecutionKey)
	return nil
}

func (e *CustomProcessEngine) resolveCallbackDescriptorHandler(descriptor bpmnCallbackDescriptor) bpmn.ServiceTaskHandlerInterface {
	if descriptor.HandlerID == "" || descriptor.HandlerID == bpmnNoUserTaskCallbackHandlerID || descriptor.HandlerID == bpmnUnresolvedUserTaskCallbackHandlerID {
		return nil
	}
	handler := e.callbackRegistry.GetHandler(descriptor.HandlerID)
	if handler == nil || handler.GetHandlerID() != descriptor.HandlerID || handler.GetTaskType() != descriptor.TaskType {
		return nil
	}
	return handler
}

func (e *CustomProcessEngine) executeAsyncUserTaskCompletion(ctx context.Context, effect *completedTaskEffect) {
	if effect == nil || effect.asyncHandler == nil {
		return
	}
	_, _ = effect.asyncHandler.Execute(ctx, effect.task, effect.variables)
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

// isAsyncHandler reports whether handler opts into the pause/resume execution
// semantics (bpmn.AsyncServiceTaskHandler, IsAsync()==true) instead of the
// default synchronous Execute-then-advance behavior. Both the pause decision
// (handleElement's two serviceTask resolution paths) and the authorization
// decision (authorizeTaskActor) must use this exact same check against a
// handler resolved via findHandlerByTaskType — otherwise the two gates can
// key on different things and diverge.
func isAsyncHandler(handler bpmn.ServiceTaskHandlerInterface) bool {
	asyncHandler, ok := handler.(bpmn.AsyncServiceTaskHandler)
	return ok && asyncHandler.IsAsync()
}

func (e *CustomProcessEngine) recordApprovalDecision(ctx context.Context, instance *ent.ProcessInstance, task *ent.ProcessTask, variables map[string]interface{}) error {
	return e.recordApprovalDecisionWithClient(ctx, e.client, instance, task, variables)
}

func (e *CustomProcessEngine) recordApprovalDecisionWithClient(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance, task *ent.ProcessTask, variables map[string]interface{}) error {
	action, _ := variables["approvalAction"].(string)
	if action == "" {
		return nil
	}
	decision, _ := variables["approvalResult"].(string)
	comment, _ := variables["approvalComment"].(string)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if _, scopePresent := bpmnAccessScopeValue(ctx); scopePresent {
		scope, err := BPMNAccessScopeFromContext(ctx)
		if err != nil {
			return err
		}
		if scope.TenantID != instance.TenantID {
			return common.NewForbiddenError("审批操作人与流程实例租户不一致")
		}
		actorID = scope.UserID
	}
	if actorID <= 0 {
		return fmt.Errorf("审批决策缺少认证操作人")
	}
	actorName := ""
	if actor, err := client.User.Query().Where(user.ID(actorID), user.TenantID(instance.TenantID)).Only(ctx); err == nil {
		actorName = actor.Name
	}
	businessType := instance.BusinessType
	businessID := ""
	if instance.BusinessID > 0 {
		businessID = strconv.Itoa(instance.BusinessID)
	}
	_, err := client.ProcessApprovalDecision.Create().
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

// kafAutomationRole 是 KAF 自动化账号在 ent.User.Role 上的取值。KAF 与 ITSM
// 是同一应用的不同模块，不引入独立的技术账号/scoped-token 体系——KAF 以真实
// ITSM 用户身份（绑定这个角色）调用 API，走跟其他调用方相同的认证中间件。
const kafAutomationRole = "kaf_automation"

// authorizeTaskActor ensures ordinary task mutations always carry a validated,
// tenant-bound actor scope. The narrow typed CAB cascade is the only actorless
// exception.
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	return e.authorizeTaskActorWithClient(ctx, e.client, task)
}

func (e *CustomProcessEngine) authorizeTaskActorWithClient(ctx context.Context, client *ent.Client, task *ent.ProcessTask) error {
	if internal, err := authorizeInternalCascadeTask(ctx, client, task); internal {
		return err
	}
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	if scope.TenantID != task.TenantID {
		return common.NewForbiddenError("任务不属于当前租户")
	}
	callbackTaskType := task.CallbackTaskType
	if callbackTaskType == "" {
		callbackTaskType = task.TaskType
	}
	if handler := e.findHandlerByTaskType(callbackTaskType); handler != nil && isAsyncHandler(handler) {
		return e.authorizeKafAutomationActorWithClient(ctx, client, task, scope)
	}
	if scope.CanUpdateAllTasks {
		return nil
	}
	return e.authorizeTaskParticipantWithClient(ctx, client, task, scope)
}

func bpmnTaskMutationTenant(ctx context.Context) (int, error) {
	if internal, ok := ctx.Value(bpmnInternalCascadeContextKey{}).(bpmnInternalCascadeContext); ok {
		if internal.TenantID > 0 {
			return internal.TenantID, nil
		}
	}
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scope.TenantID, nil
}

func (e *CustomProcessEngine) authorizeTaskParticipantWithClient(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope) error {
	resolver := e.participationResolver
	if resolver == nil {
		resolver = newBPMNParticipationResolver(e.client, e.groupResolver)
	}
	resolver = resolver.forClient(client)
	actor, err := resolver.resolveActor(ctx, scope)
	if err == nil && resolver.matchesTask(task, actor) {
		if purpose, _ := task.TaskVariables["taskPurpose"].(string); purpose == "approval" {
			instance, instanceErr := client.ProcessInstance.Query().Where(
				processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID),
			).Only(ctx)
			if instanceErr != nil {
				return common.NewForbiddenError("无法验证审批任务申请人")
			}
			if requesterID, ok := numericInt(instance.Variables["requester_id"]); ok && requesterID == scope.UserID {
				return common.NewForbiddenError("申请人不能审批自己的任务")
			}
		}
		return nil
	}
	return common.NewForbiddenError("当前用户不是该任务的审批人或候选人")
}

// authorizeKafAutomationActor 校验异步委派任务（如 kaf_delegate）只能被 kaf_automation
// 角色的账号完成，任务必须处于 delegated 状态，且账号所属租户必须与任务所属租户一致。
// assignee/candidateUsers 对机器完成的任务没有意义——同一租户下所有委派任务都由
// 同一个账号处理，不存在"候选人"概念。无用户上下文时直接拒绝，不复用人工任务分支
// "无上下文即放行"的口子：委派任务必须始终有明确的认证主体。
func (e *CustomProcessEngine) authorizeKafAutomationActor(ctx context.Context, task *ent.ProcessTask) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	return e.authorizeKafAutomationActorWithClient(ctx, e.client, task, scope)
}

func (e *CustomProcessEngine) authorizeKafAutomationActorWithClient(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope) error {
	actor, err := client.User.Query().Where(user.ID(scope.UserID), user.TenantID(scope.TenantID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("KAF 自动化账号不存在: %w", err)
	}
	// 角色比较做大小写/首尾空白归一化，跟 middleware.RequireRole（HTTP 层的角色门禁）
	// 保持一致，避免同一份数据在两层用不同的比较口径。
	if strings.ToLower(strings.TrimSpace(actor.Role)) != kafAutomationRole {
		return fmt.Errorf("当前账号不是 KAF 自动化账号，无权完成委派任务")
	}
	if actor.TenantID != task.TenantID {
		return fmt.Errorf("KAF 自动化账号与委派任务所属租户不一致，拒绝跨租户完成任务")
	}
	if task.Status != common.ProcessTaskStatusDelegated {
		return fmt.Errorf("委派任务当前状态不允许完成: %s", task.Status)
	}
	return nil
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
		return fmt.Errorf("流程节点 %s 没有出向顺序流且不是结束事件", currentElementID)
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
		// 优先按 metaData 里的 service_task_type/action 分发。UserTask callback
		// enqueue uses the same findHandlerByTaskType lookup,
		// 保证"模板声明了 service_task_type 就一定能找到对应 handler"这条规则
		// 在 UserTask 和 ServiceTask 两种节点类型上表现一致。
		if serviceTaskType := serviceTask.ServiceTaskType(); serviceTaskType != "" {
			if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
				if isAsyncHandler(handler) {
					return e.createDelegatedTask(ctx, instance, serviceTask, serviceTaskType)
				}
				callbackVars := mergeServiceTaskVariables(instance.Variables, serviceTask)
				return e.enqueueServiceTaskCallback(
					ctx, instance, handler, handler.GetTaskType(), elementID,
					serviceTask.ServiceTaskAction(), serviceTask.CallbackConfigRef(), callbackVars,
				)
			}
			return fmt.Errorf("ServiceTask %s 声明的处理器未注册", elementID)
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
			// resolvedTaskType 记录到底是 serviceRef 还是 GetType() 兜底值命中的 handler——
			// 如果这个节点解析出的 handler 是异步的，createDelegatedTask 需要把这个值原样
			// 写进 ProcessTask.TaskType/TaskVariables["service_task_type"]，这样后续
			// authorizeTaskActor（完成鉴权）和 UserTask callback enqueue 用
			// findHandlerByTaskType 重新查找时，能查到同一个 handler——三处必须用同一套
			// 查找口径，否则会出现 Important #2 那种鉴权跟暂停判断各自为政的分裂。
			resolvedTaskType := serviceRef
			handler := e.callbackRegistry.GetHandler(resolvedTaskType)
			if handler == nil {
				resolvedTaskType = serviceTask.GetType()
				handler = e.callbackRegistry.GetHandler(resolvedTaskType)
			}
			if handler != nil {
				if isAsyncHandler(handler) {
					return e.createDelegatedTask(ctx, instance, serviceTask, resolvedTaskType)
				}
				taskVariables := mergeServiceTaskVariables(instance.Variables, serviceTask)
				return e.enqueueServiceTaskCallback(
					ctx, instance, handler, handler.GetTaskType(), elementID,
					serviceTask.ServiceTaskAction(), serviceTask.CallbackConfigRef(), taskVariables,
				)
			}
		}
		return fmt.Errorf("ServiceTask %s 声明的处理器未注册", elementID)
	}

	return e.executeStep(ctx, instance, process, elementID, instance.Variables)
}

func (e *CustomProcessEngine) enqueueServiceTaskCallback(
	ctx context.Context,
	instance *ent.ProcessInstance,
	handler bpmn.ServiceTaskHandlerInterface,
	taskType string,
	elementID string,
	action string,
	configRef string,
	variables map[string]interface{},
) error {
	payload, err := filterBPMNCallbackPayload(handler, action, variables)
	if err != nil {
		return err
	}
	row, err := e.callbackOutbox.enqueue(ctx, e.client, bpmnCallbackEnqueueRequest{
		TenantID: instance.TenantID, ProcessInstanceID: instance.ID,
		CallbackKind: "service_task", HandlerID: handler.GetHandlerID(),
		TaskType: taskType, ElementID: elementID, Action: action, ConfigRef: configRef,
		Variables: payload,
	})
	if err != nil {
		return fmt.Errorf("enqueue service task callback failed")
	}
	if e.callbackExecutionKeys != nil {
		*e.callbackExecutionKeys = append(*e.callbackExecutionKeys, row.ExecutionKey)
		return nil
	}
	e.processCommittedCallbackKeys(ctx, instance.TenantID, []string{row.ExecutionKey})
	return nil
}

func (e *CustomProcessEngine) collectCallbackExecutionKey(executionKey string) {
	if e.callbackExecutionKeys != nil {
		*e.callbackExecutionKeys = append(*e.callbackExecutionKeys, executionKey)
	}
}

func (e *CustomProcessEngine) processCommittedCallbackKeys(ctx context.Context, tenantID int, executionKeys []string) {
	if len(executionKeys) == 0 || tenantID <= 0 {
		return
	}
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	_, err := e.callbackOutbox.processExecutionKeys(ctx, "bpmn-inline-"+uuid.NewString(), executionKeys)
	if err == nil {
		return
	}
	for _, executionKey := range executionKeys {
		row, queryErr := e.client.ProcessCallbackOutbox.Query().Where(
			processcallbackoutbox.ExecutionKey(executionKey),
			processcallbackoutbox.TenantID(tenantID),
		).Only(ctx)
		if queryErr == nil && row.Status == bpmnCallbackStatusCompleted {
			continue
		}
		callbackKind := ""
		attemptCount := 0
		errorClass := "unknown_error"
		if queryErr == nil {
			callbackKind = row.CallbackKind
			attemptCount = row.AttemptCount
			if isBPMNCallbackErrorClass(row.LastErrorClass) {
				errorClass = row.LastErrorClass
			}
		}
		e.logger.Warnw("BPMN callback attempt remains durable for retry",
			"execution_key", executionKey,
			"tenant_id", tenantID,
			"callback_kind", callbackKind,
			"attempt_count", attemptCount,
			"error_class", errorClass,
		)
	}
}

// ProcessPendingCallbacks performs one deterministic durable callback sweep.
func (e *CustomProcessEngine) ProcessPendingCallbacks(ctx context.Context, workerID string, limit int) (int, error) {
	if e.callbackOutbox == nil {
		return 0, fmt.Errorf("bpmn callback outbox is not configured")
	}
	return e.callbackOutbox.processPending(ctx, workerID, limit)
}

// RunCallbackOutboxWorker performs an immediate sweep and then polls until the
// caller cancels the lifecycle context.
func (e *CustomProcessEngine) RunCallbackOutboxWorker(ctx context.Context, workerID string, interval time.Duration) {
	if validateBPMNCallbackWorkerID(workerID) != nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	e.runCallbackOutboxSweep(ctx, workerID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runCallbackOutboxSweep(ctx, workerID)
		}
	}
}

func (e *CustomProcessEngine) runCallbackOutboxSweep(ctx context.Context, workerID string) {
	if _, err := e.ProcessPendingCallbacks(ctx, workerID, 50); err != nil {
		e.logger.Warnw("BPMN callback sweep incomplete",
			"worker_id", workerID,
			"error_class", "callback_sweep_error",
		)
	}
}

func (e *CustomProcessEngine) executeClaimedCallback(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox) (bpmnCallbackExecutionResult, error) {
	claimedRow, err := e.loadClaimedCallback(ctx, workerID, row)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	handler := e.resolveClaimedCallbackHandler(claimedRow)
	if handler == nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackHandlerError(errors.New("callback handler is unavailable"))
	}
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, claimedRow.TenantID)
	ctx = bpmn.WithBPMNCallbackExecutionKey(ctx, claimedRow.ExecutionKey)
	claimedRow.Variables, err = filterPersistedBPMNCallbackPayload(handler, claimedRow.Action, claimedRow.Variables)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackHandlerError(err)
	}
	claimedRow.Variables["bpmn_callback_execution_key"] = claimedRow.ExecutionKey
	claimedRow.Variables[bpmnMetaDataAction] = claimedRow.Action
	if claimedRow.ConfigRef != "" {
		claimedRow.Variables[bpmnMetaDataCallbackConfig] = claimedRow.ConfigRef
	}
	instance, err := e.client.ProcessInstance.Query().Where(
		processinstance.ID(claimedRow.ProcessInstanceID),
		processinstance.TenantID(claimedRow.TenantID),
	).Only(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	claimedRow.Variables, err = e.authoritativeCallbackVariables(ctx, instance, handler, claimedRow.Variables)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackHandlerError(err)
	}

	switch claimedRow.CallbackKind {
	case "service_task":
		return e.executeClaimedServiceTaskCallback(ctx, workerID, claimedRow, handler)
	case "user_task_callback":
		return e.executeClaimedUserTaskCallback(ctx, workerID, claimedRow, handler)
	default:
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(errors.New("unsupported callback kind"))
	}
}

func (e *CustomProcessEngine) resolveClaimedCallbackHandler(row *ent.ProcessCallbackOutbox) bpmn.ServiceTaskHandlerInterface {
	if e.callbackRegistry == nil || row == nil || row.TaskType == "" {
		return nil
	}
	if row.HandlerID == bpmnUnresolvedUserTaskCallbackHandlerID {
		return nil
	}

	handler := e.callbackRegistry.GetHandler(row.HandlerID)
	if handler == nil || handler.GetHandlerID() != row.HandlerID || handler.GetTaskType() != row.TaskType || isAsyncHandler(handler) {
		return nil
	}
	return handler
}

func (e *CustomProcessEngine) loadClaimedCallback(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox) (*ent.ProcessCallbackOutbox, error) {
	if row == nil || row.ID <= 0 || row.TenantID <= 0 {
		return nil, errors.New("callback identity is incomplete")
	}
	return e.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID),
		processcallbackoutbox.TenantID(row.TenantID),
		processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
		processcallbackoutbox.LeaseOwner(workerID),
	).Only(ctx)
}

func (e *CustomProcessEngine) executeClaimedServiceTaskCallback(
	ctx context.Context,
	workerID string,
	row *ent.ProcessCallbackOutbox,
	handler bpmn.ServiceTaskHandlerInterface,
) (bpmnCallbackExecutionResult, error) {
	if _, err := handler.Execute(ctx, nil, row.Variables); err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackHandlerError(err)
	}

	tx, err := e.client.Tx(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	defer func() { _ = tx.Rollback() }()
	txRow, err := tx.Client().ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID),
		processcallbackoutbox.TenantID(row.TenantID),
		processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
		processcallbackoutbox.LeaseOwner(workerID),
	).Only(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	instance, err := tx.Client().ProcessInstance.Query().Where(
		processinstance.ID(txRow.ProcessInstanceID),
		processinstance.TenantID(txRow.TenantID),
	).Only(ctx)
	if err != nil || instance.CurrentActivityID != txRow.ElementID {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	definition, err := tx.Client().ProcessDefinition.Query().Where(
		processdefinition.ID(instance.ProcessDefinitionID),
		processdefinition.TenantID(instance.TenantID),
	).Only(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	definitions, err := e.parser.ParseXML(definition.BpmnXML)
	if err != nil || len(definitions.Processes) == 0 {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	downstreamExecutionKeys := make([]string, 0)
	txEngine := e.forClient(tx.Client(), &downstreamExecutionKeys)
	if err := txEngine.executeStep(ctx, instance, definitions.Processes[0], txRow.ElementID, instance.Variables); err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	completed, err := e.callbackOutbox.completeWithClient(ctx, tx.Client(), workerID, txRow)
	if err != nil || !completed {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	if err := tx.Commit(); err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	return bpmnCallbackExecutionResult{CompletionCommitted: true}, nil
}

func (e *CustomProcessEngine) executeClaimedUserTaskCallback(
	ctx context.Context,
	workerID string,
	row *ent.ProcessCallbackOutbox,
	handler bpmn.ServiceTaskHandlerInterface,
) (bpmnCallbackExecutionResult, error) {
	task, err := e.client.ProcessTask.Query().Where(
		processtask.ID(row.ProcessTaskID),
		processtask.TenantID(row.TenantID),
		processtask.ProcessInstanceID(row.ProcessInstanceID),
		processtask.TaskID(row.TaskID),
		processtask.TaskDefinitionKey(row.ElementID),
		processtask.Status(common.ProcessTaskStatusCompleted),
	).Only(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	if _, err := handler.Execute(ctx, task, row.Variables); err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackHandlerError(err)
	}

	tx, err := e.client.Tx(ctx)
	if err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	defer func() { _ = tx.Rollback() }()
	completed, err := e.callbackOutbox.completeWithClient(ctx, tx.Client(), workerID, row)
	if err != nil || !completed {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	if err := tx.Commit(); err != nil {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackAdvanceError(err)
	}
	return bpmnCallbackExecutionResult{CompletionCommitted: true}, nil
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
	descriptor := e.callbackDescriptor(task.ServiceTaskType(), task.ServiceTaskAction(), task.CallbackConfigRef())
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
		SetCallbackHandlerID(descriptor.HandlerID).
		SetCallbackTaskType(descriptor.TaskType).
		SetCallbackAction(descriptor.Action).
		SetCallbackConfigRef(descriptor.ConfigRef).
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
			actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
			if _, err := e.taskService.createCounterSignTasksWithClient(ctx, e.client, createdTask, &CounterSignRequest{ApprovalType: approvalType, Approvers: approvers, Threshold: threshold}, actorID); err != nil {
				return fmt.Errorf("创建会签任务失败: %w", err)
			}
		}
	}
	e.logger.Infow("User task created with auto-assignment", "taskID", task.ID, "taskName", task.Name, "assignee", assignee)
	return nil
}

// createDelegatedTask 为声明了异步 handler 的 serviceTask 节点创建 ProcessTask 并暂停流程：
// 不调用 handler.Execute、不推进 executeStep，流程实例的 CurrentActivityID（handleElement
// 顶部已设置）停在这个节点，直到外部通过 CompleteTask 显式完成该任务才会继续。
//
// taskType 是调用方（handleElement 的两条 serviceTask 解析路径）实际用来
// findHandlerByTaskType 命中这个异步 handler 的那个字符串——metaData 路径下是
// serviceTask.ServiceTaskType()，legacy 属性猜测路径下是命中的 serviceRef/GetType()
// 兜底值。必须原样传入（而不是在这里重新调用 ServiceTaskType()，legacy 路径下那会是
// 空串），因为 ProcessTask.TaskType/TaskVariables["service_task_type"] 之后要分别喂给
// authorizeTaskActor 和异步完成分发重新做 findHandlerByTaskType 查找——三处用的必须是
// 同一个能查到同一个 handler 的字符串。
func (e *CustomProcessEngine) createDelegatedTask(ctx context.Context, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask, taskType string) error {
	descriptor := e.callbackDescriptor(taskType, serviceTask.ServiceTaskAction(), serviceTask.CallbackConfigRef())
	if descriptor.HandlerID == bpmnUnresolvedUserTaskCallbackHandlerID || descriptor.HandlerID == bpmnNoUserTaskCallbackHandlerID {
		return fmt.Errorf("异步 ServiceTask %s 的回调描述符无法解析", serviceTask.ID)
	}
	_, err := e.client.ProcessTask.Create().
		SetTaskID(fmt.Sprintf("TASK-%s-%d", serviceTask.ID, time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey(serviceTask.ID).
		SetTaskName(serviceTask.Name).
		SetTaskType(taskType).
		SetStatus(common.ProcessTaskStatusDelegated).
		SetTaskVariables(map[string]interface{}{}).
		SetCallbackHandlerID(descriptor.HandlerID).
		SetCallbackTaskType(descriptor.TaskType).
		SetCallbackAction(descriptor.Action).
		SetCallbackConfigRef(descriptor.ConfigRef).
		SetTenantID(instance.TenantID).
		SetCreatedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("创建委派任务失败: %w", err)
	}
	e.logger.Infow("异步 ServiceTask 已暂停，等待外部完成",
		"elementID", serviceTask.ID, "serviceTaskType", taskType, "instanceID", instance.ProcessInstanceID)
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

func requireProcessInstanceUpdateScope(ctx context.Context) (BPMNAccessScope, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return BPMNAccessScope{}, err
	}
	if !scope.CanUpdateAllInstances {
		return BPMNAccessScope{}, common.NewForbiddenError("无权修改流程实例")
	}
	return scope, nil
}

func loadProcessInstanceMutationActor(ctx context.Context, client *ent.Client, scope BPMNAccessScope) (*ent.User, error) {
	actor, err := client.User.Query().
		Where(
			user.ID(scope.UserID),
			user.TenantID(scope.TenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例操作用户失败: %w", err)
	}
	return actor, nil
}

func (e *CustomProcessEngine) SuspendProcess(ctx context.Context, processInstanceID string, reason string) error {
	scope, err := requireProcessInstanceUpdateScope(ctx)
	if err != nil {
		return err
	}
	instance, err := e.processInstanceService.loadProcessInstance(ctx, processInstanceID, scope.TenantID)
	if err != nil {
		return err
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启暂停流程事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadProcessInstanceMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}

	_, err = tx.Client().ProcessInstance.UpdateOne(instance).
		SetStatus("suspended").
		SetSuspendedTime(time.Now()).
		SetSuspendedReason(reason).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("暂停流程实例失败: %w", err)
	}
	if err := e.auditService.ForClient(tx.Client()).RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeUserTask,
		Action:               AuditActionProcessSuspended,
		UserID:               actor.ID,
		UserName:             actor.Name,
		VariablesBefore:      map[string]interface{}{"status": instance.Status},
		VariablesAfter:       map[string]interface{}{"status": "suspended"},
		Comment:              reason,
		TenantID:             instance.TenantID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交暂停流程事务失败: %w", err)
	}
	return nil
}

func (e *CustomProcessEngine) ResumeProcess(ctx context.Context, processInstanceID string) error {
	scope, err := requireProcessInstanceUpdateScope(ctx)
	if err != nil {
		return err
	}
	instance, err := e.processInstanceService.loadProcessInstance(ctx, processInstanceID, scope.TenantID)
	if err != nil {
		return err
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启恢复流程事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadProcessInstanceMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}

	_, err = tx.Client().ProcessInstance.UpdateOne(instance).
		SetStatus("running").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("恢复流程实例失败: %w", err)
	}
	if err := e.auditService.ForClient(tx.Client()).RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeUserTask,
		Action:               AuditActionProcessResumed,
		UserID:               actor.ID,
		UserName:             actor.Name,
		VariablesBefore:      map[string]interface{}{"status": instance.Status},
		VariablesAfter:       map[string]interface{}{"status": "running"},
		TenantID:             instance.TenantID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交恢复流程事务失败: %w", err)
	}
	return nil
}

func (e *CustomProcessEngine) TerminateProcess(ctx context.Context, processInstanceID string, reason string) error {
	scope, err := requireProcessInstanceUpdateScope(ctx)
	if err != nil {
		return err
	}
	instance, err := e.processInstanceService.loadProcessInstance(ctx, processInstanceID, scope.TenantID)
	if err != nil {
		return err
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启终止流程事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadProcessInstanceMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	terminatedAt := time.Now()

	_, err = tx.Client().ProcessInstance.UpdateOne(instance).
		SetStatus("terminated").
		SetEndTime(terminatedAt).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("终止流程实例失败: %w", err)
	}
	_, err = tx.Client().ProcessTask.Update().
		Where(
			processtask.ProcessInstanceID(instance.ID),
			processtask.TenantID(scope.TenantID),
		).
		Where(processtask.StatusNEQ("completed")).
		Where(processtask.StatusNEQ("cancelled")).
		SetStatus("cancelled").
		SetCompletedTime(terminatedAt).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("取消流程任务失败: %w", err)
	}
	if err := e.auditService.ForClient(tx.Client()).RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeEndEvent,
		Action:               AuditActionProcessTerminated,
		UserID:               actor.ID,
		UserName:             actor.Name,
		VariablesBefore:      map[string]interface{}{"status": instance.Status},
		VariablesAfter:       map[string]interface{}{"status": "terminated"},
		Comment:              reason,
		TenantID:             instance.TenantID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交终止流程事务失败: %w", err)
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
	TenantID         int                    `json:"-" form:"-"`
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
	TenantID int    `json:"-" form:"-"`
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
	Assignee        string `json:"assignee" form:"assignee"`
	CandidateUsers  string `json:"candidateUsers" form:"candidateUsers"`
	CandidateGroups string `json:"candidateGroups" form:"candidateGroups"`
	// UserID 为「我的待办」语义：查询“分配给我 OR 我在候选人 OR 我所在组作为候选组”的任务。
	// 传入后：Assignee/CandidateUsers/CandidateGroups 会被忽略（可选透传）。
	UserID               int    `json:"userId" form:"userId"`
	Status               string `json:"status" form:"status"`
	ProcessDefinitionKey string `json:"processDefinitionKey" form:"processDefinitionKey"`
	ProcessInstanceID    int    `json:"processInstanceId" form:"processInstanceId"`
	TenantID             int    `json:"tenantId" form:"tenantId"`
	Page                 int    `json:"page" form:"page"`
	PageSize             int    `json:"pageSize" form:"pageSize"`
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
	if req == nil {
		return nil, common.NewValidationError("流程定义请求不能为空", nil)
	}
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// 首先检查或创建 ProcessDeployment
	var deployment *ent.ProcessDeployment
	existingDeployments, err := s.client.ProcessDeployment.Query().
		Where(processdeployment.TenantID(tenantID)).
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
			SetTenantID(tenantID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建部署记录失败: %w", err)
		}
	}

	// 获取当前最高版本号
	nextVersion := s.getNextVersion(ctx, req.Key, tenantID)

	// 将旧版本标记为非最新
	existing, err := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(req.Key)).
		Where(processdefinition.IsLatest(true)).
		Where(processdefinition.TenantID(tenantID)).
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
		SetTenantID(tenantID).
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
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(key)).
		Where(processdefinition.Version(version)).
		Where(processdefinition.TenantID(tenantID))
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	return definition, nil
}

// GetProcessDefinitionByID 根据ID获取流程定义
func (s *bpmnProcessDefinitionService) GetProcessDefinitionByID(ctx context.Context, id int) (*ent.ProcessDefinition, error) {
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.ID(id)).
		Where(processdefinition.TenantID(tenantID))
	definition, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	return definition, nil
}

func (s *bpmnProcessDefinitionService) GetLatestProcessDefinition(ctx context.Context, key string) (*ent.ProcessDefinition, error) {
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(key)).
		Where(processdefinition.IsLatest(true)).
		Where(processdefinition.TenantID(tenantID))
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
	if req == nil {
		return nil, 0, common.NewValidationError("流程定义列表请求不能为空", nil)
	}
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := s.client.ProcessDefinition.Query().Where(processdefinition.TenantID(tenantID))

	if req.Key != "" {
		query = query.Where(processdefinition.Key(req.Key))
	}
	if req.Category != "" {
		query = query.Where(processdefinition.Category(req.Category))
	}
	if req.IsActive != nil {
		query = query.Where(processdefinition.IsActive(*req.IsActive))
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
	client                *ent.Client
	logger                *zap.SugaredLogger
	participationResolver *bpmnParticipationResolver
	auditService          *BPMNAuditService
}

func (s *bpmnProcessInstanceService) loadProcessInstance(ctx context.Context, instanceKey string, tenantID int) (*ent.ProcessInstance, error) {
	if tenantID <= 0 {
		return nil, common.NewForbiddenError("缺少 BPMN 实例租户上下文")
	}
	var instancePredicate predicate.ProcessInstance
	if id, err := strconv.Atoi(instanceKey); err == nil {
		instancePredicate = processinstance.ID(id)
	} else {
		instancePredicate = processinstance.ProcessInstanceID(instanceKey)
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(
			processinstance.TenantID(tenantID),
			instancePredicate,
		).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}
	return instance, nil
}

func (s *bpmnProcessInstanceService) authorizeProcessInstanceRead(ctx context.Context, instance *ent.ProcessInstance, scope BPMNAccessScope) error {
	if instance == nil || instance.TenantID != scope.TenantID {
		return common.NewForbiddenError("无权读取流程实例")
	}
	if scope.CanReadAllInstances || instance.Initiator == strconv.Itoa(scope.UserID) {
		return nil
	}
	if s.participationResolver == nil {
		return common.NewForbiddenError("无权读取流程实例")
	}
	actor, err := s.participationResolver.resolveActor(ctx, scope)
	if err != nil {
		return common.NewForbiddenError("无权读取流程实例")
	}
	instanceIDs, err := s.participationResolver.participatingInstanceIDs(ctx, actor)
	if err != nil {
		return fmt.Errorf("解析流程实例参与范围失败: %w", err)
	}
	for _, instanceID := range instanceIDs {
		if instanceID == instance.ID {
			return nil
		}
	}
	return common.NewForbiddenError("无权读取流程实例")
}

func (s *bpmnProcessInstanceService) GetProcessInstance(ctx context.Context, processInstanceID string) (*ent.ProcessInstance, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	instance, err := s.loadProcessInstance(ctx, processInstanceID, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProcessInstanceRead(ctx, instance, scope); err != nil {
		return nil, err
	}
	return instance, nil
}

func (s *bpmnProcessInstanceService) ListProcessInstances(ctx context.Context, req *ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	if req.TenantID > 0 && req.TenantID != scope.TenantID {
		return nil, 0, common.NewForbiddenError("无权读取其他租户的流程实例")
	}
	query := s.client.ProcessInstance.Query().Where(processinstance.TenantID(scope.TenantID))

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processinstance.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.Status != "" {
		query = query.Where(processinstance.Status(req.Status))
	}
	if req.BusinessKey != "" {
		query = query.Where(processinstance.BusinessKey(req.BusinessKey))
	}
	if !scope.CanReadAllInstances {
		if s.participationResolver == nil {
			return nil, 0, common.NewForbiddenError("无权读取流程实例")
		}
		actor, resolveErr := s.participationResolver.resolveActor(ctx, scope)
		if resolveErr != nil {
			return nil, 0, common.NewForbiddenError("无权读取流程实例")
		}
		participatingIDs, participationErr := s.participationResolver.participatingInstanceIDs(ctx, actor)
		if participationErr != nil {
			return nil, 0, fmt.Errorf("解析流程实例参与范围失败: %w", participationErr)
		}
		initiatorPredicate := processinstance.Initiator(strconv.Itoa(scope.UserID))
		if len(participatingIDs) == 0 {
			query = query.Where(initiatorPredicate)
		} else {
			query = query.Where(processinstance.Or(
				initiatorPredicate,
				processinstance.IDIn(participatingIDs...),
			))
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
	scope, err := requireProcessInstanceUpdateScope(ctx)
	if err != nil {
		return err
	}
	instance, err := s.loadProcessInstance(ctx, processInstanceID, scope.TenantID)
	if err != nil {
		return err
	}

	for _, reserved := range reservedInstanceVariableKeys {
		if _, exists := variables[reserved]; exists {
			return fmt.Errorf("变量 %q 由流程触发方管理，不允许经此端点覆盖", reserved)
		}
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启流程变量事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadProcessInstanceMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	_, err = tx.Client().ProcessInstance.UpdateOne(instance).
		SetVariables(variables).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("设置流程实例变量失败: %w", err)
	}
	if err := s.auditService.ForClient(tx.Client()).RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           "variable",
		ActivityName:         "流程变量变更",
		ActivityType:         ActivityTypeServiceTask,
		Action:               AuditActionVariableChanged,
		UserID:               actor.ID,
		UserName:             actor.Name,
		VariablesBefore:      instance.Variables,
		VariablesAfter:       variables,
		TenantID:             instance.TenantID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交流程变量事务失败: %w", err)
	}
	return nil
}

func (s *bpmnProcessInstanceService) GetProcessInstanceHistory(ctx context.Context, processInstanceID string) ([]*ent.ProcessExecutionHistory, error) {
	instance, err := s.GetProcessInstance(ctx, processInstanceID)
	if err != nil {
		return nil, err
	}

	query := s.client.ProcessExecutionHistory.Query().
		Where(
			processexecutionhistory.ProcessInstanceID(instance.ID),
			processexecutionhistory.TenantID(instance.TenantID),
		)

	history, err := query.Order(ent.Asc(processexecutionhistory.FieldTimestamp)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例历史失败: %w", err)
	}

	return history, nil
}

// GetInstanceStatistics 获取实例统计
func (s *bpmnProcessInstanceService) GetInstanceStatistics(ctx context.Context, req *InstanceStatisticsRequest) (*InstanceStatistics, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if !scope.CanReadAllInstances {
		return nil, common.NewForbiddenError("无权读取流程实例统计")
	}
	req.TenantID = scope.TenantID
	query := s.client.ProcessInstance.Query().Where(processinstance.TenantID(req.TenantID))

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processinstance.ProcessDefinitionKey(req.ProcessDefinitionKey))
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
	client                *ent.Client
	logger                *zap.SugaredLogger
	groupResolver         *bpmn.GroupResolver
	participationResolver *bpmnParticipationResolver
	// engine 是创建本任务服务的那个引擎实例。任何需要推进流程（CompleteTask）或复用引擎
	// 内部鉴权/审批记录逻辑（authorizeTaskActor / recordApprovalDecision）的方法都必须用它，
	// 不能 NewCustomProcessEngine 现造——见 NewCustomProcessEngine 里的说明。
	engine *CustomProcessEngine
}

func (s *bpmnTaskService) loadTaskByKey(ctx context.Context, taskID string, tenantID int) (*ent.ProcessTask, error) {
	if tenantID <= 0 {
		return nil, common.NewForbiddenError("缺少 BPMN 任务租户上下文")
	}
	task, err := s.client.ProcessTask.Query().
		Where(processtask.TaskID(taskID), processtask.TenantID(tenantID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	return task, nil
}

func (s *bpmnTaskService) loadTaskByID(ctx context.Context, id, tenantID int) (*ent.ProcessTask, error) {
	if tenantID <= 0 {
		return nil, common.NewForbiddenError("缺少 BPMN 任务租户上下文")
	}
	task, err := s.client.ProcessTask.Query().
		Where(processtask.ID(id), processtask.TenantID(tenantID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	return task, nil
}

func (s *bpmnTaskService) authorizeTaskRead(ctx context.Context, task *ent.ProcessTask, scope BPMNAccessScope) error {
	if task == nil || task.TenantID != scope.TenantID {
		return common.NewForbiddenError("无权读取任务")
	}
	if scope.CanReadAllTasks {
		return nil
	}
	if s.participationResolver == nil {
		return common.NewForbiddenError("无权读取任务")
	}
	actor, err := s.participationResolver.resolveActor(ctx, scope)
	if err != nil || !s.participationResolver.matchesTask(task, actor) {
		return common.NewForbiddenError("无权读取任务")
	}
	return nil
}

func (s *bpmnTaskService) authorizeTaskUpdate(ctx context.Context, task *ent.ProcessTask) (BPMNAccessScope, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return BPMNAccessScope{}, err
	}
	if task == nil || task.TenantID != scope.TenantID {
		return BPMNAccessScope{}, common.NewNotFoundError("process task")
	}
	if scope.CanUpdateAllTasks {
		return scope, nil
	}
	if s.participationResolver == nil {
		return BPMNAccessScope{}, common.NewForbiddenError("无权操作该流程任务")
	}
	actor, err := s.participationResolver.resolveActor(ctx, scope)
	if err != nil || !s.participationResolver.matchesTask(task, actor) {
		return BPMNAccessScope{}, common.NewForbiddenError("无权操作该流程任务")
	}
	return scope, nil
}

func loadTaskMutationActor(ctx context.Context, client *ent.Client, scope BPMNAccessScope) (*ent.User, error) {
	actor, err := client.User.Query().
		Where(user.ID(scope.UserID), user.TenantID(scope.TenantID), user.Active(true)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务操作用户失败: %w", err)
	}
	return actor, nil
}

func resolveTaskAssignee(ctx context.Context, client *ent.Client, tenantID int, identifier string) (*ent.User, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, fmt.Errorf("任务处理人不能为空")
	}
	predicates := []predicate.User{user.UsernameEqualFold(identifier), user.EmailEqualFold(identifier)}
	if userID, err := strconv.Atoi(identifier); err == nil && userID > 0 {
		predicates = append(predicates, user.ID(userID))
	}
	assignee, err := client.User.Query().
		Where(user.TenantID(tenantID), user.Active(true), user.Or(predicates...)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("任务处理人不存在: %w", err)
	}
	return assignee, nil
}

// GetTask 根据任务ID (BPMN标准task_id字符串)获取任务
func (s *bpmnTaskService) GetTask(ctx context.Context, taskID string) (*ent.ProcessTask, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	task, err := s.loadTaskByKey(ctx, taskID, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeTaskRead(ctx, task, scope); err != nil {
		return nil, err
	}
	return task, nil
}

// GetTaskByID 根据数据库自增ID获取任务
func (s *bpmnTaskService) GetTaskByID(ctx context.Context, id int) (*ent.ProcessTask, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	task, err := s.loadTaskByID(ctx, id, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeTaskRead(ctx, task, scope); err != nil {
		return nil, err
	}
	return task, nil
}

// CompleteTaskByID 根据数据库自增ID完成任务
func (s *bpmnTaskService) CompleteTaskByID(ctx context.Context, id int, variables map[string]interface{}) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	task, err := s.loadTaskByID(ctx, id, scope.TenantID)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	return s.engine.CompleteTask(ctx, task.TaskID, variables)
}

func (s *bpmnTaskService) ListUserTasks(ctx context.Context, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	req.TenantID = scope.TenantID
	s.logger.Debugw("ListUserTasks called", "assignee", req.Assignee, "userID", req.UserID, "tenantID", req.TenantID)
	query := s.client.ProcessTask.Query().Where(processtask.TenantID(scope.TenantID))

	var actor *bpmnActorIdentity
	if !scope.CanReadAllTasks {
		if s.participationResolver == nil {
			return nil, 0, common.NewForbiddenError("无权读取任务")
		}
		actor, err = s.participationResolver.resolveActor(ctx, scope)
		if err != nil {
			return nil, 0, common.NewForbiddenError("无权读取任务")
		}
	} else if req.UserID > 0 {
		requestedScope := scope
		requestedScope.UserID = req.UserID
		actor, err = s.participationResolver.resolveActor(ctx, requestedScope)
		if err != nil {
			return nil, 0, fmt.Errorf("解析任务用户范围失败: %w", err)
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
	if actor != nil {
		prefilter := make([]predicate.ProcessTask, 0, len(actor.UserTokens)*2+len(actor.GroupTokens))
		for token := range actor.UserTokens {
			prefilter = append(prefilter,
				processtask.AssigneeContainsFold(token),
				processtask.CandidateUsersContainsFold(token),
			)
		}
		for token := range actor.GroupTokens {
			prefilter = append(prefilter, processtask.CandidateGroupsContainsFold(token))
		}
		query = query.Where(processtask.Or(prefilter...))
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
	if actor == nil {
		total, err := query.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("获取任务总数失败: %w", err)
		}
		if req.Page > 0 && req.PageSize > 0 {
			query = query.Offset((req.Page - 1) * req.PageSize).Limit(req.PageSize)
		}
		tasks, err := query.Order(ent.Desc(processtask.FieldCreatedTime)).All(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("获取任务列表失败: %w", err)
		}
		return tasks, total, nil
	}

	tasks, err := query.Order(ent.Desc(processtask.FieldCreatedTime)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取任务列表失败: %w", err)
	}
	if actor != nil {
		filtered := make([]*ent.ProcessTask, 0, len(tasks))
		for _, task := range tasks {
			if s.participationResolver.matchesTask(task, actor) {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}
	total := len(tasks)
	if req.Page > 0 && req.PageSize > 0 {
		start := (req.Page - 1) * req.PageSize
		if start >= total {
			tasks = nil
		} else {
			end := start + req.PageSize
			if end > total {
				end = total
			}
			tasks = tasks[start:end]
		}
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
			Where(processinstance.IDIn(instanceIDs...), processinstance.TenantID(req.TenantID)).
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
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	instanceService := &bpmnProcessInstanceService{
		client:                s.client,
		participationResolver: s.participationResolver,
	}
	instance, err := instanceService.loadProcessInstance(ctx, processInstanceKey, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if err := instanceService.authorizeProcessInstanceRead(ctx, instance, scope); err != nil {
		return nil, err
	}
	return s.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.ProcessInstanceKey(instance.ProcessInstanceID),
			processapprovaldecision.TenantID(scope.TenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
}

func (s *bpmnTaskService) AssignTask(ctx context.Context, taskID string, assignee string) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	task, err := s.loadTaskByKey(ctx, taskID, scope.TenantID)
	if err != nil {
		return err
	}
	if _, err := s.authorizeTaskUpdate(ctx, task); err != nil {
		return err
	}
	assigneeUser, err := resolveTaskAssignee(ctx, s.client, scope.TenantID, assignee)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务分配事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	_, err = tx.Client().ProcessTask.UpdateOne(task).
		SetAssignee(strconv.Itoa(assigneeUser.ID)).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("分配任务失败: %w", err)
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskAssigned(ctx, task, assigneeUser, actor.ID, actor.Name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务分配事务失败: %w", err)
	}
	return nil
}

// ClaimTask 认领任务 (根据task_id字符串)
func (s *bpmnTaskService) ClaimTask(ctx context.Context, taskID string, userID string) error {
	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		return fmt.Errorf("无效的用户ID")
	}
	claimCtx, tenantID, err := taskClaimContext(ctx, uid)
	if err != nil {
		return err
	}
	return s.claimTask(claimCtx, tenantID, uid, func(client *ent.Client) (*ent.ProcessTask, error) {
		query := client.ProcessTask.Query().Where(processtask.TaskID(taskID), processtask.TenantID(tenantID))
		return query.Only(claimCtx)
	})
}

// ClaimTaskByID 认领任务 (根据数据库自增ID)
func (s *bpmnTaskService) ClaimTaskByID(ctx context.Context, id int, userID int) error {
	claimCtx, tenantID, err := taskClaimContext(ctx, userID)
	if err != nil {
		return err
	}
	return s.claimTask(claimCtx, tenantID, userID, func(client *ent.Client) (*ent.ProcessTask, error) {
		query := client.ProcessTask.Query().Where(processtask.ID(id), processtask.TenantID(tenantID))
		return query.Only(claimCtx)
	})
}

func (s *bpmnTaskService) claimTask(ctx context.Context, tenantID, userID int, load func(*ent.Client) (*ent.ProcessTask, error)) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务认领事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	task, err := load(tx.Client())
	if err != nil {
		return fmt.Errorf("获取待认领任务失败: %w", err)
	}
	if handler := s.engine.findHandlerByTaskType(task.TaskType); handler != nil && isAsyncHandler(handler) {
		return common.NewForbiddenError("异步委派任务不能通过人工认领")
	}
	if err := s.engine.authorizeTaskActorWithClient(ctx, tx.Client(), task); err != nil {
		return err
	}
	if task.Assignee != "" && task.Assignee != "0" {
		return taskClaimConflict()
	}
	actor, err := tx.Client().User.Query().Where(
		user.ID(userID), user.TenantID(task.TenantID), user.Active(true),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("获取任务认领用户失败: %w", err)
	}
	affected, err := tx.Client().ProcessTask.Update().Where(
		processtask.ID(task.ID),
		processtask.TenantID(task.TenantID),
		processtask.Status(common.ProcessTaskStatusCreated),
		processtask.Or(processtask.AssigneeIsNil(), processtask.Assignee(""), processtask.Assignee("0")),
	).SetAssignee(strconv.Itoa(userID)).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("认领任务失败: %w", err)
	}
	if affected != 1 {
		return taskClaimConflict()
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskClaimed(ctx, task, actor.ID, actor.Name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务认领事务失败: %w", err)
	}
	return nil
}

func taskClaimConflict() error {
	return common.NewConflictError("process task claim", "task is no longer eligible or was already claimed")
}

func taskClaimContext(ctx context.Context, requestedUserID int) (context.Context, int, error) {
	if requestedUserID <= 0 {
		return nil, 0, fmt.Errorf("无效的用户ID")
	}
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	if requestedUserID != scope.UserID {
		return nil, 0, common.NewForbiddenError("只能以当前认证用户认领任务")
	}
	return ctx, scope.TenantID, nil
}

func (s *bpmnTaskService) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	return s.engine.CompleteTask(ctx, taskID, variables)
}

func (s *bpmnTaskService) CancelTask(ctx context.Context, taskID string, reason string) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	task, err := s.loadTaskByKey(ctx, taskID, scope.TenantID)
	if err != nil {
		return err
	}
	if _, err := s.authorizeTaskUpdate(ctx, task); err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务取消事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	_, err = tx.Client().ProcessTask.UpdateOne(task).
		SetStatus("cancelled").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("取消任务失败: %w", err)
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskCancelled(ctx, task, actor.ID, actor.Name, reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务取消事务失败: %w", err)
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
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	task, err := s.loadTaskByKey(ctx, taskID, scope.TenantID)
	if err != nil {
		return err
	}
	if _, err := s.authorizeTaskUpdate(ctx, task); err != nil {
		return err
	}
	participantVariables, err := validateAndCloneBPMNParticipantVariables(variables, true)
	if err != nil {
		return err
	}
	mergedVariables := mergeBPMNTaskVariables(task.TaskVariables, participantVariables)
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务变量事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	_, err = tx.Client().ProcessTask.UpdateOne(task).
		SetTaskVariables(mergedVariables).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("设置任务变量失败: %w", err)
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskVariablesChanged(ctx, task, actor.ID, actor.Name, task.TaskVariables, mergedVariables); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务变量事务失败: %w", err)
	}
	return nil
}

func (s *bpmnTaskService) DelegateTask(ctx context.Context, taskID string, newAssignee string) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启任务委派事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	task, err := tx.Client().ProcessTask.Query().Where(
		processtask.TaskID(taskID), processtask.TenantID(scope.TenantID),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("获取委派任务失败: %w", err)
	}
	txEngine := s.engine.forClient(tx.Client(), nil)
	if err := txEngine.authorizeTaskActorWithClient(ctx, tx.Client(), task); err != nil {
		return err
	}
	assignee, err := resolveTaskAssignee(ctx, tx.Client(), scope.TenantID, newAssignee)
	if err != nil {
		return err
	}
	actor, err := loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	affected, err := tx.Client().ProcessTask.Update().Where(
		processtask.ID(task.ID),
		processtask.TenantID(scope.TenantID),
		processtask.StatusNEQ(common.ProcessTaskStatusCompleted),
		processtask.StatusNEQ(common.ProcessTaskStatusCancelled),
	).
		SetAssignee(strconv.Itoa(assignee.ID)).
		SetStatus(common.ProcessTaskStatusDelegated).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("委派任务失败: %w", err)
	}
	if affected != 1 {
		return common.NewConflictError("process task delegation", "task is no longer active")
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskDelegated(ctx, task, actor.ID, actor.Name, assignee); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务委派事务失败: %w", err)
	}
	return nil
}

func (s *bpmnTaskService) GetTaskStatistics(ctx context.Context, req *TaskStatisticsRequest) (*TaskStatistics, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if !scope.CanReadAllTasks {
		return nil, common.NewForbiddenError("无权读取任务统计")
	}
	req.TenantID = scope.TenantID
	query := s.client.ProcessTask.Query().Where(processtask.TenantID(scope.TenantID))

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processtask.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.Assignee != "" {
		query = query.Where(processtask.Assignee(req.Assignee))
	}
	if req.Status != "" {
		query = query.Where(processtask.Status(req.Status))
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
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启会签任务事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txTaskService := s.engine.forClient(tx.Client(), nil).taskService
	parentTask, err := txTaskService.loadTaskByKey(ctx, parentTaskID, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if _, err := txTaskService.authorizeTaskUpdate(ctx, parentTask); err != nil {
		return nil, err
	}
	tasks, err := txTaskService.createCounterSignTasksWithClient(ctx, tx.Client(), parentTask, req, scope.UserID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交会签任务事务失败: %w", err)
	}
	for _, task := range tasks {
		task.Unwrap()
	}
	return tasks, nil
}

func (s *bpmnTaskService) createCounterSignTasksWithClient(ctx context.Context, client *ent.Client, parentTask *ent.ProcessTask, req *CounterSignRequest, actorID int) ([]*ent.ProcessTask, error) {
	if req == nil || len(req.Approvers) == 0 {
		return nil, fmt.Errorf("会签审批人不能为空")
	}

	actorName := ""
	if actorID > 0 {
		actor, actorErr := client.User.Query().
			Where(user.ID(actorID), user.TenantID(parentTask.TenantID), user.Active(true)).
			Only(ctx)
		if actorErr != nil {
			return nil, fmt.Errorf("获取会签操作用户失败: %w", actorErr)
		}
		actorName = actor.Name
	}
	for _, approver := range req.Approvers {
		if _, err := resolveTaskAssignee(ctx, client, parentTask.TenantID, approver); err != nil {
			return nil, err
		}
	}

	// 生成根任务ID（如果是第一个会签任务）
	rootTaskID := parentTask.TaskID
	if parentTask.RootTaskID != "" {
		rootTaskID = parentTask.RootTaskID
	}

	threshold := req.Threshold
	if threshold == 0 {
		threshold = len(req.Approvers)
	}

	var tasks []*ent.ProcessTask
	for i, approver := range req.Approvers {
		taskID := fmt.Sprintf("%s_countersign_%d", parentTask.TaskID, i)
		status := common.ProcessTaskStatusAssigned
		if req.ApprovalType == "serial" && i > 0 {
			status = "created"
		}
		task, err := client.ProcessTask.Create().
			SetTaskID(taskID).
			SetProcessInstanceID(parentTask.ProcessInstanceID).
			SetProcessDefinitionKey(parentTask.ProcessDefinitionKey).
			SetTaskDefinitionKey(parentTask.TaskDefinitionKey + "_counter").
			SetTaskName(parentTask.TaskName + "_会签").
			SetTaskType("user_task").
			SetAssignee(approver).
			SetStatus(status).
			SetPriority(parentTask.Priority).
			SetParentTaskID(parentTask.TaskID).
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
	_, err := client.ProcessTask.UpdateOneID(parentTask.ID).
		Where(processtask.TenantID(parentTask.TenantID)).
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
		return nil, fmt.Errorf("更新会签父任务失败: %w", err)
	}
	if err := s.engine.auditService.ForClient(client).RecordCounterSignCreated(ctx, parentTask, actorID, actorName, len(req.Approvers)); err != nil {
		return nil, err
	}

	return tasks, nil
}

// GetCounterSignStatus 获取会签状态
func (s *bpmnTaskService) GetCounterSignStatus(ctx context.Context, parentTaskID string) (*CounterSignStatus, error) {
	parent, err := s.GetTask(ctx, parentTaskID)
	if err != nil {
		return nil, err
	}
	return getCounterSignStatus(ctx, s.client, parent)
}

func getCounterSignStatus(ctx context.Context, client *ent.Client, parent *ent.ProcessTask) (*CounterSignStatus, error) {
	// 获取所有会签子任务
	subTasks, err := client.ProcessTask.Query().
		Where(processtask.ParentTaskID(parent.TaskID), processtask.TenantID(parent.TenantID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取会签子任务失败: %w", err)
	}

	status := &CounterSignStatus{
		ParentTaskID: parent.TaskID,
		Total:        len(subTasks),
		Completed:    0,
		Approved:     0,
		Rejected:     0,
		Pending:      0,
		Status:       "pending",
	}

	for _, task := range subTasks {
		switch task.Status {
		case "completed":
			status.Completed++
			// 检查审批结果
			if vars := task.TaskVariables; vars != nil {
				if approved, ok := vars["approved"].(bool); ok && approved {
					status.Approved++
				} else {
					status.Rejected++
				}
			}
		case "assigned", "created":
			status.Pending++
		}
	}

	threshold := status.Total
	if value, ok := numericInt(parent.TaskVariables["threshold"]); ok && value > 0 {
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
func (s *bpmnTaskService) Vote(ctx context.Context, taskID string, req *VoteRequest) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启会签投票事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	executionKeys := make([]string, 0)
	var parentEffect *completedTaskEffect
	task, err := tx.Client().ProcessTask.Query().
		Where(processtask.TaskID(taskID), processtask.TenantID(scope.TenantID)).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("获取会签任务失败: %w", err)
	}
	if err := s.engine.authorizeTaskActorWithClient(ctx, tx.Client(), task); err != nil {
		return err
	}
	if task.Status != common.ProcessTaskStatusAssigned {
		return taskVoteConflict()
	}
	actor, err := loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}
	var parentTask *ent.ProcessTask
	if task.ParentTaskID != "" {
		// Acquire the parent write lock before mutating this child. PostgreSQL
		// concurrent voters then observe prior sibling commits after waiting on
		// the same row, and finalization cannot deadlock while cancelling a child
		// whose transaction is waiting for the parent.
		affected, err := tx.Client().ProcessTask.Update().Where(
			processtask.TaskID(task.ParentTaskID),
			processtask.TenantID(scope.TenantID),
			processtask.StatusNEQ(common.ProcessTaskStatusCompleted),
		).AddAggregationVersion(1).Save(ctx)
		if err != nil {
			return fmt.Errorf("锁定会签父任务失败: %w", err)
		}
		if affected != 1 {
			return taskVoteConflict()
		}
		parentTask, err = tx.Client().ProcessTask.Query().Where(
			processtask.TaskID(task.ParentTaskID),
			processtask.TenantID(scope.TenantID),
		).Only(ctx)
		if err != nil {
			return fmt.Errorf("获取会签父任务失败: %w", err)
		}
	}
	voteVariables := map[string]interface{}{
		"approved": req.Approved,
		"comment":  req.Comment,
	}
	affected, err := tx.Client().ProcessTask.Update().
		Where(
			processtask.ID(task.ID),
			processtask.TenantID(scope.TenantID),
			processtask.Status(common.ProcessTaskStatusAssigned),
		).
		SetStatus("completed").
		SetCompletedTime(time.Now()).
		SetTaskVariables(voteVariables).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("完成任务失败: %w", err)
	}
	if affected != 1 {
		return taskVoteConflict()
	}
	instance, err := tx.Client().ProcessInstance.Query().
		Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(scope.TenantID)).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("获取会签流程实例失败: %w", err)
	}
	action, decision := "reject", "rejected"
	if req.Approved {
		action, decision = "approve", "approved"
	}
	decisionVariables := map[string]interface{}{"approvalAction": action, "approvalResult": decision, "approvalComment": req.Comment}
	if err := s.engine.recordApprovalDecisionWithClient(ctx, tx.Client(), instance, task, decisionVariables); err != nil {
		return err
	}
	if err := s.engine.auditService.ForClient(tx.Client()).RecordTaskCompleted(ctx, task, actor.ID, actor.Name, task.TaskVariables, voteVariables); err != nil {
		return err
	}

	parentTaskID := task.ParentTaskID
	if parentTask == nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交会签投票事务失败: %w", err)
		}
		return nil
	}
	status, err := getCounterSignStatus(ctx, tx.Client(), parentTask)
	if err != nil {
		return fmt.Errorf("获取会签状态失败: %w", err)
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
		next, err := tx.Client().ProcessTask.Query().
			Where(processtask.ParentTaskID(parentTaskID), processtask.TenantID(scope.TenantID), processtask.Status("created")).
			Order(ent.Asc(processtask.FieldID)).
			First(ctx)
		if err == nil {
			if err := tx.Client().ProcessTask.UpdateOneID(next.ID).Where(processtask.TenantID(scope.TenantID)).SetStatus(common.ProcessTaskStatusAssigned).Exec(ctx); err != nil {
				return fmt.Errorf("激活下一会签任务失败: %w", err)
			}
		} else if !ent.IsNotFound(err) {
			return fmt.Errorf("获取下一会签任务失败: %w", err)
		}
	}

	final := status.Status == "approved" || status.Status == "rejected"
	if final {
		if _, err := tx.Client().ProcessTask.Update().
			Where(processtask.ParentTaskID(parentTaskID), processtask.TenantID(scope.TenantID), processtask.StatusNEQ("completed"), processtask.StatusNEQ("cancelled")).
			SetStatus("cancelled").SetCompletedTime(time.Now()).Save(ctx); err != nil {
			return fmt.Errorf("取消剩余会签任务失败: %w", err)
		}
		summaryVariables := mergeBPMNTaskVariables(parentTask.TaskVariables, map[string]interface{}{
			"approval_type": approvalType,
			"threshold":     threshold,
			"total":         status.Total,
			"completed":     status.Completed,
			"approved":      status.Approved,
			"rejected":      status.Rejected,
			"final_status":  status.Status,
		})
		if err := tx.Client().ProcessTask.UpdateOneID(parentTask.ID).
			Where(processtask.TenantID(scope.TenantID)).
			SetTaskVariables(summaryVariables).
			Exec(ctx); err != nil {
			return fmt.Errorf("更新会签父任务失败: %w", err)
		}
		parentTask.TaskVariables = summaryVariables
		parentEffect, err = s.engine.completeAuthorizedTaskWithClient(
			ctx, tx.Client(), parentTask,
			map[string]interface{}{"approvalResult": status.Status, "approved": status.Status == "approved"},
			&executionKeys,
		)
		if err != nil {
			return fmt.Errorf("推进会签父任务失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交会签投票事务失败: %w", err)
	}
	if parentEffect != nil {
		parentEffect.task.Unwrap()
		if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID <= 0 {
			ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, parentEffect.task.TenantID)
		}
		s.engine.processCommittedCallbackKeys(ctx, parentEffect.task.TenantID, executionKeys)
		s.engine.executeAsyncUserTaskCompletion(ctx, parentEffect)
	}
	return nil
}

func taskVoteConflict() error {
	return common.NewConflictError("process task vote", "task is no longer assigned or was already voted")
}
