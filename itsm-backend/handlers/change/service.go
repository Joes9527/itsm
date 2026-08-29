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
	"itsm-backend/ent/processapprovaldecision"
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
	return svc
}

// SetProcessTriggerService 注入 BPMN 流程触发服务——参照 IncidentService/TicketService
// 的 SetProcessTriggerService 模式（internal/bootstrap/app.go 里同样的 setter 注入方式），
// 不是构造函数参数，避免循环依赖初始化顺序问题。
func (s *Service) SetProcessTriggerService(svc service.ProcessTriggerServiceInterface) {
	s.processTriggerService = svc
}

// SetProcessEngine 注入 BPMN 流程引擎——完成 CAB 审批任务、其级联的排期/驳回节点，
// 以及后续阶段流转（排期/实施/验证/关闭）节点，都需要直接调用引擎的 CompleteTask，
// 绕不开 Repository/entClient 能提供的能力。跟 SetProcessTriggerService 一样用 setter
// 注入，避免构造函数循环依赖。
func (s *Service) SetProcessEngine(engine service.ProcessEngine) {
	s.processEngine = engine
}

// Change methods
func (s *Service) CreateChange(ctx context.Context, c *Change) (*Change, error) {
	s.logger.Infow("Creating change", "title", c.Title, "tenant_id", c.TenantID)
	return s.repo.Create(ctx, c)
}

// CreateChangeForWorkflow 实现 service/bpmn.ChangeDomainServiceInterface，供
// ChangeServiceTaskHandler.createChange 委托调用——BPMN 自动创建的 Change 必须走跟用户在
// UI 手动创建同一条事务化建表路径（CreateChange -> repo.Create），同步建好 WorkItem，
// 不能绕过（Wave 2 迁移前，service/bpmn/change_handler.go 直接 h.client.Change.Create()，
// 完全跳过 WorkItem 创建，是一个真实缺陷，见交付说明）。
//
// ImpactScope/RiskLevel 固定给 "medium"：BPMN service task 的输入变量（title/description/
// type/priority）里从来没有携带这两个字段，迁移前的直接 Ent 写法也没有显式设置它们，靠
// ent/schema/change.go 的 field.Default("medium") 隐式生效。EntRepository.Create 现在会
// 无条件 SetImpactScope/SetRiskLevel（不管值是不是空字符串），如果这里传空字符串会用空值
// 覆盖掉 schema 默认值——所以必须显式传 "medium" 才能保持跟迁移前一致的行为。
func (s *Service) CreateChangeForWorkflow(ctx context.Context, tenantID, createdBy int, title, description, changeType, priority string) (int, error) {
	created, err := s.repo.Create(ctx, &Change{
		Title:       title,
		Description: description,
		Type:        changeType,
		Status:      "draft",
		Priority:    priority,
		ImpactScope: "medium",
		RiskLevel:   "medium",
		CreatedBy:   createdBy,
		TenantID:    tenantID,
	})
	if err != nil {
		return 0, err
	}
	return created.ID, nil
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

// resolveWorkItemID 返回一个变更关联的 WorkItem ID（tickets.id）——Wave 2 起这是 BPMN
// businessKey（"change:{workItemID}"）的权威身份来源，不再是 changeID 自己。查询直接打
// s.entClient.Change（不经过 s.repo），这样无论调用方是走真实 EntRepository 还是测试里的
// mockRepository，只要 s.entClient 里存在这条 Change 行（且已经回填 work_item_id），
// businessKey 解析就是一致的——这也是 completeChangeApprovalTask 等方法在纯 mock repo 测试
// 里依然能正确工作的原因。返回 0 且非 nil error 表示这条变更不存在、不属于当前租户，或者
// 还没有关联的 WorkItem（迁移前创建、还没跑 cmd/backfill_change_work_item 回填）——调用方
// 必须按各自的容错策略处理：SubmitChange/completeAssessmentTask/completeChangeApprovalTask/
// BackfillLegacyPendingChange 是显式的用户发起动作，必须 fail closed；
// cancelRunningProcessInstance/completeChangeStageTasks 是收尾/尽力而为动作，按"没有可操作的
// 运行中实例"同等对待，静默跳过。
func (s *Service) resolveWorkItemID(ctx context.Context, tenantID, changeID int) (int, error) {
	c, err := s.entClient.Change.Query().
		Where(change.ID(changeID), change.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, fmt.Errorf("变更 %d 不存在或不属于当前租户", changeID)
		}
		return 0, fmt.Errorf("查询变更失败: %w", err)
	}
	if c.WorkItemID <= 0 {
		return 0, fmt.Errorf("变更 %d 缺少关联的 WorkItem（work_item_id 未回填），请先运行 cmd/backfill_change_work_item", changeID)
	}
	return c.WorkItemID, nil
}

