package controller

import (
	"errors"
	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	"strconv"
)

func (c *IncidentController) StartIncident(ctx *gin.Context) { c.applyIncidentCommand(ctx, "start") }

func (c *IncidentController) applyIncidentCommand(ctx *gin.Context, action string) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil || id <= 0 {
		common.Fail(ctx, common.ParamErrorCode, "invalid incident ID")
		return
	}
	var req dto.IncidentCommandRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "version and operationId are required")
		return
	}
	meta := workitemmutation.Meta{TenantID: ctx.GetInt("tenant_id"), ActorID: ctx.GetInt("user_id"), ExpectedVersion: req.Version, OperationID: req.OperationID, CorrelationID: ctx.GetString("request_id"), Source: "http"}
	if meta.TenantID <= 0 || meta.ActorID <= 0 {
		common.Forbidden(ctx, "authenticated actor and tenant required")
		return
	}
	result, err := c.incidentService.ApplyIncidentCommand(ctx.Request.Context(), dto.IncidentCommand{Meta: meta, IncidentID: id, Action: action, AssigneeID: req.AssigneeID, Reason: req.Reason, Resolution: req.Resolution})
	if err != nil {
		var operationConflict *workitemmutation.OperationConflictError
		if common.IsVersionConflictError(err) || errors.As(err, &operationConflict) {
			common.Conflict(ctx, err.Error(), nil)
			return
		}
		respondIncidentMutationError(ctx, err)
		return
	}
	common.Success(ctx, result)
}

func respondIncidentMutationError(ctx *gin.Context, err error) {
	var operationConflict *workitemmutation.OperationConflictError
	if common.IsVersionConflictError(err) || errors.As(err, &operationConflict) {
		common.Conflict(ctx, err.Error(), nil)
		return
	}
	if appErr, ok := common.AsAppError(err); ok {
		switch appErr.Code {
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.Fail(ctx, common.ParamErrorCode, appErr.Message)
		case common.ErrCodeForbidden:
			common.Forbidden(ctx, appErr.Message)
		case common.ErrCodeNotFound:
			common.NotFound(ctx, appErr.Message)
		case common.ErrCodeConflict:
			common.Conflict(ctx, appErr.Message, nil)
		default:
			common.Fail(ctx, common.InternalErrorCode, "incident mutation failed")
		}
		return
	}
	common.Fail(ctx, common.InternalErrorCode, "incident mutation failed")
}
