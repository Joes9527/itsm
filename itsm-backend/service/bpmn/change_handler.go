package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"

	"go.uber.org/zap"
)

// ChangeDomainServiceInterface 变更领域服务接口（避免 service/bpmn 反向 import
// handlers/change 包造成循环依赖——handlers/change/service.go 已经 import service/bpmn
// 用它的 BPMNTenantIDContextKey/BPMNUserIDContextKey，反向依赖会成环，同
// IncidentDomainServiceInterface 的理由）。
//
// CreateChangeForWorkflow 是 Wave 2（统一 WorkItem 领域模型迁移）新增：把这个文件原来
// createChange 直接 h.client.Change.Create() 建变更的代码收回到领域服务，让 BPMN 自动
// 创建的 Change 走 handlers/change.Service.CreateChange -> EntRepository.Create 的
// 事务化建表逻辑，同步建好 WorkItem（tickets 行，record_class="change_request"）。不这样
// 做的话，通过 BPMN 流程自动创建的 Change（不是用户在 UI 手动创建的）永远不会有
// WorkItem，违反统一 WorkItem 领域模型宪章 §3 的"任意专业记录必须对应且仅对应一条
// WorkItem"不变式。
type ChangeDomainServiceInterface interface {
	CreateChangeForWorkflow(ctx context.Context, tenantID, createdBy int, title, description, changeType, priority string) (int, error)
}

// ChangeServiceTaskHandler 变更服务任务处理器
type ChangeServiceTaskHandler struct {
	HandlerBase
	client        *ent.Client
	logger        *zap.SugaredLogger
	changeService ChangeDomainServiceInterface
}

// NewChangeServiceTaskHandler 创建变更处理器
func NewChangeServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ChangeServiceTaskHandler {
	return &ChangeServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// SetChangeService 注入变更领域服务，由 bootstrap 在 handlers/change.Service 构造完成后
// 调用（同 IncidentServiceTaskHandler.SetIncidentService 的 setter 注入模式，避免构造函数
// 循环依赖初始化顺序问题）。
func (h *ChangeServiceTaskHandler) SetChangeService(svc ChangeDomainServiceInterface) {
	h.changeService = svc
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
		return nil, fmt.Errorf("不支持的变更回调动作")
	}
}

// Validate 验证配置
func (h *ChangeServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// createChange 创建变更。委托给 handlers/change.Service.CreateChange（通过
// ChangeDomainServiceInterface 窄接口），不再直接 h.client.Change.Create()——那样会绕开
// WorkItem 的事务化创建，见 ChangeDomainServiceInterface 的注释。
func (h *ChangeServiceTaskHandler) createChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	if _, durable := BPMNCallbackExecutionKey(ctx); durable {
		return nil, fmt.Errorf("持久化回调必须使用流程实例中的既有变更目标")
	}
	title, _ := variables["title"].(string)
	description, _ := variables["description"].(string)
	changeType, _ := variables["type"].(string)
	priority, _ := variables["priority"].(string)
	tenantID := GetTenantIDFromVars(ctx, variables)

	if title == "" {
		return nil, fmt.Errorf("变更标题不能为空")
	}
	if h.changeService == nil {
		return nil, fmt.Errorf("change service 未注入，无法创建变更")
	}

	changeID, err := h.changeService.CreateChangeForWorkflow(ctx, tenantID, GetIntFromVars(variables, "created_by"), title, description, changeType, priority)
	if err != nil {
		return nil, fmt.Errorf("创建变更失败: %w", err)
	}

	h.logger.Infow("Change created via BPMN", "change_id", changeID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("变更 %d 已创建", changeID),
		OutputVars: map[string]interface{}{"change_id": changeID},
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
// approve_change 这个 action 在 CAB 审批节点（Activity_CABApproval）本身触发，
// 不管审批结果是 approve 还是 reject 都会走到这里（节点自己的 action 是固定的，
// 不代表审批结果）——真正的终态判定在 schedule_change/reject_change。
// 这里不改 Change.Status，只做一次存在性确认，避免 change_id 无效时静默成功。
func (h *ChangeServiceTaskHandler) approveChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取变更信息（带租户约束）。approveChange 本身不写状态——CAB 节点的 action 是
	// 固定的 approve_change，不代表审批结果是通过还是驳回；写"pending_approval"这个
	// 中间态会跟域侧 canonical 状态机（service.IsValidChangeStatusTransition，没有
	// pending_approval 这个成员）产生第二套状态机分叉。真正的终态判定和落库在
	// scheduleChange（approved/scheduled）和 rejectChange（rejected）。
	if _, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx); err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}
	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 审批节点已处理", changeID),
	}, nil
}

