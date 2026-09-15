package delegated_execution

import (
	"itsm-backend/common"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type listQuery struct {
	EventID string `form:"eventId"`
	TaskID  string `form:"taskId"`
	Status  string `form:"status"`
	Page    int    `form:"page"`
	Size    int    `form:"size"`
}

// List provides a tenant-scoped, redacted operational view of KAF delegation.
func (h *Handler) List(c *gin.Context) {
	var query listQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		common.Fail(c, common.ParamErrorCode, "invalid delegated execution query")
		return
	}
	result, err := h.service.List(c.Request.Context(), c.GetInt("tenant_id"), ListFilter(query))
	if appErr, ok := common.AsAppError(err); ok {
		common.Fail(c, common.ParamErrorCode, appErr.Message)
		return
	}
	if err != nil {
		common.InternalError(c, "list delegated executions failed")
		return
	}
	common.Success(c, result)
}

type reconcileBody struct {
	Conclusion string `json:"conclusion" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

// Reconcile records the authenticated operator's conclusion before a repair
// operation is considered. Authorization is performed by router middleware;
// tenant filtering is repeated in Service before any state transition.
func (h *Handler) Reconcile(c *gin.Context) {
	var body reconcileBody
	if err := c.ShouldBindJSON(&body); err != nil {
		common.Fail(c, common.ParamErrorCode, "conclusion and reason are required")
		return
	}
	err := h.service.Reconcile(c.Request.Context(), c.GetInt("tenant_id"), c.Param("eventId"), ReconcileRequest{Conclusion: body.Conclusion, Reason: body.Reason, ActorID: c.GetInt("user_id")})
	if appErr, ok := common.AsAppError(err); ok {
		switch appErr.Code {
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.Fail(c, common.ParamErrorCode, appErr.Message)
		case common.ErrCodeNotFound:
			common.Fail(c, common.NotFoundErrorCode, appErr.Message)
		case common.ErrCodeConflict:
			common.Fail(c, common.ConflictCode, appErr.Message)
		default:
			common.InternalError(c, appErr.Message)
		}
		return
	}
	if err != nil {
		common.InternalError(c, "delegated execution reconciliation failed")
		return
	}
	common.Success(c, gin.H{"eventId": c.Param("eventId"), "conclusion": body.Conclusion})
}

func (h *Handler) Requeue(c *gin.Context) {
	err := h.service.Requeue(c.Request.Context(), c.GetInt("tenant_id"), c.Param("eventId"), RequeueRequest{ActorID: c.GetInt("user_id")})
	if appErr, ok := common.AsAppError(err); ok {
		if appErr.Code == common.ErrCodeConflict {
			common.Fail(c, common.ConflictCode, appErr.Message)
			return
		}
		if appErr.Code == common.ErrCodeNotFound {
			common.Fail(c, common.NotFoundErrorCode, appErr.Message)
			return
		}
		common.Fail(c, common.ParamErrorCode, appErr.Message)
		return
	}
	if err != nil {
		common.InternalError(c, "delegated execution requeue failed")
		return
	}
	common.Success(c, gin.H{"eventId": c.Param("eventId"), "status": "pending"})
}
