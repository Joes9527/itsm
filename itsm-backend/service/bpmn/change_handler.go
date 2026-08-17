package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"

	"go.uber.org/zap"
)

// ChangeServiceTaskHandler 变更服务任务处理器
type ChangeServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewChangeServiceTaskHandler 创建变更处理器
func NewChangeServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ChangeServiceTaskHandler {
	return &ChangeServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *ChangeServiceTaskHandler) GetTaskType() string {
	return "change_task"
}

// GetHandlerID 返回处理器标识
func (h *ChangeServiceTaskHandler) GetHandlerID() string {
	return "change_service_handler"
}

// Execute 执行变更服务任务
func (h *ChangeServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_change":
		return h.createChange(ctx, variables)
	case "update_change":
		return h.updateChange(ctx, variables)
	case "approve_change":
		return h.approveChange(ctx, variables)
	case "reject_change":
		return h.rejectChange(ctx, variables)
	case "schedule_change":
		return h.scheduleChange(ctx, variables)
	case "implement_change":
		return h.implementChange(ctx, variables)
	case "verify_change":
		return h.verifyChange(ctx, variables)
	case "close_change":
		return h.closeChange(ctx, variables)
	case "assess_risk":
		return h.assessRisk(ctx, variables)
	case "notify_stakeholders":
		return h.notifyStakeholders(ctx, variables)
	default:
		return &dto.ServiceTaskResult{
			Success: true,
			Message: "无操作执行",
		}, nil
	}
}

// Validate 验证配置
func (h *ChangeServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// createChange 创建变更
func (h *ChangeServiceTaskHandler) createChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	title, _ := variables["title"].(string)
	description, _ := variables["description"].(string)
	changeType, _ := variables["type"].(string)
	priority, _ := variables["priority"].(string)
	tenantID := GetTenantIDFromVars(ctx, variables)

	if title == "" {
		return nil, fmt.Errorf("变更标题不能为空")
	}

	change, err := h.client.Change.Create().
		SetTitle(title).
		SetDescription(description).
		SetType(changeType).
		SetPriority(priority).
		SetStatus("draft").
		SetCreatedBy(GetIntFromVars(variables, "created_by")).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建变更失败: %w", err)
	}

	h.logger.Infow("Change created via BPMN", "change_id", change.ID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("变更 %d 已创建", change.ID),
		OutputVars: map[string]interface{}{"change_id": change.ID},
	}, nil
}

// updateChange 更新变更
func (h *ChangeServiceTaskHandler) updateChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	updateQuery := h.client.Change.Update().
		Where(change.ID(changeID), change.TenantID(tenantID))

	if title, ok := variables["title"].(string); ok && title != "" {
		updateQuery.SetTitle(title)
	}
	if description, ok := variables["description"].(string); ok && description != "" {
		updateQuery.SetDescription(description)
	}
	if status, ok := variables["status"].(string); ok && status != "" {
		updateQuery.SetStatus(status)
	}

	updated, err := updateQuery.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新变更失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
	}

	h.logger.Infow("Change updated via BPMN", "change_id", changeID)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已更新", changeID),
	}, nil
}

// approveChange 审批变更
func (h *ChangeServiceTaskHandler) approveChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取变更信息（带租户约束）
	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	// 更新状态为审批中
	if !isValidChangeStatusTransitionForBPMN(entity.Status, "pending_approval") {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "pending_approval")
	}
	if _, err := entity.Update().
		SetStatus("pending_approval").
		Save(ctx); err != nil {
		return nil, fmt.Errorf("更新变更状态失败: %w", err)
	}

	h.logger.Infow("Change submitted for approval via BPMN", "change_id", changeID, "title", entity.Title)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已提交审批", changeID),
	}, nil
}

// rejectChange 驳回变更
func (h *ChangeServiceTaskHandler) rejectChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	reason, _ := variables["reject_reason"].(string)

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}
	if !isValidChangeStatusTransitionForBPMN(entity.Status, "rejected") {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "rejected")
	}

	if _, err := entity.Update().SetStatus("rejected").Save(ctx); err != nil {
		return nil, fmt.Errorf("驳回变更失败: %w", err)
	}

	h.logger.Infow("Change rejected via BPMN", "change_id", changeID, "reason", reason)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已驳回: %s", changeID, reason),
	}, nil
}

// scheduleChange 排期变更
func (h *ChangeServiceTaskHandler) scheduleChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	// 解析日期时间
	var plannedStart, plannedEnd time.Time
	if startStr, ok := variables["planned_start_date"].(string); ok && startStr != "" {
		plannedStart, _ = time.Parse(time.RFC3339, startStr)
	}
	if endStr, ok := variables["planned_end_date"].(string); ok && endStr != "" {
		plannedEnd, _ = time.Parse(time.RFC3339, endStr)
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}
	if !isValidChangeStatusTransitionForBPMN(entity.Status, "scheduled") {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "scheduled")
	}

	updateQuery := entity.Update().SetStatus("scheduled")
	if !plannedStart.IsZero() {
		updateQuery.SetPlannedStartDate(plannedStart)
	}
	if !plannedEnd.IsZero() {
		updateQuery.SetPlannedEndDate(plannedEnd)
	}

	if _, err := updateQuery.Save(ctx); err != nil {
		return nil, fmt.Errorf("排期变更失败: %w", err)
	}

	h.logger.Infow("Change scheduled via BPMN", "change_id", changeID)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已排期", changeID),
	}, nil
}

