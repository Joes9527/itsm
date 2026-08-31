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
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
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
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/user"
	"itsm-backend/ent/workflowtask"
	"itsm-backend/service/approver"
	"itsm-backend/service/bpmn"

	entsql "entgo.io/ent/dialect/sql"
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
	// KAF 委派服务负责委派任务、审计和 Outbox 的原子写入。
	kafDelegationService *KafDelegationService
}

type kafCompletionFenceContextKey struct{}

type kafCompletionFence struct {
	ledgerID   int
	leaseOwner string
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
	// taskService 持有 engine 自身的引用（而不是每次调用再 NewCustomProcessEngine 造一个新的）：
	// callbackRegistry 是 engine 级别的状态，bootstrap 在各领域 service 构造完成后往
	// 这一个 engine 的 registry 里注入 TicketService/IncidentService。任务完成路径若临时
	// 新建 engine，拿到的就是一个从未被注入过的空 registry，UserTask 回调只会 Warn 一句
	// 静默失败（见 dispatchUserTaskCallback 的"失败只告警不阻断"注释）。
	engine.taskService = &bpmnTaskService{client: client, logger: logger, groupResolver: engine.groupResolver, engine: engine}
	engine.auditService = NewBPMNAuditService(client, logger)
	engine.kafDelegationService = NewKafDelegationService(client)

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

// StartProcess 启动流程实例
func (e *CustomProcessEngine) StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error) {
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
	createInstance := e.client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("PI-%s-%d", processDefinitionKey, time.Now().UnixNano())).
		SetBusinessKey(businessKey).
		SetProcessDefinitionKey(processDefinitionKey).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
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
	return e.completeTask(ctx, taskID, variables, func(callbackCtx context.Context, task *ent.ProcessTask, callbackVariables map[string]interface{}) error {
		if err := e.dispatchUserTaskCallback(callbackCtx, task, callbackVariables); err != nil {
			e.logger.Warnw("UserTask completion callback failed", "taskID", task.TaskID, "error", err)
		}
		return nil
	})
}

// CompleteKafDelegatedTask completes one KAF-ledger-scoped task and durably
// records the callback outcome. It intentionally does not widen ProcessEngine:
// generic callers retain CompleteTask's established best-effort callback behavior.
func (e *CustomProcessEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, leaseOwner, taskID string, variables map[string]interface{}) error {
	ledger, err := e.loadExecutingKafLedger(ctx, ledgerID, leaseOwner)
	if err != nil {
		return fmt.Errorf("load KAF completion ledger: %w", err)
	}
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 && ledger.TenantID != tenantID {
		return fmt.Errorf("KAF completion ledger does not belong to tenant")
	}
	if ledger.TaskID != taskID {
		return fmt.Errorf("KAF completion ledger does not match task")
	}
	ctx = bpmn.WithKafActionScope(ctx, bpmn.NewKafActionScope(
		ledger.ID, ledger.TenantID, ledger.TaskID, ledger.RunID, ledger.StepID,
		ledger.Action, ledger.IdempotencyKey, ledger.CorrelationID,
		ledger.ProcedureRef, ledger.ProcedureVersion,
	))
	ctx = context.WithValue(ctx, kafCompletionFenceContextKey{}, kafCompletionFence{
		ledgerID: ledger.ID, leaseOwner: leaseOwner,
	})
	receipt, err := e.ensureKafCompletionReceipt(ctx, ledgerID, ledger.TenantID, taskID)
	if err != nil {
		return err
	}
	task, err := e.client.ProcessTask.Query().Where(
		processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(ledger.TenantID),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("load KAF delegated task: %w", err)
	}
	if task.Status == common.ProcessTaskStatusCompleted {
		return e.recoverKafCompletionCallback(ctx, ledgerID, leaseOwner, receipt, task)
	}

	completionVariables := cloneKafVariables(task.TaskVariables)
	for key, value := range variables {
		completionVariables[key] = value
	}
	return e.completeTask(ctx, taskID, completionVariables, func(callbackCtx context.Context, callbackTask *ent.ProcessTask, callbackVariables map[string]interface{}) error {
		if _, err := e.loadExecutingKafLedger(callbackCtx, ledgerID, leaseOwner); err != nil {
			return err
		}
		if err := e.dispatchUserTaskCallback(callbackCtx, callbackTask, callbackVariables); err != nil {
			return e.updateKafCompletionReceipt(callbackCtx, ledgerID, leaseOwner, receipt.ID, "callback_failed", "callback_failed", err)
		}
		return e.updateKafCompletionReceipt(callbackCtx, ledgerID, leaseOwner, receipt.ID, "callback_succeeded", "", nil)
	})
}

