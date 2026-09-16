package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/service"
)

func (c *BPMNProcessTriggerController) DeactivateBinding(ctx *gin.Context) {
	if ctx.GetInt("tenant_id") <= 0 || ctx.GetInt("user_id") <= 0 {
		common.Fail(ctx, common.AuthFailedCode, "缺少当前租户身份")
		return
	}
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil || id <= 0 {
		common.Fail(ctx, common.ParamErrorCode, "无效的绑定ID")
		return
	}
	var request dto.DeactivateProcessBindingRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "流程绑定停用请求格式无效")
		return
	}
	result, err := c.bindingService.DeactivateBinding(ctx.Request.Context(), service.ActionActor{TenantID: ctx.GetInt("tenant_id"), UserID: ctx.GetInt("user_id"), Role: ctx.GetString("role")}, id, request)
	if err != nil {
		respondBPMNError(ctx, err, "停用流程绑定失败")
		return
	}
	common.Success(ctx, result)
}