// changeBusinessKey 是 resolveWorkItemID 的便捷封装，直接返回 "change:{workItemID}"。
func (s *Service) changeBusinessKey(ctx context.Context, tenantID, changeID int) (string, error) {
	workItemID, err := s.resolveWorkItemID(ctx, tenantID, changeID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("change:%d", workItemID), nil
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

	// 3. 先启动 BPMN 流程，再落库 draft -> pending，避免流程启动失败时变更被永久卡在
	// pending（draft 是 SubmitChange 的入口门槛，一旦离开 draft 就没有其他修复路径能
	// 再次提交）。s.processTriggerService 未注入时（理论上不应发生在生产环境，但测试或
	// 未完全 bootstrap 的环境可能出现）保留原有的纯状态流转兜底行为。
	if s.processTriggerService != nil {
		processDefKey := "change_normal_flow"
		if c.Type == "emergency" {
			processDefKey = "change_emergency_flow"
		}
		// Wave 2 起 businessKey 的身份收敛为 WorkItem ID（tickets.id），不再是 changeID
		// 自己——resolveWorkItemID 在这里 fail closed：没有关联的 WorkItem 就不可能构造出
		// 正确的 businessKey，宁可拒绝提交也不要用错误的业务身份启动流程（污染 BPMN
		// 审批决策回查，参见 IncidentService.triggerWorkflowForIncident 的同款判断）。
		workItemID, err := s.resolveWorkItemID(ctx, tenantID, changeID)
		if err != nil {
			return nil, err
		}

		// 幂等保护：同一个 change 不应该有两个并行的运行中流程实例（比如重复点击提交、
		// 或者前端重试）。businessKey 的约定跟 BPMNApprovalBridge.findPendingApprovalTask
		// 保持一致（"change:{workItemID}"），查询逻辑复用 ent 直接查，不新增一层抽象。
		businessKey := fmt.Sprintf("change:%d", workItemID)
		exists, err := s.entClient.ProcessInstance.Query().
			Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查是否已有运行中审批流程失败: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("该变更已经有一个正在进行的审批流程，不能重复提交")
		}

		triggerResp, err := s.processTriggerService.TriggerProcess(ctx, &dto.ProcessTriggerRequest{
			BusinessType:         dto.BusinessTypeChange,
			BusinessID:           workItemID,
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

		// 没有任何业务端点能完成 Activity_Assessment（变更评估）这个 userTask——
		// handlers/change 包本身不暴露这样的接口，唯一能完成它的是通用 BPMN 工作流
		// 控制台（权限模型、UI 都跟变更详情页不是一回事）。不级联完成它，流程会永远
		// 停在这一步，CAB 审批任务根本不会产生。用系统身份（不是 submitter 的身份）
		// 完成它——这不是一次独立的人工决定，是"提交审批"这个动作本身该做完的事，
		// 跟 completeChangeApprovalTask 级联完成 Activity_Schedule/Activity_Reject
		// 是同一个模式。失败时的补偿处理跟下面 MarkSubmittedForApproval 失败时一致：
		// 取消刚创建的流程实例，避免留下一个永远卡在 Activity_Assessment、又被幂等
		// 保护挡住无法重新提交的运行中实例。
		if err := s.completeAssessmentTask(ctx, tenantID, changeID); err != nil {
			if triggerResp != nil {
				if cancelErr := s.processTriggerService.CancelProcess(ctx, triggerResp.ProcessInstanceID,
					"SubmitChange: compensating rollback after assessment auto-completion failure", tenantID); cancelErr != nil {
					s.logger.Warnw("SubmitChange: 补偿取消流程实例失败，可能残留运行中实例阻塞后续重新提交",
						"error", cancelErr, "change_id", changeID, "process_instance_id", triggerResp.ProcessInstanceID)
				}
			}
			s.logger.Errorw("SubmitChange: failed to auto-complete assessment task", "error", err, "change_id", changeID)
			return nil, fmt.Errorf("推进变更评估节点失败: %w", err)
		}

		if err := s.repo.MarkSubmittedForApproval(ctx, changeID, tenantID); err != nil {
			// 补偿：流程实例已经成功创建（状态 running），但落库 draft -> pending 失败了。
			// 如果不取消刚创建的实例，它会一直卡在 running，导致上面的幂等保护永久拒绝
			// 后续重新提交（同一 businessKey 已经存在一个"运行中"实例，且没有任何正常流程
			// 会去完成/取消它）。取消失败不掩盖原始错误——只记录警告，仍然返回
			// MarkSubmittedForApproval 的原始错误。
			if triggerResp != nil {
				if cancelErr := s.processTriggerService.CancelProcess(ctx, triggerResp.ProcessInstanceID,
					"SubmitChange: compensating rollback after MarkSubmittedForApproval failure", tenantID); cancelErr != nil {
					s.logger.Warnw("SubmitChange: 补偿取消流程实例失败，可能残留运行中实例阻塞后续重新提交",
						"error", cancelErr, "change_id", changeID, "process_instance_id", triggerResp.ProcessInstanceID)
				}
			}
			s.logger.Warnw("Failed to mark change as submitted", "error", err, "change_id", changeID)
			return nil, fmt.Errorf("提交变更审批失败: %w", err)
		}
	} else {
		if err := s.repo.MarkSubmittedForApproval(ctx, changeID, tenantID); err != nil {
			s.logger.Warnw("Failed to mark change as submitted", "error", err, "change_id", changeID)
			return nil, fmt.Errorf("提交变更审批失败: %w", err)
		}
	}

	// 4. Notify approvers (optional - to be implemented later or via async)
	// For now, just log the submission
	s.logger.Infow("Change submitted for approval", "change_id", changeID, "submitter_id", submitterID)

	c.Status = "pending"
	return c, nil
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

// cancelRunningProcessInstance 取消一个变更身上还挂着的运行中 BPMN 流程实例（如果有）。
// 纯收尾操作：找不到运行中实例（正常情况——大多数终态转换发生时流程早就走完了）
// 或者取消调用本身失败，都只记日志，不向调用方传播错误，不影响已经提交成功的业务侧
// 状态转换。s.processTriggerService 未注入时直接跳过（理论上不应该发生在生产环境，
// 但测试或未完全 bootstrap 的环境可能出现，参照 SubmitChange 同样的判空处理）。
func (s *Service) cancelRunningProcessInstance(ctx context.Context, tenantID, changeID int, reason string) {
	if s.processTriggerService == nil {
		return
	}
	businessKey, err := s.changeBusinessKey(ctx, tenantID, changeID)
	if err != nil {
		// 没有关联的 WorkItem（存量未回填数据，或 changeID 本身查不到）——按"没有运行中
		// 流程实例"同等对待，静默跳过。这是收尾清理，不应该因为缺 WorkItem 就报错阻塞
		// 已经生效的业务侧终态转换。
		return
	}
	instance, err := s.entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
		Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			s.logger.Warnw("cancelRunningProcessInstance: 查询运行中流程实例失败，跳过清理", "error", err, "change_id", changeID)
		}
		return
	}
	if err := s.processTriggerService.CancelProcess(ctx, instance.ID, reason, tenantID); err != nil {
		s.logger.Warnw("cancelRunningProcessInstance: 取消流程实例失败，可能残留在工作流控制台", "error", err, "change_id", changeID, "process_instance_id", instance.ID)
	}
}

// BackfillLegacyPendingChange 面向 Track4 上线切换时刻的存量数据：一个变更在旧版审批链
// 流程下已经提交到了 pending 状态，但因为迁移前后审批机制整体切换，没有对应的 BPMN
// 流程实例——正常的审批/驳回操作都要求存在运行中的流程实例，这批变更会永久卡住。
// 这不是常规业务入口，是给一次性迁移 CLI（cmd/backfill_legacy_pending_changes）用的：
// 效果等价于重放一次 SubmitChange，但跳过"必须是 draft 状态"这道门槛——这些变更已经不在
// draft 了，且旧的 change_approvals/change_approval_chains 表数据不会被读取（Track4 之后
// 这两张表已经不是权威数据源）。审批人沿用 assigneeRole=change_manager 的候选人解析，
// 不尝试还原旧审批链里指定的具体审批人。
//
// ⚠️ 排期依赖（Wave 2 新增）：这个方法现在要求变更已经有关联的 WorkItem（work_item_id
// 已回填），否则 resolveWorkItemID 会 fail closed。这意味着运维流程里
// cmd/backfill_change_work_item 必须先于 cmd/backfill_legacy_pending_changes 运行——
// 后者自己的 findCandidates 预过滤仍然用旧的 "change:{changeID}" 格式查重（那个文件不在
// 本次任务允许修改的范围内），可能会把已经有 WorkItem 但旧格式查不到运行中实例的变更
// 误判为候选，但这里的 resolveWorkItemID 会在缺 WorkItem 时正确拒绝、在有 WorkItem 时
// 正确按新格式查重，所以不会产生错误的双重实例，只是重跑一次没有实际效果的候选而已，
// 交付说明里有更详细的说明。
func (s *Service) BackfillLegacyPendingChange(ctx context.Context, changeID, tenantID int) error {
	c, err := s.repo.Get(ctx, changeID, tenantID)
	if err != nil {
		return fmt.Errorf("变更不存在: %w", err)
	}
	if c.Status != "pending" {
		return fmt.Errorf("变更当前状态是 %q，不是 pending，跳过（可能已经被处理过，或者本来就不需要回填）", c.Status)
	}
	if s.processTriggerService == nil || s.processEngine == nil {
		return fmt.Errorf("流程触发/引擎服务未初始化")
	}

	workItemID, err := s.resolveWorkItemID(ctx, tenantID, changeID)
	if err != nil {
		return fmt.Errorf("回填旧流程前必须先有关联的 WorkItem，请先运行 cmd/backfill_change_work_item: %w", err)
	}

	businessKey := fmt.Sprintf("change:%d", workItemID)
	// 只有非 terminated 的实例才算"已经在走 Track4 新流程"。之前失败的回填尝试
	// 会用 CancelProcess 补偿，留下一条 terminated 记录——如果这里不排除它，
	// 失败一次就会把这个变更永久挡在回填之外，重试也没用。
	exists, err := s.entClient.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(businessKey),
			processinstance.TenantID(tenantID),
			processinstance.StatusNEQ("terminated"),
		).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("检查是否已有流程实例失败: %w", err)
	}
	if exists {
		return fmt.Errorf("该变更已经有关联的流程实例，不需要回填（可能已经走过 Track4 的新流程）")
	}

	processDefKey := "change_normal_flow"
	if c.Type == "emergency" {
		processDefKey = "change_emergency_flow"
	}
	triggerResp, err := s.processTriggerService.TriggerProcess(ctx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           workItemID,
		ProcessDefinitionKey: processDefKey,
		Variables: map[string]interface{}{
			"approval_required": true,
			// 旧审批链数据不记录"提交人"这个概念对应的现代字段，用创建人兜底——
			// 只影响流程变量里的 requester_id（用于候选人解析时排除申请人自己），
			// 不影响状态机、不影响谁能审批。
			"requester_id": float64(c.CreatedBy),
		},
		TriggeredBy: fmt.Sprintf("%d", c.CreatedBy),
		TenantID:    tenantID,
	})
	if err != nil {
		return fmt.Errorf("触发审批流程失败: %w", err)
	}

	if err := s.completeAssessmentTask(ctx, tenantID, changeID); err != nil {
		// 流程实例已经创建，评估节点没推进成功——补偿取消掉，避免留下一个孤儿实例，
		// 保持"回填要么完整成功、要么完全没发生"这个不变式，方便这个一次性工具重跑。
		if triggerResp != nil && s.processTriggerService != nil {
			if cancelErr := s.processTriggerService.CancelProcess(ctx, triggerResp.ProcessInstanceID,
				"BackfillLegacyPendingChange: compensating rollback after assessment failure", tenantID); cancelErr != nil {
				s.logger.Warnw("BackfillLegacyPendingChange: 补偿取消流程实例失败", "error", cancelErr, "change_id", changeID)
			}
		}
		return fmt.Errorf("推进变更评估节点失败: %w", err)
	}
	return nil
}

