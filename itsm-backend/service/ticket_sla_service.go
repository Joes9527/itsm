package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/slacontract"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// TicketSLAServiceInterface 工单SLA服务接口
type TicketSLAServiceInterface interface {
	// GetTicketSLAInfo 获取工单SLA信息
	GetTicketSLAInfo(ctx context.Context, ticketID int, tenantID int) (*TicketSLAInfoResult, error)
	// GetOverdueTickets 获取逾期工单
	GetOverdueTickets(ctx context.Context, tenantID int) ([]*ent.Ticket, error)
	// GetTicketStats 获取工单统计
	GetTicketStats(ctx context.Context, tenantID int) (*TicketStats, error)
	// CalculateSLADeadline 计算SLA截止时间。categoryID>=1 时优先按分类匹配。
	CalculateSLADeadline(ctx context.Context, tenantID int, ticketType, priority string, categoryID int) (*SLADeadlineResult, error)
	// CalculateSLADeadlineFromRequest 根据请求参数计算SLA截止时间（包含SLADefinitionID）。
	// categoryID 可选：传入 >0 时优先按分类匹配 SLA 定义。
	CalculateSLADeadlineFromRequest(ctx context.Context, tenantID int, ticketType, priority string, categoryID int) (*SLADeadlineResult, error)
	// AdjustToBusinessHours 调整时间到工作时间
	AdjustToBusinessHours(t time.Time) time.Time
}

// TicketSLAInfoResult 工单SLA信息（计算结果）
type TicketSLAInfoResult struct {
	CycleNumber        int                  `json:"cycleNumber"`
	CycleStartedAt     *time.Time           `json:"cycleStartedAt"`
	PausedMinutes      int                  `json:"pausedMinutes"`
	AppliedPolicy      *slacontract.Policy  `json:"appliedPolicy"`
	History            []dto.SLACycleResult `json:"history"`
	TicketID           int                  `json:"ticketId"`
	TicketNumber       string               `json:"ticketNumber"`
	Priority           string               `json:"priority"`
	TicketType         string               `json:"ticketType"`
	ResponseDeadline   *time.Time           `json:"responseDeadline"`
	ResolutionDeadline *time.Time           `json:"resolutionDeadline"`
	ResponseTimeUsed   int                  `json:"responseTimeUsed"`   // 分钟
	ResolutionTimeUsed int                  `json:"resolutionTimeUsed"` // 分钟
	ResponseBreached   bool                 `json:"responseBreached"`
	ResolutionBreached bool                 `json:"resolutionBreached"`
	SLAStatus          string               `json:"slaStatus"` // ok, warning, breached
}

// SLADeadlineResult SLA截止时间计算结果
type SLADeadlineResult struct {
	SLADefinitionID    int
	ResponseDeadline   *time.Time
	ResolutionDeadline *time.Time
	BusinessHoursOnly  bool
}

// TicketStats 工单统计
type TicketStats struct {
	TotalTickets      int `json:"totalTickets"`
	OpenTickets       int `json:"openTickets"`
	InProgressTickets int `json:"inProgressTickets"`
	ResolvedTickets   int `json:"resolvedTickets"`
	ClosedTickets     int `json:"closedTickets"`
	OverdueTickets    int `json:"overdueTickets"`
	BreachedTickets   int `json:"breachedTickets"`
}

// TicketSLAService 工单SLA服务
type TicketSLAService struct {
	directory database.DirectorySnapshot
	client    *ent.Client
	logger    *zap.SugaredLogger
}

// NewTicketSLAService 创建工单SLA服务
func NewTicketSLAService(client *ent.Client, logger *zap.SugaredLogger) *TicketSLAService {
	return &TicketSLAService{
		client: client,
		logger: logger,
	}
}

