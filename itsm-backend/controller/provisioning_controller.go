package controller

import (
	"strconv"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
)

// ProvisioningController M2：交付任务接口（骨架）
type ProvisioningController struct {
	provisioningService *service.ProvisioningService
}

func NewProvisioningController(provisioningService *service.ProvisioningService) *ProvisioningController {
	return &ProvisioningController{provisioningService: provisioningService}
}

// StartProvisioning 启动交付（创建任务并把 SR 置为 provisioning）
// @Summary 启动交付
// @Tags 交付
// @Produce json
// @Param id path int true "服务请求ID"
// @Success 200 {object} common.Response{data=dto.StartProvisioningResponse}
// @Failure 400 {object} common.Response
// @Failure 500 {object} common.Response
// @Security BearerAuth
// @Router /api/v1/service-requests/{id}/provision [post]
func (pc *ProvisioningController) StartProvisioning(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的服务请求ID")
		return
	}
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.UnauthorizedCode, "租户信息缺失")
		return
	}
	userID := c.GetInt("user_id")
	if userID == 0 {
		common.Fail(c, common.UnauthorizedCode, "用户未认证")
		return
	}
	role := c.GetString("role")

	task, err := pc.provisioningService.CreateTaskFromServiceRequest(c.Request.Context(), id, tenantID, userID, role)
	if err != nil {
		if isProvisionDenied(err) {
			common.Fail(c, common.ForbiddenCode, err.Error())
			return
		}
		common.Fail(c, common.BadRequestCode, err.Error())
		return
	}

	resp := &dto.ProvisioningTaskResponse{
		ID:               task.ID,
		ServiceRequestID: task.ServiceRequestID,
		Provider:         task.Provider,
		ResourceType:     task.ResourceType,
		Status:           task.Status,
		Payload:          task.Payload,
		Result:           task.Result,
		ErrorMessage:     task.ErrorMessage,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
	}
	common.Success(c, dto.StartProvisioningResponse{Task: resp})
}

// ListProvisioningTasks 列出某个 SR 的交付任务
// @Summary 获取交付任务列表
// @Tags 交付
// @Produce json
// @Param id path int true "服务请求ID"
// @Success 200 {object} common.Response{data=[]dto.ProvisioningTaskResponse}
// @Failure 400 {object} common.Response
// @Failure 500 {object} common.Response
// @Security BearerAuth
// @Router /api/v1/service-requests/{id}/provisioning-tasks [get]
func (pc *ProvisioningController) ListProvisioningTasks(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的服务请求ID")
		return
	}
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.UnauthorizedCode, "租户信息缺失")
		return
	}

	tasks, err := pc.provisioningService.ListTasksByServiceRequest(c.Request.Context(), id, tenantID)
	if err != nil {
		common.Fail(c, common.InternalErrorCode, "获取交付任务列表失败")
		return
	}
	out := make([]dto.ProvisioningTaskResponse, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, dto.ProvisioningTaskResponse{
			ID:               t.ID,
			ServiceRequestID: t.ServiceRequestID,
			Provider:         t.Provider,
			ResourceType:     t.ResourceType,
			Status:           t.Status,
			Payload:          t.Payload,
			Result:           t.Result,
			ErrorMessage:     t.ErrorMessage,
			CreatedAt:        t.CreatedAt,
			UpdatedAt:        t.UpdatedAt,
		})
	}
	common.Success(c, out)
}

// ExecuteProvisioningTask 执行交付任务（Stub）
// @Summary 执行交付任务
// @Tags 交付
// @Produce json
// @Param id path int true "交付任务ID"
// @Success 200 {object} common.Response{data=dto.ProvisioningTaskResponse}
// @Failure 400 {object} common.Response
// @Failure 500 {object} common.Response
// @Security BearerAuth
// @Router /api/v1/provisioning-tasks/{id}/execute [post]
func (pc *ProvisioningController) ExecuteProvisioningTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的交付任务ID")
		return
	}
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.UnauthorizedCode, "租户信息缺失")
		return
	}
	userID := c.GetInt("user_id")
	if userID == 0 {
		common.Fail(c, common.UnauthorizedCode, "用户未认证")
		return
	}
	role := c.GetString("role")

	task, err := pc.provisioningService.ExecuteTask(c.Request.Context(), id, tenantID, userID, role)
	if err != nil {
		if isProvisionDenied(err) {
			common.Fail(c, common.ForbiddenCode, err.Error())
			return
		}
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}
	common.Success(c, dto.ProvisioningTaskResponse{
		ID:               task.ID,
		ServiceRequestID: task.ServiceRequestID,
		Provider:         task.Provider,
		ResourceType:     task.ResourceType,
		Status:           task.Status,
		Payload:          task.Payload,
		Result:           task.Result,
		ErrorMessage:     task.ErrorMessage,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
	})
}

// isProvisionDenied 判断错误是否来自 service.CanProvision 的拒绝原因，
// 用于把职责分离/权限不足映射成 403 而不是通用 400/500。
func isProvisionDenied(err error) bool {
	msg := err.Error()
	return msg == "申请人不能交付自己提交的服务请求" || msg == "无交付权限"
}
