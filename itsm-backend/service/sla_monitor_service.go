package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/slaviolation"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

type SLAMonitorService struct {
	client          *ent.Client
	execution       *database.ExecutionPolicy
	logger          *zap.SugaredLogger
	alertService    *SLAAlertService
	notificationSvc *TicketNotificationService
}

type SLAMetrics struct {
	ResponseTime       float64 `json:"responseTime"`
	ResolutionTime     float64 `json:"resolutionTime"`
	SLACompliance      float64 `json:"slaCompliance"`
	TotalTickets       int     `json:"totalTickets"`
	ViolatedTickets    int     `json:"violatedTickets"`
	AvgResponseMinutes float64 `json:"avgResponseMinutes"`
	AvgResolutionHours float64 `json:"avgResolutionHours"`
}

func NewSLAMonitorService(client *ent.Client, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *SLAMonitorService {
	return &SLAMonitorService{
		client:    client,
		execution: execution,
		logger:    logger,
	}
}

// SetAlertService 设置告警服务
func (s *SLAMonitorService) SetAlertService(alertService *SLAAlertService) {
	s.alertService = alertService
}

// SetNotificationService 设置通知服务
func (s *SLAMonitorService) SetNotificationService(notificationSvc *TicketNotificationService) {
	s.notificationSvc = notificationSvc
}

type SLACheckStats struct {
	TotalChecked       int `json:"totalChecked"`       // 检查的工单总数
	NewViolations      int `json:"newViolations"`      // 新创建的违规数
	ExistingViolations int `json:"existingViolations"` // 已存在的违规数
	WarningsTriggered  int `json:"warningsTriggered"`  // 触发的预警数
	AlertsTriggered    int `json:"alertsTriggered"`    // 触发的告警数
}

// CheckSLAViolations 检查所有工单的SLA违规情况
func (s *SLAMonitorService) CheckSLAViolations(ctx context.Context, tenantID int) (*SLACheckStats, error) {
	s.logger.Infow("Starting SLA violation check", "tenant_id", tenantID)

	// Alert history and its critical transport still require their own scope
	// transaction integration. Do not run that path under candidate admission.
	if s.execution != nil && s.execution.IsCandidate() && s.alertService != nil {
		return nil, fmt.Errorf("candidate SLA alert execution is not yet admitted")
	}
	stats := &SLACheckStats{}
	lastID := 0
	for {
		tx, err := s.client.Tx(ctx)
		if err != nil {
			return stats, err
		}
		member, err := s.execution.TenantPredicate(ctx, tx, tenantID, ticket.FieldTenantID, ticket.FieldID)
		if err != nil {
			_ = tx.Rollback()
			return stats, err
		}
		items, err := tx.Ticket.Query().Where(ticket.TenantIDEQ(tenantID), ticket.IDGT(lastID), ticket.ResolvedAtIsNil(), ticket.ClosedAtIsNil(), ticket.DeletedAtIsNil(), member).Order(ent.Asc(ticket.FieldID)).Limit(100).All(ctx)
		if err != nil {
			_ = tx.Rollback()
			return stats, err
		}
		if err = tx.Commit(); err != nil {
			return stats, err
		}
		if len(items) == 0 {
			return stats, nil
		}
		for _, item := range items {
			lastID = item.ID
			added, existing, err := s.checkTicketViolations(ctx, item.ID, tenantID)
			if err != nil {
				return stats, fmt.Errorf("check SLA work item %d: %w", item.ID, err)
			}
			stats.TotalChecked++
			stats.NewViolations += added
			stats.ExistingViolations += existing
			if s.alertService != nil {
				if s.checkAndTriggerWarning(ctx, item, time.Now()) {
					stats.WarningsTriggered++
				}
				alerted, err := s.alertService.CheckAndTriggerAlerts(ctx, item.ID, tenantID)
				if err != nil {
					return stats, err
				}
				if alerted {
					stats.AlertsTriggered++
				}
			}
		}
	}
}

// checkTicketViolations re-reads the cycle and duplicates in the owning write
// transaction. The version fence serializes competing scans and lifecycle edits.
func (s *SLAMonitorService) checkTicketViolations(ctx context.Context, ticketID, tenantID int) (int, int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	member, err := s.execution.TenantPredicate(ctx, tx, tenantID, ticket.FieldTenantID, ticket.FieldID)
	if err != nil {
		return 0, 0, err
	}
	if err := s.execution.RequireEntMembers(ctx, tx, tenantID, ticketID); err != nil {
		return 0, 0, err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), member).Only(ctx)
	if err != nil {
		return 0, 0, err
	}
	if !item.ResolvedAt.IsZero() || item.ClosedAt != nil {
		return 0, 0, tx.Commit()
	}
	now := time.Now()
	cycle := projectSLACycle(item, now)
	kinds := []struct {
		name     string
		breached bool
		deadline time.Time
	}{
		{"response_time", cycle.ResponseBreached, item.SLAResponseDeadline},
		{"resolution_time", cycle.ResolutionBreached, item.SLAResolutionDeadline},
	}
	added, existing := 0, 0
	fenced := false
	for _, kind := range kinds {
		if !kind.breached {
			continue
		}
		found, err := tx.SLAViolation.Query().Where(slaviolation.TenantIDEQ(tenantID), slaviolation.TicketIDEQ(ticketID), slaviolation.ViolationTypeEQ(kind.name), slaviolation.ResolvedAtIsNil(), slaviolation.ViolationTimeGTE(slaCycleStart(item))).Exist(ctx)
		if err != nil {
			return 0, 0, err
		}
		if found {
			existing++
			continue
		}
		if !fenced {
			count, err := tx.Ticket.Update().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.VersionEQ(item.Version), ticket.DeletedAtIsNil(), member).AddVersion(1).Save(ctx)
			if err != nil {
				return 0, 0, err
			}
			if count != 1 {
				return 0, 0, fmt.Errorf("SLA cycle changed during scan")
			}
			fenced = true
		}
		if err := s.createViolationTx(ctx, tx, item, kind.name, kind.deadline, now); err != nil {
			return 0, 0, err
		}
		added++
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, existing, nil
}

