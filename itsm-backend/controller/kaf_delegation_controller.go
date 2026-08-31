package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/gin-gonic/gin"
)

// KafDelegationController exposes only task-scoped automation operations. It
// derives tenant and actor solely from the authenticated Gin context.
type KafDelegationController struct {
	service       *service.KafDelegationService
	processEngine service.ProcessEngine
}

func NewKafDelegationController(client *ent.Client, processEngine service.ProcessEngine) *KafDelegationController {
	return &KafDelegationController{
		service:       service.NewKafDelegationService(client),
		processEngine: processEngine,
	}
}

func (c *KafDelegationController) RegisterRoutes(r *gin.RouterGroup) {
	tasks := r.Group("/bpmn/process-tasks")
	tasks.GET("/:taskId/kaf-context", c.GetContext)
	tasks.GET("/kaf-delegated", c.ListDelegated)
	tasks.POST("/:taskId/actions", c.ExecuteAction)
}

func (c *KafDelegationController) GetContext(ctx *gin.Context) {
	workflowCtx, ok := kafAuthenticatedWorkflowContext(ctx)
	if !ok {
		return
	}
	result, err := c.service.GetTaskContext(workflowCtx, ctx.Param("taskId"))
	if err != nil {
		writeKafDelegationError(ctx, err)
		return
	}
	common.Success(ctx, result)
}

func (c *KafDelegationController) ListDelegated(ctx *gin.Context) {
	workflowCtx, ok := kafAuthenticatedWorkflowContext(ctx)
	if !ok {
		return
	}
	if status := strings.TrimSpace(ctx.Query("status")); status != "" && status != common.ProcessTaskStatusDelegated {
		writeKafValidationError(ctx, "only status=delegated is supported")
		return
	}
	limit := 100
	if rawLimit := ctx.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > 100 {
			writeKafValidationError(ctx, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	page, err := c.service.ListDelegatedTaskPage(workflowCtx, limit, ctx.Query("cursor"))
	if err != nil {
		writeKafDelegationError(ctx, err)
		return
	}
	common.Success(ctx, gin.H{"items": page.Items, "limit": page.Limit, "nextCursor": page.NextCursor})
}

func (c *KafDelegationController) ExecuteAction(ctx *gin.Context) {
	workflowCtx, ok := kafAuthenticatedWorkflowContext(ctx)
	if !ok {
		return
	}
	var req service.KafActionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeKafValidationError(ctx, "invalid KAF action request")
		return
	}
	result, err := c.service.ExecuteAction(workflowCtx, ctx.Param("taskId"), req, c.processEngine)
	if err != nil {
		writeKafDelegationError(ctx, err)
		return
	}
	common.Success(ctx, result)
}

func kafAuthenticatedWorkflowContext(ctx *gin.Context) (context.Context, bool) {
	tenantID := ctx.GetInt("tenant_id")
	userID := ctx.GetInt("user_id")
	if tenantID <= 0 || userID <= 0 {
		common.AuthFailed(ctx, "KAF delegation requires an authenticated tenant automation actor")
		return nil, false
	}
	workflowCtx := context.WithValue(ctx.Request.Context(), bpmn.BPMNTenantIDContextKey, tenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, userID)
	return workflowCtx, true
}

func writeKafDelegationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKafDelegationForbidden):
		common.Forbidden(ctx, "KAF delegation access denied")
	case errors.Is(err, service.ErrKafDelegationNotFound):
		common.NotFound(ctx, "KAF delegated task not found")
	case errors.Is(err, service.ErrKafActionInvalid):
		writeKafValidationError(ctx, "invalid KAF action")
	case errors.Is(err, service.ErrKafDelegationInvalidCursor):
		writeKafValidationError(ctx, "invalid KAF delegated list cursor")
	case errors.Is(err, service.ErrKafActionInProgress):
		common.Conflict(ctx, "KAF action is in progress", gin.H{"code": "in_progress"})
	case errors.Is(err, service.ErrKafActionConflict):
		common.Conflict(ctx, "KAF action version conflict", nil)
	default:
		common.InternalError(ctx, "KAF delegation request failed")
	}
}

func writeKafValidationError(ctx *gin.Context, message string) {
	ctx.JSON(http.StatusUnprocessableEntity, common.Response{Code: common.ValidationError, Message: message})
	ctx.Abort()
}
