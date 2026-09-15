package controller

import (
	"net/http"
	"strconv"
	"time"

	"itsm-backend/service"

	"github.com/gin-gonic/gin"
)

// BPMNMonitoringController BPMN监控控制器
type BPMNMonitoringController struct {
	monitoringService *service.BPMNMonitoringService
}

// NewBPMNMonitoringController 创建BPMN监控控制器
func NewBPMNMonitoringController(monitoringService *service.BPMNMonitoringService) *BPMNMonitoringController {
	return &BPMNMonitoringController{
		monitoringService: monitoringService,
	}
}

// SetMonitoringService 设置监控服务（用于延迟注入）
func (c *BPMNMonitoringController) SetMonitoringService(s *service.BPMNMonitoringService) {
	c.monitoringService = s
}

// RegisterRoutes 注册路由
func (c *BPMNMonitoringController) RegisterRoutes(r *gin.RouterGroup) {
	monitoring := r.Group("/bpmn/monitoring")
	{
		// 流程指标监控
		monitoring.GET("/metrics", c.GetProcessMetrics)
		monitoring.GET("/metrics/:processKey", c.GetProcessMetricsByKey)

		// 流程实例状态监控
		monitoring.GET("/instances/:instanceId/status", c.GetProcessInstanceStatus)
		monitoring.GET("/instances/status", c.ListProcessInstancesStatus)
		// 新增：完整执行轨迹时间线
		monitoring.GET("/instances/:instanceId/timeline", c.GetProcessTimeline)

		// 性能监控
		monitoring.GET("/performance", c.GetPerformanceMetrics)
		monitoring.GET("/performance/alerts", c.GetPerformanceAlerts)

		// 系统健康检查
		monitoring.GET("/health", c.GetSystemHealth)

		// 审计日志
		monitoring.GET("/audit-logs", c.GetAuditLogs)
	}
}

// GetProcessMetrics 获取流程指标
func (c *BPMNMonitoringController) GetProcessMetrics(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	// 解析查询参数
	timeRange := ctx.Query("time_range")
	if timeRange == "" {
		timeRange = "24h" // 默认24小时
	}

	req := &service.ProcessMetricsRequest{
		TimeRange: timeRange,
	}

	// 解析时间范围
	if startTimeStr := ctx.Query("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			req.StartTime = &startTime
		}
	}

	if endTimeStr := ctx.Query("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			req.EndTime = &endTime
		}
	}

	metrics, err := c.monitoringService.GetProcessMetrics(requestCtx, req)
	if err != nil {
		respondBPMNError(ctx, err, "获取流程指标失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取流程指标成功",
		"data":    metrics,
	})
}

// GetProcessMetricsByKey 根据流程定义键获取指标
func (c *BPMNMonitoringController) GetProcessMetricsByKey(ctx *gin.Context) {
	processKey := ctx.Param("processKey")
	if processKey == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "流程定义键不能为空"})
		return
	}

	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	// 解析查询参数
	timeRange := ctx.Query("time_range")
	if timeRange == "" {
		timeRange = "24h"
	}

	req := &service.ProcessMetricsRequest{
		ProcessDefinitionKey: processKey,
		TimeRange:            timeRange,
	}

	// 解析时间范围
	if startTimeStr := ctx.Query("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			req.StartTime = &startTime
		}
	}

	if endTimeStr := ctx.Query("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			req.EndTime = &endTime
		}
	}

	metrics, err := c.monitoringService.GetProcessMetrics(requestCtx, req)
	if err != nil {
		respondBPMNError(ctx, err, "获取流程指标失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取流程指标成功",
		"data":    metrics,
	})
}

// GetProcessInstanceStatus 获取流程实例状态
func (c *BPMNMonitoringController) GetProcessInstanceStatus(ctx *gin.Context) {
	instanceIDStr := ctx.Param("instanceId")
	instanceID, err := strconv.Atoi(instanceIDStr)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的流程实例ID"})
		return
	}

	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	status, err := c.monitoringService.GetProcessInstanceStatus(requestCtx, instanceID)
	if err != nil {
		respondBPMNError(ctx, err, "获取流程实例状态失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取流程实例状态成功",
		"data":    status,
	})
}