func (s *SLAMonitorService) createViolationTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, violationType string, deadline, now time.Time) error {
	definition, err := tx.SLADefinition.Query().Where(sladefinition.IDEQ(item.SLADefinitionID), sladefinition.TenantIDEQ(item.TenantID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("SLA definition: %w", err)
	}
	exceededMinutes := now.Sub(deadline).Minutes()
	if exceededMinutes < 0 {
		exceededMinutes = 0
	}
	severity := "low"
	if exceededMinutes > 60 {
		severity = "medium"
	}
	if exceededMinutes > 240 {
		severity = "high"
	}
	if exceededMinutes > 480 {
		severity = "critical"
	}
	violation, err := tx.SLAViolation.Create().SetCreatedBy(0).SetTicketID(item.ID).SetTicketType("ticket").SetSLADefinitionID(definition.ID).SetSLAName(definition.Name).SetViolationType(violationType).SetViolationTime(now).SetDescription(fmt.Sprintf("工单 %s 违反SLA (%s): 超过截止时间 %.1f 分钟", item.TicketNumber, violationType, exceededMinutes)).SetSeverity(severity).SetIsResolved(false).SetTenantID(item.TenantID).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return err
	}
	if s.notificationSvc != nil {
		if err := s.notificationSvc.EnqueueSLABreachedTx(ctx, tx, item, violation.ID, violationType, exceededMinutes); err != nil {
			return err
		}
	}
	eventID := uuid.NewString()
	payload, err := json.Marshal(slaBreachDeliveryPayload{Version: 1, EventID: eventID, TenantID: item.TenantID, TicketID: item.ID, ViolationID: violation.ID, SLAPolicyID: definition.ID, BreachedType: mapViolationTypeToBreachType(violationType), BreachedAt: now})
	if err != nil {
		return err
	}
	_, err = enqueueOutboxEvent(ctx, s.client, tx, NewOutboxEvent{ExecutionWorkItemID: item.ID, EventID: eventID, EventType: slaBreachEventType, TenantID: item.TenantID, AggregateType: "sla_violation", AggregateID: fmt.Sprint(violation.ID), Payload: payload})
	return err
}

// mapViolationTypeToBreachType 将内部违规类型映射为领域事件契约值
func mapViolationTypeToBreachType(violationType string) string {
	switch violationType {
	case "response_time":
		return "response"
	case "resolution_time":
		return "resolve"
	default:
		return violationType
	}
}

