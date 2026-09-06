package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/connector"
	feishuConnector "itsm-backend/connector/builtin/feishu"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/slaviolation"
	entTicket "itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"
	entTicketComment "itsm-backend/ent/ticketcomment"
	"itsm-backend/ent/user"
	"itsm-backend/repository/base"
	"itsm-backend/repository/ticket"

	"go.uber.org/zap"
)

// TicketService 改进版的工单服务
// 使用构造函数注入和 Repository 模式
type TicketService struct {
	repo                   ticket.Repository
	client                 *ent.Client // 用于 ProcessInstance 等系统级查询（不走 Repository）
	logger                 *zap.SugaredLogger
	notificationSvc        *TicketNotificationService
	automationRuleSvc      *TicketAutomationRuleService
	slaSvc                 *TicketSLAService
	assignmentSmartService *TicketAssignmentSmartService
	connectorManager       *connector.Manager // 连接器管理器，用于飞书等外部集成

	// Existing workflow cancellation remains owned by the process service.
	processTriggerSvc ProcessTriggerServiceInterface
}

// TicketServiceConfig 工单服务配置
// 所有依赖都在配置中明确声明
type TicketServiceConfig struct {
	ProcessTriggerService ProcessTriggerServiceInterface
	Repository            ticket.Repository
	Client                *ent.Client // 可选；传入后可用作 ProcessInstance 等系统级查询
	Logger                *zap.SugaredLogger
	NotificationService   *TicketNotificationService
	AutomationRuleService *TicketAutomationRuleService
	SLAService            *TicketSLAService
	ConnectorManager      *connector.Manager // 连接器管理器
}

// NewTicketService 创建工单服务
// 使用构造函数注入，所有依赖必须显式传入
func NewTicketService(cfg *TicketServiceConfig) *TicketService {
	if cfg.Repository == nil {
		panic("Repository is required")
	}
	if cfg.Logger == nil {
		panic("Logger is required")
	}

	s := &TicketService{
		processTriggerSvc: cfg.ProcessTriggerService,
		repo:              cfg.Repository,
		client:            cfg.Client,
		logger:            cfg.Logger,
		notificationSvc:   cfg.NotificationService,
		automationRuleSvc: cfg.AutomationRuleService,
		slaSvc:            cfg.SLAService,
		connectorManager:  cfg.ConnectorManager,
	}
	if cfg.Client != nil {
		assignmentService := NewTicketAssignmentService(cfg.Client, cfg.Logger)
		assignmentRuleService := NewTicketAssignmentRuleService(cfg.Client, cfg.Logger)
		s.assignmentSmartService = NewTicketAssignmentSmartService(cfg.Client, cfg.Logger, assignmentService, assignmentRuleService)
	}
	return s
}

// NewTicketServiceForTest 构造一个最小可运行的 TicketService（仅用于测试）
// 自动构造一个 EntRepository，避免每个测试都要写完整配置
func NewTicketServiceForTest(client *ent.Client, logger *zap.SugaredLogger) *TicketService {
	return NewTicketService(&TicketServiceConfig{
		Repository: ticket.NewEntRepository(client, logger),
		Client:     client,
		Logger:     logger,
	})
}

// SetNotificationService 注入通知服务（运行时依赖注入）
func (s *TicketService) SetNotificationService(n *TicketNotificationService) {
	s.notificationSvc = n
}

// parseFieldValuesArray 把 formFields["values"] 解析成 [{name,value}] 数组形状，
// 转成内部用的 map[name]value。数组形状是必须的——字段名作为数组元素里的字符串值
// （而不是对象的 key）传输，这样才能绕开前端 http-client.ts 那个全局的、不区分
// 契约字段和用户数据的 snake_case→camelCase 请求体转换（那个转换会把 map 形状里
// 带下划线的字段名 key 悄悄改写，导致匹配失败、值静默丢失）。
// 解析不出数组形状返回 nil，调用方会退回到兼容 map 形状的旧逻辑。
func parseFieldValuesArray(formFields map[string]interface{}) map[string]interface{} {
	if formFields == nil {
		return nil
	}
	rawValues, ok := formFields["values"].([]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]interface{}, len(rawValues))
	for _, raw := range rawValues {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		if val, ok := entry["value"]; ok {
			result[name] = val
		}
	}
	return result
}

// isEmptyFieldValue 判断字段值是否为空
func isEmptyFieldValue(val interface{}) bool {
	if val == nil {
		return true
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	case []interface{}:
		return len(v) == 0
	default:
		return false
	}
}

// extractCustomFieldValues 从提交的 formFields 中取出用户实际填写的自定义字段值（"values" 键），
// 忽略 presetTypeId 等仅用于类型推断/路由的元数据键。
func extractCustomFieldValues(formFields map[string]interface{}) map[string]interface{} {
	if formFields == nil {
		return nil
	}
	if values := parseFieldValuesArray(formFields); values != nil {
		return values
	}
	// 兼容旧的 map 形状——直接调用 service 层的测试/调用方还在用。
	if values, ok := formFields["values"].(map[string]interface{}); ok {
		return values
	}
	return nil
}

// extractAdHocFieldValues 解析 formFields["fieldDefs"]（客户端提交的 {name,label} 列表，
// 用于没有 field_definitions 行的静态预设）配合 formFields["values"] 里的实际值，
// 构造成 AdHocFieldValue 列表。fieldDefs 缺失或为空返回 nil。
func isFinalStatus(s ticket.Status) bool {
	return s == ticket.StatusResolved || s == ticket.StatusClosed || s == ticket.StatusCancelled
}

func getCategoryIDValue(categoryID *int) int {
	if categoryID == nil {
		return 0
	}
	return *categoryID
}

// autoCloseSLAViolations 关闭工单的未解决 SLA 违规记录，返回关闭数量。
func (s *TicketService) autoCloseSLAViolations(ctx context.Context, ticketID int) (int, error) {
	now := time.Now()
	violations, err := s.client.SLAViolation.Query().
		Where(slaviolation.TicketIDEQ(ticketID), slaviolation.ResolvedAtIsNil()).
		All(ctx)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, v := range violations {
		_, err := v.Update().
			SetResolvedAt(now).
			SetIsResolved(true).
			SetResolutionNotes("工单已关闭，系统自动解决违规").
			Save(ctx)
		if err != nil {
			s.logger.Warnw("Failed to auto-close SLA violation", "error", err, "violation_id", v.ID)
			continue
		}
		closed++
	}
	return closed, nil
}

func extractAdHocFieldValues(formFields map[string]interface{}) []AdHocFieldValue {
	if formFields == nil {
		return nil
	}
	rawDefs, ok := formFields["fieldDefs"].([]interface{})
	if !ok || len(rawDefs) == 0 {
		return nil
	}
	values := parseFieldValuesArray(formFields)
	if len(values) == 0 {
		if mapValues, ok := formFields["values"].(map[string]interface{}); ok {
			values = mapValues
		}
	}
	if len(values) == 0 {
		return nil
	}
	result := make([]AdHocFieldValue, 0, len(rawDefs))
	for i, raw := range rawDefs {
		defMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := defMap["name"].(string)
		if name == "" {
			continue
		}
		val, ok := values[name]
		if !ok {
			continue
		}
		label, _ := defMap["label"].(string)
		if label == "" {
			label = name
		}
		result = append(result, AdHocFieldValue{Name: name, Label: label, SortOrder: i, Value: val})
	}
	return result
}

func uniqueIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// isTicketDataScopeAllRole 判断角色是否拥有全租户工单可见权限（DataScopeAll）。
// 阻断8：管理角色（super_admin/sysadmin）可见全租户工单，
// 其余角色（end_user 等）只能查看本人创建或分配给自己的工单。
func isTicketDataScopeAllRole(role string) bool {
	switch role {
	case "super_admin", "sysadmin":
		return true
	default:
		return false
	}
}

