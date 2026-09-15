package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

func respondWorkItemDeletionError(c *gin.Context, err error) {
	var intake *creation.IntakeError
	if errors.As(err, &intake) {
		intakehttp.Fail(c, err)
		return
	}
	var callback *workitemmutation.UnresolvedChangeCallbackError
	if errors.As(err, &callback) {
		common.Conflict(c, "prior callback is unresolved", nil)
		return
	}
	var conflict *workitemmutation.OperationConflictError
	var state interface{ SQLState() string }
	if common.IsVersionConflictError(err) || errors.As(err, &conflict) || (errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")) {
		common.Conflict(c, "WorkItem mutation conflicts with current state", nil)
		return
	}
	if ent.IsNotFound(err) {
		common.NotFound(c, "WorkItem not found")
		return
	}
	if app, ok := common.AsAppError(err); ok {
		switch app.Code {
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.ParamError(c, app.Message)
		case common.ErrCodeForbidden:
			common.Forbidden(c, app.Message)
		case common.ErrCodeNotFound:
			common.NotFound(c, app.Message)
		case common.ErrCodeConflict:
			common.Conflict(c, app.Message, nil)
		default:
			common.InternalError(c, "WorkItem mutation failed")
		}
		return
	}
	common.InternalError(c, "WorkItem mutation failed")
}