// rejectChange 驳回变更
func (h *ChangeServiceTaskHandler) rejectChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	if err := h.transitionChangeStatus(ctx, tenantID, changeID, "rejected"); err != nil {
		return nil, err
	}
	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("变更 %d 已驳回", changeID),
	}, nil
}

// scheduleChange 排期变更
// 状态机推进分两跳：
//  1. pending/submitted -> approved：CAB 批准后所有变更类型都要经过的中间态。
//  2. approved -> scheduled：只对状态机里声明了这条转换的类型（normal/standard）生效。
//     emergency 类型的状态机没有 scheduled 这个中间态——approved 直接对接 in_progress
//     （快速通道），如果这里对 emergency 也强行写 scheduled，第二跳会被状态机拒绝，
//     把本该成功的 CAB 审批级联搞失败。所以先查一次当前变更的 type，只在
//     isValidChangeStatusTransition("approved", "scheduled", type) 为真时才做第二跳；
//     否则停在 approved，交给后续 Activity_Implement 直接把 approved 推进到
//     in_progress（这也是 emergency 状态机唯一合法的下一跳）。
func (h *ChangeServiceTaskHandler) scheduleChange(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	changeID := GetIntFromVars(variables, "change_id")
	if changeID <= 0 {
		return nil, fmt.Errorf("无效的变更ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	current, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	// A completed first delivery leaves normal/standard changes in scheduled. A retry
	// must never walk that desired state backwards through approved.
	if current.Status != "approved" && current.Status != "scheduled" {
		if err := h.transitionChangeStatus(ctx, tenantID, changeID, "approved"); err != nil {
			return nil, err
		}
	}

	c, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return nil, fmt.Errorf("查询变更失败: %w", err)
	}

	// 第二跳：仅当该变更类型的状态机允许 approved -> scheduled 时才推进
	if c.Status == "approved" && isValidChangeStatusTransition("approved", "scheduled", c.Type) {
		if err := h.transitionChangeStatus(ctx, tenantID, changeID, "scheduled"); err != nil {
			return nil, err
		}
	}

	// 解析日期时间
	var plannedStart, plannedEnd time.Time
	if startStr, ok := variables["planned_start_date"].(string); ok && startStr != "" {
		plannedStart, _ = time.Parse(time.RFC3339, startStr)
	}
	if endStr, ok := variables["planned_end_date"].(string); ok && endStr != "" {
		plannedEnd, _ = time.Parse(time.RFC3339, endStr)
	}

	updateQuery := h.client.Change.UpdateOneID(changeID).Where(change.TenantID(tenantID))
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

// transitionChangeStatus 统一做租户约束 + 状态机校验后写入，任何调用点都不能绕过
// isValidChangeStatusTransition —— BPMN 回调跟 handlers/change 自己的
// TransitionStatus 必须遵守同一套状态机规则，不能各自为政。
func (h *ChangeServiceTaskHandler) transitionChangeStatus(ctx context.Context, tenantID, changeID int, targetStatus string) error {
	c, err := h.client.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return fmt.Errorf("查询变更失败: %w", err)
	}
	if c.Status == targetStatus {
		return nil
	}
	if !isValidChangeStatusTransition(c.Status, targetStatus, c.Type) {
		return fmt.Errorf("无效的状态转换: 从 %q 到 %q", c.Status, targetStatus)
	}
	if _, err := h.client.Change.UpdateOneID(changeID).
		Where(change.TenantID(tenantID)).
		SetStatus(targetStatus).
		Save(ctx); err != nil {
		return fmt.Errorf("更新变更状态失败: %w", err)
	}
	return nil
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

	if entity.Status == "in_progress" {
		return &dto.ServiceTaskResult{
			Success: true,
			Message: fmt.Sprintf("变更 %d 已在实施中", changeID),
		}, nil
	}
	if !isValidChangeStatusTransition(entity.Status, "in_progress", entity.Type) {
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
	if entity.Status != newStatus && !isValidChangeStatusTransition(entity.Status, newStatus, entity.Type) {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, newStatus)
	}

	if entity.Status != newStatus {
		if _, err := entity.Update().SetStatus(newStatus).Save(ctx); err != nil {
			return nil, fmt.Errorf("验证变更失败: %w", err)
		}
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
	if entity.Status != "completed" && !isValidChangeStatusTransition(entity.Status, "completed", entity.Type) {
		return nil, fmt.Errorf("非法的变更状态转换: %s -> %s", entity.Status, "completed")
	}

	if entity.Status != "completed" || entity.ActualEndDate.IsZero() {
		update := entity.Update()
		if entity.Status != "completed" {
			update.SetStatus("completed")
		}
		if entity.ActualEndDate.IsZero() {
			update.SetActualEndDate(time.Now())
		}
		if _, err := update.Save(ctx); err != nil {
			return nil, fmt.Errorf("关闭变更失败: %w", err)
		}
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

// isValidChangeStatusTransition 检查变更状态转换是否有效。
// 注意：这个函数与 service/change_service.go 中的 IsValidChangeStatusTransition 保持规则一致，
// 两边修改要同步。之所以复制到这里是为了避免 service/bpmn 包和 service 包之间的循环导入。
func isValidChangeStatusTransition(currentStatus, newStatus, changeType string) bool {
	// 历史兼容：handlers/change 模块使用 "pending"，而 common 常量用 "submitted"，
	// 两者是等价的状态（变更已提交等待CAB审批）。在此处做归一化，避免状态机误判。
	if currentStatus == "pending" {
		currentStatus = common.ChangeStatusSubmitted
	}

	// 基础转换规则（适用于所有变更类型）
	baseTransitions := map[string][]string{
		common.ChangeStatusRejected:        {}, // 被拒绝后不允许转换
		common.ChangeStatusCompleted:       {}, // 已完成不允许转换
		common.ChangeStatusCancelled:       {}, // 已取消不允许转换
		string(dto.ChangeStatusRolledBack): {},
	}

	// 不同变更类型的特殊转换规则
	var typeSpecificTransitions map[string][]string
	switch changeType {
	case string(dto.ChangeTypeStandard):
		// 标准变更：预授权，可以跳过审批步骤
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusApproved, common.ChangeStatusScheduled, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusScheduled, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusScheduled:  {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	case string(dto.ChangeTypeEmergency):
		// 紧急变更：可以跳过多个步骤，快速实施
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusApproved, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	default: // 普通变更：严格的ITIL流程
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusScheduled, common.ChangeStatusCancelled},
			common.ChangeStatusScheduled:  {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	}

	// 合并基础规则和类型特定规则
	validTransitions := make(map[string][]string)
	for k, v := range baseTransitions {
		validTransitions[k] = v
	}
	for k, v := range typeSpecificTransitions {
		validTransitions[k] = v
	}

	allowed, ok := validTransitions[currentStatus]
	if !ok {
		// 未知状态必须失败关闭，避免绕过变更生命周期约束。
		return false
	}

	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}
	return false
}

// 确保 ChangeServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ChangeServiceTaskHandler)(nil)