// GetWorkflowStatus 获取工单关联的流程状态
// 与 V1 (ticket_service.go:282-319) 等价
func (s *TicketService) GetWorkflowStatus(ctx context.Context, ticketID int, tenantID int) (*dto.ProcessTriggerResponse, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for workflow status query")
	}
	businessKey := fmt.Sprintf("ticket:%d", ticketID)

	processInstance, err := s.client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(businessKey),
			processinstance.TenantID(tenantID),
		).
		WithDefinition().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("未找到工单关联的流程实例")
		}
		return nil, fmt.Errorf("查询流程实例失败: %w", err)
	}

	processDefName := ""
	if processInstance.Edges.Definition != nil {
		processDefName = processInstance.Edges.Definition.Name
	}

	return &dto.ProcessTriggerResponse{
		ProcessInstanceID:     processInstance.ID,
		ProcessDefinitionKey:  processInstance.ProcessDefinitionKey,
		ProcessDefinitionName: processDefName,
		BusinessKey:           processInstance.BusinessKey,
		Status:                mapProcessStatusToDTO(processInstance.Status),
		CurrentActivityID:     processInstance.CurrentActivityID,
		CurrentActivityName:   processInstance.CurrentActivityName,
		StartTime:             processInstance.StartTime,
		EndTime:               &processInstance.EndTime,
	}, nil
}

// CancelWorkflow 取消工单关联的流程
func (s *TicketService) CancelWorkflow(ctx context.Context, ticketID int, tenantID int, reason string) error {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	if scope.TenantID != tenantID || !scope.CanUpdateAllInstances {
		return common.NewForbiddenError("无权取消工单流程")
	}
	if s.client == nil {
		return fmt.Errorf("ent client not available for workflow cancel")
	}
	businessKey := fmt.Sprintf("ticket:%d", ticketID)

	processInstance, err := s.client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(businessKey),
			processinstance.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("未找到工单关联的流程实例")
		}
		return fmt.Errorf("查询流程实例失败: %w", err)
	}

	if s.processTriggerSvc != nil {
		return s.processTriggerSvc.CancelProcess(ctx, processInstance.ID, reason)
	}
	return fmt.Errorf("流程触发服务未配置")
}

// SyncTicketStatusWithWorkflow 同步工单状态与流程状态
func (s *TicketService) SyncTicketStatusWithWorkflow(ctx context.Context, ticketID int, tenantID int) error {
	workflowStatus, err := s.GetWorkflowStatus(ctx, ticketID, tenantID)
	if err != nil {
		s.logger.Warnw("Failed to get workflow status for sync", "error", err, "ticket_id", ticketID)
		return err
	}

	var newStatus ticket.Status
	switch workflowStatus.Status {
	case dto.ProcessStatusCompleted:
		newStatus = ticket.StatusResolved
	case dto.ProcessStatusTerminated, dto.ProcessStatusSuspended:
		newStatus = ticket.StatusPending
	default:
		return nil
	}

	if _, err := s.repo.UpdateStatus(ctx, ticketID, newStatus, tenantID); err != nil {
		return fmt.Errorf("同步工单状态失败: %w", err)
	}

	s.logger.Infow(
		"Ticket status synced with workflow",
		"ticket_id", ticketID,
		"workflow_status", workflowStatus.Status,
		"ticket_status", string(newStatus),
	)
	return nil
}

// mapProcessStatusToDTO 映射流程状态（与 V1 mapProcessStatus 等价）
func mapProcessStatusToDTO(status string) dto.ProcessStatus {
	switch status {
	case "running", "active":
		return dto.ProcessStatusRunning
	case "completed":
		return dto.ProcessStatusCompleted
	case "suspended":
		return dto.ProcessStatusSuspended
	case "terminated", "cancelled":
		return dto.ProcessStatusTerminated
	default:
		return dto.ProcessStatusPending
	}
}