// checkAndTriggerWarning 检查是否需要发送SLA预警（在截止时间前触发）
// 返回是否发送了预警
func (s *SLAMonitorService) checkAndTriggerWarning(ctx context.Context, t *ent.Ticket, now time.Time) bool {
	if t.ClosedAt != nil {
		return false
	}
	// SLA预警阈值：默认在截止时间前20%时预警
	warningThreshold := 0.8

	sentWarning := false
	// Persisted deadlines include the applied pause extension. Remove it
	// from both elapsed and target durations to compare the same active clock.
	paused := time.Duration(t.SLAPausedMinutes) * time.Minute

	// 检查响应时间SLA预警
	if t.FirstResponseAt.IsZero() && !t.SLAResponseDeadline.IsZero() {
		totalDuration := t.SLAResponseDeadline.Sub(slaCycleStart(t)) - paused
		elapsed := now.Sub(slaCycleStart(t)) - paused
		progress := elapsed.Seconds() / totalDuration.Seconds()

		if totalDuration > 0 && progress >= warningThreshold && now.Before(t.SLAResponseDeadline) {
			if s.alertService != nil {
				if warned, _ := s.alertService.TriggerSLAWarning(ctx, t.ID, "response_time", t.TenantID); warned {
					sentWarning = true
					s.logger.Infow("SLA response warning sent", "ticket_id", t.ID, "ticket_number", t.TicketNumber,
						"deadline", t.SLAResponseDeadline)
				}
			}
		}
	}

	// 检查解决时间SLA预警
	if t.ResolvedAt.IsZero() && !t.SLAResolutionDeadline.IsZero() {
		totalDuration := t.SLAResolutionDeadline.Sub(slaCycleStart(t)) - paused
		elapsed := now.Sub(slaCycleStart(t)) - paused
		progress := elapsed.Seconds() / totalDuration.Seconds()

		if totalDuration > 0 && progress >= warningThreshold && now.Before(t.SLAResolutionDeadline) {
			if s.alertService != nil {
				if warned, _ := s.alertService.TriggerSLAWarning(ctx, t.ID, "resolution_time", t.TenantID); warned {
					sentWarning = true
					s.logger.Infow("SLA resolution warning sent", "ticket_id", t.ID, "ticket_number", t.TicketNumber,
						"deadline", t.SLAResolutionDeadline)
				}
			}
		}
	}

	return sentWarning
}

// CalculateSLAMetrics 计算SLA指标
func (s *SLAMonitorService) CalculateSLAMetrics(ctx context.Context, tenantID int, startTime, endTime time.Time) (*SLAMetrics, error) {
	s.logger.Infow("Calculating SLA metrics", "tenant_id", tenantID, "start_time", startTime, "end_time", endTime)

	metrics := &SLAMetrics{}

	// 获取时间范围内的工单
	tickets, err := s.client.Ticket.Query().
		Where(
			ticket.TenantIDEQ(tenantID),
			ticket.CreatedAtGTE(startTime),
			ticket.CreatedAtLTE(endTime),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets: %w", err)
	}

	metrics.TotalTickets = len(tickets)

	var totalResponseMinutes float64
	var totalResolutionHours float64
	responseCount := 0
	resolutionCount := 0
	violatedCount := 0

	for _, t := range tickets {
		// 计算首次响应时间
		if !t.FirstResponseAt.IsZero() {
			responseMinutes := float64(projectSLACycle(t, time.Now()).ResponseTimeUsed)
			totalResponseMinutes += responseMinutes
			responseCount++
		}

		// 计算解决时间
		if !t.ResolvedAt.IsZero() {
			resolutionHours := float64(projectSLACycle(t, time.Now()).ResolutionTimeUsed) / 60
			totalResolutionHours += resolutionHours
			resolutionCount++

		}
		projection := projectSLACycle(t, time.Now())
		if projection.ResponseBreached || projection.ResolutionBreached {
			violatedCount++
		}
	}

	// 计算平均值
	if responseCount > 0 {
		metrics.AvgResponseMinutes = totalResponseMinutes / float64(responseCount)
	}
	if resolutionCount > 0 {
		metrics.AvgResolutionHours = totalResolutionHours / float64(resolutionCount)
	}

	// 计算SLA达成率
	if metrics.TotalTickets > 0 {
		metrics.SLACompliance = float64(metrics.TotalTickets-violatedCount) / float64(metrics.TotalTickets) * 100
	}

	metrics.ViolatedTickets = violatedCount

	s.logger.Infow("SLA metrics calculated",
		"tenant_id", tenantID,
		"compliance", metrics.SLACompliance,
		"avg_response", metrics.AvgResponseMinutes,
		"avg_resolution", metrics.AvgResolutionHours)

	return metrics, nil
}