// GetTicketSLAInfo 获取工单SLA信息
func (s *TicketSLAService) GetTicketSLAInfo(ctx context.Context, ticketID int, tenantID int) (*TicketSLAInfoResult, error) {
	// 查询工单
	t, err := s.client.Ticket.Query().
		Where(ticket.IDEQ(ticketID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		s.logger.Errorw("Failed to find ticket", "ticketID", ticketID, "error", err)
		return nil, err
	}

	result := projectSLACycle(t, time.Now())
	result.TicketType = common.WorkItemLegacyType(t.RecordClass, t.GenericSubtype)
	result.History, err = s.cycleHistory(ctx, t)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOverdueTickets 获取逾期工单
func (s *TicketSLAService) GetOverdueTickets(ctx context.Context, tenantID int) ([]*ent.Ticket, error) {
	// Persisted deadlines already include pauses; keep the active deadline filter
	// in SQL rather than scanning every unresolved WorkItem in the tenant.
	items, err := s.client.Ticket.Query().Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.ResolvedAtIsNil(), ticket.ClosedAtIsNil(), ticket.SLAResolutionDeadlineLT(time.Now())).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*ent.Ticket, 0)
	for _, item := range items {
		if projectSLACycle(item, time.Now()).ResolutionBreached {
			result = append(result, item)
		}
	}
	return result, nil
}

// GetTicketStats 获取工单统计
func (s *TicketSLAService) GetTicketStats(ctx context.Context, tenantID int) (*TicketStats, error) {
	stats := &TicketStats{}

	// 统计总数
	total, err := s.client.Ticket.Query().
		Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.TotalTickets = total

	// 统计各状态数量
	openCount, err := s.client.Ticket.Query().
		Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.Status(common.TicketStatusOpen)).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.OpenTickets = openCount

	inProgressCount, err := s.client.Ticket.Query().
		Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.Status(common.TicketStatusInProgress)).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.InProgressTickets = inProgressCount

	resolvedCount, err := s.client.Ticket.Query().
		Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.Status(common.TicketStatusResolved)).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.ResolvedTickets = resolvedCount

	closedCount, err := s.client.Ticket.Query().
		Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.Status(common.TicketStatusClosed)).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.ClosedTickets = closedCount

	// 统计逾期工单
	overdueTickets, err := s.GetOverdueTickets(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	stats.OverdueTickets = len(overdueTickets)

	items, err := s.client.Ticket.Query().Where(ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		p := projectSLACycle(item, time.Now())
		if p.ResponseBreached || p.ResolutionBreached {
			stats.BreachedTickets++
		}
	}

	return stats, nil
}

// CalculateSLADeadline 计算SLA截止时间。categoryID>=1 时优先按分类匹配。
func (s *TicketSLAService) CalculateSLADeadline(ctx context.Context, tenantID int, ticketType, priority string, categoryID int) (*SLADeadlineResult, error) {
	slaDef, err := s.getSLADefinition(ctx, tenantID, ticketType, priority, categoryID)
	if err != nil {
		return nil, err
	}

	result := &SLADeadlineResult{SLADefinitionID: slaDef.ID}

	now := time.Now()

	if slaDef.ResponseTime > 0 {
		respDeadline, err := s.calculateDeadlineWithBusinessHours(now, slaDef.ResponseTime, slaDef.BusinessHours)
		if err != nil {
			return nil, err
		}
		result.ResponseDeadline = &respDeadline
	}

	if slaDef.ResolutionTime > 0 {
		resDeadline, err := s.calculateDeadlineWithBusinessHours(now, slaDef.ResolutionTime, slaDef.BusinessHours)
		if err != nil {
			return nil, err
		}
		result.ResolutionDeadline = &resDeadline
	}

	return result, nil
}