// GetTicket 获取工单
func (s *TicketService) GetTicket(ctx context.Context, id int, tenantID int) (*ticket.Ticket, error) {
	updated, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// GetTicketByNumber 根据编号获取工单
func (s *TicketService) GetTicketByNumber(ctx context.Context, ticketNumber string, tenantID int) (*ticket.Ticket, error) {
	return s.repo.GetByNumber(ctx, ticketNumber, tenantID)
}

// UpdateTicket 更新工单
func (s *TicketService) UpdateTicket(ctx context.Context, id int, req *dto.UpdateTicketRequest, tenantID int) (*ticket.Ticket, error) {
	s.logger.Infow("Updating ticket", "ticket_id", id, "tenant_id", tenantID)
	if req.Status == "approved" || req.Status == "rejected" {
		return nil, fmt.Errorf("审批状态只能由 BPMN 任务命令推进")
	}

	// 获取当前工单
	current, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}

	// 状态转换验证
	if req.Status != "" && ticket.Status(req.Status) != current.Status {
		if !current.CanTransitionTo(ticket.Status(req.Status)) {
			return nil, &ticket.StateError{
				CurrentStatus: current.Status,
				Message:       "invalid state transition",
			}
		}
	}
	if req.Status == string(ticket.StatusResolved) && strings.TrimSpace(req.Resolution) == "" &&
		(current.Resolution == nil || strings.TrimSpace(*current.Resolution) == "") {
		return nil, fmt.Errorf("解决工单时必须填写解决方案")
	}

	// 转换更新参数
	params := &ticket.UpdateParams{
		Version: current.Version,
	}
	if req.Version > 0 {
		params.Version = req.Version
	}

	if req.Title != "" {
		params.Title = &req.Title
	}
	if req.Description != "" {
		params.Description = &req.Description
	}
	if req.Status != "" {
		status := ticket.Status(req.Status)
		params.Status = &status
	}
	if req.Type != "" {
		class, subtype := common.WorkItemIdentityFilter(req.Type)
		if class != "generic" || current.RecordClass != "generic" {
			return nil, fmt.Errorf("legacy type cannot change professional class")
		}
		if err := s.validateGenericSubtype(ctx, tenantID, subtype); err != nil {
			return nil, err
		}
		params.GenericSubtype = &subtype
	}
	if req.Priority != "" {
		priority := ticket.Priority(req.Priority)
		params.Priority = &priority
	}
	if req.AssigneeID != 0 {
		if s.client != nil {
			assigneeExists, err := s.client.User.Query().
				Where(user.IDEQ(req.AssigneeID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
				Exist(ctx)
			if err != nil {
				return nil, fmt.Errorf("验证处理人失败: %w", err)
			}
			if !assigneeExists {
				return nil, fmt.Errorf("处理人不存在或不可用")
			}
		}
		params.AssigneeID = &req.AssigneeID
	}
	categoryID := req.CategoryID
	if categoryID == nil && strings.TrimSpace(req.Category) != "" {
		if s.client == nil {
			return nil, fmt.Errorf("无法解析工单分类")
		}
		category, err := s.client.TicketCategory.Query().
			Where(ticketcategory.NameEQ(strings.TrimSpace(req.Category)), ticketcategory.TenantIDEQ(tenantID), ticketcategory.IsActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("工单分类不存在或不可用")
		}
		categoryID = &category.ID
	}
	if categoryID != nil {
		if *categoryID != 0 && s.client != nil {
			exists, err := s.client.TicketCategory.Query().
				Where(ticketcategory.IDEQ(*categoryID), ticketcategory.TenantIDEQ(tenantID), ticketcategory.IsActiveEQ(true)).
				Exist(ctx)
			if err != nil {
				return nil, fmt.Errorf("验证工单分类失败: %w", err)
			}
			if !exists {
				return nil, fmt.Errorf("工单分类不存在或不可用")
			}
		}
		params.CategoryID = categoryID
	}
	if req.Tags != nil {
		params.ReplaceTags = true
		if len(req.Tags) > 0 {
			if s.client == nil {
				return nil, fmt.Errorf("无法解析工单标签")
			}
			tagIDs, err := NewTicketTagService(s.client).ResolveTagIDsByNames(ctx, req.Tags, tenantID, true)
			if err != nil {
				return nil, fmt.Errorf("解析工单标签失败: %w", err)
			}
			params.TagIDs = tagIDs
		}
	}
	if req.Resolution != "" {
		params.Resolution = &req.Resolution
	}

	// 更新工单
	updated, err := s.repo.Update(ctx, id, params, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to update ticket", "error", err)
		return nil, err
	}

	s.logger.Infow("Ticket updated", "ticket_id", id)

	// 状态变更时发送 ticket_updated 通知
	if req.Status != "" && ticket.Status(req.Status) != current.Status {
		if s.notificationSvc != nil {
			if err := s.notificationSvc.NotifyTicketStatusChanged(ctx, id, string(current.Status), req.Status, tenantID); err != nil {
				s.logger.Warnw("Failed to send status change notification", "error", err, "ticket_id", id)
			}
		}
	}

	// 工单进入终态时自动关闭 SLA 违规
	if req.Status != "" {
		if isFinalStatus(ticket.Status(req.Status)) && s.client != nil {
			if count, err := s.autoCloseSLAViolations(ctx, id); err != nil {
				s.logger.Warnw("Failed to auto-close SLA violations", "error", err, "ticket_id", id)
			} else if count > 0 {
				s.logger.Infow("Auto-closed SLA violations", "ticket_id", id, "count", count)
			}
		}
	}

	// 优先级或分类变更时重新计算 SLA
	if (req.Priority != "" || req.CategoryID != nil || strings.TrimSpace(req.Category) != "") && s.slaSvc != nil {
		slaResult, err := s.slaSvc.CalculateSLADeadlineFromRequest(ctx, tenantID, common.WorkItemLegacyType(updated.RecordClass, updated.GenericSubtype), string(updated.Priority), getCategoryIDValue(categoryID))
		if err != nil {
			s.logger.Warnw("Failed to recalculate SLA after update", "error", err, "ticket_id", id)
		} else {
			if err := s.repo.UpdateSLADeadlines(ctx, id, slaResult.ResponseDeadline, slaResult.ResolutionDeadline, &slaResult.SLADefinitionID, tenantID); err != nil {
				s.logger.Warnw("Failed to persist SLA recalculation", "error", err, "ticket_id", id)
			}
		}
	}

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// DeleteTicket 删除工单
func (s *TicketService) DeleteTicket(ctx context.Context, id int, tenantID int) error {
	return s.repo.Delete(ctx, id, tenantID)
}

// ListTickets 列表查询工单。
// 阻断8 修复：新增 currentUserID + currentRole 参数，按角色注入行级数据权限。
// - 管理角色（super_admin/admin/manager）：DataScopeAll，可见全租户工单。
// - 普通角色（end_user/agent）：DataScopeOwnedOrAssigned，仅可见本人创建或分配给自己的工单。
// 这是安全关键路径：即使前端不传 RequesterID 过滤，service 层也会强制收窄。
func (s *TicketService) ListTickets(ctx context.Context, req *dto.ListTicketsRequest, tenantID int, currentUserID int, currentRole string) (*dto.ListTicketsResponse, error) {
	// 构建过滤参数
	filters := &ticket.FilterParams{}
	if req.Status != "" {
		status := ticket.Status(req.Status)
		filters.Status = &status
	}
	if req.Priority != "" {
		priority := ticket.Priority(req.Priority)
		filters.Priority = &priority
	}
	if req.RequesterID != nil {
		filters.RequesterID = req.RequesterID
	}
	if req.AssigneeID != nil {
		filters.AssigneeID = req.AssigneeID
	}
	if req.Type != "" {
		class, subtype := common.WorkItemIdentityFilter(req.Type)
		filters.RecordClass = &class
		if class == "generic" {
			filters.GenericSubtype = &subtype
		}
	}
	if req.CategoryID != nil {
		filters.CategoryID = req.CategoryID
	}
	if req.ParentTicketID != nil {
		filters.ParentTicketID = req.ParentTicketID
	}
	if req.TemplateID != nil {
		filters.TemplateID = req.TemplateID
	}
	filters.IsOverdue = req.IsOverdue
	if req.Keyword != "" {
		filters.Keyword = req.Keyword
	}
	if req.DateFrom != nil {
		filters.DateFrom = req.DateFrom
	}
	if req.DateTo != nil {
		filters.DateTo = req.DateTo
	}

	// 阻断8：按角色注入行级数据权限。
	// 管理角色放行全租户；普通角色强制收窄到本人创建或分配给自己的工单。
	filters.CurrentUserID = currentUserID
	if isTicketDataScopeAllRole(currentRole) {
		filters.DataScope = ticket.DataScopeAll
	} else {
		filters.DataScope = ticket.DataScopeOwnedOrAssigned
	}

	// 分页参数
	pagination := &base.QueryParams{
		Page:     req.Page,
		PageSize: req.PageSize,
		OrderBy:  req.SortBy,
		OrderDir: req.SortOrder,
	}

	// 查询
	result, err := s.repo.List(ctx, tenantID, filters, pagination)
	if err != nil {
		return nil, err
	}

	// 转换为 DTO
	response := &dto.ListTicketsResponse{
		Total:    result.Total,
		Page:     req.Page,
		PageSize: req.PageSize,
		Tickets:  make([]*dto.TicketResponse, len(result.Data)),
	}

	for i, t := range result.Data {
		response.Tickets[i] = ToTicketResponse(ctx, t)
	}

	return response, nil
}

// AssignTicket 分配工单
func (s *TicketService) AssignTicket(ctx context.Context, ticketID int, assigneeID int, tenantID int) (*ticket.Ticket, error) {
	s.logger.Infow("Assigning ticket", "ticket_id", ticketID, "assignee_id", assigneeID)
	current, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := current.Assign(assigneeID); err != nil {
		return nil, err
	}
	if s.client != nil {
		exists, err := s.client.User.Query().
			Where(user.IDEQ(assigneeID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("验证处理人失败: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("处理人不存在或不可用")
		}
	}
	status := current.Status
	updated, err := s.repo.Update(ctx, ticketID, &ticket.UpdateParams{
		AssigneeID: &assigneeID,
		Status:     &status,
		Version:    current.Version,
	}, tenantID)
	if err != nil {
		return nil, err
	}

	// 发送通知
	if s.notificationSvc != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.notificationSvc.NotifyTicketAssigned(ctx2, ticketID, assigneeID, tenantID); err != nil {
				s.logger.Warnw("Assignment notification failed", "error", err)
			}
		}()
	}

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// ResolveTicket 解决工单
func (s *TicketService) ResolveTicket(ctx context.Context, ticketID int, resolution string, tenantID int) (*ticket.Ticket, error) {
	s.logger.Infow("Resolving ticket", "ticket_id", ticketID)
	resolution = strings.TrimSpace(resolution)
	if resolution == "" {
		return nil, fmt.Errorf("解决方案不能为空")
	}

	// 获取工单
	tkt, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}

	// 状态转换验证
	if !tkt.CanTransitionTo(ticket.StatusResolved) {
		return nil, &ticket.StateError{
			CurrentStatus: tkt.Status,
			Message:       "cannot resolve ticket from current status",
		}
	}

	status := ticket.StatusResolved
	updated, err := s.repo.Update(ctx, ticketID, &ticket.UpdateParams{
		Status:     &status,
		Resolution: &resolution,
		Version:    tkt.Version,
	}, tenantID)
	if err != nil {
		return nil, err
	}

	s.logger.Infow("Ticket resolved", "ticket_id", ticketID)

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// CloseTicket 关闭工单
func (s *TicketService) CloseTicket(ctx context.Context, ticketID int, tenantID int, feedback string) (*ticket.Ticket, error) {
	s.logger.Infow("Closing ticket", "ticket_id", ticketID, "tenant_id", tenantID)

	// 获取工单
	tkt, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}

	// 状态转换验证
	if !tkt.CanTransitionTo(ticket.StatusClosed) {
		return nil, &ticket.StateError{
			CurrentStatus: tkt.Status,
			Message:       "cannot close ticket from current status",
		}
	}

	status := ticket.StatusClosed
	params := &ticket.UpdateParams{Status: &status, Version: tkt.Version}
	if strings.TrimSpace(feedback) != "" {
		feedback = strings.TrimSpace(feedback)
		params.Resolution = &feedback
	}
	updated, err := s.repo.Update(ctx, ticketID, params, tenantID)
	if err != nil {
		return nil, err
	}

	s.logger.Infow("Ticket closed", "ticket_id", ticketID)

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// GetTicketStats 获取工单统计
func (s *TicketService) GetTicketStats(ctx context.Context, tenantID int) (*dto.TicketStatsResponse, error) {
	statusCounts, err := s.repo.CountByStatus(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	overdue, err := s.repo.FindOverdue(ctx, tenantID)
	if err != nil {
		s.logger.Warnw("Failed to get overdue tickets", "error", err)
	}

	total := 0
	for _, count := range statusCounts {
		total += count
	}

	return &dto.TicketStatsResponse{
		Total:        total,
		Open:         statusCounts[ticket.StatusNew] + statusCounts[ticket.StatusOpen],
		InProgress:   statusCounts[ticket.StatusInProgress],
		Resolved:     statusCounts[ticket.StatusResolved],
		Pending:      statusCounts[ticket.StatusNew] + statusCounts[ticket.StatusPending],
		HighPriority: 0, // 需要单独查询
		Overdue:      len(overdue),
	}, nil
}

// ==================== 辅助方法 ====================

// ToTicketResponse 是工单领域模型转 DTO 响应的唯一入口，创建/详情/列表所有路径都应该调用它。
// 列表路径直接用这个函数，不查字段值（避免 N+1）。
func ToTicketResponse(ctx context.Context, t *ticket.Ticket) *dto.TicketResponse {
	if t == nil {
		return nil
	}
	resp := &dto.TicketResponse{
		ID:             t.ID,
		TicketNumber:   t.TicketNumber,
		Title:          t.Title,
		Description:    t.Description,
		Status:         string(t.Status),
		Priority:       string(t.Priority),
		Type:           common.WorkItemLegacyType(t.RecordClass, t.GenericSubtype),
		GenericSubtype: t.GenericSubtype,
		RequesterID:    t.RequesterID,
		TenantID:       t.TenantID,
		Version:        t.Version,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
		Source:         t.Source,
	}

	if t.AssigneeID != nil {
		resp.AssigneeID = *t.AssigneeID
	}
	if t.CategoryID != nil {
		resp.CategoryID = *t.CategoryID
	}
	if t.DepartmentID != nil {
		resp.DepartmentID = *t.DepartmentID
	}
	if t.ParentTicketID != nil {
		resp.ParentTicketID = *t.ParentTicketID
	}
	resp.TemplateID = t.TemplateID
	if t.Resolution != nil {
		resp.Resolution = *t.Resolution
	}
	resp.ResolvedAt = t.ResolvedAt
	resp.ClosedAt = t.ClosedAt
	resp.FirstResponseAt = t.FirstResponseAt
	resp.SLAResponseDeadline = t.SLAResponseDeadline
	resp.SLAResolutionDeadline = t.SLAResolutionDeadline

	return resp
}

// ToTicketResponseWithCustomFields 是 TicketService 持有 client 时的便捷封装，
// 供没有单独持有 *ent.Client 的调用方（如 MSPController）获取带自定义字段值的详情响应。
func (s *TicketService) ToTicketResponseWithCustomFields(ctx context.Context, t *ticket.Ticket) *dto.TicketResponse {
	return ToTicketResponseWithCustomFields(ctx, s.client, t)
}

// ToTicketResponseWithCustomFieldsAndActions 在 ToTicketResponseWithCustomFields 基础上
// 额外组装 actions（批准/拒绝/分配/编辑/抄送/删除权限）。需要调用者身份（actorUserID/actorRole），
// 只用于真正的详情响应场景——BuildTicketActions 内部会为 CanCC/CanDelete 各发起一次查询。
func ToTicketResponseWithCustomFieldsAndActions(ctx context.Context, client *ent.Client, t *ticket.Ticket, actorUserID int, actorRole string) *dto.TicketResponse {
	resp := ToTicketResponseWithCustomFields(ctx, client, t)
	if resp == nil || client == nil {
		return resp
	}
	actor := ActionActor{Client: client, TenantID: t.TenantID, UserID: actorUserID, Role: actorRole}
	resp.Actions = BuildTicketActions(ctx, actor, t)
	return resp
}

// ToTicketResponseWithCustomFields 在 ToTicketResponse 基础上额外查一次 field_values。
// 只用于单条工单详情/创建响应，列表接口不调用（避免 N+1）。
func ToTicketResponseWithCustomFields(ctx context.Context, client *ent.Client, t *ticket.Ticket) *dto.TicketResponse {
	resp := ToTicketResponse(ctx, t)
	if resp == nil || client == nil {
		return resp
	}
	values, err := NewFieldValueService(client).ListValues(ctx, t.TenantID, "ticket", t.ID)
	if err != nil {
		zap.S().Warnw("Failed to load custom field values for ticket response", "error", err, "ticket_id", t.ID)
		return resp
	}
	if len(values) == 0 {
		return resp
	}
	resp.CustomFieldValues = make([]dto.CustomFieldValueResponse, 0, len(values))
	for _, v := range values {
		resp.CustomFieldValues = append(resp.CustomFieldValues, dto.CustomFieldValueResponse{
			Name: v.Name, Label: v.Label, Value: v.Value,
		})
	}
	return resp
}

// toEntTicket 转换为 Ent 工单（兼容现有 ProcessResolver / BPMN 触发）
// 用于 BPMN 流程解析、触发、状态同步等需要走 Ent 查询的场景。
// 这是一个临时方案：理想情况下 ProcessResolver 应该接受领域模型。
func (s *TicketService) toEntTicket(t *ticket.Ticket) *ent.Ticket {
	entTicket := &ent.Ticket{
		ID:             t.ID,
		TicketNumber:   t.TicketNumber,
		Title:          t.Title,
		Description:    t.Description,
		Status:         string(t.Status),
		GenericSubtype: t.GenericSubtype,
		RecordClass:    t.RecordClass,
		Priority:       string(t.Priority),
		RequesterID:    t.RequesterID,
		TenantID:       t.TenantID,
		Version:        t.Version,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}
	if t.AssigneeID != nil {
		entTicket.AssigneeID = *t.AssigneeID
	}
	if t.CategoryID != nil {
		entTicket.CategoryID = *t.CategoryID
	}
	if t.Resolution != nil {
		entTicket.Resolution = *t.Resolution
	}
	if t.ResolvedAt != nil {
		entTicket.ResolvedAt = *t.ResolvedAt
	}
	entTicket.ClosedAt = t.ClosedAt
	entTicket.CustomFieldValues = t.CustomFieldValues
	return entTicket
}

// ==================== 状态变更 / SLA / 批量 / 升级 / 查询（V1 兼容） ====================

// UpdateTicketStatus 更新工单状态（等价 V1.TicketService.UpdateTicketStatus）
func (s *TicketService) UpdateTicketStatus(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) (*ticket.Ticket, error) {
	if status == "approved" || status == "rejected" {
		return nil, fmt.Errorf("审批状态只能由 BPMN 任务命令推进")
	}
	return s.updateTicketStatus(ctx, ticketID, status, tenantID, operatorID)
}

func (s *TicketService) updateTicketStatus(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) (*ticket.Ticket, error) {
	s.logger.Infow("Updating ticket status", "ticket_id", ticketID, "status", status, "tenant_id", tenantID, "operator_id", operatorID)

	current, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if !IsValidTicketStatusTransition(string(current.Status), status) {
		return nil, fmt.Errorf("invalid status transition: %s -> %s", current.Status, status)
	}
	if status == string(ticket.StatusResolved) && (current.Resolution == nil || strings.TrimSpace(*current.Resolution) == "") {
		return nil, fmt.Errorf("解决工单必须通过 ResolveTicket 提交解决方案")
	}

	updated, err := s.repo.UpdateStatus(ctx, ticketID, ticket.Status(status), tenantID)
	if err != nil {
		s.logger.Errorw("Failed to update ticket status", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to update ticket status: %w", err)
	}

	// 如果是解决或关闭状态，标记 FirstResponse / Resolved 时间
	if status == string(ticket.StatusResolved) || status == string(ticket.StatusClosed) {
		if updated.FirstResponseAt == nil {
			_ = s.repo.MarkFirstResponse(ctx, ticketID, tenantID)
		}
	}

	s.logger.Infow("Ticket status updated", "ticket_id", ticketID, "new_status", status)

	// 状态变更通知（ticket_updated）
	if s.notificationSvc != nil {
		if err := s.notificationSvc.NotifyTicketStatusChanged(ctx, ticketID, string(current.Status), status, tenantID); err != nil {
			s.logger.Warnw("Failed to send status change notification", "error", err, "ticket_id", ticketID)
		}
	}

	updated, err = s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// UpdateTicketStatusForWorkflow 是给 BPMN ServiceTask handler 用的窄接口适配：
// handler 不需要返回的领域 Ticket 对象，也不能 import service 包（会形成 service ->
// service/bpmn -> service 的循环依赖），所以这里只暴露一个返回 error 的签名，供
// service/bpmn 包本地声明的接口去匹配。
func (s *TicketService) UpdateTicketStatusForWorkflow(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) error {
	_, err := s.updateTicketStatus(ctx, ticketID, status, tenantID, operatorID)
	return err
}

// TicketSLAInfo 工单 SLA 信息
type TicketSLAInfo struct {
	TicketID                int        `json:"ticketId"`
	TicketNumber            string     `json:"ticketNumber"`
	Priority                string     `json:"priority"`
	SLADefinitionID         int        `json:"slaDefinitionId"`
	SlaName                 string     `json:"slaName"`
	ServiceType             string     `json:"serviceType"`
	ResponseTime            int        `json:"responseTime"`
	ResolutionTime          int        `json:"resolutionTime"`
	ResponseDeadline        *time.Time `json:"responseDeadline"`
	ResolutionDeadline      *time.Time `json:"resolutionDeadline"`
	IsBreached              bool       `json:"isBreached"`
	SlaStatus               string     `json:"slaStatus"` // on_track | at_risk | breached
	ResponseTimeRemaining   *int       `json:"responseTimeRemaining"`
	ResolutionTimeRemaining *int       `json:"resolutionTimeRemaining"`
	FirstResponseAt         *time.Time `json:"firstResponseAt,omitempty"`
	ResolvedAt              *time.Time `json:"resolvedAt,omitempty"`
}

// GetTicketSLAInfo 获取工单 SLA 信息
func (s *TicketService) GetTicketSLAInfo(ctx context.Context, ticketID int, tenantID int) (*TicketSLAInfo, error) {
	tkt, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	info := &TicketSLAInfo{
		TicketID:        tkt.ID,
		TicketNumber:    tkt.TicketNumber,
		Priority:        string(tkt.Priority),
		SLADefinitionID: 0,
		SlaName:         "默认SLA",
	}
	if tkt.SLADefinitionID != nil {
		info.SLADefinitionID = *tkt.SLADefinitionID
	}
	if tkt.SLAResponseDeadline != nil {
		info.ResponseDeadline = tkt.SLAResponseDeadline
	}
	if tkt.SLAResolutionDeadline != nil {
		info.ResolutionDeadline = tkt.SLAResolutionDeadline
	}
	if tkt.FirstResponseAt != nil {
		info.FirstResponseAt = tkt.FirstResponseAt
	}
	if tkt.ResolvedAt != nil {
		info.ResolvedAt = tkt.ResolvedAt
	}

	// 获取 SLA 定义详情
	if info.SLADefinitionID > 0 && s.client != nil {
		sla, err := s.client.SLADefinition.Get(ctx, info.SLADefinitionID)
		if err == nil && sla != nil {
			info.SlaName = sla.Name
			info.ServiceType = sla.ServiceType
			info.ResponseTime = sla.ResponseTime
			info.ResolutionTime = sla.ResolutionTime
		}
	}

	// 计算剩余时间和违规状态
	now := time.Now()
	info.IsBreached = false
	info.SlaStatus = "on_track"

	if info.ResponseDeadline != nil && !info.ResponseDeadline.IsZero() {
		remaining := int(info.ResponseDeadline.Sub(now).Minutes())
		info.ResponseTimeRemaining = &remaining
		if remaining < 0 {
			info.IsBreached = true
		} else if total := info.ResponseTime; total > 0 && remaining < total/5 {
			info.SlaStatus = "at_risk"
		}
	}

	if info.ResolutionDeadline != nil && !info.ResolutionDeadline.IsZero() {
		remaining := int(info.ResolutionDeadline.Sub(now).Minutes())
		info.ResolutionTimeRemaining = &remaining
		if remaining < 0 {
			info.IsBreached = true
		} else if total := info.ResolutionTime; total > 0 && remaining < total/5 {
			info.SlaStatus = "at_risk"
		}
	}

	if info.IsBreached {
		info.SlaStatus = "breached"
	}

	return info, nil
}

// BatchDeleteTickets 批量删除工单
func (s *TicketService) BatchDeleteTickets(ctx context.Context, ticketIDs []int, tenantID int) error {
	s.logger.Infow("Batch deleting tickets", "ticket_ids", ticketIDs, "tenant_id", tenantID)
	if len(ticketIDs) == 0 {
		return nil
	}
	return s.repo.BatchDelete(ctx, ticketIDs, tenantID)
}

// EscalateTicket 升级工单
func (s *TicketService) EscalateTicket(ctx context.Context, ticketID int, reason string, tenantID int, escalatedBy int) (*ticket.Ticket, error) {
	s.logger.Infow("Escalating ticket", "ticket_id", ticketID, "reason", reason, "tenant_id", tenantID)

	current, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}

	newPriority := s.getEscalatedPriority(string(current.Priority))
	newAssignee := s.getEscalationAssignee(newPriority, tenantID)

	params := &ticket.UpdateParams{
		Version: current.Version,
		Priority: func() *ticket.Priority {
			p := ticket.Priority(newPriority)
			return &p
		}(),
		AssigneeID: &newAssignee,
		Status: func() *ticket.Status {
			st := ticket.StatusInProgress
			return &st
		}(),
	}

	updated, err := s.repo.Update(ctx, ticketID, params, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to escalate ticket", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to escalate ticket: %w", err)
	}

	if s.notificationSvc != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.notificationSvc.NotifyTicketAssigned(ctx2, ticketID, newAssignee, tenantID)
		}()
	}

	s.logger.Infow("Ticket escalated", "ticket_id", ticketID, "new_priority", newPriority, "new_assignee", newAssignee)

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(tenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// SearchTickets 高级搜索工单
func (s *TicketService) SearchTickets(ctx context.Context, searchTerm string, tenantID int) ([]*ticket.Ticket, error) {
	s.logger.Infow("Searching tickets", "search_term", searchTerm, "tenant_id", tenantID)
	term := strings.TrimSpace(searchTerm)
	if term == "" {
		return []*ticket.Ticket{}, nil
	}
	// V2 Repository 暂不提供全文搜索，走 ent 客户端查询并转为领域模型
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for search")
	}
	ents, err := s.client.Ticket.Query().
		Where(
			entTicket.TenantID(tenantID),
			entTicket.Or(
				entTicket.TitleContains(strings.ToLower(term)),
				entTicket.DescriptionContains(strings.ToLower(term)),
			),
		).
		Order(ent.Desc(entTicket.FieldCreatedAt)).
		Limit(100).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}
	result := make([]*ticket.Ticket, len(ents))
	for i, e := range ents {
		result[i] = s.entToDomain(e)
	}
	return result, nil
}

// GetOverdueTickets 获取逾期工单（V2 走 SLA 服务或 Repository 兜底）
func (s *TicketService) GetOverdueTickets(ctx context.Context, tenantID int) ([]*ticket.Ticket, error) {
	s.logger.Infow("Getting overdue tickets", "tenant_id", tenantID)
	if s.slaSvc != nil {
		ents, err := s.slaSvc.GetOverdueTickets(ctx, tenantID)
		if err == nil {
			result := make([]*ticket.Ticket, len(ents))
			for i, e := range ents {
				result[i] = s.entToDomain(e)
			}
			return result, nil
		}
		s.logger.Warnw("slaSvc.GetOverdueTickets failed, falling back", "error", err)
	}
	ents, err := s.repo.FindOverdue(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get overdue tickets: %w", err)
	}
	return ents, nil
}

// GetTicketsByAssignee 获取指定处理人的工单
func (s *TicketService) GetTicketsByAssignee(ctx context.Context, assigneeID int, tenantID int) ([]*ticket.Ticket, error) {
	s.logger.Infow("Getting tickets by assignee", "assignee_id", assigneeID, "tenant_id", tenantID)
	return s.repo.FindByAssignee(ctx, assigneeID, tenantID)
}

// GetTicketActivity 获取工单活动日志（合并 comments、attachments、状态变更）
func (s *TicketService) GetTicketActivity(ctx context.Context, ticketID int, tenantID int) ([]*dto.TicketActivityItem, error) {
	s.logger.Infow("Getting ticket activity", "ticket_id", ticketID, "tenant_id", tenantID)
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for activity query")
	}
	tkt, err := s.repo.GetByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}

	activities := make([]*dto.TicketActivityItem, 0)

	// 创建事件
	activities = append(activities, &dto.TicketActivityItem{
		ID:           1,
		Action:       "created",
		User:         &dto.ActivityUser{ID: tkt.RequesterID},
		ChangeReason: "工单已创建",
		NewValue:     toPointer(tkt.Title),
		CreatedAt:    tkt.CreatedAt.Format(time.RFC3339),
	})

	// 评论事件
	comments, err := s.client.TicketComment.Query().
		Where(entTicketComment.TicketID(ticketID)).
		WithUser().
		Order(ent.Asc(entTicketComment.FieldCreatedAt)).
		All(ctx)
	if err == nil {
		for _, c := range comments {
			user := &dto.ActivityUser{ID: c.UserID}
			if c.Edges.User != nil {
				user.Name = c.Edges.User.Name
				user.Username = c.Edges.User.Username
			}
			activities = append(activities, &dto.TicketActivityItem{
				ID:           int(c.ID),
				Action:       "commented",
				User:         user,
				ChangeReason: "添加了评论",
				CreatedAt:    c.CreatedAt.Format(time.RFC3339),
			})
		}
	} else {
		s.logger.Warnw("Failed to get comments for activity", "error", err)
	}

	// 分配事件
	if tkt.AssigneeID != nil && *tkt.AssigneeID > 0 {
		activities = append(activities, &dto.TicketActivityItem{
			ID:           2,
			Action:       "assigned",
			User:         &dto.ActivityUser{ID: *tkt.AssigneeID},
			ChangeReason: "工单已分配",
			NewValue:     toPointer(strconv.Itoa(*tkt.AssigneeID)),
			CreatedAt:    tkt.UpdatedAt.Format(time.RFC3339),
		})
	}

	// 首次响应
	if tkt.FirstResponseAt != nil {
		activities = append(activities, &dto.TicketActivityItem{
			ID:           3,
			Action:       "first_response",
			ChangeReason: "首次响应工单",
			CreatedAt:    tkt.FirstResponseAt.Format(time.RFC3339),
		})
	}

	// 解决事件
	if tkt.ResolvedAt != nil {
		activities = append(activities, &dto.TicketActivityItem{
			ID:           4,
			Action:       "resolved",
			ChangeReason: "工单已解决",
			CreatedAt:    tkt.ResolvedAt.Format(time.RFC3339),
		})
	}

	// 倒序（最新在前）
	for i, j := 0, len(activities)-1; i < j; i, j = i+1, j-1 {
		activities[i], activities[j] = activities[j], activities[i]
	}
	return activities, nil
}

// ==================== 辅助函数 ====================

// getEscalatedPriority 获取升级后的优先级
func (s *TicketService) getEscalatedPriority(currentPriority string) string {
	switch currentPriority {
	case "low":
		return "medium"
	case "medium":
		return "high"
	case "high":
		return "critical"
	default:
		return "high"
	}
}

// getEscalationAssignee 获取升级后的处理人
func (s *TicketService) getEscalationAssignee(priority string, tenantID int) int {
	switch priority {
	case "critical":
		return 1
	case "high":
		return 2
	default:
		return 3
	}
}

// entToDomain 将 ent.Ticket 转为领域模型（用于 SearchTickets / GetOverdueTickets 等结果适配）
func (s *TicketService) entToDomain(e *ent.Ticket) *ticket.Ticket {
	if e == nil {
		return nil
	}
	t := &ticket.Ticket{
		ID:             e.ID,
		TicketNumber:   e.TicketNumber,
		Title:          e.Title,
		Description:    e.Description,
		Status:         ticket.Status(e.Status),
		GenericSubtype: e.GenericSubtype,
		Priority:       ticket.Priority(e.Priority),
		RequesterID:    e.RequesterID,
		TenantID:       e.TenantID,
		Version:        e.Version,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
		Source:         e.Source,
	}
	if e.AssigneeID > 0 {
		aid := e.AssigneeID
		t.AssigneeID = &aid
	}
	if e.CategoryID > 0 {
		cid := e.CategoryID
		t.CategoryID = &cid
	}
	if e.Resolution != "" {
		r := e.Resolution
		t.Resolution = &r
	}
	if !e.FirstResponseAt.IsZero() {
		ft := e.FirstResponseAt
		t.FirstResponseAt = &ft
	}
	if !e.ResolvedAt.IsZero() {
		rt := e.ResolvedAt
		t.ResolvedAt = &rt
	}
	return t
}

// ==================== 导出/导入/批量分配/分析 ====================

// ExportTickets 导出工单
func (s *TicketService) ExportTickets(ctx context.Context, tenantID int, filters map[string]interface{}, format string) ([]byte, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for export")
	}
	query := s.client.Ticket.Query().Where(entTicket.TenantID(tenantID))
	if status, ok := filters["status"].(string); ok && status != "" {
		query = query.Where(entTicket.StatusEQ(status))
	}
	if priority, ok := filters["priority"].(string); ok && priority != "" {
		query = query.Where(entTicket.PriorityEQ(priority))
	}
	tickets, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	exportData := make([]map[string]interface{}, 0, len(tickets))
	for _, t := range tickets {
		exportData = append(exportData, map[string]interface{}{
			"工单编号": t.TicketNumber,
			"标题":   t.Title,
			"描述":   t.Description,
			"状态":   t.Status,
			"优先级":  t.Priority,
			"创建时间": t.CreatedAt.Format("2006-01-02 15:04:05"),
			"更新时间": t.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	switch format {
	case "csv":
		return s.generateCSV(exportData)
	case "excel":
		return s.generateExcel(exportData)
	case "json":
		return json.Marshal(exportData)
	default:
		return nil, fmt.Errorf("不支持的导出格式: %s", format)
	}
}

// AssignTickets 批量分配工单
func (s *TicketService) AssignTickets(ctx context.Context, tenantID int, ticketIDs []int, assigneeID int) error {
	if s.client == nil {
		return fmt.Errorf("ent client not available for assign")
	}
	if _, err := s.client.User.Get(ctx, assigneeID); err != nil {
		return fmt.Errorf("分配者不存在: %v", err)
	}
	for _, ticketID := range ticketIDs {
		if _, err := s.repo.AssignTicket(ctx, ticketID, assigneeID, tenantID); err != nil {
			return fmt.Errorf("分配工单 %d 失败: %v", ticketID, err)
		}
	}
	return nil
}

// BatchCloseTickets 批量关闭工单
func (s *TicketService) BatchCloseTickets(ctx context.Context, ticketIDs []int, tenantID int, closeReason string) error {
	for _, ticketID := range ticketIDs {
		if _, err := s.CloseTicket(ctx, ticketID, tenantID, closeReason); err != nil {
			return fmt.Errorf("关闭工单 %d 失败: %v", ticketID, err)
		}
	}
	return nil
}

// BatchUpdatePriority 批量更新优先级
func (s *TicketService) BatchUpdatePriority(ctx context.Context, ticketIDs []int, priority string, tenantID int) error {
	for _, ticketID := range ticketIDs {
		p := ticket.Priority(priority)
		_, err := s.repo.Update(ctx, ticketID, &ticket.UpdateParams{Priority: &p}, tenantID)
		if err != nil {
			return fmt.Errorf("更新工单 %d 优先级失败: %v", ticketID, err)
		}
	}
	return nil
}

// GetTicketAnalytics 获取工单分析数据
func (s *TicketService) GetTicketAnalytics(ctx context.Context, tenantID int, dateFrom, dateTo time.Time) (*dto.TicketAnalyticsResponse, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for analytics")
	}
	query := s.client.Ticket.Query().Where(entTicket.TenantID(tenantID))
	if !dateFrom.IsZero() {
		query = query.Where(entTicket.CreatedAtGTE(dateFrom))
	}
	if !dateTo.IsZero() {
		query = query.Where(entTicket.CreatedAtLTE(dateTo))
	}
	total, err := query.Count(ctx)
	if err != nil {
		return nil, err
	}
	tickets, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	statusStats := make(map[string]int)
	priorityStats := make(map[string]int)
	for _, t := range tickets {
		statusStats[t.Status]++
		priorityStats[t.Priority]++
	}
	resolvedTickets, err := query.Where(entTicket.StatusEQ("resolved")).All(ctx)
	if err != nil {
		return nil, err
	}
	var totalResolutionTime time.Duration
	resolvedCount := 0
	for _, t := range resolvedTickets {
		if !t.UpdatedAt.IsZero() {
			totalResolutionTime += t.UpdatedAt.Sub(t.CreatedAt)
			resolvedCount++
		}
	}
	avgResolutionTime := time.Duration(0)
	if resolvedCount > 0 {
		avgResolutionTime = totalResolutionTime / time.Duration(resolvedCount)
	}
	return &dto.TicketAnalyticsResponse{
		Data: []map[string]interface{}{
			{"total": total},
			{"status_distribution": statusStats},
			{"priority_distribution": priorityStats},
			{"avg_resolution_time": avgResolutionTime.Hours()},
			{"resolved_count": resolvedCount},
		},
		Summary: map[string]interface{}{
			"total":    total,
			"resolved": resolvedCount,
		},
		GeneratedAt: time.Now(),
	}, nil
}

// ==================== 模板 CRUD ====================

// ToFieldDefinitionInputs 把前端提交的模板字段（[]map[string]interface{}）转换成
// FieldDefinitionService 消费的 []FieldDefinitionInput。
func ToFieldDefinitionInputs(fields []map[string]interface{}) []FieldDefinitionInput {
	result := make([]FieldDefinitionInput, 0, len(fields))
	for i, f := range fields {
		name, _ := f["name"].(string)
		if name == "" {
			continue
		}
		label, _ := f["label"].(string)
		fieldType, _ := f["type"].(string)
		required, _ := f["required"].(bool)
		var options []interface{}
		if raw, ok := f["options"].([]interface{}); ok {
			options = raw
		}
		result = append(result, FieldDefinitionInput{
			Name:      name,
			Label:     label,
			FieldType: fieldType,
			Required:  required,
			Options:   options,
			SortOrder: i,
		})
	}
	return result
}

// CreateTicketTemplate 创建工单模板
func (s *TicketService) CreateTicketTemplate(ctx context.Context, tenantID int, req interface{}) (interface{}, error) {
	createReq, ok := req.(*dto.TicketTemplate)
	if !ok {
		return nil, fmt.Errorf("无效的请求参数类型")
	}
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for template")
	}
	isActive := true
	priority := strings.TrimSpace(createReq.Priority)
	if priority == "" {
		priority = "medium"
	}

	templateService := NewTicketTemplateService(s.client)
	serviceReq := &CreateTemplateRequest{
		Name:          createReq.Name,
		Description:   createReq.Description,
		Category:      createReq.Category,
		Priority:      priority,
		Fields:        ToFieldDefinitionInputs(createReq.Fields),
		CategoryIDs:   createReq.CategoryIDs,
		WorkflowSteps: createReq.WorkflowSteps,
		IsActive:      isActive,
		TenantID:      tenantID,
	}
	template, err := templateService.CreateTemplate(ctx, serviceReq)
	if err != nil {
		return nil, err
	}
	return s.toTicketTemplateDTO(ctx, template)
}

// UpdateTicketTemplate 更新工单模板
func (s *TicketService) UpdateTicketTemplate(ctx context.Context, tenantID int, templateID int, req interface{}) (interface{}, error) {
	updateReq, ok := req.(*dto.TicketTemplate)
	if !ok {
		return nil, fmt.Errorf("无效的请求参数类型")
	}
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for template")
	}
	var isActive *bool
	if updateReq.IsActiveAlt != nil {
		isActive = updateReq.IsActiveAlt
	}
	priority := strings.TrimSpace(updateReq.Priority)
	templateService := NewTicketTemplateService(s.client)
	var fields []FieldDefinitionInput
	if updateReq.Fields != nil {
		fields = ToFieldDefinitionInputs(updateReq.Fields)
	}
	serviceReq := &UpdateTemplateRequest{
		Name:          updateReq.Name,
		Description:   updateReq.Description,
		Category:      updateReq.Category,
		Priority:      priority,
		Fields:        fields,
		CategoryIDs:   updateReq.CategoryIDs,
		WorkflowSteps: updateReq.WorkflowSteps,
		IsActive:      isActive,
	}
	template, err := templateService.UpdateTemplate(ctx, templateID, serviceReq, tenantID)
	if err != nil {
		return nil, err
	}
	return s.toTicketTemplateDTO(ctx, template)
}

