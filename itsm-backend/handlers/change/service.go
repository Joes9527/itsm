package change

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/cirelationship"
	"itsm-backend/ent/configurationitem"
	"itsm-backend/ent/incident"
	"itsm-backend/service"

	"go.uber.org/zap"
)

type Service struct {
	repo              Repository
	logger            *zap.SugaredLogger
	entClient         *ent.Client
	pirService        *service.ChangePIRService
	approvalBridge    *service.BPMNApprovalBridge
	processTriggerSvc service.ProcessTriggerServiceInterface
}

func NewService(repo Repository, entClient *ent.Client, logger *zap.SugaredLogger) *Service {
	svc := &Service{
		repo:      repo,
		entClient: entClient,
		logger:    logger,
	}
	// Initialize PIR service
	svc.pirService = service.NewChangePIRService(entClient, logger)
	if entClient != nil {
		// P0-1：变更审批桥接到 BPMN 任务，避免流程实例悬挂
		svc.approvalBridge = service.NewBPMNApprovalBridge(entClient, logger)
	}
	return svc
}

// SetProcessTriggerService 注入流程触发服务（提交变更审批后自动启动 change_normal_flow）。
// 与 ReleaseService.SetProcessTriggerService 同一模式：由 bootstrap 装配。变更域此前只
// 桥接了"域动作 -> 完成已存在的 BPMN 任务"方向（approvalBridge），却从未触发流程实例本身，
// 导致普通变更提交审批后压根没有 change_normal_flow 实例可桥接。
func (s *Service) SetProcessTriggerService(p service.ProcessTriggerServiceInterface) {
	s.processTriggerSvc = p
}

// Change methods
func (s *Service) CreateChange(ctx context.Context, c *Change) (*Change, error) {
	s.logger.Infow("Creating change", "title", c.Title, "tenant_id", c.TenantID)
	return s.repo.Create(ctx, c)
}

func (s *Service) GetChange(ctx context.Context, id int, tenantID int) (*Change, error) {
	return s.repo.Get(ctx, id, tenantID)
}

func (s *Service) ListChanges(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error) {
	return s.repo.List(ctx, tenantID, page, size, status, search, riskLevel)
}

func (s *Service) UpdateChange(ctx context.Context, c *Change) (*Change, error) {
	return s.repo.Update(ctx, c)
}

func (s *Service) DeleteChange(ctx context.Context, id int, tenantID int) error {
	return s.repo.Delete(ctx, id, tenantID)
}

func (s *Service) GetStats(ctx context.Context, tenantID int) (*Stats, error) {
	return s.repo.GetStats(ctx, tenantID)
}

// GetCalendarView 获取日历视图数据
func (s *Service) GetCalendarView(ctx context.Context, tenantID int, startDate, endDate, status string) (*dto.ChangeCalendarResponse, error) {
	changes, err := s.repo.ListByDateRange(ctx, tenantID, startDate, endDate, status)
	if err != nil {
		return nil, err
	}

	items := make([]dto.ChangeCalendarItem, 0, len(changes))
	for _, c := range changes {
		var plannedStart, plannedEnd time.Time
		if c.PlannedStartDate != nil {
			plannedStart = *c.PlannedStartDate
		}
		if c.PlannedEndDate != nil {
			plannedEnd = *c.PlannedEndDate
		}

		items = append(items, dto.ChangeCalendarItem{
			ID:           c.ID,
			Title:        c.Title,
			ChangeNumber: fmt.Sprintf("C-%d", c.ID),
			Status:       c.Status,
			RiskLevel:    c.RiskLevel,
			Category:     c.Type,
			PlannedStart: plannedStart,
			PlannedEnd:   plannedEnd,
		})
	}

	return &dto.ChangeCalendarResponse{
		Items: items,
		Total: len(items),
	}, nil
}