// getSLADefinition 获取SLA定义。匹配优先级：category_id > type+priority > type-only > default。
func (s *TicketSLAService) getSLADefinition(ctx context.Context, tenantID int, ticketType, priority string, categoryID int) (*ent.SLADefinition, error) {
	// 1) 分类优先：必须扫描**全部**活跃候选并按确定性顺序判断分类命中。
	// 既有实现只取一条无排序的活跃定义再判断它是否含该分类，会漏掉真正持有该分类的定义
	// 并静默退化到通用 SLA；这里改为完整扫描 + 显式顺序（id 升序，先建先得）。
	if categoryID > 0 {
		path, err := NewTicketCategoryService(s.client).GetCategoryPath(ctx, tenantID, categoryID)
		if err != nil {
			// 分类存在却解析不出路径：必须报错，不得静默用通用 SLA 顶替。
			return nil, fmt.Errorf("resolve SLA classification path: %w", err)
		}
		candidates, err := s.client.SLADefinition.Query().
			Where(sladefinition.TenantIDEQ(tenantID), sladefinition.IsActiveEQ(true)).
			Order(ent.Asc(sladefinition.FieldID)).
			All(ctx)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			matched, err := categoryMatchesPath(candidate.CategoryIds, path)
			if err != nil {
				return nil, err
			}
			if matched {
				return candidate, nil
			}
		}
	}

	// 2) 按 type + priority 精确匹配
	sla, err := s.matchSLA(ctx, tenantID, func(q *ent.SLADefinitionQuery) {
		q.Where(
			sladefinition.ServiceType(ticketType),
			sladefinition.Priority(priority),
			sladefinition.IsActive(true),
		)
	})
	if err == nil && sla != nil {
		return sla, nil
	}

	// 3) 按 type-only 匹配
	sla, err = s.matchSLA(ctx, tenantID, func(q *ent.SLADefinitionQuery) {
		q.Where(
			sladefinition.ServiceType(ticketType),
			sladefinition.IsActive(true),
		)
	})
	if err == nil && sla != nil {
		return sla, nil
	}

	// 4) 默认兜底
	return s.defaultSLADefinition(), nil
}

// matchSLA 查找租户下第一个匹配的 SLA 定义
func (s *TicketSLAService) matchSLA(ctx context.Context, tenantID int, apply func(q *ent.SLADefinitionQuery)) (*ent.SLADefinition, error) {
	q := s.client.SLADefinition.Query().Where(sladefinition.TenantIDEQ(tenantID))
	apply(q)
	return q.First(ctx)
}