// DeleteTicketTemplate 删除工单模板
func (s *TicketService) DeleteTicketTemplate(ctx context.Context, tenantID int, templateID int) error {
	if s.client == nil {
		return fmt.Errorf("ent client not available for template")
	}
	templateService := NewTicketTemplateService(s.client)
	return templateService.DeleteTemplate(ctx, templateID, tenantID)
}

// GetTicketTemplates 获取工单模板列表
func (s *TicketService) GetTicketTemplates(ctx context.Context, tenantID int) ([]interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for template")
	}
	templateService := NewTicketTemplateService(s.client)
	templates, _, err := templateService.ListTemplates(ctx, &ListTemplatesRequest{
		Page:      1,
		PageSize:  100,
		TenantID:  tenantID,
		SortBy:    "created_at",
		SortOrder: "desc",
	})
	if err != nil {
		return nil, err
	}
	result := make([]interface{}, 0, len(templates))
	for _, template := range templates {
		templateDTO, err := s.toTicketTemplateDTO(ctx, template)
		if err != nil {
			return nil, err
		}
		result = append(result, templateDTO)
	}
	return result, nil
}

// GetTicketTemplate 获取工单模板详情
func (s *TicketService) GetTicketTemplate(ctx context.Context, tenantID int, templateID int) (*dto.TicketTemplate, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for template")
	}
	templateService := NewTicketTemplateService(s.client)
	template, err := templateService.GetTemplate(ctx, templateID, tenantID)
	if err != nil {
		return nil, err
	}
	return s.toTicketTemplateDTO(ctx, template)
}