func (e *CustomProcessEngine) loadExecutingKafLedger(ctx context.Context, ledgerID int, leaseOwner string) (*ent.KafTaskActionLedger, error) {
	if strings.TrimSpace(leaseOwner) == "" {
		return nil, fmt.Errorf("KAF completion lease owner is required")
	}
	ledger, err := e.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.IDEQ(ledgerID),
		kaftaskactionledger.ResultStatusEQ("executing"),
		kaftaskactionledger.LeaseOwnerEQ(leaseOwner),
		kaftaskactionledger.LeaseExpiresAtGT(time.Now()),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("KAF completion lease owner is stale or expired")
	}
	if err != nil {
		return nil, fmt.Errorf("load executing KAF completion ledger: %w", err)
	}
	return ledger, nil
}

func (e *CustomProcessEngine) completeTask(ctx context.Context, taskID string, variables map[string]interface{}, callback func(context.Context, *ent.ProcessTask, map[string]interface{}) error) error {
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
	if err := e.validateTicketRecordClassInput(ctx, instance, variables); err != nil {
		return err
	}
	mergedVariables := mergeProcessVariables(instance.Variables, variables)
	if targetRef, resolveErr := e.resolveNextElement(instance, process, task.TaskDefinitionKey, mergedVariables); resolveErr == nil {
		if serviceTask := e.findServiceTask(process, targetRef); serviceTask != nil && e.isKafDelegationServiceTask(serviceTask) {
			instance, err = e.completeTaskIntoKafWaitState(ctx, task, instance, serviceTask, variables)
			if err != nil {
				return err
			}
			return callback(ctx, task, instance.Variables)
		}
	}

	updated := 0
	err = e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		var updateErr error
		updated, updateErr = client.ProcessTask.Update().
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
		return updateErr
	})
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
	if err := callback(ctx, task, instance.Variables); err != nil {
		return err
	}

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

func mergeProcessVariables(current, incoming map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(current)+len(incoming))
	for key, value := range current {
		merged[key] = value
	}
	for key, value := range incoming {
		merged[key] = value
	}
	return merged
}

// completeTaskIntoKafWaitState owns the single persistent transaction for a
// user-task transition into the registered KAF asynchronous wait state.
func (e *CustomProcessEngine) completeTaskIntoKafWaitState(ctx context.Context, task *ent.ProcessTask, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask, variables map[string]interface{}) (*ent.ProcessInstance, error) {
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		updated, err := e.completeTaskIntoKafWaitStateOnce(ctx, task, instance, serviceTask, variables)
		if err == nil || !isRetryableBPMNHandoffConflict(err) {
			return updated, err
		}
		lastErr = err
		if err := waitForBPMNHandoffRetry(ctx, time.Duration(attempt+1)*5*time.Millisecond); err != nil {
			return nil, err
		}
	}
	completed, err := e.client.ProcessTask.Query().Where(
		processtask.IDEQ(task.ID),
		processtask.TenantIDEQ(instance.TenantID),
		processtask.StatusEQ(common.ProcessTaskStatusCompleted),
	).Exist(ctx)
	if err == nil && completed {
		return nil, fmt.Errorf("任务已被处理，请刷新后重试")
	}
	return nil, lastErr
}

func isRetryableBPMNHandoffConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "could not serialize access") ||
		strings.Contains(message, "deadlock detected")
}

func waitForBPMNHandoffRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *CustomProcessEngine) completeTaskIntoKafWaitStateOnce(ctx context.Context, task *ent.ProcessTask, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask, variables map[string]interface{}) (*ent.ProcessInstance, error) {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启 KAF 交接事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	completedAt := time.Now()
	updated, err := tx.ProcessTask.Update().Where(
		processtask.IDEQ(task.ID),
		processtask.TenantIDEQ(instance.TenantID),
		processtask.StatusNEQ(common.ProcessTaskStatusCompleted),
		processtask.StatusNEQ(common.ProcessTaskStatusCancelled),
	).
		SetStatus(common.ProcessTaskStatusCompleted).
		SetCompletedTime(completedAt).
		SetTaskVariables(variables).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新 KAF 交接源任务失败: %w", err)
	}
	if updated != 1 {
		return nil, fmt.Errorf("任务已被处理，请刷新后重试")
	}

	current, err := tx.ProcessInstance.Query().Where(
		processinstance.IDEQ(instance.ID),
		processinstance.TenantIDEQ(instance.TenantID),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询 KAF 交接流程实例失败: %w", err)
	}
	merged := mergeProcessVariables(current.Variables, variables)
	updated, err = tx.ProcessInstance.Update().Where(
		processinstance.IDEQ(current.ID),
		processinstance.TenantIDEQ(current.TenantID),
		processinstance.VersionEQ(current.Version),
	).
		SetVariables(merged).
		SetVersion(current.Version + 1).
		SetCurrentActivityID(serviceTask.ID).
		SetCurrentActivityName(serviceTask.ID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新 KAF 交接流程实例失败: %w", err)
	}
	if updated != 1 {
		return nil, fmt.Errorf("KAF 交接流程实例已被并发更新，请刷新后重试")
	}
	current.Variables = merged
	current.Version++
	current.CurrentActivityID = serviceTask.ID
	current.CurrentActivityName = serviceTask.ID

	if err := e.recordApprovalDecisionWithClient(ctx, tx.Client(), current, task, variables); err != nil {
		return nil, err
	}
	if _, err := e.kafDelegationService.createDelegatedTaskInTx(ctx, tx, current, serviceTask); err != nil {
		return nil, fmt.Errorf("创建 KAF 委派任务失败: %w", err)
	}

	userID := 0
	userName := ""
	if actor, ok := ctx.Value("user").(*ent.User); ok {
		userID = actor.ID
		userName = actor.Name
	}
	transactionalAudit := NewBPMNAuditService(tx.Client(), e.logger)
	if err := transactionalAudit.RecordTaskCompleted(ctx, task, userID, userName, task.TaskVariables, variables); err != nil {
		return nil, fmt.Errorf("记录 KAF 交接源任务审计失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交 KAF 交接事务失败: %w", err)
	}
	return current, nil
}

func (e *CustomProcessEngine) ensureKafCompletionReceipt(ctx context.Context, ledgerID, tenantID int, taskID string) (*ent.KafTaskCompletionReceipt, error) {
	receipt, err := e.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledgerID),
	).Only(ctx)
	if err == nil {
		if receipt.TenantID != tenantID || receipt.TaskID != taskID {
			return nil, fmt.Errorf("KAF completion receipt scope mismatch")
		}
		return receipt, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("load KAF completion receipt: %w", err)
	}
	err = e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		var createErr error
		receipt, createErr = client.KafTaskCompletionReceipt.Create().
			SetLedgerID(ledgerID).SetTenantID(tenantID).SetTaskID(taskID).
			SetStatus("callback_pending").Save(ctx)
		return createErr
	})
	if err == nil {
		return receipt, nil
	}
	if !ent.IsConstraintError(err) {
		return nil, fmt.Errorf("create KAF completion receipt: %w", err)
	}
	return e.ensureKafCompletionReceipt(ctx, ledgerID, tenantID, taskID)
}

