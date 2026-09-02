package intake

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"itsm-backend/common"
	itsmservice "itsm-backend/service"

	"github.com/gin-gonic/gin"
)

type workflowRetryRepository interface {
	RetryDeadWorkflowStart(context.Context, int, int) error
}

type WorkflowInterventionHandler struct {
	repository workflowRetryRepository
	actors     *ActorResolver
}

func NewWorkflowInterventionHandler(repository workflowRetryRepository) *WorkflowInterventionHandler {
	return &WorkflowInterventionHandler{repository: repository, actors: NewActorResolver()}
}

func (h *WorkflowInterventionHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/intake/work-items/:id/workflow-start/retry", h.RetryWorkflowStart)
}

func (h *WorkflowInterventionHandler) RetryWorkflowStart(c *gin.Context) {
	identity, err := h.actors.Resolve(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	workItemID, err := strconv.Atoi(c.Param("id"))
	if err != nil || workItemID <= 0 {
		WriteError(c, NewInvalidCommand("invalid work item ID", FieldError{Field: "id", Message: "must be a positive integer"}, err))
		return
	}
	if h.repository == nil {
		WriteError(c, NewInternalFailure("workflow intervention repository is unavailable", nil))
		return
	}
	ctx := context.WithValue(c.Request.Context(), "user_id", identity.ActorID)
	ctx = context.WithValue(ctx, "request_id", c.GetString("request_id"))
	if err := h.repository.RetryDeadWorkflowStart(ctx, identity.TenantID, workItemID); err != nil {
		if errors.Is(err, itsmservice.ErrWorkflowStartNotDead) {
			WriteError(c, NewReferenceNotFound("workflow start intervention was not found", nil))
			return
		}
		WriteError(c, NewInfrastructureUnavailable("workflow start retry could not be requested", err))
		return
	}
	common.SuccessWithStatus(c, http.StatusAccepted, gin.H{
		"workItemId": workItemID, "workflowStartStatus": "pending",
	})
}