// UpdateTicketTemplateStatus 启用或停用工单模板
func (s *TicketService) UpdateTicketTemplateStatus(ctx context.Context, tenantID int, templateID int, isActive bool) (*dto.TicketTemplate, error) {
	returned, err := s.UpdateTicketTemplate(ctx, tenantID, templateID, &dto.TicketTemplate{
		IsActiveAlt: &isActive,
	})
	if err != nil {
		return nil, err
	}
	template, ok := returned.(*dto.TicketTemplate)
	if !ok {
		return nil, fmt.Errorf("invalid template response type")
	}
	return template, nil
}

// CopyTicketTemplate 复制工单模板
func (s *TicketService) CopyTicketTemplate(ctx context.Context, tenantID int, templateID int, newName string) (*dto.TicketTemplate, error) {
	source, err := s.GetTicketTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(newName) == "" {
		newName = source.Name + " - 副本"
	}
	copied, err := s.CreateTicketTemplate(ctx, tenantID, &dto.TicketTemplate{
		Name:          newName,
		Description:   source.Description,
		Category:      source.Category,
		Priority:      source.Priority,
		Fields:        source.Fields,
		WorkflowSteps: source.WorkflowSteps,
		IsActive:      source.IsActive,
	})
	if err != nil {
		return nil, err
	}
	template, ok := copied.(*dto.TicketTemplate)
	if !ok {
		return nil, fmt.Errorf("invalid template response type")
	}
	return template, nil
}