// SubmitChange submits a change for approval
// Transitions status from 'draft' to 'pending' and creates approval records for specified approvers
func (s *Service) SubmitChange(ctx context.Context, changeID, tenantID, submitterID int, req *dto.SubmitChangeRequest) (*Change, error) {
	// 1. Get the change
	c, err := s.repo.Get(ctx, changeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("change not found")
	}

	// 2. Check if change is in draft status
	if c.Status != "draft" {
		return nil, fmt.Errorf("change must be in draft status to submit")
	}

	// 3.5. If no approvers specified, default to the change creator
	if len(req.ApproverIDs) == 0 {
		req.ApproverIDs = []int{c.CreatedBy}
		s.logger.Infow("No approvers specified, defaulting to change creator", "change_id", changeID, "creator_id", c.CreatedBy)
	}

	// 4. Validate approvers belong to the same tenant before creating approval records
	for _, approverID := range req.ApproverIDs {
		valid, err := s.repo.ValidateApproverBelongsToTenant(ctx, approverID, tenantID)
		if err != nil {
			s.logger.Warnw("Failed to validate approver", "error", err, "approver_id", approverID)
			return nil, fmt.Errorf("验证审批人失败")
		}
		if !valid {
			s.logger.Warnw("Approver does not belong to tenant", "approver_id", approverID, "tenant_id", tenantID)
			return nil, fmt.Errorf("审批人 %d 不属于当前租户", approverID)
		}
	}

	if err := s.repo.SubmitForApproval(ctx, changeID, tenantID, req.ApproverIDs, req.Comment); err != nil {
		s.logger.Warnw("Failed to atomically submit change", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("提交变更审批失败: %w", err)
	}

	// 6. Notify approvers (optional - to be implemented later or via async)
	// For now, just log the submission
	s.logger.Infow("Change submitted for approval", "change_id", changeID, "submitter_id", submitterID, "approvers", req.ApproverIDs)

	// 触发变更流程（按 ProcessBinding 默认绑定解析 change_normal_flow）。
	// fail-soft 与发布/工单域一致：触发失败只告警不阻断提交——变更生命周期本身
	// 不依赖流程实例，approvalBridge 对"无关联流程实例"回退纯业务路径。
	//
	// approval_required 按变更类型预置为初始流程变量：change_normal_flow 的
	// Gateway_Approval 网关靠这个变量决定是否路由到 CAB 审批——normal 类型的变更
	// 走完整 CAB 流程，standard（预授权标准变更）跳过审批直接排期，这是 ITIL
	// 里标准变更"预先批准"的定义，不是遗漏审批环节。
	if s.processTriggerSvc != nil {
		triggerVars := map[string]interface{}{
			"approval_required": c.Type == "normal",
		}
		if _, triggerErr := s.processTriggerSvc.TriggerByBusinessType(
			ctx, dto.BusinessTypeChange, changeID, triggerVars, strconv.Itoa(submitterID), tenantID,
		); triggerErr != nil {
			s.logger.Warnw("Failed to trigger change workflow",
				"change_id", changeID, "tenant_id", tenantID, "error", triggerErr)
		}
	}

	c.Status = "pending"
	return c, nil
}

// Approval methods
func (s *Service) SubmitApproval(ctx context.Context, record *ApprovalRecord, tenantID int) (*ApprovalRecord, error) {
	// Custom business logic: when submitting, we check if change exists
	c, err := s.repo.Get(ctx, record.ChangeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("change not found")
	}

	record.Status = "pending"
	record.TenantID = tenantID
	res, err := s.repo.CreateApprovalRecord(ctx, record)
	if err != nil {
		return nil, err
	}

	// Update change status to pending if needed
	if c.Status == "draft" {
		c.Status = "pending"
		if _, err := s.repo.Update(ctx, c); err != nil {
			s.logger.Errorw("SubmitApproval: failed to update change status to pending", "error", err, "change_id", c.ID)
			return nil, fmt.Errorf("failed to update change status: %w", err)
		}
	}

	return res, nil
}

func (s *Service) ProcessApproval(ctx context.Context, recordID int, status string, comment *string, tenantID int) (*ApprovalRecord, error) {
	// 1. Get existing record (we need to know what change it refers to)
	// We might need a repo.GetApprovalRecord method, let's add it or use a workaround if it's missing in repo interface
	// For now, I'll assume I can update by ID directly if the repository implementation allows it

	rec := &ApprovalRecord{
		ID:       recordID,
		TenantID: tenantID,
		Status:   status,
		Comment:  comment,
	}

	res, err := s.repo.UpdateApprovalRecord(ctx, rec)
	if err != nil {
		return nil, err
	}

	// 2. Logic to check if all approvals are done
	if status == "approved" {
		if err := s.checkAndTransitionChange(ctx, res.ChangeID, tenantID); err != nil {
			s.logger.Errorw("ProcessApproval: checkAndTransitionChange failed", "error", err, "change_id", res.ChangeID)
		}
	} else if status == "rejected" {
		// If one rejects, the whole change is rejected?
		c, err := s.repo.Get(ctx, res.ChangeID, tenantID)
		if err != nil {
			s.logger.Errorw("ProcessApproval: failed to get change on rejection", "error", err, "change_id", res.ChangeID)
			return nil, fmt.Errorf("failed to get change: %w", err)
		}
		if c != nil {
			// C-2 修复：通过状态机校验禁止非法转换（例如 cancelled → rejected 终态互跳）
			target := "rejected"
			if !service.IsValidChangeStatusTransition(c.Status, target, c.Type) {
				s.logger.Warnw("ProcessApproval: skip invalid change status transition",
					"change_id", res.ChangeID, "from", c.Status, "to", target)
				return res, nil
			}
			// H-2 修复：事务化更新 change 为终态；提交成功后单独收口 pending chains（CloseChangeApprovalChains 内部使用 rawDB，*ent.Tx 不提供 ExecContext）
			tx, txErr := s.entClient.Tx(ctx)
			if txErr != nil {
				return nil, fmt.Errorf("开启事务失败: %w", txErr)
			}
			defer tx.Rollback()

			if _, updateErr := tx.Change.UpdateOneID(c.ID).
				Where(change.TenantID(tenantID)).
				SetStatus(target).
				Save(ctx); updateErr != nil {
				s.logger.Errorw("ProcessApproval: failed to update change status to rejected", "error", updateErr, "change_id", res.ChangeID)
				return nil, fmt.Errorf("failed to update change status: %w", updateErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("提交事务失败: %w", commitErr)
			}
			if closeErr := service.CloseChangeApprovalChains(ctx, res.ChangeID, tenantID); closeErr != nil {
				s.logger.Errorw("ProcessApproval: 收口审批链失败（非致命，后续状态机兜底）",
					"error", closeErr, "change_id", res.ChangeID, "tenant_id", tenantID)
			}
		}
	}

	return res, nil
}

func (s *Service) checkAndTransitionChange(ctx context.Context, changeID, tenantID int) error {
	chain, err := s.repo.GetApprovalChain(ctx, changeID, tenantID)
	if err != nil {
		s.logger.Errorw("checkAndTransitionChange: failed to get approval chain", "error", err, "change_id", changeID)
		return err
	}
	history, err := s.repo.GetApprovalHistory(ctx, changeID, tenantID)
	if err != nil {
		s.logger.Errorw("checkAndTransitionChange: failed to get approval history", "error", err, "change_id", changeID)
		return err
	}

	// Simple logic: if all required members approved, transition to 'approved'
	allApproved := true
	requiredCount := 0
	approvedMap := make(map[int]bool)
	for _, h := range history {
		if h.Status == "approved" {
			approvedMap[h.ApproverID] = true
		}
	}

	for _, item := range chain {
		if item.IsRequired {
			requiredCount++
			if !approvedMap[item.ApproverID] {
				allApproved = false
				break
			}
		}
	}

	if allApproved && requiredCount > 0 {
		c, err := s.repo.Get(ctx, changeID, tenantID)
		if err != nil {
			s.logger.Errorw("checkAndTransitionChange: failed to get change", "error", err, "change_id", changeID)
			return err
		}
		if c != nil {
			// C-2 修复：通过状态机校验禁止非法转换（如 cancelled → approved 终态复活）
			target := "approved"
			if !service.IsValidChangeStatusTransition(c.Status, target, c.Type) {
				s.logger.Warnw("checkAndTransitionChange: skip invalid change status transition",
					"change_id", changeID, "from", c.Status, "to", target)
				return nil
			}
			c.Status = target
			if _, err := s.repo.Update(ctx, c); err != nil {
				s.logger.Errorw("checkAndTransitionChange: failed to update change status to approved", "error", err, "change_id", changeID)
				return err
			}
		}
	}
	return nil
}

func (s *Service) ConfigureWorkflow(ctx context.Context, changeID, tenantID int, items []*ApprovalChain) error {
	// Clear existing and set new
	if _, err := s.repo.Get(ctx, changeID, tenantID); err != nil {
		return fmt.Errorf("change not found")
	}
	for _, item := range items {
		item.ChangeID = changeID
		item.TenantID = tenantID
	}
	if err := s.repo.ReplaceApprovalChain(ctx, changeID, tenantID, items); err != nil {
		s.logger.Errorw("ConfigureWorkflow: failed to replace approval chain", "error", err, "change_id", changeID)
		return fmt.Errorf("failed to replace approval chain: %w", err)
	}
	return nil
}

func (s *Service) GetApprovalSummary(ctx context.Context, changeID, tenantID int) (interface{}, error) {
	chain, err := s.repo.GetApprovalChain(ctx, changeID, tenantID)
	if err != nil {
		s.logger.Warnw("GetApprovalSummary: failed to get approval chain", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("failed to get approval chain: %w", err)
	}
	history, err := s.repo.GetApprovalHistory(ctx, changeID, tenantID)
	if err != nil {
		s.logger.Warnw("GetApprovalSummary: failed to get approval history", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("failed to get approval history: %w", err)
	}

	return map[string]interface{}{
		"chain":   chain,
		"history": history,
	}, nil
}

// Risk Assessment
func (s *Service) AssessRisk(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error) {
	return s.repo.CreateRiskAssessment(ctx, ra)
}

func (s *Service) GetRisk(ctx context.Context, changeID int, tenantID int) (*RiskAssessment, error) {
	return s.repo.GetRiskAssessment(ctx, changeID, tenantID)
}

func (s *Service) UpdateRisk(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error) {
	changeEntity, err := s.repo.Get(ctx, ra.ChangeID, ra.TenantID)
	if err != nil || changeEntity == nil {
		return nil, fmt.Errorf("change not found")
	}
	existing, err := s.repo.GetRiskAssessment(ctx, ra.ChangeID, ra.TenantID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return s.repo.CreateRiskAssessment(ctx, ra)
	}
	return s.repo.UpdateRiskAssessment(ctx, ra)
}

func (s *Service) GetCMDBImpactSummary(ctx context.Context, changeID, tenantID int) (*dto.ChangeCMDBImpactSummary, error) {
	if s.entClient == nil {
		return nil, fmt.Errorf("CMDB impact summary unavailable")
	}

	changeEntity, err := s.repo.Get(ctx, changeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("change not found")
	}

	ciIDs := make([]int, 0, len(changeEntity.AffectedCIs))
	for _, raw := range changeEntity.AffectedCIs {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			continue
		}
		ciIDs = append(ciIDs, id)
	}

	summary := &dto.ChangeCMDBImpactSummary{
		ChangeID:               changeID,
		AffectedCIs:            ciIDs,
		WorkflowHints:          []string{},
		ITILPractices:          []string{"service_configuration_management", "change_enablement"},
		RecommendedRiskLevel:   "low",
		RecommendedImpactScope: "low",
	}

	if len(ciIDs) == 0 {
		summary.WorkflowHints = append(summary.WorkflowHints, "当前变更未绑定 CI，建议在提交流程前关联受影响配置项。")
		return summary, nil
	}

	cis, err := s.entClient.ConfigurationItem.Query().
		Where(
			configurationitem.TenantID(tenantID),
			configurationitem.IDIn(ciIDs...),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询受影响CI失败: %w", err)
	}

	summary.TotalAffectedCIs = len(cis)
	for _, ci := range cis {
		if ci.Criticality == "high" || ci.Criticality == "critical" {
			summary.CriticalCICount++
		}
	}

	relCount, err := s.entClient.CIRelationship.Query().
		Where(
			cirelationship.TenantID(tenantID),
			cirelationship.IsActive(true),
			cirelationship.Or(
				cirelationship.SourceCiIDIn(ciIDs...),
				cirelationship.TargetCiIDIn(ciIDs...),
			),
			cirelationship.Or(
				cirelationship.StrengthEQ(cirelationship.StrengthHigh),
				cirelationship.StrengthEQ(cirelationship.StrengthCritical),
				cirelationship.ImpactLevelEQ(cirelationship.ImpactLevelHigh),
				cirelationship.ImpactLevelEQ(cirelationship.ImpactLevelCritical),
			),
		).
		Count(ctx)
	if err == nil {
		summary.HighRiskDependencyCount = relCount
	}

	openIncidentCount, err := s.entClient.Incident.Query().
		Where(
			incident.TenantID(tenantID),
			incident.ConfigurationItemIDIn(ciIDs...),
			incident.StatusNotIn("resolved", "closed"),
		).
		Count(ctx)
	if err == nil {
		summary.OpenIncidentCount = openIncidentCount
	}

	summary.RecommendedRiskLevel = recommendRiskLevel(
		summary.TotalAffectedCIs,
		summary.CriticalCICount,
		summary.HighRiskDependencyCount,
		summary.OpenIncidentCount,
		changeEntity.Type,
	)
	summary.RecommendedImpactScope = recommendImpactScope(
		summary.TotalAffectedCIs,
		summary.CriticalCICount,
		summary.HighRiskDependencyCount,
	)
	summary.RequiresCAB = summary.RecommendedRiskLevel == "high" || changeEntity.Type == "emergency" || summary.CriticalCICount > 0
	summary.RequiresBackoutPlan = summary.TotalAffectedCIs > 0
	summary.WorkflowHints = buildWorkflowHints(summary, changeEntity.Type)
	summary.ITILPractices = append(summary.ITILPractices, inferITILPractices(summary)...)

	return summary, nil
}

func recommendRiskLevel(totalCIs, criticalCIs, highRiskDependencies, openIncidents int, changeType string) string {
	switch {
	case changeType == "emergency":
		return "high"
	case criticalCIs > 0:
		return "high"
	case highRiskDependencies >= 4:
		return "high"
	case openIncidents >= 2:
		return "high"
	case totalCIs >= 5 || highRiskDependencies > 0 || openIncidents > 0:
		return "medium"
	default:
		return "low"
	}
}

func recommendImpactScope(totalCIs, criticalCIs, highRiskDependencies int) string {
	switch {
	case criticalCIs > 0 || totalCIs >= 5 || highRiskDependencies >= 3:
		return "high"
	case totalCIs >= 2 || highRiskDependencies > 0:
		return "medium"
	default:
		return "low"
	}
}

func buildWorkflowHints(summary *dto.ChangeCMDBImpactSummary, changeType string) []string {
	hints := make([]string, 0, 6)
	if summary.TotalAffectedCIs == 0 {
		hints = append(hints, "补充受影响 CI 后再发起审批，以便自动执行风险分流。")
	}
	if summary.CriticalCICount > 0 {
		hints = append(hints, "命中关键 CI，建议走 CAB 审批并校验变更窗口。")
	}
	if summary.OpenIncidentCount > 0 {
		hints = append(hints, "受影响 CI 当前存在未关闭事件，建议先做冲突检查和实施前健康确认。")
	}
	if summary.HighRiskDependencyCount > 0 {
		hints = append(hints, "存在高风险依赖，建议在工作流中增加影响确认和回滚演练节点。")
	}
	if changeType == "emergency" {
		hints = append(hints, "紧急变更建议启用快速审批路径，并在实施后自动创建 PIR 任务。")
	}
	if summary.RequiresBackoutPlan {
		hints = append(hints, "建议在提交流程前强制校验回滚计划与实施计划完整性。")
	}
	return hints
}

func inferITILPractices(summary *dto.ChangeCMDBImpactSummary) []string {
	practices := []string{}
	if summary.OpenIncidentCount > 0 {
		practices = append(practices, "incident_management")
	}
	if summary.HighRiskDependencyCount > 0 {
		practices = append(practices, "risk_management")
	}
	if summary.RequiresCAB {
		practices = append(practices, "change_enablement")
	}
	if summary.CriticalCICount > 0 {
		practices = append(practices, "monitoring_and_event_management")
	}
	return practices
}

// TransitionStatus transitions a change to a new status
// For approve/reject actions, verifies user is the designated approver
func (s *Service) TransitionStatus(ctx context.Context, id, tenantID, userID int, targetStatus, comment string) (*Change, error) {
	c, err := s.repo.Get(ctx, id, tenantID)
	if err != nil {
		return nil, fmt.Errorf("change not found")
	}

	// Validate state transition (使用 service 包的 canonical 状态机，保证与 legacy service 一致)
	if !service.IsValidChangeStatusTransition(c.Status, targetStatus, c.Type) {
		return nil, fmt.Errorf("无效的状态转换: 从 '%s' 到 '%s'", c.Status, targetStatus)
	}

	// For approval actions, verify user is the approver
	if targetStatus == "approved" || targetStatus == "rejected" {
		history, err := s.repo.GetApprovalHistory(ctx, id, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to get approval history")
		}
		// Find if this user has a pending approval
		isApprover := false
		for _, h := range history {
			if h.ApproverID == userID && h.Status == "pending" {
				isApprover = true
				break
			}
		}
		if !isApprover {
			return nil, fmt.Errorf("用户不是该变更的审批人，无权执行此操作")
		}

		// P0-1：审批先桥接完成对应的 BPMN 待办任务（以流程任务为权威审批来源）。
		// 无关联运行中流程实例时回退为纯业务审批；若存在待办流程任务但完成失败，
		// 则中止业务审批，避免变更状态与流程状态分叉。
		if s.approvalBridge != nil {
			action := "approve"
			if targetStatus == "rejected" {
				action = "reject"
			}
			if _, bridgeErr := s.approvalBridge.CompleteBusinessApprovalTask(
				ctx, tenantID, userID, string(dto.BusinessTypeChange), id, action, comment,
			); bridgeErr != nil {
				return nil, fmt.Errorf("同步流程审批任务失败: %w", bridgeErr)
			}
		}
	}

	// For approve action, update the approval record to approved
	if targetStatus == "approved" {
		history, err := s.repo.GetApprovalHistory(ctx, id, tenantID)
		if err != nil {
			s.logger.Warnw("TransitionStatus: failed to get approval history for record update", "error", err)
		} else {
			for _, h := range history {
				if h.ApproverID == userID && h.Status == "pending" {
					approvedStatus := "approved"
					if _, err := s.repo.UpdateApprovalRecord(ctx, &ApprovalRecord{
						ID:       h.ID,
						TenantID: tenantID,
						Status:   approvedStatus,
					}); err != nil {
						s.logger.Warnw("TransitionStatus: failed to update approval record", "error", err, "record_id", h.ID)
					}
					break
				}
			}
		}
	}

	// P1 域侧桥接：阶段流转（start/complete）先完成对应的 change_normal_flow 阶段节点
	// （注入 change_id），再由域层写权威状态。handler 侧写入的中间值（scheduled/
	// pending_approval）会被随后的域写覆盖，最终状态以域为准；handler 写同值时幂等放行。
	// 桥接失败（存在待办任务但完成不了）则中止，避免变更状态与流程状态分叉。
	// actorUserID 传 0：阶段流转的授权边界在域层（JWT + 资源权限 + 租户隔离），
	// 不强制 BPMN 任务 assignee 匹配（authorizeTaskActor 对 userID<=0 按设计跳过）。
	if s.approvalBridge != nil {
		for _, st := range changeStageTasks(targetStatus) {
			if _, bridgeErr := s.approvalBridge.CompleteBusinessStageTask(
				ctx, tenantID, 0, string(dto.BusinessTypeChange), id, st.key, st.vars,
			); bridgeErr != nil {
				return nil, fmt.Errorf("同步流程阶段任务失败: %w", bridgeErr)
			}
		}
	}

	// H-2 / C-2 修复：
	// 1. 终态（rejected/completed/cancelled/rolled_back）需要事务化：写 change + 收口 pending chains
	// 2. 非终态直接更新
	isTerminal := targetStatus == "rejected" || targetStatus == "completed" ||
		targetStatus == "cancelled" || targetStatus == "rolled_back"
	if isTerminal {
		tx, txErr := s.entClient.Tx(ctx)
		if txErr != nil {
			return nil, fmt.Errorf("开启事务失败: %w", txErr)
		}
		defer tx.Rollback()

		if _, updateErr := tx.Change.UpdateOneID(c.ID).
			Where(change.TenantID(tenantID)).
			SetStatus(targetStatus).
			Save(ctx); updateErr != nil {
			return nil, fmt.Errorf("failed to update change status: %w", updateErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("提交事务失败: %w", commitErr)
		}
		if closeErr := service.CloseChangeApprovalChains(ctx, id, tenantID); closeErr != nil {
			s.logger.Errorw("TransitionStatus: 收口审批链失败（非致命，后续状态机兜底）",
				"error", closeErr, "change_id", id, "tenant_id", tenantID)
		}
		c.Status = targetStatus
		return c, nil
	}

	c.Status = targetStatus
	return s.repo.Update(ctx, c)
}

// changeStageTask 描述一次需要桥接完成的变更阶段节点。
type changeStageTask struct {
	key  string
	vars map[string]interface{}
}

// changeStageTasks 返回变更阶段流转需要依次完成的 change_normal_flow 节点。
// start（in_progress）先完成排期节点（若流程仍停在该节点，handler 写入的 scheduled
// 随后被域写 in_progress 覆盖），再完成实施节点；complete 依次完成验证与关闭两个
// 节点让流程走完，验证节点需带 verify_passed 供 Gateway_VerifyResult 路由。
// 节点不存在或已完成时桥接层返回 (false, nil)，按顺序尝试即可。
func changeStageTasks(targetStatus string) []changeStageTask {
	switch targetStatus {
	case "in_progress":
		return []changeStageTask{
			{key: "Activity_Schedule"},
			{key: "Activity_Implement"},
		}
	case "completed":
		return []changeStageTask{
			{key: "Activity_Verify", vars: map[string]interface{}{"verify_passed": true}},
			{key: "Activity_Close"},
		}
	default:
		return nil
	}
}

// GetApprovalHistory returns approval records for a change
func (s *Service) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	return s.repo.GetApprovalHistory(ctx, changeID, tenantID)
}

// ==================== PIR (Post-Implementation Review) Methods ====================

func (s *Service) CreatePIR(ctx context.Context, req *dto.CreateChangePIRRequest, reviewerID, tenantID int) (*dto.ChangePIRResponse, error) {
	s.logger.Infow("Creating PIR", "change_id", req.ChangeID, "reviewer_id", reviewerID)
	if s.pirService != nil {
		return s.pirService.CreatePIR(ctx, req, reviewerID, tenantID)
	}
	return nil, fmt.Errorf("PIR service not initialized")
}

func (s *Service) GetPIRByChange(ctx context.Context, changeID, tenantID int) (*dto.ChangePIRResponse, error) {
	if s.pirService != nil {
		return s.pirService.GetPIRByChange(ctx, changeID, tenantID)
	}
	return nil, fmt.Errorf("PIR service not initialized")
}

func (s *Service) ListPIRs(ctx context.Context, tenantID int, page, pageSize int, result string) (*dto.ChangePIRListResponse, error) {
	if s.pirService != nil {
		return s.pirService.ListPIRs(ctx, tenantID, page, pageSize, result)
	}
	return nil, fmt.Errorf("PIR service not initialized")
}

func (s *Service) UpdatePIR(ctx context.Context, pirID int, req *dto.UpdateChangePIRRequest, tenantID int) (*dto.ChangePIRResponse, error) {
	if s.pirService != nil {
		return s.pirService.UpdatePIR(ctx, pirID, req, tenantID)
	}
	return nil, fmt.Errorf("PIR service not initialized")
}

func (s *Service) DeletePIR(ctx context.Context, pirID, tenantID int) error {
	if s.pirService != nil {
		return s.pirService.DeletePIR(ctx, pirID, tenantID)
	}
	return fmt.Errorf("PIR service not initialized")
}
