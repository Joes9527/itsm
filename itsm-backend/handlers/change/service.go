package change

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/cirelationship"
	"itsm-backend/ent/configurationitem"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

type Service struct {
	repo                  Repository
	logger                *zap.SugaredLogger
	entClient             *ent.Client
	pirService            *service.ChangePIRService
	approvalBridge        *service.BPMNApprovalBridge
	processTriggerService service.ProcessTriggerServiceInterface
	processEngine         service.ProcessEngine
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

// SetProcessTriggerService 注入 BPMN 流程触发服务——参照 IncidentService/TicketService
// 的 SetProcessTriggerService 模式（internal/bootstrap/app.go 里同样的 setter 注入方式），
// 不是构造函数参数，避免循环依赖初始化顺序问题。
func (s *Service) SetProcessTriggerService(svc service.ProcessTriggerServiceInterface) {
	s.processTriggerService = svc
}

// SetProcessEngine 注入 BPMN 流程引擎——完成 CAB 审批任务及其级联的排期/驳回节点
// 需要直接调用引擎的 CompleteTask，绕不开 Repository/entClient 能提供的能力。
// 跟 SetProcessTriggerService 一样用 setter 注入，避免构造函数循环依赖。
func (s *Service) SetProcessEngine(engine service.ProcessEngine) {
	s.processEngine = engine
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

	// 5. 先启动 BPMN 流程，再落库 draft -> pending，避免流程启动失败时变更被永久卡在
	// pending（draft 是 SubmitChange 的入口门槛，一旦离开 draft 就没有其他修复路径能
	// 再次提交）。s.processTriggerService 未注入时（理论上不应发生在生产环境，但测试或
	// 未完全 bootstrap 的环境可能出现）保留原有的纯状态流转兜底行为。
	if s.processTriggerService != nil {
		processDefKey := "change_normal_flow"
		if c.Type == "emergency" {
			processDefKey = "change_emergency_flow"
		}
		// 幂等保护：同一个 change 不应该有两个并行的运行中流程实例（比如重复点击提交、
		// 或者前端重试）。businessKey 的约定跟 BPMNApprovalBridge.findPendingApprovalTask
		// 保持一致（"change:{id}"），查询逻辑复用 ent 直接查，不新增一层抽象。
		businessKey := fmt.Sprintf("change:%d", changeID)
		exists, err := s.entClient.ProcessInstance.Query().
			Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查是否已有运行中审批流程失败: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("该变更已经有一个正在进行的审批流程，不能重复提交")
		}

		_, err = s.processTriggerService.TriggerProcess(ctx, &dto.ProcessTriggerRequest{
			BusinessType:         dto.BusinessTypeChange,
			BusinessID:           changeID,
			ProcessDefinitionKey: processDefKey,
			Variables: map[string]interface{}{
				"approval_required": true,
				"requester_id":      float64(submitterID),
			},
			TriggeredBy: fmt.Sprintf("%d", submitterID),
			TenantID:    tenantID,
		})
		if err != nil {
			s.logger.Errorw("SubmitChange: failed to trigger BPMN process", "error", err, "change_id", changeID)
			return nil, fmt.Errorf("启动审批流程失败: %w", err)
		}
	}

	if err := s.repo.MarkSubmittedForApproval(ctx, changeID, tenantID); err != nil {
		s.logger.Warnw("Failed to mark change as submitted", "error", err, "change_id", changeID)
		return nil, fmt.Errorf("提交变更审批失败: %w", err)
	}

	// 6. Notify approvers (optional - to be implemented later or via async)
	// For now, just log the submission
	s.logger.Infow("Change submitted for approval", "change_id", changeID, "submitter_id", submitterID, "approvers", req.ApproverIDs)

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

// completeChangeApprovalTask 完成一个变更的 CAB 审批任务，并在通过/驳回后级联完成
// 紧邻的排期/驳回节点（Activity_Schedule/Activity_Reject）——这两个节点在 BPMN 图里
// 是 userTask，没有任何机制会自动完成它们，不级联完成会变成永远挂起的孤儿任务。
// 级联到此为止：Activity_Schedule 完成后流程会走到 Activity_Implement，那是
// Track4 范围之外的下一阶段，故意让它停在那里，不继续级联。
func (s *Service) completeChangeApprovalTask(ctx context.Context, tenantID, actorUserID, changeID int, action, comment string) error {
	if s.processEngine == nil {
		return fmt.Errorf("流程引擎未初始化")
	}

	businessKey := fmt.Sprintf("change:%d", changeID)
	instance, err := s.entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("该变更没有正在运行的审批流程")
		}
		return fmt.Errorf("查询审批流程实例失败: %w", err)
	}

	task, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
			processtask.TaskType("user_task"),
			processtask.TaskDefinitionKey("Activity_CABApproval"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("该变更没有待处理的审批任务")
		}
		return fmt.Errorf("查询待办审批任务失败: %w", err)
	}

	approvalResult := "rejected"
	if action == "approve" {
		approvalResult = "approved"
	}

	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorUserID)
	actorCtx = context.WithValue(actorCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	// change_id 必须显式传给 CompleteTask：UserTask 完成后触发的
	// ChangeServiceTaskHandler 回调只看"完成任务时提交的 variables"，不会自动合并
	// 流程实例变量（ProcessTriggerService 写进实例变量的是 business_id，键名跟
	// ChangeServiceTaskHandler 期望的 change_id 也对不上），漏传会导致
	// approveChange/scheduleChange/rejectChange 都因为 changeID<=0 静默跳过。
	if err := s.processEngine.CompleteTask(actorCtx, task.TaskID, map[string]interface{}{
		"change_id":       changeID,
		"approvalAction":  action,
		"approvalResult":  approvalResult,
		"approvalComment": comment,
	}); err != nil {
		return fmt.Errorf("完成审批任务失败: %w", err)
	}

	// 级联完成排期/驳回节点。这一步用系统身份（不注入 actorUserID），因为它不是
	// 一个新的、独立的人工决定，是上面那次审批决定的自动延伸——如果这里也要求
	// actorUserID 通过 assignee/candidateUsers 校验，会因为 Activity_Schedule/
	// Activity_Reject 没有声明 assigneeRole 而始终失败。
	systemCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	nextTask, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
			processtask.TaskType("user_task"),
			processtask.TaskDefinitionKeyIn("Activity_Schedule", "Activity_Reject"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 没有下一个任务——理论上不应该发生（Gateway_ApprovalResult 总是路由到
			// Activity_Schedule 或 Activity_Reject 之一），但不要因为找不到就整体失败，
			// 上面那次真正的审批决定已经生效了。
			s.logger.Warnw("completeChangeApprovalTask: 未找到审批后紧邻的任务，跳过级联完成", "change_id", changeID)
			return nil
		}
		return fmt.Errorf("查询审批后续任务失败: %w", err)
	}
	if err := s.processEngine.CompleteTask(systemCtx, nextTask.TaskID, map[string]interface{}{
		"change_id": changeID,
	}); err != nil {
		return fmt.Errorf("级联完成审批后续任务失败: %w", err)
	}
	return nil
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

	// For approval actions, delegate entirely to BPMN: actor authorization is
	// authorizeTaskActor (assigneeRole=change_manager candidates), and the
	// terminal status write happens in ChangeServiceTaskHandler's callback
	// (Task 1), not here. This replaces both the legacy ApprovalHistory-based
	// actor check and the P0-1 bridge — there is no longer a business-side
	// shadow state machine to keep in sync.
	if targetStatus == "approved" || targetStatus == "rejected" {
		if targetStatus == "rejected" && strings.TrimSpace(comment) == "" {
			return nil, fmt.Errorf("驳回变更时必须填写意见")
		}
		action := "approve"
		if targetStatus == "rejected" {
			action = "reject"
		}
		if err := s.completeChangeApprovalTask(ctx, tenantID, userID, id, action, comment); err != nil {
			return nil, err
		}
		// 状态已经由 BPMN 回调写入，重新读一次返回给调用方，不要用调用前的 c（陈旧）。
		return s.repo.Get(ctx, id, tenantID)
	}

	// H-2 / C-2 修复：
	// 1. 终态（rejected/completed/cancelled/rolled_back）需要事务化：写 change + 收口 pending chains
	// 2. 非终态直接更新
	// 注意：targetStatus == "rejected" 这个分支实际上已经在上面的 approve/reject 分支里提前
	// return 了，永远不会走到这里——保留这个条件是防御式编程，避免以后有人调整分支顺序时
	// 悄悄引入 bug，不删除。
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