// GetTicketTemplateCategories 获取模板分类
func (s *TicketService) GetTicketTemplateCategories(ctx context.Context, tenantID int) ([]string, error) {
	templates, err := s.GetTicketTemplates(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	categories := make([]string, 0)
	for _, item := range templates {
		template, ok := item.(*dto.TicketTemplate)
		if !ok || strings.TrimSpace(template.Category) == "" {
			continue
		}
		if !seen[template.Category] {
			seen[template.Category] = true
			categories = append(categories, template.Category)
		}
	}
	return categories, nil
}

func (s *TicketService) toTicketTemplateDTO(ctx context.Context, template *ent.TicketTemplate) (*dto.TicketTemplate, error) {
	defs, err := NewFieldDefinitionService(s.client).ListDefinitions(ctx, template.TenantID, "ticket_template", template.ID)
	if err != nil {
		s.logger.Warnw("加载模板字段定义失败", "error", err, "template_id", template.ID)
		defs = nil
	}
	fields := make([]map[string]interface{}, 0, len(defs))
	for _, d := range defs {
		fields = append(fields, map[string]interface{}{
			"name":     d.Name,
			"label":    d.Label,
			"type":     d.FieldType,
			"required": d.Required,
			"options":  d.Options,
		})
	}

	var workflowSteps []map[string]interface{}
	if len(template.WorkflowSteps) > 0 {
		if err := json.Unmarshal(template.WorkflowSteps, &workflowSteps); err != nil {
			s.logger.Warnw("反序列化工作流步骤失败", "error", err, "template_id", template.ID)
			workflowSteps = nil
		}
	}

	isActive := template.IsActive
	return &dto.TicketTemplate{
		ID:            template.ID,
		Name:          template.Name,
		Description:   template.Description,
		Category:      template.Category,
		Priority:      template.Priority,
		Fields:        fields,
		CategoryIDs:   template.CategoryIds,
		WorkflowSteps: workflowSteps,
		IsActive:      isActive,
		CreatedAt:     template.CreatedAt,
		UpdatedAt:     template.UpdatedAt,
	}, nil
}

// ==================== CSV / Excel / JSON 独立实现（V2 不依赖 V1） ====================

// generateCSV 生成 CSV
func (s *TicketService) generateCSV(data []map[string]interface{}) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	var headers []string
	for key := range data[0] {
		headers = append(headers, key)
	}
	if err := writer.Write(headers); err != nil {
		return nil, err
	}
	for _, row := range data {
		record := make([]string, 0, len(headers))
		for _, header := range headers {
			value := row[header]
			if value == nil {
				record = append(record, "")
			} else {
				record = append(record, fmt.Sprintf("%v", value))
			}
		}
		if err := writer.Write(sanitizeSpreadsheetRow(record)); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// generateExcel 生成 Excel（实际上返回 CSV，保持与 V1 一致的行为）
func (s *TicketService) generateExcel(data []map[string]interface{}) ([]byte, error) {
	return s.generateCSV(data)
}

// ==================== MSP 相关方法 ====================

// GetCustomerTicketsForMSP 获取 MSP 视角下的客户工单
func (s *TicketService) GetCustomerTicketsForMSP(ctx context.Context, userID, customerTenantID int, status *string, page, pageSize int) ([]*ticket.Ticket, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for MSP query")
	}
	query := s.client.Ticket.Query().Where(entTicket.TenantIDEQ(customerTenantID))
	if status != nil && *status != "" {
		query = query.Where(entTicket.StatusEQ(*status))
	}
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		query = query.Offset(offset).Limit(pageSize)
	}
	query = query.Order(ent.Desc(entTicket.FieldCreatedAt))
	ents, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer tickets for MSP: %w", err)
	}
	result := make([]*ticket.Ticket, len(ents))
	for i, e := range ents {
		result[i] = s.entToDomain(e)
	}
	return result, nil
}