func (e *CustomProcessEngine) recoverKafCompletionCallback(ctx context.Context, ledgerID int, leaseOwner string, receipt *ent.KafTaskCompletionReceipt, task *ent.ProcessTask) error {
	if receipt.Status == "callback_succeeded" {
		return nil
	}
	if _, err := e.loadExecutingKafLedger(ctx, ledgerID, leaseOwner); err != nil {
		return err
	}
	if err := e.dispatchUserTaskCallback(ctx, task, task.TaskVariables); err != nil {
		return e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_failed", "callback_failed", err)
	}
	return e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_succeeded", "", nil)
}

func (e *CustomProcessEngine) updateKafCompletionReceipt(ctx context.Context, ledgerID int, leaseOwner string, receiptID int, status, errorCode string, callbackErr error) error {
	allowedFrom := []string{"callback_pending", "callback_failed"}
	update := e.client.KafTaskCompletionReceipt.Update().Where(
		kaftaskcompletionreceipt.IDEQ(receiptID),
		kaftaskcompletionreceipt.LedgerIDEQ(ledgerID),
		kaftaskcompletionreceipt.StatusIn(allowedFrom...),
		kafReceiptOwnedByExecutingLease(ledgerID, leaseOwner, time.Now()),
	).SetStatus(status)
	if errorCode == "" {
		update.ClearErrorCode()
	} else {
		// Do not persist callback text: it can include credentials or external payloads.
		update.SetErrorCode(errorCode)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("update KAF completion receipt: %w", err)
	}
	if updated != 1 {
		receipt, loadErr := e.client.KafTaskCompletionReceipt.Get(ctx, receiptID)
		if loadErr == nil && receipt.Status == "callback_succeeded" && status == "callback_succeeded" {
			return nil
		}
		return fmt.Errorf("KAF completion receipt transition is stale or non-monotonic")
	}
	return callbackErr
}

func kafReceiptOwnedByExecutingLease(ledgerID int, leaseOwner string, now time.Time) predicate.KafTaskCompletionReceipt {
	return func(selector *entsql.Selector) {
		ledger := entsql.Table(kaftaskactionledger.Table)
		ownedLedger := entsql.Select(ledger.C(kaftaskactionledger.FieldID)).From(ledger).Where(entsql.And(
			entsql.EQ(ledger.C(kaftaskactionledger.FieldID), ledgerID),
			entsql.EQ(ledger.C(kaftaskactionledger.FieldResultStatus), "executing"),
			entsql.EQ(ledger.C(kaftaskactionledger.FieldLeaseOwner), leaseOwner),
			entsql.GT(ledger.C(kaftaskactionledger.FieldLeaseExpiresAt), now),
		))
		selector.Where(entsql.In(selector.C(kaftaskcompletionreceipt.FieldLedgerID), ownedLedger))
	}
}

func (e *CustomProcessEngine) runKafFencedWrite(ctx context.Context, write func(*ent.Client) error) error {
	fence, fenced := ctx.Value(kafCompletionFenceContextKey{}).(kafCompletionFence)
	if !fenced {
		return write(e.client)
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF owner-fenced write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := write(tx.Client()); err != nil {
		return err
	}
	if err := assertKafCompletionFence(ctx, tx.Client(), fence); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF owner-fenced write: %w", err)
	}
	return nil
}

func assertKafCompletionFence(ctx context.Context, client *ent.Client, fence kafCompletionFence) error {
	now := time.Now()
	updated, err := client.KafTaskActionLedger.Update().Where(
		kaftaskactionledger.IDEQ(fence.ledgerID),
		kaftaskactionledger.ResultStatusEQ("executing"),
		kaftaskactionledger.LeaseOwnerEQ(fence.leaseOwner),
		kaftaskactionledger.LeaseExpiresAtGT(now),
	).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return fmt.Errorf("fence KAF completion write: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("KAF completion lease owner is stale or expired")
	}
	return nil
}

