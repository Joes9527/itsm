package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

// BPMNAuditService BPMN审计服务
type BPMNAuditService struct {
	client               *ent.Client
	logger               *zap.SugaredLogger
	instanceAccessPolicy *bpmnInstanceAccessPolicy
}

// NewBPMNAuditService 创建BPMN审计服务
func NewBPMNAuditService(client *ent.Client, logger *zap.SugaredLogger) *BPMNAuditService {
	groupResolver := bpmn.NewGroupResolver(client)
	participationResolver := newBPMNParticipationResolver(client, groupResolver)
	return &BPMNAuditService{
		client:               client,
		logger:               logger,
		instanceAccessPolicy: newBPMNInstanceAccessPolicy(client, participationResolver),
	}
}

// ForClient binds audit writes to the caller's Ent client, including transaction clients.
func (s *BPMNAuditService) ForClient(client *ent.Client) *BPMNAuditService {
	policy := s.instanceAccessPolicy
	if policy == nil {
		groupResolver := bpmn.NewGroupResolver(s.client)
		policy = newBPMNInstanceAccessPolicy(s.client, newBPMNParticipationResolver(s.client, groupResolver))
	}
	return &BPMNAuditService{
		client:               client,
		logger:               s.logger,
		instanceAccessPolicy: policy.forClient(client),
	}
}

// AuditAction 审计操作类型
const (
	AuditActionProcessStarted       = "started"
	AuditActionProcessCompleted     = "completed"
	AuditActionProcessSuspended     = "suspended"
	AuditActionProcessResumed       = "resumed"
	AuditActionProcessTerminated    = "terminated"
	AuditActionTaskAssigned         = "assigned"
	AuditActionTaskUnassigned       = "unassigned"
	AuditActionTaskClaimed          = "claimed"
	AuditActionTaskCompleted        = "completed"
	AuditActionTaskCancelled        = "task_cancelled"
	AuditActionTaskVariablesChanged = "task_variables_changed"
	AuditActionCounterSignCreated   = "counter_sign_created"
	AuditActionTaskEscalated        = "escalated"
	AuditActionTaskReassigned       = "reassigned"
	AuditActionVariableChanged      = "variable_changed"
	AuditActionActivityStarted      = "activity_started"
	AuditActionActivityCompleted    = "activity_completed"
)

// ActivityType 活动类型
const (
	ActivityTypeStartEvent        = "startEvent"
	ActivityTypeEndEvent          = "endEvent"
	ActivityTypeUserTask          = "userTask"
	ActivityTypeServiceTask       = "serviceTask"
	ActivityTypeScriptTask        = "scriptTask"
	ActivityTypeManualTask        = "manualTask"
	ActivityTypeGateway           = "gateway"
	ActivityTypeSubProcess        = "subProcess"
	ActivityTypeIntermediateEvent = "intermediateEvent"
)

// AuditContext 审计上下文
type AuditContext struct {
	ProcessInstanceID    int    // ProcessInstance 表的数据库整数主键（instance.ID）
	ProcessInstanceKey   string // BPMN 流程实例业务键（instance.ProcessInstanceID，例如 PI-change-123）
	ProcessDefinitionKey string
	ProcessDefinitionID  int
	ActivityID           string
	ActivityName         string
	ActivityType         string
	Action               string
	UserID               int
	UserName             string
	AssigneeID           int
	AssigneeName         string
	VariablesBefore      map[string]interface{}
	VariablesAfter       map[string]interface{}
	Comment              string
	IPAddress            string
	UserAgent            string
	TenantID             int
	DurationMs           int
	Metadata             map[string]interface{}
}