// completeAssessmentTask 用系统身份自动完成一个刚触发流程的变更的 Activity_Assessment
// （变更评估）任务，把流程从"评估中"直接推进到 CAB 审批节点。Activity_Assessment 没有
// 声明 assigneeRole，所以这里不注入 actorUserID（跟 completeChangeApprovalTask 级联完成
// Activity_Schedule/Activity_Reject 时用系统身份的理由一样：不注入的话 authorizeTaskActor
// 因为 userID<=0 直接放行，注入了反而可能因为不符合任何候选人规则而报错）。
// updateChange（Activity_Assessment 声明的 action=update_change）只在 change_id 传对时
// 才不会短路失败——其余字段（title/description/status）全部可选，传空变量表示"确认存在、
// 不改任何字段"的空更新，是安全的。
func (s *Service) completeAssessmentTask(ctx context.Context, tenantID, changeID int) error {
	if s.processEngine == nil {
		return fmt.Errorf("流程引擎未初始化")
	}

	businessKey, err := s.changeBusinessKey(ctx, tenantID, changeID)
	if err != nil {
		return err
	}
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
			processtask.TaskDefinitionKey("Activity_Assessment"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("该变更没有待处理的评估任务")
		}
		return fmt.Errorf("查询待办评估任务失败: %w", err)
	}

	systemCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	if err := s.processEngine.CompleteTask(systemCtx, task.TaskID, map[string]interface{}{
		"change_id": changeID,
	}); err != nil {
		return fmt.Errorf("完成变更评估任务失败: %w", err)
	}
	return nil
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

	businessKey, err := s.changeBusinessKey(ctx, tenantID, changeID)
	if err != nil {
		return err
	}
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
		if !ent.IsNotFound(err) {
			return fmt.Errorf("查询待办审批任务失败: %w", err)
		}
		// 找不到待处理的 CAB 任务不一定是错误——很可能是重试：上一次调用已经成功完成了
		// CAB 决定（决定已经落库在 ProcessApprovalDecision），只是紧接着的级联步骤失败了
		// （比如瞬时 DB 错误），调用方看到整体报错后重试，这次自然找不到"待处理的 CAB
		// 任务"，因为它已经是 completed 状态。resumePendingCascade 负责判断这到底是真的
		// "没有审批任务"，还是这种可以续做的场景。
		return s.resumePendingCascade(ctx, instance.ID, tenantID, changeID, action)
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

	// CAB 决定已经生效并落库——从这里开始，级联完成排期/驳回节点失败不应该让整个调用报错，
	// 那样会让调用方以为审批没生效而重试，但重试会撞上"该变更没有待处理的审批任务"（上面
	// 那次判断会走进 resumePendingCascade 分支）。用 Warnw 记录、返回 nil，调用方下一次
	// 对同一个 change 的 TransitionStatus 调用会自然经由 resumePendingCascade 续完级联。
	if err := s.completeCascadeTask(ctx, instance.ID, tenantID, changeID); err != nil {
		s.logger.Warnw("completeChangeApprovalTask: 级联完成审批后续任务失败，CAB 决定已生效，可通过重试续做",
			"error", err, "change_id", changeID)
	}
	return nil
}