// categoryMatchesPath 判断 SLA 的 category_ids 是否命中工单分类。
//
// 保持既有精确语义（只比较工单的最深节点），经由共享的 MatchCTI 实现，
// 避免 SLA 与规则使用两套分类匹配逻辑。未分类工单不命中，且不是错误。
func categoryMatchesPath(categoryIDs []int, path []CTINode) (bool, error) {
	if len(categoryIDs) == 0 || len(path) == 0 {
		return false, nil
	}
	for _, id := range categoryIDs {
		matched, err := MatchCTI(path, id, CTIExact)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// defaultSLADefinition 返回一个内联默认 SLA 定义
func (s *TicketSLAService) defaultSLADefinition() *ent.SLADefinition {
	return &ent.SLADefinition{ID: 0, Name: "默认SLA", ResponseTime: 480, ResolutionTime: 1440}
}

// calculateDeadlineWithBusinessHours applies an SLA definition's configured
// business calendar. An empty calendar intentionally preserves 24x7 SLA time.
func (s *TicketSLAService) calculateDeadlineWithBusinessHours(startTime time.Time, durationMinutes int, businessHours map[string]interface{}) (time.Time, error) {
	if durationMinutes < 0 || int64(durationMinutes) > int64(^uint64(0)>>1)/int64(time.Minute) {
		return time.Time{}, fmt.Errorf("SLA duration is out of range")
	}
	if len(businessHours) == 0 {
		return startTime.Add(time.Duration(durationMinutes) * time.Minute), nil
	}
	cfg, err := parseBusinessHoursConfig(businessHours)
	if err != nil {
		return time.Time{}, err
	}
	return addBusinessMinutes(startTime, durationMinutes, cfg)
}

// AdjustToBusinessHours 调整到工作时间（公开方法，供外部调用）。
// 阻断7 说明：此方法保留向后兼容，仅用于"把某个时刻对齐到最近的工作时段起点"，
// 不能用于计算 SLA 截止时间（截止时间必须用 calculateDeadline/addBusinessMinutes）。
func (s *TicketSLAService) AdjustToBusinessHours(t time.Time) time.Time {
	result, _ := adjustToBusinessHoursStart(t, defaultBusinessHoursConfig())
	return result
}

// businessHoursConfig 业务时间配置。
// 默认：周一至周五 9:00-18:00，无节假日。
type businessHoursConfig struct {
	workDays  map[time.Weekday]bool // 工作日集合
	startHour int                   // 工作时段起始小时（含），9 表示 09:00
	startMin  int                   // 工作时段起始分钟
	endHour   int                   // 工作时段结束小时（不含），18 表示 18:00
	endMin    int                   // 工作时段结束分钟
	holidays  map[string]bool       // 节假日集合，格式 "2006-01-02"
}

// defaultBusinessHoursConfig 返回默认业务时间配置（周一至周五 9:00-18:00）。
func defaultBusinessHoursConfig() businessHoursConfig {
	return businessHoursConfig{
		workDays: map[time.Weekday]bool{
			time.Monday: true, time.Tuesday: true, time.Wednesday: true,
			time.Thursday: true, time.Friday: true,
		},
		startHour: 9,
		endHour:   18,
		holidays:  map[string]bool{},
	}
}

// parseBusinessHoursConfig 从 SLADefinition.BusinessHours (map[string]interface{}) 解析配置。
// 配置格式参考 ent/schema/sla_policy.go 的 BusinessHoursConfig：
//
//	{ "work_days": [1,2,3,4,5], "start_time": "09:00", "end_time": "18:00",
//	  "time_zone": "Asia/Shanghai", "holiday_list": ["2026-01-01"] }
//
// Missing calendar attributes use documented defaults; invalid declarations fail closed.
func parseBusinessHoursConfig(raw map[string]interface{}) (businessHoursConfig, error) {
	cfg := defaultBusinessHoursConfig()
	if value, exists := raw["work_days"]; exists {
		days, ok := value.([]interface{})
		if !ok || len(days) == 0 {
			return cfg, fmt.Errorf("SLA work_days must be a nonempty array")
		}
		cfg.workDays = map[time.Weekday]bool{}
		for _, day := range days {
			n, err := slaConfigurationInteger(day, 1, 7)
			if err != nil {
				return cfg, fmt.Errorf("invalid SLA work day: %w", err)
			}
			cfg.workDays[time.Weekday(n%7)] = true
		}
	}
	parseHM := func(key string, hour, minute *int) error {
		rawValue, exists := raw[key]
		if !exists {
			return nil
		}
		value, ok := rawValue.(string)
		if !ok {
			return fmt.Errorf("invalid SLA %s", key)
		}
		parsed, err := time.Parse("15:04", value)
		if err != nil {
			return fmt.Errorf("invalid SLA %s", key)
		}
		*hour, *minute = parsed.Hour(), parsed.Minute()
		return nil
	}
	if err := parseHM("start_time", &cfg.startHour, &cfg.startMin); err != nil {
		return cfg, err
	}
	if err := parseHM("end_time", &cfg.endHour, &cfg.endMin); err != nil {
		return cfg, err
	}
	if cfg.endHour*60+cfg.endMin <= cfg.startHour*60+cfg.startMin {
		return cfg, fmt.Errorf("SLA work period must end after it starts")
	}
	if value, exists := raw["holiday_list"]; exists {
		holidays, ok := value.([]interface{})
		if !ok {
			return cfg, fmt.Errorf("invalid SLA holiday_list")
		}
		for _, value := range holidays {
			day, ok := value.(string)
			if !ok {
				return cfg, fmt.Errorf("invalid SLA holiday")
			}
			if _, err := time.Parse("2006-01-02", day); err != nil {
				return cfg, fmt.Errorf("invalid SLA holiday")
			}
			cfg.holidays[day] = true
		}
	}
	return cfg, nil
}

// isHoliday 判断给定日期是否为节假日。
func (c businessHoursConfig) isHoliday(t time.Time) bool {
	return c.holidays[t.Format("2006-01-02")]
}

// isWorkDay 判断给定日期是否为工作日（工作日集合 + 非节假日）。
func (c businessHoursConfig) isWorkDay(t time.Time) bool {
	if c.isHoliday(t) {
		return false
	}
	return c.workDays[t.Weekday()]
}

// workDayStart 返回 t 所在工作日的工时开始时刻。
func (c businessHoursConfig) workDayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, c.startHour, c.startMin, 0, 0, t.Location())
}

// workDayEnd 返回 t 所在工作日的工时结束时刻。
func (c businessHoursConfig) workDayEnd(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, c.endHour, c.endMin, 0, 0, t.Location())
}

