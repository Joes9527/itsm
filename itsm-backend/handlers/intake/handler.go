package intake

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"itsm-backend/common"

	"github.com/gin-gonic/gin"
)

const maxCreateWorkItemBodyBytes int64 = 256 * 1024

type createService interface {
	Create(context.Context, Identity, CreateWorkItemCommand) (*CreateWorkItemResult, error)
}

type ActorResolver struct{}

func NewActorResolver() *ActorResolver { return &ActorResolver{} }

func (r *ActorResolver) Resolve(c *gin.Context) (Identity, error) {
	if c == nil {
		return Identity{}, NewAuthenticationRequired("authenticated intake identity is required", nil)
	}
	tenantID := c.GetInt("tenant_id")
	actorID := c.GetInt("user_id")
	role := strings.TrimSpace(c.GetString("role"))
	if tenantID <= 0 || actorID <= 0 || role == "" {
		return Identity{}, NewAuthenticationRequired("authenticated intake identity is required", nil)
	}
	channel := "itsm_web"
	if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
		channel = "itsm_api"
	}
	return Identity{
		TenantID: tenantID, ActorID: actorID, RequesterID: actorID, Role: role,
		Channel: channel, TokenID: c.GetString("token_id"),
	}, nil
}

type Handler struct {
	service createService
	actors  *ActorResolver
}

func NewHandler(service createService) *Handler {
	return &Handler{service: service, actors: NewActorResolver()}
}

func (h *Handler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/intake/work-items", h.CreateWorkItem)
}

func (h *Handler) CreateWorkItem(c *gin.Context) {
	identity, err := h.actors.Resolve(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if h.service == nil {
		writeError(c, NewInternalFailure("intake service is unavailable", nil))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateWorkItemBodyBytes)
	command, err := DecodeCreateWorkItemCommand(c.Request.Body)
	if err != nil {
		writeError(c, err)
		return
	}
	ctx := WithRequestID(c.Request.Context(), c.GetString("request_id"))
	result, err := h.service.Create(ctx, identity, command)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	common.SuccessWithStatus(c, status, result)
}

func writeError(c *gin.Context, err error) {
	var typed *IntakeError
	if !errors.As(err, &typed) {
		typed = NewInternalFailure("intake request failed", nil)
	}
	message := typed.Message
	switch typed.Code {
	case InfrastructureUnavailable:
		message = "intake service is temporarily unavailable"
	case InternalFailure:
		message = "intake request failed"
	}
	var fields interface{}
	if len(typed.FieldErrors) > 0 {
		fields = typed.FieldErrors
	}
	common.TypedFail(c, typed.HTTPStatus, string(typed.Code), message, typed.Retryable, fields)
}
