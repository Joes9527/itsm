package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
	"itsm-backend/service"
)

// WorkItemRelationController exposes the sole relation owner for existing WorkItems of every registered class.
type WorkItemRelationController struct {
	owner *service.WorkItemRelationService
}

func NewWorkItemRelationController(owner *service.WorkItemRelationService) *WorkItemRelationController {
	return &WorkItemRelationController{owner: owner}
}

func relationRequestIdentity(c *gin.Context) (int, workitemmutation.Meta, bool) {
	tenant, err := middleware.ResolveRequestTenantID(c)
	if middleware.AbortIfTenantError(c, err) {
		return 0, workitemmutation.Meta{}, false
	}
	actor := c.GetInt("user_id")
	if actor <= 0 {
		common.AuthFailed(c, "authenticated actor required")
		return 0, workitemmutation.Meta{}, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ParamError(c, "invalid WorkItem ID")
		return 0, workitemmutation.Meta{}, false
	}
	return id, workitemmutation.Meta{TenantID: tenant, ActorID: actor, Source: "http"}, true
}

// List shared WorkItem relations.
// @Summary List WorkItem relations
// @Description Current persisted actor, registry permission and both endpoint row scopes are rechecked. The path is a WorkItem ID; mutation body sourceWorkItemId must match. Writes return immutable receipts; refresh separately.
// @Tags work-items
// @Accept json
// @Produce json
// @Param id path int true "Source WorkItem ID (read may traverse either endpoint)"
// @Success 200 {object} common.Response{data=[]relationmetadata.View}
// @Router /api/v1/work-items/{id}/relations [get]
func (h *WorkItemRelationController) List(c *gin.Context) {
	id, meta, ok := relationRequestIdentity(c)
	if !ok {
		return
	}
	views, err := h.owner.List(c.Request.Context(), meta, id)
	if err != nil {
		relationHTTPError(c, err)
		return
	}
	common.Success(c, views)
}

// Add shared WorkItem relations.
// @Summary Add WorkItem relations
// @Description Current persisted actor, registry permission and both endpoint row scopes are rechecked. The path is a WorkItem ID; mutation body sourceWorkItemId must match. Writes return immutable receipts; refresh separately.
// @Tags work-items
// @Accept json
// @Produce json
// @Param id path int true "Source WorkItem ID (read may traverse either endpoint)"
// @Param body body dto.WorkItemRelationRequest true "Observed source version, stable operationId, exact directed endpoints and typed metadata"
// @Success 200 {object} common.Response{data=workitemmutation.Result}
// @Router /api/v1/work-items/{id}/relations [post]
func (h *WorkItemRelationController) Add(c *gin.Context) { h.apply(c, false) }

// Remove shared WorkItem relations.
// @Summary Remove WorkItem relations
// @Description Current persisted actor, registry permission and both endpoint row scopes are rechecked. The path is a WorkItem ID; mutation body sourceWorkItemId must match. Writes return immutable receipts; refresh separately.
// @Tags work-items
// @Accept json
// @Produce json
// @Param id path int true "Source WorkItem ID (read may traverse either endpoint)"
// @Param body body dto.WorkItemRelationRequest true "Observed source version, stable operationId, exact directed endpoints and typed metadata"
// @Success 200 {object} common.Response{data=workitemmutation.Result}
// @Router /api/v1/work-items/{id}/relations [delete]
func (h *WorkItemRelationController) Remove(c *gin.Context) { h.apply(c, true) }

func (h *WorkItemRelationController) apply(c *gin.Context, remove bool) {
	id, meta, ok := relationRequestIdentity(c)
	if !ok {
		return
	}
	req, ok := intakehttp.BindRelation(c)
	if !ok {
		return
	}
	if req.SourceWorkItemID != id {
		common.ParamError(c, "path WorkItem ID must equal sourceWorkItemId")
		return
	}
	meta.ExpectedVersion = req.ExpectedVersion
	meta.OperationID = req.OperationID
	result, err := h.owner.Apply(c.Request.Context(), service.RelationCommand{Meta: meta, SourceID: id, TargetID: req.TargetWorkItemID, Type: req.RelationType, Required: req.Metadata.Required}, remove)
	if err != nil {
		relationHTTPError(c, err)
		return
	}
	common.Success(c, result)
}

func relationHTTPError(c *gin.Context, err error) {
	var typed *creation.IntakeError
	if errors.As(err, &typed) {
		intakehttp.Fail(c, err)
		return
	}
	var conflict *workitemmutation.OperationConflictError
	var state interface{ SQLState() string }
	if common.IsVersionConflictError(err) || errors.As(err, &conflict) || (errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")) {
		common.Conflict(c, "relation conflicts with current state", nil)
		return
	}
	if ent.IsNotFound(err) {
		common.NotFound(c, "WorkItem not found")
		return
	}
	if app, ok := common.AsAppError(err); ok {
		switch app.Code {
		case common.ErrCodeUnauthorized:
			common.AuthFailed(c, app.Message)
		case common.ErrCodeForbidden:
			common.Forbidden(c, app.Message)
		case common.ErrCodeNotFound:
			common.NotFound(c, app.Message)
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.ParamError(c, app.Message)
		case common.ErrCodeConflict:
			common.Conflict(c, app.Message, nil)
		default:
			common.InternalError(c, "relation operation failed")
		}
		return
	}
	common.InternalError(c, "relation operation failed")
}

// Context returns current source identity/version and source-only relation eligibility.
// @Summary Get WorkItem relation context
// @Description Read-only current-actor source context; target authorization and relation validity are checked on mutation.
// @Tags work-items
// @Produce json
// @Param id path int true "Source WorkItem ID"
// @Success 200 {object} common.Response{data=service.RelationContext}
// @Router /api/v1/work-items/{id}/relation-context [get]
func (h *WorkItemRelationController) Context(c *gin.Context) {
	id, meta, ok := relationRequestIdentity(c)
	if !ok {
		return
	}
	result, err := h.owner.Context(c.Request.Context(), meta, id)
	if err != nil {
		relationHTTPError(c, err)
		return
	}
	common.Success(c, result)
}
