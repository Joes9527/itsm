package service

import (
	"context"
	"fmt"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/slacontract"
	"strings"
	"time"

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
	// 1) 按分类ID精确匹配
	if categoryID > 0 {
		sla, err := s.matchSLA(ctx, tenantID, func(q *ent.SLADefinitionQuery) {
			q.Where(sladefinition.IsActive(true))
		})
		if err == nil && sla != nil && s.categoryMatches(sla, categoryID) {
			return sla, nil
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

// categoryMatches 检查 SLA 的 category_ids 是否包含目标分类
func (s *TicketSLAService) categoryMatches(sla *ent.SLADefinition, categoryID int) bool {
	for _, id := range sla.CategoryIds {
		if id == categoryID {
			return true
		}
	}
	return false
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
	workDays   map[time.Weekday]bool // 工作日集合
	startHour  int                   // 工作时段起始小时（含），9 表示 09:00
	startMin   int                   // 工作时段起始分钟
	endHour    int                   // 工作时段结束小时（不含），18 表示 18:00
	endMin     int                   // 工作时段结束分钟
	holidays   map[string]bool       // 节假日集合，格式 "2006-01-02"
	makeupDays map[string]bool       // 指定日期补班，覆盖每周工作日规则
	location   *time.Location        // nil preserves the input location for undeclared legacy calendars
	validFrom  string                // optional inclusive local-date coverage; both bounds must be declared
	validUntil string
}

// defaultBusinessHoursConfig 返回默认业务时间配置（周一至周五 9:00-18:00）。
func defaultBusinessHoursConfig() businessHoursConfig {
	return businessHoursConfig{
		workDays: map[time.Weekday]bool{
			time.Monday: true, time.Tuesday: true, time.Wednesday: true,
			time.Thursday: true, time.Friday: true,
		},
		startHour:  9,
		endHour:    18,
		holidays:   map[string]bool{},
		makeupDays: map[string]bool{},
	}
}

// parseBusinessHoursConfig 从 SLADefinition.BusinessHours (map[string]interface{}) 解析配置。
// 配置存储于 SLADefinition.BusinessHours：
//
//	{ "work_days": [1,2,3,4,5], "start_time": "09:00", "end_time": "18:00",
//	  "time_zone": "Asia/Shanghai", "holiday_list": ["2026-01-01"],
//	  "makeup_days": ["2026-01-04"], "valid_from": "2026-01-01", "valid_until": "2026-12-31" }
//
// Missing calendar attributes use documented defaults; invalid declarations fail closed.
func parseBusinessHoursConfig(raw map[string]interface{}) (businessHoursConfig, error) {
	cfg := defaultBusinessHoursConfig()
	if value, exists := raw["time_zone"]; exists {
		name, ok := value.(string)
		if !ok || name == "" || name == "Local" {
			return cfg, fmt.Errorf("SLA time_zone must name an explicit IANA location")
		}
		location, err := time.LoadLocation(name)
		if err != nil {
			return cfg, fmt.Errorf("invalid SLA time_zone: %w", err)
		}
		cfg.location = location
	}
	from, hasFrom := raw["valid_from"]
	until, hasUntil := raw["valid_until"]
	if hasFrom != hasUntil {
		return cfg, fmt.Errorf("SLA calendar coverage requires valid_from and valid_until")
	}
	if hasFrom {
		for _, field := range []struct {
			value  interface{}
			target *string
		}{{from, &cfg.validFrom}, {until, &cfg.validUntil}} {
			date, ok := field.value.(string)
			if !ok {
				return cfg, fmt.Errorf("invalid SLA calendar coverage date")
			}
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return cfg, fmt.Errorf("invalid SLA calendar coverage date")
			}
			*field.target = date
		}
		if cfg.validFrom > cfg.validUntil {
			return cfg, fmt.Errorf("SLA calendar coverage ends before it starts")
		}
	}
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
	for key, dates := range map[string]map[string]bool{"holiday_list": cfg.holidays, "makeup_days": cfg.makeupDays} {
		value, exists := raw[key]
		if !exists {
			continue
		}
		declarations, ok := value.([]interface{})
		if !ok {
			return cfg, fmt.Errorf("invalid SLA %s", key)
		}
		for _, value := range declarations {
			day, ok := value.(string)
			if !ok {
				return cfg, fmt.Errorf("invalid SLA %s date", key)
			}
			if _, err := time.Parse("2006-01-02", day); err != nil {
				return cfg, fmt.Errorf("invalid SLA %s date", key)
			}
			if cfg.validFrom != "" && (day < cfg.validFrom || day > cfg.validUntil) {
				return cfg, fmt.Errorf("SLA %s date is outside calendar coverage", key)
			}
			dates[day] = true
		}
	}
	for day := range cfg.makeupDays {
		if cfg.holidays[day] {
			return cfg, fmt.Errorf("SLA date cannot be both a holiday and a makeup day")
		}
	}
	return cfg, nil
}

func (c businessHoursConfig) calendarTime(t time.Time) time.Time {
	if c.location != nil {
		return t.In(c.location)
	}
	return t
}

func (c businessHoursConfig) checkCoverage(t time.Time) error {
	date := c.calendarTime(t).Format("2006-01-02")
	if c.validFrom != "" && (date < c.validFrom || date > c.validUntil) {
		return fmt.Errorf("SLA date %s is outside calendar coverage %s..%s", date, c.validFrom, c.validUntil)
	}
	return nil
}

// isHoliday 判断给定日期是否为节假日。
func (c businessHoursConfig) isHoliday(t time.Time) bool {
	return c.holidays[c.calendarTime(t).Format("2006-01-02")]
}

// isWorkDay 判断给定日期是否为工作日（工作日集合 + 非节假日）。
func (c businessHoursConfig) isWorkDay(t time.Time) bool {
	t = c.calendarTime(t)
	if c.isHoliday(t) {
		return false
	}
	if c.makeupDays[t.Format("2006-01-02")] {
		return true
	}
	return c.workDays[t.Weekday()]
}

// workDayStart 返回 t 所在工作日的工时开始时刻。
func (c businessHoursConfig) workDayStart(t time.Time) time.Time {
	t = c.calendarTime(t)
	y, m, d := t.Date()
	return time.Date(y, m, d, c.startHour, c.startMin, 0, 0, t.Location())
}

// workDayEnd 返回 t 所在工作日的工时结束时刻。
func (c businessHoursConfig) workDayEnd(t time.Time) time.Time {
	t = c.calendarTime(t)
	y, m, d := t.Date()
	return time.Date(y, m, d, c.endHour, c.endMin, 0, 0, t.Location())
}

// nextWorkDayStart 返回 t 之后下一个工作日的工时开始时刻。
func (c businessHoursConfig) nextWorkDayStart(t time.Time) (time.Time, error) {
	t = c.calendarTime(t)
	if err := c.checkCoverage(t); err != nil {
		return time.Time{}, err
	}
	if len(c.workDays) == 0 {
		return time.Time{}, fmt.Errorf("SLA has no working days")
	}
	// Each holiday can exclude at most one candidate date. Seven days per
	// holiday plus one complete week bounds the search even for sparse calendars.
	for i := 1; i <= 7*(len(c.holidays)+1); i++ {
		next := t.AddDate(0, 0, i)
		if err := c.checkCoverage(next); err != nil {
			return time.Time{}, err
		}
		if c.isWorkDay(next) {
			return c.workDayStart(next), nil
		}
	}
	return time.Time{}, fmt.Errorf("SLA has no reachable working day")
}

func adjustToBusinessHoursStart(t time.Time, cfg businessHoursConfig) (time.Time, error) {
	t = cfg.calendarTime(t)
	if err := cfg.checkCoverage(t); err != nil {
		return time.Time{}, err
	}
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
	start = cfg.calendarTime(start)
	if err := cfg.checkCoverage(start); err != nil {
		return time.Time{}, err
	}
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