// GetSLAComplianceByDefinition 获取按SLA定义的合规率统计
func (s *SLAMonitorService) GetSLAComplianceByDefinition(ctx context.Context, tenantID int) ([]*SLAComplianceStat, error) {
	slas, err := s.client.SLADefinition.Query().
		Where(sladefinition.TenantIDEQ(tenantID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	var stats []*SLAComplianceStat
	for _, sla := range slas {
		// 获取该SLA的工单数量
		tickets, err := s.client.Ticket.Query().
			Where(
				ticket.TenantIDEQ(tenantID),
				ticket.SLADefinitionID(sla.ID),
			).
			All(ctx)
		if err != nil {
			continue
		}

		total := len(tickets)
		if total == 0 {
			continue
		}

		violated := 0
		for _, item := range tickets {
			p := projectSLACycle(item, time.Now())
			if p.ResponseBreached || p.ResolutionBreached {
				violated++
			}
		}

		stats = append(stats, &SLAComplianceStat{
			SLADefinitionID:   sla.ID,
			SLADefinitionName: sla.Name,
			TotalTickets:      total,
			ViolatedTickets:   violated,
			ComplianceRate:    float64(total-violated) / float64(total) * 100,
		})
	}

	return stats, nil
}

type SLAComplianceStat struct {
	SLADefinitionID   int     `json:"slaDefinitionId"`
	SLADefinitionName string  `json:"slaDefinitionName"`
	TotalTickets      int     `json:"totalTickets"`
	ViolatedTickets   int     `json:"violatedTickets"`
	ComplianceRate    float64 `json:"complianceRate"`
}

// GetDashboardMetrics 获取SLA监控仪表板完整指标
func (s *SLAMonitorService) GetDashboardMetrics(ctx context.Context, tenantID int) (*dto.SLAMonitoringDashboard, error) {
	s.logger.Infow("Getting SLA dashboard metrics", "tenant_id", tenantID)

	now := time.Now()
	dashboard := &dto.SLAMonitoringDashboard{
		UpcomingDeadlines: make([]dto.SLADeadline, 0),
		TopViolations:     make([]dto.SLAViolationItem, 0),
		SLAByPriority:     make(map[string]float64),
		TrendData:         make([]dto.SLATrendPoint, 0),
	}

	// 获取所有活跃工单（带有SLA定义的）
	tickets, err := s.client.Ticket.Query().
		Where(
			ticket.TenantIDEQ(tenantID),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets: %w", err)
	}

	dashboard.TotalTickets = len(tickets)

	// 遍历工单进行分类统计
	atRiskCount := 0
	breachedCount := 0
	priorityMap := make(map[string]int)
	priorityViolationMap := make(map[string]int)

	for _, t := range tickets {
		projection := projectSLACycle(t, now)
		hasViolation := projection.ResponseBreached || projection.ResolutionBreached

		// 按优先级统计
		priority := t.Priority
		if priority == "" {
			priority = "unknown"
		}
		priorityMap[priority]++

		if hasViolation {
			breachedCount++
			priorityViolationMap[priority]++
		} else if projection.SLAStatus == "warning" {
			atRiskCount++
		}
	}

	dashboard.AtRiskTickets = atRiskCount
	dashboard.BreachedTickets = breachedCount

	// 计算合规率和违规率
	if dashboard.TotalTickets > 0 {
		compliantCount := dashboard.TotalTickets - breachedCount
		dashboard.ComplianceRate = float64(compliantCount) / float64(dashboard.TotalTickets) * 100
		dashboard.ViolationRate = float64(breachedCount) / float64(dashboard.TotalTickets) * 100
	}

	// 按优先级计算SLA达成率
	for priority, total := range priorityMap {
		violated := priorityViolationMap[priority]
		if total > 0 {
			dashboard.SLAByPriority[priority] = float64(total-violated) / float64(total) * 100
		}
	}

	// 获取即将到期的工单（未来24小时内）
	upcomingDeadline := now.Add(24 * time.Hour)
	upcomingTickets, err := s.client.Ticket.Query().
		Where(
			ticket.TenantIDEQ(tenantID),
			ticket.ResolvedAtIsNil(),
			ticket.ClosedAtIsNil(),
			ticket.DeletedAtIsNil(),
			ticket.SLAResolutionDeadlineGT(now),
			ticket.SLAResolutionDeadlineLT(upcomingDeadline),
		).
		All(ctx)
	if err == nil {
		for _, t := range upcomingTickets {
			timeLeft := time.Until(t.SLAResolutionDeadline)
			timeLeftStr := formatDuration(timeLeft)

			slaName := "Default SLA"
			if t.SLADefinitionID != 0 {
				slaDef, err := s.client.SLADefinition.Get(ctx, t.SLADefinitionID)
				if err == nil && slaDef != nil {
					slaName = slaDef.Name
				}
			}

			dashboard.UpcomingDeadlines = append(dashboard.UpcomingDeadlines, dto.SLADeadline{
				TicketID:    t.ID,
				TicketTitle: t.Title,
				Deadline:    t.SLAResolutionDeadline,
				SLAName:     slaName,
				TimeLeft:    timeLeftStr,
			})
		}
	}

	// 获取最新的违规记录作为Top Violations
	recentViolations, err := s.client.SLAViolation.Query().
		Where(
			slaviolation.TenantIDEQ(tenantID),
			slaviolation.ResolvedAtIsNil(),
		).
		Order(ent.Desc(slaviolation.FieldViolationTime)).
		Limit(10).
		All(ctx)
	if err == nil {
		for _, v := range recentViolations {
			ticket, err := s.client.Ticket.Get(ctx, v.TicketID)
			ticketTitle := fmt.Sprintf("Ticket #%d", v.TicketID)
			if err == nil && ticket != nil {
				ticketTitle = ticket.Title
			}

			// 计算延迟分钟数
			delayMinutes := 0
			if !v.ViolationTime.IsZero() {
				delayMinutes = int(time.Since(v.ViolationTime).Minutes())
			}

			dashboard.TopViolations = append(dashboard.TopViolations, dto.SLAViolationItem{
				TicketID:    v.TicketID,
				TicketTitle: ticketTitle,
				SLAName:     v.SLAName,
				ViolatedAt:  v.ViolationTime.Format(time.RFC3339),
				Delay:       delayMinutes,
			})
		}
	}

	// 生成趋势数据（最近7天）
	for i := 6; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		startOfDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
		endOfDay := startOfDay.Add(24 * time.Hour)

		dayTickets, err := s.client.Ticket.Query().
			Where(
				ticket.TenantIDEQ(tenantID),
				ticket.CreatedAtGTE(startOfDay),
				ticket.CreatedAtLT(endOfDay),
			).
			All(ctx)
		if err != nil {
			continue
		}

		dayViolations, _ := s.client.SLAViolation.Query().
			Where(
				slaviolation.TenantIDEQ(tenantID),
				slaviolation.ViolationTimeGTE(startOfDay),
				slaviolation.ViolationTimeLT(endOfDay),
			).
			Count(ctx)

		ticketCount := len(dayTickets)
		complianceRate := 100.0
		if ticketCount > 0 {
			compliant := ticketCount - dayViolations
			complianceRate = float64(compliant) / float64(ticketCount) * 100
		}

		dashboard.TrendData = append(dashboard.TrendData, dto.SLATrendPoint{
			Date:           startOfDay.Format("2006-01-02"),
			ComplianceRate: complianceRate,
			TicketCount:    ticketCount,
		})
	}

	s.logger.Infow("SLA dashboard metrics generated",
		"tenant_id", tenantID,
		"total_tickets", dashboard.TotalTickets,
		"compliance_rate", dashboard.ComplianceRate,
		"breached_tickets", dashboard.BreachedTickets)

	return dashboard, nil
}

// formatDuration 格式化时间间隔为可读字符串
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "overdue"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