// RecordAudit 记录审计日志
func (s *BPMNAuditService) RecordAudit(ctx context.Context, auditCtx *AuditContext) error {
	startTime := time.Now()

	// 构建审计日志
	create := s.client.ProcessAuditLog.Create().
		SetProcessInstanceID(auditCtx.ProcessInstanceID).
		SetProcessInstanceKey(auditCtx.ProcessInstanceKey).
		SetProcessDefinitionKey(auditCtx.ProcessDefinitionKey).
		SetProcessDefinitionID(auditCtx.ProcessDefinitionID).
		SetActivityID(auditCtx.ActivityID).
		SetActivityName(auditCtx.ActivityName).
		SetActivityType(auditCtx.ActivityType).
		SetAction(auditCtx.Action).
		SetUserID(auditCtx.UserID).
		SetUserName(auditCtx.UserName).
		SetAssigneeID(auditCtx.AssigneeID).
		SetAssigneeName(auditCtx.AssigneeName).
		SetVariablesBefore(auditCtx.VariablesBefore).
		SetVariablesAfter(auditCtx.VariablesAfter).
		SetComment(auditCtx.Comment).
		SetIPAddress(auditCtx.IPAddress).
		SetUserAgent(auditCtx.UserAgent).
		SetTenantID(auditCtx.TenantID).
		SetTimestamp(time.Now()).
		SetMetadata(auditCtx.Metadata)

	// 如果没有设置durationMs，在保存后计算
	if auditCtx.DurationMs > 0 {
		create.SetDurationMs(auditCtx.DurationMs)
	} else {
		create.SetDurationMs(int(time.Since(startTime).Milliseconds()))
	}

	_, err := create.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to record audit log", "error", err, "action", auditCtx.Action)
		return fmt.Errorf("记录审计日志失败: %w", err)
	}

	s.logger.Debugw("Audit log recorded", "action", auditCtx.Action, "processInstanceKey", auditCtx.ProcessInstanceKey)
	return nil
}

// RecordProcessStarted 记录流程启动
func (s *BPMNAuditService) RecordProcessStarted(ctx context.Context, instance *ent.ProcessInstance, userID int, userName string, variables map[string]interface{}) error {
	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           "start",
		ActivityName:         "流程开始",
		ActivityType:         ActivityTypeStartEvent,
		Action:               AuditActionProcessStarted,
		UserID:               userID,
		UserName:             userName,
		VariablesBefore:      nil,
		VariablesAfter:       variables,
		TenantID:             instance.TenantID,
	})
}

// RecordProcessCompleted 记录流程完成
func (s *BPMNAuditService) RecordProcessCompleted(ctx context.Context, instance *ent.ProcessInstance, userID int, userName string, variables map[string]interface{}) error {
	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           "end",
		ActivityName:         "流程结束",
		ActivityType:         ActivityTypeEndEvent,
		Action:               AuditActionProcessCompleted,
		UserID:               userID,
		UserName:             userName,
		VariablesBefore:      instance.Variables,
		VariablesAfter:       variables,
		TenantID:             instance.TenantID,
	})
}

