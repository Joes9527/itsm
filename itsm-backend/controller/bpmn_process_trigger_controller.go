package controller

import (
	"strconv"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
)

// BPMNProcessTriggerController 流程触发控制器
type BPMNProcessTriggerController struct {
	triggerService *service.ProcessTriggerService
	bindingService *service.ProcessBindingService
	configService  *service.ConfigInheritanceService
}

// NewBPMNProcessTriggerController 创建流程触发控制器
func NewBPMNProcessTriggerController(triggerService *service.ProcessTriggerService, bindingService *service.ProcessBindingService, configService *service.ConfigInheritanceService) *BPMNProcessTriggerController {
	return &BPMNProcessTriggerController{
		triggerService: triggerService,
		bindingService: bindingService,
		configService:  configService,
	}
}

// RegisterRoutes 注册路由
func (c *BPMNProcessTriggerController) RegisterRoutes(r *gin.RouterGroup) {
	bpmnRoleGate := middleware.RequireLegacyBPMNRoles()

	// 流程触发 — matches the /api/v1/bpmn/* wildcard's current role set.
	trigger := r.Group("/process-trigger")
	trigger.Use(bpmnRoleGate)
	{
		trigger.POST("", c.TriggerProcess)
		trigger.GET("/status/:instance_id", c.GetProcessStatus)
		trigger.POST("/cancel/:instance_id", c.CancelProcess)
		trigger.POST("/suspend/:instance_id", c.SuspendProcess)
		trigger.POST("/resume/:instance_id", c.ResumeProcess)
	}

	// 流程绑定管理 — read/create match the bpmn:* wildcard's role set, but
	// update/delete are NOT covered by that wildcard today and fall back to
	// super_admin-only; the extra RequireRole("super_admin") on those two
	// routes stacks on top of bpmnRoleGate (Gin chains middleware with AND)
	// to reproduce that narrower requirement exactly.
	bindings := r.Group("/process-bindings")
	bindings.Use(bpmnRoleGate)
	{
		bindings.POST("", c.CreateBinding)
		bindings.GET("", c.QueryBindings)
		bindings.GET("/by-type/:business_type", c.GetBindingsByBusinessType)
		bindings.GET("/:id", c.GetBinding)
		bindings.PUT("/:id", middleware.RequireRole("super_admin"), c.UpdateBinding)
		bindings.DELETE("/:id", middleware.RequireRole("super_admin"), c.DeleteBinding)
	}

	// 部门流程配置 — no ResourceActionMap coverage today, super_admin only.
	departments := r.Group("/departments")
	departments.Use(middleware.RequireRole("super_admin"))
	{
		departments.GET("/:id/processes", c.GetDepartmentProcesses)
		departments.POST("/:id/init-processes", c.InitDepartmentProcesses)
	}

	// no ResourceActionMap coverage today, super_admin only.
	domainConfigs := r.Group("/domain-configs")
	domainConfigs.Use(middleware.RequireRole("super_admin"))
	{
		domainConfigs.GET("", c.ListDomainConfigs)
		domainConfigs.POST("", c.SetDomainConfig)
		domainConfigs.GET("/effective", c.GetEffectiveDomainConfig)
	}
}

// TriggerProcess 触发流程
// @Summary 触发流程
// @Description 根据业务类型和业务ID触发对应的流程
// @Tags BPMN-ProcessTrigger
// @Accept json
// @Produce json
// @Param request body dto.ProcessTriggerRequest true "流程触发请求"
// @Success 200 {object} common.Response{dto.ProcessTriggerResponse}
// @Failure 400 {object} common.Response
// @Router /api/v1/process-trigger [post]
func (c *BPMNProcessTriggerController) TriggerProcess(ctx *gin.Context) {
	var req dto.ProcessTriggerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, 1001, "流程触发请求格式无效")
		return
	}
	workflowCtx, tenantID, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	req.TenantID = tenantID

	result, err := c.triggerService.TriggerProcess(workflowCtx, &req)
	if err != nil {
		respondBPMNError(ctx, err, "触发流程失败")
		return
	}

	common.Success(ctx, result)
}