// completeCascadeTask 用系统身份完成 CAB 审批之后紧邻的排期/驳回节点
// （Activity_Schedule/Activity_Reject）。这两个节点在 BPMN 图里是 userTask，没有任何机制
// 会自动完成它们，不完成会变成永远挂起的孤儿任务。用系统身份（不注入 actorUserID）——这
// 不是一个新的、独立的人工决定，是 CAB 决定的自动延伸——如果这里也要求 actorUserID 通过
// assignee/candidateUsers 校验，会因为 Activity_Schedule/Activity_Reject 没有声明
// assigneeRole 而始终失败。
func (s *Service) completeCascadeTask(ctx context.Context, instanceID, tenantID, changeID int) error {
	systemCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	nextTask, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instanceID)),
			processtask.TaskType("user_task"),
			processtask.TaskDefinitionKeyIn("Activity_Schedule", "Activity_Reject"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 没有下一个任务——理论上不应该发生（Gateway_ApprovalResult 总是路由到
			// Activity_Schedule 或 Activity_Reject 之一），但不要因为找不到就整体失败，
			// 真正的审批决定已经生效了。
			s.logger.Warnw("completeCascadeTask: 未找到审批后紧邻的任务，跳过级联完成", "change_id", changeID)
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

// resumePendingCascade 处理"CAB 任务已经不在待办状态"这个入口——区分两种情况：
//  1. 真的是重试场景：CAB 决定已经生效（Activity_Schedule/Activity_Reject 之一还卡在待办，
//     说明网关已经按上一次的决定路由过去了），这里直接续做级联，不要求也不允许重新做一次
//     CAB 决定。如果续做的级联节点跟本次调用请求的 action 对不上（比如上次已经 approve
//     路由到了 Activity_Schedule，这次却调用 reject），说明是一次矛盾的重试，明确报错而不是
//     悄悄按旧决定生效或者悄悄改成新决定——审批决定不能被静默覆盖。
//  2. 级联在上一次调用里已经真正做完了（没有任何待办任务）：整个操作其实已经成功，这次调用
//     只是调用方没收到成功响应就重试——按幂等处理，直接返回成功，不报错。
//  3. 两种都不是（从来没有产生过 CAB 决定）：这才是真正的"没有待处理的审批任务"。
func (s *Service) resumePendingCascade(ctx context.Context, instanceID, tenantID, changeID int, action string) error {
	nextTask, err := s.entClient.ProcessTask.Query().
		Where(
			processtask.HasProcessInstanceWith(processinstance.ID(instanceID)),
			processtask.TaskType("user_task"),
			processtask.TaskDefinitionKeyIn("Activity_Schedule", "Activity_Reject"),
			processtask.StatusIn("created", "assigned", "started", "delegated"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 没有待办的 CAB 任务，也没有待办的级联任务——要么这个变更从来没有产生过
			// CAB 决定，要么上一次调用其实已经完整成功了。查一下有没有已经落库的
			// ProcessApprovalDecision 来区分这两种情况，避免把"其实已经成功"的重试
			// 报成错误。
			decided, decErr := s.entClient.ProcessApprovalDecision.Query().
				Where(
					processapprovaldecision.ProcessInstanceID(instanceID),
					processapprovaldecision.TenantID(tenantID),
					processapprovaldecision.NodeKey("Activity_CABApproval"),
				).
				Exist(ctx)
			if decErr != nil {
				return fmt.Errorf("查询审批决策记录失败: %w", decErr)
			}
			if decided {
				// 上一次调用已经完整做完了 CAB 决定 + 级联，这次是重复调用，按幂等处理。
				return nil
			}
			return fmt.Errorf("该变更没有待处理的审批任务")
		}
		return fmt.Errorf("查询审批后续任务失败: %w", err)
	}

	expectedKey := "Activity_Reject"
	if action == "approve" {
		expectedKey = "Activity_Schedule"
	}
	if nextTask.TaskDefinitionKey != expectedKey {
		return fmt.Errorf("该变更的审批决定已经生效（%s），跟本次请求的操作不一致，不能重复决定", nextTask.TaskDefinitionKey)
	}

	if err := s.completeCascadeTask(ctx, instanceID, tenantID, changeID); err != nil {
		return fmt.Errorf("续做级联完成审批后续任务失败: %w", err)
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
		if err := canApproveChange(userID, c); err != nil {
			return nil, err
		}
		if err := s.completeChangeApprovalTask(ctx, tenantID, userID, id, action, comment); err != nil {
			return nil, err
		}
		// 状态已经由 BPMN 回调写入，重新读一次返回给调用方，不要用调用前的 c（陈旧）。
		return s.repo.Get(ctx, id, tenantID)
	}

	// 阶段流转（start/complete）先完成对应的 change_normal_flow 阶段节点（注入
	// change_id），再由域层写权威状态。handler 侧写入的中间值（scheduled/
	// pending_approval）会被随后的域写覆盖，最终状态以域为准；节点不存在/已完成时
	// completeChangeStageTasks 幂等跳过，只有节点存在但完成失败才中止，避免变更状态
	// 与流程状态分叉。不注入 actorUserID：阶段流转的授权边界在域层（JWT + 资源权限 +
	// 租户隔离），不强制 BPMN 任务 assignee 匹配（authorizeTaskActor 对 userID<=0
	// 按设计跳过）。
	if err := s.completeChangeStageTasks(ctx, tenantID, id, targetStatus); err != nil {
		return nil, err
	}

	// H-2 / C-2 修复：
	// 1. 终态（rejected/completed/cancelled/rolled_back）需要事务化写 change
	// 2. 非终态直接更新
	// 注意：targetStatus == "rejected" 这个分支实际上已经在上面的 approve/reject 分支里提前
	// return 了，永远不会走到这里——保留这个条件是防御式编程，避免以后有人调整分支顺序时
	// 悄悄引入 bug，不删除。
	// 注：此前这里还会调用 service.CloseChangeApprovalChains 收口 change_approval_chains 里的
	// pending 行；Track4 迁移后本包已不再向该表写入 pending 记录（详见 Task 6），保留该调用只会
	// 是一次必然的空操作，且在裸 DB 句柄为 nil 时还会打一条误导性的 ERROR 日志，故删除。
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
		// 变更进入终态后，如果还有一个运行中的 BPMN 流程实例挂在它身上（比如卡在
		// Track4 范围之外的 Activity_Implement/Verify 节点——那几个节点不接 BPMN 任务
		// 完成，业务侧终态转换不会自动帮它们收尾），把它取消掉，避免在工作流控制台
		// 堆积孤儿实例（包括理论上还能被人误操作完成的待办任务）。这一步是收尾性质的
		// 清理，找不到运行中实例（最常见情况：CAB 驳回已经通过 Flow_End 正常终止了）
		// 或者取消本身失败都只记警告，不影响已经提交成功的业务侧终态转换。
		s.cancelRunningProcessInstance(ctx, tenantID, c.ID, fmt.Sprintf("change transitioned to %s", targetStatus))
		c.Status = targetStatus
		return c, nil
	}

	c.Status = targetStatus
	return s.repo.Update(ctx, c)
}