// RecordTaskAssigned 记录任务分配
func (s *BPMNAuditService) RecordTaskAssigned(ctx context.Context, task *ent.ProcessTask, assignee *ent.User, assignerID int, assignerName string) error {
	assigneeName := ""
	assigneeID := 0
	if assignee != nil {
		assigneeName = assignee.Name
		assigneeID = assignee.ID
	}

	auditCtx, err := s.taskAuditContext(ctx, task, assignerID, assignerName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskAssigned
	auditCtx.AssigneeID = assigneeID
	auditCtx.AssigneeName = assigneeName
	return s.RecordAudit(ctx, auditCtx)
}

// RecordTaskDelegated records the actor and tenant-validated target assignee.
func (s *BPMNAuditService) RecordTaskDelegated(ctx context.Context, task *ent.ProcessTask, actorID int, actorName string, assignee *ent.User) error {
	auditCtx, err := s.taskAuditContext(ctx, task, actorID, actorName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskReassigned
	auditCtx.Metadata = map[string]interface{}{"previous_assignee": task.Assignee}
	if assignee != nil {
		auditCtx.AssigneeID = assignee.ID
		auditCtx.AssigneeName = assignee.Name
	}
	return s.RecordAudit(ctx, auditCtx)
}

// RecordTaskCancelled records a user-visible task cancellation and its reason.
func (s *BPMNAuditService) RecordTaskCancelled(ctx context.Context, task *ent.ProcessTask, userID int, userName, reason string) error {
	auditCtx, err := s.taskAuditContext(ctx, task, userID, userName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskCancelled
	auditCtx.Comment = reason
	return s.RecordAudit(ctx, auditCtx)
}

// RecordTaskVariablesChanged records task-local variable changes.
func (s *BPMNAuditService) RecordTaskVariablesChanged(ctx context.Context, task *ent.ProcessTask, userID int, userName string, before, after map[string]interface{}) error {
	auditCtx, err := s.taskAuditContext(ctx, task, userID, userName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskVariablesChanged
	auditCtx.VariablesBefore = before
	auditCtx.VariablesAfter = after
	return s.RecordAudit(ctx, auditCtx)
}

// RecordCounterSignCreated records creation of a counter-sign task set.
func (s *BPMNAuditService) RecordCounterSignCreated(ctx context.Context, parentTask *ent.ProcessTask, userID int, userName string, approverCount int) error {
	auditCtx, err := s.taskAuditContext(ctx, parentTask, userID, userName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionCounterSignCreated
	auditCtx.Comment = fmt.Sprintf("创建 %d 个会签任务", approverCount)
	auditCtx.Metadata = map[string]interface{}{"approverCount": approverCount}
	return s.RecordAudit(ctx, auditCtx)
}

func (s *BPMNAuditService) taskAuditContext(ctx context.Context, task *ent.ProcessTask, userID int, userName string) (*AuditContext, error) {
	if task == nil {
		return nil, fmt.Errorf("构建任务审计上下文失败: 任务为空")
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务所属流程实例失败: %w", err)
	}
	return &AuditContext{
		ProcessInstanceID:    task.ProcessInstanceID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: task.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           task.TaskDefinitionKey,
		ActivityName:         task.TaskName,
		ActivityType:         task.TaskType,
		UserID:               userID,
		UserName:             userName,
		TenantID:             task.TenantID,
	}, nil
}

// RecordTaskClaimed 记录任务签收
func (s *BPMNAuditService) RecordTaskClaimed(ctx context.Context, task *ent.ProcessTask, userID int, userName string) error {
	auditCtx, err := s.taskAuditContext(ctx, task, userID, userName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskClaimed
	auditCtx.AssigneeID = userID
	auditCtx.AssigneeName = userName
	return s.RecordAudit(ctx, auditCtx)
}

// RecordTaskCompleted 记录任务完成
func (s *BPMNAuditService) RecordTaskCompleted(ctx context.Context, task *ent.ProcessTask, userID int, userName string, variablesBefore, variablesAfter map[string]interface{}) error {
	return s.RecordTaskCompletedWithMetadata(ctx, task, userID, userName, variablesBefore, variablesAfter, nil)
}

func (s *BPMNAuditService) RecordTaskCompletedWithMetadata(ctx context.Context, task *ent.ProcessTask, userID int, userName string, variablesBefore, variablesAfter, metadata map[string]interface{}) error {
	auditCtx, err := s.taskAuditContext(ctx, task, userID, userName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskCompleted
	auditCtx.VariablesBefore = variablesBefore
	auditCtx.VariablesAfter = variablesAfter
	auditCtx.Metadata = metadata
	return s.RecordAudit(ctx, auditCtx)
}

// RecordVariableChanged 记录变量变更
func (s *BPMNAuditService) RecordVariableChanged(ctx context.Context, instance *ent.ProcessInstance, userID int, userName string, varName string, oldValue, newValue interface{}) error {
	variablesBefore := map[string]interface{}{varName: oldValue}
	variablesAfter := map[string]interface{}{varName: newValue}

	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           "variable",
		ActivityName:         "变量变更: " + varName,
		ActivityType:         ActivityTypeServiceTask,
		Action:               AuditActionVariableChanged,
		UserID:               userID,
		UserName:             userName,
		VariablesBefore:      variablesBefore,
		VariablesAfter:       variablesAfter,
		TenantID:             instance.TenantID,
	})
}

// QueryAuditLogs 查询审计日志
func (s *BPMNAuditService) QueryAuditLogs(ctx context.Context, req *QueryAuditLogsRequest) ([]*ent.ProcessAuditLog, int, error) {
	if req == nil {
		return nil, 0, common.NewBadRequestError("审计日志查询不能为空", nil)
	}

	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	if req.ProcessInstanceID > 0 {
		if _, err = s.instanceAccessPolicy.loadForRead(ctx, strconv.Itoa(req.ProcessInstanceID)); err != nil {
			return nil, 0, err
		}
	} else if req.ProcessInstanceKey != "" {
		if _, err = s.instanceAccessPolicy.loadForRead(ctx, req.ProcessInstanceKey); err != nil {
			return nil, 0, err
		}
	} else if _, err = RequireBPMNInstanceReadAll(ctx); err != nil {
		return nil, 0, err
	}

	query := s.client.ProcessAuditLog.Query().
		Where(processauditlog.TenantID(scope.TenantID))

	// 构建查询条件
	if req.ProcessInstanceID > 0 {
		query = query.Where(processauditlog.ProcessInstanceID(req.ProcessInstanceID))
	}
	if req.ProcessInstanceKey != "" {
		query = query.Where(processauditlog.ProcessInstanceKey(req.ProcessInstanceKey))
	}
	if req.ProcessDefinitionKey != "" {
		query = query.Where(processauditlog.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.ActivityID != "" {
		query = query.Where(processauditlog.ActivityID(req.ActivityID))
	}
	if req.ActivityType != "" {
		query = query.Where(processauditlog.ActivityType(req.ActivityType))
	}
	if req.Action != "" {
		query = query.Where(processauditlog.Action(req.Action))
	}
	if req.UserID > 0 {
		query = query.Where(processauditlog.UserID(req.UserID))
	}
	if req.AssigneeID > 0 {
		query = query.Where(processauditlog.AssigneeID(req.AssigneeID))
	}
	if !req.StartTime.IsZero() {
		query = query.Where(processauditlog.TimestampGTE(req.StartTime))
	}
	if !req.EndTime.IsZero() {
		query = query.Where(processauditlog.TimestampLTE(req.EndTime))
	}

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志总数失败: %w", err)
	}

	// 分页查询
	if req.Page > 0 && req.PageSize > 0 {
		query = query.Offset((req.Page - 1) * req.PageSize).Limit(req.PageSize)
	}

	// 排序
	if req.SortBy != "" {
		switch req.SortBy {
		case "timestamp":
			if req.SortOrder == "asc" {
				query = query.Order(ent.Asc(processauditlog.FieldTimestamp))
			} else {
				query = query.Order(ent.Desc(processauditlog.FieldTimestamp))
			}
		default:
			query = query.Order(ent.Desc(processauditlog.FieldTimestamp))
		}
	} else {
		query = query.Order(ent.Desc(processauditlog.FieldTimestamp))
	}

	logs, err := query.All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}

	return logs, total, nil
}

// GetProcessTimeline 获取流程时间线
func (s *BPMNAuditService) GetProcessTimeline(ctx context.Context, processInstanceKey string) ([]*ent.ProcessAuditLog, error) {
	instance, err := s.instanceAccessPolicy.loadForRead(ctx, processInstanceKey)
	if err != nil {
		return nil, err
	}

	logs, err := s.client.ProcessAuditLog.Query().
		Where(
			processauditlog.TenantID(instance.TenantID),
			processauditlog.ProcessInstanceID(instance.ID),
		).
		Order(ent.Asc(processauditlog.FieldTimestamp)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程时间线失败: %w", err)
	}
	return logs, nil
}

// GetUserActivity 获取用户活动
func (s *BPMNAuditService) GetUserActivity(ctx context.Context, userID int, startTime, endTime time.Time) ([]*ent.ProcessAuditLog, error) {
	scope, err := RequireBPMNInstanceReadAll(ctx)
	if err != nil {
		return nil, err
	}
	query := s.client.ProcessAuditLog.Query().
		Where(processauditlog.UserID(userID)).
		Where(processauditlog.TimestampGTE(startTime)).
		Where(processauditlog.TimestampLTE(endTime)).
		Where(processauditlog.TenantID(scope.TenantID))

	return query.Order(ent.Desc(processauditlog.FieldTimestamp)).All(ctx)
}

// GetActivityStatistics 获取活动统计
func (s *BPMNAuditService) GetActivityStatistics(ctx context.Context, processDefinitionKey string, startTime, endTime time.Time) (map[string]int, error) {
	scope, err := RequireBPMNInstanceReadAll(ctx)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]int)

	query := s.client.ProcessAuditLog.Query().
		Where(processauditlog.ProcessDefinitionKey(processDefinitionKey)).
		Where(processauditlog.TimestampGTE(startTime)).
		Where(processauditlog.TimestampLTE(endTime)).
		Where(processauditlog.TenantID(scope.TenantID))

	logs, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取活动统计失败: %w", err)
	}

	for _, log := range logs {
		action := log.Action
		stats[action]++
	}

	return stats, nil
}

// QueryAuditLogsRequest 查询审计日志请求
type QueryAuditLogsRequest struct {
	ProcessInstanceID    int
	ProcessInstanceKey   string
	ProcessDefinitionKey string
	ActivityID           string
	ActivityType         string
	Action               string
	UserID               int
	AssigneeID           int
	StartTime            time.Time
	EndTime              time.Time
	Page                 int
	PageSize             int
	SortBy               string
	SortOrder            string
}