// AssignMSPTechnician 为工单分配 MSP 技术员
func (s *TicketService) AssignMSPTechnician(ctx context.Context, ticketID, customerTenantID, assignerID int) (*ticket.Ticket, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for MSP assign")
	}
	t, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("工单不存在")
		}
		return nil, err
	}
	if t.TenantID != customerTenantID {
		return nil, fmt.Errorf("工单不属于指定客户租户")
	}
	// 分配给 MSP 技术员（这里 assignerID 作为目标处理人；可后续扩展为查表分配）
	if _, err := s.repo.AssignTicket(ctx, ticketID, assignerID, customerTenantID); err != nil {
		return nil, fmt.Errorf("failed to assign MSP technician: %w", err)
	}
	updated, err := s.repo.GetByID(ctx, ticketID, customerTenantID)
	if err != nil {
		return nil, err
	}

	// 异步同步工单到飞书
	if s.connectorManager != nil {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 获取Feishu连接器
			conn, ok := s.connectorManager.Get(customerTenantID, "feishu")
			if !ok {
				// 飞书连接器未配置，忽略
				return
			}
			feishuConn, ok := conn.(*feishuConnector.Feishu)
			if !ok {
				return
			}
			// 开启事务
			tx, err := s.client.Tx(ctx2)
			if err != nil {
				s.logger.Warnw("Failed to start transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
			defer tx.Rollback()
			// 同步工单到飞书
			_, err = feishuConn.UpdateExistingTicketTask(ctx2, tx, s.toEntTicket(updated))
			if err != nil {
				s.logger.Warnw("Failed to sync ticket to feishu", "error", err, "ticket_id", updated.ID)
				return
			}
			// 提交事务
			if err := tx.Commit(); err != nil {
				s.logger.Warnw("Failed to commit transaction for feishu sync", "error", err, "ticket_id", updated.ID)
				return
			}
		}()
	}

	return updated, nil
}