// GetProcessStatus 获取流程状态
func (c *BPMNProcessTriggerController) GetProcessStatus(ctx *gin.Context) {
	instanceID, err := strconv.Atoi(ctx.Param("instance_id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的流程实例ID")
		return
	}

	tenantID, _ := ctx.Get("tenant_id")

	result, err := c.triggerService.GetProcessStatus(ctx.Request.Context(), instanceID, tenantID.(int))
	if err != nil {
		respondBPMNError(ctx, err, "获取流程状态失败")
		return
	}

	common.Success(ctx, result)
}

// CancelProcess 取消流程
func (c *BPMNProcessTriggerController) CancelProcess(ctx *gin.Context) {
	instanceID, err := strconv.Atoi(ctx.Param("instance_id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的流程实例ID")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	ctx.ShouldBindJSON(&req)

	workflowCtx, tenantID, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	err = c.triggerService.CancelProcess(workflowCtx, instanceID, req.Reason, tenantID)
	if err != nil {
		respondBPMNError(ctx, err, "取消流程失败")
		return
	}

	common.SuccessWithMessage(ctx, "流程已取消", nil)
}

// SuspendProcess 暂停流程
func (c *BPMNProcessTriggerController) SuspendProcess(ctx *gin.Context) {
	instanceID, err := strconv.Atoi(ctx.Param("instance_id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的流程实例ID")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	ctx.ShouldBindJSON(&req)

	workflowCtx, tenantID, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	err = c.triggerService.SuspendProcess(workflowCtx, instanceID, req.Reason, tenantID)
	if err != nil {
		respondBPMNError(ctx, err, "暂停流程失败")
		return
	}

	common.SuccessWithMessage(ctx, "流程已暂停", nil)
}

// ResumeProcess 恢复流程
func (c *BPMNProcessTriggerController) ResumeProcess(ctx *gin.Context) {
	instanceID, err := strconv.Atoi(ctx.Param("instance_id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的流程实例ID")
		return
	}

	workflowCtx, tenantID, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}

	err = c.triggerService.ResumeProcess(workflowCtx, instanceID, tenantID)
	if err != nil {
		respondBPMNError(ctx, err, "恢复流程失败")
		return
	}

	common.SuccessWithMessage(ctx, "流程已恢复", nil)
}

// CreateBinding 创建流程绑定
func (c *BPMNProcessTriggerController) CreateBinding(ctx *gin.Context) {
	var binding dto.ProcessBinding
	if err := ctx.ShouldBindJSON(&binding); err != nil {
		common.Fail(ctx, 1001, err.Error())
		return
	}

	tenantID, _ := ctx.Get("tenant_id")
	binding.TenantID = tenantID.(int)

	result, err := c.bindingService.CreateBinding(ctx.Request.Context(), &binding)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// GetBinding 获取流程绑定
func (c *BPMNProcessTriggerController) GetBinding(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的绑定ID")
		return
	}

	tenantID, _ := ctx.Get("tenant_id")

	result, err := c.bindingService.GetBinding(ctx.Request.Context(), id, tenantID.(int))
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// QueryBindings 查询流程绑定列表
func (c *BPMNProcessTriggerController) QueryBindings(ctx *gin.Context) {
	var req dto.ProcessBindingQueryRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		common.Fail(ctx, 1001, err.Error())
		return
	}

	tenantID, _ := ctx.Get("tenant_id")
	req.TenantID = tenantID.(int)

	result, err := c.bindingService.QueryBindings(ctx.Request.Context(), &req)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// UpdateBinding 更新流程绑定
func (c *BPMNProcessTriggerController) UpdateBinding(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的绑定ID")
		return
	}

	var binding dto.ProcessBinding
	if err := ctx.ShouldBindJSON(&binding); err != nil {
		common.Fail(ctx, 1001, err.Error())
		return
	}

	tenantID, _ := ctx.Get("tenant_id")
	binding.TenantID = tenantID.(int)

	result, err := c.bindingService.UpdateBinding(ctx.Request.Context(), id, &binding)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// DeleteBinding 删除流程绑定
func (c *BPMNProcessTriggerController) DeleteBinding(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的绑定ID")
		return
	}

	tenantID, _ := ctx.Get("tenant_id")

	err = c.bindingService.DeleteBinding(ctx.Request.Context(), id, tenantID.(int))
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "绑定已删除", nil)
}