// validateTicketRecordClassInput rejects caller-controlled class changes before
// completion writes any task or process state. Non-ticket workflows retain
// their established variable behavior.
func (e *CustomProcessEngine) validateTicketRecordClassInput(ctx context.Context, instance *ent.ProcessInstance, variables map[string]interface{}) error {
	if instance.BusinessType != "ticket" {
		return nil
	}
	provided, present := variables["record_class"]
	if !present {
		return nil
	}
	if instance.BusinessID <= 0 {
		return fmt.Errorf("ticket process instance %d has no work item ID", instance.ID)
	}
	workItem, err := e.client.Ticket.Query().
		Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(instance.TenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("load ticket-backed work item record class: %w", err)
	}
	recordClass, ok := provided.(string)
	if !ok || recordClass != workItem.RecordClass {
		return fmt.Errorf("record class variable conflicts with persisted work item record class")
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
func (e *CustomProcessEngine) dispatchUserTaskCallback(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) error {
	if e.callbackRegistry == nil || task == nil {
		return nil
	}
	serviceTaskType, _ := task.TaskVariables[bpmnMetaDataServiceTaskType].(string)
	if serviceTaskType == "" {
		return nil
	}

	handler := e.findHandlerByTaskType(serviceTaskType)
	if handler == nil {
		// 模板声明了类型但没有注册对应 handler（例如 release_task），按 ServiceTask
		// 分支的既有约定视为 NoOp，仅告警不阻断流程。
		e.logger.Warnw("UserTask 声明的 service_task_type 没有注册处理器，跳过回调",
			"taskID", task.TaskID, "serviceTaskType", serviceTaskType)
		return nil
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
			"serviceTaskType", serviceTaskType, "errorCode", "user_task_callback_failed")
		return errors.New("user task callback failed")
	}
	e.logger.Infow("UserTask 完成后回调执行成功",
		"taskID", task.TaskID, "taskDefinitionKey", task.TaskDefinitionKey,
		"serviceTaskType", serviceTaskType)
	return nil
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
	if actorID <= 0 {
		return fmt.Errorf("审批决策缺少认证操作人")
	}
	actorName := ""
	if actor, err := client.User.Get(ctx, actorID); err == nil {
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

// authorizeTaskActor ensures that task actions are performed by the assigned
// user or an explicitly resolved candidate. System/internal calls without an
// authenticated actor keep their existing behavior — except tasks whose
// TaskType resolves, via findHandlerByTaskType, to a handler implementing
// AsyncServiceTaskHandler with IsAsync()==true (e.g. kaf_delegate): those are
// never authorized through this permissive no-context path and always go
// through authorizeKafAutomationActor instead. Using the same
// findHandlerByTaskType+isAsyncHandler lookup that handleElement uses to
// decide whether to pause in the first place guarantees the pause decision
// and the authorization decision can never diverge.
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	if handler := e.findHandlerByTaskType(task.TaskType); handler != nil && isAsyncHandler(handler) {
		return e.kafDelegationService.AuthorizeTask(ctx, task)
	}

	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return nil
	}
	actor, err := e.client.User.Query().Where(user.ID(userID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("审批用户不存在: %w", err)
	}
	allowed := func(csv string) bool {
		for _, candidate := range strings.Split(csv, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == strconv.Itoa(userID) || candidate == actor.Username {
				return true
			}
		}
		return false
	}
	if allowed(task.Assignee) || allowed(task.CandidateUsers) {
		return nil
	}
	return fmt.Errorf("当前用户不是该任务的审批人或候选人")
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
			if fence, ok := ctx.Value(kafCompletionFenceContextKey{}).(kafCompletionFence); ok {
				if err := assertKafCompletionFence(ctx, tx.Client(), fence); err != nil {
					_ = tx.Rollback()
					return nil, err
				}
			}
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
	targetRef, err := e.resolveNextElement(instance, process, currentElementID, variables)
	if err != nil {
		return err
	}
	if targetRef == "" {
		if e.isEndEvent(process, currentElementID) {
			return e.completeProcess(ctx, instance)
		}
		return nil
	}
	return e.handleElement(ctx, instance, process, targetRef)
}

func (e *CustomProcessEngine) resolveNextElement(instance *ent.ProcessInstance, process *BPMNProcess, currentElementID string, variables map[string]interface{}) (string, error) {
	outgoingFlows := e.findOutgoingFlows(process, currentElementID)

	if len(outgoingFlows) == 0 {
		return "", nil
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
		return "", fmt.Errorf("没有符合条件的路径")
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

	return targetRef, nil
}

func (e *CustomProcessEngine) handleElement(ctx context.Context, instance *ent.ProcessInstance, process *BPMNProcess, elementID string) error {
	if serviceTask := e.findServiceTask(process, elementID); serviceTask != nil && e.isKafDelegationServiceTask(serviceTask) {
		return e.createDelegatedTask(ctx, instance, serviceTask, bpmn.KafDelegateTaskType)
	}

	// Find the element name for logging
	elementName := elementID
	if task := e.findUserTask(process, elementID); task != nil {
		elementName = task.Name
	} else if endEvent := e.findEndEvent(process, elementID); endEvent != nil {
		elementName = endEvent.Name
	}

	err := e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		_, updateErr := client.ProcessInstance.UpdateOneID(instance.ID).
			SetCurrentActivityID(elementID).
			SetCurrentActivityName(elementName).
			Save(ctx)
		return updateErr
	})
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
				if isAsyncHandler(handler) {
					return e.createDelegatedTask(ctx, instance, serviceTask, serviceTaskType)
				}
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
			// resolvedTaskType 记录到底是 serviceRef 还是 GetType() 兜底值命中的 handler——
			// 如果这个节点解析出的 handler 是异步的，createDelegatedTask 需要把这个值原样
			// 写进 ProcessTask.TaskType/TaskVariables["service_task_type"]，这样后续
			// authorizeTaskActor（完成鉴权）和 dispatchUserTaskCallback（完成回调）用
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

func (e *CustomProcessEngine) isKafDelegationServiceTask(serviceTask *BPMNServiceTask) bool {
	if serviceTask == nil || serviceTask.ServiceTaskType() != bpmn.KafDelegateTaskType {
		return false
	}
	handler := e.findHandlerByTaskType(bpmn.KafDelegateTaskType)
	return handler != nil && isAsyncHandler(handler)
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
	var createdTask *ent.ProcessTask
	err := e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		var createErr error
		createdTask, createErr = client.ProcessTask.Create().
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
		return createErr
	})
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

// createDelegatedTask 为声明了异步 handler 的 serviceTask 节点创建 ProcessTask 并暂停流程：
// 不调用 handler.Execute、不推进 executeStep，流程实例的 CurrentActivityID（handleElement
// 顶部已设置）停在这个节点，直到外部通过 CompleteTask 显式完成该任务才会继续。
//
// taskType 是调用方（handleElement 的两条 serviceTask 解析路径）实际用来
// findHandlerByTaskType 命中这个异步 handler 的那个字符串——metaData 路径下是
// serviceTask.ServiceTaskType()，legacy 属性猜测路径下是命中的 serviceRef/GetType()
// 兜底值。必须原样传入（而不是在这里重新调用 ServiceTaskType()，legacy 路径下那会是
// 空串），因为 ProcessTask.TaskType/TaskVariables["service_task_type"] 之后要分别喂给
// authorizeTaskActor 和 dispatchUserTaskCallback 重新做 findHandlerByTaskType 查找——
// 三处用的必须是同一个能查到同一个 handler 的字符串。
func (e *CustomProcessEngine) createDelegatedTask(ctx context.Context, instance *ent.ProcessInstance, serviceTask *BPMNServiceTask, taskType string) error {
	if taskType == bpmn.KafDelegateTaskType {
		if e.kafDelegationService == nil {
			return fmt.Errorf("KAF delegation service is not configured")
		}
		if _, err := e.kafDelegationService.CreateDelegatedTask(ctx, instance.ID, serviceTask); err != nil {
			return fmt.Errorf("创建 KAF 委派任务失败: %w", err)
		}
		e.logger.Infow("KAF ServiceTask 已暂停，等待外部完成", "elementID", serviceTask.ID, "instanceID", instance.ProcessInstanceID)
		return nil
	}

	taskVariables := map[string]interface{}{
		bpmnMetaDataServiceTaskType: taskType,
	}
	if action := serviceTask.ServiceTaskAction(); action != "" {
		taskVariables[bpmnMetaDataAction] = action
	}
	if allowedActions := serviceTask.AllowedActions(); allowedActions != "" {
		taskVariables[bpmnMetaDataAllowedActions] = allowedActions
	}

	err := e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		_, createErr := client.ProcessTask.Create().
			SetTaskID(fmt.Sprintf("TASK-%s-%d", serviceTask.ID, time.Now().UnixNano())).
			SetProcessInstanceID(instance.ID).
			SetProcessDefinitionKey(instance.ProcessDefinitionKey).
			SetTaskDefinitionKey(serviceTask.ID).
			SetTaskName(serviceTask.Name).
			SetTaskType(taskType).
			SetStatus(common.ProcessTaskStatusDelegated).
			SetTaskVariables(taskVariables).
			SetTenantID(instance.TenantID).
			SetCreatedTime(time.Now()).
			Save(ctx)
		return createErr
	})
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
	return e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		_, err := client.ProcessInstance.UpdateOneID(instance.ID).
			SetStatus("completed").
			SetEndTime(time.Now()).
			Save(ctx)
		return err
	})
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
	if req.TenantID > 0 {
		query = query.Where(processinstance.TenantID(req.TenantID))
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
	// engine 是创建本任务服务的那个引擎实例。任何需要推进流程（CompleteTask）或复用引擎
	// 内部鉴权/审批记录逻辑（authorizeTaskActor / recordApprovalDecision）的方法都必须用它，
	// 不能 NewCustomProcessEngine 现造——见 NewCustomProcessEngine 里的说明。
	engine *CustomProcessEngine
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

	return task, nil
}