// implementChange 实施变更
func (h *ChangeServiceTaskHandler) implementChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取变更信息（带租户约束）
	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	if !isValidChangeStatusTransitionForBPMN(entity.Status, "in_progress") {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "in_progress")
	}

	now := time.Now()
	if _, err := entity.Update().
		SetStatus("in_progress").
		SetActualStartDate(now).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("实施变更失败: %w", err)
	}

	h.logger.Infow("Change implementation started via BPMN", "change_id", changeID, "title", entity.Title)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 开始实施", changeID),
	}, nil
}

// verifyChange 验证变更
func (h *ChangeServiceTaskHandler) verifyChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	verificationResult, _ := variables["verification_result"].(string)

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	// 根据验证结果更新状态。目标状态对齐域状态机（service.IsValidChangeStatusTransition）
	// 的 canonical 值：passed → completed，failed → failed（旧的 verification_passed/
	// verification_failed 不是域状态机成员，域侧桥接会被覆盖成非法状态）。
	newStatus := "completed"
	if verificationResult == "failed" {
		newStatus = "failed"
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}
	if !isValidChangeStatusTransitionForBPMN(entity.Status, newStatus) {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, newStatus)
	}

	if _, err := entity.Update().SetStatus(newStatus).Save(ctx); err != nil {
		return nil, fmt.Errorf("验证变更失败: %w", err)
	}

	h.logger.Infow("Change verification via BPMN", "change_id", changeID, "result", verificationResult)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 验证结果: %s", changeID, verificationResult),
	}, nil
}

// closeChange 关闭变更
func (h *ChangeServiceTaskHandler) closeChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	feedback, _ := variables["feedback"].(string)

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}
	// 关闭目标状态对齐域状态机 canonical 值 completed（旧的 "closed" 不是域状态机成员）
	if !isValidChangeStatusTransitionForBPMN(entity.Status, "completed") {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "completed")
	}

	now := time.Now()
	if _, err := entity.Update().
		SetStatus("completed").
		SetActualEndDate(now).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("关闭变更失败: %w", err)
	}

	h.logger.Infow("Change closed via BPMN", "change_id", changeID, "feedback", feedback)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已关闭", changeID),
	}, nil
}

// assessRisk 评估变更风险
func (h *ChangeServiceTaskHandler) assessRisk(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取变更信息进行风险评估（带租户约束）
	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	// 简化的风险评估逻辑
	riskLevel := "medium"
	impactScope := entity.ImpactScope

	// 根据变更类型和优先级确定风险等级
	if entity.Type == "emergency" {
		riskLevel = "high"
	} else if entity.Type == "minor" {
		riskLevel = "low"
	}

	if _, err := entity.Update().
		SetRiskLevel(riskLevel).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("评估风险失败: %w", err)
	}

	h.logger.Infow("Change risk assessed via BPMN", "change_id", changeID, "risk_level", riskLevel, "impact_scope", impactScope)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 风险评估完成: %s", changeID, riskLevel),
		OutputVars: map[string]interface{}{
			"risk_level":   riskLevel,
			"impact_scope": impactScope,
		},
	}, nil
}

// notifyStakeholders 通知利益相关者
func (h *ChangeServiceTaskHandler) notifyStakeholders(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	notificationType, _ := variables["notification_type"].(string)

	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取变更信息（带租户约束）
	entity, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	// 记录通知日志（实际应调用通知服务）
	h.logger.Infow(
		"Stakeholders notification via BPMN",
		"change_id", changeID,
		"change_title", entity.Title,
		"notification_type", notificationType,
	)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 利益相关者已通知", changeID),
	}, nil
}

// isValidChangeStatusTransitionForBPMN 变更状态转换白名单。service/bpmn 不能 import
// service 包（循环依赖），这里复制自 service.IsValidChangeStatusTransition 的 canonical
// 口径、仅覆盖 handler 动作会触发的转换。pending_approval 是 handler 侧的中间态
// （域状态机里 submitted 的等价物）。改动域状态机规则时两处要一起改。
func isValidChangeStatusTransitionForBPMN(current, newStatus string) bool {
	if current == newStatus {
		// 幂等：同值转换放行（域侧桥接写完后 handler 再写同值不会报错）
		return true
	}
	transitions := map[string]map[string]struct{}{
		"draft":            {"pending_approval": {}, "submitted": {}, "cancelled": {}},
		"submitted":        {"pending_approval": {}, "approved": {}, "rejected": {}, "cancelled": {}},
		"pending_approval": {"approved": {}, "rejected": {}, "scheduled": {}, "in_progress": {}, "cancelled": {}},
		"approved":         {"scheduled": {}, "in_progress": {}, "cancelled": {}},
		"scheduled":        {"in_progress": {}, "cancelled": {}},
		"in_progress":      {"completed": {}, "failed": {}, "cancelled": {}},
		"failed":           {"scheduled": {}, "cancelled": {}},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[newStatus]
	return ok
}

// 确保 ChangeServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ChangeServiceTaskHandler)(nil)