// nextWorkDayStart 返回 t 之后下一个工作日的工时开始时刻。
func (c businessHoursConfig) nextWorkDayStart(t time.Time) (time.Time, error) {
	if len(c.workDays) == 0 {
		return time.Time{}, fmt.Errorf("SLA has no working days")
	}
	// Each holiday can exclude at most one candidate date. Seven days per
	// holiday plus one complete week bounds the search even for sparse calendars.
	for i := 1; i <= 7*(len(c.holidays)+1); i++ {
		next := t.AddDate(0, 0, i)
		if c.isWorkDay(next) {
			return c.workDayStart(next), nil
		}
	}
	return time.Time{}, fmt.Errorf("SLA has no reachable working day")
}

func adjustToBusinessHoursStart(t time.Time, cfg businessHoursConfig) (time.Time, error) {
	if !cfg.isWorkDay(t) {
		return cfg.nextWorkDayStart(t)
	}
	if t.Before(cfg.workDayStart(t)) {
		return cfg.workDayStart(t), nil
	}
	if !t.Before(cfg.workDayEnd(t)) {
		return cfg.nextWorkDayStart(t)
	}
	return t, nil
}

func addBusinessMinutes(start time.Time, minutes int, cfg businessHoursConfig) (time.Time, error) {
	if minutes == 0 {
		return start, nil
	}
	cursor, err := adjustToBusinessHoursStart(start, cfg)
	if err != nil {
		return time.Time{}, err
	}
	remaining := time.Duration(minutes) * time.Minute
	for i := 0; i < 366 && remaining > 0; i++ {
		available := cfg.workDayEnd(cursor).Sub(cursor)
		if available <= 0 {
			return time.Time{}, fmt.Errorf("invalid SLA work period")
		}
		if remaining <= available {
			return cursor.Add(remaining), nil
		}
		remaining -= available
		cursor, err = cfg.nextWorkDayStart(cursor)
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, fmt.Errorf("SLA business duration exceeds supported calendar span")
}

// CalculateSLADeadlineFromRequest 根据请求参数计算SLA截止时间（包含SLADefinitionID）
func (s *TicketSLAService) CalculateSLADeadlineFromRequest(ctx context.Context, tenantID int, ticketType, priority string, categoryID int) (*SLADeadlineResult, error) {
	now := time.Now()
	serviceType := mapTicketTypeToServiceType(ticketType)
	normalizedPriority := strings.ToLower(priority)
	if normalizedPriority == "urgent" {
		normalizedPriority = "critical"
	}
	slaDef, err := s.getSLADefinition(ctx, tenantID, serviceType, normalizedPriority, categoryID)
	if err != nil || slaDef == nil {
		s.logger.Warnw("No SLA definition found, using defaults", "service_type", serviceType, "priority", normalizedPriority)
		return &SLADeadlineResult{
			SLADefinitionID:    0,
			ResponseDeadline:   toPointer(now.Add(8 * time.Hour)),
			ResolutionDeadline: toPointer(now.Add(24 * time.Hour)),
		}, nil
	}
	businessHoursOnly := len(slaDef.BusinessHours) > 0
	responseDeadline, err := s.calculateDeadlineWithBusinessHours(now, slaDef.ResponseTime, slaDef.BusinessHours)
	if err != nil {
		return nil, err
	}
	resolutionDeadline, err := s.calculateDeadlineWithBusinessHours(now, slaDef.ResolutionTime, slaDef.BusinessHours)
	if err != nil {
		return nil, err
	}
	return &SLADeadlineResult{
		SLADefinitionID:    slaDef.ID,
		ResponseDeadline:   &responseDeadline,
		ResolutionDeadline: &resolutionDeadline,
		BusinessHoursOnly:  businessHoursOnly,
	}, nil
}

func mapTicketTypeToServiceType(ticketType string) string {
	switch ticketType {
	case "incident", "service_request", "change":
		return ticketType
	default:
		return "incident"
	}
}

// toPointer 返回指针（辅助函数）
func toPointer[T any](v T) *T {
	return &v
}

func (s *TicketSLAService) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	s.directory = directory
}