// CompleteTaskByID 根据数据库自增ID完成任务
func (s *bpmnTaskService) CompleteTaskByID(ctx context.Context, id int, variables map[string]interface{}) error {
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
	return s.engine.CompleteTask(ctx, task.TaskID, variables)
}

func (s *bpmnTaskService) ListUserTasks(ctx context.Context, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error) {
	s.logger.Debugw("ListUserTasks called", "assignee", req.Assignee, "userID", req.UserID, "tenantID", req.TenantID)
	query := s.client.ProcessTask.Query()

	// 「我的待办」语义：UserID 透传时，查出“分配给我 OR 我是候选人 OR 我所在组是候选组”的任务。
	// 这样能同时覆盖 assignee / candidate_users / candidate_groups 三种途径。
	if req.UserID > 0 {
		tenantID := req.TenantID
		if tenantID == 0 {
			if v, ok := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); ok {
				tenantID = v
			}
		}
		userIDStr := strconv.Itoa(req.UserID)

		// 1. 取得该用户所在的组名（逗号分隔）
		userGroupsCSV := ""
		if s.groupResolver != nil {
			groups, gErr := s.groupResolver.GetUserGroupNames(ctx, tenantID, req.UserID)
			if gErr != nil {
				s.logger.Warnw("查询用户所属组失败", "error", gErr, "userID", req.UserID)
			} else {
				userGroupsCSV = groups
			}
		}

		// 2. OR 复合查询：assignee == me OR candidate_users 包含我 OR candidate_groups 包含我所在组
		orPreds := []predicate.ProcessTask{
			processtask.Assignee(userIDStr),
			processtask.CandidateUsersContains(userIDStr),
		}
		// 同时以 username/email 形式匹配 assignee 和 candidate_users——两个字段都可能存的是
		// username/email/ID 混合（例如流程设计器"受理人"选择器写入的是 username，而
		// resolveApprovalAssignee 等自动解析路径写入的是数字 ID），只匹配 ID 会导致通过
		// 设计器指定受理人的任务在"我的待办"里对该用户不可见（2026-08-18 实测复现）。
		if u, err := s.client.User.Get(ctx, req.UserID); err == nil && u != nil {
			username := strings.TrimSpace(u.Username)
			if username != "" && username != userIDStr {
				orPreds = append(orPreds, processtask.Assignee(username))
				orPreds = append(orPreds, processtask.CandidateUsersContains(username))
			}
			email := strings.TrimSpace(u.Email)
			if email != "" && email != userIDStr && email != username {
				orPreds = append(orPreds, processtask.Assignee(email))
				orPreds = append(orPreds, processtask.CandidateUsersContains(email))
			}
		}
		if userGroupsCSV != "" {
			orPreds = append(orPreds, processtask.CandidateGroupsContains(userGroupsCSV))
		}
		query = query.Where(processtask.Or(orPreds...))
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

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(assignee).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)

	return err
}