// GetMSPCustomerReports 获取 MSP 客户报告
func (s *TicketService) GetMSPCustomerReports(ctx context.Context, mspTenantID int, dateFrom, dateTo time.Time) ([]map[string]interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for MSP reports")
	}
	query := s.client.Ticket.Query().Where(entTicket.TenantID(mspTenantID))
	if !dateFrom.IsZero() {
		query = query.Where(entTicket.CreatedAtGTE(dateFrom))
	}
	if !dateTo.IsZero() {
		query = query.Where(entTicket.CreatedAtLTE(dateTo))
	}
	tickets, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get MSP customer reports: %w", err)
	}
	reports := make([]map[string]interface{}, 0, len(tickets))
	statusCount := make(map[string]int)
	for _, t := range tickets {
		statusCount[t.Status]++
	}
	reports = append(reports, map[string]interface{}{
		"total_tickets":  len(tickets),
		"status_summary": statusCount,
		"date_from":      dateFrom,
		"date_to":        dateTo,
	})
	return reports, nil
}

// GetMSPPerformanceReports 获取 MSP 性能报告
func (s *TicketService) GetMSPPerformanceReports(ctx context.Context, mspTenantID int, dateFrom, dateTo time.Time) ([]map[string]interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not available for MSP performance")
	}
	query := s.client.Ticket.Query().Where(entTicket.TenantID(mspTenantID))
	if !dateFrom.IsZero() {
		query = query.Where(entTicket.CreatedAtGTE(dateFrom))
	}
	if !dateTo.IsZero() {
		query = query.Where(entTicket.CreatedAtLTE(dateTo))
	}
	tickets, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	resolvedCount := 0
	var totalResolutionTime time.Duration
	for _, t := range tickets {
		if t.Status == "resolved" {
			resolvedCount++
			if !t.UpdatedAt.IsZero() {
				totalResolutionTime += t.UpdatedAt.Sub(t.CreatedAt)
			}
		}
	}
	avgResolution := time.Duration(0)
	if resolvedCount > 0 {
		avgResolution = totalResolutionTime / time.Duration(resolvedCount)
	}
	return []map[string]interface{}{
		{
			"msp_tenant_id":       mspTenantID,
			"total_tickets":       len(tickets),
			"resolved_tickets":    resolvedCount,
			"avg_resolution_time": avgResolution.Hours(),
			"date_from":           dateFrom,
			"date_to":             dateTo,
		},
	}, nil
}

func (s *TicketService) SetProcessTriggerService(owner ProcessTriggerServiceInterface) {
	s.processTriggerSvc = owner
}