// ListProcessInstancesStatus 获取流程实例状态列表
func (c *BPMNMonitoringController) ListProcessInstancesStatus(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	// 解析分页参数
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))

	// 解析查询参数
	processKey := ctx.Query("process_key")
	status := ctx.Query("status")
	assignee := ctx.Query("assignee")

	query := &service.ListProcessInstanceStatusQuery{
		Page:       page,
		PageSize:   pageSize,
		ProcessKey: processKey,
		Status:     status,
		Assignee:   assignee,
	}

	// 解析时间范围
	if startTimeStr := ctx.Query("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			query.StartTime = &startTime
		}
	}
	if endTimeStr := ctx.Query("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			query.EndTime = &endTime
		}
	}

	statuses, total, err := c.monitoringService.ListProcessInstancesStatus(requestCtx, query)
	if err != nil {
		respondBPMNError(ctx, err, "获取流程实例状态失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取流程实例状态成功",
		"data": gin.H{
			"instances": statuses,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetProcessTimeline 获取流程实例完整时间线
func (c *BPMNMonitoringController) GetProcessTimeline(ctx *gin.Context) {
	processInstanceKey := ctx.Param("instanceId")
	if processInstanceKey == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "流程实例Key不能为空"})
		return
	}
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	entries, err := c.monitoringService.GetProcessTimeline(requestCtx, processInstanceKey)
	if err != nil {
		respondBPMNError(ctx, err, "获取流程时间线失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取流程时间线成功",
		"data": gin.H{
			"process_instance_id": processInstanceKey,
			"entries":             entries,
			"total":               len(entries),
		},
	})
}

// GetPerformanceMetrics 获取性能指标
func (c *BPMNMonitoringController) GetPerformanceMetrics(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	// 解析时间范围
	timeRange := ctx.DefaultQuery("time_range", "24h")

	req := &service.ProcessMetricsRequest{
		TimeRange: timeRange,
	}

	metrics, err := c.monitoringService.GetProcessMetrics(requestCtx, req)
	if err != nil {
		respondBPMNError(ctx, err, "获取性能指标失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取性能指标成功",
		"data":    metrics.PerformanceMetrics,
	})
}

// GetPerformanceAlerts 获取性能告警
func (c *BPMNMonitoringController) GetPerformanceAlerts(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	alerts, err := c.monitoringService.GetPerformanceAlerts(requestCtx)
	if err != nil {
		respondBPMNError(ctx, err, "获取性能告警失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取性能告警成功",
		"data":    alerts,
	})
}

// GetSystemHealth 获取系统健康状态
func (c *BPMNMonitoringController) GetSystemHealth(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	health, err := c.monitoringService.GetSystemHealth(requestCtx)
	if err != nil {
		respondBPMNError(ctx, err, "获取系统健康状态失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取系统健康状态成功",
		"data":    health,
	})
}

// GetAuditLogs 获取审计日志
func (c *BPMNMonitoringController) GetAuditLogs(ctx *gin.Context) {
	requestCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	// 解析查询参数
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	userID := ctx.Query("user_id")
	action := ctx.Query("action")
	resourceType := ctx.Query("resource_type")
	resourceID := ctx.Query("resource_id")

	req := &service.AuditLogRequest{
		Page:         page,
		PageSize:     pageSize,
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}

	// 解析时间范围
	if startTimeStr := ctx.Query("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			req.StartTime = &startTime
		}
	}

	if endTimeStr := ctx.Query("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			req.EndTime = &endTime
		}
	}

	logs, total, err := c.monitoringService.GetAuditLogs(requestCtx, req)
	if err != nil {
		respondBPMNError(ctx, err, "获取审计日志失败")
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "获取审计日志成功",
		"data": gin.H{
			"logs":      logs,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}