// GetBindingsByBusinessType 根据业务类型获取绑定列表
func (c *BPMNProcessTriggerController) GetBindingsByBusinessType(ctx *gin.Context) {
	businessType := dto.BusinessType(ctx.Param("business_type"))

	tenantID, _ := ctx.Get("tenant_id")

	result, err := c.bindingService.GetBindingsByBusinessType(ctx.Request.Context(), businessType, tenantID.(int))
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// GetDepartmentProcesses 获取部门专属流程绑定
func (c *BPMNProcessTriggerController) GetDepartmentProcesses(ctx *gin.Context) {
	departmentID, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的部门ID")
		return
	}

	tenantID, _ := ctx.Get("tenant_id")

	result, err := c.bindingService.GetDepartmentBindings(ctx.Request.Context(), tenantID.(int), departmentID)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

// InitDepartmentProcesses 初始化部门默认流程模板
func (c *BPMNProcessTriggerController) InitDepartmentProcesses(ctx *gin.Context) {
	departmentID, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的部门ID")
		return
	}

	var req struct {
		DepartmentType string `json:"departmentType" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, 1001, err.Error())
		return
	}

	tenantID, _ := ctx.Get("tenant_id")

	if err := c.bindingService.InitDepartmentDefaultBindings(ctx.Request.Context(), tenantID.(int), departmentID, req.DepartmentType); err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "部门流程模板已初始化", nil)
}

// ListDomainConfigs 查询当前租户配置
func (c *BPMNProcessTriggerController) ListDomainConfigs(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	configType := ctx.Query("config_type")

	result, err := c.configService.ListConfigs(ctx.Request.Context(), tenantID.(int), configType)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, dto.ToDomainConfigResponseList(result))
}

// SetDomainConfig 创建或更新层级配置
func (c *BPMNProcessTriggerController) SetDomainConfig(ctx *gin.Context) {
	var req struct {
		ConfigType   string                 `json:"configType" binding:"required"`
		ConfigKey    string                 `json:"configKey" binding:"required"`
		ConfigValue  map[string]interface{} `json:"configValue" binding:"required"`
		InheritMode  string                 `json:"inheritMode"`
		DepartmentID int                    `json:"departmentId"`
		TeamID       int                    `json:"teamId"`
		Description  string                 `json:"description"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, 1001, err.Error())
		return
	}
	if req.InheritMode == "" {
		req.InheritMode = "inherit"
	}

	tenantID, _ := ctx.Get("tenant_id")
	err := c.configService.SetConfig(
		ctx.Request.Context(),
		tenantID.(int),
		req.DepartmentID,
		req.TeamID,
		req.ConfigType,
		req.ConfigKey,
		req.ConfigValue,
		req.InheritMode,
		req.Description,
	)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.SuccessWithMessage(ctx, "配置已保存", nil)
}

// GetEffectiveDomainConfig 获取继承解析后的有效配置
func (c *BPMNProcessTriggerController) GetEffectiveDomainConfig(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	departmentID, err := parseOptionalIntQuery(ctx, "department_id")
	if err != nil {
		common.Fail(ctx, 1001, "无效的部门ID")
		return
	}
	teamID, err := parseOptionalIntQuery(ctx, "team_id")
	if err != nil {
		common.Fail(ctx, 1001, "无效的团队ID")
		return
	}
	configType := ctx.Query("config_type")
	configKey := ctx.Query("config_key")
	if configType == "" || configKey == "" {
		common.Fail(ctx, 1001, "config_type 和 config_key 不能为空")
		return
	}

	result, err := c.configService.GetEffectiveConfig(ctx.Request.Context(), tenantID.(int), departmentID, teamID, configType, configKey)
	if err != nil {
		common.Fail(ctx, 5001, err.Error())
		return
	}

	common.Success(ctx, result)
}

func parseOptionalIntQuery(ctx *gin.Context, key string) (int, error) {
	value := ctx.Query(key)
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}