// changeStageTask 描述一次需要原生完成的变更阶段节点。
type changeStageTask struct {
	key  string
	vars map[string]interface{}
}

// changeStageTasks 返回变更阶段流转需要依次完成的 change_normal_flow 节点。
// start（in_progress）先完成排期节点（若流程仍停在该节点，handler 写入的 scheduled
// 随后被域写 in_progress 覆盖），再完成实施节点；complete 依次完成验证与关闭两个
// 节点让流程走完，验证节点需带 verify_passed 供 Gateway_VerifyResult 路由。
// 节点不存在或已完成时 completeChangeStageTasks 跳过，按顺序尝试即可。
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

// completeChangeStageTasks 变更阶段流转（start/complete）的原生 BPMN 完成：依次完成
// change_normal_flow 里排期/实施/验证/关闭节点，让流程随域状态推进——不经
// BPMNApprovalBridge，直接用 processEngine，跟 completeChangeApprovalTask 同一套
// 引擎依赖。语义特意保持跟旧桥接一致：变更没有关联的运行中流程实例、或指定节点当前
// 不是待办任务（已完成/流程走了别的分支），都不是错误，跳过继续下一个节点；节点存在
// 但 CompleteTask 调用失败才中止转换，避免变更状态和流程状态分叉。
func (s *Service) completeChangeStageTasks(ctx context.Context, tenantID, changeID int, targetStatus string) error {
	if s.processEngine == nil {
		return nil
	}
	tasks := changeStageTasks(targetStatus)
	if len(tasks) == 0 {
		return nil
	}

	// Review fix：这里原来直接用裸 changeID 拼 businessKey，跟 cancelRunningProcessInstance
	// 同一批"收尾/尽力而为"函数里唯一没有切到 resolveWorkItemID 的一个——changes.id 和
	// tickets.id 是两张表各自独立的自增序列，只有在全新数据库里第一条 Change 恰好也是
	// 第一条 ticket 时两者数值才会碰巧相等，这也是原有测试
	// TestTransitionStatus_StageCompletion_AdvanceProcessEndToEnd 没有测出这个 bug 的
	// 原因（该测试的 Change 和 WorkItem 恰好都是各自表里的第一行）。真实环境/多条数据
	// 下两者几乎必然不同，会导致这里永远查不到用新 businessKey 格式启动的运行中实例，
	// 排期/实施/验证/关闭节点静默永远不会被原生完成，流程实例永久卡在 Activity_Schedule
	// 或更早的节点——域侧状态字段仍然会正确推进（TransitionStatus 的域写在这之后，
	// 不依赖这里成功），但 BPMN 监控台会看到大量卡死不动的流程实例，审批平台的运行
	// 台账与域状态从此对不上。改成跟 cancelRunningProcessInstance 完全一致的
	// changeBusinessKey 解析 + 静默跳过（这里本来就是尽力而为语义，找不到 WorkItem
	// 或 resolveWorkItemID 失败都不应该阻塞域状态转换本身）。
	businessKey, err := s.changeBusinessKey(ctx, tenantID, changeID)
	if err != nil {
		// 没有关联的 WorkItem（存量未回填数据，或 changeID 本身查不到）——按"没有可操作的
		// 运行中实例"同等对待，静默跳过，不阻塞已经生效的域状态转换。
		return nil
	}
	instance, err := s.entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(businessKey), processinstance.TenantID(tenantID), processinstance.Status("running")).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 没有关联的运行中流程实例——不是所有变更都走 change_normal_flow（比如紧急
			// 变更走 emergency 流程，或流程已经在别的分支提前结束），跳过阶段完成。
			return nil
		}
		return fmt.Errorf("查询变更流程实例失败: %w", err)
	}

	// actorUserID 不注入：阶段流转的授权边界在域层（JWT + 资源权限 + 租户隔离），不
	// 强制 BPMN 任务 assignee 匹配（authorizeTaskActor 对 userID<=0 按设计跳过）。
	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, 0)
	actorCtx = context.WithValue(actorCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	for _, st := range tasks {
		task, err := s.entClient.ProcessTask.Query().
			Where(
				processtask.HasProcessInstanceWith(processinstance.ID(instance.ID)),
				processtask.TaskType("user_task"),
				processtask.TaskDefinitionKey(st.key),
				processtask.StatusIn("created", "assigned", "started", "delegated"),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				// 节点当前不是待办任务——可能还没走到、已经被完成过（重试场景），或
				// 流程走了别的分支。跳过，尝试列表里的下一个节点。
				continue
			}
			return fmt.Errorf("查询变更阶段任务(%s)失败: %w", st.key, err)
		}

		vars := map[string]interface{}{"change_id": changeID}
		for k, v := range st.vars {
			vars[k] = v
		}
		if err := s.processEngine.CompleteTask(actorCtx, task.TaskID, vars); err != nil {
			return fmt.Errorf("完成变更阶段任务(%s)失败: %w", st.key, err)
		}
	}
	return nil
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
