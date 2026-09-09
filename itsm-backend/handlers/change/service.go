package change

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"time"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/cirelationship"
	"itsm-backend/ent/configurationitem"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

type Service struct {
	repo          Repository
	logger        *zap.SugaredLogger
	entClient     *ent.Client
	pirService    *service.ChangePIRService
	processEngine service.ProcessEngine
	directory     database.DirectorySnapshot
}

func (s *Service) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	s.directory = directory
	if s.pirService != nil {
		s.pirService.SetDirectorySnapshot(directory)
	}
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

// SetProcessEngine connects the owning task and callback boundaries.
func (s *Service) SetProcessEngine(engine service.ProcessEngine) {
	s.processEngine = engine
	if customEngine, ok := engine.(*service.CustomProcessEngine); ok {
		if handler, ok := customEngine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler); ok {
			handler.SetChangeService(s)
		}
	}
}

func (s *Service) GetChange(ctx context.Context, id int, tenantID int) (*Change, error) {
	return s.repo.Get(ctx, id, tenantID)
}

func (s *Service) ListChanges(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error) {
	return s.repo.List(ctx, tenantID, page, size, status, search, riskLevel)
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

func (s *Service) GetRisk(ctx context.Context, changeID int, tenantID int) (*RiskAssessment, error) {
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := tx.Change.Query().Where(change.ID(changeID), change.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil())).Only(ctx)
	if err != nil {
		return nil, err
	}
	return readRiskDetails(ctx, tx, changeID, tenantID, current.RiskLevel)
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
			incident.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil()),
			incident.ConfigurationItemIDIn(ciIDs...),
			incident.HasWorkItemWith(ticket.StatusNotIn("resolved", "closed")),
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

// GetApprovalHistory returns approval records for a change
func (s *Service) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	return s.repo.GetApprovalHistory(ctx, changeID, tenantID)
}

// ==================== PIR (Post-Implementation Review) Methods ====================

func (s *Service) CreatePIR(ctx context.Context, req *dto.CreateChangePIRRequest, meta workitemmutation.Meta) (service.PIRMutationResult, error) {
	return s.pirService.CreatePIR(ctx, req, meta)
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

func (s *Service) UpdatePIR(ctx context.Context, id int, req *dto.UpdateChangePIRRequest, meta workitemmutation.Meta) (service.PIRMutationResult, error) {
	return s.pirService.UpdatePIR(ctx, id, req, meta)
}
func (s *Service) DeletePIR(ctx context.Context, id int, req *dto.DeleteChangePIRRequest, meta workitemmutation.Meta) (service.PIRMutationResult, error) {
	return s.pirService.DeletePIR(ctx, id, req, meta)
}