// isTaskCandidate 复用 authorizeTaskActor 的候选人匹配语义（用户 ID 十进制字符串或用户名），
// 用于 ClaimTask/ClaimTaskByID 校验：只有任务的 assignee 或 candidate_users 里的人才能认领
// 未分配的任务——否则任何登录用户都能抢先认领任何审批任务（包括自己提交的工单）。
func isTaskCandidate(ctx context.Context, client *ent.Client, userID int, task *ent.ProcessTask) (bool, error) {
	actor, err := client.User.Query().Where(user.ID(userID)).Only(ctx)
	if err != nil {
		return false, fmt.Errorf("用户不存在: %w", err)
	}
	allowed := func(csv string) bool {
		for _, candidate := range strings.Split(csv, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == strconv.Itoa(userID) || candidate == actor.Username {
				return true
			}
		}
		return false
	}
	return allowed(task.Assignee) || allowed(task.CandidateUsers), nil
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
	return s.engine.CompleteTask(ctx, taskID, variables)
}

func (s *bpmnTaskService) CancelTask(ctx context.Context, taskID string, reason string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetStatus("cancelled").
		Save(ctx)

	return err
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

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetTaskVariables(variables).
		Save(ctx)

	return err
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
	// 获取父任务
	parentTask, err := s.client.ProcessTask.Query().
		Where(processtask.TaskID(parentTaskID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取父任务失败: %w", err)
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

	return tasks, nil
}

// GetCounterSignStatus 获取会签状态
func (s *bpmnTaskService) GetCounterSignStatus(ctx context.Context, parentTaskID string) (*CounterSignStatus, error) {
	// 获取所有会签子任务
	subTasks, err := s.client.ProcessTask.Query().
		Where(processtask.ParentTaskID(parentTaskID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取会签子任务失败: %w", err)
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
	if parent, err := s.client.ProcessTask.Query().Where(processtask.TaskID(parentTaskID)).Only(ctx); err == nil {
		if value, ok := numericInt(parent.TaskVariables["threshold"]); ok && value > 0 {
			threshold = value
		}
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
	task, err := s.client.ProcessTask.Query().
		Where(processtask.TaskID(taskID)).
		First(ctx)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	if err := s.engine.authorizeTaskActor(ctx, task); err != nil {
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
		if err := s.engine.recordApprovalDecision(ctx, instance, task, map[string]interface{}{"approvalAction": action, "approvalResult": decision, "approvalComment": req.Comment}); err != nil {
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
	parentTask, err := s.client.ProcessTask.Query().
		Where(processtask.TaskID(parentTaskID)).
		First(ctx)
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
		if err := s.engine.CompleteTask(workflowCtx, parentTask.TaskID, map[string]interface{}{"approvalResult": status.Status, "approved": status.Status == "approved"}); err != nil {
			return fmt.Errorf("推进会签父任务失败: %w", err)
		}
	}

	return nil
}
