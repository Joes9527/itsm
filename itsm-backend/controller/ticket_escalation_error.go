package controller

import (
	"errors"
	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

func respondTicketEscalationError(ctx *gin.Context, err error) {
	var conflict *workitemmutation.OperationConflictError
	switch {
	case errors.Is(err, creation.ErrPermissionDenied), errors.Is(err, creation.ErrAuthenticationRequired):
		common.Forbidden(ctx, "current escalation permission required")
	case errors.Is(err, executionscope.ErrDenied):
		common.Forbidden(ctx, "ticket execution scope denied")
	case common.IsVersionConflictError(err), errors.As(err, &conflict):
		common.Conflict(ctx, err.Error(), nil)
	case ent.IsNotFound(err):
		common.NotFound(ctx, "ticket unavailable")
	default:
		if app, ok := common.AsAppError(err); ok {
			switch app.Code {
			case common.ErrCodeForbidden:
				common.Forbidden(ctx, app.Message)
				return
			case common.ErrCodeValidation, common.ErrCodeBadRequest:
				common.Fail(ctx, common.ParamErrorCode, app.Message)
				return
			case common.ErrCodeNotFound:
				common.NotFound(ctx, app.Message)
				return
			}
		}
		common.Fail(ctx, common.InternalErrorCode, "ticket escalation could not be committed")
	}
}
